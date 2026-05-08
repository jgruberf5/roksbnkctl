#!/usr/bin/env python3
"""
BIG-IP Next for Kubernetes 2.3 — License Schematics Lifecycle Runner

Manages a single IBM Schematics workspace through the full Terraform lifecycle:
create → plan → apply → destroy → delete.

Each phase is independently selectable via --phases so partial runs
(e.g. --phases plan apply) can be resumed against an existing workspace
with --ws-id.

Phases (preflight and setup always run):
  create   — create the Schematics workspace
  plan     — plan (validate) the workspace
  apply    — apply (provision) the workspace
  destroy  — destroy (deprovision) the workspace
  delete   — delete the workspace record from Schematics

Usage:
    python3 schematics_runner.py [path/to/terraform.tfvars] [options]

    --branch BRANCH     GitHub branch to deploy (default: main)
    --phases PHASE ...  Phases to run (default: all)
    --ws-id WS_ID       Existing workspace ID (required when create is not in --phases)
    --list              List workspaces matching this repo's name prefix and exit
    --resources         Print workspace resource list and exit
    --outputs           Print workspace output variables and exit

Prerequisites:
    ibmcloud CLI installed and authenticated:
        ibmcloud login --apikey YOUR_API_KEY -r REGION
    Schematics plugin:
        ibmcloud plugin install schematics
"""

import json
import re
import signal
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

# ── Configuration ─────────────────────────────────────────────────────────────

REPO_URL       = "https://github.com/f5devcentral/ibmcloud_schematics_bigip_next_for_kubernetes_2_3_license"
WS_NAME_PREFIX = "bnk-23-license"
TITLE          = "BIG-IP Next for Kubernetes 2.3 — License"
TFVARS_DEFAULT = "terraform.tfvars"
WS_JSON_PATH   = "workspace.json"      # written by setup, consumed by create
REPORT_DIR     = Path("test-reports")

POLL_INTERVAL = 30      # seconds between status checks during job polling
JOB_TIMEOUT   = 18000   # 300 min — maximum wait for any single Schematics job
READY_TIMEOUT = 300     # maximum wait for workspace to leave DRAFT after creation

# Variables whose values must not appear in plain text in reports or logs.
# The Schematics API marks them "secure" so they are stored encrypted.
SECURE_VARS = {"ibmcloud_api_key", "bigip_password"}

# Schematics workspace statuses that indicate no job is currently running.
# Polling stops as soon as any of these is observed.
TERMINAL_STATUSES = {"INACTIVE", "ACTIVE", "FAILED", "STOPPED", "DRAFT"}

VALID_PHASES = ["create", "plan", "apply", "destroy", "delete"]

# Output variables promoted to the top of the Key Outputs section in the report
KEY_OUTPUTS = [
    "license_id",
    "license_namespace",
]

# Width used for all report and console section dividers
_W = 72


# ── Low-level helpers ─────────────────────────────────────────────────────────

def tee(msg, lf=None):
    """Print msg to stdout and, when lf is given, to the log file simultaneously.

    Using tee throughout ensures that every console message is also captured in
    the structured log file without a separate logging framework.
    """
    print(msg, flush=True)
    if lf:
        print(msg, file=lf, flush=True)


def run_cmd(cmd, lf=None, stream=False):
    """Run a shell command and return (returncode, stdout, stderr).

    stream=False (default): capture output and return it — use this when the
    output must be parsed (e.g. JSON) before the next step.

    stream=True: pipe stdout line-by-line to the console and lf in real time —
    use this for long-running jobs where visibility matters more than capture.
    In streaming mode stderr is merged into stdout so log ordering matches
    what the CLI prints, and stderr is returned as an empty string.
    """
    if not stream:
        r = subprocess.run(cmd, shell=True, capture_output=True, text=True)
        return r.returncode, r.stdout, r.stderr

    proc = subprocess.Popen(
        cmd, shell=True,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        text=True, bufsize=1,
    )
    buf = []
    for line in proc.stdout:
        print(line, end="", flush=True)
        if lf:
            print(line, end="", file=lf, flush=True)
        buf.append(line)
    proc.wait()
    return proc.returncode, "".join(buf), ""


def ibmcloud_json(cmd, lf=None):
    """Run an ibmcloud CLI command with --output json and return parsed JSON.

    All ibmcloud Schematics commands support --output json for machine-readable
    output. The raw JSON payload is always written to lf for traceability.
    Raises RuntimeError if the command fails or the output is not valid JSON.
    """
    rc, out, err = run_cmd(f"{cmd} --output json")
    if lf and out.strip():
        print(out, file=lf, flush=True)
    if rc != 0:
        raise RuntimeError(f"Command failed: {cmd}\n{(err or out).strip()}")
    try:
        return json.loads(out)
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"Non-JSON output from: {cmd}\n{out}") from exc


def section(title, lf):
    """Print a titled section divider to both the console and the log file."""
    bar = "─" * _W
    tee(f"\n{bar}\n  {title}\n{bar}", lf)


# ── tfvars / workspace.json ───────────────────────────────────────────────────

def parse_tfvars(path):
    """Parse a terraform.tfvars file into Schematics variablestore format.

    Schematics workspaces require variables as a JSON array of typed objects
    ({name, value, type, secure?}) rather than the raw HCL tfvars format.
    This function bridges that gap so the same tfvars file used locally with
    `terraform apply` can also drive a Schematics workspace.

    Type inference rules:
      - "true" / "false" literals → bool
      - integer or decimal numeric strings → number
      - everything else → string (HCL surrounding quotes are stripped)
    """
    variables = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            m = re.match(r'^(\w+)\s*=\s*(.+)$', line)
            if not m:
                continue
            name, raw = m.group(1), m.group(2).strip()
            if raw in ("true", "false"):
                entry = {"name": name, "value": raw, "type": "bool"}
            elif re.match(r'^-?\d+(\.\d+)?$', raw):
                entry = {"name": name, "value": raw, "type": "number"}
            else:
                entry = {"name": name, "value": raw.strip('"'), "type": "string"}
            if name in SECURE_VARS:
                entry["secure"] = True
            variables.append(entry)
    return variables


def build_workspace_json(variables, ts_label, branch="main"):
    """Write workspace.json and return the workspace definition dict.

    workspace.json is consumed by `ibmcloud schematics workspace new --file`.
    The workspace name includes a timestamp so concurrent test runs targeting
    the same prefix do not collide with each other.

    Location and resource group are read directly from the tfvars so the
    workspace is created in the same region as the resources it will manage.
    """
    var_map        = {v["name"]: v["value"] for v in variables}
    location       = var_map.get("ibmcloud_schematics_region", "us-south")
    resource_group = var_map.get("ibmcloud_resource_group", "default")
    ws = {
        "name": f"{WS_NAME_PREFIX}-test-{ts_label}",
        "type": ["terraform_v1.5"],
        "location": location,
        "description": f"Lifecycle runner — {datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M UTC')}",
        "resource_group": resource_group,
        "template_repo": {
            "url": REPO_URL,
            "branch": branch,
        },
        "template_data": [{
            "folder": ".",
            "type": "terraform_v1.5",
            "variablestore": variables,
        }],
    }
    Path(WS_JSON_PATH).write_text(json.dumps(ws, indent=2))
    return ws


# ── Schematics polling ────────────────────────────────────────────────────────

def get_ws_info(ws_id):
    """Return (status, locked) for a workspace without raising on API failure.

    Schematics can place the workspace status in different response fields
    depending on the workspace state, so we probe both locations. Returns
    ("UNKNOWN", True) on any error so callers treat the workspace as not-ready.
    """
    try:
        data   = ibmcloud_json(f"ibmcloud schematics workspace get --id {ws_id}")
        status = (
            data.get("status")
            or data.get("workspace_status_msg", {}).get("status_code")
            or "UNKNOWN"
        )
        locked = data.get("workspace_status", {}).get("locked", False)
        return status, locked
    except Exception:
        return "UNKNOWN", True


def get_ws_status(ws_id):
    """Convenience wrapper — returns only the workspace status string."""
    status, _ = get_ws_info(ws_id)
    return status


def wait_for_workspace_ready(ws_id, lf, timeout=READY_TIMEOUT):
    """Block until the workspace leaves DRAFT and its lock is released.

    A newly created workspace starts in DRAFT while Schematics clones the
    repository and configures the template. Submitting plan or apply before
    the workspace reaches INACTIVE will fail with a 409 conflict. Polls every
    10 seconds, which is fast enough to catch the typical 30-60 second
    initialization without hammering the API.
    """
    start = time.time()
    while True:
        elapsed = int(time.time() - start)
        if elapsed > timeout:
            tee(f"\n  WARNING: workspace not ready after {timeout}s — proceeding anyway", lf)
            return get_ws_status(ws_id)
        status, locked = get_ws_info(ws_id)
        if status in {"INACTIVE", "ACTIVE", "FAILED"} and not locked:
            print()  # end the overwriting progress line before returning
            return status
        msg = f"  [ready] {elapsed}s  status={status}  locked={locked}"
        # Overwrite the same console line so progress doesn't flood the terminal
        print(f"\r{msg:<76}", end="", flush=True)
        print(msg, file=lf, flush=True)
        time.sleep(10)


def poll_until_terminal(ws_id, label, lf, timeout=JOB_TIMEOUT):
    """Poll workspace status every POLL_INTERVAL seconds until a terminal state.

    All Schematics job submissions are asynchronous — the CLI returns immediately
    and the workspace status transitions through intermediate states (e.g.
    INPROGRESS) before settling in a terminal state. Returns (status, elapsed).
    The caller inspects the status to determine whether the job succeeded.
    """
    start = time.time()
    while True:
        elapsed = int(time.time() - start)
        if elapsed > timeout:
            return "TIMEOUT", elapsed
        status = get_ws_status(ws_id)
        if status in TERMINAL_STATUSES:
            print()  # end the overwriting progress line before returning
            return status, elapsed
        msg = f"  [{label}] {elapsed}s elapsed  status={status}"
        print(f"\r{msg:<76}", end="", flush=True)
        print(msg, file=lf, flush=True)
        time.sleep(POLL_INTERVAL)


def stream_logs(ws_id, act_id, lf):
    """Stream the Schematics activity log to the console and log file."""
    run_cmd(
        f"ibmcloud schematics logs --id {ws_id} --act-id {act_id}",
        lf=lf, stream=True,
    )


def run_job(cmd, ws_id, label, lf, success_statuses, timeout=JOB_TIMEOUT):
    """Submit a Schematics job, wait for completion, stream logs, and report outcome.

    Handles the full async job lifecycle:
      1. Submit the job (CLI returns an activity ID immediately).
      2. Wait for the workspace status to change from its pre-submit value,
         confirming Schematics has picked up the job.
      3. Poll until the workspace reaches a terminal status.
      4. Stream the final logs via the activity ID.

    409 / "temporarily locked" responses indicate a prior job has not fully
    released the workspace yet (common after destroy before the state is flushed).
    These are retried every 30 seconds for up to `timeout` seconds.

    Returns (passed, final_status, elapsed_seconds).
    """
    pre_status    = get_ws_status(ws_id)
    lock_deadline = time.time() + timeout
    attempt       = 0

    # Submit loop — retries only for 409 workspace-locked transient errors
    while True:
        attempt += 1
        rc, out, err = run_cmd(f"{cmd} --output json")
        combined = (out + err).lower()
        if rc == 0:
            break
        if ("409" in combined or "temporarily locked" in combined) and time.time() < lock_deadline:
            remaining = int(lock_deadline - time.time())
            tee(
                f"  Workspace locked (409) — retrying in 30s "
                f"(attempt {attempt}, {remaining}s remaining in budget)",
                lf,
            )
            time.sleep(30)
            continue
        # Non-retryable error: surface it to the caller immediately
        if out.strip():
            print(out, file=lf, flush=True)
        raise RuntimeError((err or out).strip())

    if out.strip():
        print(out, file=lf, flush=True)

    try:
        act_id = json.loads(out).get("activityid")
    except (json.JSONDecodeError, AttributeError):
        act_id = None

    tee(f"  Activity ID : {act_id or '(unavailable)'}", lf)

    t0 = time.time()
    if act_id:
        # Wait up to 120 seconds for the workspace status to transition away from
        # its pre-submit value. This confirms Schematics has started the job before
        # we begin polling for a terminal status.
        tee("  Waiting for activity to start...", lf)
        t_transition = time.time()
        while time.time() - t_transition < 120:
            if get_ws_status(ws_id) != pre_status:
                break
            time.sleep(5)

        tee("  Polling until activity completes...", lf)
        final_status, _ = poll_until_terminal(ws_id, label, lf, timeout=timeout)

        tee("  Fetching final logs...", lf)
        stream_logs(ws_id, act_id, lf)
        tee("", lf)
    else:
        # Some CLI versions do not return an activity ID — fall back to polling only
        tee("  No activity ID returned — polling workspace status...", lf)
        final_status, _ = poll_until_terminal(ws_id, label, lf, timeout=timeout)

    elapsed = int(time.time() - t0)
    passed  = final_status in success_statuses
    return passed, final_status, elapsed


def fetch_outputs(ws_id, lf=None):
    """Retrieve and flatten Terraform output variables from the workspace.

    The Schematics output API returns a nested structure per template:
        [{output_values: [{output_name: {value, type, sensitive}}, ...]}, ...]
    We flatten this into a simple {name: value} dict for report rendering.
    Returns an empty dict on any error so callers can proceed without outputs.
    """
    try:
        data  = ibmcloud_json(f"ibmcloud schematics output --id {ws_id}", lf)
        items = data if isinstance(data, list) else [data]
        out   = {}
        for template in items:
            for item in template.get("output_values", []):
                for name, meta in item.items():
                    out[name] = meta.get("value", "") if isinstance(meta, dict) else meta
        return out
    except Exception as exc:
        if lf:
            tee(f"  WARNING: could not fetch outputs: {exc}", lf)
        return {}


# ── Cleanup ───────────────────────────────────────────────────────────────────

def cleanup(ws_id, lf):
    """Destroy and delete the workspace to avoid leaving orphaned billable resources.

    Called by the SIGINT handler so a Ctrl-C mid-run does not leave managed
    resources running. Uses --force to bypass interactive confirmation prompts.
    """
    if not ws_id:
        return
    tee(f"\n  Cleanup: destroying workspace {ws_id} ...", lf)
    run_cmd(f"ibmcloud schematics destroy --id {ws_id} --force", lf=lf, stream=True)
    poll_until_terminal(ws_id, "cleanup-destroy", lf, timeout=JOB_TIMEOUT)
    tee(f"  Cleanup: deleting workspace {ws_id} ...", lf)
    run_cmd(f"ibmcloud schematics workspace delete --id {ws_id} --force", lf=lf)


# ── Report rendering ──────────────────────────────────────────────────────────

class Phase:
    """Holds the result of a single lifecycle phase for report generation."""
    __slots__ = ("name", "status", "duration", "error")

    def __init__(self, name):
        self.name     = name
        self.status   = "SKIP"  # default until the phase actually executes
        self.duration = 0
        self.error    = None


def render_report(started_at, ws_id, ws_name, phases, outputs, overall):
    """Render a human-readable lifecycle runner report as a multi-line string.

    KEY_OUTPUTS are listed first in the outputs section so the most important
    values are immediately visible. Any remaining outputs follow below a
    secondary divider.
    """
    elapsed = int((datetime.now(timezone.utc) - started_at).total_seconds())
    sep = "=" * _W
    thn = "-" * _W
    lines = [
        "",
        sep,
        f"  {TITLE} — Schematics Lifecycle Runner Report",
        sep,
        f"  Started     {started_at.strftime('%Y-%m-%d %H:%M:%S UTC')}",
        f"  Workspace   {ws_name or 'not created'}",
        f"  WS ID       {ws_id   or 'not created'}",
        f"  Result      {overall}",
        f"  Total time  {elapsed}s  ({elapsed / 60:.1f} min)",
        thn,
        f"  {'Phase':<20} {'Result':<8} {'Duration':>10}",
        thn,
    ]
    for p in phases:
        lines.append(f"  {p.name:<20} {p.status:<8} {p.duration:>8}s")
        if p.error:
            lines.append(f"    !! {p.error}")

    if outputs:
        lines += [thn, "  Key Outputs", thn]
        printed = set()
        for key in KEY_OUTPUTS:
            val = outputs.get(key)
            if val is not None:
                lines.append(f"  {key}")
                lines.append(f"    {val}")
                printed.add(key)
        extras = {k: v for k, v in outputs.items() if k not in printed}
        if extras:
            lines.append(thn)
            for k, v in extras.items():
                lines.append(f"  {k}")
                lines.append(f"    {v}")

    lines += [sep, ""]
    return "\n".join(lines)


# ── Workspace info helpers ────────────────────────────────────────────────────

def _list_matching_workspaces():
    """Fetch all workspaces and filter to those starting with WS_NAME_PREFIX.

    Returns (matches, error_string). On success, matches is sorted newest-first
    by name — the timestamp suffix in WS_NAME_PREFIX-test-YYYYMMDD_HHmmss makes
    lexicographic descending order equivalent to chronological descending order.
    """
    rc, out, err = run_cmd("ibmcloud schematics workspace list --output json")
    if rc != 0:
        return None, (err or out).strip()
    try:
        data    = json.loads(out)
        ws_list = data.get("workspaces", []) if isinstance(data, dict) else (data or [])
        matches = [
            w for w in ws_list
            if (w.get("name") or "").startswith(WS_NAME_PREFIX)
        ]
        matches.sort(key=lambda w: w.get("name", ""), reverse=True)
        return matches, None
    except json.JSONDecodeError as exc:
        return None, str(exc)


def _ws_status_str(w):
    """Extract a printable status string from a workspace list entry.

    The status field location varies between workspace list and workspace get
    responses, so we check both locations.
    """
    return (
        w.get("status")
        or w.get("workspace_status_msg", {}).get("status_code")
        or "UNKNOWN"
    )


def show_workspace_list(tfvars_path):
    """Print a formatted table of workspaces matching WS_NAME_PREFIX.

    Returns an exit code (0 = success, 1 = error).
    """
    sep = "=" * _W
    thn = "─" * (_W - 4)

    print(f"\n{sep}")
    print(f"  {TITLE}")
    print(f"  Workspace prefix : {WS_NAME_PREFIX}")
    if tfvars_path:
        print(f"  tfvars           : {tfvars_path}")
    print(sep)

    matches, err = _list_matching_workspaces()
    if err:
        print(f"\n  ERROR: {err}\n{sep}\n")
        return 1

    print(f"\n  {thn}")
    if not matches:
        print(f"  (no workspaces found with prefix '{WS_NAME_PREFIX}')")
    else:
        for w in matches:
            status = _ws_status_str(w)
            print(f"  {status:<12}  {w.get('name', ''):<50}  {w.get('id', '')}")
    print(f"\n{sep}\n")
    return 0


def show_resources(ws_id):
    """Print the Terraform state resource list for a workspace.

    Returns an exit code (0 = success, 1 = error).
    """
    sep = "=" * _W
    print(f"\n{sep}")
    print(f"  Resources  —  {ws_id}")
    print(sep)

    rc, out, err = run_cmd(f"ibmcloud schematics state list --id {ws_id}")
    if rc != 0:
        print(f"\n  ERROR: {(err or out).strip()}\n{sep}\n")
        return 1
    if out.strip():
        for line in out.strip().splitlines():
            print(f"  {line}")
    else:
        print("  (no resources)")
    print(f"\n{sep}\n")
    return 0


def show_outputs(ws_id):
    """Print workspace output variables in human-readable form.

    Returns an exit code (0 = success, 1 = error).
    """
    sep = "=" * _W
    print(f"\n{sep}")
    print(f"  Output Variables  —  {ws_id}")
    print(sep)

    outputs = fetch_outputs(ws_id)
    if not outputs:
        print("\n  (no outputs or workspace not yet applied)")
    else:
        print()
        for k, v in outputs.items():
            print(f"  {k}")
            print(f"    {v}")
    print(f"\n{sep}\n")
    return 0


def _resolve_ws_id(ws_id_arg):
    """Resolve a workspace ID from --ws-id or by auto-detecting the most recent match.

    When --ws-id is not provided, selects the most recently created workspace
    whose name matches WS_NAME_PREFIX. Returns (ws_id, error_string).
    """
    if ws_id_arg:
        return ws_id_arg, None
    matches, err = _list_matching_workspaces()
    if err:
        return None, err
    if not matches:
        return None, (
            f"No workspace with prefix '{WS_NAME_PREFIX}' found.\n"
            f"       Use --ws-id WS_ID or run --list to see available workspaces."
        )
    ws_id = matches[0].get("id")
    print(f"  Auto-detected workspace: {matches[0].get('name')}  ({ws_id})")
    return ws_id, None


# ── Lifecycle phase helpers ───────────────────────────────────────────────────

def _phase_preflight(lf):
    """Verify the ibmcloud CLI is authenticated before any API calls are made.

    `ibmcloud iam oauth-tokens` returns a non-zero exit code when the session
    has expired or no login has been performed, making it a reliable auth probe.
    Returns a filled Phase object.
    """
    p = Phase("preflight")
    t0 = time.time()
    try:
        rc, out, err = run_cmd("ibmcloud iam oauth-tokens")
        if rc != 0:
            raise RuntimeError(
                "Not logged in. Run: ibmcloud login --apikey YOUR_API_KEY -r REGION"
            )
        tee("  ibmcloud CLI authenticated", lf)
        p.status = "PASS"
    except Exception as exc:
        p.status = "FAIL"
        p.error  = str(exc)
        tee(f"  ERROR: {exc}", lf)
    p.duration = int(time.time() - t0)
    return p


def _phase_setup(tfvars_path, ts_label, branch, ws_id_arg, run, lf):
    """Parse tfvars, write workspace.json, and resolve the workspace display name.

    When --ws-id is supplied, the real workspace name is fetched from Schematics
    so the report shows the human-readable name rather than just the ID.
    When creating a new workspace, the prospective name from build_workspace_json
    is used — it will match the actual name after creation.

    Returns (phase, ws_name).
    """
    p = Phase("setup")
    ws_name = None
    t0 = time.time()
    try:
        if not Path(tfvars_path).exists():
            raise FileNotFoundError(
                f"{tfvars_path} not found — "
                "copy terraform.tfvars.example and fill in your values"
            )
        variables = parse_tfvars(tfvars_path)
        ws        = build_workspace_json(variables, ts_label, branch=branch)
        ws_name   = ws["name"]

        # If the workspace already exists, fetch its real name for the report header
        if ws_id_arg:
            try:
                d = ibmcloud_json(f"ibmcloud schematics workspace get --id {ws_id_arg}", lf)
                ws_name = d.get("name", ws_id_arg)
            except Exception:
                ws_name = ws_id_arg

        tee(f"  {len(variables)} variables parsed from {tfvars_path}", lf)
        tee(f"  Workspace name : {ws_name}", lf)
        tee(f"  Branch         : {branch}", lf)
        tee(f"  Location       : {ws['location']}", lf)
        tee(f"  Phases         : {' '.join(ph for ph in VALID_PHASES if ph in run)}", lf)
        if ws_id_arg:
            tee(f"  WS ID (--ws-id): {ws_id_arg}", lf)
        p.status = "PASS"
    except Exception as exc:
        p.status = "FAIL"
        p.error  = str(exc)
        tee(f"  ERROR: {exc}", lf)
    p.duration = int(time.time() - t0)
    return p, ws_name


def _phase_create(lf):
    """Create a new Schematics workspace from workspace.json and wait until ready.

    The workspace is ready when it leaves DRAFT and its lock is released —
    see wait_for_workspace_ready. Returns (phase, ws_id); ws_id is None on failure.
    """
    p = Phase("create")
    ws_id = None
    t0 = time.time()
    try:
        rc, out, err = run_cmd(
            f"ibmcloud schematics workspace new --file {WS_JSON_PATH} --output json"
        )
        if out.strip():
            print(out, file=lf, flush=True)
        if rc != 0:
            raise RuntimeError((err or out).strip())
        data  = json.loads(out)
        ws_id = data.get("id") or data.get("workspace_id")
        if not ws_id:
            raise RuntimeError(f"workspace ID not in response: {out[:300]}")
        tee(f"  Workspace ID : {ws_id}", lf)
        tee("  Waiting for workspace to become ready...", lf)
        status = wait_for_workspace_ready(ws_id, lf)
        tee(f"  Ready status : {status}", lf)
        p.status = "PASS"
    except Exception as exc:
        p.status = "FAIL"
        p.error  = str(exc)
        tee(f"  ERROR: {exc}", lf)
    p.duration = int(time.time() - t0)
    return p, ws_id


def _phase_plan(ws_id, lf):
    """Run `ibmcloud schematics plan` and wait for a terminal workspace status.

    A terminal status of INACTIVE or ACTIVE indicates the plan succeeded.
    FAILED indicates a Terraform plan error (usually a configuration problem).
    Returns a filled Phase object.
    """
    p = Phase("plan")
    t0 = time.time()
    try:
        passed, final_status, elapsed = run_job(
            cmd              = f"ibmcloud schematics plan --id {ws_id}",
            ws_id            = ws_id,
            label            = "plan",
            lf               = lf,
            success_statuses = {"INACTIVE", "ACTIVE"},
            timeout          = JOB_TIMEOUT,
        )
        tee(f"  Final status : {final_status}  ({elapsed}s)", lf)
        p.status = "PASS" if passed else "FAIL"
        if not passed:
            p.error = f"status after plan: {final_status}"
    except Exception as exc:
        p.status = "FAIL"
        p.error  = str(exc)
        tee(f"  ERROR: {exc}", lf)
    p.duration = int(time.time() - t0)
    return p


def _phase_apply(ws_id, p_plan, lf):
    """Run `ibmcloud schematics apply` and capture output variables on success.

    Skips automatically when the plan phase failed — applying a failed plan
    would either error immediately or provision resources in an inconsistent state.
    Output variables are captured only on a clean ACTIVE terminal status, since
    partial applies may not have all outputs populated.

    Returns (phase, outputs_dict).
    """
    p = Phase("apply")
    outputs = {}

    # If plan failed (or was skipped due to a prior failure), do not attempt apply
    if p_plan.status == "FAIL":
        p.status = "SKIP"
        p.error  = "skipped — plan failed"
        return p, outputs

    t0 = time.time()
    try:
        passed, final_status, elapsed = run_job(
            cmd              = f"ibmcloud schematics apply --id {ws_id} --force",
            ws_id            = ws_id,
            label            = "apply",
            lf               = lf,
            success_statuses = {"ACTIVE"},
            timeout          = JOB_TIMEOUT,
        )
        tee(f"  Final status : {final_status}  ({elapsed}s)", lf)
        p.status = "PASS" if passed else "FAIL"
        if not passed:
            p.error = f"status after apply: {final_status}"
        if p.status == "PASS":
            tee("  Fetching outputs...", lf)
            outputs = fetch_outputs(ws_id, lf)
    except Exception as exc:
        p.status = "FAIL"
        p.error  = str(exc)
        tee(f"  ERROR: {exc}", lf)
    p.duration = int(time.time() - t0)
    return p, outputs


def _phase_destroy(ws_id, lf):
    """Run `ibmcloud schematics destroy` to deprovision all managed resources.

    Skips when the workspace has no managed Terraform state (INACTIVE or DRAFT),
    which is the case if apply never ran successfully or resources were already
    cleaned up by a previous destroy. A successful destroy leaves the workspace
    in INACTIVE status.

    Returns a filled Phase object.
    """
    p = Phase("destroy")

    # Check current state before running — nothing to destroy if INACTIVE/DRAFT
    pre = get_ws_status(ws_id) if ws_id else "UNKNOWN"
    if pre in {"INACTIVE", "DRAFT"}:
        p.status = "SKIP"
        p.error  = f"no managed state (status={pre})"
        return p

    t0 = time.time()
    try:
        passed, final_status, elapsed = run_job(
            cmd              = f"ibmcloud schematics destroy --id {ws_id} --force",
            ws_id            = ws_id,
            label            = "destroy",
            lf               = lf,
            success_statuses = {"INACTIVE"},
            timeout          = JOB_TIMEOUT,
        )
        tee(f"  Final status : {final_status}  ({elapsed}s)", lf)
        p.status = "PASS" if passed else "FAIL"
        if not passed:
            p.error = f"status after destroy: {final_status}"
    except Exception as exc:
        p.status = "FAIL"
        p.error  = str(exc)
        tee(f"  ERROR: {exc}", lf)
    p.duration = int(time.time() - t0)
    return p


def _phase_delete(ws_id, lf):
    """Delete the Schematics workspace record from the service inventory.

    This removes the workspace entry from Schematics but does NOT deprovision
    any cloud resources. Always run destroy before delete in a full lifecycle.
    Returns a filled Phase object.
    """
    p = Phase("delete")

    if not ws_id:
        # Can happen if --phases included delete but create was skipped or failed
        p.status = "SKIP"
        p.error  = "no workspace ID — create was skipped"
        return p

    t0 = time.time()
    try:
        rc, out, err = run_cmd(
            f"ibmcloud schematics workspace delete --id {ws_id} --force"
        )
        if rc != 0:
            raise RuntimeError((err or out).strip())
        tee("  Workspace record deleted", lf)
        p.status = "PASS"
    except Exception as exc:
        p.status = "FAIL"
        p.error  = str(exc)
        tee(f"  ERROR: {exc}", lf)
    p.duration = int(time.time() - t0)
    return p


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    import argparse
    parser = argparse.ArgumentParser(
        description=f"{TITLE} — Schematics lifecycle runner",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "phases (in execution order):\n"
            "  create   create the Schematics workspace\n"
            "  plan     plan (validate) the workspace\n"
            "  apply    apply (provision) the workspace\n"
            "  destroy  destroy (deprovision) the workspace\n"
            "  delete   delete the workspace record\n"
        ),
    )
    parser.add_argument(
        "tfvars", nargs="?", default=TFVARS_DEFAULT,
        help="Path to terraform.tfvars (default: %(default)s)",
    )
    parser.add_argument("--branch", default="main",
                        help="GitHub branch to deploy (default: %(default)s)")
    parser.add_argument(
        "--phases", nargs="+", default=VALID_PHASES,
        choices=VALID_PHASES, metavar="PHASE",
        help="Phases to run (default: all). Choices: " + " ".join(VALID_PHASES),
    )
    parser.add_argument(
        "--ws-id", default=None, dest="ws_id", metavar="WS_ID",
        help="Existing workspace ID (required when 'create' is not in --phases)",
    )
    parser.add_argument(
        "--list", action="store_true",
        help="List workspaces matching this repo's name prefix and exit",
    )
    parser.add_argument(
        "--resources", action="store_true",
        help="Print workspace resource list and exit",
    )
    parser.add_argument(
        "--outputs", action="store_true",
        help="Print workspace output variables and exit",
    )
    args = parser.parse_args()

    # ── Early-exit info commands ──────────────────────────────────────────────
    # These do not modify any workspace state — safe to run at any time.
    if args.list:
        return show_workspace_list(args.tfvars)

    if args.resources or args.outputs:
        ws_id, err = _resolve_ws_id(args.ws_id)
        if err:
            print(f"ERROR: {err}")
            return 1
        if args.resources:
            return show_resources(ws_id)
        return show_outputs(ws_id)

    # ── Lifecycle run — validate inputs before touching any cloud resources ────
    run = set(args.phases)

    needs_ws = run & {"plan", "apply", "destroy", "delete"}
    if "create" not in run and needs_ws and not args.ws_id:
        print(
            "ERROR: --ws-id is required when 'create' is not in --phases\n"
            "       Use --list to find the workspace ID."
        )
        return 1

    REPORT_DIR.mkdir(exist_ok=True)
    ts_label    = datetime.now(timezone.utc).strftime("%Y%m%d_%H%M%S")
    report_path = REPORT_DIR / f"lifecycle_{ts_label}.txt"
    log_path    = REPORT_DIR / f"lifecycle_{ts_label}_logs.txt"

    started_at = datetime.now(timezone.utc)
    ws_id      = args.ws_id   # updated after create; stays None if create is skipped
    ws_name    = None          # resolved in setup; used by report and SIGINT handler
    phases     = []
    outputs    = {}
    overall    = "FAIL"

    with open(log_path, "w") as lf:

        def _write_report(result):
            """Render and persist the current state of the report."""
            report = render_report(started_at, ws_id, ws_name, phases, outputs, result)
            tee(report, lf)
            report_path.write_text(report)

        def _sigint(sig, frame):
            """SIGINT handler: destroy and delete the workspace before exiting.

            Without this, a Ctrl-C mid-apply would leave running cloud resources
            that continue to accrue charges. ws_id and ws_name are captured from
            the enclosing scope and reflect the latest values at interrupt time.
            """
            tee("\n\nInterrupted — running cleanup...", lf)
            cleanup(ws_id, lf)
            _write_report("INTERRUPTED")
            sys.exit(130)

        signal.signal(signal.SIGINT, _sigint)

        # ── Preflight (always) ────────────────────────────────────────────────
        section("PRE-FLIGHT — Check ibmcloud CLI login", lf)
        p = _phase_preflight(lf)
        phases.append(p)
        if p.status != "PASS":
            _write_report("FAIL")
            return 1

        # ── Setup (always) ────────────────────────────────────────────────────
        section("SETUP — Parse terraform.tfvars → workspace.json", lf)
        p, ws_name = _phase_setup(args.tfvars, ts_label, args.branch, ws_id, run, lf)
        phases.append(p)
        if p.status != "PASS":
            _write_report("FAIL")
            return 1

        # ── Phase: create ─────────────────────────────────────────────────────
        if "create" in run:
            section("PHASE — Create workspace", lf)
            p, new_ws_id = _phase_create(lf)
            if new_ws_id:
                ws_id = new_ws_id   # make ws_id available to SIGINT handler
            phases.append(p)
            if p.status != "PASS":
                _write_report("FAIL")
                return 1

        # ── Phase: plan ───────────────────────────────────────────────────────
        p_plan = Phase("plan")
        if "plan" in run:
            section("PHASE — Plan workspace", lf)
            p_plan = _phase_plan(ws_id, lf)
        phases.append(p_plan)

        # ── Phase: apply ──────────────────────────────────────────────────────
        p_apply = Phase("apply")
        if "apply" in run:
            section("PHASE — Apply workspace", lf)
            p_apply, outputs = _phase_apply(ws_id, p_plan, lf)
        phases.append(p_apply)

        # ── Phase: destroy ────────────────────────────────────────────────────
        p_destroy = Phase("destroy")
        if "destroy" in run:
            section("PHASE — Destroy workspace", lf)
            p_destroy = _phase_destroy(ws_id, lf)
        phases.append(p_destroy)

        # ── Phase: delete ─────────────────────────────────────────────────────
        p_delete = Phase("delete")
        if "delete" in run:
            section("PHASE — Delete workspace record", lf)
            p_delete = _phase_delete(ws_id, lf)
        phases.append(p_delete)

        # ── Final report ──────────────────────────────────────────────────────
        # Skipped phases do not count for or against the overall result — only
        # phases that actually ran (PASS or FAIL) determine the outcome.
        all_run = [p for p in phases if p.status != "SKIP"]
        overall = "PASS" if all(p.status == "PASS" for p in all_run) else "FAIL"

        _write_report(overall)
        tee(f"  Log    : {log_path}", lf)
        tee(f"  Report : {report_path}", lf)

        return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
