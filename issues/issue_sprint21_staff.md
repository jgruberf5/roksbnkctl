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
**Status**: open

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
