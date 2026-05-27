# Sprint 24 — staff issues (test hosts CLI surface)

> **Surfaced 2026-05-27** during the demo.sh re-verify cycle. The
> presenter asked how to add test hosts so `roksbnkctl test dns`
> and `roksbnkctl test connectivity` would work against the
> canada-roks workspace. There is no CLI for it — the only path
> is hand-editing `~/.roksbnkctl/<workspace>/config.yaml` to add
> a `test.connectivity.extra_hosts` list. The error message the
> test commands themselves emit when no hosts are configured
> (`internal/cli/test.go:803`) reads:
>
> ```
> no hosts configured to probe; add to test.connectivity.extra_hosts in config.yaml
> ```
>
> …which points the operator at the YAML. Other workspace-config
> surfaces with comparable shape (`targets`, `cluster register`)
> have first-class CLI; test hosts don't. This closes that gap.

`Status: open`.

---

## Issue 1 — `roksbnkctl test hosts {list,add,remove}` CLI

**Severity**: low (UX, not correctness). No resource-damage
implication; the YAML path works today.
**Status**: open

### Motivation

The `test connectivity` and (no-flag) `test dns` commands consume a
single workspace config slice — `test.connectivity.extra_hosts`. To
populate it today an operator must:

1. `cat ~/.roksbnkctl/<workspace>/config.yaml` (no CLI to inspect
   current value).
2. Open it in an editor.
3. Hand-write YAML respecting indentation and the (existing or
   not-yet-existing) `test:` / `connectivity:` parent structure.
4. Save, hope the indentation is right, run `test connectivity` to
   discover whether the parse succeeded.

`roksbnkctl targets {list,add,remove,show}` already demonstrates
the ergonomic shape that closes this loop for SSH targets — the
same affordance applied to test hosts is the natural symmetry.
`cluster register` is the same shape one rung deeper (writes a
JSON sidecar instead of into config.yaml, but the
"discover-and-persist" pattern is identical).

### Proposed CLI surface

Mirrors `targets` byte-for-byte where it makes sense:

```
roksbnkctl test hosts list                            # print current extra_hosts, one per line
roksbnkctl test hosts add <url> [<url> ...]           # append; idempotent (no-op if already present)
roksbnkctl test hosts remove <url> [<url> ...]        # remove; tolerant of "not present" (no error)
roksbnkctl test hosts clear                           # remove ALL (confirmation prompt, --auto skip)
```

Scope guards:

- Out of scope for this issue: `test.dns.default_target`,
  `test.dns.resolvers`, `test.throughput.*`. Those have working
  flag-driven equivalents (`--target`, `--server`, `--duration`,
  …) and adding CLI for them is a separate scope decision. This
  issue covers only `test.connectivity.extra_hosts`, the slice
  shared by both `test connectivity` and the no-flag
  workspace-driven `test dns`.
- Out of scope: input validation beyond URL syntax (no live
  reachability check at `add` time — that's what `test
  connectivity` is for). Reject non-URLs with an actionable error;
  accept any URL the std-lib `url.Parse` accepts.
- Out of scope: an interactive `init` prompt for test hosts. The
  `init` flow stays the same (test section remains optional and
  empty by default); this CLI is the post-init / mid-cycle path.

### Acceptance criteria

1. **New command group**: `roksbnkctl test hosts` with four
   subcommands matching `targets`' shape. Each subcommand declares
   `Args: cobra.NoArgs` (`list`, `clear`) or `cobra.MinimumNArgs(1)`
   (`add`, `remove`) per the Sprint 21 strictness contract.
2. **Persistence**: write through `internal/config`'s existing
   workspace marshaller (the same path `targets add` uses for
   `TargetCfg`). Preserve unrelated YAML structure and comments
   verbatim — operators commonly add their own comments to
   config.yaml and the writer must not strip them. If the current
   marshaller doesn't preserve comments, this issue blocks on
   either fixing it OR scoping that work into the same sprint.
3. **Idempotence**: `add <url>` against an already-present URL is
   a no-op + log line ("already present"); `remove <url>` against
   an absent URL is a no-op + log line ("not present"). Neither
   error. Exit 0 in both cases. Mirrors `targets add/remove`.
4. **`list` output shape**: bare newline-separated URLs on stdout
   (one per line, sorted insertion-order-stable — no re-sort).
   Empty list emits zero bytes + exit 0 (NOT an error;
   distinguishes "nothing configured" from "command failed" by
   exit code). `--output json` emits the slice as a JSON array.
5. **`clear` prompt**: confirmation defaults to No, `--auto`
   skips (matches the `roksbnkctl down` / `cluster down` pattern,
   not the `targets remove` pattern which has no confirmation
   because it's per-target). Optional — scope can drop this and
   require `--auto` always.
6. **Tests** (`internal/cli/test_hosts_test.go` — new additive
   file, no edits to existing `_test.go`): unit-test the four
   subcommands against a temp-`ROKSBNKCTL_HOME` workspace; pin
   the idempotence properties, the `list`-of-empty exit-0 contract,
   and the `clear` prompt's default-No.

### Files affected

- `internal/cli/test.go` OR new `internal/cli/test_hosts.go` —
  cobra command declarations + RunE wiring. Existing `testCmd`
  gets `testCmd.AddCommand(testHostsCmd)`; `testHostsCmd` gets
  the four `AddCommand` calls.
- `internal/cli/test_hosts_test.go` — new additive test file.
- `internal/config/workspace.go` — only if a helper is needed for
  the write path. The existing read path (`ConnectivityCfg.ExtraHosts`)
  is already there; persistence likely just needs a thin "load,
  mutate, save" wrapper.
- `book/src/12-running-tests.md` (or wherever the test commands
  are documented) — add the `test hosts` reference. Tech-writer
  scope, not staff.
- Update `internal/cli/test.go:803`'s error message to point at
  the new CLI instead of the YAML path:
  `"no hosts configured to probe; add via roksbnkctl test hosts add <url>"`.

### Related

- `internal/cli/test.go:803` — the error message that today
  points at YAML editing. Updating it is part of this issue.
- `internal/cli/targets.go` (or wherever `targets add/list/remove`
  lives) — the ergonomic precedent this issue mirrors.
- Sprint 21 (`issues/issue_sprint21_staff.md`) — establishes the
  `cobra.NoArgs` discipline that the new subcommands must follow.
- The demo.sh re-verify on 2026-05-27 — original surfacing
  context. The demo currently exits with the "no hosts
  configured" error on its `test dns` / `test connectivity`
  lines until the operator edits YAML by hand. Closing this
  issue lets the demo script include
  `roksbnkctl test hosts add ...` lines as part of its setup
  block, removing one manual prep step.

### Out-of-band note

This issue is **lower priority than** `issue_sprint23_staff.md`
(the phase-separation leak — resource-damage class) and should
NOT be sequenced ahead of it. UX gaps wait; resource-damage
guards don't. Either ship Sprint 23 first and slot this into
Sprint 24, or batch them together if the same sprint can
reasonably hold both (staff scope is bounded for both).
