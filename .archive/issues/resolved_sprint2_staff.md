# Sprint 2 — staff engineer issues, resolution notes

Six issues filed: 2 resolved during the agent's own work, 1 deferred to Phase 2.1 with rationale documented, 2 informational accepted, 1 (Issue 6) effectively resolved by the validator's reconciliation.

## Issue 1 (top-level `apply` alias collides with terraform `apply`) — resolved by agent

PRD 02 listed `apply` as a top-level alias for `k apply`. Adding it would shadow the existing `roksbnkctl apply` lifecycle verb (which runs `terraform apply`). Staff agent dropped the `apply` alias and shipped only `get` and `logs` aliases. Chapter 24 reflects the same caveat. Documented in `internal/cli/k_aliases.go`.

**Status**: ✅ resolved by agent during sprint

## Issue 2 (doctor downgrade) — resolved by agent

`kubectl` and `oc` rows in `roksbnkctl doctor` now produce `StatusOK` with `internalised; passthrough still works if installed` detail when the binary is missing, instead of the prior `StatusWarning`. Verified by validator (their Issue 6).

**Status**: ✅ resolved by agent during sprint

## Issue 3 (OpenShift typed clients) — accepted, deferred to Phase 2.1

`github.com/openshift/client-go` integration deferred. `BuildOpenShiftClient` reserved as the function name in `internal/k8s/openshift.go` (currently a stub). PLAN.md and PRD 02 explicitly mark this as Phase 2.1 work. Sprint 5 polish or earlier as scope allows.

**Status**: ⏸ deferred to Phase 2.1

## Issue 4 (apply -k kustomize doc boundary) — accepted, behaviour matches kubectl

Staff's auto-detect of `kustomization.yaml` when `-f <dir>` is passed. `kubectl` itself accepts both `-k` and `-f <kustomize-dir>` interchangeably; staff matches that. No fix needed.

**Status**: ✅ noted; matches kubectl

## Issue 5 (go.mod additions) — accepted, integrator note

Added: `k8s.io/cli-runtime v0.30.0`, `k8s.io/kubectl v0.30.0`, `sigs.k8s.io/kustomize/api v0.17.2`, `sigs.k8s.io/kustomize/kyaml v0.17.1`. All four are required for the kubectl-internalisation surface. The PRD 02 spec called these out; sizing matches expectation. CI uses `go-version-file: go.mod` (Sprint 1 setup) so version drift tracks automatically.

**Status**: ✅ accepted; documented for v0.8 release notes

## Issue 6 (validator API mismatch) — resolved by validator's reconciliation

Mid-flight observation: validator's `*_test.go` files initially used an API shape that didn't match staff's final implementation. Specifically `apply_test.go` referenced `Apply(ApplyOptions{Dynamic, Paths, Stdin})` while the shipped surface is `(*ApplyOptions).Run(ctx)` with `Filename`.

By the time the validator agent submitted, the tests had been reconciled against staff's actual shipped surface. The default `go test ./...` is green: 41 tests in `internal/k8s/...`, all passing. Tag-gated tests (`//go:build live` on `golden_test.go`) only — no `k8sinternal` tag in any final test file.

**Status**: ✅ resolved by validator during sprint
**Verification**: `go test ./internal/k8s/... -count=1` → ok (41 tests)
