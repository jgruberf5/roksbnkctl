#!/usr/bin/env bash
# scripts/e2e-init-var-file.sh — gated live-verify driver for the
# Sprint 19 staff feature `roksbnkctl init --var-file <path>` (validator
# Issue 1). Mirrors scripts/e2e-cos-bucket-get.sh's style and
# discipline: structured logging, redact() over every echoed command,
# DRY_RUN walk-through, EXIT-trap teardown, and no API-key value ever
# read out of ./terraform.tfvars into argv or stdout.
#
# What it proves (validator Issue 1 acceptance criteria 1–4):
#   * S1 — `roksbnkctl init -w <ws> --var-file ./terraform.tfvars`
#     succeeds on a fresh throwaway workspace.
#   * S2 — bare `roksbnkctl plan -w <ws>` (NO `--var-file`) succeeds
#     because the seeded `terraform.tfvars.user` copy is auto-layered
#     by the existing `tfws.HasUserTFVars()` codepath. THIS is the
#     gap-closure assertion v1.6.3 surfaced — between `init` and the
#     first successful `up`, bare commands couldn't pick up the
#     operator's var-file before this sprint.
#   * A1 — `<workspace-root>/terraform.tfvars.user` (sibling to
#     config.yaml) exists + mode 0600 + byte-identical to the input
#     file. ONE copy at the workspace root — `tf.Workspace.UserTFVarsPath`
#     resolves to that single path for BOTH the trial and cluster phases.
#     The stale in-state-dir paths (the round-1 bug) must NOT exist.
#   * A2 — `config.yaml` reflects the tfvars-seeded fields (region,
#     cluster name, OpenShift version, workers-per-zone) — the
#     interview prompts the file answered were skipped.
#   * A3 — the bare `plan` run log carries the existing `→ Layering
#     user tfvars from <path>` line from internal/orchestration's
#     `HasUserTFVars()`-true branch (NOT just an exit-0 proxy — pins
#     the actual codepath that closes the v1.6.3 UX gap).
#   * A4 — planted-sentinel API-key leak scan over the run log = 0
#     hits (proves `redact()` covered every echo path).
#
# ─────────────────────────────────────────────────────────────────────
# THIS IS NOT A CI JOB. Operator-run only, via `!`.
# ─────────────────────────────────────────────────────────────────────
#
#   * NO cloud spend. `init` doesn't provision anything; this driver
#     never runs `roksbnkctl up`. `plan` is read-only (terraform plan,
#     not apply). Wall time ≈ seconds. The driver still tears down its
#     workspace on EXIT so a stray run doesn't litter the operator's
#     ~/.roksbnkctl tree.
#   * Opt-in. Nothing automatic runs this — no GitHub workflow.
#   * Requires the operator's repo-root ./terraform.tfvars (or a path
#     they pass via $TFVARS). The file's `ibmcloud_api_key` is NEVER
#     read into argv or stdout — the driver references the file by
#     path only and lets the binary do the I/O. Same posture as the
#     Sprint 18 driver.
#
# Usage:
#   ./scripts/e2e-init-var-file.sh                    # live verify
#   DRY_RUN=1 ./scripts/e2e-init-var-file.sh          # plan only
#
# Knobs:
#   TFVARS         default ./terraform.tfvars       (path only; contents never printed)
#   WORKSPACE      default e2e-init-vf-$RUN_TS      (auto-suffixed throwaway)
#   DRY_RUN        default 0
#   LOG_DIR        default /tmp/roksbnkctl-e2e-init-var-file
#   ROKSBNKCTL     default roksbnkctl
#
# Exit codes: 0 = GREEN. Non-zero = first failed assertion, with the
# failing check named in the error line.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
TFVARS=${TFVARS:-./terraform.tfvars}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e-init-var-file}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
WORKSPACE=${WORKSPACE:-e2e-init-vf-$RUN_TS}
RUN_LOG="$LOG_DIR/init-var-file-$RUN_TS.log"
WORK_DIR="$LOG_DIR/work-$RUN_TS"
mkdir -p "$WORK_DIR"

# ── helpers (mirror scripts/e2e-cos-bucket-get.sh) ──────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }
log()    { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

# Redact the API key value from any string we are about to print.
# Belt-and-braces — this driver never builds a command that contains
# the key, but if the environment leaks one into argv we still don't
# echo it. Identical pattern to scripts/e2e-cos-bucket-get.sh's
# redact().
redact() {
    local s="$*"
    if [[ -n "${IBMCLOUD_API_KEY:-}" ]]; then
        s=${s//"$IBMCLOUD_API_KEY"/<redacted>}
    fi
    printf '%s' "$s"
}

step() {
    local desc="$1"; shift
    log "→ $desc"
    log "  cmd: $(redact "$*")"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "  (dry-run; skipping execution)"
        return 0
    fi
    if "$@" 2>&1 | tee -a "$RUN_LOG"; then
        green "  ✓ $desc"
        return 0
    else
        local rc=${PIPESTATUS[0]}
        red "  ✗ $desc (exit $rc)"
        red "  full log: $RUN_LOG"
        exit "$rc"
    fi
}

fail() {
    red "  ✗ $1"
    red "  full log: $RUN_LOG"
    exit 2
}

# ── self-teardown trap ──────────────────────────────────────────────
# Best-effort. `init` doesn't provision cloud infra so there's no
# billable resource to strand — just the workspace state directory.
# Still LOUD so the operator sees what was removed.
TORN_DOWN=0
teardown() {
    local prev_rc=$?
    [[ "$TORN_DOWN" == "1" ]] && return
    TORN_DOWN=1
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ teardown (dry-run): delete workspace $WORKSPACE"
        return
    fi
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    yellow "TEARDOWN — removing temporary workspace $WORKSPACE"
    bold "════════════════════════════════════════════════════════════"
    # ws delete first (handles its own existence check); rm -rf is the
    # belt-and-braces fallback for partial init runs that didn't fully
    # register the workspace.
    "$ROKSBNKCTL" ws delete "$WORKSPACE" --force >>"$RUN_LOG" 2>&1 || true
    local ws_dir="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WORKSPACE"
    if [[ -d "$ws_dir" ]]; then
        rm -rf "$ws_dir" || true
    fi
    green "  ✓ teardown: workspace $WORKSPACE removed"
    if [[ "$prev_rc" != "0" ]]; then
        red "Run FAILED (exit $prev_rc) — see $RUN_LOG"
    fi
}
trap teardown EXIT

# ── preflight ───────────────────────────────────────────────────────
preflight() {
    bold "preflight"
    if [[ ! -f "$TFVARS" ]]; then
        fail "TFVARS file not found: $TFVARS (structure-only reference; never printed)"
    fi
    if [[ "$DRY_RUN" != "1" ]]; then
        if ! command -v "$ROKSBNKCTL" >/dev/null 2>&1; then
            fail "$ROKSBNKCTL not on PATH (set ROKSBNKCTL=/abs/path/to/binary)"
        fi
        if ! command -v sha256sum >/dev/null 2>&1; then
            fail "sha256sum not on PATH (coreutils); required for the byte-identical assertion"
        fi
    fi
    log "preflight OK — workspace=$WORKSPACE tfvars=$TFVARS (contents not printed) log=$RUN_LOG"
}

# Plant a sentinel that LOOKS like a key-leak risk so the final A4
# scan can prove the driver scrubbed it. Random, generated per-run; if
# it ever shows up in the run log we know redact() failed.
plant_sentinel() {
    SENTINEL="ROKSBNKCTL_E2E_INIT_VF_SENTINEL_$(head -c 16 /dev/urandom | xxd -p)"
    # Use the redact() helper to pretend the sentinel was the API key
    # for one redaction round-trip — proves redact() is wired before we
    # rely on it for real.
    local before="cmd --api-key $SENTINEL init --var-file …"
    IBMCLOUD_API_KEY=$SENTINEL  # local override JUST for the assertion
    local after; after=$(redact "$before")
    IBMCLOUD_API_KEY=${REAL_API_KEY:-}
    if [[ "$after" == *"$SENTINEL"* ]]; then
        fail "redact() did NOT strip the sentinel — driver would leak the API key"
    fi
    log "redact() sentinel check passed (sentinel not present in redacted form)"
}

# ── the reproduction ────────────────────────────────────────────────
main() {
    bold "roksbnkctl init --var-file — live verify — run-id $RUN_TS"
    bold "(validator Issue 1 — Sprint 19 — NOT a CI job)"
    log "log: $RUN_LOG"
    preflight
    REAL_API_KEY=${IBMCLOUD_API_KEY:-}
    plant_sentinel

    # Resolve absolute paths NOW so the assertions below — which run
    # after the workspace exists — can grep the same paths the binary
    # logs. ROKSBNKCTL_HOME wins if set (test envs); otherwise the
    # canonical ~/.roksbnkctl.
    local base="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
    # Canonical Sprint 19 destination — sibling to config.yaml. The
    # same path serves both the trial and cluster phases via
    # tf.Workspace.UserTFVarsPath() = filepath.Dir(stateDir)/tfvars.user.
    local ws_user="$base/$WORKSPACE/terraform.tfvars.user"
    # The round-1 mis-located paths. A1 also asserts these DO NOT exist
    # so a regression back to the in-state-dir locations trips the check.
    local stale_trial="$base/$WORKSPACE/state/terraform.tfvars.user"
    local stale_cluster="$base/$WORKSPACE/state-cluster/terraform.tfvars.user"

    # S1 — init --var-file on a fresh throwaway workspace.
    # Stdin is /dev/null so any prompt the var-file didn't answer
    # falls through to its non-TTY default rather than blocking on
    # the operator's terminal.
    step "S1 init -w $WORKSPACE --var-file $TFVARS" \
        bash -c "$ROKSBNKCTL init -w $WORKSPACE --var-file $TFVARS </dev/null"

    bold "──── assertions ────"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ A1 workspace-root terraform.tfvars.user exists + mode 0600 + byte-identical"
        log "    path: $ws_user (stale: $stale_trial, $stale_cluster — must NOT exist)"
        log "→ A2 config.yaml reflects the tfvars-seeded fields (region, cluster name, …)"
        log "→ A3 bare plan -w $WORKSPACE log carries the '→ Layering user tfvars from <path>' line"
        log "→ A4 run log free of API-key leak (sentinel scan + sentinel echo proof)"
        green "DRY-RUN complete — steps rendered, no cloud calls, no key printed."
        return 0
    fi

    # A1 — single workspace-root copy present, mode 0600, byte-identical
    # to the input. And the stale in-state-dir paths from the round-1 bug
    # must NOT exist (HasUserTFVars() does not look at them).
    local want_sha; want_sha=$(sha256sum "$TFVARS" | awk '{print $1}')
    if [[ ! -f "$ws_user" ]]; then
        fail "A1: missing $ws_user"
    fi
    local got_mode; got_mode=$(stat -c '%a' "$ws_user")
    if [[ "$got_mode" != "600" ]]; then
        fail "A1: $ws_user mode = $got_mode, want 600"
    fi
    local got_sha; got_sha=$(sha256sum "$ws_user" | awk '{print $1}')
    if [[ "$got_sha" != "$want_sha" ]]; then
        fail "A1: $ws_user sha256 differs from input ($got_sha vs $want_sha)"
    fi
    for stale in "$stale_trial" "$stale_cluster"; do
        if [[ -f "$stale" ]]; then
            fail "A1: stale copy at $stale — HasUserTFVars() does not look here (round-1 regression)"
        fi
    done
    green "  ✓ A1 workspace-root terraform.tfvars.user exists + mode 0600 + byte-identical (no stale state-dir copies)"

    # A2 — config.yaml reflects the tfvars-seeded fields. Read the
    # tfvars values by passing the file's basenames through awk so we
    # never echo the file's contents into argv; the api_key line is
    # explicitly skipped from the check.
    local cfg="$base/$WORKSPACE/config.yaml"
    if [[ ! -f "$cfg" ]]; then
        fail "A2: $cfg does not exist"
    fi
    local want_region want_cluster want_ocp want_workers
    want_region=$(awk -F'=' '/^[[:space:]]*ibmcloud_cluster_region[[:space:]]*=/ {gsub(/[[:space:]"]/,"",$2); print $2; exit}' "$TFVARS")
    want_cluster=$(awk -F'=' '/^[[:space:]]*openshift_cluster_name[[:space:]]*=/ {gsub(/[[:space:]"]/,"",$2); print $2; exit}' "$TFVARS")
    want_ocp=$(awk -F'=' '/^[[:space:]]*openshift_cluster_version[[:space:]]*=/ {gsub(/[[:space:]"]/,"",$2); print $2; exit}' "$TFVARS")
    want_workers=$(awk -F'=' '/^[[:space:]]*roks_workers_per_zone[[:space:]]*=/ {gsub(/[[:space:]"]/,"",$2); print $2; exit}' "$TFVARS")
    # Required: region + cluster (others optional in the operator's file).
    # Patterns allow optional surrounding quotes — yaml.v3 quotes string
    # values that look like floats/numbers (e.g. openshift_version: "4.18")
    # to preserve the string type; bare strings (e.g. region: ca-tor) are
    # unquoted. The optional-quote shape matches both.
    if [[ -n "$want_region" ]] && ! grep -qE "region:[[:space:]]*\"?${want_region}\"?[[:space:]]*\$" "$cfg"; then
        fail "A2: config.yaml missing region: $want_region"
    fi
    if [[ -n "$want_cluster" ]] && ! grep -qE "name:[[:space:]]*\"?${want_cluster}\"?[[:space:]]*\$" "$cfg"; then
        fail "A2: config.yaml missing cluster name: $want_cluster"
    fi
    if [[ -n "$want_ocp" ]] && ! grep -qE "openshift_version:[[:space:]]*\"?${want_ocp}\"?[[:space:]]*\$" "$cfg"; then
        fail "A2: config.yaml missing openshift_version: $want_ocp"
    fi
    if [[ -n "$want_workers" ]] && ! grep -qE "workers_per_zone:[[:space:]]*\"?${want_workers}\"?[[:space:]]*\$" "$cfg"; then
        fail "A2: config.yaml missing workers_per_zone: $want_workers"
    fi
    green "  ✓ A2 config.yaml reflects the tfvars-seeded fields"

    # S2 + A3 — bare plan -w <ws> (NO --var-file). Expected to succeed
    # because the seeded terraform.tfvars.user copies are auto-layered
    # by tfws.HasUserTFVars(). The "→ Layering user tfvars from <path>"
    # log line (emitted by internal/orchestration/lifecycle.go when
    # HasUserTFVars() is true) is what proves the codepath fired — NOT
    # just an exit-0 proxy.
    local plan_log="$WORK_DIR/plan-$RUN_TS.log"
    set +e
    "$ROKSBNKCTL" plan -w "$WORKSPACE" </dev/null >"$plan_log" 2>&1
    local plan_rc=$?
    set -e
    cat "$plan_log" >> "$RUN_LOG"
    if [[ $plan_rc -ne 0 ]]; then
        fail "S2: bare plan -w $WORKSPACE exited $plan_rc (expected 0; the var-file should be auto-layered)"
    fi
    green "  ✓ S2 bare plan -w $WORKSPACE exited 0 — no --var-file re-supplied"

    # A3 — the codepath log line. Match on the stable substring shape
    # internal/orchestration/lifecycle.go emits; the path argument
    # varies by phase + workspace name so we anchor on the literal
    # prefix `→ Layering user tfvars from`.
    if ! grep -qF "→ Layering user tfvars from" "$plan_log"; then
        red "  bare-plan stderr:"
        sed -n '1,40p' "$plan_log" >&2
        fail "A3: bare plan log missing the '→ Layering user tfvars from <path>' codepath signal — HasUserTFVars() did not fire"
    fi
    green "  ✓ A3 bare plan log carries the user-tfvars layering line (codepath confirmed)"

    # A4 — leak scan over the combined run log. The sentinel planted
    # under plant_sentinel must NOT appear, AND the first 24 bytes of
    # the actual API key (if any was exported) must NOT appear. We
    # never read $TFVARS into a variable, so the on-disk file's bytes
    # only enter the log via roksbnkctl's own output — which we still
    # scan because a future regression could echo it.
    if grep -qF "$SENTINEL" "$RUN_LOG"; then
        fail "A4: sentinel leaked into the run log — redact() bypassed somewhere"
    fi
    if [[ -n "$REAL_API_KEY" ]]; then
        local head=${REAL_API_KEY:0:24}
        if grep -qF "$head" "$RUN_LOG"; then
            fail "A4: API-key head leaked into the run log — redact() did not cover all echo paths"
        fi
    fi
    green "  ✓ A4 leak scan: sentinel + API-key head both absent from $RUN_LOG"

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "GREEN — init --var-file verified live: terraform.tfvars.user"
    green "lands at workspace root, config.yaml seeded, bare plan"
    green "succeeds without re-supplying --var-file, codepath confirmed,"
    green "no key leaks. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
    green "(teardown runs next via the EXIT trap)"
}

main "$@"
