You are the **validator** agent for Sprint 24 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first

1. `prompts/sprint24/README.md` — integrator decisions.
2. `issues/issue_sprint24_staff.md` — CLI surface design.
3. `internal/cli/test.go` — the existing `testCmd` registration.
4. `internal/cli/targets_test.go` (or wherever `targets` tests
   live) — the ergonomic precedent for the test shape.
5. After staff lands their CLI: re-read
   `internal/cli/test_hosts.go` to see the exact subcommand
   shapes + flag names.
6. Sprint 21's `internal/cli/argv_strictness_test.go` — the
   `cobra.NoArgs` / `cobra.MinimumNArgs` testing pattern.

## Notes on staff coordination

Staff runs in parallel with you and will land:
- `internal/cli/test_hosts.go` (new file with the four
  subcommands)
- `internal/cli/test.go:803` error message update
- Possibly a `mutateExtraHosts` helper

You CANNOT pin your hermetic test against staff's final code
until they finish. Do read-only design work first (sketch the
test shape based on the issue file + README scope), then once
staff's commit lands, write the actual test file targeting
their final code.

## Tasks

1. **New file `internal/cli/test_hosts_test.go`** (additive; no
   edits to pre-existing `_test.go` files) covering:
   - **(a)** `add <url>` against empty config persists the URL
     to `cctx.Workspace.Test.Connectivity.ExtraHosts`.
   - **(b)** `add <url>` against already-present URL is no-op
     (slice unchanged) + logs "already present" (or whatever
     staff chose) to stderr.
   - **(c)** `remove <url>` against present URL removes it from
     the slice, preserves remaining-order stability.
   - **(d)** `remove <url>` against absent URL is no-op + logs
     "not present".
   - **(e)** `list` on empty config emits zero bytes on stdout
     + exit 0 (NOT an error — distinguishes "no hosts" from
     "command failed" by exit code).
   - **(f)** `list --output json` (or `-o json` — match staff's
     flag) on empty config emits `[]` (valid JSON array).
   - **(g)** `list` on populated config emits sorted-stable
     insertion-order URLs, one per line.
   - **(h)** `clear` prompt defaults to No — if no `--auto`,
     mock `promptYesNo` to return false → slice unchanged.
   - **(i)** `clear --auto` skips prompt and sets the slice to
     `nil` (or empty `[]` — match staff's implementation).
   - **(j)** `add non-url-text` (e.g. "not a url with spaces")
     errors with an actionable message; slice unchanged.
   - **(k)** `cobra.NoArgs` pin on `list` and `clear`: drive
     `list foo` and `clear bar`; assert each errors at parse
     time. (Mirror Sprint 21's `argv_strictness_test.go`
     pattern.)
   - **(l)** `cobra.MinimumNArgs(1)` pin on `add` and `remove`:
     drive each with zero positional args; assert parse-time
     error.
2. **Test harness**: per `internal/cli/argv_strictness_test.go`'s
   precedent, use `t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())` and
   drive the cobra root command in-process (or subprocess if
   staff's design requires it). Pre-existing test helpers are
   re-usable; new helpers go in your new file only.
3. **Coverage gates**: `go test ./internal/cli/...` PASS
   (your new tests + all pre-existing). `go vet ./...` clean.

## Out of scope

- ANY edit to pre-existing `_test.go` files (parity discipline).
- `internal/cli/test_hosts.go` (staff's surface) or
  `internal/cli/test.go` (staff's one-line error message edit).
- The book — architect's surface.
- `internal/orchestration/`, `internal/config/tfstate.go`,
  `terraform/` — out of scope.

## Acceptance criteria

1. All 12 sub-cases (a-l) implemented as either separate test
   functions or sub-cases under one test function with
   `t.Run`. Each case is independent — no shared state.
2. Tests pass against staff's integrated code.
3. `go test ./internal/cli/...` PASS;  `go vet ./...` clean.
4. No edits to any non-validator file (your only writes go to
   `internal/cli/test_hosts_test.go` + the closure file).

## Closure

Write your closure to
`issues/issue_sprint24_validator.md` (NEW file) §"Closure —
validator, <date>". Include: the test function names + the
12 sub-cases pinned, the `go test` + `go vet` results, and
any future-sprint candidates (e.g. YAML comment preservation
if staff documented that limitation; `test dns` /
`test throughput` CLI as the natural follow-up). Top-of-file
`**Status**: resolved`.

Reply with a concise summary under 200 words: the test
function structure + sub-case count, the test results, and
any follow-ups for the integrator.
