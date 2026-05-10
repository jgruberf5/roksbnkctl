# Sprint 0 — staff engineer issues

## Issue 1: pre-existing gofmt violations across the codebase
**Severity**: medium
**Status**: open
**Description**: The new `scripts/pre-commit.sh` and the new gofmt step in
`.github/workflows/ci.yml` both fail today because eight files predating
this sprint are not gofmt-clean. The doctor refactor itself is clean, but
the next commit anybody makes will trip the new hook unless these are
fixed first.

`gofmt -d -l .` finds (as of this sprint):
- `internal/cli/cluster_phase.go`
- `internal/cli/install.go`
- `internal/cli/self.go`
- `internal/config/paths.go`
- `internal/config/workspace.go`
- `internal/test/result.go`
- `internal/tf/doc.go`
- `internal/tf/terraform.go`

All are alignment-tab vs. spaces nits inside struct literals or
`var ( ... )` blocks — `gofmt -w .` resolves them in one pass with no
behavioural change.

**Files affected**: the eight files listed above.
**Proposed fix**: integrator runs `gofmt -w .` and folds the result into
the same commit (or a sibling cleanup commit) that lands the new
pre-commit hook + CI gofmt step. Not done in this agent's output to
avoid touching files outside the staff scope.

## Issue 2: Makefile target collisions with existing recipes
**Severity**: low
**Status**: resolved
**Description**: The Sprint 0 spec asked for `build:` and `test:` targets
in the Makefile. Both already exist with strictly richer recipes:
- existing `build` wires `-ldflags` for `Version`/`Commit`/`BuildDate`
  stamping and outputs to `bin/roksbnkctl`
- existing `test` is identical to the spec (`go test ./...`)

Per the agent brief ("If you find conflicting target names, file an
issue rather than overwriting"), the existing recipes were preserved and
only `test-short`, `lint`, and `pre-commit-install` were appended. A
short note in the Makefile points at this issue for the rationale.

**Files affected**: `/mnt/d/project/roksbnkctl/Makefile`
**Proposed fix**: leave existing recipes; the appended block plus the
inline comment is the resolution.

## Issue 3: Check struct extended with a Why field beyond the spec
**Severity**: low
**Status**: open
**Description**: The Sprint 0 spec's `Check` struct has five fields
(`Name`, `Status`, `Detail`, `Optional`, `BackendName`). The pre-refactor
doctor output renders a "(why roksbnkctl cares)" parenthetical alongside
each row, and byte-identical output was a hard requirement. To preserve
that without adding a sixth public field on `Check`, the refactor stashes
the why blurbs in a package-private side-channel (`lastWhys`) populated
by `Run` and consumed by `PrintResults`. This works because the CLI calls
the two sequentially and never concurrently.

If a future caller invokes `Run` from a goroutine while another renders
results, the side-channel will race. The risk is low (only `meta.go`
calls `Run` today) but worth flagging.

**Files affected**: `/mnt/d/project/roksbnkctl/internal/doctor/doctor.go`,
`/mnt/d/project/roksbnkctl/internal/doctor/check.go`
**Proposed fix**: when Phase 3 per-backend checks land, either add a
`Why string` field to `Check` (cleanest) or change `Run` to return a
struct that pairs `[]Check` with `[]string`. Either option drops the
side-channel.

## Issue 4: existing doctor `Status` type replaced rather than retained
**Severity**: low
**Status**: open
**Description**: The pre-refactor doctor exported a `Status` type with
constants `StatusOK`, `StatusWarn`, `StatusFail`. The refactor replaces
those with `CheckStatus` + `StatusOK`, `StatusWarning`, `StatusError`,
`StatusSkipped` (per spec). The two type names would have collided on
`StatusOK`, so keeping both side-by-side wasn't possible without
renaming.

A grep of the codebase confirms no caller outside `internal/doctor`
imported any of the legacy symbols (`CheckResult`, `Status`,
`StatusWarn`, `StatusFail`), so the replacement is safe — but if any
out-of-tree consumer existed (none known), they'd break.

**Files affected**: `/mnt/d/project/roksbnkctl/internal/doctor/doctor.go`
**Proposed fix**: none needed; flagging for the integrator's awareness.
