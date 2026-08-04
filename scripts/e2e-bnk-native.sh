#!/usr/bin/env bash
# scripts/e2e-bnk-native.sh — gated LIVE-verify driver for the Sprint 27
# terraform-native BNK phase (validator Issue 2). Mirrors the gating,
# redact(), DRY_RUN walk-through, structured-log, and EXIT-trap discipline
# of scripts/e2e-init-var-file.sh.
#
# The terraform-native BNK phase (helm_release wait=true + alekc/kubectl
# kubectl_manifest wait_for) gates the apply on REAL readiness. This driver
# proves the path is CORRECT, that the License wait_for literal is right, and
# that re-deploys are fast (terraform diffs only the changed spec).
#
# ─────────────────────────────────────────────────────────────────────
# THIS IS NOT A CI JOB. Operator-run only, against an EXISTING cluster.
# It is CLUSTER-MUTATING (real bnk up / down). No GitHub workflow runs it.
# ─────────────────────────────────────────────────────────────────────
#
# What it proves (validator Issue 2 acceptance criteria):
#
#   S1 — CORRECTNESS (kubectl mode). `roksbnkctl bnk up -w <ws>` (default
#        kubectl mode) completes a clean apply. Because helm_release wait=true
#        and every kubectl_manifest wait_for gate the apply on REAL readiness,
#        a clean apply IS the readiness assertion. We additionally `kubectl
#        get` the live status to double-check:
#          * CNEInstance .status.conditions[type=Available].status == "True"
#          * License .status.state (captured for S2)
#
#   S2 — LICENSE status.state LIVE-CONFIRM (the spike residual). Capture the
#        actual terminal `.status.state` literal on the licensed cluster:
#          kubectl get licenses.k8s.f5net.com -n f5-utils \
#            -o jsonpath='{.status.state}'
#        and assert it equals the wait_for literal "Verification Complete".
#        If it DIFFERS, the driver prints the real value LOUDLY and FAILS so
#        staff can pin it (or switch the matcher to status.conditions[]).
#
#   S4 — FAST RE-DEPLOY. With BNK up, bump
#        f5_bigip_k8s_manifest_version and re-`up`. Assert the re-up is
#        markedly faster than the cold up (terraform diffs only the changed
#        helm_release version + CNEInstance spec).
#
#   S5 — TEARDOWN. `roksbnkctl bnk down -w <ws>` destroys the CRs cleanly:
#        no orphaned CNEInstance/License, no stuck namespace finalizers on
#        f5-utils / f5-bnk.
#
# Gating: requires IBMCLOUD_API_KEY + an EXISTING cluster the workspace is
# attached to (the cluster phase is NOT provisioned here — too slow/costly;
# this driver only exercises the BNK trial layer on a durable cluster).
# Honors DRY_RUN=1 (renders every step, runs NOTHING, no cloud/cluster calls).
# redact()s the API key out of every echoed command.
#
# Usage:
#   ./scripts/e2e-bnk-native.sh                 # live verify (mutating!)
#   DRY_RUN=1 ./scripts/e2e-bnk-native.sh       # render steps only, no calls
#
# Knobs:
#   WORKSPACE_KUBECTL   workspace already init'd + attached to a cluster,
#                       used for the live run. REQUIRED for a live run.
#   MANIFEST_BUMP_VERSION  the f5_bigip_k8s_manifest_version to bump to for S4.
#                       REQUIRED for S4 (skipped with a warning if unset).
#   LICENSE_NS          license namespace (default f5-utils).
#   LICENSE_NAME        license CR name (default bnk-license).
#   FLO_NS              flo namespace (default f5-bnk).
#   EXPECT_LICENSE_STATE  the wait_for literal to confirm (default
#                       "Verification Complete").
#   DRY_RUN             default 0.
#   LOG_DIR             default /tmp/roksbnkctl-e2e-bnk-native.
#   ROKSBNKCTL          default roksbnkctl.
#   KUBECTL             default kubectl.
#
# Exit codes: 0 = GREEN. Non-zero = first failed assertion, named in the
# error line (correctness miss, wrong License literal, slow re-deploy).

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e-bnk-native}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}
KUBECTL=${KUBECTL:-kubectl}

WORKSPACE_KUBECTL=${WORKSPACE_KUBECTL:-}
MANIFEST_BUMP_VERSION=${MANIFEST_BUMP_VERSION:-}
LICENSE_NS=${LICENSE_NS:-f5-utils}
LICENSE_NAME=${LICENSE_NAME:-bnk-license}
FLO_NS=${FLO_NS:-f5-bnk}
EXPECT_LICENSE_STATE=${EXPECT_LICENSE_STATE:-Verification Complete}

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/bnk-native-$RUN_TS.log"
WORK_DIR="$LOG_DIR/work-$RUN_TS"
mkdir -p "$WORK_DIR"

# ── helpers (mirror scripts/e2e-init-var-file.sh) ───────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }
log()    { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

# Redact the API key value from any string we print. This driver never
# builds a command that contains the key (roksbnkctl reads IBMCLOUD_API_KEY
# from the env itself), but belt-and-braces: if it ever leaks into argv we
# still don't echo it. Identical pattern to e2e-init-var-file.sh's redact().
redact() {
    local s="$*"
    if [[ -n "${IBMCLOUD_API_KEY:-}" ]]; then
        s=${s//"$IBMCLOUD_API_KEY"/<redacted>}
    fi
    printf '%s' "$s"
}

# step <desc> <cmd...> — log + run a command (or skip under DRY_RUN), tee'ing
# output to the run log; on non-zero, abort with the failing step named.
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

# timed_up <desc> <workspace> [extra-args...] — run `bnk up` and echo the
# elapsed whole seconds on stdout (the caller captures it). Logs to RUN_LOG.
# Aborts on a non-zero apply (a clean apply IS the readiness assertion).
timed_up() {
    local desc="$1" ws="$2"; shift 2
    log "→ TIMED $desc — bnk up -w $ws $*"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "  (dry-run; skipping execution; would time the apply)"
        echo 0
        return 0
    fi
    local start end
    start=$(date +%s)
    if ! "$ROKSBNKCTL" bnk up -w "$ws" --auto "$@" </dev/null >>"$RUN_LOG" 2>&1; then
        red "  ✗ $desc — bnk up exited non-zero (a clean apply IS the readiness gate)"
        red "  full log: $RUN_LOG"
        exit 2
    fi
    end=$(date +%s)
    echo $((end - start))
}

# ── self-teardown trap ──────────────────────────────────────────────
# CLUSTER-MUTATING: this driver runs real bnk up. On exit (success OR
# failure) it tears the BNK trial down so a stray/failed run doesn't strand
# the trial layer on the operator's cluster. The cluster phase is left
# intact (we never provisioned it).
TORN_DOWN=0
teardown() {
    local prev_rc=$?
    [[ "$TORN_DOWN" == "1" ]] && return
    TORN_DOWN=1
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ teardown (dry-run): would bnk down -w $WORKSPACE_KUBECTL"
        return
    fi
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    yellow "TEARDOWN — destroying the BNK trial layer (cluster left intact)"
    bold "════════════════════════════════════════════════════════════"
    if [[ -n "$WORKSPACE_KUBECTL" ]]; then
        "$ROKSBNKCTL" bnk down -w "$WORKSPACE_KUBECTL" --auto >>"$RUN_LOG" 2>&1 || true
    fi
    green "  ✓ teardown: bnk down issued (best-effort)"
    if [[ "$prev_rc" != "0" ]]; then
        red "Run FAILED (exit $prev_rc) — see $RUN_LOG"
    fi
}
trap teardown EXIT

# ── preflight / gating ──────────────────────────────────────────────
preflight() {
    bold "preflight"
    if [[ "$DRY_RUN" != "1" ]]; then
        if [[ -z "${IBMCLOUD_API_KEY:-}" ]]; then
            fail "IBMCLOUD_API_KEY not set — this driver is gated on it (live, cluster-mutating)"
        fi
        if [[ -z "$WORKSPACE_KUBECTL" ]]; then
            fail "WORKSPACE_KUBECTL not set — point it at a workspace already attached to an EXISTING cluster"
        fi
        if ! command -v "$ROKSBNKCTL" >/dev/null 2>&1; then
            fail "$ROKSBNKCTL not on PATH (set ROKSBNKCTL=/abs/path/to/binary)"
        fi
        if ! command -v "$KUBECTL" >/dev/null 2>&1; then
            fail "$KUBECTL not on PATH (set KUBECTL=/abs/path); required for the live .status checks"
        fi
    fi
    log "preflight OK — ws=${WORKSPACE_KUBECTL:-<unset>} log=$RUN_LOG"
}

# Confirm redact() is wired before we rely on it (sentinel round-trip, same
# proof as e2e-init-var-file.sh's plant_sentinel).
plant_sentinel() {
    SENTINEL="ROKSBNKCTL_E2E_BNK_SENTINEL_$(head -c 16 /dev/urandom | xxd -p)"
    local saved=${IBMCLOUD_API_KEY:-}
    IBMCLOUD_API_KEY=$SENTINEL
    local after; after=$(redact "cmd --api-key $SENTINEL bnk up …")
    IBMCLOUD_API_KEY=$saved
    if [[ "$after" == *"$SENTINEL"* ]]; then
        fail "redact() did NOT strip the sentinel — driver would leak the API key"
    fi
    log "redact() sentinel check passed"
}

# kget <args...> — kubectl get wrapper that logs + returns stdout.
kget() {
    "$KUBECTL" "$@" 2>>"$RUN_LOG"
}

# ── S1 — correctness (kubectl mode) ─────────────────────────────────
assert_kubectl_status() {
    local ws="$1"
    bold "S1 — live .status double-check (kubectl mode)"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ S1 would: kubectl get cneinstances -n $FLO_NS -o jsonpath conditions[Available]"
        log "→ S1 would: kubectl get licenses.k8s.f5net.com -n $LICENSE_NS"
        return 0
    fi
    # CNEInstance Available=True. The CR name is derived; match the single
    # CNEInstance in the flo namespace.
    local avail
    avail=$(kget get cneinstances.k8s.f5.com -n "$FLO_NS" \
        -o jsonpath='{.items[0].status.conditions[?(@.type=="Available")].status}' || true)
    log "  CNEInstance Available condition = ${avail:-<empty>}"
    if [[ "$avail" != "True" ]]; then
        fail "S1: CNEInstance Available != True (got '${avail:-<empty>}') — apply gate should have caught this; investigate"
    fi
    green "  ✓ S1 CNEInstance .status.conditions[Available] == True"
}

# ── S2 — License status.state live-confirm ──────────────────────────
assert_license_state() {
    bold "S2 — License status.state live-confirm (the spike residual)"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ S2 would: kubectl get licenses.k8s.f5net.com $LICENSE_NAME -n $LICENSE_NS -o jsonpath='{.status.state}'"
        log "    and assert it == \"$EXPECT_LICENSE_STATE\" (else print the real value LOUDLY + FAIL)"
        return 0
    fi
    local state
    state=$(kget get licenses.k8s.f5net.com "$LICENSE_NAME" -n "$LICENSE_NS" \
        -o jsonpath='{.status.state}' || true)
    log "  License .status.state = '${state:-<empty>}'  (expected: '$EXPECT_LICENSE_STATE')"
    if [[ "$state" != "$EXPECT_LICENSE_STATE" ]]; then
        red "════════════════════════════════════════════════════════════"
        red "S2 LICENSE LITERAL MISMATCH — the wait_for literal must be pinned!"
        red "  live  .status.state = '${state:-<empty>}'"
        red "  wait_for value      = '$EXPECT_LICENSE_STATE'"
        red "  ACTION for staff: update terraform/modules/license/modules/"
        red "  license/main.tf kubectl_manifest.bnk_license wait_for.field.value"
        red "  to the live literal above, OR switch to a status.conditions[]"
        red "  matcher if a stable condition type proves better."
        red "════════════════════════════════════════════════════════════"
        fail "S2: License status.state literal mismatch (see above)"
    fi
    green "  ✓ S2 License .status.state == \"$EXPECT_LICENSE_STATE\" (wait_for literal confirmed)"
}

# ── S4 — fast re-deploy (version bump, delta-only) ──────────────────
fast_redeploy() {
    bold "S4 — fast re-deploy (bump f5_bigip_k8s_manifest_version, delta-only)"
    if [[ -z "$MANIFEST_BUMP_VERSION" ]]; then
        yellow "  MANIFEST_BUMP_VERSION unset — SKIPPING S4 (set it to exercise the fast re-deploy)"
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ S4 would: write a bump var-file (f5_bigip_k8s_manifest_version=$MANIFEST_BUMP_VERSION),"
        log "    re-bnk up, assert the re-up is markedly faster than the cold up (delta-only plan)"
        return 0
    fi
    # Ensure kubectl-mode trial is up first (cold), then bump + re-up.
    local t_cold t_bump
    "$ROKSBNKCTL" bnk down -w "$WORKSPACE_KUBECTL" --auto >>"$RUN_LOG" 2>&1 || true
    t_cold=$(timed_up "S4 cold up (baseline)" "$WORKSPACE_KUBECTL")
    log "  S4 cold up baseline: ${t_cold}s"

    local bump_vf="$WORK_DIR/bump.tfvars"
    printf 'f5_bigip_k8s_manifest_version = "%s"\n' "$MANIFEST_BUMP_VERSION" > "$bump_vf"
    log "  wrote bump var-file: $bump_vf (f5_bigip_k8s_manifest_version=$MANIFEST_BUMP_VERSION)"
    t_bump=$(timed_up "S4 version-bump re-up" "$WORKSPACE_KUBECTL" --var-file "$bump_vf")
    log "  S4 version-bump re-up: ${t_bump}s"

    bold "  ──── RE-DEPLOY RESULT ────"
    bold "    cold up      : ${t_cold}s"
    bold "    bump re-up   : ${t_bump}s"
    if [[ "$t_bump" -ge "$t_cold" ]]; then
        fail "S4: version-bump re-up (${t_bump}s) was NOT faster than the cold up (${t_cold}s) — terraform should diff only the changed helm_release version + CNEInstance spec"
    fi
    green "  ✓ S4 version-bump re-up faster than cold (delta-only re-deploy confirmed)"
}

# ── S5 — teardown verify (clean CR removal, no stuck finalizers) ────
teardown_verify() {
    bold "S5 — teardown verify (clean CR removal, no stuck finalizers)"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ S5 would: bnk down -w $WORKSPACE_KUBECTL, then assert CNEInstance/License gone"
        log "    + f5-utils/f5-bnk namespaces not stuck Terminating on finalizers"
        return 0
    fi
    step "S5 bnk down -w $WORKSPACE_KUBECTL" \
        "$ROKSBNKCTL" bnk down -w "$WORKSPACE_KUBECTL" --auto

    # No CNEInstance / License left.
    local cne lic
    cne=$(kget get cneinstances.k8s.f5.com -n "$FLO_NS" -o name 2>/dev/null || true)
    lic=$(kget get licenses.k8s.f5net.com -n "$LICENSE_NS" -o name 2>/dev/null || true)
    if [[ -n "$cne" ]]; then
        fail "S5: CNEInstance still present after bnk down: $cne (orphaned CR)"
    fi
    if [[ -n "$lic" ]]; then
        fail "S5: License still present after bnk down: $lic (orphaned CR)"
    fi
    green "  ✓ S5 CNEInstance + License removed cleanly"

    # Namespaces not stuck Terminating (finalizer-aware delete should clear).
    for ns in "$LICENSE_NS" "$FLO_NS"; do
        local phase
        phase=$(kget get namespace "$ns" -o jsonpath='{.status.phase}' 2>/dev/null || true)
        if [[ "$phase" == "Terminating" ]]; then
            fail "S5: namespace $ns stuck in Terminating (stuck finalizers) after bnk down"
        fi
    done
    green "  ✓ S5 no namespace stuck on finalizers"
    TORN_DOWN=1  # S5 already did the teardown; skip the trap's redundant down
}

# ── main ────────────────────────────────────────────────────────────
main() {
    bold "roksbnkctl terraform-native BNK — LIVE verify — run-id $RUN_TS"
    bold "(CLUSTER-MUTATING — operator-run, NOT a CI job)"
    log "log: $RUN_LOG"
    preflight
    plant_sentinel

    if [[ "$DRY_RUN" == "1" ]]; then
        bold "──── DRY-RUN: rendering the live plan (no cloud/cluster calls) ────"
        assert_kubectl_status "$WORKSPACE_KUBECTL"
        assert_license_state
        fast_redeploy
        teardown_verify
        green "DRY-RUN complete — steps rendered, no cluster mutated, no key printed."
        return 0
    fi

    # S1 — correctness: cold up, then live .status double-check.
    step "S1 bnk up -w $WORKSPACE_KUBECTL (clean apply = readiness)" \
        "$ROKSBNKCTL" bnk up -w "$WORKSPACE_KUBECTL" --auto
    assert_kubectl_status "$WORKSPACE_KUBECTL"

    # S2 — License literal confirm.
    assert_license_state

    # S4 — fast re-deploy (re-ups cold then bump).
    fast_redeploy

    # S5 — teardown verify (final bnk down + assertions).
    teardown_verify

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "GREEN — terraform-native BNK verified live: correct, License"
    green "literal confirmed, fast re-deploy + clean teardown. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
}

main "$@"
