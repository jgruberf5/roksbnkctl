You are the validator agent for Sprint 2 of the roksbnkctl project. Your scope is **unit tests + golden-file byte-equivalence + e2e PATH-strip + CI** for the kubectl-internalisation work the staff agent is implementing per PRD 02.

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25.

## Read first

- `docs/prd/02-KUBECTL-INTERNAL.md` — the spec staff is implementing; pay special attention to the "Acceptance criteria" section
- `docs/PLAN.md` Sprint 2 "Test deliverables" — your acceptance criteria
- `internal/test/throughput.go` — already uses client-go for fixture deployment; reference for fake-clientset patterns and SPDY exec usage
- `scripts/e2e-test.sh` — existing E2E driver. Phase D step D3 currently is `roksbnkctl kubectl get pods -n f5-bnk` (passthrough). Sprint 2 replaces it with `roksbnkctl k get pods -n f5-bnk` and adds a PATH-strip check.
- `prompts/sprint1/validator.md` — Sprint 1's validator prompt as template
- `issues/resolved_sprint1_validator.md` — Sprint 1's testcontainers-go integration test pattern; reusable here for kind-based testing if you go there

## Coordinate with parallel agents

An architect agent is replacing 7 chapter stubs with real prose under `book/src/` (5, 6, 8, 9, 10, 11, 24). A staff-engineer agent is implementing PRD 02 in `internal/k8s/` (production code) and `internal/cli/k_*.go` (cobra wiring), plus editing `internal/doctor/doctor.go` (kubectl/oc downgrade). **Do not touch their files.** You own `internal/k8s/*_test.go`, `.github/workflows/`, `scripts/e2e-test.sh`, `docs/E2E_TEST.md`.

Specifically: write `_test.go` files in `internal/k8s/` for the new packages staff creates, but never the production files themselves. Use the same package name (`package k8s`) so you can access unexported symbols.

## Tasks

### 1. Fake-clientset unit tests (`internal/k8s/*_test.go`)

For each new file staff adds, write a sibling `_test.go` covering the happy path and one or two edge cases. Use `k8s.io/client-go/kubernetes/fake` for typed-client stubbing and `k8s.io/client-go/dynamic/fake` for the dynamic-client side.

Test files:
- `internal/k8s/get_test.go` — exercise typed + dynamic paths; assert output formatting matches `cli-runtime`'s expectations for `-o yaml/json/wide/name`. Include namespace scoping (`-n`, `-A`).
- `internal/k8s/describe_test.go` — basic Pod + Node + Service describe; verify the `kubectl/pkg/describe` library is being delegated to (don't try to test the library itself, just our delegation).
- `internal/k8s/apply_test.go` — single-file YAML apply via fake dynamic client; kustomize-base apply if staff implemented it.
- `internal/k8s/delete_test.go` — cascade options round-trip into the dynamic client's call args.
- `internal/k8s/logs_test.go` — extend the existing logs test (if any) with the raw-pod-name path. The fake clientset doesn't support GetLogs streaming directly; test the request-construction path and use mock interfaces.
- `internal/k8s/exec_test.go` — SPDY executor is hard to fake; cover the request-URL construction and option mapping. Skip end-to-end execution; integration tests cover that.
- `internal/k8s/port_forward_test.go` — same; option mapping only.

Aim for ~70%+ coverage on the new `internal/k8s/` files. Skip code paths that need a real kube API (live integration). Use table-driven tests where the surface is wide.

### 2. Golden-file byte-equivalence tests (`internal/k8s/golden_test.go`)

Build tag `// +build live` so they only run against a real cluster. The PLAN.md "Acceptance criteria" requires byte-equivalence with kubectl for representative resources (Node, Pod, Service, ConfigMap) when output format is `-o yaml`. Generate fixtures from a live cluster and compare:

```go
//go:build live

func TestGolden_GetNodes_YAML(t *testing.T) {
    // Skip if KUBECONFIG not set or cluster not reachable
    // Run kubectl get nodes -o yaml; capture stdout
    // Run roksbnkctl k get nodes -o yaml; capture stdout
    // Diff, ignoring managedFields/resourceVersion/creationTimestamp
    // Fail if any other diff exists
}
```

Add a Makefile target `make test-live` that runs these (mirrors `make test-integration` from Sprint 1).

These tests don't run in CI (no live cluster); they're for the integrator to run manually before tagging v0.8. Document in CONTRIBUTING.md.

### 3. CI workflow updates

Edit `.github/workflows/ci.yml`:
- Existing test matrix already runs unit tests; ensure the new `internal/k8s/*_test.go` files are exercised.
- Optionally add a `kind`-based integration job that spins up a single-node kind cluster and runs a smoke test (`roksbnkctl k get nodes` against it). Lower priority — skip if you run out of time. PLAN.md sequences this as Sprint 4 territory.
- Verify `go-version-file: go.mod` is still in use (Sprint 1 set this); the kubectl/cli-runtime deps may bump go.mod further — let `go mod tidy` decide and ensure CI follows.

### 4. E2E patch — `scripts/e2e-test.sh` Phase D

Currently D3 is `roksbnkctl kubectl get pods -n f5-bnk` (passthrough). Sprint 2 replaces it with the internalised verb:

```bash
# Replace D3 with the native path
step "D3 k get pods -n f5-bnk" "$ROKSBNKCTL" k get pods -n f5-bnk
```

Add a new substep D3b that **strips kubectl from PATH** for the duration of one command, runs `roksbnkctl k get nodes`, and asserts >= 3 Ready nodes. This validates the no-kubectl-required claim:

```bash
# D3b: PATH-strip check — proves we don't shell out to kubectl
local stripped_path
stripped_path=$(echo "$PATH" | tr ':' '\n' | grep -v -E '/kubectl$' | paste -sd:)
capture "D3b k get nodes (PATH-stripped)" env PATH="$stripped_path" "$ROKSBNKCTL" k get nodes \
    | assert_contains "Ready" "D3b nodes Ready (no host kubectl)"
```

Use the env-PATH approach (don't `mv` the binary — that mutates the host). Document the trick in `docs/E2E_TEST.md`.

### 5. Update `docs/E2E_TEST.md`

Reflect the D3 + D3b changes. Note that D3b validates the v0.8 "no kubectl required" claim. If you didn't get to the kind-based CI integration job, document the gap.

### 6. CONTRIBUTING.md "Running golden tests" section

Append a section to CONTRIBUTING.md (do not edit the staff-engineer agent's content) documenting:
- `make test-live` requires a real ROKS cluster + KUBECONFIG pointing at it
- `kubectl` must also be on PATH for the comparison side
- Byte equivalence is checked modulo `managedFields/resourceVersion/creationTimestamp`
- Run before tagging v0.8

### 7. Doctor regression check

The staff agent downgrades `kubectl` + `oc` from "needed" to informational. Verify (after staff lands their commit) that doctor on a kubectl-less host doesn't produce warnings for those rows. File an issue if behaviour diverges from PRD 02's spec.

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean (unit suite — your `_test.go` files green; staff's production code passes)
- `go test -tags live -timeout 5m ./internal/k8s/...` works against a real cluster (run only if KUBECONFIG points at one; skip + note in issue file if not)
- `bash -n scripts/e2e-test.sh` clean
- `DRY_RUN=1 PHASE_FROM=D ./scripts/e2e-test.sh` shows the new D3 + D3b cleanly
- `gofmt -d -l .` clean for any Go file you touch

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint2_validator.md`. Same format as Sprint 1. `Severity: roadmap` for forward-looking observations (e.g., "kind-based CI integration would be valuable in Sprint 4 alongside PRD 03's k8s backend tests").

## Final report (under 200 words)

- Files created
- Files edited
- Test results (unit + golden if KUBECONFIG available)
- Issues filed (counts by severity)
- Whether `DRY_RUN=1` shows D3 + D3b cleanly
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
