#!/usr/bin/env bash
# scripts/integration-test.sh — local integration-test runner.
#
# Sprint 10 / PLAN.md §"Sprint 10 → Code deliverable 3": closes the
# v1.2.x cascade gap where `make release` ran `go build -tags
# integration ./...` (compile check) but not `go test -tags integration`
# (which requires a kind cluster + docker daemon). This script brings up
# an ephemeral kind cluster, runs the integration-tagged tests against
# it, then tears the cluster down. Intended invocation paths:
#
#   make integration-test           # standalone — bring up cluster, run, tear down
#   make release                    # release gate calls this if kind is reachable
#
# Env knobs:
#
#   KIND_CLUSTER_NAME — name of the kind cluster (default: roksbnkctl-it)
#   KEEP_KIND=1       — don't delete the cluster on exit (for iterative debug)
#   SKIP_REMOTE=1     — skip internal/remote integration tests (docker-only,
#                       no kind dependency; useful when iterating on k8s tests)
#   SKIP_K8S=1        — skip internal/exec k8s integration tests (kind
#                       dependency); leaves docker-only + remote tests running
#
# Exit codes:
#
#   0   — all integration tests passed
#   2   — preflight failed (kind / docker not reachable)
#   3   — kind cluster bring-up failed
#   4+  — test failures (forwards `go test` exit code shifted by base)
#
# This file is sibling to scripts/e2e-test.sh + scripts/e2e-test-backends.sh
# (which exercise the binary against a real IBM Cloud account). The
# integration tests here run *the test code in this module* against a
# local kind cluster — no IBM Cloud API calls, no real workspace.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-roksbnkctl-it}"
KEEP_KIND="${KEEP_KIND:-0}"
SKIP_REMOTE="${SKIP_REMOTE:-0}"
SKIP_K8S="${SKIP_K8S:-0}"

# Test timeout — generous; kind pod scheduling can be slow on first
# image-pull.
TEST_TIMEOUT="${TEST_TIMEOUT:-10m}"

# ── helpers ─────────────────────────────────────────────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }

log()    { echo "[$(date +%H:%M:%S)] $*" >&2; }

# ── preflight ───────────────────────────────────────────────────────
preflight() {
    bold "preflight"

    if ! command -v kind >/dev/null 2>&1; then
        red "kind not on PATH — install via:"
        red "    go install sigs.k8s.io/kind@latest"
        red "    (or download a binary from https://kind.sigs.k8s.io/)"
        exit 2
    fi
    log "kind: $(kind version 2>&1 | head -1)"

    if ! command -v docker >/dev/null 2>&1; then
        red "docker not on PATH — kind needs docker (or another runtime) to host nodes"
        exit 2
    fi
    if ! docker info >/dev/null 2>&1; then
        red "docker daemon not reachable — start it before re-running"
        red "    (kind nodes are docker containers; the daemon must be up)"
        exit 2
    fi
    log "docker: $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo unknown)"

    if ! command -v go >/dev/null 2>&1; then
        red "go not on PATH"
        exit 2
    fi
    log "go: $(go version | awk '{print $3, $4}')"
}

# ── kind cluster lifecycle ──────────────────────────────────────────

# bring_up_kind: idempotent. If a cluster with $KIND_CLUSTER_NAME
# already exists, reuse it (useful for KEEP_KIND=1 iterative loops).
# Otherwise create a fresh one.
bring_up_kind() {
    if kind get clusters 2>/dev/null | grep -qx "$KIND_CLUSTER_NAME"; then
        yellow "  ⊘ kind cluster $KIND_CLUSTER_NAME already exists — reusing"
        return 0
    fi
    log "creating kind cluster $KIND_CLUSTER_NAME"
    if ! kind create cluster --name "$KIND_CLUSTER_NAME" --wait 2m; then
        red "kind create cluster failed — see output above"
        exit 3
    fi
    green "  ✓ kind cluster $KIND_CLUSTER_NAME created"
}

# tear_down_kind: skipped under KEEP_KIND=1. Always non-fatal — the
# cluster may have been removed manually mid-run, and a failed delete
# shouldn't mask test failures.
tear_down_kind() {
    if [[ "$KEEP_KIND" == "1" ]]; then
        yellow "  ⊘ KEEP_KIND=1 — leaving $KIND_CLUSTER_NAME for debugging"
        yellow "    (delete manually with: kind delete cluster --name $KIND_CLUSTER_NAME)"
        return 0
    fi
    log "deleting kind cluster $KIND_CLUSTER_NAME"
    kind delete cluster --name "$KIND_CLUSTER_NAME" >/dev/null 2>&1 || \
        yellow "  ⊘ kind delete failed (already gone?) — non-fatal"
}

# Trap-driven cleanup so tear-down runs even on test failure / SIGINT.
# Honors KEEP_KIND.
#
# Sprint 10 / validator Issue 4 closure: the trap is installed inside
# main() *after* bring_up_kind succeeds, NOT at the top level. Earlier
# revisions installed the trap at the top level, which meant a
# preflight-exit (kind not on PATH) still fired tear_down_kind and
# printed a misleading "deleting kind cluster" log line followed by
# "kind delete failed (already gone?)". With the trap installed only
# after the cluster is up, preflight-fail and bring-up-fail paths exit
# cleanly without the spurious teardown chatter.

# ── test runners ────────────────────────────────────────────────────

# run_exec_tests: internal/exec/... — the k8s + docker integration
# tests. The k8s tests use the active KUBECONFIG which, after `kind
# create cluster`, points at the kind cluster's API server. The docker
# tests are kind-independent; they spawn ephemeral containers via the
# host docker daemon.
run_exec_tests() {
    if [[ "$SKIP_K8S" == "1" ]]; then
        yellow "  ⊘ SKIP_K8S=1 — skipping internal/exec tests"
        return 0
    fi
    bold "go test -tags integration ./internal/exec/..."
    go test -tags integration -timeout "$TEST_TIMEOUT" ./internal/exec/...
}

# run_remote_tests: internal/remote/... — testcontainers-go sshd
# integration. Kind-independent (uses the host docker daemon for an
# ephemeral sshd container); could in principle run without the
# cluster up, but we keep it under the same gate so contributors only
# have to remember one entrypoint.
run_remote_tests() {
    if [[ "$SKIP_REMOTE" == "1" ]]; then
        yellow "  ⊘ SKIP_REMOTE=1 — skipping internal/remote tests"
        return 0
    fi
    bold "go test -tags integration ./internal/remote/..."
    go test -tags integration -timeout "$TEST_TIMEOUT" ./internal/remote/...
}

# ── main ────────────────────────────────────────────────────────────
main() {
    bold "roksbnkctl integration test — kind cluster $KIND_CLUSTER_NAME"

    preflight
    bring_up_kind
    # Install the EXIT/INT/TERM trap only AFTER the cluster is up. See
    # the comment on tear_down_kind for the rationale (validator Issue 4).
    trap tear_down_kind EXIT INT TERM

    # The k8s integration tests resolve their kubeconfig via
    # k8s.DefaultKubeconfigPath() which honors $KUBECONFIG, falling
    # back to ~/.kube/config. `kind create cluster` writes its kubeconfig
    # entry into ~/.kube/config and sets the current-context to the
    # kind cluster — so the tests pick it up automatically without us
    # exporting anything here. Document the assumption in case it ever
    # drifts.
    log "kube-context: $(kubectl config current-context 2>/dev/null || echo unknown)"

    run_exec_tests
    run_remote_tests

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "Integration tests passed against kind cluster $KIND_CLUSTER_NAME"
    green "════════════════════════════════════════════════════════════"
}

main "$@"
