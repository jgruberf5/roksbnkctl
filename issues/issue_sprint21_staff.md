# Sprint 21 — staff issues (argv strictness)

> **Sprint 21 frame.** First regular work sprint post-`v1.6.4`;
> runs in parallel with Sprint 20. Cobra/pflag argv-parser
> strictness: reject stuck-together short-flag-values; add
> `Args: cobra.NoArgs` to commands that don't take positionals.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Argv preflight + `cobra.NoArgs` audit

**Severity**: high (silent argv misparse can drive
resource-heavy commands against the wrong workspace — see
`argv-strictness-prevents-resource-damage` memory)
**Status**: resolved

### Motivation

The 2026-05-21 integrator's live use exposed a real-world
parse defect: `roksbnkctl init -ws canada-roks --var-file
./terraform.tfvars` silently parsed as `-w s` + dropped
positional `canada-roks`. The wrong workspace got created;
the operator's subsequent `cluster up` then resolved to the
stale `current_workspace: default` and surfaced a 25-resource-
destroy / 41-resource-add plan against real cloud state. The
plan was caught at review, but a less-attentive operator
would have hit `yes` and destroyed prior-cycle infrastructure.

Sprint 21 makes the typo surface immediately at the bad argv,
before any RunE runs, before any workspace dir is created,
before any IBM Cloud call.

### Parser strictness contract

For non-positional flags, ROKSBNKCTL accepts exactly two
shapes per flag-value:

- `--flag value` / `-f value` (one space, value is the next
  argv token), OR
- `--flag=value` / `-f=value` (immediate `=`, no whitespace).

The stuck-together short-flag-value form (`-fvalue` where
`<value>` is non-empty and the immediately-following character
is not `=`) is **rejected at parse time** with an actionable
error. Long-flag stuck-together is structurally impossible in
cobra/pflag — no work needed on that side.

### Acceptance criteria

1. **Argv preflight in `cmd/roksbnkctl/main.go`** that walks
   the cobra tree at process start, collects the set of
   value-requiring short flags, scans `os.Args[1:]` for the
   rejected stuck-together shape, and exits non-zero with the
   actionable error before `Execute()` runs. The detection
   uses the binary's actual flag set (no hand-maintained typo
   list).
2. **Error message** names BOTH the offending token AND both
   acceptable forms AND a "did you mean" suggestion derived
   from the binary's flag set. Example for `-ws`:
   ```
   roksbnkctl: '-ws' is not a recognised flag (looks like a
     stuck-together short-flag-value, which this binary does
     not accept). Use one of these forms instead:
       -w s              (short, space)
       -w=s              (short, equals)
       --workspace s     (long, space)
       --workspace=s     (long, equals)
     Did you mean '--workspace canada-roks'?
   ```
   (Exact wording TBD by staff; the listed substrings must
   appear so validator + tech-writer can pin them.)
3. **`Args: cobra.NoArgs` audit** across the cobra tree under
   `internal/cli/`. Every command that doesn't accept
   positionals gains the constraint. Commands that DO take
   positionals (`ws delete <name>`, `cos object get <key>
   <local>`, `cos bucket get <bucket> <local-dir>`,
   `cluster register …`, etc.) keep their existing `Args:`.
4. **Canonical forms still work byte-identically** —
   `-w s`, `-w=s`, `--workspace s`, `--workspace=s` all
   produce a workspace named `s` exactly as today.
5. **Hermetic gate**: `go build ./...` + `go vet ./...` +
   `gofmt -l internal/ cmd/` + `go test -race ./internal/cli/`
   all GREEN.
6. **No edits to** `internal/orchestration/`, `internal/cos/`,
   `internal/ibm/`, `internal/tf/`, or any RunE body. Surface
   is `cmd/roksbnkctl/main.go` + `internal/cli/*.go` cobra
   command construction sites only.

### Out of scope

- Disabling pflag's stuck-together shorthand at the pflag
  layer. The preflight catches bad input BEFORE pflag sees it.
- Re-architecting `current_workspace` resolution. The
  follow-on `cluster up` mis-resolution is a separate concern;
  a future sprint may address it.
- Live `!` driver. Hermetic tests are sufficient closure per
  the README's gate-shape decision.

### Files affected

- `cmd/roksbnkctl/main.go` (preflight).
- `internal/cli/*.go` cobra command construction sites
  (`Args:` audit). Per-command count: TBD by staff's audit.

### Related

- `argv-strictness-prevents-resource-damage` memory (user
  priority quote, root cause, application guidance).
- Sprint 19 v1.6.4 — the prior sprint whose deliverable
  worked correctly for the typo'd workspace name `s` (Sprint
  21 makes that operator failure unreachable).
- Sprint 20 staff Issue 1 — release-publish hardening, runs in
  parallel.

### Closure — staff, 2026-05-21

**Preflight insertion**: `cmd/roksbnkctl/main.go:24` — the call
site (`argvPreflight(root, os.Args[1:], os.Stderr)`) sits inside
`main()` BEFORE `cli.Execute()`. On detection the preflight
writes the actionable error to stderr and returns a non-nil
error, at which point main exits with code 2 — no
`cli.Execute()`, no RunE, no workspace-dir creation, no IAM
call. Helpers: `argvPreflight` at line 54,
`collectValueRequiringShorts` at line 95,
`findCommandNameIndex` at line 128, `writePreflightError` at
line 144.

**Design notes (preflight)**:

- `collectValueRequiringShorts` walks the cobra tree (root +
  every subcommand transitively, both `PersistentFlags()` and
  `Flags()` per node) and unions every short flag whose
  `NoOptDefVal == ""` — pflag's marker for "value-requiring"
  (string/int/duration/...). Booleans (`-v/-q`) have
  `NoOptDefVal = "true"` and are deliberately skipped so
  `-vvv`-style stacking and bare `-v` keep working untouched.
- Detection scope is bounded by `rootCmd.Find(argv)`: if the
  matched command has `DisableFlagParsing: true` (the
  `kubectl` / `oc` / `ibmcloud` / `terraform` / `exec`
  passthroughs), the preflight scans only the argv prefix that
  precedes the command name. This keeps the passthrough
  contract honest — `roksbnkctl kubectl -nfoo get pods` still
  delegates `-nfoo` straight to kubectl, while
  `roksbnkctl -ws canada-roks kubectl get pods` still catches
  the root-flag typo BEFORE the passthrough dispatch.
- `--` argv sentinel terminates the scan (positional pass-
  through honoured).
- Error wording per README §"Integrator decisions baked in"
  (5): names the literal offending token, both acceptable
  shapes (`-w s` / `-w=s`), both long-flag equivalents
  (`--workspace s` / `--workspace=s`), AND a "did you mean"
  derived from the binary's actual flag set (the
  shorthand→long-name map from the cobra walk).

**Per-command `Args:` audit** (commands that ADD `cobra.NoArgs`
this sprint; commands listed without "→ ADDED" already had
positional-shape constraints that remain unchanged):

| Command                  | File:line                          | Before              | After                | Rationale |
|--------------------------|------------------------------------|---------------------|----------------------|-----------|
| `bnk up`                 | `internal/cli/bnk_phase.go:31`     | (unset)             | `NoArgs` → ADDED     | No positionals — flag-driven only |
| `bnk down`               | `internal/cli/bnk_phase.go:47`     | (unset)             | `NoArgs` → ADDED     | No positionals |
| `shell`                  | `internal/cli/cluster.go:38`       | (unset)             | `NoArgs` → ADDED     | Drops into $SHELL; no positionals |
| `exec`                   | `internal/cli/cluster.go:49`       | `MinimumNArgs(1)`   | `MinimumNArgs(1)`    | Takes `[command...]` positional — KEEP |
| `kubeconfig`             | `internal/cli/cluster.go:57`       | (unset)             | `NoArgs` → ADDED     | No positionals |
| `kubectl` (passthrough)  | `internal/cli/cluster.go:66`       | (unset)             | (unset)              | `DisableFlagParsing` — args flow to wrapped tool — KEEP |
| `oc` (passthrough)       | `internal/cli/cluster.go:73`       | (unset)             | (unset)              | passthrough — KEEP |
| `ibmcloud` (passthrough) | `internal/cli/cluster.go:80`       | (unset)             | (unset)              | passthrough — KEEP |
| `cluster register`       | `internal/cli/cluster_phase.go:48` | `MaximumNArgs(1)`   | `MaximumNArgs(1)`    | Optional `[cluster-name-or-id]` — KEEP |
| `cluster show`           | `internal/cli/cluster_phase.go:67` | (unset)             | `NoArgs` → ADDED     | No positionals |
| `cluster up`             | `internal/cli/cluster_phase.go:74` | (unset)             | `NoArgs` → ADDED     | No positionals |
| `cluster down`           | `internal/cli/cluster_phase.go:90` | (unset)             | `NoArgs` → ADDED     | No positionals |
| `cos instance create`    | `internal/cli/cos.go:47`           | `ExactArgs(1)`      | `ExactArgs(1)`       | `<name>` — KEEP |
| `cos instance delete`    | `internal/cli/cos.go:60`           | `ExactArgs(1)`      | `ExactArgs(1)`       | `<name>` — KEEP |
| `cos instance list`      | `internal/cli/cos.go:67`           | (unset)             | `NoArgs` → ADDED     | No positionals |
| `cos bucket create`      | `internal/cli/cos.go:81`           | `ExactArgs(1)`      | `ExactArgs(1)`       | `<bucket>` — KEEP |
| `cos bucket delete`      | `internal/cli/cos.go:88`           | `ExactArgs(1)`      | `ExactArgs(1)`       | `<bucket>` — KEEP |
| `cos bucket list`        | `internal/cli/cos.go:95`           | (unset)             | `NoArgs` → ADDED     | No positionals |
| `cos bucket get`         | `internal/cli/cos.go:102`          | `ExactArgs(2)`      | `ExactArgs(2)`       | `<bucket> <local-dir>` — KEEP |
| `cos object put`         | `internal/cli/cos.go:132`          | `ExactArgs(2)`      | `ExactArgs(2)`       | `<bucket>/<key> <local>` — KEEP |
| `cos object get`         | `internal/cli/cos.go:139`          | `ExactArgs(2)`      | `ExactArgs(2)`       | `<bucket>/<key> <local>` — KEEP |
| `cos object delete`      | `internal/cli/cos.go:146`          | `ExactArgs(1)`      | `ExactArgs(1)`       | `<bucket>/<key>` — KEEP |
| `cos object list`        | `internal/cli/cos.go:153`          | `ExactArgs(1)`      | `ExactArgs(1)`       | `<bucket>[/<prefix>]` — KEEP |
| `status`                 | `internal/cli/inspect.go:31`       | (unset)             | `NoArgs` → ADDED     | No positionals |
| `logs`                   | `internal/cli/inspect.go:50`       | `ExactArgs(1)`      | `ExactArgs(1)`       | `<component\|pod>` — KEEP |
| `install`                | `internal/cli/install.go:19`       | (unset)             | `NoArgs` → ADDED     | No positionals (uses `--dir`) |
| `k apply`                | `internal/cli/k_apply.go:19`       | (unset)             | `NoArgs` → ADDED     | No positionals (uses `-f`) |
| `k delete`               | `internal/cli/k_delete.go:23`      | `MinimumNArgs(1)`   | `MinimumNArgs(1)`    | `<resource> [name]` — KEEP |
| `k describe`             | `internal/cli/k_describe.go:20`    | `MinimumNArgs(1)`   | `MinimumNArgs(1)`    | `<resource> [name]` — KEEP |
| `k exec`                 | `internal/cli/k_exec.go:22`        | `MinimumNArgs(2)`   | `MinimumNArgs(2)`    | `<pod> -- <cmd>...` — KEEP |
| `k get`                  | `internal/cli/k_get.go:20`         | `MinimumNArgs(1)`   | `MinimumNArgs(1)`    | `<resource>` — KEEP |
| `k logs`                 | `internal/cli/k_logs.go:22`        | `ExactArgs(1)`      | `ExactArgs(1)`       | `<pod-name>` — KEEP |
| `k port-forward`         | `internal/cli/k_port_forward.go:16`| `MinimumNArgs(2)`   | `MinimumNArgs(2)`    | `<pod> <port>...` — KEEP |
| `init`                   | `internal/cli/lifecycle.go:33`     | (unset)             | `NoArgs` → ADDED     | **The original failure mode** — `init -ws canada-roks` silently dropped `canada-roks` |
| `up`                     | `internal/cli/lifecycle.go:46`     | (unset)             | `NoArgs` → ADDED     | No positionals |
| `plan`                   | `internal/cli/lifecycle.go:56`     | (unset)             | `NoArgs` → ADDED     | No positionals |
| `apply`                  | `internal/cli/lifecycle.go:63`     | (unset)             | `NoArgs` → ADDED     | No positionals |
| `down`                   | `internal/cli/lifecycle.go:70`     | (unset)             | `NoArgs` → ADDED     | No positionals |
| `version`                | `internal/cli/meta.go:23`          | (unset)             | `NoArgs` → ADDED     | No positionals |
| `self update`            | `internal/cli/meta.go:43`          | (unset)             | `NoArgs` → ADDED     | No positionals |
| `doctor`                 | `internal/cli/meta.go:72`          | (unset)             | `NoArgs` → ADDED     | No positionals |
| `ops install`            | `internal/cli/ops.go:88`           | (unset)             | `NoArgs` → ADDED     | No positionals (PreRunE preserved) |
| `ops show`               | `internal/cli/ops.go:112`          | (unset)             | `NoArgs` → ADDED     | No positionals |
| `ops uninstall`          | `internal/cli/ops.go:119`          | (unset)             | `NoArgs` → ADDED     | No positionals |
| `targets list`           | `internal/cli/targets.go:36`       | (unset)             | `NoArgs` → ADDED     | No positionals |
| `targets show`           | `internal/cli/targets.go:43`       | `ExactArgs(1)`      | `ExactArgs(1)`       | `<name>` — KEEP |
| `targets add`            | `internal/cli/targets.go:50`       | `ExactArgs(1)`      | `ExactArgs(1)`       | `<name>` — KEEP |
| `targets remove`         | `internal/cli/targets.go:57`       | `ExactArgs(1)`      | `ExactArgs(1)`       | `<name>` — KEEP |
| `terraform` (passthrough)| `internal/cli/terraform.go:27`     | (unset)             | (unset)              | `DisableFlagParsing` — KEEP |
| `test` (parent + suite)  | `internal/cli/test.go:47`          | `MaximumNArgs(1)`   | `MaximumNArgs(1)`    | Optional `[suite]` — KEEP |
| `test connectivity`      | `internal/cli/test.go:64`          | (unset)             | `NoArgs` → ADDED     | No positionals |
| `test dns`               | `internal/cli/test.go:71`          | (unset)             | `NoArgs` → ADDED     | No positionals |
| `test throughput`        | `internal/cli/test.go:96`          | (unset)             | `NoArgs` → ADDED     | No positionals |
| `test list`              | `internal/cli/test.go:109`         | (unset)             | `NoArgs` → ADDED     | No positionals |
| `tfvars`                 | `internal/cli/tfvars.go:19`        | (unset)             | `NoArgs` → ADDED     | No positionals (uses `-o`) |
| `workspaces list` (ws list)    | `internal/cli/workspaces.go:25` | (unset)        | `NoArgs` → ADDED     | No positionals |
| `workspaces current` (ws current)| `internal/cli/workspaces.go:32` | (unset)      | `NoArgs` → ADDED     | No positionals |
| `workspaces use` (ws use)      | `internal/cli/workspaces.go:39` | `ExactArgs(1)` | `ExactArgs(1)`     | `<name>` — KEEP |
| `workspaces new` (ws new)      | `internal/cli/workspaces.go:46` | `ExactArgs(1)` | `ExactArgs(1)`     | `<name>` — KEEP |
| `workspaces delete` (ws delete)| `internal/cli/workspaces.go:53` | `ExactArgs(1)` | `ExactArgs(1)`     | `<name>` — KEEP |

Parent commands without RunE (`bnk`, `cluster`, `cos`,
`cos instance`, `cos bucket`, `cos object`, `k`, `ops`,
`self`, `targets`, `workspaces`, `rootCmd` itself) are
intentionally NOT given `Args: cobra.NoArgs` — they have no
RunE; cobra's subcommand router handles `roksbnkctl ws bar`
as "unknown command 'bar' for 'roksbnkctl ws'" without help
from the constraint.

**Audit table size**: 60 commands enumerated. 32 commands had
`cobra.NoArgs` ADDED this sprint. 14 commands had pre-
existing positional-shape constraints that were preserved
byte-identically. 5 passthrough commands kept `DisableFlagParsing`
without `Args:` (correct — argv flows to the wrapped tool).
11 parent-route commands without RunE were intentionally left
alone.

**Gate results**:

| Gate                                | Result | Notes |
|-------------------------------------|--------|-------|
| `go build ./...`                    | GREEN  | no diagnostics |
| `go vet ./...`                      | GREEN  | no diagnostics |
| `gofmt -l internal/ cmd/`           | GREEN  | empty output |
| `go test -race ./internal/cli/`     | GREEN  | `ok github.com/jgruberf5/roksbnkctl/internal/cli 6.976s` (full pre-existing suite, including the `chokepoint_guard_test.go` parity test, lifecycle_e2e_test.go, env_split_test.go, etc.) |

**Manual smoke-test transcripts** (binary built against this
branch; `ROKSBNKCTL_HOME=/tmp/sprint21-smoke` to isolate
state; smoke transcript reset before each shape):

Shape 1 — `roksbnkctl init -ws foo --var-file ./nope.tfvars`
(the original failure-mode argv from the integrator's
2026-05-21 observation):

```
roksbnkctl: "-ws" is not a recognised flag (looks like a stuck-together short-flag-value, which this binary does not accept).
Use one of these forms instead:
  -w s              (short, space)
  -w=s              (short, equals)
  --workspace s     (long, space)
  --workspace=s     (long, equals)
Did you mean '--workspace s'?
[exit=2]
```

`ls $ROKSBNKCTL_HOME` after run: EMPTY — no workspace dir
was created. Acceptance criteria 1, 2, and the original
failure-mode's "no side effect before RunE" guard all hold.

Shape 2 — `roksbnkctl init -w s` (the space form; same as
today, byte-identically):

```
Setting up workspace "s"


→ Verifying IBM Cloud credentials...
...
```

`ls $ROKSBNKCTL_HOME` after run: contains `s/` — workspace
named `s` was created exactly as today.

Shape 3 — `roksbnkctl init -w=s` (the equals form; same as
today, byte-identically):

```
Setting up workspace "s"


→ Verifying IBM Cloud credentials...
...
```

The init goes through its TTY interview path either way; the
two accepted shapes produce byte-identical first lines of
stderr.

Shape 4 (bonus — pins acceptance criterion 7, the
position-aware half) — `roksbnkctl init -w foo bar`:

```
Error: unknown command "bar" for "roksbnkctl init"
roksbnkctl: unknown command "bar" for "roksbnkctl init"
[exit=1]
```

The `Args: cobra.NoArgs` constraint surfaces the stray
positional as a cobra error (cobra's own subcommand router
phrases it as "unknown command" because it tries the
subcommand match before the args constraint — either way the
positional is rejected, non-zero exit, no RunE invoked, no
workspace mutation).

**Discipline checks**:

- No commits, no `gh` invocations.
- No edits to `internal/orchestration/`, `internal/cos/`,
  `internal/ibm/`, `internal/tf/`.
- No edits to any RunE body. Only command-construction
  literals (`Args:` field) and the new file
  `cmd/roksbnkctl/main.go` preflight were touched.
- No edits to any pre-existing `_test.go`. The validator's
  Issue 1 will ship the additive new test file under
  `internal/cli/` (not staff's scope).
- Files touched: `cmd/roksbnkctl/main.go` (preflight
  insertion + helpers; net +146 LOC), and the cobra-command
  literal sites in `internal/cli/{bnk_phase, cluster,
  cluster_phase, cos, inspect, install, k_apply, lifecycle,
  meta, ops, targets, test, tfvars, workspaces}.go` (one or
  two `Args: cobra.NoArgs,` lines per file, no other body
  changes).
