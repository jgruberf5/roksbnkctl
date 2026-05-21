You are the **staff engineer** agent for Sprint 21 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in order)

1. `prompts/sprint21/README.md` — integrator decisions; especially
   §"Integrator decisions baked in" (parser strictness contract,
   pre-parse argv preflight, `cobra.NoArgs` audit).
2. `issues/issue_sprint21_staff.md` Issue 1 — the **authoritative
   spec** for what to build.
3. `cmd/roksbnkctl/main.go` — the binary entrypoint where the
   argv preflight lands.
4. `internal/cli/root.go` (and any sibling that constructs the
   root cobra command) — the cobra tree's spine; you walk it to
   collect the value-requiring short flag set.
5. Every command file under `internal/cli/` whose name maps to
   a cobra command (e.g. `init.go`, `up.go`, `down.go`,
   `cluster_phase.go`, `ws.go`, `cos*.go`). You audit each
   command's `Args:` setting.
6. The cobra/pflag docs you need:
   `vendor/github.com/spf13/pflag/flag.go` — read enough to know
   how stuck-together short-flag-value parsing works today, so
   your preflight is correct.

## Tasks

1. **Argv preflight in the binary entrypoint.** In
   `cmd/roksbnkctl/main.go` (or wherever `rootCmd.Execute()` is
   called), add a preflight that runs BEFORE `Execute()`:
   - Walk the cobra tree once (`rootCmd.VisitParents` /
     `Commands()`) and collect the set of registered short flags
     across root + every subcommand. For each short flag, record
     whether it's value-requiring (non-boolean).
   - Scan `os.Args[1:]` for any token of the shape `-X<suffix>`
     where `X` is a single character AND `X` is in the
     value-requiring short-flag set AND `<suffix>` is non-empty
     AND `<suffix>[0]` is NOT `=`.
   - On detection, print the actionable error to stderr (see
     decision (5) in the README for exact wording) and exit
     non-zero. Use the binary's own flag set to compose the "did
     you mean" suggestion — match the typo'd short flag to its
     long-form name.
   - On no detection, proceed to `rootCmd.Execute()` byte-
     identically to today.
2. **`Args: cobra.NoArgs` audit.** Walk every cobra command
   under `internal/cli/`. For each command:
   - If it accepts positionals (`ws delete <name>`,
     `cos object get <key> <local>`, `cluster register …`, etc.)
     KEEP its existing `Args:` setting.
   - If it does NOT accept positionals AND its current `Args:`
     is unset (cobra's default `ArbitraryArgs`), ADD
     `Args: cobra.NoArgs`.
   - Capture the audit decision per command in your closure
     section (one line per command).
3. **Hermetic verification at staff scope** — the validator's
   issue ships executable tests. At staff scope, prove the
   preflight + Args audit didn't break the rest of the suite:
   `go test -race ./internal/cli/` GREEN; `go build ./...`
   GREEN; `go vet ./...` clean; `gofmt -l` empty.

## Out of scope

- `internal/orchestration/`, `internal/cos/`, `internal/ibm/`,
  `internal/tf/`. This is a cobra/argv hardening sprint —
  business logic is not touched.
- Re-architecting how the cobra tree is constructed. The
  preflight runs BEFORE `Execute()`; you don't change
  `Execute()`'s behaviour.
- Disabling pflag's stuck-together shorthand at the pflag layer.
  The preflight catches the bad input BEFORE pflag sees it;
  pflag's behavior on the remaining inputs is unchanged.
- Any change to RunE bodies. The strictness is at the argv
  layer, before cobra dispatches.

## Acceptance criteria

1. `cmd/roksbnkctl/main.go` (or the equivalent entrypoint) has a
   preflight that walks the cobra tree, collects value-requiring
   short flags, and rejects the stuck-together shape with an
   actionable error. The error names BOTH the offending token
   AND both acceptable shapes (`-f value` and `-f=value`).
2. Every cobra command under `internal/cli/` that doesn't take
   positionals declares `Args: cobra.NoArgs`. Commands that
   take positionals keep their existing `Args:` setting.
3. `go build ./...` + `go vet ./...` + `gofmt -l` clean.
4. `go test -race ./internal/cli/` GREEN.
5. `roksbnkctl init -ws foo --var-file ./fixture.tfvars` exits
   non-zero with the actionable error; no workspace dir is
   created under `ROKSBNKCTL_HOME`.
6. `roksbnkctl init -w s` (space) AND `roksbnkctl init -w=s`
   (equals) BOTH still work — they create a workspace named
   `s` byte-identically to today.
7. `roksbnkctl init -w foo bar` (stray positional) errors via
   the new `cobra.NoArgs` constraint.

## Closure

Write your closure to `issues/issue_sprint21_staff.md`
§"Closure — staff, <date>". Include:

- The exact file:line of the preflight insertion.
- The per-command `Args:` audit table (command name → existing
  vs new Args).
- The gate results (build/vet/gofmt/test).
- Manual smoke-test transcript of the three argv shapes
  (`-ws`, `-w s`, `-w=s`) — one stderr block per shape.
- Discipline checks: no commit, no gh, no out-of-scope edits.
