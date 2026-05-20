---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: `internal/orchestration` 12.1% coverage — `lifecycle.go` + `cluster.go` (1746 LoC, the post-Sprint-16 home of `RunUp`/`RunDown`/`RunPlan`/`RunApply`/`RunShell`/`RunExec`/`RunKubeconfig`/`Run*Passthrough`) have no direct unit tests'
labels: []
assignees: ''
---

## Symptom

`HOME=$(mktemp -d) KUBECONFIG= go test -cover ./...` from
`/mnt/c/project/roksbnkctl` reports **`internal/orchestration`
coverage: 12.1%** — the lowest of any package with tests in the
repo (next-lowest: `internal/cli` 16.9%, `internal/cos` 19.2%,
`internal/ibm` 19.1%). Run output verbatim, recorded in this
sprint's closure section.

The package has six source files —
`applied_replay.go`, `chokepoint.go`, `cluster.go`, `env.go`,
`lifecycle.go`, `second_phase_reuse.go` — but only three test
files: `applied_replay_test.go`, `chokepoint_test.go`,
`second_phase_reuse_test.go`. The two largest files,
`lifecycle.go` (1061 LoC, exports `RunUp`/`RunTrialUp`/`RunPlan`/
`RunApply`/`RunDown`/`RunTrialDown`/`WriteAndInit`/`ApplyWithRetry`/
`TryAutoKubeconfig`/`TryAutoClusterJumphosts`) and `cluster.go`
(685 LoC, exports `RunShell`/`RunExec`/`RunKubeconfig`/
`RunKubectlPassthrough`/`RunOCPassthrough`/`RunIBMCloudPassthrough`/
`ExtractWorkspaceFlag`/`ExtractOnFlag`/`ResolveBackendSpecWith`) —
the post-Sprint-16-phase-1b home of *every* RunE body the cli layer
delegates to — are not exercised by any test in the
`orchestration` package itself.

Sprint 16's behavior-parity gate
(`issues/issue_sprint16_validator.md` Issue 1) is GREEN because the
move from `internal/cli` to `internal/orchestration` was
byte-identical at the test-result level: the *cli-layer* tests
(`lifecycle_e2e_test.go`, `lifecycle_e2e_integration_test.go`,
`env_split_test.go`, `bnk_phase_test.go`, ...) still pass because
they thread through the thin cli adapters into the orchestration
bodies. That is a *parity* statement, not a *coverage* statement.
The orchestration package's own coverage measurement does not
benefit — and a future refactor that breaks `RunDown` *without*
also breaking the cli-layer integration tests passes the full
hermetic gate.

This is the same defect class Sprint 16 Issue 2 fell into: hermetic
GREEN, real-world RED. The Sprint 16 round-1 fix code path
(`tf.RenderTFVarsWithClusterOutputs` + `WriteAndInit`) was covered
by the staff-added `internal/tf/secondphase_handoff_test.go` and
*still* shipped broken because the test exercised the renderer in
isolation, not the `RunUp`/`RunTrialUp` orchestration shape that
calls it. The orchestration-side cracks are exactly where
hermetic-only coverage stops helping.

## Reproduction

```
# 1. fresh checkout, hermetic env (matches the validator-closure shape):
cd /mnt/c/project/roksbnkctl
HOME=$(mktemp -d) KUBECONFIG= go test -cover ./... 2>&1 | tail -20

# 2. observe the orchestration line:
#   ok  github.com/jgruberf5/roksbnkctl/internal/orchestration  0.258s  coverage: 12.1% of statements
# (12.1% — second-lowest after cmd/roksbnkctl's 0.0%.)

# 3. list the source vs test files in the package:
ls internal/orchestration/*.go | grep -v _test.go
ls internal/orchestration/*_test.go

# Source files: applied_replay.go chokepoint.go cluster.go env.go
#               lifecycle.go second_phase_reuse.go
# Test files:   applied_replay_test.go chokepoint_test.go
#               second_phase_reuse_test.go
# Missing: any test that imports orchestration and calls
# RunUp/RunTrialUp/RunPlan/RunApply/RunDown/RunTrialDown/RunShell/
# RunExec/RunKubeconfig/Run*Passthrough/ExtractWorkspaceFlag/
# ExtractOnFlag/ResolveBackendSpecWith.
```

## Expected behavior

`internal/orchestration` carries direct unit-test coverage of the
exported RunE-equivalents that landed during Sprint 16 phase-1b —
not just the helper-shaped second_phase_reuse / applied_replay /
chokepoint corners. Coverage cleared **40%** is a reasonable
near-term target; cleared 60% (the median of the other packages)
is the longer arc. A regression that breaks `RunDown`'s var-file
ordering, `ExtractOnFlag`'s flag-parsing precedence, or
`ResolveBackendSpecWith`'s context-resolution surfaces as a failed
orchestration-package test, *not* solely as a failed cli-layer
integration test that runs an order of magnitude slower.

## Actual behavior

The package's only direct tests cover the three smallest source
files. `lifecycle.go` + `cluster.go` (1746 LoC combined — the bulk
of the Sprint 16 phase-1b move) are exercised exclusively through
the cli adapters' tests. A regression internal to `RunUp` that
happens to be masked by the cli-layer fixture passes
`go test ./internal/orchestration/`.

## Environment

- `roksbnkctl version`: N/A — this is a test-coverage bug.
- OS / arch: Linux x86_64 (the validator's sandbox; reproduces on
  macOS arm64 per the integrator's local runs).
- IBM Cloud region: N/A.
- Backend: N/A (hermetic).

## Suspect pipeline / hypotheses (optional)

1. **Most likely:** Sprint 16 phase-1b was a behavior-parity move,
   not a coverage move. The Sprint 16 validator gate proves the
   *test results* didn't change byte-for-byte; nothing in the gate
   notices that the *coverage attribution* shifted from
   `internal/cli` to `internal/orchestration` without the
   corresponding test files following. The integrator's invariant
   "no edits to existing `_test.go`" during phase-1b is what kept
   the gate clean and is also what left the new package thinly
   tested in its own right.
2. **Second:** the easy-to-add tests (helper functions like
   `ExtractOnFlag`, `ExtractWorkspaceFlag`,
   `ResolveBackendSpecWith`) need no fixtures and are pure
   string-in / string-out — they're 5-line tests that should have
   been written when the functions moved. They weren't.
3. **Third:** the harder targets (`RunUp`/`RunDown` with their
   `tf.Workspace` / function-field DI dependencies) want a small
   fake `tf.Workspace` + `LifecycleInputs` builder. That fake
   doesn't exist yet and is its own one-time investment.

## Acceptance criteria

1. New test file
   `internal/orchestration/cluster_helpers_test.go` (or similar
   name — *new file*, no edits to existing `_test.go`) exercises
   the pure helpers: `ExtractWorkspaceFlag`, `ExtractOnFlag` /
   `extractOnFlag`, `extractBackendFlag`, `ResolveBackendSpecWith` /
   `resolveBackendSpecWith`, `envValue`, `applyWorkspaceFlag`,
   `clusterFromTFOutput`. Each gets ≥3 table-driven subtests
   covering present/absent/edge-case input. Together these are
   ≥150 LoC of orchestration code that today is statement-uncovered
   from this package's tests.
2. New test file
   `internal/orchestration/lifecycle_helpers_test.go` exercises the
   pure helpers in `lifecycle.go`: `terraformBackendSpec`,
   `resolveClusterIdentity` (with a mocked `tf.Workspace`), and the
   defensive `openTF` happy-path/no-api-key branches. ≥3 subtests
   each.
3. New test file
   `internal/orchestration/run_lifecycle_test.go` introduces a tiny
   in-package `fakeTFWorkspace` (or borrows the pattern
   `applied_replay_test.go` uses) and a `LifecycleInputs` builder,
   then exercises `RunPlan` and `RunTrialDown` against happy
   path + a missing-var-file failure path + an
   `cluster-outputs.json`-present second-phase reuse path. ≥3
   subtests, focused on the orchestration's *own* logic (var-file
   layering order, snapshot replay, second-phase override-file
   emission) — not terraform's behavior.
4. `HOME=$(mktemp -d) KUBECONFIG= go test -cover
   ./internal/orchestration/...` reports coverage **≥35%** after
   landing — measurably more than today's 12.1%. (Stretch: ≥50%.)
   The number is asserted in this issue's closure section so a
   future regression makes a value visible.
5. The full hermetic gate
   `HOME=$(mktemp -d) KUBECONFIG= go test -race ./...` stays green
   end to end. The new tests add **no** dependency on
   `internal/cli`, preserving the orchestration⊄cli boundary (the
   companion boundary-grep issue's gate must stay green).
6. Regression: the Sprint 16 Issue 2 round-1 mistake (the second
   phase still managing duplicate cluster-shared modules even when
   `cluster-outputs.json` exists) is *test-observable* from this
   package's own tests after this lands — there is a subtest under
   criterion 3 that fails if `RunTrialUp`'s emitted
   `bnk-phase-override.tfvars` is missing `create_roks_cluster =
   false`. The cli-layer integration test that catches it today
   stays; the orchestration-layer test is added.

## Out of scope (deliberately)

- Edits to *any* existing `_test.go` file. Validator constraint;
  also the right call — every Sprint 16 parity-proof test stays
  byte-identical.
- Pushing coverage of `internal/cli` (currently 16.9%). Different
  package, different fixtures, different issue if it bites.
- Pushing coverage of `internal/cos` (19.2%) and `internal/ibm`
  (19.1%) — both are SDK-wrapper packages where the right test is
  an integration-tagged test against a real backend; the hermetic
  unit-coverage number is misleading there. File separately if
  needed.
- Removing the cli-layer integration tests
  (`lifecycle_e2e_test.go`, `bnk_phase_test.go`, etc.). Those stay
  — they catch the cross-package wiring. This issue is the
  in-package unit tier that was missed.
- Re-running the behavior-parity gate against `v1.6.0`. That's
  Sprint 16's job and is closed.

## Files likely touched

- `internal/orchestration/cluster_helpers_test.go` — new file
  (≥250 LoC, table-driven).
- `internal/orchestration/lifecycle_helpers_test.go` — new file
  (≥150 LoC).
- `internal/orchestration/run_lifecycle_test.go` — new file
  (≥200 LoC including the local `fakeTFWorkspace`).
- `internal/orchestration/fake_tf_workspace.go` — new helper file
  (NOT a `_test.go` — needs to be importable by all three new test
  files; alternatively put it in a `testfixtures.go` with a build
  tag, or inline it in `run_lifecycle_test.go` and live with the
  smaller scope). Implementer picks the shape.
- No source file edits in `cluster.go` / `lifecycle.go` /
  `env.go` — purely additive tests.

## Notes

- The exact coverage measurement for this issue's evidence (from
  the Sprint 17 validator closure):
  `ok  github.com/jgruberf5/roksbnkctl/internal/orchestration
  0.258s  coverage: 12.1% of statements`. The full output is in
  `issues/issue_sprint17_validator.md` §Closure.
- The Sprint 16 phase-1b move was a behavior-preserving refactor;
  the coverage hole is a *consequence* of that move's tight scope,
  not a defect in the move itself. This issue closes the
  consequence.
- This issue's lift is most cost-effective when paired with the
  boundary-grep gate (issue 02) — both work on the same package,
  both ratify the post-Sprint-16 orchestration package as a
  first-class testable surface.
