#!/usr/bin/env bash
# scripts/e2e-test.sh — full end-to-end shake-out of roksbnkctl against
# a live IBM Cloud account. Designed to be runnable both manually and
# unattended (the loop runner that babysits this for hours).
#
# Phases match docs/E2E_TEST.md.
#
# Usage:
#   IBMCLOUD_API_KEY=... ./scripts/e2e-test.sh                 # full pass from scratch
#   IBMCLOUD_API_KEY=... PHASE_FROM=D ./scripts/e2e-test.sh    # resume from phase D
#   IBMCLOUD_API_KEY=... DRY_RUN=1 ./scripts/e2e-test.sh       # print steps without executing
#
# Exits 0 on a clean pass, non-zero on the first assertion failure with
# the phase + step number in the error message.

set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
WORKSPACE=${WORKSPACE:-e2e}
TFVARS=${TFVARS:-$HOME/bnkfun/terraform.tfvars}
PHASE_FROM=${PHASE_FROM:-A}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/run-$RUN_TS.log"

# ── helpers ─────────────────────────────────────────────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }

log()    { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

# Run a command, stream + capture output, fail the script if it returns
# non-zero. Pass description as $1, the command as the rest.
step() {
    local desc="$1"; shift
    log "→ $desc"
    log "  cmd: $*"
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

# Like `step`, but captures stdout for downstream comparison instead of
# only logging. Echoes the captured output to stdout; assertions in the
# caller can pipe it into grep/awk.
capture() {
    local desc="$1"; shift
    log "→ $desc"
    log "  cmd: $*"
    if [[ "$DRY_RUN" == "1" ]]; then
        echo ""
        return 0
    fi
    local out
    out=$("$@" 2>&1) || {
        red "  ✗ $desc (exit $?)"
        echo "$out" >> "$RUN_LOG"
        exit 1
    }
    echo "$out" | tee -a "$RUN_LOG"
}

# Assert that a command's output (passed via stdin) contains a substring.
# Usage:  echo "$out" | assert_contains "expected substring" "step name"
#
# In dry-run mode, drains stdin and returns success — there's nothing
# to assert when no command actually ran.
assert_contains() {
    local needle="$1"
    local label="$2"
    if [[ "$DRY_RUN" == "1" ]]; then
        cat >/dev/null
        log "  (dry-run; skipping assertion: $label)"
        return 0
    fi
    if grep -qF "$needle" -; then
        green "  ✓ $label"
    else
        red "  ✗ $label — expected substring not found: $needle"
        exit 2
    fi
}

phase_header() {
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    bold "Phase $1 — $2"
    bold "════════════════════════════════════════════════════════════"
}

# Compare current phase letter against PHASE_FROM. Skip phases that come
# before PHASE_FROM. Used so the driver can resume at, say, phase D
# without re-running the cheap A/B/C phases.
should_run() {
    [[ "$1" > "$PHASE_FROM" || "$1" == "$PHASE_FROM" ]]
}

# ── preflight ───────────────────────────────────────────────────────
preflight() {
    bold "preflight"
    if [[ -z "${IBMCLOUD_API_KEY:-}" ]]; then
        # Try to extract the key from the tfvars file. The lifecycle
        # commands also need it as TF_VAR_ibmcloud_api_key, but
        # roksbnkctl itself reads IBMCLOUD_API_KEY first.
        local key
        key=$(grep -E '^ibmcloud_api_key' "$TFVARS" 2>/dev/null \
              | sed -E 's/.*"([^"]+)".*/\1/')
        if [[ -n "$key" ]]; then
            export IBMCLOUD_API_KEY="$key"
            log "Pulled IBMCLOUD_API_KEY from $TFVARS"
        else
            red "IBMCLOUD_API_KEY is unset and not found in $TFVARS"
            exit 3
        fi
    fi
    if [[ ! -f "$TFVARS" ]]; then
        red "TFVARS file not found: $TFVARS"
        exit 3
    fi
    if ! command -v "$ROKSBNKCTL" >/dev/null 2>&1; then
        red "$ROKSBNKCTL not on PATH (set ROKSBNKCTL=/path/to/binary)"
        exit 3
    fi
    log "preflight OK — workspace=$WORKSPACE tfvars=$TFVARS log=$RUN_LOG"
}

# ── phases ──────────────────────────────────────────────────────────

phase_A() {
    phase_header A "sanity (no cloud cost)"

    step "A1 version" "$ROKSBNKCTL" version

    # doctor may flag warnings (e.g., iperf3 missing) but should
    # complete the credential check. We treat exit 0 as pass.
    step "A2 doctor" "$ROKSBNKCTL" doctor

    # init non-interactively — close stdin so isTTY() returns false
    # and prompts default. Workspace defaults from existing config or
    # hardcoded fallbacks; --var-file overrides land at apply time.
    log "A3 init -w $WORKSPACE (non-TTY mode)"
    if [[ "$DRY_RUN" != "1" ]]; then
        if ! "$ROKSBNKCTL" init -w "$WORKSPACE" </dev/null >>"$RUN_LOG" 2>&1; then
            red "  ✗ A3 init failed — see $RUN_LOG"
            exit 1
        fi
        green "  ✓ A3 init"
    fi

    capture "A4 ws list" "$ROKSBNKCTL" ws list \
        | assert_contains "$WORKSPACE" "A4 ws list contains $WORKSPACE"

    rm -f /tmp/e2e-tfvars.tf
    step "A5 tfvars dump" "$ROKSBNKCTL" -w "$WORKSPACE" tfvars -o /tmp/e2e-tfvars.tf
    if [[ "$DRY_RUN" != "1" ]]; then
        grep -q "openshift_cluster_name" /tmp/e2e-tfvars.tf \
            && green "  ✓ A5 tfvars contains openshift_cluster_name" \
            || { red "  ✗ A5 tfvars dump missing expected variable"; exit 1; }
    fi
}

phase_B() {
    phase_header B "cluster-only lifecycle (~50 minutes)"

    step "B1 cluster up" "$ROKSBNKCTL" cluster up --auto -w "$WORKSPACE" --var-file "$TFVARS"

    capture "B2 cluster show" "$ROKSBNKCTL" cluster show -w "$WORKSPACE" \
        | assert_contains "cluster_name:     canada-roks" "B2 cluster_name correct"

    capture "B3 ibmcloud ks cluster get" "$ROKSBNKCTL" ibmcloud ks cluster get --cluster canada-roks \
        | assert_contains "normal" "B3 cluster state normal"

    capture "B4 kubectl get nodes" "$ROKSBNKCTL" kubectl get nodes \
        | assert_contains "Ready" "B4 nodes Ready"

    capture "B5 oc whoami" "$ROKSBNKCTL" oc whoami \
        | assert_contains "@" "B5 oc whoami returns user identity"

    log "B6 cluster stays up — Phase C will use it"
}

phase_C() {
    phase_header C "register an existing cluster (~30 seconds)"

    local outputs="$HOME/.roksbnkctl/$WORKSPACE/cluster-outputs.json"
    log "C1 simulate externally-created cluster"
    [[ "$DRY_RUN" == "1" ]] || rm -f "$outputs"

    step "C2 cluster register canada-roks" \
        "$ROKSBNKCTL" cluster register canada-roks \
        --registry-cos-name canada-roks-cos-instance \
        -w "$WORKSPACE"

    capture "C3 cluster show post-register" "$ROKSBNKCTL" cluster show -w "$WORKSPACE" \
        | assert_contains "cluster_name:     canada-roks" "C3 show matches"

    step "C4 cluster down" "$ROKSBNKCTL" cluster down --auto -w "$WORKSPACE" --var-file "$TFVARS"

    log "C5 cluster show should now error"
    if [[ "$DRY_RUN" != "1" ]]; then
        if "$ROKSBNKCTL" cluster show -w "$WORKSPACE" >/dev/null 2>&1; then
            red "  ✗ C5 cluster show unexpectedly succeeded after down"
            exit 1
        fi
        green "  ✓ C5 cluster show errors after down (expected)"
    fi
}

phase_D() {
    phase_header D "full lifecycle: cluster + BNK (~70 minutes)"

    step "D1 up" "$ROKSBNKCTL" up --auto -w "$WORKSPACE" --var-file "$TFVARS"

    capture "D2 status" "$ROKSBNKCTL" status -w "$WORKSPACE" \
        | assert_contains "canada-roks" "D2 status references cluster"

    step "D3 kubectl get pods -n f5-bnk" "$ROKSBNKCTL" kubectl get pods -n f5-bnk

    # logs may produce many lines or be empty if FLO hasn't logged
    # recently — just confirm the command exits 0.
    log "D4 logs flo (10s sample)"
    if [[ "$DRY_RUN" != "1" ]]; then
        timeout 10 "$ROKSBNKCTL" logs flo >>"$RUN_LOG" 2>&1
        local rc=$?
        # timeout returns 124 when it kills the process; that's expected
        # for `logs` without -f cap. Either 0 or 124 is fine here.
        if [[ "$rc" == "0" || "$rc" == "124" ]]; then
            green "  ✓ D4 logs flo"
        else
            red "  ✗ D4 logs flo (exit $rc)"
            exit "$rc"
        fi
    fi

    # Phase G + E + F run here — during the cluster's "up but not torn
    # down yet" window — to amortize wall time.
    phase_G_during_D
    phase_E_during_D
    phase_F_during_D

    step "D5 test connectivity" "$ROKSBNKCTL" test connectivity -o json -w "$WORKSPACE"
    step "D6 test dns"          "$ROKSBNKCTL" test dns -o json -w "$WORKSPACE"
    step "D7 test throughput"   "$ROKSBNKCTL" test throughput -o json -w "$WORKSPACE"

    step "D8 down" "$ROKSBNKCTL" down --auto -w "$WORKSPACE" --var-file "$TFVARS"
}

phase_E_during_D() {
    phase_header E "workspace ops (during D's idle window)"

    step "E1 ws new e2e-second" "$ROKSBNKCTL" ws new e2e-second
    capture "E2 ws list" "$ROKSBNKCTL" ws list \
        | assert_contains "e2e-second" "E2 ws list contains e2e-second"

    capture "E3 ws current" "$ROKSBNKCTL" ws current \
        | assert_contains "$WORKSPACE" "E3 ws current is $WORKSPACE"

    step    "E4a ws use e2e-second" "$ROKSBNKCTL" ws use e2e-second
    capture "E4b ws current after use" "$ROKSBNKCTL" ws current \
        | assert_contains "e2e-second" "E4b ws current switched"

    step "E5 ws use $WORKSPACE" "$ROKSBNKCTL" ws use "$WORKSPACE"
    step "E6 ws delete e2e-second" "$ROKSBNKCTL" ws delete e2e-second --force
}

phase_F_during_D() {
    phase_header F "COS object CRUD (during D's idle window)"

    # Per-run scratch bucket — avoids writing into the user's prod
    # buckets and dodges global-name collisions. IBM Cloud bucket names
    # are globally unique, so include $RANDOM and the run timestamp.
    local bucket="roksbnkctl-e2e-${RUN_TS}-${RANDOM}"
    bucket=$(echo "$bucket" | tr 'A-Z' 'a-z')  # IBM bucket names must be lowercase
    log "F bucket: $bucket on instance bnk-orchestration"

    capture "F1 cos instance list" "$ROKSBNKCTL" cos instance list \
        | assert_contains "bnk-orchestration" "F1 lists bnk-orchestration"

    step "F2 cos bucket list" "$ROKSBNKCTL" cos bucket list --instance bnk-orchestration

    step "F3 cos bucket create $bucket" \
        "$ROKSBNKCTL" cos bucket create "$bucket" --instance bnk-orchestration

    if [[ "$DRY_RUN" != "1" ]]; then
        dd if=/dev/urandom of=/tmp/e2e-cos-blob bs=1M count=4 status=none
    fi

    step "F4 cos object put" \
        "$ROKSBNKCTL" cos object put "$bucket/blob" /tmp/e2e-cos-blob \
        --instance bnk-orchestration

    step "F5 cos object get" \
        "$ROKSBNKCTL" cos object get "$bucket/blob" /tmp/e2e-cos-blob.out \
        --instance bnk-orchestration

    if [[ "$DRY_RUN" != "1" ]]; then
        if cmp -s /tmp/e2e-cos-blob /tmp/e2e-cos-blob.out; then
            green "  ✓ F5b roundtrip bytes match"
        else
            red "  ✗ F5b roundtrip bytes differ"
            exit 1
        fi
    fi

    step "F6 cos object delete" \
        "$ROKSBNKCTL" cos object delete "$bucket/blob" \
        --instance bnk-orchestration

    step "F7 cos bucket delete" \
        "$ROKSBNKCTL" cos bucket delete "$bucket" --instance bnk-orchestration
}

phase_G_during_D() {
    phase_header G "passthrough commands (during D's idle window)"

    step "G1 ibmcloud account show" "$ROKSBNKCTL" ibmcloud account show
    step "G2 kubectl version --client" "$ROKSBNKCTL" kubectl version --client
    step "G3 oc version --client" "$ROKSBNKCTL" oc version --client

    if [[ "$DRY_RUN" != "1" ]]; then
        capture "G4 exec env contains KUBECONFIG" "$ROKSBNKCTL" exec env \
            | grep -q "^KUBECONFIG=" \
            && green "  ✓ G4 KUBECONFIG exported" \
            || { red "  ✗ G4 KUBECONFIG missing in exec env"; exit 1; }
    else
        log "(dry-run; skipping G4 KUBECONFIG check)"
    fi
}

phase_H() {
    phase_header H "final cleanup"

    step "H1 ws delete $WORKSPACE" "$ROKSBNKCTL" ws delete "$WORKSPACE" --force

    if [[ "$DRY_RUN" != "1" ]]; then
        if [[ ! -d "$HOME/.roksbnkctl/$WORKSPACE" ]]; then
            green "  ✓ H2 workspace dir removed"
        else
            red "  ✗ H2 workspace dir still exists at $HOME/.roksbnkctl/$WORKSPACE"
            exit 1
        fi
    fi
}

# ── main ────────────────────────────────────────────────────────────
main() {
    bold "roksbnkctl E2E test — run-id $RUN_TS"
    log "log: $RUN_LOG"

    preflight

    should_run A && phase_A
    should_run B && phase_B
    should_run C && phase_C
    should_run D && phase_D
    should_run H && phase_H

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "All phases passed. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
}

main "$@"
