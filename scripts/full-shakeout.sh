#!/usr/bin/env bash
# scripts/full-shakeout.sh — single-button pre-flight for a deep, full
# product shake-out.
#
# Runs every test tier that costs nothing — local unit/lint/cred-audit
# (Tier 0) and DRY_RUN plan-walkthroughs of every live e2e driver
# (Tier 1) — collecting a pass/fail/skip summary. It then PRINTS the
# exact Tier 2 + Tier 3 cloud commands (real IBM Cloud spend, hours of
# wall time) for you to launch by hand with your key.
#
# It never spends cloud money and never needs IBMCLOUD_API_KEY: the
# live tiers are printed, not executed. The DRY_RUN drivers redact the
# key and make no cloud calls.
#
# Usage:
#   ./scripts/full-shakeout.sh                 # run Tier 0 + Tier 1, print Tier 2+3
#   SKIP_LOCAL=1 ./scripts/full-shakeout.sh    # skip Tier 0 (just dry-run the drivers)
#   SKIP_DRYRUN=1 ./scripts/full-shakeout.sh   # skip Tier 1
#   WS=e2e SSH_TARGET=jumphost ./scripts/full-shakeout.sh   # parameterize the printed cloud cmds
#
# Exit code: 0 only if every NON-SKIPPED step passed. A skipped step
# (missing Docker, no kubeconfig) does not fail the run — it is reported
# as ⊘ so you know that surface is unverified.

set -u
set -o pipefail

# ── locate repo root ────────────────────────────────────────────────
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." &>/dev/null && pwd)
cd "$REPO_ROOT"

# ── config / knobs ──────────────────────────────────────────────────
SKIP_LOCAL=${SKIP_LOCAL:-0}
SKIP_DRYRUN=${SKIP_DRYRUN:-0}
WS=${WS:-e2e}                       # workspace name for the printed cloud cmds
SSH_TARGET=${SSH_TARGET:-jumphost}  # ROKSBNKCTL_E2E_SSH_TARGET for the printed cmds
STEP_TIMEOUT=${STEP_TIMEOUT:-900}   # per-step wall cap (s); guards a hung dry-run

RUN_TS=$(date +%Y%m%d-%H%M%S)
RESULTS_DIR=${RESULTS_DIR:-$REPO_ROOT/.shakeout/$RUN_TS}
mkdir -p "$RESULTS_DIR"

# Several drivers default TFVARS=./terraform.tfvars and preflight-check
# that the file EXISTS even in DRY_RUN (it is read for structure only and
# never printed). Resolve the first one that actually exists so the
# dry-run tier doesn't false-fail on a missing path. Order: caller's
# $TFVARS → ./terraform.tfvars → ~/bnkfun/terraform.tfvars → the
# committed structure-only example.
resolve_tfvars() {
    local c
    for c in "${TFVARS:-}" "$REPO_ROOT/terraform.tfvars" \
             "$HOME/bnkfun/terraform.tfvars" \
             "$REPO_ROOT/terraform/terraform.tfvars.example"; do
        [[ -n "$c" && -f "$c" ]] && { printf '%s' "$c"; return 0; }
    done
    return 1
}
DRYRUN_TFVARS=$(resolve_tfvars || true)

# ── colors / helpers ────────────────────────────────────────────────
if [[ -t 1 ]]; then
    RED=$'\033[31m'; GRN=$'\033[32m'; YEL=$'\033[33m'; BLU=$'\033[34m'; BLD=$'\033[1m'; RST=$'\033[0m'
else
    RED=; GRN=; YEL=; BLU=; BLD=; RST=
fi

hdr()  { printf '\n%s━━ %s ━━%s\n' "$BLD" "$*" "$RST"; }
info() { printf '%s•%s %s\n' "$BLU" "$RST" "$*"; }

# Parallel result arrays (bash 3.2-safe — no associative arrays).
RES_NAME=(); RES_STATUS=(); RES_NOTE=()

record() { RES_NAME+=("$1"); RES_STATUS+=("$2"); RES_NOTE+=("${3:-}"); }

# run_step <name> <cmd...> — execute, capture to a log, record PASS/FAIL.
run_step() {
    local name="$1"; shift
    local log="$RESULTS_DIR/${name}.log"
    printf '%s→%s %-26s ' "$BLU" "$RST" "$name"
    local rc=0
    if command -v timeout >/dev/null 2>&1; then
        timeout "$STEP_TIMEOUT" "$@" >"$log" 2>&1 || rc=$?
    else
        "$@" >"$log" 2>&1 || rc=$?
    fi
    if [[ $rc -eq 0 ]]; then
        printf '%sPASS%s\n' "$GRN" "$RST"; record "$name" PASS
    elif [[ $rc -eq 124 ]]; then
        printf '%sFAIL%s (timeout %ss)\n' "$RED" "$RST" "$STEP_TIMEOUT"; record "$name" FAIL "timeout"
    else
        printf '%sFAIL%s (rc=%s) — tail:\n' "$RED" "$RST" "$rc"
        tail -n 8 "$log" | sed 's/^/      /'
        record "$name" FAIL "rc=$rc"
    fi
}

# skip_step <name> <reason> — record a clean skip (does not fail the run).
skip_step() {
    printf '%s→%s %-26s %s⊘ SKIP%s — %s\n' "$BLU" "$RST" "$1" "$YEL" "$RST" "$2"
    record "$1" SKIP "$2"
}

info "results + per-step logs → $RESULTS_DIR"
if [[ -n "$DRYRUN_TFVARS" ]]; then
    info "dry-run TFVARS (structure only) → $DRYRUN_TFVARS"
else
    info "${YEL}no tfvars file found — TFVARS-dependent dry-runs may preflight-fail${RST}"
fi

# ── prereq detection ────────────────────────────────────────────────
have_docker=0
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then have_docker=1; fi
have_kube=0
if [[ -n "${KUBECONFIG:-}" && -f "${KUBECONFIG%%:*}" ]] || [[ -f "$HOME/.kube/config" ]]; then have_kube=1; fi

# ════════════════════════════════════════════════════════════════════
# TIER 0 — local, no cloud, no key
# ════════════════════════════════════════════════════════════════════
if [[ "$SKIP_LOCAL" == "1" ]]; then
    hdr "TIER 0 — local build + tests  (SKIPPED via SKIP_LOCAL=1)"
else
    hdr "TIER 0 — local build + tests  (no cloud, no key)"
    run_step build           make build
    run_step vet             make vet
    run_step lint            make lint
    run_step test-short      make test-short
    run_step test-cred-audit make test-cred-audit   # stop-ship if this fails

    if [[ "$have_docker" == "1" ]]; then
        run_step test-integration make test-integration
    else
        skip_step test-integration "Docker daemon not reachable"
    fi

    if [[ "$have_kube" == "1" ]]; then
        run_step test-live make test-live
    else
        skip_step test-live "no \$KUBECONFIG / ~/.kube/config"
    fi
fi

# ════════════════════════════════════════════════════════════════════
# TIER 1 — DRY_RUN plan-walkthroughs of every live driver (no cloud)
# ════════════════════════════════════════════════════════════════════
# Every e2e driver honors DRY_RUN=1 EXCEPT e2e-airgap-mirror.sh, which
# runs live and requires a workspace arg — it is intentionally absent
# here and appears in the printed Tier 3 block instead.
DRYRUN_DRIVERS=(
    e2e-test-full.sh
    e2e-test.sh
    e2e-test-backends.sh
    e2e-phase-handoff.sh
    e2e-cos-bucket-get.sh
    e2e-init-var-file.sh
    e2e-init-prefix.sh
    e2e-bnk-native.sh
    e2e-three-phase.sh
)

if [[ "$SKIP_DRYRUN" == "1" ]]; then
    hdr "TIER 1 — DRY_RUN driver walkthroughs  (SKIPPED via SKIP_DRYRUN=1)"
else
    hdr "TIER 1 — DRY_RUN driver walkthroughs  (no cloud, no key)"
    for drv in "${DRYRUN_DRIVERS[@]}"; do
        path="$SCRIPT_DIR/$drv"
        if [[ -x "$path" ]]; then
            run_step "dry:${drv%.sh}" env DRY_RUN=1 TFVARS="$DRYRUN_TFVARS" "$path"
        else
            skip_step "dry:${drv%.sh}" "not found / not executable"
        fi
    done
fi

# ════════════════════════════════════════════════════════════════════
# SUMMARY
# ════════════════════════════════════════════════════════════════════
hdr "SUMMARY"
pass=0; fail=0; skip=0; failed_names=()
for i in "${!RES_NAME[@]}"; do
    case "${RES_STATUS[$i]}" in
        PASS) mark="${GRN}✓${RST}"; ((pass++)) ;;
        SKIP) mark="${YEL}⊘${RST}"; ((skip++)) ;;
        FAIL) mark="${RED}✗${RST}"; ((fail++)); failed_names+=("${RES_NAME[$i]}") ;;
    esac
    printf '  %s %-26s %s\n' "$mark" "${RES_NAME[$i]}" "${RES_NOTE[$i]}"
done
printf '\n  %sPASS %d  FAIL %d  SKIP %d%s\n' "$BLD" "$pass" "$fail" "$skip" "$RST"
[[ $fail -gt 0 ]] && printf '  %sfailing:%s %s\n' "$RED" "$RST" "${failed_names[*]}"

# ════════════════════════════════════════════════════════════════════
# TIER 2 + TIER 3 — the live cloud pass (PRINTED, not run)
# ════════════════════════════════════════════════════════════════════
cat <<EOF

${BLD}━━ TIER 2 — canonical full cloud pass  (~4–6h, ~\$8–13, REAL SPEND) ━━${RST}

The deep full test. Brings one cluster up, runs A–H baseline + the
I/K/L/L-DNS/M/N backend matrix against it, then tears itself down:

  ${GRN}IBMCLOUD_API_KEY=... \\
  ROKSBNKCTL_E2E_SSH_TARGET=${SSH_TARGET} \\
  ./scripts/e2e-test-full.sh --teardown${RST}

  • Drop --teardown to KEEP the cluster up so the Tier-3 "reuse" drivers
    below can run against it (saves a second cluster apply).
  • Resume after a failure: prepend PHASE_FROM=<letter>  (A–H baseline,
    I/K/L/M/N backends).

${BLD}━━ TIER 3 — per-sprint gated drivers  (run in this order) ━━${RST}

Features added AFTER e2e-test-full.sh was written. "self" = creates +
tears down its own cluster; "reuse" = runs against a standing cluster
(point them at the Tier-2 cluster if you kept it up).

  1. ${GRN}IBMCLOUD_API_KEY=... ./scripts/e2e-phase-handoff.sh${RST}      # S16  self
  2. ${GRN}IBMCLOUD_API_KEY=... ./scripts/e2e-cos-bucket-get.sh${RST}     # S18  COS only
  3. ${GRN}IBMCLOUD_API_KEY=... ./scripts/e2e-init-var-file.sh${RST}      # S19  cheap/plan
  4. ${GRN}IBMCLOUD_API_KEY=... ./scripts/e2e-init-prefix.sh${RST}        # S26  cheap/plan
  5. ${GRN}IBMCLOUD_API_KEY=... ./scripts/e2e-bnk-native.sh -w ${WS}${RST}   # S27  reuse
  6. ${GRN}IBMCLOUD_API_KEY=... ./scripts/e2e-three-phase.sh${RST}        # S28  self
  7. ${GRN}IBMCLOUD_API_KEY=... ./scripts/e2e-airgap-mirror.sh ${WS}${RST}    # S29  reuse  ← current branch

Cost-saver: run the ${BLD}reuse${RST} drivers (5, 7) against ONE held cluster, then
the ${BLD}self${RST} drivers (1, 6) separately.

${BLD}━━ TIER 4 — manual human-in-the-loop checks ━━${RST}

  See docs/E2E_TEST.md §"Per-release checklist": manual GSLB divergence
  validation (LD8) and the docker-backend full TF lifecycle.
EOF

[[ $fail -eq 0 ]] && exit 0 || exit 1
