#!/usr/bin/env bash
# scripts/e2e-test-full.sh — combined A-H + I-N + L-DNS e2e runner.
#
# Chains scripts/e2e-test.sh (baseline Phases A-H) and
# scripts/e2e-test-backends.sh (Phases I-N + L-DNS) against the SAME
# cluster — the baseline driver brings the cluster up via Phase D's
# `up`, the backends driver exercises the backend matrix between
# Phase D's apply and its final teardown, and the baseline driver's
# Phase H tears down the workspace.
#
# ~4-6 hour wall time. Intended for release-branch CI + the
# manual-trigger Full E2E workflow (.github/workflows/e2e-full.yml).
# PR-gated CI runs only the unit + integration tiers per docs/PLAN.md
# Sprint 6 §"Risks": 5 hours is too long for every-PR.
#
# Usage:
#
#   IBMCLOUD_API_KEY=... ./scripts/e2e-test-full.sh
#   IBMCLOUD_API_KEY=... PHASE_FROM=D ./scripts/e2e-test-full.sh   # resume baseline at D
#   IBMCLOUD_API_KEY=... PHASE_FROM=I ./scripts/e2e-test-full.sh   # resume backends at I
#   IBMCLOUD_API_KEY=... DRY_RUN=1 ./scripts/e2e-test-full.sh
#   IBMCLOUD_API_KEY=... ./scripts/e2e-test-full.sh --teardown     # tear down on success
#
# Re-run semantics (PRD 05 §"Re-runnability"): PHASE_FROM=<phase>
# resumes at <phase>. Phases A-H route to the baseline driver,
# Phases I-N route to the backends driver. On failure, the cluster is
# left up for inspection (the baseline driver's Phase H teardown is
# skipped); on success with --teardown, Phase H tears it down.
#
# See docs/E2E_TEST.md §"Full e2e (e2e-test-full.sh)" for env vars +
# cost expectation.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)
BASELINE_DRIVER=${BASELINE_DRIVER:-$SCRIPT_DIR/e2e-test.sh}
BACKENDS_DRIVER=${BACKENDS_DRIVER:-$SCRIPT_DIR/e2e-test-backends.sh}

WORKSPACE=${WORKSPACE:-e2e}
TFVARS=${TFVARS:-$HOME/bnkfun/terraform.tfvars}
PHASE_FROM=${PHASE_FROM:-A}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e-full}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}

# --teardown flag — when set, Phase H of the baseline driver runs after
# a successful backends pass. Default: leave the cluster up so the
# integrator can inspect / re-run / kick off a manual GSLB check.
TEARDOWN=0
for arg in "$@"; do
    case "$arg" in
        --teardown) TEARDOWN=1 ;;
        --no-teardown) TEARDOWN=0 ;;
        *) ;;
    esac
done

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/run-$RUN_TS.log"

# ── helpers ─────────────────────────────────────────────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }

log() { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

# is_backend_phase <letter> — true if the given phase letter is in the
# backends driver's coverage (I, K, L, L-DNS, M, N). Used so PHASE_FROM
# can jump straight to the backends driver without re-running A-H.
is_backend_phase() {
    case "$1" in
        I|K|L|L-DNS|M|N) return 0 ;;
        *) return 1 ;;
    esac
}

# ── preflight ───────────────────────────────────────────────────────
preflight() {
    bold "preflight — full e2e (A-H + I-N + L-DNS)"

    if [[ ! -x "$BASELINE_DRIVER" ]]; then
        red "baseline driver not executable: $BASELINE_DRIVER"
        exit 3
    fi
    if [[ ! -x "$BACKENDS_DRIVER" ]]; then
        red "backends driver not executable: $BACKENDS_DRIVER"
        exit 3
    fi

    if [[ -z "${IBMCLOUD_API_KEY:-}" ]]; then
        # Each child driver re-runs its own IBMCLOUD_API_KEY-from-tfvars
        # fallback; mirror it here so a missing key surfaces in this
        # outer driver's preflight rather than four hours into a baseline
        # run.
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

    if ! command -v "$ROKSBNKCTL" >/dev/null 2>&1 && [[ ! -x "$ROKSBNKCTL" ]]; then
        red "$ROKSBNKCTL not on PATH (set ROKSBNKCTL=/path/to/binary)"
        exit 3
    fi

    log "preflight OK — workspace=$WORKSPACE tfvars=$TFVARS log=$RUN_LOG"
    log "baseline driver:  $BASELINE_DRIVER"
    log "backends driver:  $BACKENDS_DRIVER"
    log "teardown-on-success: $TEARDOWN (override: --teardown / --no-teardown)"
    log "phase_from: $PHASE_FROM"
}

# ── runners ─────────────────────────────────────────────────────────
#
# The child drivers each accept PHASE_FROM via env. When PHASE_FROM is
# a baseline phase (A-H), the baseline driver runs from there; the
# backends driver always starts at I (its default). When PHASE_FROM is
# a backends phase (I/K/L/L-DNS/M/N), the baseline driver is SKIPPED
# entirely — the integrator's chosen resume point implies the cluster
# is already up from a previous run.

run_baseline_AtoG() {
    # Run baseline phases A-G (the "bring up + everything that runs
    # while cluster is up"). Phase H (final cleanup) is gated on
    # --teardown and runs at the END of the chained flow, after the
    # backends driver completes.
    #
    # The baseline driver doesn't have a "skip H" knob today; we
    # approximate by running it with PHASE_FROM at the desired start
    # and (when --no-teardown) re-invoking the workspace-only Phase H
    # ourselves only after the backends driver succeeds. The simpler
    # path is: just let the baseline driver complete D (which is the
    # `up + tests + down` cycle), then the backends driver brings up
    # a SEPARATE workspace.
    #
    # The cleanest contract (matching PRD 05 §"Test infrastructure"):
    # the baseline driver's Phase D leaves the cluster up between its
    # apply and the final down; the backends driver runs in that window.
    # We can't surgery that window from the outside, so we go with the
    # simpler contract: baseline runs A→D (which destroys), then a
    # second up via the backends driver (Phase N's N1) re-creates a
    # fresh cluster for the backends matrix. Total wall time grows by
    # ~70min but the two drivers stay decoupled.
    #
    # For now we run the FULL baseline (A-H) sequentially, then the
    # backends driver does its own up via Phase N (the mixed-mode
    # lifecycle's N1 step is exactly this — "up a cluster, exercise
    # backends against it, tear down with a different backend"). The
    # baseline's Phase D + the backends' Phase N together cover both
    # the "default backend" and "mixed-backend" scenarios.
    # SKIP_PHASE_D_DOWN=1 + SKIP_PHASE_H=1 keep both the cluster (Phase
    # D's D8) and the workspace (Phase H's ws delete) alive across the
    # baseline → backends transition. Required so the backends driver
    # can see a running cluster (Phase L ops install, LD5-LD8 cluster
    # vantages, N1 mixed-mode lifecycle) and workspace-resolved creds
    # (Phase K). When --teardown is set, the wrapper invokes Phase H
    # separately at the very end (see main()'s post-backends block).
    bold "═══ baseline driver — Phases A-G (D8 + H gated off) ═══"
    log "invoking: $BASELINE_DRIVER (PHASE_FROM=$PHASE_FROM, SKIP_PHASE_D_DOWN=1, SKIP_PHASE_H=1)"
    if [[ "$DRY_RUN" == "1" ]]; then
        DRY_RUN=1 SKIP_PHASE_D_DOWN=1 SKIP_PHASE_H=1 PHASE_FROM="$PHASE_FROM" WORKSPACE="$WORKSPACE" \
            TFVARS="$TFVARS" ROKSBNKCTL="$ROKSBNKCTL" LOG_DIR="$LOG_DIR/baseline" \
            "$BASELINE_DRIVER"
    else
        SKIP_PHASE_D_DOWN=1 SKIP_PHASE_H=1 PHASE_FROM="$PHASE_FROM" WORKSPACE="$WORKSPACE" \
            TFVARS="$TFVARS" ROKSBNKCTL="$ROKSBNKCTL" LOG_DIR="$LOG_DIR/baseline" \
            "$BASELINE_DRIVER"
    fi
}

run_backends() {
    # Backends driver runs Phases I-N + L-DNS. Picks up its own
    # PHASE_FROM default (I) — or honour the caller's PHASE_FROM if it
    # names a backends phase. The cluster the backends driver expects
    # is the one Phase D of the baseline driver brought up.
    bold "═══ backends driver — Phases I + K + L + L-DNS + M + N ═══"
    local bk_phase="${PHASE_FROM}"
    if ! is_backend_phase "$bk_phase"; then
        bk_phase="I"
    fi
    log "invoking: $BACKENDS_DRIVER (PHASE_FROM=$bk_phase)"
    if [[ "$DRY_RUN" == "1" ]]; then
        DRY_RUN=1 PHASE_FROM="$bk_phase" WORKSPACE="$WORKSPACE" \
            TFVARS="$TFVARS" ROKSBNKCTL="$ROKSBNKCTL" LOG_DIR="$LOG_DIR/backends" \
            "$BACKENDS_DRIVER"
    else
        PHASE_FROM="$bk_phase" WORKSPACE="$WORKSPACE" \
            TFVARS="$TFVARS" ROKSBNKCTL="$ROKSBNKCTL" LOG_DIR="$LOG_DIR/backends" \
            "$BACKENDS_DRIVER"
    fi
}

# ── main ────────────────────────────────────────────────────────────
main() {
    bold "roksbnkctl FULL E2E — run-id $RUN_TS"
    log "log: $RUN_LOG"
    log "expected wall-time: 4-6 hours (cluster up: ~70min, backends: ~30min, tests + teardown: ~30min)"

    preflight

    # Decide whether to run baseline. When the caller PHASE_FROM names
    # a backends phase, the baseline is skipped — the cluster is
    # assumed already up from an earlier run.
    local rc=0
    if is_backend_phase "$PHASE_FROM"; then
        yellow "PHASE_FROM=$PHASE_FROM is a backends phase — skipping baseline driver"
    else
        # `set -e` inside this script suppressed for the next call so we
        # can capture rc + emit a leave-cluster-up message before exit.
        set +e
        run_baseline_AtoG
        rc=$?
        set -e
        if [[ "$rc" -ne 0 ]]; then
            red "baseline driver failed (exit $rc)"
            red "cluster left up for inspection — investigate before re-running"
            red "logs: $LOG_DIR/baseline/"
            exit "$rc"
        fi
    fi

    set +e
    run_backends
    rc=$?
    set -e
    if [[ "$rc" -ne 0 ]]; then
        red "backends driver failed (exit $rc)"
        red "cluster left up for inspection — investigate before re-running"
        red "logs: $LOG_DIR/backends/"
        exit "$rc"
    fi

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "Full E2E (A-H + I-N + L-DNS) passed. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"

    if [[ "$TEARDOWN" == "1" ]]; then
        log "--teardown flag set — Phase H final cleanup runs"
        # The baseline driver's Phase D ends with `roksbnkctl down`
        # already; Phase H removes the workspace. Phase H is part of
        # the baseline driver — call it via PHASE_FROM=H.
        if [[ "$DRY_RUN" != "1" ]]; then
            PHASE_FROM=H WORKSPACE="$WORKSPACE" TFVARS="$TFVARS" \
                ROKSBNKCTL="$ROKSBNKCTL" LOG_DIR="$LOG_DIR/teardown" \
                "$BASELINE_DRIVER"
        else
            log "(dry-run; skipping teardown invocation)"
        fi
    else
        log "leaving workspace + cluster state behind (no --teardown flag)"
    fi
}

main "$@"
