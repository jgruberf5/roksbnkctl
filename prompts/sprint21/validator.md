You are the **validator** agent for Sprint 21 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first

1. `prompts/sprint21/README.md` — integrator decisions.
2. `issues/issue_sprint21_validator.md` Issue 1.
3. `internal/cli/` test files for the cobra-execution test
   shape — `internal/cli/init_var_file_test.go` is a good
   reference (drives `rootCmd.Execute()` via `runRootCmd` with
   captured stdout/stderr).
4. `cmd/roksbnkctl/main.go` once staff lands — to know exactly
   where the preflight runs and how it can be exercised by a
   test.

## Tasks

Author one new test file (additive, additive ONLY — no edits
to any pre-existing `_test.go`) covering:

1. **Stuck-together short-flag-value REJECTED.**
   - `roksbnkctl init -ws foo --var-file ./fixture.tfvars`
     → non-zero exit, stderr names `-ws`, stderr names BOTH
     acceptable forms (`-f value` and `-f=value` substrings or
     equivalents), stderr names `--workspace` as the long form,
     NO workspace dir created under `ROKSBNKCTL_HOME`.
   - `roksbnkctl init -vfpath/to/file` (stuck-together
     `--var-file` typo) → rejected.
2. **Canonical short-flag-value forms ACCEPTED.**
   - `roksbnkctl init -w s --var-file ./fixture.tfvars`
     (space) → workspace `s` is created. Treat the IBM Cloud
     verify failure (no live creds) as expected; the
     assertion is the workspace dir + tfvars copy landed
     before the verify, per Sprint 19's design.
   - `roksbnkctl init -w=s --var-file ./fixture.tfvars`
     (equals) → workspace `s` is created.
3. **`Args: cobra.NoArgs` pinning.**
   - `roksbnkctl init -w foo bar` (stray positional) →
     non-zero exit, stderr names `bar` or "unknown command" or
     equivalent — the test asserts non-zero exit + the
     positional appears in the error.
   - Sub-tests for every other command staff added the
     `cobra.NoArgs` constraint to (verify via staff's closure
     audit table). One sub-test per command, driving a
     stray-positional invocation, asserting the error.
4. **Hermetic harness** — `ROKSBNKCTL_HOME` pointed at a
   `t.TempDir()`; the IBM verify step's failure on no creds
   is tolerated (matches the Sprint 19 hermetic shape); the
   assertions never depend on network or live IBM Cloud.

## Out of scope

- Editing any pre-existing `_test.go`. Strict additive parity.
- A live `!` driver. This sprint is purely argv-shape;
  hermetic tests are sufficient closure per the README.
- Testing pflag's internals. The strictness contract is
  ROKSBNKCTL's, not pflag's.

## Acceptance criteria

1. One new test file ships
   (`internal/cli/argv_strictness_test.go` or similar).
2. `go test -race ./internal/cli/` GREEN with the new file.
3. Pre-existing `_test.go` byte-unchanged
   (`git diff --stat -- 'internal/cli/*_test.go'` shows only
   the new file).
4. Every sub-test maps explicitly to an acceptance criterion
   in `issues/issue_sprint21_staff.md` or
   `issues/issue_sprint21_validator.md`.
5. `bash -n` not relevant (no shell driver this sprint —
   the entire surface is the Go test file).

## Closure

Write your closure to
`issues/issue_sprint21_validator.md` §"Closure — validator,
<date>". Include: the test file path, sub-test → AC map,
the `go test -v ./internal/cli/ -run TestArgvStrictness`
output, the `git diff --stat -- 'internal/cli/*_test.go'`
result. Flip status `open` → `resolved` (pending integrator
sign-off if any sub-test depends on staff landing first).
