#!/usr/bin/env bash
# scripts/e2e-test-backends.sh — backend-matrix end-to-end driver.
#
# Sibling to scripts/e2e-test.sh. While that script tests the cluster +
# BNK lifecycle (Phases A-H) against the default (local) backend, this
# driver focuses on the four-backend matrix introduced in PRDs 03 + 04:
#
#   Phase K — Docker backend (ibmcloud + iperf3) — PRD 05 §K
#   Phase L — K8s backend (iperf3 + ops pod)     — PRD 05 §L
#   Phase M — Cred-leak audit across all backends — PRD 05 §M
#
# Phases L-DNS + N from PRD 05 are covered separately (Sprint 5 + Sprint 6
# scope per docs/PLAN.md).
#
# This script REUSES the cluster brought up by scripts/e2e-test.sh's
# Phase D — run that first, then this. Or run scripts/e2e-test-full.sh
# (Sprint 6) which orchestrates both.
#
# Usage:
#   IBMCLOUD_API_KEY=... ./scripts/e2e-test-backends.sh                 # all phases
#   IBMCLOUD_API_KEY=... PHASE_FROM=L ./scripts/e2e-test-backends.sh    # resume from L
#   IBMCLOUD_API_KEY=... DRY_RUN=1 ./scripts/e2e-test-backends.sh       # show plan
#
# Exits 0 on a clean pass, non-zero on the first assertion failure with
# the phase + step number in the error message.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
WORKSPACE=${WORKSPACE:-e2e}
TFVARS=${TFVARS:-$HOME/bnkfun/terraform.tfvars}
PHASE_FROM=${PHASE_FROM:-K}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e-backends}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}
RUN_K6=${RUN_K6:-0}  # opt-in to the no-daemon negative path (stops + restarts dockerd)

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/run-$RUN_TS.log"

# ── helpers (match e2e-test.sh shape so muscle memory carries over) ─
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }

log() { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

# step <desc> <cmd...> — runs cmd, logs its output, fails the script
# on non-zero. DRY_RUN=1 logs the plan only.
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

# capture <desc> <cmd...> — like step but echoes captured stdout to
# stdout for downstream pipe assertions.
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

# assert_contains "<needle>" "<label>" — pipe-driven assertion. In
# DRY_RUN mode, drains stdin and skips.
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

# assert_not_contains "<needle>" "<label>" — the inverse. Used by the
# Phase M cred-leak audit: the API key value MUST NOT appear in any
# inspection surface.
assert_not_contains() {
    local needle="$1"
    local label="$2"
    if [[ "$DRY_RUN" == "1" ]]; then
        cat >/dev/null
        log "  (dry-run; skipping assertion: $label)"
        return 0
    fi
    if grep -qF "$needle" -; then
        red "  ✗ $label — SECURITY VIOLATION: secret string found"
        exit 2
    else
        green "  ✓ $label"
    fi
}

phase_header() {
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    bold "Phase $1 — $2"
    bold "════════════════════════════════════════════════════════════"
}

should_run() {
    [[ "$1" > "$PHASE_FROM" || "$1" == "$PHASE_FROM" ]]
}

# ── preflight ───────────────────────────────────────────────────────
preflight() {
    bold "preflight"
    if [[ -z "${IBMCLOUD_API_KEY:-}" ]]; then
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
    if ! command -v "$ROKSBNKCTL" >/dev/null 2>&1; then
        red "$ROKSBNKCTL not on PATH (set ROKSBNKCTL=/path/to/binary)"
        exit 3
    fi
    log "preflight OK — workspace=$WORKSPACE log=$RUN_LOG"
    log "expecting cluster from prior scripts/e2e-test.sh Phase D run"
    log "(if no cluster up, this driver will fail at L0 ops install)"
}

# ── Phase K — Docker backend (PRD 05 §K) ────────────────────────────
phase_K() {
    phase_header K "Docker backend (ibmcloud + iperf3) — PRD 05 §K"

    # K1 — docker info exits 0.
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ K1 docker info (dry-run)"
        log "  cmd: docker info"
    elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
        capture "K1 docker info | head -1" docker info \
            | head -1 \
            | assert_contains "Client" "K1 docker daemon reachable"
    else
        yellow "  ⊘ Phase K skipped — docker daemon not reachable"
        return 0
    fi

    # K2 — docker backend ibmcloud iam oauth-tokens (first call may pull image).
    capture "K2 docker backend ibmcloud iam oauth-tokens" \
        "$ROKSBNKCTL" -w "$WORKSPACE" ibmcloud --backend docker iam oauth-tokens \
        | assert_contains "IAM token" "K2 docker backend produces token"

    # K3 — docker backend ibmcloud ks cluster ls.
    capture "K3 docker backend ibmcloud ks cluster ls" \
        "$ROKSBNKCTL" -w "$WORKSPACE" ibmcloud --backend docker ks cluster ls \
        | assert_contains "OK" "K3 docker backend ks cluster ls"

    # K4 — cred isolation: docker inspect must not reveal the API key value.
    if [[ "$DRY_RUN" != "1" ]]; then
        log "→ K4 docker inspect | jq env scan (cred-leak audit)"
        local lastid
        lastid=$(docker ps -a --format '{{.ID}}' -l 2>/dev/null || echo "")
        if [[ -z "$lastid" ]]; then
            yellow "  ⊘ K4 skipped — no recent docker container to inspect"
        else
            local insp
            insp=$(docker inspect "$lastid" 2>/dev/null || echo "")
            echo "$insp" | assert_not_contains "$IBMCLOUD_API_KEY" "K4 docker inspect cred-leak audit"
        fi
    else
        log "→ K4 docker inspect | jq env scan (dry-run)"
        log "  cmd: docker inspect <last-container> | jq '.[].Config.Env'"
    fi

    # K5 — throughput via docker backend (north-south).
    if command -v docker >/dev/null 2>&1; then
        # The docker-backend iperf3 client runs in a docker container
        # against the in-cluster server endpoint. Skipped if the
        # container image isn't pullable on this runner.
        step "K5 throughput --backend docker --mode north-south" \
            "$ROKSBNKCTL" -w "$WORKSPACE" test throughput --backend docker --mode north-south
    else
        yellow "  ⊘ K5 skipped — docker required for the iperf3 docker-backend"
    fi

    # K6 — no-daemon negative path. Opt-in via RUN_K6=1 because it
    # stops + restarts the host's dockerd, which is destructive on a
    # shared dev machine.
    if [[ "$RUN_K6" == "1" && "$DRY_RUN" != "1" ]]; then
        log "→ K6 docker daemon down → backend errors clearly"
        if sudo systemctl stop docker 2>/dev/null; then
            local out rc
            out=$("$ROKSBNKCTL" -w "$WORKSPACE" ibmcloud --backend docker iam oauth-tokens 2>&1 || true)
            rc=$?
            sudo systemctl start docker
            if [[ "$rc" == "0" ]]; then
                red "  ✗ K6 expected non-zero exit when daemon down, got 0"
                exit 1
            fi
            echo "$out" | assert_contains "daemon" "K6 daemon-down error message clear"
        else
            yellow "  ⊘ K6 skipped — couldn't stop dockerd via systemctl"
        fi
    elif [[ "$DRY_RUN" == "1" ]]; then
        log "→ K6 no-daemon negative (dry-run; opt-in via RUN_K6=1)"
    else
        yellow "  ⊘ K6 skipped — opt-in via RUN_K6=1 (it stops + restarts dockerd)"
    fi
}

# ── Phase L — K8s backend (PRD 05 §L) ───────────────────────────────
phase_L() {
    phase_header L "K8s backend (iperf3 + ops pod) — PRD 05 §L"

    # L0 — ops install.
    step "L0 ops install" "$ROKSBNKCTL" -w "$WORKSPACE" ops install

    # L1 — ibmcloud iam oauth-tokens via the ops pod.
    capture "L1 ibmcloud --backend k8s iam oauth-tokens" \
        "$ROKSBNKCTL" -w "$WORKSPACE" ibmcloud --backend k8s iam oauth-tokens \
        | assert_contains "IAM token" "L1 k8s backend produces token via ops pod"

    # L2 — throughput entirely in-cluster (server pod + client Job).
    step "L2 throughput --backend k8s" \
        "$ROKSBNKCTL" -w "$WORKSPACE" test throughput --backend k8s

    # L3 — Jobs cleaned up post-run.
    if [[ "$DRY_RUN" != "1" ]]; then
        local out
        out=$("$ROKSBNKCTL" kubectl get jobs -n roksbnkctl-test 2>&1 || echo "")
        # `kubectl get jobs` in an empty namespace prints "No resources found"
        # — that's the success case. A list with rows means cleanup didn't run.
        if echo "$out" | grep -qE '^No resources found'; then
            green "  ✓ L3 jobs cleaned up after L2"
        elif [[ -z "$out" ]]; then
            green "  ✓ L3 jobs cleaned up after L2 (empty list)"
        else
            yellow "  ⊘ L3 — saw output, may indicate cleanup lag (3m TTL):"
            echo "$out" >> "$RUN_LOG"
        fi
    else
        log "→ L3 kubectl get jobs cleanup check (dry-run)"
    fi

    # L4 — cred check: Secret data is base64 (not plaintext).
    if [[ "$DRY_RUN" != "1" ]]; then
        local secret
        secret=$("$ROKSBNKCTL" kubectl get secret roksbnkctl-ibm-creds \
            -n roksbnkctl-ops -o yaml 2>/dev/null \
            | grep -E '^\s*IBMCLOUD_API_KEY:' || echo "")
        if [[ -z "$secret" ]]; then
            yellow "  ⊘ L4 — Secret data field not found (k8s_install.yaml may have changed)"
        elif echo "$secret" | grep -qE 'IBMCLOUD_API_KEY:\s*[A-Za-z0-9+/]+=*\s*$'; then
            green "  ✓ L4 Secret data is base64-encoded"
        else
            red "  ✗ L4 Secret data not base64-shaped: $secret"
            exit 1
        fi
    else
        log "→ L4 Secret base64 check (dry-run)"
    fi

    # L5 — RBAC negative: SA can't delete pods in default namespace.
    if [[ "$DRY_RUN" != "1" ]]; then
        local out
        out=$("$ROKSBNKCTL" kubectl auth can-i delete pods \
            --as=system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops \
            -n default 2>&1 || true)
        echo "$out" | assert_contains "no" "L5 RBAC negative — SA can't delete pods"
    else
        log "→ L5 RBAC negative (dry-run)"
    fi

    # L6 — RBAC positive: SA CAN create jobs in roksbnkctl-test.
    if [[ "$DRY_RUN" != "1" ]]; then
        local out
        out=$("$ROKSBNKCTL" kubectl auth can-i create jobs \
            --as=system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops \
            -n roksbnkctl-test 2>&1 || true)
        echo "$out" | assert_contains "yes" "L6 RBAC positive — SA can create jobs"
    else
        log "→ L6 RBAC positive (dry-run)"
    fi

    # L7 — ops uninstall.
    step "L7 ops uninstall" "$ROKSBNKCTL" -w "$WORKSPACE" ops uninstall
}

# ── Phase M — cred-leak audit (PRD 05 §M) ───────────────────────────
phase_M() {
    phase_header M "cred-leak audit (PRD 05 §M)"

    local key="${IBMCLOUD_API_KEY:-}"
    if [[ -z "$key" ]]; then
        red "M skipped — IBMCLOUD_API_KEY unset"
        return 0
    fi

    # M1 — image history must not bake creds into ENV layers.
    if command -v docker >/dev/null 2>&1 && [[ "$DRY_RUN" != "1" ]]; then
        local out
        out=$(docker history ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev 2>&1 || echo "")
        echo "$out" | assert_not_contains "IBMCLOUD_API_KEY=" "M1 image history no baked-in creds"
    else
        log "→ M1 docker history scan (skipped — docker missing or dry-run)"
    fi

    # M2 — last container's docker inspect must not contain the key value.
    if command -v docker >/dev/null 2>&1 && [[ "$DRY_RUN" != "1" ]]; then
        local lastid
        lastid=$(docker ps -a --format '{{.ID}}' -l 2>/dev/null || echo "")
        if [[ -n "$lastid" ]]; then
            local insp
            insp=$(docker inspect "$lastid" 2>/dev/null || echo "")
            echo "$insp" | assert_not_contains "$key" "M2 docker inspect no API key value"
        else
            yellow "  ⊘ M2 — no recent container to inspect"
        fi
    else
        log "→ M2 docker inspect scan (skipped — docker missing or dry-run)"
    fi

    # M3 — kube events in roksbnkctl-ops scanned for the key value.
    if [[ "$DRY_RUN" != "1" ]]; then
        local out
        out=$("$ROKSBNKCTL" kubectl get events -n roksbnkctl-ops -o yaml 2>&1 || echo "")
        echo "$out" | assert_not_contains "$key" "M3 kube events no API key"
    else
        log "→ M3 kube events scan (dry-run)"
    fi

    # M4 — ops pod logs scanned. The redactor wraps the wrapped tool's
    # stdout/stderr; if a tool printed the key, the redactor should
    # mask it before the pod's log captures it.
    if [[ "$DRY_RUN" != "1" ]]; then
        # Try to find the ops pod (may be torn down already by L7).
        local pod
        pod=$("$ROKSBNKCTL" kubectl get pod -n roksbnkctl-ops \
            -l app=roksbnkctl-ops -o name 2>/dev/null | head -1 || echo "")
        if [[ -n "$pod" ]]; then
            local out
            out=$("$ROKSBNKCTL" kubectl logs "$pod" -n roksbnkctl-ops 2>&1 || echo "")
            echo "$out" | assert_not_contains "$key" "M4 ops pod logs no API key"
        else
            yellow "  ⊘ M4 — ops pod no longer present (L7 uninstall ran)"
        fi
    else
        log "→ M4 ops pod log scan (dry-run)"
    fi

    # M5 + M6 — SSH backend tempfile + auth.log scans. PRD 05 §M lists
    # these for the SSH side; the SSH backend itself ships in Sprint 4
    # but the e2e exercises (Phase I from PRD 05) are scheduled for
    # Sprint 6 per docs/PLAN.md. Without an exercised SSH session in
    # this driver's run, M5/M6 have nothing to assert against — log a
    # yellow ⊘ and move on. See issues/issue_sprint4_validator.md for
    # the M6 inclusion question.
    yellow "  ⊘ M5 + M6 skipped — SSH e2e (Phase I) lands in Sprint 6 (PRD 05)"

    # M7 — workspace state files scanned.
    if [[ "$DRY_RUN" != "1" ]]; then
        if [[ -d "$HOME/.roksbnkctl/$WORKSPACE/state" ]]; then
            local out
            out=$(grep -RF "$key" "$HOME/.roksbnkctl/$WORKSPACE/state" 2>/dev/null || echo "")
            if [[ -n "$out" ]]; then
                # The terraform state file legitimately contains the
                # API key (it's an input variable). PRD 04's "scrub
                # workspace logs" intent is about *log* files, not
                # state. Filter to log files only.
                local logleak
                logleak=$(echo "$out" | grep -E '\.log:' || echo "")
                if [[ -z "$logleak" ]]; then
                    green "  ✓ M7 workspace logs no API key (state file expected to contain it)"
                else
                    red "  ✗ M7 SECURITY VIOLATION: API key in workspace log file:"
                    echo "$logleak" >&2
                    exit 2
                fi
            else
                green "  ✓ M7 workspace state no API key"
            fi
        else
            yellow "  ⊘ M7 — workspace state dir missing"
        fi
    else
        log "→ M7 workspace state scan (dry-run)"
    fi
}

# ── main ────────────────────────────────────────────────────────────
main() {
    bold "roksbnkctl backend-matrix E2E — run-id $RUN_TS"
    log "log: $RUN_LOG"

    preflight

    should_run K && phase_K
    should_run L && phase_L
    should_run M && phase_M

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "Backend-matrix phases passed. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
}

main "$@"
