# Sprint 2 — validator issues, resolution notes

Eight issues filed: 0 blockers. Most are informational explanations of test-coverage trade-offs (cli-runtime can't be cleanly faked) or roadmap entries for Sprint 4+ test work.

## Issue 1 (fake-clientset coverage narrower than the brief envisaged) — informational, accepted trade-off

The `cli-runtime` `resource.Builder` chain that staff uses for `k get`/`k apply` (chosen specifically to achieve byte-equivalence with kubectl) cannot be faked via `client-go`'s `fake.NewSimpleClientset` — it constructs its own discovery + REST mapping at call time. Validator covered the parts that *are* fakeable (option validation, helper functions, parse/split routines, cascade enums) and left end-to-end behavior to the live golden tests.

This is the **correct trade-off** given PRD 02's byte-equivalence goal — synthesizing kubectl-equivalent output means using kubectl-equivalent machinery, which doesn't fake cleanly. Documented in test-file headers.

**Status**: ✅ accepted as informational; test pyramid is sound

## Issue 2 (kind-based CI integration deferred to Sprint 4) — roadmap

Spinning up a one-node `kind` cluster in CI for end-to-end k8s tests was scoped optional in the validator brief and deferred. PLAN.md sequences kind-based testing into Sprint 4 alongside PRD 03's K8s execution backend (`internal/exec/k8s.go`). Doing it in Sprint 2 would require a second migration when Sprint 4 lands; doing it once in Sprint 4 gets the same coverage cleanly.

**Status**: ⏸ tracked for Sprint 4

## Issue 3 (validator unit tests use staff's API but cannot fake cli-runtime) — same as Issue 1

Subset of Issue 1; same accepted trade-off.

**Status**: ✅ informational

## Issue 4 (golden test byte-equivalence relies on simple line-level diff) — accepted

The byte-equivalence comparison (validator's `golden_test.go`) uses straightforward string compare modulo `managedFields/resourceVersion/creationTimestamp`. A more sophisticated YAML AST diff would catch ordering changes that don't affect semantics; for v0.8 the simple diff is sufficient because both sides go through cli-runtime's printer and produce identical ordering. Worth revisiting only if false-positive flakes appear.

**Status**: ✅ accepted; revisit only on flake

## Issue 5 (e2e D3b PATH-strip — busybox sort/paste portability) — verified portable

D3b uses `tr ':' '\n' | grep -v -E '/kubectl$' | paste -sd:` to strip kubectl from PATH. `paste -sd:` is in both GNU and busybox coreutils with the same semantics. The script's existing dependencies are POSIX; this is consistent.

**Status**: ✅ accepted; portable

## Issue 6 (Doctor regression check) — resolved by verification

Validator confirmed against staff's commit: `roksbnkctl doctor` with kubectl PATH-stripped now shows `StatusOK` with `internalised; passthrough still works if installed` detail rather than a `StatusWarning`. The downgrade behavior matches PRD 02's "drop kubectl from required" goal.

**Status**: ✅ resolved (verified)

## Issue 7 (kustomize `loadKustomization` integration test) — roadmap

A future test that exercises the `apply -f <kustomize-dir>` path against a real kustomize base. PLAN.md doesn't sequence this explicitly; reasonable to fold into Sprint 4's expanded test infrastructure or whenever a real kustomize-based deployment scenario surfaces. Not blocking v0.8.

**Status**: ⏸ tracked roadmap

## Issue 8 (kind-based smoke test alongside PRD 03 K8s backend) — roadmap

Same as Issue 2; PRD 03's K8s execution backend has its own kind requirements and Sprint 4 is the natural home for both.

**Status**: ⏸ tracked for Sprint 4
