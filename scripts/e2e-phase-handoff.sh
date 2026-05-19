#!/usr/bin/env bash
# scripts/e2e-phase-handoff.sh — gated live-verify driver for validator
# Issue 2 (Sprint 16 follow-up): the `roksbnkctl up` phase-handoff
# regression where the second (bnk/testing) phase re-creates the cluster
# VPC / transit gateway / client VPC the cluster phase already made, and
# IBM Cloud rejects the duplicate names.
#
#   "Provided Name (<ws>-vpc) is not unique"
#   "A gateway with the same name already exists."
#   "Provided Name (<ws>-j-vpc) is not unique"
#
# ─────────────────────────────────────────────────────────────────────
# THIS IS NOT A CI JOB. Operator-run only, via `!`.
# ─────────────────────────────────────────────────────────────────────
#
#   * REAL CLOUD SPEND. A pass provisions a 3-zone ROKS cluster, a
#     transit gateway, COS, and the BNK workload, then tears it all down.
#     Budget ≈ $5-8 and ≈ 70+ minutes wall time.
#   * Opt-in. Nothing automatic runs this — no GitHub workflow, no
#     `workflow_dispatch`. Per the Sprint 16 follow-up integrator
#     decision (README decision 2) e2e is gated live-verify, not CI.
#   * Requires `IBMCLOUD_API_KEY` in the environment. This driver does
#     NOT echo, log, or read the key out of the tfvars file into output.
#     The project ./terraform.tfvars holds a live key — its contents are
#     never printed and the file is never committed.
#   * Self-tears-down on EXIT so a failed run does not strand billable
#     infrastructure.
#
# Usage:
#   IBMCLOUD_API_KEY=... ./scripts/e2e-phase-handoff.sh        # live verify
#   DRY_RUN=1            ./scripts/e2e-phase-handoff.sh        # plan only, no cloud
#
# Knobs (same names/shape as scripts/e2e-test.sh):
#   TFVARS      default ./terraform.tfvars   (structure only; never echoed)
#   WORKSPACE   default e2e-handoff
#   DRY_RUN     default 0
#   LOG_DIR     default /tmp/roksbnkctl-e2e-handoff
#   ROKSBNKCTL  default roksbnkctl (set to an absolute path if not on PATH)
#
# Exit codes: 0 = GREEN (handoff fixed). Non-zero = first failed
# assertion, with the failing check named in the error line.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
TFVARS=${TFVARS:-./terraform.tfvars}
WORKSPACE=${WORKSPACE:-e2e-handoff}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e-handoff}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/handoff-$RUN_TS.log"

# ── helpers (mirror scripts/e2e-test.sh) ────────────────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }
log()    { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

# Redact the API key value from any string we are about to print. Belt
# and braces — this driver never builds a command containing the key,
# but if the environment leaks one into argv we still don't echo it.
redact() {
    local s="$*"
    if [[ -n "${IBMCLOUD_API_KEY:-}" ]]; then
        s=${s//"$IBMCLOUD_API_KEY"/<redacted>}
    fi
    printf '%s' "$s"
}

# Run a command, stream + capture, fail the script on non-zero. The
# echoed command line is redacted.
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
# Best-effort and LOUD. A failed up must not strand a ROKS cluster +
# transit gateway billing for hours. We always attempt `down`; we do
# not let its exit status mask the real failure.
TORN_DOWN=0
teardown() {
    local prev_rc=$?
    [[ "$TORN_DOWN" == "1" ]] && return
    TORN_DOWN=1
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ teardown (dry-run): $ROKSBNKCTL down --auto -w $WORKSPACE --var-file $TFVARS"
        return
    fi
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    yellow "TEARDOWN — destroying workspace $WORKSPACE so no billable infra is stranded"
    bold "════════════════════════════════════════════════════════════"
    if "$ROKSBNKCTL" down --auto -w "$WORKSPACE" --var-file "$TFVARS" 2>&1 | tee -a "$RUN_LOG"; then
        green "  ✓ teardown complete"
    else
        red "  ✗ TEARDOWN FAILED — infra MAY still be live."
        red "  Manually run:  $ROKSBNKCTL down --auto -w $WORKSPACE --var-file $TFVARS"
        red "  and/or check the IBM Cloud console for a leftover ROKS cluster /"
        red "  transit gateway / client VPC in this account."
    fi
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
        if [[ -z "${IBMCLOUD_API_KEY:-}" ]]; then
            # Deliberately do NOT scrape the key out of $TFVARS. Live
            # runs must pass it explicitly in the environment so it
            # never transits this driver's stdout/stderr/argv.
            fail "IBMCLOUD_API_KEY is unset. Export it in the environment (do NOT rely on the tfvars echo) and re-run."
        fi
        if ! command -v "$ROKSBNKCTL" >/dev/null 2>&1; then
            fail "$ROKSBNKCTL not on PATH (set ROKSBNKCTL=/abs/path/to/binary)"
        fi
    fi
    log "preflight OK — workspace=$WORKSPACE tfvars=$TFVARS (contents not printed) log=$RUN_LOG"
}

# ── derived paths ───────────────────────────────────────────────────
WS_DIR="$HOME/.roksbnkctl/$WORKSPACE"
CLUSTER_STATE="$WS_DIR/state-cluster/terraform.tfstate"   # cluster phase
SECOND_STATE="$WS_DIR/state/terraform.tfstate"            # bnk/testing phase
SECOND_TFVARS="$WS_DIR/state/terraform.tfvars"            # rendered 2nd-phase tfvars
CLUSTER_OUTPUTS="$WS_DIR/cluster-outputs.json"

# ── the reproduction ────────────────────────────────────────────────
main() {
    bold "roksbnkctl phase-handoff live verify — run-id $RUN_TS"
    bold "(validator Issue 2 — Sprint 16 follow-up — NOT a CI job)"
    log "log: $RUN_LOG"
    preflight

    # Clean slate so we exercise the real cluster-phase → second-phase
    # handoff from scratch (the exact path Issue 2 fails on). Skipped in
    # dry-run so the walkthrough stays side-effect-free.
    if [[ "$DRY_RUN" != "1" ]]; then
        log "→ pre-clean: remove any stale $WORKSPACE workspace"
        "$ROKSBNKCTL" ws delete "$WORKSPACE" --force >/dev/null 2>&1 || true
        rm -rf "$WS_DIR" 2>/dev/null || true
    else
        log "→ pre-clean (dry-run): ws delete $WORKSPACE --force; rm -rf $WS_DIR"
    fi

    step "S1 init -w $WORKSPACE" "$ROKSBNKCTL" init -w "$WORKSPACE"

    # S2 — the real reproduction. `up` runs the cluster phase, then the
    # bnk/testing phase, end to end. Pre-fix this is exactly where IBM
    # Cloud rejects the duplicate VPC / transit gateway / client VPC.
    step "S2 up (cluster phase THEN bnk/testing phase — the Issue 2 path)" \
        "$ROKSBNKCTL" up --auto -w "$WORKSPACE" --var-file "$TFVARS"

    bold "──── assertions: the second phase REUSES, it does not RE-CREATE ────"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ A1 cluster phase tracked the VPC/TG: grep ibm_is_vpc.cluster_vpc in $CLUSTER_STATE"
        log "→ A2 second-phase state does NOT manage a duplicate cluster_vpc/transit_gateway/client_vpc: $SECOND_STATE"
        log "→ A3 rendered second-phase tfvars carries use_existing_cluster_vpc = true: $SECOND_TFVARS"
        log "→ A4 run log free of 'not unique' / 'already exists'"
        green "DRY-RUN complete — steps rendered, no cloud calls, no key printed."
        return 0
    fi

    # A1 — sanity: the cluster phase really did create + track the VPC
    # and transit gateway in its own state. If not, the reproduction
    # premise is wrong and a later "no duplicate" pass would be vacuous.
    [[ -f "$CLUSTER_STATE" ]] || fail "A1 cluster-phase state missing: $CLUSTER_STATE"
    if ! grep -q 'ibm_is_vpc.*cluster_vpc' "$CLUSTER_STATE"; then
        fail "A1 cluster phase did not track a cluster_vpc — reproduction premise broken"
    fi
    green "  ✓ A1 cluster phase created + tracked the cluster VPC / transit gateway"

    # cluster-outputs.json must carry the handoff vpc_id the second
    # phase consumes.
    [[ -f "$CLUSTER_OUTPUTS" ]] || fail "A1b cluster-outputs.json missing: $CLUSTER_OUTPUTS"
    if ! grep -q '"vpc_id"' "$CLUSTER_OUTPUTS"; then
        fail "A1b cluster-outputs.json has no vpc_id — nothing for the second phase to reuse"
    fi
    green "  ✓ A1b cluster-outputs.json carries vpc_id for handoff"

    # A2 — THE FIX ASSERTION. The second-phase state must NOT contain a
    # freshly *managed* duplicate of the cluster-phase resources. If the
    # bug is unfixed, `up` already failed at S2; if a partial fix lets
    # `up` succeed but the second phase still manages its own copies,
    # this catches it.
    [[ -f "$SECOND_STATE" ]] || fail "A2 second-phase state missing: $SECOND_STATE"
    for res in \
        "module.roks_cluster.module.cluster.ibm_is_vpc.cluster_vpc" \
        "ibm_tg_gateway.transit_gateway" \
        "module.testing.ibm_is_vpc.client_vpc"
    do
        # A managed resource instance appears in tfstate under
        # "mode": "managed" with the address; a `data` reuse lookup is
        # "mode": "data" and is fine. Flag a managed-mode match.
        if grep -F "$res" "$SECOND_STATE" \
            | grep -q 'cluster_vpc\|transit_gateway\|client_vpc'; then
            # Tighten: only fail if it is managed (not a data lookup).
            if grep -B2 -A2 -F "$res" "$SECOND_STATE" | grep -q '"mode": "managed"'; then
                fail "A2 second phase MANAGES a duplicate $res — phase handoff still re-creates (Issue 2 NOT fixed)"
            fi
        fi
    done
    green "  ✓ A2 second-phase state reuses (no managed duplicate cluster_vpc / transit_gateway / client_vpc)"

    # A3 — the rendered second-phase tfvars must carry the reuse toggle.
    [[ -f "$SECOND_TFVARS" ]] || fail "A3 rendered second-phase tfvars missing: $SECOND_TFVARS"
    if ! grep -q 'use_existing_cluster_vpc[[:space:]]*=[[:space:]]*true' "$SECOND_TFVARS"; then
        fail "A3 second-phase terraform.tfvars missing use_existing_cluster_vpc = true — handoff toggle not wired"
    fi
    green "  ✓ A3 second-phase tfvars carries use_existing_cluster_vpc = true"

    # A4 — the run log must be free of the IBM Cloud duplicate-name
    # rejections that are Issue 2's fingerprint.
    if grep -qiE 'is not unique|already exists' "$RUN_LOG"; then
        fail "A4 run log contains a duplicate-name rejection ('not unique' / 'already exists') — Issue 2 reproduced"
    fi
    green "  ✓ A4 no duplicate-name rejection in the run log"

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "GREEN — phase handoff verified: second phase reuses the"
    green "cluster-phase VPC / transit gateway / client VPC. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
    green "(teardown runs next via the EXIT trap)"
}

main "$@"
