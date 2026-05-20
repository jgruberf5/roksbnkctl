---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: CI gate — `internal/orchestration ⊄ internal/cli` one-directional import boundary'
labels: []
assignees: ''
---

## Motivation

Sprint 15 (phase-1a) introduced `internal/orchestration` as a service
layer with the documented invariant that it never imports
`internal/cli` — see the doc-comment headers in
`internal/orchestration/lifecycle.go:57`,
`internal/orchestration/cluster.go:47`,
`internal/orchestration/chokepoint.go:12`, and
`internal/orchestration/second_phase_reuse.go:70`, all of which
assert "this package never imports `internal/cli`" and one of which
claims the boundary is "asserted by the chokepoint-invariant guard
test". Sprint 16 phase-1b moved ~64 KB of lifecycle/cluster RunE
bodies into the layer on the strength of that invariant
(`issues/issue_sprint16_validator.md` Issue 1: "Boundary/import
audit: `internal/orchestration` does **not** import `internal/cli`
(grep-clean — one-directional boundary held under the function-field
dependency-injection shape)"). Sprint 16 validator closure says it
ran the grep by hand.

In fact **no CI gate or unit test asserts this boundary**. The
chokepoint-guard test (`internal/cli/chokepoint_guard_test.go`)
asserts something different — that `flagVarFiles` / `flagTFSource` /
`KUBECONFIG`-classification re-derivation does not creep back into
RunE bodies — and lives in the `cli` package. There is no
greppable / parsing assertion anywhere that
`go list -deps ./internal/orchestration/...` does not include
`github.com/jgruberf5/roksbnkctl/internal/cli`. The invariant rests
on doc-comments, manual integrator grep, and the function-field DI
shape — exactly the regression-prone state Sprint 15's chokepoint
guard exists to retire for the path/env-derivation class.

A one-line `go list` check, gated in CI, would catch a future RunE
import-back regression at PR time instead of in the next phase-1c
audit's manual grep.

## Proposed surface

No new `roksbnkctl` verb — this is a build-time invariant, like the
existing chokepoint guard. The surface is **either** (a) a new step
in `.github/workflows/ci.yml` that runs the grep, or (b) a new
guard test under `internal/orchestration/boundary_guard_test.go`
that does the same check via `go/packages` (so it also runs under
the pre-commit hook). Prefer (b) — keeps the assertion next to the
code, runs under the local hermetic gate, and surfaces in the same
test output developers already read.

```
# Conceptual shape — actual implementation goes in the test file.
go test ./internal/orchestration/ -run TestBoundary_OrchestrationDoesNotImportCLI
```

- New test file `internal/orchestration/boundary_guard_test.go`,
  package `orchestration_test` (so `_test` doesn't cycle back into
  cli either).
- The test calls `packages.Load(&packages.Config{Mode: packages.NeedImports | packages.NeedDeps}, "./...")`, scoped to `./internal/orchestration/...`,
  and walks `pkg.Imports` (transitively) — any path equal to
  `github.com/jgruberf5/roksbnkctl/internal/cli` fails the test with
  the offending parent package and the import chain.
- The test self-runs under `go test ./internal/orchestration/`
  (already in the unit-test set, already runs in `ci.yml`).

## Behavior

- **Happy path (today):** the test passes; no `internal/orchestration/...`
  package's transitive import set contains `internal/cli`.
- **Regression:** a contributor adds
  `import "github.com/jgruberf5/roksbnkctl/internal/cli"` to (for
  example) `internal/orchestration/lifecycle.go` because they need
  some flag-bound symbol. The test fails with:
  `boundary violation: internal/orchestration/lifecycle.go imports
  internal/cli (chain: internal/orchestration → internal/cli)`.
  The fix path is to lift the symbol into the orchestration package
  (or inject it via the existing function-field DI), not to suppress
  the test.
- **Indirect chain:** the same test catches an *indirect* import
  back into cli through a third package — e.g. if a new
  `internal/foo` is created that imports `internal/cli`, and
  `internal/orchestration` imports `internal/foo`. The transitive
  walk surfaces the whole chain in the failure message.
- **Interaction with chokepoint guard:** orthogonal — the chokepoint
  guard catches per-RunE re-derivation, this catches the import
  boundary. Both can fail independently.
- **Side-effects on filesystem / IBM Cloud account:** none — pure
  static analysis.

## Acceptance criteria

1. New test file `internal/orchestration/boundary_guard_test.go`
   (package `orchestration_test`) runs as part of
   `go test ./internal/orchestration/` and as part of CI's
   `go test -race ./...`. The test name reads
   `TestBoundary_OrchestrationDoesNotImportCLI`.
2. The test uses `golang.org/x/tools/go/packages` (already in the
   module graph via the chokepoint guard's `go/ast` neighbours) or
   a `go list -deps -json` shell-out — pick whichever keeps the test
   <80 lines. The implementation walks transitive imports, not just
   direct ones.
3. On the current `main` tree the test passes. A throwaway branch
   that adds `import "github.com/jgruberf5/roksbnkctl/internal/cli"`
   to any file under `internal/orchestration/` fails the test with
   a message naming the offending file and the import chain.
4. Failure message is actionable: it names (a) the orchestration-side
   package that pulled cli in, (b) the full transitive chain, and
   (c) a one-line fix hint pointing at the function-field DI shape
   (`see lifecycle.go's LifecycleInputs for the injection pattern`).
5. The doc-comment claims in
   `internal/orchestration/{lifecycle,cluster,chokepoint,second_phase_reuse}.go`
   that reference "this package never imports `internal/cli`" /
   "asserted by the chokepoint-invariant guard test" are updated to
   name *this* test — the prose and the gate finally agree.
6. The Sprint 17 closure (and every subsequent sprint closure) can
   drop the "grep-clean — boundary held" line: the gate is mechanical,
   not manual.

## Out of scope (deliberately)

- Asserting the inverse direction (`cli ⊂ orchestration`). That's
  the desired direction and is not at risk.
- Extending the gate to other internal/ packages (e.g.
  `internal/tf ⊄ internal/cli`). Worth doing — file as a follow-up
  once this lands and the shape is proven.
- Renaming any existing `internal/orchestration` symbol. Pure
  additive guard; no code moves.
- Replacing the chokepoint guard with this one — different invariant,
  different failure mode, keep both.
- Catching `internal/cli`'s own *test* files importing
  `internal/orchestration` — that's fine and is the documented
  pattern (`chokepoint_guard_test.go` does it explicitly).

## Files likely touched

- `internal/orchestration/boundary_guard_test.go` — new file, the
  guard test itself (~80 lines).
- `internal/orchestration/lifecycle.go` — update the doc-comment at
  line 57 to name the new test.
- `internal/orchestration/cluster.go` — update the doc-comment at
  line 47 likewise.
- `internal/orchestration/chokepoint.go` — update line 12's "asserted
  by the chokepoint-invariant guard test" to "asserted by
  `TestBoundary_OrchestrationDoesNotImportCLI` in
  `boundary_guard_test.go`".
- `internal/orchestration/second_phase_reuse.go` — line 70 doc-comment.
- `go.sum` — may pick up `golang.org/x/tools/go/packages` if not
  already pulled (it is, transitively via `staticcheck`-action's
  toolchain; integrator confirms at landing time).

## Notes

- The chokepoint guard (`internal/cli/chokepoint_guard_test.go`) is
  the precedent shape: a CI-asserted greppable invariant that
  retires a recurring defect class. Same pattern, different invariant.
- The function-field DI shape (`LifecycleInputs`, `ClusterInputs`)
  exists *precisely* to make this boundary holdable; a future
  contributor adding the cli import would be sidestepping that DI,
  which is the symptom this test names.
- The Sprint 16 validator closure ran the grep `grep -rn
  '"github.com/jgruberf5/roksbnkctl/internal/cli"'
  internal/orchestration/` by hand and reported "clean". This issue
  is making that one-liner mechanical so the next phase-1c / phase-2
  audit doesn't depend on whoever's at the keyboard remembering.
