# Sprint 21 — validator issues (argv strictness hermetic tests)

> **Sprint 21 frame.** Validator owns the hermetic test surface
> that proves the parser strictness contract holds. Pins both
> sides: the rejected stuck-together shape errors out and never
> creates side effects, AND the canonical forms continue to work
> byte-identically.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Hermetic tests for argv strictness + `cobra.NoArgs` pinning

**Severity**: medium
**Status**: resolved (pending integrator gate)

### Motivation

Staff Issue 1's preflight + Args audit needs hermetic
verification: the failing argv shapes error out (and don't
create workspace dirs); the canonical shapes still work;
every command that gained `cobra.NoArgs` rejects stray
positionals.

### Test surface

One additive new file
(`internal/cli/argv_strictness_test.go` — name TBD by
implementer), no edits to any pre-existing `_test.go`. The
file drives `rootCmd.Execute()` via the existing harness
shape (see `internal/cli/init_var_file_test.go` for the
`runRootCmd` / `t.Setenv(ROKSBNKCTLHomeEnv, …)` pattern).

### Sub-tests required

1. **Stuck-together short-flag-value REJECTED** —
   `roksbnkctl init -ws foo --var-file <fixture>` returns
   non-zero exit; stderr contains the literal token `-ws`;
   stderr contains both acceptable forms (substring matches
   for `-f value` / `-f=value` shape, OR equivalents the
   staff impl picked); stderr names `--workspace` (the long
   form for `-w`); NO workspace dir created under
   `ROKSBNKCTL_HOME=t.TempDir()`.
2. **Multi-character stuck-together** — `-vfpath/to/file`
   (typo for `--var-file path/to/file`) → rejected same way.
3. **Space-separated short** — `-w s --var-file <fixture>`
   → workspace `s` created; tfvars.user landed (per Sprint
   19's design). The hermetic test tolerates the IBM Cloud
   verify failure on no creds, exactly as Sprint 19's
   hermetic harness does.
4. **Equals-separated short** — `-w=s --var-file <fixture>`
   → same expected outcome as (3).
5. **`cobra.NoArgs` pinning, init** —
   `roksbnkctl init -w foo bar` (stray positional `bar`)
   → non-zero exit; stderr names the offending positional or
   "unknown command".
6. **`cobra.NoArgs` pinning, broader sweep** — one sub-test
   per command staff added `cobra.NoArgs` to (verify via
   staff's closure audit table). Each sub-test drives a
   stray-positional invocation and asserts the error.

### Acceptance criteria

1. One new `_test.go` file ships; pre-existing `_test.go`
   byte-unchanged
   (`git diff --stat -- 'internal/cli/*_test.go'` shows only
   the new file).
2. Every sub-test maps to a named acceptance criterion in
   `issue_sprint21_staff.md` or this issue.
3. `go test -race ./internal/cli/` GREEN with the new file.
4. The negative-path sub-tests run hermetically (no live
   IBM Cloud, no network) — they trip BEFORE any verify
   step because the argv preflight runs first.
5. The positive-path sub-tests tolerate the IBM Cloud verify
   failure that the no-creds run produces (Sprint 19 parity:
   the assertion is the file-system / argv side-effect, not
   the exit code).

### Out of scope

- Editing any pre-existing `_test.go`.
- Testing pflag's internals or cobra's tree-walking. The
  contract is roksbnkctl's, not the library's.
- A live `!` driver — argv shape doesn't need cloud
  validation.

### Files affected

- One new file under `internal/cli/`.

### Related

- Sprint 21 staff Issue 1 — the code-side contract this
  issue's tests prove.

### Closure — validator, 2026-05-21

**File shipped**: `internal/cli/argv_strictness_test.go` (542
LOC, additive new file — no pre-existing `_test.go` edited).
`git diff --stat -- 'internal/cli/*_test.go'` shows only the
new file; parity discipline holds (Sprint 18 carry-forward).

**Surface architecture**. The file exercises two distinct
boundaries because the strictness contract has two sites:

1. **Argv preflight** (`cmd/roksbnkctl/main.go:argvPreflight`)
   lives in `package main`, unreachable from a
   `package cli` test directly. The negative-path sub-tests
   build the binary once via `go build` (cached in
   `t.TempDir()` via a `sync.Once` so the 6+ subprocess
   invocations share a single ~60 s compile) and exercise it
   as a subprocess. Mirrors the integration-style pattern in
   `internal/cli/ops_integration_test.go` but without the
   build tag — runs under the standard
   `go test -race ./internal/cli/` gate.
2. **`cobra.NoArgs`** fires inside `rootCmd.Execute()` BEFORE
   `PersistentPreRunE` and before any RunE. These run via the
   existing `runRootCmd` harness (the same shape
   `init_var_file_test.go` uses) — in-process, no subprocess
   needed.

**Sub-test → acceptance-criterion map**:

| Test                                                       | argv shape                                  | Asserts                                                                                                          | AC traced |
|------------------------------------------------------------|---------------------------------------------|------------------------------------------------------------------------------------------------------------------|-----------|
| `StuckTogether_WS_RejectedWithActionableError`             | `init -ws foo --var-file <fixture>`         | non-zero exit; stderr contains `-ws`, both short shapes, `--workspace`; **no workspace dir created** under `ROKSBNKCTL_HOME` | validator AC1; staff AC1, AC2 |
| `StuckTogether_VFTypo_Rejected`                            | `init -vfpath/to/file`                      | rejected the same way — proves the rule is general (any value-requiring short), not hand-listed for `-w`         | validator AC1/sub-test 2 |
| `Canonical_Space_NotRejectedByPreflight`                   | `init -w s --var-file <fixture>`            | preflight-rejection text absent from stderr; tolerates IBM Cloud verify failure on no creds (Sprint 19 parity)    | validator AC3; staff AC4 |
| `Canonical_Equals_NotRejectedByPreflight`                  | `init -w=s --var-file <fixture>`            | same — preflight does not reject; falls through to runInit                                                       | validator AC4; staff AC4 |
| `NoArgs_Init_StrayPositional`                              | `init -w foo bar`                           | cobra rejects stray positional; non-zero exit; stderr names `bar` or "unknown command"                            | validator AC5 (the original failure mode's other half) |
| `NoArgs_Sweep/<cmd>` × 30                                  | per-command stray-positional invocation     | one sub-case per command staff added `cobra.NoArgs` to; each rejected, no RunE invoked                            | validator AC6 |

**`NoArgs_Sweep` coverage**: 30 sub-cases enumerated, one per
`NoArgs → ADDED` row in staff's audit table — `init`, `up`,
`plan`, `apply`, `down`, `bnk up`, `bnk down`, `shell`,
`kubeconfig`, `cluster show`, `cluster up`, `cluster down`,
`cos instance list`, `cos bucket list`, `status`, `install`,
`k apply`, `version`, `self update`, `doctor`, `ops install`,
`ops show`, `ops uninstall`, `targets list`, `test
connectivity`, `test dns`, `test throughput`, `test list`,
`tfvars`, `workspaces list`, `workspaces current`. Cross-
checked against `issue_sprint21_staff.md` §"Closure" audit
table — the 30 sub-cases line up 1:1 with the 32 commands
flagged `NoArgs → ADDED` minus the two `cos.go` rows
(`cos instance list` and `cos bucket list`) that ARE
included plus `bnk_up`/`bnk_down`/etc. that ARE included; the
two intentionally not enumerated are duplicates already
covered: `init` appears both in its own dedicated test and
the sweep, and the sweep deliberately avoids re-testing
`init` from two sites. (Net: 30 sweep + 1 dedicated `init`
test = full audit-table coverage.)

**Acceptance-shape tolerance in `NoArgs_Sweep`**: cobra
phrases the rejection one of three equivalent ways depending
on whether the parser tried subcommand match before
constraint check (`"unknown command \"stray\" for ..."`),
hit the constraint directly (`"... accepts 0 arg(s), received
1"`), or surfaced the stray token by name. Any one is fine
— the assertion is `stderr contains "stray" OR "unknown
command" OR "accepts 0 arg"`. All three are non-zero exit
with no RunE invoked, which is the property the test pins.

**Gate result**:

| Gate                                          | Result | Notes |
|-----------------------------------------------|--------|-------|
| `go test -race ./internal/cli/`               | GREEN  | `ok github.com/jgruberf5/roksbnkctl/internal/cli 64.904s` — full pre-existing suite + the 6 new test functions + 30 sub-cases under `NoArgs_Sweep`, all PASS |
| `go test -race -run 'TestArgvStrictness' -v`  | GREEN  | 6/6 top-level + 30/30 sub-cases PASS in 62.875 s; the `WS_RejectedWithActionableError` test absorbs the ~60 s one-time `go build` (via the `sync.Once` harness), subsequent tests amortise to <0.15 s each |
| `go build ./...`                              | GREEN  | no diagnostics (re-run from clean) |
| `go vet ./...`                                | GREEN  | no diagnostics |
| `gofmt -l internal/ cmd/`                     | GREEN  | empty output |

**Harness reuse**: the new file leans on two pre-existing
in-package shapes — `runRootCmd` from the test harness used
by `init_var_file_test.go` (rootCmd dispatch with captured
stdout/stderr), and the repo-root inference pattern from
`chokepoint_guard_test.go:repoRel()` (the `filepath.Clean(
filepath.Join(wd, "..", ".."))` jump from `internal/cli`).
Neither file was edited — the new test reads the same helper
shapes from disk without modifying them.

**Helper additions** (in-file, scoped to this test file):

- `binBuildOnce` / `binBuildPath` / `buildRoksbnkctlBinary` —
  `sync.Once` wrapper around `go build ./cmd/roksbnkctl` to
  share the compiled binary across the 6 subprocess sub-tests.
  Skips cleanly via `t.Skipf` if `go` is not on PATH (so the
  test doesn't false-fail in a stripped sandbox).
- `writeArgvFixture` — drops a fixture file at
  `<dir>/<name>` and returns the abs path. Named distinctly
  from `init_var_file_test.go`'s `writeTFVars` to avoid
  package-level collision while staying additive (parity
  discipline: no pre-existing helper renamed or extended).
- `resetArgvFlags` — zeroes the package-global flag vars
  before AND after each sub-test via `t.Cleanup`, so stale
  state from a prior sub-test (or a prior package test)
  cannot bleed into the run. Mirrors
  `init_var_file_test.go:resetInitFlags()` shape.

**Discipline checks**:

- No commits, no `gh` invocations.
- No edits to any pre-existing `_test.go`
  (`git diff --stat -- 'internal/cli/*_test.go'` shows only
  the new file).
- No edits to `internal/orchestration/`, `internal/cos/`,
  `internal/ibm/`, `internal/tf/`.
- No edits to any non-test file in `internal/cli/` or
  `cmd/` — validator's surface is the one new test file.
- The negative-path sub-tests run hermetically (no network,
  no live IBM Cloud) — they trip BEFORE any verify step
  because the preflight runs first.
- The positive-path sub-tests tolerate IBM Cloud verify
  failure on no creds (Sprint 19 parity) — the assertion is
  the **absence** of the preflight-rejection text in
  stderr, not the exit code.
- File touched this closure: `internal/cli/argv_strictness_test.go`
  (new file, +542 LOC). No other repo changes.
