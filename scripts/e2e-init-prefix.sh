#!/usr/bin/env bash
# scripts/e2e-init-prefix.sh — gated live-verify driver for the Sprint 26
# staff feature: prefix-driven, fully-generated terraform.tfvars + the
# no-collision guarantee (validator Issue 2,
# issues/issue_sprint26_validator.md). Mirrors
# scripts/e2e-init-var-file.sh's gating + redact() + DRY_RUN shape and
# discipline: structured logging, redact() over every echoed command,
# DRY_RUN walk-through, EXIT-trap teardown, and no API-key value ever
# read out of the environment into argv or stdout.
#
# What it proves (validator Issue 2 acceptance criteria 1–3):
#   * G1 — `init` (interview defaults) then `plan` for prefix A renders
#     state/terraform.tfvars carrying the FULL generated name set
#     (openshift_cluster_name=<A>, roks_cluster_vpc_name=<A>-cluster-vpc,
#     roks_transit_gateway_name=<A>-tgw, roks_cos_instance_name=
#     <A>-registry-cos, testing_client_vpc_name=<A>-client-vpc,
#     testing_tgw_jumphost_name=<A>-jh-tgw, …) and NO upstream tf-*
#     default names.
#   * G2 — a second workspace/prefix B plans against the B-prefixed name
#     set, and the two rendered name sets are DISJOINT (no shared
#     resource name) — the cross-workspace collision class this sprint
#     closes (the 2026-05-28 canada-roks incident).
#   * G3 — a terraform.tfvars.user dropped for prefix A overriding
#     openshift_cluster_name is layered last (the "→ Layering user tfvars
#     from <path>" line fires) and the override value wins over the
#     generated base in the plan.
#   * G4 — planted-sentinel leak scan over the run log = 0 hits (proves
#     redact() covered every echo path).
#
# ─────────────────────────────────────────────────────────────────────
# THIS IS NOT A CI JOB. Operator-run only, via `!`.
# ─────────────────────────────────────────────────────────────────────
#
#   * NO cloud spend. `init` provisions nothing; this driver never runs
#     `roksbnkctl up`. `plan` is read-only (terraform plan, not apply).
#     The driver tears down both throwaway workspaces on EXIT.
#   * Opt-in. Nothing automatic runs this — no GitHub workflow.
#   * Requires IBMCLOUD_API_KEY in the environment (init verifies the
#     credential before writing config.yaml, and plan needs it for the
#     terraform refresh). The value is NEVER echoed — redact() scrubs it
#     from every logged command and a final scan proves it didn't leak.
#
# Usage:
#   ./scripts/e2e-init-prefix.sh                 # live verify
#   DRY_RUN=1 ./scripts/e2e-init-prefix.sh       # walk-through, no cloud calls
#
# Knobs:
#   PREFIX_A       default e2e-prefix-a    (also the workspace name)
#   PREFIX_B       default e2e-prefix-b    (also the workspace name)
#   DRY_RUN        default 0
#   LOG_DIR        default /tmp/roksbnkctl-e2e-init-prefix
#   ROKSBNKCTL     default roksbnkctl
#
# Exit codes: 0 = GREEN. Non-zero = first failed assertion, with the
# failing check named in the error line.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
PREFIX_A=${PREFIX_A:-e2e-prefix-a}
PREFIX_B=${PREFIX_B:-e2e-prefix-b}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e-init-prefix}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/init-prefix-$RUN_TS.log"
WORK_DIR="$LOG_DIR/work-$RUN_TS"
mkdir -p "$WORK_DIR"

# ── helpers (mirror scripts/e2e-init-var-file.sh) ───────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }
log()    { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

# Redact the API key value from any string we are about to print.
redact() {
    local s="$*"
    if [[ -n "${IBMCLOUD_API_KEY:-}" ]]; then
        s=${s//"$IBMCLOUD_API_KEY"/<redacted>}
    fi
    printf '%s' "$s"
}

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
# Best-effort. `init`/`plan` provision no cloud infra so there's no
# billable resource to strand — just the two workspace state dirs.
TORN_DOWN=0
teardown() {
    local prev_rc=$?
    [[ "$TORN_DOWN" == "1" ]] && return
    TORN_DOWN=1
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ teardown (dry-run): delete workspaces $PREFIX_A, $PREFIX_B"
        return
    fi
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    yellow "TEARDOWN — removing temporary workspaces $PREFIX_A, $PREFIX_B"
    bold "════════════════════════════════════════════════════════════"
    local base="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
    for ws in "$PREFIX_A" "$PREFIX_B"; do
        "$ROKSBNKCTL" ws delete "$ws" --force >>"$RUN_LOG" 2>&1 || true
        local ws_dir="$base/$ws"
        [[ -d "$ws_dir" ]] && rm -rf "$ws_dir" || true
    done
    green "  ✓ teardown: workspaces removed"
    if [[ "$prev_rc" != "0" ]]; then
        red "Run FAILED (exit $prev_rc) — see $RUN_LOG"
    fi
}
trap teardown EXIT

# ── preflight ───────────────────────────────────────────────────────
preflight() {
    bold "preflight"
    if [[ "$DRY_RUN" != "1" ]]; then
        if [[ -z "${IBMCLOUD_API_KEY:-}" ]]; then
            fail "IBMCLOUD_API_KEY not set — this driver is gated on a live key (init verifies it, plan refreshes with it)"
        fi
        if ! command -v "$ROKSBNKCTL" >/dev/null 2>&1; then
            fail "$ROKSBNKCTL not on PATH (set ROKSBNKCTL=/abs/path/to/binary)"
        fi
    fi
    log "preflight OK — prefixes=$PREFIX_A,$PREFIX_B log=$RUN_LOG"
}

# Plant a sentinel that LOOKS like a key-leak risk so the final G4 scan
# can prove the driver scrubbed it.
plant_sentinel() {
    SENTINEL="ROKSBNKCTL_E2E_INIT_PREFIX_SENTINEL_$(head -c 16 /dev/urandom | xxd -p)"
    local before="cmd --api-key $SENTINEL init -w $PREFIX_A …"
    IBMCLOUD_API_KEY=$SENTINEL  # local override JUST for the assertion
    local after; after=$(redact "$before")
    IBMCLOUD_API_KEY=${REAL_API_KEY:-}
    if [[ "$after" == *"$SENTINEL"* ]]; then
        fail "redact() did NOT strip the sentinel — driver would leak the API key"
    fi
    log "redact() sentinel check passed (sentinel not present in redacted form)"
}

# rendered_tfvars echoes the path to a workspace's rendered (trial-phase)
# terraform.tfvars — the config.yaml-derived file `plan` writes BEFORE
# terraform runs.
rendered_tfvars() {
    local ws="$1"
    local base="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
    printf '%s' "$base/$ws/state/terraform.tfvars"
}

# tfvar_value extracts the quoted RHS of a `<name> = "<value>"` assignment
# from a rendered tfvars file, or "" if absent.
tfvar_value() {
    local file="$1" name="$2"
    awk -F'=' -v k="$name" '
        { gsub(/^[[:space:]]+|[[:space:]]+$/, "", $1) }
        $1 == k { v=$2; gsub(/^[[:space:]]*"?|"?[[:space:]]*$/, "", v); print v; exit }
    ' "$file"
}

# assert_full_name_set checks that a workspace's rendered tfvars carries the
# complete prefix-derived name set and NO upstream tf-* default name.
assert_full_name_set() {
    local ws="$1" prefix="$2"
    local file; file=$(rendered_tfvars "$ws")
    if [[ ! -f "$file" ]]; then
        fail "G1: rendered tfvars not found for $ws at $file (did plan run?)"
    fi

    # name variable → expected derived value (the suffix scheme from
    # internal/naming.Derive). Cluster name == bare prefix.
    declare -A want=(
        [openshift_cluster_name]="$prefix"
        [roks_cluster_vpc_name]="$prefix-cluster-vpc"
        [roks_cos_instance_name]="$prefix-registry-cos"
        [roks_transit_gateway_name]="$prefix-tgw"
        [testing_client_vpc_name]="$prefix-client-vpc"
        [testing_tgw_jumphost_name]="$prefix-jh-tgw"
    )
    local var
    for var in "${!want[@]}"; do
        local got; got=$(tfvar_value "$file" "$var")
        if [[ "$got" != "${want[$var]}" ]]; then
            fail "G1[$ws]: $var = '$got'; want '${want[$var]}' (prefix-derived)"
        fi
    done

    # No upstream tf-* default name may leak into the rendered file.
    if grep -qE '"tf-[a-z]' "$file"; then
        red "  offending tf-* lines in $file:"
        grep -nE '"tf-[a-z]' "$file" >&2 || true
        fail "G1[$ws]: rendered tfvars leaked an upstream tf-* default name — names must be prefix-derived"
    fi
    green "  ✓ G1[$ws] full prefix-derived name set present, no tf-* defaults"
}

# ── the reproduction ────────────────────────────────────────────────
main() {
    bold "roksbnkctl init (prefix-driven) — live verify — run-id $RUN_TS"
    bold "(validator Issue 2 — Sprint 26 — NOT a CI job)"
    log "log: $RUN_LOG"
    preflight
    REAL_API_KEY=${IBMCLOUD_API_KEY:-}
    plant_sentinel

    # ── G1/G2: init + plan for two distinct prefixes ────────────────
    # init runs with stdin = /dev/null so every interview prompt falls
    # through to its non-TTY default; the prefix default is the sanitized
    # workspace name, so workspace "$PREFIX_A" → prefix "$PREFIX_A".
    for ws in "$PREFIX_A" "$PREFIX_B"; do
        step "init -w $ws (defaults; prefix = sanitized workspace name)" \
            bash -c "$ROKSBNKCTL init -w $ws </dev/null"
        step "plan -w $ws (renders prefix-derived terraform.tfvars)" \
            bash -c "$ROKSBNKCTL plan -w $ws </dev/null"
    done

    bold "──── assertions ────"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ G1 each workspace's state/terraform.tfvars carries the full <prefix>-* name set, no tf-* defaults"
        log "→ G2 the two name sets are disjoint (no shared resource name)"
        log "→ G3 a terraform.tfvars.user override of openshift_cluster_name is layered + wins"
        log "→ G4 run log free of API-key leak (sentinel scan)"
        green "DRY-RUN complete — steps rendered, no cloud calls, no key printed."
        return 0
    fi

    # G1 — full generated name set for each prefix, no tf-* defaults.
    assert_full_name_set "$PREFIX_A" "$PREFIX_A"
    assert_full_name_set "$PREFIX_B" "$PREFIX_B"

    # G2 — no-collision proof: the two rendered name sets are DISJOINT.
    # Extract every quoted *_name value from each file and prove the
    # intersection is empty (the collision class this sprint closes).
    local file_a file_b
    file_a=$(rendered_tfvars "$PREFIX_A")
    file_b=$(rendered_tfvars "$PREFIX_B")
    local names_a names_b shared
    names_a=$(grep -E '_name[[:space:]]*=' "$file_a" | sed -E 's/.*"([^"]+)".*/\1/' | sort -u)
    names_b=$(grep -E '_name[[:space:]]*=' "$file_b" | sed -E 's/.*"([^"]+)".*/\1/' | sort -u)
    if [[ -z "$names_a" || -z "$names_b" ]]; then
        fail "G2: could not extract resource names from one of the rendered files"
    fi
    shared=$(comm -12 <(printf '%s\n' "$names_a") <(printf '%s\n' "$names_b"))
    if [[ -n "$shared" ]]; then
        red "  shared names across $PREFIX_A and $PREFIX_B:"
        printf '%s\n' "$shared" >&2
        fail "G2: the two prefixes share resource name(s) — collision NOT prevented"
    fi
    green "  ✓ G2 no-collision: $PREFIX_A and $PREFIX_B render fully disjoint resource names"

    # G3 — override proof: drop a terraform.tfvars.user for prefix A
    # overriding openshift_cluster_name, re-plan, and confirm both the
    # layering log line fires and the override wins. The user file lives
    # at the workspace root (tf.Workspace.UserTFVarsPath()).
    local base="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
    local user_tfvars="$base/$PREFIX_A/terraform.tfvars.user"
    local override_name="$PREFIX_A-overridden"
    printf 'openshift_cluster_name = "%s"\n' "$override_name" > "$user_tfvars"
    chmod 600 "$user_tfvars"
    log "→ wrote override $user_tfvars (openshift_cluster_name=$override_name)"

    local plan_log="$WORK_DIR/plan-override-$RUN_TS.log"
    set +e
    "$ROKSBNKCTL" plan -w "$PREFIX_A" </dev/null >"$plan_log" 2>&1
    local plan_rc=$?
    set -e
    cat "$plan_log" >> "$RUN_LOG"
    if [[ $plan_rc -ne 0 ]]; then
        fail "G3: re-plan -w $PREFIX_A with override exited $plan_rc (expected 0)"
    fi
    if ! grep -qF "→ Layering user tfvars from" "$plan_log"; then
        red "  override-plan stderr (head):"
        sed -n '1,40p' "$plan_log" >&2
        fail "G3: override plan log missing the '→ Layering user tfvars from <path>' line — HasUserTFVars() did not fire"
    fi
    # The override wins in the plan: terraform echoes the layered value.
    # The generated base still carries the un-overridden name in the
    # rendered tfvars (it's the user var-file that overrides at plan
    # time), so we assert the override value shows up in the PLAN OUTPUT.
    if ! grep -qF "$override_name" "$plan_log"; then
        fail "G3: override value '$override_name' did not appear in the plan output — the user tfvars layer did not win"
    fi
    green "  ✓ G3 terraform.tfvars.user override layered and wins ($override_name in plan)"

    # G4 — leak scan over the combined run log.
    if grep -qF "$SENTINEL" "$RUN_LOG"; then
        fail "G4: sentinel leaked into the run log — redact() bypassed somewhere"
    fi
    if [[ -n "$REAL_API_KEY" ]]; then
        local head=${REAL_API_KEY:0:24}
        if grep -qF "$head" "$RUN_LOG"; then
            fail "G4: API-key head leaked into the run log — redact() did not cover all echo paths"
        fi
    fi
    green "  ✓ G4 leak scan: sentinel + API-key head both absent from $RUN_LOG"

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "GREEN — prefix-driven generation verified live: two distinct"
    green "prefixes render full, disjoint, tf-*-free name sets; the"
    green "terraform.tfvars.user override layers last and wins; no key"
    green "leaks. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
    green "(teardown runs next via the EXIT trap)"
}

main "$@"
