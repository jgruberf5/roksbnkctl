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
**Status**: open

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
