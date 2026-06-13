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
# By DEFAULT it never spends cloud money and never needs IBMCLOUD_API_KEY:
# the live tiers are printed, not executed, and the DRY_RUN drivers redact
# the key and make no cloud calls.
#
# Opt in to TIER L (--live) and it ALSO executes a full live lifecycle
# against the named workspace — up (Cluster+BNK+Testing) → gateway up →
# connectivity/DNS probes → down (+ opt-in reuse drivers) — which DOES create
# and destroy real, billable IBM Cloud infra. --live requires a workspace and
# IBMCLOUD_API_KEY, and runs only behind a green Tier 0 + Tier 1.
#
# Usage:
#   ./scripts/full-shakeout.sh [<workspace>]   # run Tier 0 + Tier 1, print Tier 2+3
#   ./scripts/full-shakeout.sh -w <workspace>  # same, workspace as a flag
#   IBMCLOUD_API_KEY=… ./scripts/full-shakeout.sh --live <ws>         # + TIER L: real lifecycle (SPENDS)
#   IBMCLOUD_API_KEY=… ./scripts/full-shakeout.sh --live --keep <ws>  # … but leave the cluster UP
#   SKIP_LOCAL=1 ./scripts/full-shakeout.sh    # skip Tier 0 (just dry-run the drivers)
#   SKIP_DRYRUN=1 ./scripts/full-shakeout.sh   # skip Tier 1
#   SSH_TARGET=jumphost ./scripts/full-shakeout.sh <ws>    # also set the printed SSH target
#
# Workspace mode: pass an INITIALIZED roksbnkctl workspace name and the
# shakeout targets THAT workspace instead of scanning for a loose
# ./terraform.tfvars. It derives the dry-run TFVARS from the workspace's
# own inputs (~/.roksbnkctl/<ws>/state/terraform.tfvars → the committed
# terraform.tfvars.user → `roksbnkctl -w <ws> tfvars`), threads
# WORKSPACE=<ws> into the dry-run drivers, and stamps <ws> into the
# printed Tier 2/3 cloud commands. So a fresh workspace can be shaken out
# end-to-end:
#   roksbnkctl init -w demo --var-file my.tfvars   # initialize a workspace
#   ./scripts/full-shakeout.sh demo                # shake it out
# With NO workspace arg, behavior is unchanged (WS=$WS env, default e2e).
#
# TIER L (--live / LIVE=1): runs ONLY after Tier 0 + Tier 1 are green (it
# refuses to spend on a broken tree). Steps, each recorded in the summary:
#   live:up → live:gateway → live:test-connectivity → live:test-dns →
#   live:perf-matrix → live:down  (+ opt-in live:reuse-* via LIVE_REUSE=1).
# (No pre-`up` `plan` gate: `roksbnkctl plan` targets the BNK/trial phase,
# which attaches to an EXISTING cluster, so it cannot pass before `up`
# creates one. `up` is the resumable, self-healing from-scratch path and
# validates inputs itself before the expensive cluster create.)
# The connectivity/DNS probes SKIP cleanly when the workspace has no hosts
# configured. The perf matrix (iperf3 L4 + h2load L7) has no one-shot command
# yet (`roksbnkctl test throughput` is a v1.x stub), so it is SKIPPED unless
# you pass PERF_MATRIX_CMD="<cmd>". The specialized reuse drivers
# (e2e-bnk-native, e2e-airgap-mirror) need extra setup and are OFF unless
# LIVE_REUSE=1. Teardown (live:down) runs `gateway down` then `down` (a full
# `down` is refused while the Gateway phase has resources); pass --keep /
# KEEP=1 to hold the cluster. An EXIT trap tears down a partially-applied
# workspace if the run is interrupted.
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
WS=${WS:-e2e}                       # workspace name (env fallback; CLI arg wins)
SSH_TARGET=${SSH_TARGET:-jumphost}  # ROKSBNKCTL_E2E_SSH_TARGET for the printed cmds
STEP_TIMEOUT=${STEP_TIMEOUT:-900}   # per-step wall cap (s); guards a hung dry-run
LIVE=${LIVE:-0}                     # 1 = run TIER L (real lifecycle; SPENDS)
KEEP=${KEEP:-0}                     # 1 = in --live, leave the cluster UP (skip live:down)
LIVE_TIMEOUT=${LIVE_TIMEOUT:-5400}  # per-step wall cap for the live tier (90m; up is slow)
LIVE_REUSE=${LIVE_REUSE:-0}         # 1 = also run the specialized reuse drivers (need extra setup)
LIVE_API_KEY=""                     # the live key, stashed out of the env (set at the gate)

# ── CLI args: an optional workspace name (positional or -w/--workspace) ─
# Workspace mode (see header). Without it, WS keeps its env/default value
# and the legacy ./terraform.tfvars scan is used. --live/--keep opt into
# TIER L (see header).
WS_FROM_CLI=0
print_usage() {
    sed -n '/^# Usage:/,/^# Exit code:/p' "${BASH_SOURCE[0]}" | sed 's/^#\{0,1\} \{0,1\}//'
}
while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help) print_usage; exit 0 ;;
        -L|--live) LIVE=1; shift ;;
        -k|--keep) KEEP=1; shift ;;
        -w|--workspace)
            [[ $# -ge 2 ]] || { echo "error: $1 needs a workspace name" >&2; exit 2; }
            WS="$2"; WS_FROM_CLI=1; shift 2 ;;
        --workspace=*) WS="${1#*=}"; WS_FROM_CLI=1; shift ;;
        -*) echo "error: unknown flag '$1' (see --help)" >&2; exit 2 ;;
        *)
            if [[ "$WS_FROM_CLI" == "1" ]]; then
                echo "error: unexpected extra argument '$1' (one workspace only)" >&2; exit 2
            fi
            WS="$1"; WS_FROM_CLI=1; shift ;;
    esac
done

# ── TIER L gate: validate the live prerequisites up front, before any work,
# so a misconfigured --live fails fast instead of after a full Tier 0/1.
if [[ "$LIVE" == "1" ]]; then
    live_err=0
    if [[ "$WS_FROM_CLI" != "1" ]]; then
        echo "error: --live requires a workspace (positional or -w <ws>)" >&2; live_err=1
    fi
    if [[ -z "${IBMCLOUD_API_KEY:-}" ]]; then
        echo "error: --live requires IBMCLOUD_API_KEY in the environment (it creates real infra)" >&2; live_err=1
    fi
    _rbhome="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
    if [[ "$WS_FROM_CLI" == "1" && ! -f "$_rbhome/$WS/config.yaml" ]]; then
        echo "error: --live needs an initialized workspace; '$WS' not found under $_rbhome" >&2
        echo "       run: roksbnkctl init -w $WS …   (then re-run with --live)" >&2
        live_err=1
    fi
    [[ $live_err -eq 0 ]] || exit 2

    # Stash the key in a private var, then STRIP it from the environment so
    # Tier 0/1 (the "no cloud, no key" local tiers) stay hermetic. An ambient
    # IBMCLOUD_API_KEY makes some unit tests reach the real IBM Cloud API
    # (e.g. init's resource-group verification → "test-rg not found"), which
    # would false-fail the pre-live gate. The live tier re-injects the key
    # into each step explicitly via env.
    LIVE_API_KEY="$IBMCLOUD_API_KEY"
    unset IBMCLOUD_API_KEY
fi

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

# Locate a usable roksbnkctl binary (built one preferred), for the
# `tfvars`-emit fallback below. Empty if none is on hand.
locate_roksbnkctl() {
    local c
    for c in "$REPO_ROOT/bin/roksbnkctl" "$REPO_ROOT/roksbnkctl"; do
        [[ -x "$c" ]] && { printf '%s' "$c"; return 0; }
    done
    command -v roksbnkctl 2>/dev/null && return 0
    return 1
}

# Workspace mode: derive a structure-only TFVARS from an initialized
# workspace's own inputs. Order: the rendered BNK/cluster tfvars (the
# workspace's real, applied-shaped values) → the committed user override
# → as a last resort, emit the workspace's pinned upstream example via
# `roksbnkctl -w <ws> tfvars`. Returns non-zero (caller falls back to the
# legacy scan) when <ws> isn't an initialized workspace or yields nothing.
resolve_ws_tfvars() {
    local ws="$1"
    local rbhome="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
    local wsdir="$rbhome/$ws"
    [[ -f "$wsdir/config.yaml" ]] || return 1   # not an initialized workspace
    local c
    for c in "$wsdir/state/terraform.tfvars" \
             "$wsdir/state-cluster/terraform.tfvars" \
             "$wsdir/terraform.tfvars.user"; do
        [[ -f "$c" ]] && { printf '%s' "$c"; return 0; }
    done
    # Nothing rendered yet (init'd but never planned/applied) — emit the
    # workspace's pinned upstream example into the results dir.
    local bin out
    if bin=$(locate_roksbnkctl); then
        out="$RESULTS_DIR/${ws}.tfvars"
        if "$bin" -w "$ws" tfvars -o "$out" --force >/dev/null 2>&1 && [[ -s "$out" ]]; then
            printf '%s' "$out"; return 0
        fi
    fi
    return 1
}

if [[ "$WS_FROM_CLI" == "1" ]] && DRYRUN_TFVARS=$(resolve_ws_tfvars "$WS"); then
    :   # workspace-derived tfvars in hand
else
    DRYRUN_TFVARS=$(resolve_tfvars || true)
fi

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

# run_step [-t <timeout>] <name> <cmd...> — execute, capture to a log, record
# PASS/FAIL. stdin is /dev/null so a stray prompt (e.g. a missing --auto) can
# never block the run. Returns the step's real exit code so callers can branch.
run_step() {
    local timeout_s="$STEP_TIMEOUT"
    if [[ "$1" == "-t" ]]; then timeout_s="$2"; shift 2; fi
    local name="$1"; shift
    local log="$RESULTS_DIR/${name}.log"
    printf '%s→%s %-26s ' "$BLU" "$RST" "$name"
    local rc=0
    if command -v timeout >/dev/null 2>&1; then
        timeout "$timeout_s" "$@" </dev/null >"$log" 2>&1 || rc=$?
    else
        "$@" </dev/null >"$log" 2>&1 || rc=$?
    fi
    if [[ $rc -eq 0 ]]; then
        printf '%sPASS%s\n' "$GRN" "$RST"; record "$name" PASS
    elif [[ $rc -eq 124 ]]; then
        printf '%sFAIL%s (timeout %ss)\n' "$RED" "$RST" "$timeout_s"; record "$name" FAIL "timeout"
    else
        printf '%sFAIL%s (rc=%s) — tail:\n' "$RED" "$RST" "$rc"
        tail -n 8 "$log" | sed 's/^/      /'
        record "$name" FAIL "rc=$rc"
    fi
    return $rc
}

# skip_step <name> <reason> — record a clean skip (does not fail the run).
skip_step() {
    printf '%s→%s %-26s %s⊘ SKIP%s — %s\n' "$BLU" "$RST" "$1" "$YEL" "$RST" "$2"
    record "$1" SKIP "$2"
}

# last_failed — true if the most recently recorded step is a FAIL.
last_failed() {
    local n=${#RES_STATUS[@]}
    [[ $n -gt 0 && "${RES_STATUS[$((n-1))]}" == "FAIL" ]]
}

# run_step_skippable <skip-regex> <name> <cmd...> — like run_step, but if the
# step FAILS and its log matches <skip-regex>, downgrade the recorded FAIL to a
# SKIP. For "expected prerequisite absent" outcomes (e.g. a probe with no hosts
# configured on a fresh workspace) that should not fail the run.
run_step_skippable() {
    local re="$1"; shift
    local name="$1"
    run_step "$@"
    if last_failed && grep -qiE "$re" "$RESULTS_DIR/${name}.log" 2>/dev/null; then
        local n=$((${#RES_STATUS[@]} - 1))
        RES_STATUS[$n]=SKIP
        RES_NOTE[$n]="prerequisite absent (matched: $re)"
        printf '  %s↳ downgraded FAIL → ⊘ SKIP%s (%s)\n' "$YEL" "$RST" "${RES_NOTE[$n]}"
    fi
}

info "results + per-step logs → $RESULTS_DIR"
if [[ "$WS_FROM_CLI" == "1" ]]; then
    rbhome="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
    if [[ -f "$rbhome/$WS/config.yaml" ]]; then
        info "workspace → ${BLD}$WS${RST}  ($rbhome/$WS)"
    else
        info "${YEL}workspace '$WS' not initialized under $rbhome — run 'roksbnkctl init -w $WS …' first${RST}"
    fi
else
    info "workspace (printed cloud cmds) → ${BLD}$WS${RST}  [no -w/positional arg; env/default]"
fi
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

# ── TIER L state + safety teardown ──────────────────────────────────
# UP_DONE is armed the moment `up` runs (even a PARTIAL apply can leave
# billable infra), so the safety trap always cleans up unless --keep.
UP_DONE=0; DOWN_DONE=0

# live_safety_teardown — EXIT trap (armed only in --live). Best-effort
# `down` if the run is interrupted (Ctrl-C, a hard error) after `up` but
# before the recorded live:down step. Never spends if nothing was applied.
live_safety_teardown() {
    [[ "$UP_DONE" == "1" && "$DOWN_DONE" != "1" && "$KEEP" != "1" ]] || return 0
    local rb
    rb=$(locate_roksbnkctl) || return 0
    printf '\n%s[safety]%s tearing down workspace %s (interrupted before clean teardown)…\n' "$YEL" "$RST" "$WS" >&2
    # gateway down THEN down — a full `down` is refused while the Gateway phase
    # has resources.
    env IBMCLOUD_API_KEY="${LIVE_API_KEY:-}" bash -c \
        '"$1" -w "$2" gateway down --auto; "$1" -w "$2" down --auto' _ "$rb" "$WS" \
        </dev/null >>"$RESULTS_DIR/live:down.log" 2>&1 || true
    DOWN_DONE=1
}

# run_live_tier — the opt-in live lifecycle against $WS. Each phase is a
# recorded step (so it lands in the SUMMARY). plan/up are critical: a
# failure there aborts the rest but STILL tears down. gateway + the probes
# + reuse drivers are best-effort. live:down always runs last (unless --keep).
run_live_tier() {
    hdr "TIER L — LIVE full lifecycle vs workspace '$WS'  (REAL IBM Cloud SPEND)"
    local rb
    rb=$(locate_roksbnkctl) || { skip_step "live:lifecycle" "no roksbnkctl binary on hand"; return; }
    info "binary → $rb   teardown → $([[ "$KEEP" == "1" ]] && echo 'KEEP (cluster left up)' || echo 'down --auto on exit')"
    # The live key was stripped from the environment at the gate (so Tier 0/1
    # stayed hermetic); re-inject it into each live step via this prefix.
    local KENV=(env "IBMCLOUD_API_KEY=$LIVE_API_KEY")
    local abort=0

    # 1. up — Cluster + BNK + Testing (the parallel up). First spend + the
    #    critical step. NO pre-`up` `plan` gate: `roksbnkctl plan` targets the
    #    BNK/trial phase, which attaches to an EXISTING cluster (via the
    #    generated bnk-phase-override.tfvars, create_roks_cluster=false), so it
    #    cannot succeed before a cluster exists. `up` is the resumable,
    #    self-healing path — it creates the cluster, regenerates the phase
    #    handoff overrides from the fresh cluster-outputs.json, then deploys
    #    BNK + Testing — and validates inputs before the expensive create.
    #    Arms teardown (even a partial apply can leave billable infra).
    run_step -t "$LIVE_TIMEOUT" "live:up" "${KENV[@]}" "$rb" -w "$WS" up --auto || true
    UP_DONE=1
    if last_failed; then
        info "${RED}live:up failed — tearing down any partial infra.${RST}"
        abort=1
    fi

    # 2. gateway + probes + reuse drivers — only if up succeeded.
    if [[ $abort -eq 0 ]]; then
        run_step -t "$LIVE_TIMEOUT" "live:gateway" "${KENV[@]}" "$rb" -w "$WS" gateway up --auto || true
        last_failed && info "${YEL}live:gateway failed — continuing to validation + teardown.${RST}"

        # Validation against the live cluster (workspace-scoped; uses the
        # workspace's own kubeconfig, not the ambient ~/.kube/config). These
        # probe configured hosts; a fresh workspace has none, so a "no hosts
        # configured" outcome is a clean SKIP, not a failure.
        run_step_skippable 'no hosts configured' "live:test-connectivity" "${KENV[@]}" "$rb" -w "$WS" test connectivity
        run_step_skippable 'no hosts configured' "live:test-dns"          "${KENV[@]}" "$rb" -w "$WS" test dns

        # Perf matrix (iperf3 L4 + h2load L7): no one-shot command yet
        # (`test throughput` is a v1.x stub), so run an operator-supplied
        # PERF_MATRIX_CMD if given, else skip with a pointer.
        if [[ -n "${PERF_MATRIX_CMD:-}" ]]; then
            run_step -t "$LIVE_TIMEOUT" "live:perf-matrix" "${KENV[@]}" bash -c "$PERF_MATRIX_CMD" || true
        else
            skip_step "live:perf-matrix" "no one-shot perf cmd (test throughput is a v1.x stub) — set PERF_MATRIX_CMD=… to run iperf3/h2load"
        fi

        # Specialized reuse drivers — OPT-IN (LIVE_REUSE=1). They need setup the
        # vanilla lifecycle doesn't do: e2e-bnk-native wants WORKSPACE_KUBECTL
        # pointed at an attached workspace; e2e-airgap-mirror needs a registry
        # mirror already PUSHED to ICR. Off by default so the core lifecycle
        # (up → gateway → down) can pass on a plain fresh workspace.
        if [[ "$LIVE_REUSE" == "1" ]]; then
            if [[ -x "$SCRIPT_DIR/e2e-bnk-native.sh" ]]; then
                run_step -t "$LIVE_TIMEOUT" "live:reuse-bnk-native" \
                    "${KENV[@]}" WORKSPACE_KUBECTL="$WS" "$SCRIPT_DIR/e2e-bnk-native.sh" -w "$WS" || true
            fi
            if [[ -x "$SCRIPT_DIR/e2e-airgap-mirror.sh" ]]; then
                run_step -t "$LIVE_TIMEOUT" "live:airgap-mirror" \
                    "${KENV[@]}" "$SCRIPT_DIR/e2e-airgap-mirror.sh" "$WS" || true
            fi
        else
            skip_step "live:reuse" "specialized reuse drivers (bnk-native, airgap-mirror) need extra setup — set LIVE_REUSE=1 to run them"
        fi
    fi

    # 3. teardown — always (if up created anything), unless --keep. MUST be
    #    `gateway down` THEN `down`: roksbnkctl refuses a full `down` while the
    #    Gateway phase has resources (its CRs live in the BNK namespace and
    #    would block the BNK teardown).
    if [[ "$UP_DONE" == "1" ]]; then
        if [[ "$KEEP" == "1" ]]; then
            skip_step "live:down" "--keep set — cluster left UP; tear down later: roksbnkctl -w $WS gateway down --auto && roksbnkctl -w $WS down --auto"
        else
            run_step -t "$LIVE_TIMEOUT" "live:down" "${KENV[@]}" bash -c \
                '"$1" -w "$2" gateway down --auto; "$1" -w "$2" down --auto' _ "$rb" "$WS" || true
            last_failed || DOWN_DONE=1
        fi
    fi
}

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
            if [[ "$WS_FROM_CLI" == "1" ]]; then
                # Workspace mode: target the named workspace. DRY_RUN means
                # the drivers only render — no mutation of a real workspace.
                run_step "dry:${drv%.sh}" env DRY_RUN=1 TFVARS="$DRYRUN_TFVARS" WORKSPACE="$WS" "$path"
            else
                run_step "dry:${drv%.sh}" env DRY_RUN=1 TFVARS="$DRYRUN_TFVARS" "$path"
            fi
        else
            skip_step "dry:${drv%.sh}" "not found / not executable"
        fi
    done
fi

# ════════════════════════════════════════════════════════════════════
# TIER L — opt-in LIVE lifecycle (real spend); only behind a green tree
# ════════════════════════════════════════════════════════════════════
if [[ "$LIVE" == "1" ]]; then
    prelive_fail=0
    for s in "${RES_STATUS[@]}"; do [[ "$s" == "FAIL" ]] && prelive_fail=$((prelive_fail+1)); done
    if [[ $prelive_fail -gt 0 ]]; then
        hdr "TIER L — SKIPPED ($prelive_fail Tier 0/1 failure(s) — refusing to spend)"
        skip_step "live:lifecycle" "$prelive_fail pre-live failure(s); fix Tier 0/1 before a live run"
    else
        trap live_safety_teardown EXIT
        run_live_tier
    fi
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
# When --live already executed the lifecycle above, the manual checklist is
# noise — collapse it to a one-line pointer.
if [[ "$LIVE" == "1" ]]; then
cat <<EOF

${BLD}━━ TIER 2/3 — executed live above (TIER L) ━━${RST}

The live lifecycle ran against workspace ${BLD}${WS}${RST}. The standalone
e2e drivers below were exercised as live:* steps (reuse drivers) or are
covered by the lifecycle; see the SUMMARY. Re-run without --live to print
the full manual cloud checklist.
EOF
else
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
fi

[[ $fail -eq 0 ]] && exit 0 || exit 1
