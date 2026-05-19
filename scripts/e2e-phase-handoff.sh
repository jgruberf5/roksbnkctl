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

# down_phase runs one phase-down verb and tolerates a clean no-op
# (nothing-to-destroy / no-such-state) so a partially-applied or
# already-clean workspace does not look like a teardown failure. Real
# destroy failures (live infra not removed) return non-zero. $1 = human
# label; $2... = the roksbnkctl verb + args.
down_phase() {
    local label=$1; shift
    log "→ teardown: $label — $* --auto -w $WORKSPACE --var-file <tfvars>"
    local out rc
    out=$("$@" --auto -w "$WORKSPACE" --var-file "$TFVARS" 2>&1); rc=$?
    printf '%s\n' "$out" >> "$RUN_LOG"
    if [[ $rc -eq 0 ]]; then
        green "  ✓ $label: destroyed (or already clean)"
        return 0
    fi
    # A phase with nothing to tear down is expected on a run that failed
    # before that phase applied — treat as a no-op, not a failure.
    if grep -qiE 'nothing to destroy|no .*state|not initialised|no BNK trial state' <<<"$out"; then
        yellow "  • $label: no-op (nothing provisioned for this phase)"
        return 0
    fi
    red "  ✗ $label: destroy FAILED — infra for this phase MAY still be live."
    return 1
}

# residual_check fails LOUDLY if any canada-* VPC / canada-roks-tgw /
# canada-roks cluster is still present after both phase-downs, so a
# silent strand can never masquerade as a clean teardown. Best-effort:
# if the IBM CLI is unavailable we say so rather than green-lighting.
residual_check() {
    if ! command -v ibmcloud >/dev/null 2>&1; then
        yellow "  • residual check skipped: ibmcloud CLI not on PATH — verify the"
        yellow "    account by hand for leftover canada-* VPC / canada-roks-tgw / canada-roks."
        return 0
    fi
    local leftover=0
    if ibmcloud is vpcs --output json 2>/dev/null | grep -qE '"name": *"canada-[^"]*"'; then
        red "  ✗ residual: a canada-* VPC still exists"
        leftover=1
    fi
    if ibmcloud tg gateways 2>/dev/null | grep -q 'canada-roks-tgw'; then
        red "  ✗ residual: transit gateway canada-roks-tgw still exists"
        leftover=1
    fi
    if ibmcloud oc cluster ls 2>/dev/null | grep -q 'canada-roks'; then
        red "  ✗ residual: ROKS cluster canada-roks still exists"
        leftover=1
    fi
    if [[ $leftover -ne 0 ]]; then
        red "  ✗ RESIDUAL INFRA DETECTED — destroy did not fully complete."
        red "  Inspect the IBM Cloud console and remove leftover canada-* VPC /"
        red "  canada-roks-tgw / canada-roks cluster manually."
        return 1
    fi
    green "  ✓ residual check: no canada-* VPC / canada-roks-tgw / canada-roks cluster remains"
    return 0
}

teardown() {
    local prev_rc=$?
    [[ "$TORN_DOWN" == "1" ]] && return
    TORN_DOWN=1
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ teardown (dry-run): $ROKSBNKCTL down (trial phase) THEN $ROKSBNKCTL cluster down (cluster phase), then a canada-* residual check"
        return
    fi
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    yellow "TEARDOWN — destroying BOTH phases of workspace $WORKSPACE so no billable infra is stranded"
    bold "════════════════════════════════════════════════════════════"
    # `roksbnkctl down` destroys only the trial/bnk phase (state/). The
    # cluster phase (state-cluster/: the ROKS cluster, both VPCs, the
    # transit gateway — the bulk of the billing) is a SEPARATE
    # `roksbnkctl cluster down`. The Issue 2 verify loop progresses past
    # the cluster phase, so without the second down a failed run strands
    # a running ROKS cluster + networking (Issue 4). Tear down in reverse
    # creation order: trial first, then cluster.
    local td_rc=0
    down_phase "trial/bnk phase down" "$ROKSBNKCTL" down            || td_rc=1
    down_phase "cluster phase down"   "$ROKSBNKCTL" cluster down     || td_rc=1
    residual_check                                                   || td_rc=1
    if [[ $td_rc -eq 0 ]]; then
        green "  ✓ teardown complete — both phases destroyed, no canada-* residue"
    else
        red "  ✗ TEARDOWN INCOMPLETE — infra MAY still be live."
        red "  Manually run:  $ROKSBNKCTL down --auto -w $WORKSPACE --var-file $TFVARS"
        red "             and: $ROKSBNKCTL cluster down --auto -w $WORKSPACE --var-file $TFVARS"
        red "  then check the IBM Cloud console for a leftover ROKS cluster /"
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
SECOND_OVERRIDE="$WS_DIR/state/bnk-phase-override.tfvars" # forced 2nd-phase override (Issue 2 round-2)
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
        log "→ A3 forced bnk-phase override turns cluster-shared creation OFF (create_roks_cluster=false + use_existing_cluster_vpc=true): $SECOND_OVERRIDE"
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

    # A3 — the forced bnk-phase override must exist and turn cluster-shared
    # creation OFF. Round-2 architecture: the second phase no longer manages
    # cluster-shared infra at all (create_roks_cluster=false), so the toggle
    # now lives in bnk-phase-override.tfvars, not the rendered terraform.tfvars.
    [[ -f "$SECOND_OVERRIDE" ]] || fail "A3 forced bnk-phase override missing: $SECOND_OVERRIDE (handoff not wired)"
    if ! grep -q 'create_roks_cluster[[:space:]]*=[[:space:]]*false' "$SECOND_OVERRIDE"; then
        fail "A3 bnk-phase-override.tfvars missing create_roks_cluster = false — 2nd phase still manages the cluster-shared network"
    fi
    if ! grep -q 'use_existing_cluster_vpc[[:space:]]*=[[:space:]]*true' "$SECOND_OVERRIDE"; then
        fail "A3 bnk-phase-override.tfvars missing use_existing_cluster_vpc = true — handoff toggle not wired"
    fi
    green "  ✓ A3 bnk-phase override turns cluster-shared creation off (create_roks_cluster=false + use_existing_cluster_vpc=true)"

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
