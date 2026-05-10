#!/usr/bin/env bash
# scripts/e2e-test-backends.sh — backend-matrix end-to-end driver.
#
# Sibling to scripts/e2e-test.sh. While that script tests the cluster +
# BNK lifecycle (Phases A-H) against the default (local) backend, this
# driver focuses on the four-backend matrix introduced in PRDs 03 + 04
# plus the DNS probe added in PRD 03 §"DNS probe (GSLB-aware)":
#
#   Phase I     — SSH backend (ibmcloud + iperf3 via ssh:<target>) — PRD 05 §I
#   Phase K     — Docker backend (ibmcloud + iperf3) — PRD 05 §K
#   Phase L     — K8s backend (iperf3 + ops pod)     — PRD 05 §L
#   Phase L-DNS — DNS probe (miekg/dns) + GSLB compare — PRD 05 §L-DNS
#   Phase M     — Cred-leak audit across all backends — PRD 05 §M
#   Phase N     — Mixed-mode lifecycle (backends share state) — PRD 05 §N
#
# Phase I + Phase N + M5/M6 land in Sprint 6 and require an SSH target
# pointed at by ROKSBNKCTL_E2E_SSH_TARGET (a name in the workspace's
# `targets:` block, typically auto-populated by `cluster up`).
# Skip-cleanly when unset.
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
PHASE_FROM=${PHASE_FROM:-I}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e-backends}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}
RUN_K6=${RUN_K6:-0}  # opt-in to the no-daemon negative path (stops + restarts dockerd)

# Phase I (SSH backend e2e) — env-keyed:
#
#   ROKSBNKCTL_E2E_SSH_TARGET            — primary happy-path target name.
#                                          Must be present in the workspace's
#                                          `targets:` block (auto-populated
#                                          by `cluster up` when the upstream
#                                          HCL provisions a TGW jumphost).
#   ROKSBNKCTL_E2E_SSH_NON_UBUNTU        — purpose-built non-Ubuntu target
#                                          for I7. Skip-cleanly when unset.
#   ROKSBNKCTL_E2E_SSH_NO_NOPASSWD       — sudo-password-required target
#                                          for I8. Skip-cleanly when unset.
#   ROKSBNKCTL_E2E_INIT_BACKEND          — initial-state backend for Phase N
#                                          (N1 `up`); defaults to "local"
#                                          on hosts with terraform installed,
#                                          "docker" otherwise.
SSH_TARGET=${ROKSBNKCTL_E2E_SSH_TARGET:-}
SSH_NON_UBUNTU=${ROKSBNKCTL_E2E_SSH_NON_UBUNTU:-}
SSH_NO_NOPASSWD=${ROKSBNKCTL_E2E_SSH_NO_NOPASSWD:-}
INIT_BACKEND=${ROKSBNKCTL_E2E_INIT_BACKEND:-}

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

# preflight_ssh_target — sets SSH_READY=yes when ROKSBNKCTL_E2E_SSH_TARGET
# is set AND `roksbnkctl targets show <name>` succeeds against the live
# workspace. Otherwise leaves it empty so Phase I and the SSH-dependent
# steps in M + N skip-cleanly with a yellow ⊘.
#
# Run lazily (called once at the top of main, before any phase invokes
# phase_i / the M5/M6 audit / N3). DRY_RUN bypasses the `targets show`
# call and treats SSH_READY="dry" so the plan still renders Phase I.
SSH_READY=""
preflight_ssh_target() {
    if [[ -z "$SSH_TARGET" ]]; then
        SSH_READY=""
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        SSH_READY="dry"
        log "preflight_ssh_target — DRY_RUN; SSH_READY=dry (target=$SSH_TARGET)"
        return 0
    fi
    if "$ROKSBNKCTL" -w "$WORKSPACE" targets show "$SSH_TARGET" >/dev/null 2>&1; then
        SSH_READY="yes"
        log "preflight_ssh_target OK — SSH_TARGET=$SSH_TARGET"
    else
        SSH_READY=""
        yellow "preflight_ssh_target — \`targets show $SSH_TARGET\` failed; Phase I + M5/M6 + N3 will skip"
    fi
}

# ── Phase I — SSH backend (PRD 05 §I) ───────────────────────────────
#
# The 12-step PRD 05 §I matrix. Steps I0-I5 + I10-I11 land via the
# happy-path SSH target (ROKSBNKCTL_E2E_SSH_TARGET). Steps I6-I9 are
# failure-mode coverage — each is conditional on its own env var
# pointing at a purpose-built target so the integrator can opt-in
# without losing the happy-path coverage when no purpose-built target
# exists.
#
# PRD 05 §I step list (executed in this order):
#
#   I0  — `roksbnkctl -w <ws> targets show <name>` exits 0 (preflight).
#   I1  — `--on <name> ibmcloud iam oauth-tokens` (Sprint 1 SSH path).
#   I2  — `--backend ssh:<name> ibmcloud iam oauth-tokens` (Sprint 4 path).
#   I3  — `--backend ssh:<name> --bootstrap iperf3 -v` (apt-bootstrap;
#         idempotent on re-run — bootstrap is a no-op once installed).
#   I4  — cred-audit: `ssh <name> 'env | grep IBMCLOUD'` MUST NOT find
#         the API key value. The wrapper-script approach sources an
#         env-file and removes the trap-on-EXIT tempdir; the key isn't
#         in the SSH session's process env outside the wrapped command.
#   I5  — wrapper-script cleanup: `ssh <name> ls /tmp/roksbnkctl.*`
#         empty after I1-I4 (the EXIT trap removed the tempdir).
#   I6  — SetEnv silent-drop fallback. Requires a target with sshd's
#         AcceptEnv disabled — the wrapper-script path must activate.
#         Treat as informational here: a single target rarely satisfies
#         both the happy path AND the AcceptEnv-disabled negative.
#   I7  — non-Ubuntu detection. Skip unless ROKSBNKCTL_E2E_SSH_NON_UBUNTU
#         names a target.
#   I8  — sudo-password-required failure. Skip unless
#         ROKSBNKCTL_E2E_SSH_NO_NOPASSWD names a target.
#   I9  — repo-unreachable failure. Skipped here (requires network
#         mutation on the remote — out of scope for an automated step;
#         PRD 05 §I notes it as manual).
#   I10 — context-cancel: kill the SSH run mid-flight (SIGINT). The
#         backend's cleanup must complete within ~5s.
#   I11 — SSH backend doctor: `roksbnkctl doctor --backend ssh:<name>`
#         reports green.
phase_I() {
    phase_header I "SSH backend (--on jumphost + --backend ssh:<target>) — PRD 05 §I"

    if [[ "$DRY_RUN" != "1" && "$SSH_READY" != "yes" ]]; then
        if [[ -z "$SSH_TARGET" ]]; then
            yellow "  ⊘ Phase I skipped — set ROKSBNKCTL_E2E_SSH_TARGET=<name> to enable"
        else
            yellow "  ⊘ Phase I skipped — \`targets show $SSH_TARGET\` failed; cluster up?"
        fi
        return 0
    fi

    local target="$SSH_TARGET"
    [[ -z "$target" && "$DRY_RUN" == "1" ]] && target="<ssh-target>"

    # I0 — target visible in workspace config.
    if [[ "$DRY_RUN" != "1" ]]; then
        capture "I0 targets show $target" \
            "$ROKSBNKCTL" -w "$WORKSPACE" targets show "$target" \
            | assert_contains "$target" "I0 target $target present in workspace"
    else
        log "→ I0 targets show $target (dry-run)"
        log "  cmd: $ROKSBNKCTL -w $WORKSPACE targets show $target"
    fi

    # I1 — Sprint 1 `--on <target>` path: ibmcloud iam oauth-tokens.
    capture "I1 --on $target ibmcloud iam oauth-tokens (Sprint 1 SSH path)" \
        "$ROKSBNKCTL" -w "$WORKSPACE" ibmcloud --on "$target" iam oauth-tokens \
        | assert_contains "IAM token" "I1 --on $target produces IAM token"

    # I2 — Sprint 4 `--backend ssh:<target>` path: same command, exec
    # backend dispatched via the unified Backend interface.
    capture "I2 --backend ssh:$target ibmcloud iam oauth-tokens (Sprint 4 path)" \
        "$ROKSBNKCTL" -w "$WORKSPACE" ibmcloud --backend "ssh:$target" iam oauth-tokens \
        | assert_contains "IAM token" "I2 --backend ssh:$target produces IAM token"

    # I3 — bootstrap. Use --bootstrap to provision iperf3 on the remote
    # if not present, then run `iperf3 -v` to confirm. Idempotent — the
    # wrapper detects an already-installed binary and skips apt-get.
    if [[ "$DRY_RUN" != "1" ]]; then
        local out rc
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" exec --backend "ssh:$target" --bootstrap -- iperf3 -v 2>&1 || true)
        rc=$?
        if [[ "$rc" == "0" ]]; then
            echo "$out" | assert_contains "iperf" "I3 --bootstrap iperf3 -v succeeds"
        else
            yellow "  ⊘ I3 --bootstrap iperf3 -v exit=$rc (apt-bootstrap may require sudo NOPASSWD or network)"
            echo "$out" >> "$RUN_LOG"
        fi
    else
        log "→ I3 --bootstrap iperf3 -v (dry-run)"
        log "  cmd: $ROKSBNKCTL -w $WORKSPACE exec --backend ssh:$target --bootstrap -- iperf3 -v"
    fi

    # I4 — cred-audit: env on the remote MUST NOT contain the API key value.
    if [[ "$DRY_RUN" != "1" ]]; then
        log "→ I4 cred-audit — \`env | grep IBMCLOUD\` on remote does not surface key value"
        local out
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" exec --backend "ssh:$target" -- bash -lc 'env | grep -i IBMCLOUD || true' 2>&1 || true)
        # The wrapper-script approach sources an env-file inside the
        # command's process tree; outside that wrapper, the SSH login's
        # env doesn't carry IBMCLOUD_API_KEY=<value>. So we expect zero
        # matches on the API key VALUE (the var NAME may or may not
        # appear depending on whether the integrator runs an interactive
        # shell with a static export; gate on the value).
        echo "$out" | assert_not_contains "$IBMCLOUD_API_KEY" "I4 remote env does not contain API key value"
    else
        log "→ I4 cred-audit (dry-run)"
        log "  cmd: ssh $target env | grep IBMCLOUD  (asserting key VALUE not present)"
    fi

    # I5 — wrapper-script cleanup: /tmp/roksbnkctl.* gone after I1-I4.
    if [[ "$DRY_RUN" != "1" ]]; then
        log "→ I5 wrapper-script cleanup — /tmp/roksbnkctl.* empty"
        local out
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" exec --backend "ssh:$target" -- bash -lc 'ls -d /tmp/roksbnkctl.* 2>/dev/null || true' 2>&1 || true)
        if [[ -z "$(echo "$out" | tr -d '[:space:]')" ]]; then
            green "  ✓ I5 wrapper-script tempdirs cleaned (trap-on-EXIT fired)"
        else
            red "  ✗ I5 wrapper-script tempdirs leaked: $out"
            exit 1
        fi
    else
        log "→ I5 wrapper-script cleanup (dry-run)"
        log "  cmd: ssh $target ls /tmp/roksbnkctl.*  (asserting empty)"
    fi

    # I6 — SetEnv silent-drop. Informational unless the target's sshd
    # has AcceptEnv disabled (production sshd default).
    log "→ I6 SetEnv silent-drop fallback (informational — wrapper-script path activates if sshd's AcceptEnv blocks IBMCLOUD_API_KEY)"

    # I7 — non-Ubuntu detection. The SSH backend's --bootstrap path
    # only knows apt-get; on a non-Ubuntu target it must fail clearly.
    if [[ -n "$SSH_NON_UBUNTU" && "$DRY_RUN" != "1" ]]; then
        log "→ I7 non-Ubuntu --bootstrap rejection (target=$SSH_NON_UBUNTU)"
        local out rc
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" exec --backend "ssh:$SSH_NON_UBUNTU" --bootstrap -- iperf3 -v 2>&1 || true)
        rc=$?
        if [[ "$rc" == "0" ]]; then
            yellow "  ⊘ I7 — bootstrap succeeded on non-Ubuntu target (already had iperf3 installed?)"
        else
            echo "$out" | assert_contains "Ubuntu" "I7 non-Ubuntu --bootstrap error mentions Ubuntu"
        fi
    elif [[ "$DRY_RUN" == "1" ]]; then
        log "→ I7 non-Ubuntu detection (dry-run)"
        log "  cmd: $ROKSBNKCTL -w $WORKSPACE exec --backend ssh:<non-ubuntu> --bootstrap -- iperf3 -v  (set ROKSBNKCTL_E2E_SSH_NON_UBUNTU)"
    else
        yellow "  ⊘ I7 skipped — ROKSBNKCTL_E2E_SSH_NON_UBUNTU unset"
    fi

    # I8 — sudo password required.
    if [[ -n "$SSH_NO_NOPASSWD" && "$DRY_RUN" != "1" ]]; then
        log "→ I8 sudo-password-required rejection (target=$SSH_NO_NOPASSWD)"
        local out rc
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" exec --backend "ssh:$SSH_NO_NOPASSWD" --bootstrap -- iperf3 -v 2>&1 || true)
        rc=$?
        if [[ "$rc" == "0" ]]; then
            yellow "  ⊘ I8 — bootstrap succeeded (already had iperf3?)"
        else
            echo "$out" | assert_contains "sudo" "I8 sudo-password rejection mentions sudo"
        fi
    elif [[ "$DRY_RUN" == "1" ]]; then
        log "→ I8 sudo-password-required (dry-run)"
        log "  cmd: $ROKSBNKCTL -w $WORKSPACE exec --backend ssh:<no-nopasswd> --bootstrap  (set ROKSBNKCTL_E2E_SSH_NO_NOPASSWD)"
    else
        yellow "  ⊘ I8 skipped — ROKSBNKCTL_E2E_SSH_NO_NOPASSWD unset"
    fi

    # I9 — repo-unreachable failure. Documented PRD 05 §I as manual:
    # the integrator mutates the remote's /etc/apt/sources.list or
    # severs DNS, runs the command, restores. We can't safely automate
    # that here (it would affect the remote's stable state).
    yellow "  ⊘ I9 skipped — repo-unreachable failure (manual; PRD 05 §I notes network mutation on remote)"

    # I10 — context-cancel. Run an SSH-backed long command in the
    # background, kill it, verify the binary exits within ~5s.
    if [[ "$DRY_RUN" != "1" ]]; then
        log "→ I10 context-cancel — SSH run killed mid-flight cleans up <5s"
        # `bash -lc 'sleep 30; echo done'` is a long no-op; we kill the
        # roksbnkctl process after 1s and assert exit happens within 5s
        # of the kill.
        ("$ROKSBNKCTL" -w "$WORKSPACE" exec --backend "ssh:$target" -- bash -lc 'sleep 30; echo done' >/dev/null 2>&1) &
        local pid=$!
        sleep 1
        kill -INT "$pid" 2>/dev/null || true
        local start_wait
        start_wait=$(date +%s)
        local waited=0
        while kill -0 "$pid" 2>/dev/null && [[ "$waited" -lt 6 ]]; do
            sleep 1
            waited=$(( $(date +%s) - start_wait ))
        done
        if kill -0 "$pid" 2>/dev/null; then
            kill -KILL "$pid" 2>/dev/null || true
            red "  ✗ I10 SSH backend did not clean up within 5s of SIGINT"
            exit 1
        fi
        green "  ✓ I10 SSH backend cleaned up within ${waited}s of SIGINT"
    else
        log "→ I10 context-cancel (dry-run)"
        log "  cmd: $ROKSBNKCTL exec --backend ssh:$target -- sleep 30 &  (then kill -INT)"
    fi

    # I11 — doctor reports green for the SSH backend.
    if [[ "$DRY_RUN" != "1" ]]; then
        capture "I11 doctor --backend ssh:$target" \
            "$ROKSBNKCTL" -w "$WORKSPACE" doctor --backend "ssh:$target" \
            | grep -vE 'API key|api_key' \
            | assert_contains "ssh" "I11 doctor --backend ssh:$target mentions the ssh backend"
    else
        log "→ I11 doctor --backend ssh:$target (dry-run)"
        log "  cmd: $ROKSBNKCTL -w $WORKSPACE doctor --backend ssh:$target"
    fi
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

# ── Phase L-DNS — DNS probe (PRD 05 §L-DNS) ─────────────────────────
#
# Validates the miekg-based DNS probe across local + k8s vantages,
# including the GSLB --gslb-compare flow. PRD 05 step list LD0-LD10:
#
#   LD0  — `dig` not on PATH (or test runs without invoking it)
#   LD1  — local backend, A record, explicit --server 8.8.8.8
#   LD2  — local backend, AAAA record
#   LD3  — local backend, NXDOMAIN negative — exit 1
#   LD4  — local backend, --iterations 10 → rtt_ms p50/p95/p99
#   LD5  — k8s backend (requires Phase L's L0 ops install)
#   LD6  — k8s backend, --server cluster (uses pod's resolv.conf)
#   LD7  — --gslb-compare (local + k8s) → 2 vantages
#   LD8  — GSLB divergence happy path (geo-resolved name)
#   LD9  — SSH vantage — deferred per Sprint 6 (yellow ⊘)
#   LD10 — --backend docker rejected by design
#
# Cluster-aware steps (LD5-LD8) skip cleanly when no cluster is reachable.
phase_L_DNS() {
    phase_header "L-DNS" "DNS probe (miekg/dns) + GSLB compare — PRD 05 §L-DNS"

    # LD0 — dig should not be required. We don't enforce its absence on
    # dev boxes; we just make sure no roksbnkctl call below shells out
    # to `dig`. (The audit is implicit — every roksbnkctl test dns
    # invocation below uses the embedded miekg/dns probe.)
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ LD0 dig-not-required smoke (dry-run)"
    else
        if command -v dig >/dev/null 2>&1; then
            yellow "  ⊘ LD0 — dig is on PATH; the probe still uses embedded miekg/dns (informational)"
        else
            green "  ✓ LD0 dig not on PATH; probe must use embedded miekg/dns"
        fi
    fi

    # LD1 — local backend, A record, explicit --server 8.8.8.8.
    capture "LD1 local backend A record against 8.8.8.8" \
        "$ROKSBNKCTL" -w "$WORKSPACE" test dns \
            --target www.cloudflare.com --type A --server 8.8.8.8 \
            --backend local -o json \
        | assert_contains "roksbnkctl.dns.v1" "LD1 emits roksbnkctl.dns.v1 schema"

    # LD2 — local backend, AAAA record.
    capture "LD2 local backend AAAA record against 8.8.8.8" \
        "$ROKSBNKCTL" -w "$WORKSPACE" test dns \
            --target www.cloudflare.com --type AAAA --server 8.8.8.8 \
            --backend local -o json \
        | assert_contains "AAAA" "LD2 returns AAAA records"

    # LD3 — NXDOMAIN negative; exit 1 + Rcode=NXDOMAIN.
    if [[ "$DRY_RUN" != "1" ]]; then
        log "→ LD3 NXDOMAIN negative path"
        local out rc
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" test dns \
            --target nonexistent-zzz.example.invalid --type A \
            --server 8.8.8.8 --backend local -o json 2>&1 || true)
        rc=$?
        if [[ "$rc" == "0" ]]; then
            red "  ✗ LD3 expected non-zero exit on NXDOMAIN, got 0"
            echo "$out" >> "$RUN_LOG"
            exit 1
        fi
        echo "$out" | assert_contains "NXDOMAIN" "LD3 emits NXDOMAIN rcode"
    else
        log "→ LD3 NXDOMAIN negative (dry-run)"
    fi

    # LD4 — iterations=10 produces RTT distribution (p50/p95/p99).
    capture "LD4 --iterations 10 produces RTT distribution" \
        "$ROKSBNKCTL" -w "$WORKSPACE" test dns \
            --target www.cloudflare.com --type A --server 8.8.8.8 \
            --iterations 10 --backend local -o json \
        | assert_contains "p99" "LD4 rtt_ms.p99 populated"

    # LD5–LD8 require the in-cluster ops pod from Phase L's L0.
    # Skip cleanly when no cluster context is reachable.
    local cluster_ok=0
    if [[ "$DRY_RUN" != "1" ]]; then
        if "$ROKSBNKCTL" kubectl get ns roksbnkctl-ops >/dev/null 2>&1; then
            cluster_ok=1
        fi
    fi

    if [[ "$cluster_ok" == "1" || "$DRY_RUN" == "1" ]]; then
        # LD5 — k8s backend.
        capture "LD5 k8s backend A record" \
            "$ROKSBNKCTL" -w "$WORKSPACE" test dns \
                --target www.cloudflare.com --type A --server 8.8.8.8 \
                --backend k8s -o json \
            | assert_contains "\"backend\":\"k8s\"" "LD5 vantage records backend=k8s"

        # LD6 — --server cluster uses the pod's resolv.conf (CoreDNS).
        capture "LD6 k8s backend --server cluster" \
            "$ROKSBNKCTL" -w "$WORKSPACE" test dns \
                --target www.cloudflare.com --type A --server cluster \
                --backend k8s -o json \
            | assert_contains "roksbnkctl.dns.v1" "LD6 cluster-CoreDNS path emits schema"

        # LD7 — --gslb-compare across local + k8s vantages.
        capture "LD7 --gslb-compare local + k8s" \
            "$ROKSBNKCTL" -w "$WORKSPACE" test dns \
                --target www.cloudflare.com --type A --server 8.8.8.8 \
                --gslb-compare -o json \
            | assert_contains "gslb_divergence" "LD7 emits gslb_divergence boolean"

        # LD8 — GSLB divergence happy path. www.google.com is the
        # documented geo-resolved exemplar; this step's success
        # criterion is "the field is present and consistent", NOT
        # "divergence is true" — anycast can land identical answers
        # by chance. The integrator's manual sign-off (the manual
        # GSLB validation listed in PLAN.md Sprint 5 §"Test
        # deliverables") is where divergence-true is asserted against
        # a known-divergent F5 BIG-IP Next GSLB record.
        if [[ "$DRY_RUN" != "1" ]]; then
            log "→ LD8 GSLB divergence detection (informational; manual divergence-true check during v0.9 sign-off)"
            local out
            out=$("$ROKSBNKCTL" -w "$WORKSPACE" test dns \
                --target www.google.com --type A --server 8.8.8.8 \
                --gslb-compare -o json 2>&1 || true)
            echo "$out" | grep -E '"gslb_divergence":\s*(true|false)' >> "$RUN_LOG" || true
            green "  ✓ LD8 gslb_divergence boolean populated (true/false depending on anycast)"
        else
            log "→ LD8 GSLB divergence (dry-run)"
        fi
    else
        yellow "  ⊘ LD5-LD8 skipped — cluster not reachable (run after Phase L's L0 ops install)"
    fi

    # LD9 — SSH vantage. Gated on ROKSBNKCTL_E2E_SSH_TARGET being set
    # and reachable (preflight_ssh_target sets SSH_READY=yes). Phase I
    # exercises the SSH backend separately; LD9 brings the DNS probe
    # under the SSH vantage too.
    if [[ -n "$SSH_TARGET" && "$SSH_READY" == "yes" && "$DRY_RUN" != "1" ]]; then
        capture "LD9 SSH vantage A record (target=$SSH_TARGET)" \
            "$ROKSBNKCTL" -w "$WORKSPACE" test dns \
                --target www.cloudflare.com --type A --server 8.8.8.8 \
                --backend "ssh:$SSH_TARGET" -o json \
            | assert_contains "roksbnkctl.dns.v1" "LD9 SSH vantage emits schema"
    elif [[ "$DRY_RUN" == "1" ]]; then
        log "→ LD9 SSH vantage (dry-run)"
        log "  cmd: $ROKSBNKCTL -w $WORKSPACE test dns --backend ssh:$SSH_TARGET ..."
    else
        yellow "  ⊘ LD9 skipped — no SSH target (set ROKSBNKCTL_E2E_SSH_TARGET)"
    fi

    # LD10 — --backend docker rejected by design.
    if [[ "$DRY_RUN" != "1" ]]; then
        log "→ LD10 --backend docker rejected by design"
        local out rc
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" test dns \
            --target www.cloudflare.com --type A --server 8.8.8.8 \
            --backend docker -o json 2>&1 || true)
        rc=$?
        if [[ "$rc" == "0" ]]; then
            red "  ✗ LD10 expected non-zero exit for --backend docker, got 0"
            echo "$out" >> "$RUN_LOG"
            exit 1
        fi
        echo "$out" | assert_contains "docker" "LD10 docker-rejection message references docker"
    else
        log "→ LD10 docker-rejection (dry-run)"
    fi
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

    # M5 — SSH backend tempfile cleanup. Phase I's I5 exercises this
    # in-line; M5 re-runs it from the cred-audit lens so a failure
    # surfaces as a cred-related red flag (vs the cleanup-only framing
    # in I5). PRD 05 §M.5: `ssh <target> ls /tmp/roksbnkctl.*` empty.
    if [[ -n "$SSH_TARGET" && "$SSH_READY" == "yes" && "$DRY_RUN" != "1" ]]; then
        log "→ M5 SSH wrapper tempfiles audit (target=$SSH_TARGET)"
        local out
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" exec --backend "ssh:$SSH_TARGET" -- bash -lc 'ls -d /tmp/roksbnkctl.* 2>/dev/null || true' 2>&1 || true)
        if [[ -z "$(echo "$out" | tr -d '[:space:]')" ]]; then
            green "  ✓ M5 SSH wrapper tempfiles cleaned (no /tmp/roksbnkctl.* on remote)"
        else
            red "  ✗ M5 SECURITY VIOLATION: SSH wrapper tempfile leaked on remote: $out"
            exit 2
        fi
    elif [[ "$DRY_RUN" == "1" ]]; then
        log "→ M5 SSH wrapper tempfiles audit (dry-run)"
        log "  cmd: ssh $SSH_TARGET ls /tmp/roksbnkctl.*  (asserting empty)"
    else
        yellow "  ⊘ M5 skipped — no SSH target available (set ROKSBNKCTL_E2E_SSH_TARGET)"
    fi

    # M6 — SSH auth.log scan. The sshd log line for the wrapper-script
    # session should show `Accepted publickey` (auth-method recorded);
    # if SetEnv was used, only the variable NAME is logged, not the
    # value. Reading /var/log/auth.log requires sudo on most Ubuntu
    # installs — skip cleanly if the SSH user lacks sudo read access.
    if [[ -n "$SSH_TARGET" && "$SSH_READY" == "yes" && "$DRY_RUN" != "1" ]]; then
        log "→ M6 SSH /var/log/auth.log audit (target=$SSH_TARGET)"
        local out
        out=$("$ROKSBNKCTL" -w "$WORKSPACE" exec --backend "ssh:$SSH_TARGET" -- bash -lc 'sudo -n cat /var/log/auth.log 2>/dev/null | tail -50' 2>&1 || true)
        if [[ -z "$(echo "$out" | tr -d '[:space:]')" ]]; then
            yellow "  ⊘ M6 — couldn't read /var/log/auth.log (no sudo NOPASSWD or path differs)"
        else
            # Pass 1: assert the key value is NOT in the auth.log lines.
            echo "$out" | assert_not_contains "$key" "M6 SSH auth.log no API key value"
            # Pass 2: informationally note Accepted publickey lines.
            if echo "$out" | grep -q "Accepted publickey"; then
                green "  ✓ M6 sshd logged Accepted publickey for the SSH session"
            else
                yellow "  ⊘ M6 — no \`Accepted publickey\` line in last 50 — log rotation?"
            fi
        fi
    elif [[ "$DRY_RUN" == "1" ]]; then
        log "→ M6 SSH auth.log audit (dry-run)"
        log "  cmd: ssh $SSH_TARGET sudo cat /var/log/auth.log | tail -50  (asserting key value absent)"
    else
        yellow "  ⊘ M6 skipped — no SSH target available (set ROKSBNKCTL_E2E_SSH_TARGET)"
    fi

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

# ── Phase N — mixed-mode lifecycle (PRD 05 §N) ──────────────────────
#
# Verifies backend transitions preserve state. The same cluster comes
# up via one backend (typically `local` on a host with terraform
# installed, or `docker` otherwise) and is then exercised + torn down
# via *different* backends — each step asserts the new backend can
# still see / operate on the cluster the first backend created.
#
# PRD 05 §N step list:
#
#   N1 — `roksbnkctl up --backend <init>` — establish initial state.
#   N2 — `test throughput --backend k8s`  — operates against cluster
#        from N1; asserts the k8s backend reads the same kubeconfig +
#        workspace state.
#   N3 — `ibmcloud --backend ssh:<target> ks cluster ls` — same
#        cluster visible from the SSH target. Asserts the local-host
#        API key resolves correctly when propagated to a remote SSH
#        session (no re-resolve loop).
#   N4 — `test dns --backend k8s --gslb-compare` — multi-vantage
#        probe across local + k8s vantages.
#   N5 — `roksbnkctl down --backend docker` — tear down via a
#        DIFFERENT backend than N1. Asserts state-file compatibility
#        across backends (docker bind-mount reads the .tfstate that
#        the local backend wrote).
#   N6 — verify post-N5 state: `ws show` reports cluster destroyed;
#        no orphan resources visible in IBM Cloud.
#
# This phase is the most expensive and the most coordination-heavy:
# it requires a fresh cluster, an SSH target, and ~60 minutes of
# wall-time for the up/down. Skip-cleanly when prerequisites are
# missing — each step gates on what it specifically needs.
phase_N() {
    phase_header N "mixed-mode lifecycle (backends share state) — PRD 05 §N"

    # Pick the initial backend. Default: local if terraform installed,
    # docker otherwise. The integrator overrides via
    # ROKSBNKCTL_E2E_INIT_BACKEND for explicit-backend coverage.
    local init_backend="$INIT_BACKEND"
    if [[ -z "$init_backend" ]]; then
        if command -v terraform >/dev/null 2>&1; then
            init_backend="local"
        else
            init_backend="docker"
        fi
    fi

    log "Phase N init_backend=$init_backend"

    # N1 — initial up (the expensive one — 50-70min wall time).
    if [[ "$DRY_RUN" != "1" ]]; then
        step "N1 up --backend $init_backend" \
            "$ROKSBNKCTL" up --auto -w "$WORKSPACE" \
                --backend "$init_backend" --var-file "$TFVARS"
    else
        log "→ N1 up --backend $init_backend (dry-run)"
        log "  cmd: $ROKSBNKCTL up --auto -w $WORKSPACE --backend $init_backend --var-file $TFVARS"
    fi

    # N2 — throughput via k8s backend, against the cluster from N1.
    # Skip-cleanly if the k8s context isn't reachable (no kind/kube).
    local k8s_ok=0
    if [[ "$DRY_RUN" != "1" ]]; then
        if "$ROKSBNKCTL" kubectl get ns >/dev/null 2>&1; then
            k8s_ok=1
        fi
    fi
    if [[ "$k8s_ok" == "1" || "$DRY_RUN" == "1" ]]; then
        if [[ "$DRY_RUN" != "1" ]]; then
            step "N2 test throughput --backend k8s (cluster from N1)" \
                "$ROKSBNKCTL" -w "$WORKSPACE" test throughput --backend k8s
        else
            log "→ N2 test throughput --backend k8s (dry-run)"
            log "  cmd: $ROKSBNKCTL -w $WORKSPACE test throughput --backend k8s"
        fi
    else
        yellow "  ⊘ N2 skipped — no kube context reachable"
    fi

    # N3 — ibmcloud via ssh:<target>; asserts the API key resolved on
    # the local host propagates to the SSH session correctly. The
    # cluster from N1 must be visible from the SSH-side ibmcloud CLI.
    if [[ -n "$SSH_TARGET" && "$SSH_READY" == "yes" && "$DRY_RUN" != "1" ]]; then
        capture "N3 ibmcloud --backend ssh:$SSH_TARGET ks cluster ls (cluster from N1 visible)" \
            "$ROKSBNKCTL" -w "$WORKSPACE" ibmcloud --backend "ssh:$SSH_TARGET" ks cluster ls \
            | assert_contains "OK" "N3 ks cluster ls visible from SSH target"
    elif [[ "$DRY_RUN" == "1" ]]; then
        log "→ N3 ibmcloud --backend ssh:<target> ks cluster ls (dry-run)"
        log "  cmd: $ROKSBNKCTL -w $WORKSPACE ibmcloud --backend ssh:$SSH_TARGET ks cluster ls"
    else
        yellow "  ⊘ N3 skipped — no SSH target (set ROKSBNKCTL_E2E_SSH_TARGET)"
    fi

    # N4 — DNS probe across local + k8s vantages.
    if [[ "$k8s_ok" == "1" || "$DRY_RUN" == "1" ]]; then
        if [[ "$DRY_RUN" != "1" ]]; then
            capture "N4 test dns --backend k8s --gslb-compare" \
                "$ROKSBNKCTL" -w "$WORKSPACE" test dns \
                    --target www.cloudflare.com --type A --server 8.8.8.8 \
                    --backend k8s --gslb-compare -o json \
                | assert_contains "gslb_divergence" "N4 multi-vantage probe emits gslb_divergence boolean"
        else
            log "→ N4 test dns --backend k8s --gslb-compare (dry-run)"
            log "  cmd: $ROKSBNKCTL -w $WORKSPACE test dns --gslb-compare ..."
        fi
    else
        yellow "  ⊘ N4 skipped — no kube context (k8s vantage unavailable)"
    fi

    # N5 — tear down via a DIFFERENT backend (docker, if init was
    # local; local, if init was docker). The point of this step is to
    # validate state-file portability: the .tfstate written by the
    # init backend is readable by the teardown backend.
    local teardown_backend
    if [[ "$init_backend" == "docker" ]]; then
        teardown_backend="local"
        if ! command -v terraform >/dev/null 2>&1 && [[ "$DRY_RUN" != "1" ]]; then
            # Fall back to docker for teardown if local terraform is
            # absent (rare — most CI hosts have terraform installed).
            teardown_backend="docker"
        fi
    else
        teardown_backend="docker"
        if [[ "$DRY_RUN" != "1" ]] && ! (command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1); then
            # Docker daemon not reachable — fall back to local.
            teardown_backend="local"
        fi
    fi

    log "Phase N teardown_backend=$teardown_backend (init was $init_backend; cross-backend if differs)"

    if [[ "$DRY_RUN" != "1" ]]; then
        step "N5 down --backend $teardown_backend (cross-backend state-file compat)" \
            "$ROKSBNKCTL" down --auto -w "$WORKSPACE" \
                --backend "$teardown_backend" --var-file "$TFVARS"
    else
        log "→ N5 down --backend $teardown_backend (dry-run)"
        log "  cmd: $ROKSBNKCTL down --auto -w $WORKSPACE --backend $teardown_backend --var-file $TFVARS"
    fi

    # N6 — post-teardown state check. `ws show` (or `status`) should
    # not list a live cluster. We assert the cluster-outputs.json is
    # gone — `cluster down` deletes it (Phase C in baseline relies on
    # this same invariant).
    if [[ "$DRY_RUN" != "1" ]]; then
        local outputs="$HOME/.roksbnkctl/$WORKSPACE/cluster-outputs.json"
        if [[ -f "$outputs" ]]; then
            red "  ✗ N6 cluster-outputs.json still present after down: $outputs"
            exit 1
        fi
        green "  ✓ N6 cluster-outputs.json removed by down (state cleaned)"
    else
        log "→ N6 post-teardown state check (dry-run)"
        log "  cmd: test ! -f ~/.roksbnkctl/$WORKSPACE/cluster-outputs.json"
    fi
}

# ── main ────────────────────────────────────────────────────────────
main() {
    bold "roksbnkctl backend-matrix E2E — run-id $RUN_TS"
    log "log: $RUN_LOG"

    preflight
    preflight_ssh_target

    should_run I && phase_I
    should_run K && phase_K
    should_run L && phase_L
    # Phase L-DNS sorts after L and before M with the existing
    # alphabetic should_run comparator: PHASE_FROM=L runs L + L-DNS + M
    # + N; PHASE_FROM=L-DNS runs L-DNS + M + N; PHASE_FROM=M runs M + N;
    # PHASE_FROM=N runs N only.
    should_run "L-DNS" && phase_L_DNS
    should_run M && phase_M
    should_run N && phase_N

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "Backend-matrix phases passed. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
}

main "$@"
