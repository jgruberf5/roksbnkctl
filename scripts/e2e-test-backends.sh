#!/usr/bin/env bash
# scripts/e2e-test-backends.sh — backend-matrix end-to-end driver.
#
# The AWS-flavoured test verbs (connectivity, dns, throughput) and the
# K8s + SSH execution backends have landed in dry-run / mocked form —
# the offline regression surface is exercised by the `test-dryrun` job
# in `.github/workflows/ci.yml`. The live-AWS exercise of these phases
# still gates on the operator-run spike.
#
#     Phase I     (SSH backend)              → dry-run via CI; spike for live apply
#     Phase K     (Docker backend)           → dry-run via CI; spike for live apply
#     Phase L     (K8s backend / iperf3)     → dry-run via CI; spike for live apply
#     Phase L-DNS (DNS probe + GSLB compare) → dry-run via CI; spike for live apply
#     Phase M     (cred-leak audit)          → CI implements; spike validates live
#     Phase N     (mixed-mode lifecycle)     → CI implements; spike validates live
#
#   Invocation surface preserved (PHASE_FROM, DRY_RUN, env vars) so
#   downstream consumers (scripts/e2e-test-full.sh, the babysit loop)
#   keep parsing the script. Phase bodies still emit skip banners —
#   the markers below point operators at the right artefact
#   (the CI `test-dryrun` job or the live spike) for each surface.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
WORKSPACE=${WORKSPACE:-e2e}
PHASE_FROM=${PHASE_FROM:-I}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/awsbnkctl-e2e-backends}
AWSBNKCTL=${AWSBNKCTL:-awsbnkctl}

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/run-$RUN_TS.log"

# ── helpers ─────────────────────────────────────────────────────────
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }

phase_header() {
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    bold "Phase $1 — $2"
    bold "════════════════════════════════════════════════════════════"
}

skip_phase() {
    local letter="$1"
    local desc="$2"
    local marker="$3"
    phase_header "$letter" "$desc"
    yellow "  ⊘ Phase $letter skipped — $marker"
}

should_run() {
    [[ "$1" > "$PHASE_FROM" || "$1" == "$PHASE_FROM" ]]
}

# ── phases (dry-run tier covered by CI's test-dryrun job; live tier
#   gates on the operator-run spike) ──────────────────────────────────
phase_I()     { skip_phase I     "SSH backend (awsbnkctl --backend ssh)"            "dry-run via CI; live apply gates on spike"; }
phase_K()     { skip_phase K     "Docker backend (awsbnkctl --backend docker)"      "dry-run via CI; live apply gates on spike"; }
phase_L()     { skip_phase L     "K8s backend (iperf3 + ops pod via --backend k8s)" "dry-run via CI; live apply gates on spike"; }
phase_L_DNS() { skip_phase L-DNS "AWS Route 53 GSLB DNS probe + cross-vantage compare (miekg/dns)" "dry-run via CI; live apply gates on spike"; }
phase_M()     { skip_phase M     "cred-leak audit across all backends"              "CI implements; live exercise in spike"; }
phase_N()     { skip_phase N     "mixed-mode lifecycle (backends share state)"      "CI implements; live exercise in spike"; }

# ── main ────────────────────────────────────────────────────────────
main() {
    bold "awsbnkctl backends E2E — run-id $RUN_TS"
    echo "[$(date +%H:%M:%S)] log: $RUN_LOG" | tee -a "$RUN_LOG" >&2
    echo "[$(date +%H:%M:%S)] Status: backend + DNS phases at dry-run tier; live apply gates on operator-run spike." \
        | tee -a "$RUN_LOG" >&2

    should_run I     && phase_I
    should_run K     && phase_K
    should_run L     && phase_L
    should_run L     && phase_L_DNS
    should_run M     && phase_M
    should_run N     && phase_N

    echo "" >&2
    yellow "════════════════════════════════════════════════════════════"
    yellow "Status: backend matrix (I, K, L) + L-DNS + audit (M, N)"
    yellow "  at dry-run / mocked tier — see CI test-dryrun job."
    yellow "Live-apply tier gates on the operator-run spike"
    yellow "  (docs/prd/07-EKS-CLUSTER-SRIOV.md § \"Spike protocol\")."
    yellow "════════════════════════════════════════════════════════════"
}

main "$@"
