#!/usr/bin/env python3
"""
BIG-IP Next for Kubernetes 2.3 — FLO Schematics Lifecycle Runner

Manages a single IBM Schematics workspace for the flo (F5 Lifecycle Operator)
Terraform module.  The script drives the full lifecycle end-to-end — create,
plan, apply, destroy, delete — while polling for completion, streaming logs,
and writing a structured report.

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

import argparse
import json
import re
import signal
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

# ── Configuration ─────────────────────────────────────────────────────────────

REPO_URL       = "https://github.com/f5devcentral/ibmcloud_schematics_bigip_next_for_kubernetes_2_3_flo"
WS_NAME_PREFIX = "bnk-23-flo"
TITLE          = "BIG-IP Next for Kubernetes 2.3 — FLO"
TFVARS_DEFAULT = "terraform.tfvars"
WS_JSON_PATH   = "workspace.json"   # ephemeral file passed to `ibmcloud schematics workspace new`
REPORT_DIR     = Path("test-reports")
REPORT_WIDTH   = 72                 # column width used by all report and display functions

# Schematics job polling configuration.
# POLL_INTERVAL: how often to query workspace status while a job is running.
# JOB_TIMEOUT: hard ceiling for any single plan/apply/destroy (300 min).
# READY_TIMEOUT: how long to wait for a freshly created workspace to leave DRAFT.
POLL_INTERVAL = 30
JOB_TIMEOUT   = 18000   # 300 min max
READY_TIMEOUT = 300

# Variables whose values must not appear in plain text in the workspace JSON or
# any log output.  Schematics marks them as sensitive and redacts them in the UI.
SECURE_VARS = {"ibmcloud_api_key", "bigip_password"}

# Workspace statuses that indicate a job has finished (success or failure).
# Anything outside this set means a job is still running and we keep polling.
TERMINAL_STATUSES = {"INACTIVE", "ACTIVE", "FAILED", "STOPPED", "DRAFT"}

VALID_PHASES = ["create", "plan", "apply", "destroy", "delete"]

# Ordered list of output variable names to surface prominently in the report.
# Any output not in this list is printed in a secondary section below.
KEY_OUTPUTS = [
    "flo_release_name",
    "flo_namespace",
    "flo_version",
    "flo_extracted_flo_version",
    "flo_trusted_profile_id",
    "flo_pod_deployment_status",
    "flo_cluster_issuer_name",
    "cneinstance_network_attachments",
]

# Metadata passed to Schematics so the UI can display output descriptions.
# The `name` fields must match the actual Terraform output names in outputs.tf.
OUTPUT_METADATA = [
    {"name": "flo_release_name",               "description": "Name of the f5-lifecycle-operator Helm release"},
    {"name": "flo_namespace",                  "description": "Namespace where f5-lifecycle-operator is installed"},
    {"name": "flo_version",                    "description": "Installed f5-lifecycle-operator version"},
    {"name": "flo_extracted_flo_version",      "description": "FLO version extracted from f5-bigip-k8s-manifest"},
    {"name": "flo_trusted_profile_id",         "description": "IBM IAM Trusted Profile ID created for the CNE controller service account"},
    {"name": "flo_pod_deployment_status",      "description": "FLO pod deployment status"},
    {"name": "flo_cluster_issuer_name",        "description": "mTLS certificate issuer name"},
    {"name": "cneinstance_network_attachments","description": "Network attachments configured for CNEInstance"},
]


# ── Low-level helpers ─────────────────────────────────────────────────────────

def tee(msg, lf=None):
    """Print msg to stdout and, if a log file handle is supplied, to that file too."""
    print(msg, flush=True)
    if lf:
        print(msg, file=lf, flush=True)


def run_cmd(cmd, lf=None, stream=False):
    """Run a shell command and return (returncode, stdout, stderr).

    stream=False (default): buffers all output and returns it as strings.
    stream=True: prints each line of combined stdout+stderr in real time (useful
    for log commands where output can take minutes to arrive) and also mirrors to
    `lf` if supplied.  In streaming mode stderr is merged into stdout, so the
    returned stderr string is always empty.
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
    """Run an ibmcloud command with --output json and return the parsed result.

    The --output json flag is appended automatically so callers don't need to
    include it.  Raises RuntimeError if the command exits non-zero or if the
    output cannot be parsed as JSON.
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


# ── tfvars / workspace.json ───────────────────────────────────────────────────

def parse_tfvars(path):
    """Parse a terraform.tfvars file into the variable list format Schematics expects.

    This is a limited HCL parser that handles only simple scalar assignments
    (string, number, bool).  Multi-line strings, lists, and maps are not
    supported — the Schematics variablestore format itself only accepts scalars.

    Returns a list of dicts with keys: name, value, type, and optionally secure.
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
                # Strip surrounding quotes from HCL string literals.
                entry = {"name": name, "value": raw.strip('"'), "type": "string"}
            if name in SECURE_VARS:
                # The `secure` flag tells Schematics to encrypt the value at rest
                # and suppress it in log output and the UI.
                entry["secure"] = True
            variables.append(entry)
    return variables


def build_workspace_json(variables, ts_label, branch="main"):
    """Build the workspace.json payload and write it to WS_JSON_PATH.

    The Schematics `workspace new --file` command expects a specific JSON schema:
    - template_repo points to the GitHub repo and branch containing the TF code.
    - template_data[0].variablestore carries the variable values we parsed from tfvars.
    - output_values_metadata populates the Outputs tab in the Schematics UI.

    The workspace name embeds a timestamp so concurrent test runs don't collide.
    Returns the full workspace dict (callers use it to read back location/name).
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
            "output_values_metadata": OUTPUT_METADATA,
        }],
    }
    Path(WS_JSON_PATH).write_text(json.dumps(ws, indent=2))
    return ws


# ── Schematics polling ────────────────────────────────────────────────────────

def get_ws_info(ws_id):
    """Return (status, locked) for the given workspace.

    Both fields are needed together when deciding whether to proceed: a workspace
    can report INACTIVE (terminal) while still being locked by a finishing job,
    which would cause a 409 on the next operation.  Fetching them atomically
    avoids the TOCTOU race.

    Returns ("UNKNOWN", True) on any error so callers treat failures conservatively.
    """
    try:
        data   = ibmcloud_json(f"ibmcloud schematics workspace get --id {ws_id}")
        status = data.get("status") or data.get("workspace_status_msg", {}).get("status_code") or "UNKNOWN"
        locked = data.get("workspace_status", {}).get("locked", False)
        return status, locked
    except Exception:
        return "UNKNOWN", True


def get_ws_status(ws_id):
    """Return only the status string for the given workspace."""
    status, _ = get_ws_info(ws_id)
    return status


def wait_for_workspace_ready(ws_id, lf, timeout=READY_TIMEOUT):
    """Block until the workspace is in a non-locked terminal state.

    A freshly created workspace starts in DRAFT and transitions to INACTIVE once
    Schematics finishes its internal setup.  Running a plan before that transition
    completes results in a 409.  This wait is shorter and uses a tighter poll
    interval (10 s) than the main job poller because workspace setup is fast.
    """
    start = time.time()
    while True:
        elapsed = int(time.time() - start)
        if elapsed > timeout:
            tee(f"\n  WARNING: workspace not ready after {timeout}s — proceeding anyway", lf)
            return get_ws_status(ws_id)
        status, locked = get_ws_info(ws_id)
        if status in {"INACTIVE", "ACTIVE", "FAILED"} and not locked:
            print()  # end the in-place status line
            return status
        msg = f"  [ready] {elapsed}s  status={status}  locked={locked}"
        print(f"\r{msg:<{REPORT_WIDTH}}", end="", flush=True)
        print(msg, file=lf, flush=True)
        time.sleep(10)


def poll_until_terminal(ws_id, label, lf, timeout=JOB_TIMEOUT):
    """Poll workspace status every POLL_INTERVAL seconds until it reaches a terminal state.

    Returns (final_status, elapsed_seconds).  Callers are responsible for
    deciding whether the final status represents success or failure for their
    particular operation, because the same terminal status (e.g. INACTIVE) can
    mean "plan succeeded" or "destroy succeeded" depending on context.
    """
    start = time.time()
    while True:
        elapsed = int(time.time() - start)
        if elapsed > timeout:
            return "TIMEOUT", elapsed
        status = get_ws_status(ws_id)
        if status in TERMINAL_STATUSES:
            print()  # end the in-place status line
            return status, elapsed
        msg = f"  [{label}] {elapsed}s elapsed  status={status}"
        print(f"\r{msg:<{REPORT_WIDTH}}", end="", flush=True)
        print(msg, file=lf, flush=True)
        time.sleep(POLL_INTERVAL)


def stream_logs(ws_id, act_id, lf):
    """Stream Schematics activity logs to stdout (and lf) in real time."""
    run_cmd(
        f"ibmcloud schematics logs --id {ws_id} --act-id {act_id}",
        lf=lf, stream=True,
    )


def run_job(cmd, ws_id, label, lf, success_statuses, timeout=JOB_TIMEOUT):
    """Submit a Schematics job, wait for it to complete, and stream its logs.

    Handles two common failure modes before the job even starts:
    - 409 / "temporarily locked": Schematics serializes jobs per workspace; a
      previous job may still be releasing its lock.  We retry every 30 s until
      the overall timeout budget is exhausted.
    - Non-409 error: raised immediately as RuntimeError.

    Once the job is submitted we wait for the workspace status to change from its
    pre-submission value (confirming the activity started), then poll to terminal,
    then fetch the final logs.

    Returns (passed, final_status, elapsed_seconds).
    """
    pre_status    = get_ws_status(ws_id)
    lock_deadline = time.time() + timeout
    attempt       = 0

    # Submit the job, retrying on 409 lock conflicts.
    while True:
        attempt += 1
        rc, out, err = run_cmd(f"{cmd} --output json")
        combined = (out + err).lower()
        if rc == 0:
            break
        if ("409" in combined or "temporarily locked" in combined) and time.time() < lock_deadline:
            remaining = int(lock_deadline - time.time())
            tee(f"  Workspace locked (409) — retrying in 30s "
                f"(attempt {attempt}, {remaining}s remaining in budget)", lf)
            time.sleep(30)
            continue
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
        # Schematics can take a few seconds to transition the workspace out of
        # its pre-submission status.  Waiting for that transition before entering
        # the main poll loop avoids prematurely reading a stale terminal status.
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
        # Some Schematics API versions don't return an activity ID in the response
        # body; fall back to status polling without log streaming.
        tee("  No activity ID returned — polling workspace status...", lf)
        final_status, _ = poll_until_terminal(ws_id, label, lf, timeout=timeout)

    elapsed = int(time.time() - t0)
    passed  = final_status in success_statuses
    return passed, final_status, elapsed


def cleanup_stale_iam_profile(var_map, lf=None):
    """Delete any IBM IAM trusted profile whose name ends with the expected suffix.

    The profile name is {cluster_name}-f5-cne-controller-{flo_namespace}.  The
    cluster name is resolved at apply time, so we match by suffix to catch stale
    profiles left by previous failed runs.

    If a stale profile exists when `apply` runs, the Terraform `ibm_iam_trusted_profile`
    resource will fail with a conflict error because the name must be unique within
    the account.  Deleting it here keeps reruns idempotent.
    """
    flo_namespace = var_map.get("flo_namespace", "f5-bnk")
    suffix = f"-f5-cne-controller-{flo_namespace}"
    tee(f"  Checking for stale IAM trusted profiles (*{suffix})", lf)
    rc, out, err = run_cmd("ibmcloud iam trusted-profiles --output json")
    if rc != 0:
        tee(f"  WARNING: could not list IAM trusted profiles: {(err or out).strip()}", lf)
        return
    try:
        raw = json.loads(out)
        # The response shape varies by CLI version: either a bare list or a dict
        # with one of several possible list keys.
        profiles = raw if isinstance(raw, list) else (
            raw.get("TrustedProfiles") or raw.get("trusted_profiles") or []
        )
        for p in profiles:
            name = p.get("Name") or p.get("name", "")
            if name.endswith(suffix):
                pid = p.get("ID") or p.get("id", "")
                tee(f"  Deleting stale IAM trusted profile: {name} ({pid})", lf)
                run_cmd(f"ibmcloud iam trusted-profile-delete {pid} -f", lf=lf)
    except (json.JSONDecodeError, TypeError) as exc:
        tee(f"  WARNING: could not parse IAM trusted profiles: {exc}", lf)


def fetch_outputs(ws_id, lf=None):
    """Fetch Terraform output values from a Schematics workspace.

    The Schematics outputs API returns a list of template objects, each of which
    contains an output_values list.  Each element in that list is a dict mapping
    output name to a metadata object with keys: value, type, sensitive.  This
    function flattens all templates into a single {name: value} dict.

    Returns an empty dict if the workspace has not been applied or on any error.
    """
    try:
        data  = ibmcloud_json(f"ibmcloud schematics output --id {ws_id}", lf)
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


# ── Report rendering ──────────────────────────────────────────────────────────

class Phase:
    """Holds the result of a single lifecycle phase for inclusion in the report.

    status is initialized to "SKIP" so phases that were excluded from the run
    (via --phases) appear correctly in the report without special-casing.
    """
    __slots__ = ("name", "status", "duration", "error")

    def __init__(self, name):
        self.name     = name
        self.status   = "SKIP"
        self.duration = 0
        self.error    = None


def render_report(started_at, ws_id, ws_name, phases, outputs, overall):
    """Render a human-readable summary of the lifecycle run.

    Returns the report as a single string so callers can both print it (via tee)
    and write it to a file with a single call to render_report.
    """
    elapsed = int((datetime.now(timezone.utc) - started_at).total_seconds())
    sep = "=" * REPORT_WIDTH
    thn = "-" * REPORT_WIDTH
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
    """Return (list_of_workspace_dicts, error_string) for workspaces whose name starts with WS_NAME_PREFIX.

    The list is sorted newest-first by name (the timestamp suffix ensures
    lexicographic order matches creation order).  On error returns (None, message).
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
    """Extract a workspace status string from a workspace list entry.

    The Schematics list API returns status under different keys depending on the
    CLI version, so we try both locations.
    """
    return (
        w.get("status")
        or w.get("workspace_status_msg", {}).get("status_code")
        or "UNKNOWN"
    )


def show_workspace_list(tfvars_path):
    """Print a table of workspaces matching WS_NAME_PREFIX and exit."""
    sep = "=" * REPORT_WIDTH
    thn = "─" * (REPORT_WIDTH - 4)

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
    """Print the Terraform resource list for an existing workspace and exit."""
    sep = "=" * REPORT_WIDTH
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
    """Print Terraform output variables for an existing workspace and exit."""
    sep = "=" * REPORT_WIDTH
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
    """Return the workspace ID to use, auto-detecting the most recent one if not supplied.

    When --ws-id is not given on the command line, we query Schematics for all
    workspaces matching WS_NAME_PREFIX and pick the most recently created one
    (first in the newest-first sorted list).

    Returns (ws_id, None) on success or (None, error_message) on failure.
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
    ws_id      = args.ws_id or None   # may be reassigned by the create phase
    ws_name    = None
    var_map    = {}
    phases     = []
    outputs    = {}
    overall    = "FAIL"

    with open(log_path, "w") as lf:

        def section(title):
            """Print a separator and section heading to stdout and the log file."""
            bar = "─" * REPORT_WIDTH
            tee(f"\n{bar}\n  {title}\n{bar}", lf)

        def cleanup():
            """Destroy and delete the workspace, used by the SIGINT handler.

            This function reads `ws_id` from the enclosing main() scope at the
            moment it is called (not at definition time), so it correctly handles
            the case where ws_id was assigned during the create phase after the
            function was defined.
            """
            if not ws_id:
                return
            tee(f"\n  Cleanup: destroying workspace {ws_id} ...", lf)
            run_cmd(f"ibmcloud schematics destroy --id {ws_id} --force", lf=lf, stream=True)
            poll_until_terminal(ws_id, "cleanup-destroy", lf, timeout=JOB_TIMEOUT)
            tee(f"  Cleanup: deleting workspace {ws_id} ...", lf)
            run_cmd(f"ibmcloud schematics workspace delete --id {ws_id} --force", lf=lf)

        def _sigint(sig, frame):
            """SIGINT handler: run cleanup, write an INTERRUPTED report, then exit 130."""
            tee("\n\nInterrupted — running cleanup...", lf)
            cleanup()
            report = render_report(started_at, ws_id, ws_name, phases, outputs, "INTERRUPTED")
            tee(report, lf)
            report_path.write_text(report)
            sys.exit(130)

        signal.signal(signal.SIGINT, _sigint)

        # ── Preflight (always) ────────────────────────────────────────────
        # Verify ibmcloud CLI is authenticated before doing anything else.
        # An unauthenticated session produces cryptic 401 errors deep in a run.
        section("PRE-FLIGHT — Check ibmcloud CLI login")
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
        phases.append(p)
        if p.status != "PASS":
            report = render_report(started_at, ws_id, ws_name, phases, outputs, "FAIL")
            tee(report, lf)
            report_path.write_text(report)
            return 1

        # ── Setup (always) ────────────────────────────────────────────────
        # Parse tfvars and materialise workspace.json so it's ready for create.
        # If --ws-id was supplied (skip-create runs), we also fetch the workspace
        # name for display purposes.
        section("SETUP — Parse terraform.tfvars → workspace.json")
        p = Phase("setup")
        t0 = time.time()
        try:
            if not Path(tfvars_path).exists():
                raise FileNotFoundError(
                    f"{tfvars_path} not found — "
                    "copy terraform.tfvars.example and fill in your values"
                )
            variables = parse_tfvars(tfvars_path)
            var_map   = {v["name"]: v["value"] for v in variables}
            ws        = build_workspace_json(variables, ts_label, branch=branch)
            ws_name   = ws["name"]

            if ws_id:
                # When attaching to an existing workspace, resolve the human-readable
                # name from Schematics so it appears correctly in the report header.
                try:
                    d = ibmcloud_json(f"ibmcloud schematics workspace get --id {ws_id}", lf)
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
        phases.append(p)
        if p.status != "PASS":
            report = render_report(started_at, ws_id, ws_name, phases, outputs, "FAIL")
            tee(report, lf)
            report_path.write_text(report)
            return 1

        # ── Phase: create ─────────────────────────────────────────────────
        # Create a new Schematics workspace from the workspace.json we built in
        # setup.  This phase assigns ws_id, which all subsequent phases depend on.
        if "create" in run:
            section("PHASE — Create workspace")
            p = Phase("create")
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
            phases.append(p)
            if p.status != "PASS":
                report = render_report(started_at, ws_id, ws_name, phases, outputs, "FAIL")
                tee(report, lf)
                report_path.write_text(report)
                return 1

        # ── Phase: plan ───────────────────────────────────────────────────
        # Plan validates the Terraform configuration against the IBM Cloud API
        # without making any changes.  A passing plan is a prerequisite for apply.
        # p_plan is declared before the conditional so apply can inspect its status
        # even when plan was explicitly excluded from --phases (status stays "SKIP",
        # which does not block apply).
        p_plan = Phase("plan")
        if "plan" in run:
            section("PHASE — Plan workspace")
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
                p_plan.status = "PASS" if passed else "FAIL"
                if not passed:
                    p_plan.error = f"status after plan: {final_status}"
            except Exception as exc:
                p_plan.status = "FAIL"
                p_plan.error  = str(exc)
                tee(f"  ERROR: {exc}", lf)
            p_plan.duration = int(time.time() - t0)
        phases.append(p_plan)

        # ── Phase: apply ──────────────────────────────────────────────────
        # Apply provisions all resources.  Skipped automatically if plan failed to
        # avoid running `apply` against a configuration known to be invalid.
        # Pre-apply IAM cleanup removes any stale trusted profile that would cause
        # a naming conflict during resource creation.
        p_apply = Phase("apply")
        if "apply" in run:
            if p_plan.status == "FAIL":
                p_apply.status = "SKIP"
                p_apply.error  = "skipped — plan failed"
            else:
                section("PHASE — Apply workspace")
                tee("  Cleaning up stale IAM resources before apply...", lf)
                cleanup_stale_iam_profile(var_map, lf)
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
                    p_apply.status = "PASS" if passed else "FAIL"
                    if not passed:
                        p_apply.error = f"status after apply: {final_status}"
                    if p_apply.status == "PASS":
                        tee("  Fetching outputs...", lf)
                        outputs = fetch_outputs(ws_id, lf)
                except Exception as exc:
                    p_apply.status = "FAIL"
                    p_apply.error  = str(exc)
                    tee(f"  ERROR: {exc}", lf)
                p_apply.duration = int(time.time() - t0)
        phases.append(p_apply)

        # ── Phase: destroy ────────────────────────────────────────────────
        # Destroy deprovisions all resources tracked in Terraform state.
        # We always attempt destroy when apply failed: Schematics can settle back
        # to INACTIVE after a partial apply even though cloud resources may still
        # exist.  Skipping destroy in that case would leave orphaned infrastructure.
        # A workspace with no managed state (INACTIVE/DRAFT without a prior apply)
        # is skipped because there is nothing to destroy.
        p_destroy = Phase("destroy")
        if "destroy" in run:
            pre = get_ws_status(ws_id) if ws_id else "UNKNOWN"
            apply_failed = "apply" in run and p_apply.status == "FAIL"
            if pre in {"INACTIVE", "DRAFT"} and not apply_failed:
                p_destroy.status = "SKIP"
                p_destroy.error  = f"no managed state (status={pre})"
            else:
                section("PHASE — Destroy workspace")
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
                    p_destroy.status = "PASS" if passed else "FAIL"
                    if not passed:
                        p_destroy.error = f"status after destroy: {final_status}"
                except Exception as exc:
                    p_destroy.status = "FAIL"
                    p_destroy.error  = str(exc)
                    tee(f"  ERROR: {exc}", lf)
                p_destroy.duration = int(time.time() - t0)
        phases.append(p_destroy)

        # ── Phase: delete ─────────────────────────────────────────────────
        # Delete removes the workspace record itself from Schematics (metadata only;
        # all cloud resources must already be destroyed).  Skipped silently when
        # no ws_id exists (i.e. create was excluded and no --ws-id was supplied).
        p_delete = Phase("delete")
        if "delete" in run and ws_id:
            section("PHASE — Delete workspace record")
            t0 = time.time()
            try:
                rc, out, err = run_cmd(
                    f"ibmcloud schematics workspace delete --id {ws_id} --force"
                )
                if rc != 0:
                    raise RuntimeError((err or out).strip())
                tee("  Workspace record deleted", lf)
                p_delete.status = "PASS"
            except Exception as exc:
                p_delete.status = "FAIL"
                p_delete.error  = str(exc)
                tee(f"  ERROR: {exc}", lf)
            p_delete.duration = int(time.time() - t0)
        elif "delete" in run:
            p_delete.status = "SKIP"
            p_delete.error  = "no workspace ID — create was skipped"
        phases.append(p_delete)

        # ── Final report ──────────────────────────────────────────────────
        # Overall result is PASS only when every phase that actually ran (not
        # SKIPped) succeeded.  A phase that was skipped due to a dependency failure
        # (e.g. apply skipped because plan failed) does not itself count as a
        # failure — the root cause is already recorded on the failed phase.
        all_run = [p for p in phases if p.status not in {"SKIP"}]
        overall = "PASS" if all(p.status == "PASS" for p in all_run) else "FAIL"

        report = render_report(started_at, ws_id, ws_name, phases, outputs, overall)
        tee(report, lf)
        report_path.write_text(report)

        tee(f"  Log    : {log_path}", lf)
        tee(f"  Report : {report_path}", lf)

        return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
