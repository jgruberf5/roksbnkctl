You are the **staff** agent for Sprint 24 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in this order)

1. `prompts/sprint24/README.md` — integrator decisions; the
   "mirror `targets` ergonomic" framing.
2. `issues/issue_sprint24_staff.md` — the original issue file
   with the full CLI surface design + scope guards.
3. `internal/cli/test.go` lines 1-60 + line 803 — the existing
   `testCmd` registration + the error message you'll update.
4. The `targets` subcommand implementation — grep for
   `targetsCmd` or `targets list` to find it. This is the
   ergonomic precedent your new subcommands mirror.
5. `internal/config/workspace.go` lines 97-132 — the
   `ConnectivityCfg` / `TestCfg` schema and the `ExtraHosts`
   slice you'll mutate.
6. `internal/config/workspace.go` — find the load + save
   functions. The new subcommands call those to read/mutate/
   write `~/.roksbnkctl/<workspace>/config.yaml`.

## Tasks

1. **New file `internal/cli/test_hosts.go`** with the four
   subcommand declarations + their `RunE` functions:
   - `testHostsCmd` — parent group, `Args: cobra.NoArgs` on its
     own (it's a group, not a leaf).
   - `testHostsListCmd` — `Args: cobra.NoArgs`, `--output json`
     flag (mirror the global `-o/--output` if it's already
     defined globally — check). On empty list emit zero bytes +
     exit 0. On JSON output emit `[]` for empty, the slice
     otherwise.
   - `testHostsAddCmd` — `Args: cobra.MinimumNArgs(1)`. Each
     arg validated via `url.Parse`; reject non-URLs with an
     actionable error naming the offending arg. Idempotent —
     no-op + log to stderr when URL already present.
     Insertion-order-stable (append).
   - `testHostsRemoveCmd` — `Args: cobra.MinimumNArgs(1)`.
     Idempotent — no-op + log when URL absent. Preserve order
     of remaining entries.
   - `testHostsClearCmd` — `Args: cobra.NoArgs`. Confirmation
     prompt via `promptYesNo` (defaults to No). `--auto` flag
     skips. On clear, set the slice to `nil` (not empty array
     — distinguishes "explicitly cleared" from "never set" in
     YAML output if any code cares; most won't).
2. **Wire `testHostsCmd` into `testCmd`** in `internal/cli/test.go`
   (or wherever `testCmd` is registered). One-line `testCmd
   .AddCommand(testHostsCmd)` in an `init()` block.
3. **Update `internal/cli/test.go:803`** — the "no hosts
   configured" error message currently reads:
   `"no hosts configured to probe; add to test.connectivity.extra_hosts in config.yaml"`
   Change to point at the new CLI:
   `"no hosts configured to probe; add via roksbnkctl test hosts add <url>"`
   (exact wording your call — the substring `roksbnkctl test
   hosts add` must appear).
4. **Persistence** — use the existing `internal/config`
   workspace marshaller. Read config → mutate
   `cctx.Workspace.Test.Connectivity.ExtraHosts` → write back.
   Wrap the read/write in a function `mutateExtraHosts(workspace
   string, fn func([]string) []string) error` to keep the
   RunEs short. Each subcommand calls `mutateExtraHosts` with
   the appropriate transform.

## Out of scope

- ANY change to `internal/cli/test.go` other than the one error
  message at line 803 + the one `testHostsCmd` registration in
  init.
- `test.dns.{default_target,resolvers}` and `test.throughput.*`
  config fields — separate scope decision; do NOT add CLI for
  them in this sprint.
- The book — that's architect's surface.
- `internal/orchestration/`, `internal/config/tfstate.go`,
  `terraform/` — out of scope.
- Editing any pre-existing `_test.go` file. Your test file is
  NEW: `internal/cli/test_hosts_test.go` (additive).
- Comment preservation in the YAML marshaller — if the existing
  marshaller doesn't preserve YAML comments, document the
  limitation in your closure rather than blocking the sprint.
  This is a UX feature; perfect YAML round-tripping is
  out-of-scope.

## Acceptance criteria

1. `roksbnkctl test hosts {list,add,remove,clear}` all work
   against a temp workspace. Idempotence properties hold.
2. `internal/cli/test.go:803`'s error message points at the new
   CLI.
3. `go test ./internal/cli/...` PASS (your new
   `test_hosts_test.go` plus all pre-existing tests).
4. `go vet ./...` clean.
5. No edits to pre-existing `_test.go` files.

## Closure

Write your closure to
`issues/issue_sprint24_staff.md` §"Closure — staff, <date>"
appended at the end of the existing file (the file is filed as
a placeholder; your closure fills in what shipped). Flip the
top-of-file `**Status**:` field from `open` to `resolved`.
Include: the new CLI surface invocation summary (4 lines, one
per subcommand), the test results, and any future-sprint
candidates raised (e.g. YAML comment preservation if the
existing marshaller drops them).

Reply with a concise summary under 200 words: the four
subcommands' shape (`list` / `add` / `remove` / `clear`), the
test count + pass result, the error-message edit at
`internal/cli/test.go:803`, and any future-sprint candidates.
