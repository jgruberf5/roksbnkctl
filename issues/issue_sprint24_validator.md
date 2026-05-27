# Sprint 24 — validator issues (test hosts CLI hermetic tests)

**Status**: resolved

> **Sprint 24 frame.** Validator owns the hermetic test surface that
> proves the `roksbnkctl test hosts {list,add,remove,clear}` CLI staff
> landed in `internal/cli/test_hosts.go` behaves per the issue spec.
> Pins persistence, idempotence, the empty-list exit-0 contract, the
> `--auto` skip-prompt path, URL validation, and the argv strictness
> contract (`cobra.NoArgs` on `list`/`clear`; `cobra.MinimumNArgs(1)`
> on `add`/`remove`).

---

## Issue 1 — Hermetic tests for `test hosts` CLI surface

**Severity**: medium
**Status**: resolved

### Closure — validator, 2026-05-27

**File shipped**: `internal/cli/test_hosts_test.go` (additive new
file; no pre-existing `_test.go` edited — parity discipline holds
from Sprint 18 / 21 / 22 / 23). `git diff --stat -- 'internal/cli/*_test.go'`
shows only the new file.

**Surface architecture**. All sub-cases drive cobra in-process via
the existing `runRootCmd` harness (defined in
`internal/cli/init_var_file_test.go`) wrapped in a local
`runTestHostsCmd` helper. The wrapper redirects `os.Stdout` /
`os.Stderr` through `os.Pipe()` for the duration of the call because
staff's `RunE` family writes directly to those streams (not to
`cmd.OutOrStdout()`), so the bare `cmd.SetOut`/`SetErr` capture would
miss the test-hosts output. The wrapper also folds cobra's internal
SetErr buffer into the captured stderr stream so the
argv-strictness sub-cases see the cobra `MinimumNArgs` /
`NoArgs` wording in one place. Each sub-case seeds a fresh
`ROKSBNKCTL_HOME=t.TempDir()`, writes a minimal workspace via
`config.SaveWorkspace`, points `flagWorkspace` at it, and resets
`flagOutput` + `flagTestHostsClearAuto` — full hermetic isolation.

**Sub-case → acceptance-criterion map**:

| Test function                                          | Sub-case | Asserts                                                                                                                          |
|--------------------------------------------------------|----------|----------------------------------------------------------------------------------------------------------------------------------|
| `TestTestHostsAdd_Persists`                            | (a)      | `add <url>` against empty config persists URL to `Workspace.Test.Connectivity.ExtraHosts` (LoadWorkspace round-trip).            |
| `TestTestHostsAdd_Idempotent_AlreadyPresent`           | (b)      | `add <url>` against present URL is no-op (slice unchanged) + stderr contains `already present`; exit 0.                          |
| `TestTestHostsRemove_Persists`                         | (c)      | `remove <url>` against present URL drops it AND preserves order of remaining entries (no re-sort).                               |
| `TestTestHostsRemove_Idempotent_Absent`                | (d)      | `remove <url>` against absent URL is no-op (slice unchanged) + stderr contains `not present`; exit 0.                            |
| `TestTestHostsList_EmptyZeroBytes`                     | (e)      | `list` on empty config emits zero bytes on stdout AND exit 0 (NOT an error — distinguishes "no hosts" from "command failed").    |
| `TestTestHostsList_EmptyJSONArray`                     | (f)      | `list -o json` on empty config emits literal `[]` (not `null`); decodes as zero-length JSON array.                               |
| `TestTestHostsList_PopulatedOrderStable`               | (g)      | `list` on populated config emits one URL per line in insertion order (no re-sort, no headers, no prefixes).                      |
| `TestTestHostsClear_PromptDefaultsNo`                  | (h)      | `clear` without `--auto` calls `promptYesNo(default=false)`; under `go test` (non-TTY) it returns false → slice unchanged.       |
| `TestTestHostsClear_AutoSkipsPrompt`                   | (i)      | `clear --auto` bypasses prompt; ExtraHosts becomes empty/nil; `extra_hosts` key absent from on-disk YAML (omitempty contract).   |
| `TestTestHostsAdd_NonURL_Errors`                       | (j)      | `add "not a url with spaces"` returns non-nil error containing `invalid host URL`; slice unchanged (fail-before-write).          |
| `TestTestHostsArgs_NoArgs_ListAndClear` / `list_stray` | (k1)     | `test hosts list foo` rejected at parse time (cobra.NoArgs); `unknown command` / `accepts 0 arg` wording.                        |
| `TestTestHostsArgs_NoArgs_ListAndClear` / `clear_stray`| (k2)     | `test hosts clear bar` rejected the same way.                                                                                    |
| `TestTestHostsArgs_MinimumNArgs_AddAndRemove`/`add_zero`| (l1)    | `test hosts add` (zero positionals) rejected at parse time (cobra.MinimumNArgs(1)); `requires at least` / `1 arg` wording.       |
| `TestTestHostsArgs_MinimumNArgs_AddAndRemove`/`remove_zero`| (l2) | `test hosts remove` (zero positionals) rejected the same way.                                                                    |

Twelve sub-cases (a–l), twelve assertion points pinned, all twelve
PASS. The (k) and (l) functions each contain two `t.Run` sub-cases
(`list_stray`+`clear_stray` and `add_zero`+`remove_zero`
respectively), per the issue's "each independent" constraint.

**Test results**:

```
$ go test ./internal/cli/... -run TestTestHosts -count=1 -v
=== RUN   TestTestHostsAdd_Persists
--- PASS: TestTestHostsAdd_Persists (0.01s)
=== RUN   TestTestHostsAdd_Idempotent_AlreadyPresent
--- PASS: TestTestHostsAdd_Idempotent_AlreadyPresent (0.00s)
=== RUN   TestTestHostsRemove_Persists
--- PASS: TestTestHostsRemove_Persists (0.01s)
=== RUN   TestTestHostsRemove_Idempotent_Absent
--- PASS: TestTestHostsRemove_Idempotent_Absent (0.01s)
=== RUN   TestTestHostsList_EmptyZeroBytes
--- PASS: TestTestHostsList_EmptyZeroBytes (0.01s)
=== RUN   TestTestHostsList_EmptyJSONArray
--- PASS: TestTestHostsList_EmptyJSONArray (0.01s)
=== RUN   TestTestHostsList_PopulatedOrderStable
--- PASS: TestTestHostsList_PopulatedOrderStable (0.01s)
=== RUN   TestTestHostsClear_PromptDefaultsNo
--- PASS: TestTestHostsClear_PromptDefaultsNo (0.00s)
=== RUN   TestTestHostsClear_AutoSkipsPrompt
--- PASS: TestTestHostsClear_AutoSkipsPrompt (0.00s)
=== RUN   TestTestHostsAdd_NonURL_Errors
--- PASS: TestTestHostsAdd_NonURL_Errors (0.00s)
=== RUN   TestTestHostsArgs_NoArgs_ListAndClear
=== RUN   TestTestHostsArgs_NoArgs_ListAndClear/list_stray
=== RUN   TestTestHostsArgs_NoArgs_ListAndClear/clear_stray
--- PASS: TestTestHostsArgs_NoArgs_ListAndClear (0.01s)
=== RUN   TestTestHostsArgs_MinimumNArgs_AddAndRemove
=== RUN   TestTestHostsArgs_MinimumNArgs_AddAndRemove/add_zero
=== RUN   TestTestHostsArgs_MinimumNArgs_AddAndRemove/remove_zero
--- PASS: TestTestHostsArgs_MinimumNArgs_AddAndRemove (0.01s)
PASS
ok  	github.com/jgruberf5/roksbnkctl/internal/cli	0.199s

$ go test ./internal/cli/... -count=1
ok  	github.com/jgruberf5/roksbnkctl/internal/cli	65.019s

$ go vet ./...
(clean — no findings)
```

Full cli suite (12 new + all pre-existing) PASS; `go vet ./...`
clean. Parity discipline holds — `git diff --stat -- 'internal/cli/*_test.go'`
shows only the new `test_hosts_test.go` row.

**Comment-preservation note from staff** (per the `mutateExtraHosts`
helper's docstring): the underlying `gopkg.in/yaml.v3` marshaller
does NOT preserve YAML comments — round-tripping a config.yaml that
includes operator comments will drop them. Staff documented this
explicitly as an accepted limitation rather than a blocker. Validator
did NOT pin a test against comment preservation; the issue's
acceptance criteria called out comment-preservation as OPTIONAL.

### Future-sprint candidates

1. **YAML comment preservation across CLI writes**. Staff's
   `mutateExtraHosts` (and by extension every existing
   `config.SaveWorkspace` caller — `targets add`, `init`,
   `workspaces ...`) loses operator comments on round-trip. A
   sprint to switch the marshaller to a comment-preserving
   library (`gopkg.in/yaml.v3` does expose AST node access; the
   work is non-trivial but bounded) would close the gap for ALL
   workspace-config CLIs, not just test hosts.
2. **`test dns` / `test throughput` CLI surface**. The Sprint 24
   README §"Integrator decisions baked in" item 1 explicitly
   deferred CLI for `test.dns.{default_target,resolvers}` and
   `test.throughput.*` because those have working flag-driven
   equivalents (`--target`, `--server`, `--duration`, …). If a
   future operator surfaces friction parallel to the one
   `test hosts` closed (asymmetry between flag-driven probe
   invocation and YAML-driven config), a `test {dns,throughput}
   {list,add,remove,clear}` sweep mirroring the Sprint 24
   ergonomic would be the natural follow-up. The
   `mutateExtraHosts` helper shape is already a reusable
   template for the resolver-map and throughput-config
   equivalents.
3. **Multi-arg `add` / `remove` hermetic coverage**. Staff's
   `add <url> [<url> ...]` and `remove <url> [<url> ...]`
   accept variadic args (per `cobra.MinimumNArgs(1)`); the
   Sprint 24 validator scope pinned the single-arg cases only.
   A future-sprint sweep covering (i) `add a b c` with mixed
   already-present + new args, (ii) `remove a b c` with mixed
   absent + present, and (iii) the partial-failure mode (one
   arg fails URL validation; the test asserts ALL args are
   rejected, no partial mutation) would close out the variadic
   surface. Hermetic-only; no resource impact.
4. **Operator-friendly `clear` confirmation text**. Staff's
   confirmation prompt label is bare:
   `"Clear ALL test.connectivity.extra_hosts?"`. A future UX
   pass could include the entry count + a preview of the first
   N URLs (`"Clear ALL 7 test.connectivity.extra_hosts (incl.
   https://docs.f5.com, …)?"`) so an operator can visually
   confirm they're not about to nuke the wrong workspace's
   list. Test surface: extend `TestTestHostsClear_PromptDefaultsNo`
   to assert the count/preview substring on stderr.
