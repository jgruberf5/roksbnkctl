#!/usr/bin/env python3
"""
BIG-IP Next for Kubernetes 2.3 — Testing (Jumphosts) Schematics Lifecycle Runner

Manages a single IBM Schematics workspace for the testing (jumphost infrastructure)
Terraform module.  The script drives the full workspace lifecycle through up to five
ordered phases; each phase can be skipped via --phases so the script can be re-entered
mid-lifecycle (e.g., to run only destroy + delete against an existing workspace).

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

# ── Constants ─────────────────────────────────────────────────────────────────

# Display / report column width used throughout for formatting.
W = 72

REPO_URL       = "https://github.com/f5devcentral/ibmcloud_schematics_bigip_next_for_kubernetes_2_3_testing"
WS_NAME_PREFIX = "bnk-23-testing"
TITLE          = "BIG-IP Next for Kubernetes 2.3 — Testing"
TFVARS_DEFAULT = "terraform.tfvars"
WS_JSON_PATH   = "workspace.json"
REPORT_DIR     = Path("test-reports")

POLL_INTERVAL = 30
JOB_TIMEOUT   = 18000   # 300 min — generous upper bound for slow Terraform runs
READY_TIMEOUT = 300     # max seconds to wait for a new workspace to leave DRAFT

# Variable names whose values must not appear in plain-text output or logs.
SECURE_VARS = {"ibmcloud_api_key", "bigip_password"}

# Schematics workspace status values that indicate no job is currently running.
TERMINAL_STATUSES = {"INACTIVE", "ACTIVE", "FAILED", "STOPPED", "DRAFT"}

VALID_PHASES = ["create", "plan", "apply", "destroy", "delete"]

# Outputs printed first (in this order) in the summary report.
KEY_OUTPUTS = [
    "testing_tgw_jumphost_public_ip",
    "testing_tgw_jumphost_ssh_command",
    "testing_tgw_jumphost_private_ip",
    "testing_tgw_jumphost_zone",
    "testing_tgw_jumphost_profile_used",
    "testing_jumphost_shared_public_key",
    "testing_transit_gateway_connection_id",
    "roks_cluster_name",
]


# ── Low-level helpers ─────────────────────────────────────────────────────────

def tee(msg, lf=None):
    """Print msg to stdout and, if lf is provided, to the log file as well."""
    print(msg, flush=True)
    if lf:
        print(msg, file=lf, flush=True)


def run_cmd(cmd, lf=None, stream=False):
    """
    Run a shell command and return (returncode, stdout, stderr).

    When stream=True, stdout and stderr are merged, each line is printed as it
    arrives (and written to lf), and the full output is returned as stdout.
    Use stream=True for long-running commands whose live output is useful to see
    (e.g., schematics logs).
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
    """
    Run an ibmcloud command with --output json appended and return the parsed
    result dict or list.

    Raises RuntimeError if the command exits non-zero or the output is not
    valid JSON.
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
    """Print a section header bar to stdout and the log file."""
    bar = "─" * W
    tee(f"\n{bar}\n  {title}\n{bar}", lf)


# ── tfvars / workspace.json ───────────────────────────────────────────────────

def parse_tfvars(path):
    """
    Parse a Terraform .tfvars file and return a list of Schematics variable
    dicts suitable for the workspace creation payload.

    Each dict has the shape {name, value, type} with an optional secure=True
    flag for variables listed in SECURE_VARS.

    Handles:
      - Quoted strings:    key = "value"           → type: string
      - Booleans:          key = true / false       → type: bool
      - Numbers:           key = 42 / 3.14          → type: number
      - Unquoted strings:  key = something          → type: string
      - Inline comments:   key = value # ignored    (stripped before parsing)
      - Comment-only / blank lines                  (skipped)
    """
    variables = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            # Skip blank lines and comment-only lines.
            if not line or line.startswith("#"):
                continue
            m = re.match(r'^(\w+)\s*=\s*(.+)$', line)
            if not m:
                continue
            name, raw = m.group(1), m.group(2).strip()

            # Quoted string: capture everything between the outer quotes,
            # including escaped characters.  Any inline comment after the
            # closing quote is silently discarded.
            qm = re.match(r'^"((?:[^"\\]|\\.)*)"', raw)
            if qm:
                value = qm.group(1)
                entry = {"name": name, "value": value, "type": "string"}
            else:
                # Unquoted value: strip any trailing inline comment first.
                value = re.sub(r'\s*#.*$', '', raw).strip()
                if value in ("true", "false"):
                    entry = {"name": name, "value": value, "type": "bool"}
                elif re.match(r'^-?\d+(\.\d+)?$', value):
                    entry = {"name": name, "value": value, "type": "number"}
                else:
                    entry = {"name": name, "value": value, "type": "string"}

            if name in SECURE_VARS:
                entry["secure"] = True
            variables.append(entry)
    return variables


def build_workspace_json(variables, ts_label, branch="main"):
    """
    Build the Schematics workspace creation payload from parsed tfvars variables
    and write it to WS_JSON_PATH.  Returns the workspace dict.

    The workspace name, location, and resource group are derived from tfvars
    when present; otherwise sensible defaults are used.
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


# ── Schematics workspace polling ──────────────────────────────────────────────

def get_ws_info(ws_id):
    """
    Return (status, locked) for a workspace.

    The status field location varies slightly across Schematics API versions, so
    both the top-level status and the nested workspace_status_msg are checked.
    On any error, returns ("UNKNOWN", True) so callers treat the workspace as
    unavailable / locked.
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
    """Return just the status string for a workspace (convenience wrapper)."""
    status, _ = get_ws_info(ws_id)
    return status


def wait_for_workspace_ready(ws_id, lf, timeout=READY_TIMEOUT):
    """
    Block until the workspace leaves DRAFT / locked state, meaning Schematics
    has finished cloning the repo and the workspace can accept plan/apply jobs.

    Returns the status string when ready.  If the timeout expires first, logs a
    warning and returns whatever the current status is so the caller can decide
    whether to proceed.
    """
    start = time.time()
    while True:
        elapsed = int(time.time() - start)
        if elapsed > timeout:
            tee(f"\n  WARNING: workspace not ready after {timeout}s — proceeding anyway", lf)
            return get_ws_status(ws_id)
        status, locked = get_ws_info(ws_id)
        if status in {"INACTIVE", "ACTIVE", "FAILED"} and not locked:
            print()  # end the \r progress line cleanly
            return status
        msg = f"  [ready] {elapsed}s  status={status}  locked={locked}"
        print(f"\r{msg:<76}", end="", flush=True)
        print(msg, file=lf, flush=True)
        time.sleep(10)


def poll_until_terminal(ws_id, label, lf, timeout=JOB_TIMEOUT):
    """
    Poll the workspace status every POLL_INTERVAL seconds until a terminal
    status is reached or the timeout expires.

    Returns (status, elapsed_seconds).
    """
    start = time.time()
    while True:
        elapsed = int(time.time() - start)
        if elapsed > timeout:
            return "TIMEOUT", elapsed
        status = get_ws_status(ws_id)
        if status in TERMINAL_STATUSES:
            print()  # end the \r progress line cleanly
            return status, elapsed
        msg = f"  [{label}] {elapsed}s elapsed  status={status}"
        print(f"\r{msg:<76}", end="", flush=True)
        print(msg, file=lf, flush=True)
        time.sleep(POLL_INTERVAL)


def stream_logs(ws_id, act_id, lf):
    """Stream the activity logs for act_id to stdout and the log file."""
    run_cmd(
        f"ibmcloud schematics logs --id {ws_id} --act-id {act_id}",
        lf=lf, stream=True,
    )


def run_job(cmd, ws_id, label, lf, success_statuses, timeout=JOB_TIMEOUT):
    """
    Submit a Schematics job (plan / apply / destroy) and wait for completion.

    Retries automatically on HTTP 409 (workspace temporarily locked) until the
    overall job timeout is exhausted.  This is normal when Schematics serialises
    concurrent operations on the same workspace.

    Once the job is accepted, waits up to 120 s for the workspace status to
    transition away from its pre-submission value (Schematics can be slow to
    reflect the new activity), then polls until a terminal status is reached and
    streams the final log output.

    Elapsed time is measured from when the job command is first accepted — not
    from the first submission attempt — so 409-retry time is excluded from the
    reported duration.

    Returns (passed: bool, final_status: str, elapsed_seconds: int).
    """
    pre_status    = get_ws_status(ws_id)
    lock_deadline = time.time() + timeout
    attempt       = 0

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
        # Non-retryable failure: dump any available output before raising.
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

    # Elapsed time starts from when the job was successfully submitted.
    t0 = time.time()

    if act_id:
        tee("  Waiting for activity to start...", lf)
        # Poll for up to 120 s for the workspace to transition out of its
        # pre-submission status before beginning terminal-status polling.
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
        # No activity ID in the response (older API behaviour); fall back to
        # status polling without streaming logs.
        tee("  No activity ID returned — polling workspace status...", lf)
        final_status, _ = poll_until_terminal(ws_id, label, lf, timeout=timeout)

    elapsed = int(time.time() - t0)
    passed  = final_status in success_statuses
    return passed, final_status, elapsed


def fetch_outputs(ws_id, lf=None):
    """
    Retrieve all Terraform output values for a workspace and return them as a
    flat {name: value} dict.

    The Schematics API returns a list of template objects.  Each template has an
    output_values list of single-key dicts mapping the output name to a metadata
    object of the form {value, type, sensitive}.  This function flattens that
    nested structure into a plain name→value mapping.

    Returns an empty dict if outputs are unavailable (e.g., apply has not run).
    """
    try:
        data  = ibmcloud_json(f"ibmcloud schematics output --id {ws_id}", lf)
        # The API may return a single object or a list of template objects.
        items = data if isinstance(data, list) else [data]
        out   = {}
        for template in items:
            for item in template.get("output_values", []):
                # Each item is {output_name: {value, type, sensitive}}.
                for name, meta in item.items():
                    out[name] = meta.get("value", "") if isinstance(meta, dict) else meta
        return out
    except Exception as exc:
        if lf:
            tee(f"  WARNING: could not fetch outputs: {exc}", lf)
        return {}


# ── Phase data model ──────────────────────────────────────────────────────────

class Phase:
    """Lightweight record capturing the outcome of one lifecycle phase."""
    __slots__ = ("name", "status", "duration", "error")

    def __init__(self, name):
        self.name     = name
        self.status   = "SKIP"   # default: phase was not requested
        self.duration = 0        # seconds
        self.error    = None     # human-readable failure description


# ── Phase execution ───────────────────────────────────────────────────────────

def run_preflight(lf):
    """
    Verify that the ibmcloud CLI is currently authenticated.

    This phase always runs and must pass before any other phase is attempted.
    Returns a Phase object.
    """
    section("PRE-FLIGHT — Check ibmcloud CLI login", lf)
    p  = Phase("preflight")
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


def run_setup(tfvars_path, ts_label, branch, ws_id_hint, run, lf):
    """
    Parse the tfvars file, write workspace.json, and resolve the workspace name.

    ws_id_hint is the --ws-id argument value (None when the create phase will
    create a new workspace).  If a workspace ID is provided, its current name is
    fetched from Schematics so subsequent log and report output reflects the real
    name rather than the planned name.

    Returns (phase, ws_id, ws_name).  ws_id is ws_id_hint unchanged; ws_name is
    the resolved display name.
    """
    section("SETUP — Parse terraform.tfvars → workspace.json", lf)
    p       = Phase("setup")
    t0      = time.time()
    ws_id   = ws_id_hint
    ws_name = None
    try:
        if not Path(tfvars_path).exists():
            raise FileNotFoundError(
                f"{tfvars_path} not found — "
                "copy terraform.tfvars.example and fill in your values"
            )
        variables = parse_tfvars(tfvars_path)
        ws        = build_workspace_json(variables, ts_label, branch=branch)
        ws_name   = ws["name"]

        # When skipping the create phase, look up the real workspace name.
        if ws_id:
            try:
                d       = ibmcloud_json(f"ibmcloud schematics workspace get --id {ws_id}", lf)
                ws_name = d.get("name", ws_id)
            except Exception:
                ws_name = ws_id

        tee(f"  {len(variables)} variables parsed from {tfvars_path}", lf)
        tee(f"  Workspace name : {ws_name}", lf)
        tee(f"  Branch         : {branch}", lf)
        tee(f"  Location       : {ws['location']}", lf)
        tee(f"  Phases         : {' '.join(ph for ph in VALID_PHASES if ph in run)}", lf)
        if ws_id:
            tee(f"  WS ID (--ws-id): {ws_id}", lf)
        p.status = "PASS"
    except Exception as exc:
        p.status = "FAIL"
        p.error  = str(exc)
        tee(f"  ERROR: {exc}", lf)
    p.duration = int(time.time() - t0)
    return p, ws_id, ws_name


def run_phase_create(ws_json_path, lf):
    """
    Create a new Schematics workspace from the pre-written workspace.json and
    wait for it to finish initialising (cloning the repo, entering INACTIVE).

    Returns (phase, ws_id, ws_name).  ws_id and ws_name are None on failure.
    """
    section("PHASE — Create workspace", lf)
    p       = Phase("create")
    t0      = time.time()
    ws_id   = None
    ws_name = None
    try:
        rc, out, err = run_cmd(
            f"ibmcloud schematics workspace new --file {ws_json_path} --output json"
        )
        if out.strip():
            print(out, file=lf, flush=True)
        if rc != 0:
            raise RuntimeError((err or out).strip())
        data    = json.loads(out)
        ws_id   = data.get("id") or data.get("workspace_id")
        ws_name = data.get("name")
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
    return p, ws_id, ws_name


def run_phase_plan(ws_id, lf):
    """
    Run schematics plan on the workspace.

    A successful plan leaves the workspace in INACTIVE (no prior state) or
    ACTIVE (existing state refreshed) status.  Returns a Phase object.
    """
    section("PHASE — Plan workspace", lf)
    p  = Phase("plan")
    t0 = time.time()
    try:
        passed, final_status, elapsed = run_job(
            cmd              = f"ibmcloud schematics plan --id {ws_id}",
            ws_id            = ws_id,
            label            = "plan",
            lf               = lf,
            success_statuses = {"INACTIVE", "ACTIVE"},
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


def run_phase_apply(ws_id, p_plan, lf):
    """
    Run schematics apply on the workspace.

    Skipped automatically when the plan phase failed — there is no point
    applying a configuration that did not plan successfully.

    Returns (phase, outputs_dict).  outputs is empty on skip or failure.
    """
    p       = Phase("apply")
    outputs = {}

    if p_plan.status == "FAIL":
        # Avoid provisioning against an invalid plan.
        p.status = "SKIP"
        p.error  = "skipped — plan failed"
        return p, outputs

    section("PHASE — Apply workspace", lf)
    t0 = time.time()
    try:
        passed, final_status, elapsed = run_job(
            cmd              = f"ibmcloud schematics apply --id {ws_id} --force",
            ws_id            = ws_id,
            label            = "apply",
            lf               = lf,
            success_statuses = {"ACTIVE"},
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


def run_phase_destroy(ws_id, lf):
    """
    Run schematics destroy on the workspace.

    Skipped automatically when the workspace has no managed Terraform state
    (status INACTIVE or DRAFT), since there is nothing to destroy and Schematics
    would reject or no-op the request.  Returns a Phase object.
    """
    p = Phase("destroy")

    pre = get_ws_status(ws_id) if ws_id else "UNKNOWN"
    if pre in {"INACTIVE", "DRAFT"}:
        p.status = "SKIP"
        p.error  = f"no managed state (status={pre})"
        return p

    section("PHASE — Destroy workspace", lf)
    t0 = time.time()
    try:
        passed, final_status, elapsed = run_job(
            cmd              = f"ibmcloud schematics destroy --id {ws_id} --force",
            ws_id            = ws_id,
            label            = "destroy",
            lf               = lf,
            success_statuses = {"INACTIVE"},
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


def run_phase_delete(ws_id, lf):
    """
    Delete the Schematics workspace record.

    This removes the workspace from the Schematics console but does NOT destroy
    any infrastructure — destroy must be run first.  Returns a Phase object.
    """
    section("PHASE — Delete workspace record", lf)
    p  = Phase("delete")
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


# ── Emergency cleanup (SIGINT / CTRL-C) ──────────────────────────────────────

def _emergency_cleanup(ws_id, lf):
    """
    Best-effort destroy + delete on abnormal exit.

    Called from the SIGINT handler when the user interrupts a running lifecycle.
    Errors are not re-raised since we are already in a failure path and the goal
    is simply to avoid leaving orphaned cloud resources.
    """
    if not ws_id:
        return
    tee(f"\n  Cleanup: destroying workspace {ws_id} ...", lf)
    run_cmd(f"ibmcloud schematics destroy --id {ws_id} --force", lf=lf, stream=True)
    poll_until_terminal(ws_id, "cleanup-destroy", lf, timeout=JOB_TIMEOUT)
    tee(f"  Cleanup: deleting workspace {ws_id} ...", lf)
    run_cmd(f"ibmcloud schematics workspace delete --id {ws_id} --force", lf=lf)


# ── Report rendering ──────────────────────────────────────────────────────────

def render_report(started_at, ws_id, ws_name, phases, outputs, overall):
    """
    Render the human-readable test summary as a multi-line string.

    KEY_OUTPUTS are printed first in their predefined order; any additional
    outputs from the workspace are appended below a secondary divider.
    """
    elapsed = int((datetime.now(timezone.utc) - started_at).total_seconds())
    sep = "=" * W
    thn = "-" * W
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
    """
    Fetch all Schematics workspaces and return those whose names start with
    WS_NAME_PREFIX, sorted newest-first by name.

    Returns (matches, error_str).  error_str is None on success.
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
    """Extract the display status string from a workspace list entry."""
    return (
        w.get("status")
        or w.get("workspace_status_msg", {}).get("status_code")
        or "UNKNOWN"
    )


def show_workspace_list(tfvars_path):
    """Print a formatted table of matching workspaces and exit. Returns exit code."""
    sep = "=" * W
    thn = "─" * (W - 4)

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
    """Print the Terraform state resource list for a workspace. Returns exit code."""
    sep = "=" * W
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
    """Print all Terraform output variables for a workspace. Returns exit code."""
    sep = "=" * W
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


def _resolve_ws_id(args_ws_id):
    """
    Return the workspace ID to use for --resources / --outputs queries.

    If --ws-id was given, use it directly.  Otherwise auto-detect by selecting
    the most-recently-named workspace with the expected name prefix.

    Returns (ws_id, error_str).  error_str is None on success.
    """
    if args_ws_id:
        return args_ws_id, None
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
    parser.add_argument(
        "--branch", default="main",
        help="GitHub branch to deploy (default: %(default)s)",
    )
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

    # ── Early-exit info commands ──────────────────────────────────────────
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

    # ── Lifecycle run ─────────────────────────────────────────────────────
    run         = set(args.phases)
    tfvars_path = args.tfvars
    branch      = args.branch

    # Phases other than create all require a workspace to already exist.
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
    ws_id      = args.ws_id or None
    ws_name    = None
    phases     = []
    outputs    = {}
    overall    = "FAIL"

    with open(log_path, "w") as lf:

        def _write_report(result):
            """Render, print, and persist the report with the given result string."""
            report = render_report(started_at, ws_id, ws_name, phases, outputs, result)
            tee(report, lf)
            report_path.write_text(report)

        def _sigint(sig, frame):
            """SIGINT handler: clean up the workspace then write a final report.
            Exit code 130 is the POSIX convention for SIGINT-terminated processes."""
            tee("\n\nInterrupted — running cleanup...", lf)
            _emergency_cleanup(ws_id, lf)
            _write_report("INTERRUPTED")
            sys.exit(130)

        signal.signal(signal.SIGINT, _sigint)

        # ── Preflight (always) ────────────────────────────────────────────
        p = run_preflight(lf)
        phases.append(p)
        if p.status != "PASS":
            _write_report("FAIL")
            return 1

        # ── Setup (always) ────────────────────────────────────────────────
        p, ws_id, ws_name = run_setup(tfvars_path, ts_label, branch, ws_id, run, lf)
        phases.append(p)
        if p.status != "PASS":
            _write_report("FAIL")
            return 1

        # ── Phase: create ─────────────────────────────────────────────────
        if "create" in run:
            p, ws_id, created_name = run_phase_create(WS_JSON_PATH, lf)
            if created_name:
                # Use the name Schematics assigned in the response.
                ws_name = created_name
            phases.append(p)
            if p.status != "PASS":
                _write_report("FAIL")
                return 1

        # ── Phase: plan ───────────────────────────────────────────────────
        p_plan = Phase("plan")
        if "plan" in run:
            p_plan = run_phase_plan(ws_id, lf)
        phases.append(p_plan)

        # ── Phase: apply ──────────────────────────────────────────────────
        if "apply" in run:
            p_apply, outputs = run_phase_apply(ws_id, p_plan, lf)
        else:
            p_apply = Phase("apply")
        phases.append(p_apply)

        # ── Phase: destroy ────────────────────────────────────────────────
        if "destroy" in run:
            p_destroy = run_phase_destroy(ws_id, lf)
        else:
            p_destroy = Phase("destroy")
        phases.append(p_destroy)

        # ── Phase: delete ─────────────────────────────────────────────────
        p_delete = Phase("delete")
        if "delete" in run:
            if ws_id:
                p_delete = run_phase_delete(ws_id, lf)
            else:
                # create was skipped and no --ws-id was provided.
                p_delete.status = "SKIP"
                p_delete.error  = "no workspace ID — create was skipped"
        phases.append(p_delete)

        # ── Final report ──────────────────────────────────────────────────
        # Phases with status SKIP are not counted toward the overall result.
        all_run = [p for p in phases if p.status != "SKIP"]
        overall = "PASS" if all(p.status == "PASS" for p in all_run) else "FAIL"

        _write_report(overall)

        tee(f"  Log    : {log_path}", lf)
        tee(f"  Report : {report_path}", lf)

        return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
