# Sprint 0 — staff engineer issues, resolution notes

Resolution log for `issue_sprint0_staff.md`. Four issues filed: one
medium (gofmt sweep — fixed), two low (Makefile collisions, Why
side-channel — accepted as reasonable), one low (Status type rename —
accepted, no out-of-package callers).

## Issue 1 (pre-existing gofmt violations across the codebase) — fixed

**Resolution**: ran `gofmt -w .` from the repo root; all eight files
listed in the issue (and any others not listed) are now gofmt-clean.
Verified with `gofmt -d -l .` returning empty output. `go build ./...`
and `go test ./...` both still green after the sweep — the changes are
all alignment-tab vs spaces inside struct literals, no behavioral
change.

The new `scripts/pre-commit.sh` and `.github/workflows/ci.yml` gofmt
step now have a green codebase to land against.

**Status**: ✅ resolved
**Files touched**: 8 `internal/...go` files (alignment fixes only)
**Commit**: lands in the Sprint 0 integration commit

## Issue 2 (Makefile target collisions on `build`/`test`) — accepted as-is

**Resolution**: agent's choice to preserve the richer existing recipes
(`build` with `-ldflags` version stamping; `test` already matched the
spec) and append only the new targets is the correct call. No change
needed.

**Status**: ✅ resolved as "accepted; existing recipes are richer than
the Sprint 0 spec defaults; appended block is the right resolution"

## Issue 3 (`Why` field in package-private side-channel) — accepted as-is

**Resolution**: accepting the side-channel pattern. The Sprint 0 spec's
`Check` struct intentionally kept the public surface minimal (5 fields)
to avoid exposing implementation details ahead of the per-backend
checks landing in Phase 3. The `lastWhys` package-private map gives
byte-identical pre/post-refactor output without committing to a public
field that may not survive the Phase 3 design.

If/when Phase 3 needs richer rendering, `Why` (or a richer `Detail`
field) can graduate to public. Until then, the side-channel is fine and
internal-only.

**Status**: ✅ resolved as "accepted; defer richer rendering API to
Phase 3 needs"

## Issue 4 (legacy `Status` type replaced rather than retained) — accepted as-is

**Resolution**: the agent confirmed no out-of-package callers reference
the old `Status` type, so the rename to `CheckStatus` is safe.
Side-by-side preservation would have required namespace gymnastics
(`StatusOK` would collide between the two types) for no callers'
benefit. Clean break is correct.

**Status**: ✅ resolved as "accepted; no out-of-package callers; rename
is safe"
