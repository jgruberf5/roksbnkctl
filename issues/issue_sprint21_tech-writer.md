# Sprint 21 — tech-writer issues (argv strictness drift sweep)

> **Sprint 21 frame.** Tech-writer runs **after** the
> integrator has landed staff + architect + validator's
> deliverables to `main` and run gates. Drift sweep + GREEN/RED
> launch verdict.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Post-integration drift sweep for argv strictness

**Severity**: medium (argv strictness is a behavioural change
visible to every operator; drift here surfaces immediately in
the next person's shell)
**Status**: resolved (pending integrator)

### Motivation

Sprint 21 introduces a parser strictness contract that's a
small but real behavioural change. The drift surface includes:
the architect's book paragraph (must match verbatim binary
output), the regen'd chapter 27 (must reflect any `Args:`
change), cross-chapter examples (no stuck-together shorthand
survives), the CHANGELOG entry the integrator will write at
cut (must call out the BREAKING aspect for any operator using
the pre-strictness form), and `docs/PLAN.md`'s closure
subsection.

### Drift surface to walk

1. **`cmd/roksbnkctl/main.go`** — verify the preflight landed.
   Cite line range. Confirm it walks the cobra tree (no
   hand-maintained typo list).
2. **The architect's book paragraph** — find via grep for the
   new strictness language. Capture the binary's actual error
   output by running it against `-ws foo` (with
   `ROKSBNKCTL_HOME=/tmp/probe-tw` or similar). Compare to
   what the paragraph quotes. ANY drift here is a high finding.
3. **`book/src/27-command-reference.md`** — verify it's
   regen'd against the new `Args:` constraints. Sample a
   handful of commands staff added `cobra.NoArgs` to — their
   per-command sections should reflect the updated Usage.
4. **Cross-chapter sweep** — grep the book for stuck-together
   short-flag-value examples. If any survive, file as findings
   (severity: medium per example — operator confusion risk).
5. **`docs/PLAN.md` §"Sprint 21"** — should carry a closure
   subsection. Verify the live-`!`-not-applicable rationale
   appears (this sprint's gate is hermetic, not live).
6. **`CHANGELOG.md`** — the integrator's pending entry for
   the next tag should call out the strictness change
   prominently. Is it in `### Changed`? Does it warn operators
   who were using `-fvalue`? IF the CHANGELOG isn't written
   yet, recommend the wording the integrator should use.

### Acceptance criteria

1. Every finding names a specific file path + line number.
2. Findings tagged by severity (low / medium / high); each
   `high` finding blocks the release cut.
3. A final GREEN / RED launch verdict ends the closure.
4. The findings cite the actual binary output (capture
   `roksbnkctl init -ws foo` stderr during your review and
   include the relevant lines verbatim).

### Out of scope

- Restyling chapters; rewriting flow descriptions. Drift
  sweep only — recommend fixes, don't apply them.
- Touching any non-`issues/` file. Read-only on existing
  repo content.

### Optional Part B (≤2 issues)

If the integrated work surfaces a cross-cutting docs gap or
neighbouring UX hole the other roles didn't close, file it as
Issue 2 (or 2+3). Strict cap.

### Files affected

- `issues/issue_sprint21_tech-writer.md` (this file's
  Closure section). Read-only on the integrated tree.

### Related

- Sprint 21 staff/architect/validator Issue 1 — all reviewed
  for drift.
- `argv-strictness-prevents-resource-damage` memory — context
  for why the BREAKING callout in CHANGELOG matters.

### Closure — tech-writer, 2026-05-21

Post-integration drift sweep run against the landed tree at
commit `a3efcfd` (`sprint21: three-way integration — cobra/pflag
argv strictness + cobra.NoArgs audit`). Walked every drift
surface enumerated in this issue plus the README's per-role
acceptance criteria; captured the binary's actual stderr;
sampled chapter 27 for the `NoArgs → ADDED` commands; ran the
cross-chapter sweep; checked `docs/PLAN.md` §"Sprint 21" and
`CHANGELOG.md`.

**Findings summary**: 0 high, 1 medium, 1 low. Findings 1 and
2 are recommendations the integrator needs at cut (write
PLAN.md closure subsection; write the CHANGELOG entry against
the wording proposed here). Neither blocks the launch — the
code surface, tests, and book paragraph are all clean.

---

#### Drift surface 1 — `cmd/roksbnkctl/main.go` preflight

**Status**: CLEAN.

Preflight call-site at `cmd/roksbnkctl/main.go:24` is inside
`main()` and runs **before** `cli.Execute()` (line 30) — on
non-nil error the binary exits with code 2 (`os.Exit(2)` at
line 28) before any RunE / workspace dir / IAM call. The
helpers are at the lines verifiable by inspection (not the
exact post-comment lines staff cited — see Finding 3 below,
low):

| Function | Actual line |
|---|---|
| `argvPreflight` | `cmd/roksbnkctl/main.go:54` |
| `collectValueRequiringShorts` | `cmd/roksbnkctl/main.go:113` |
| `findCommandNameIndex` | `cmd/roksbnkctl/main.go:151` |
| `writePreflightError` | `cmd/roksbnkctl/main.go:168` |

The preflight **does walk the cobra tree** (no hand-maintained
typo list) — `collectValueRequiringShorts` at lines 113–144
recurses via `walk` (lines 134–142) and unions every short
flag whose `NoOptDefVal == ""` (the pflag marker for
"value-requiring" — booleans like `-v` / `-q` have
`NoOptDefVal == "true"` and are correctly skipped at line 119,
so `-vvv`-style bool-stacking is preserved). The
`DisableFlagParsing` cutoff for kubectl/oc/ibmcloud/terraform
passthroughs is honoured at lines 64–69 via `root.Find`.
Acceptance criterion "walks the cobra tree, no
hand-maintained typo list" → confirmed in source.

#### Drift surface 2 — book paragraph vs binary stderr

**Status**: CLEAN — line-for-line match.

Command run (from repo root):
```
ROKSBNKCTL_HOME=/tmp/probe-tw go run ./cmd/roksbnkctl init -ws foo </dev/null 2>&1
```

Stderr captured verbatim:
```
roksbnkctl: "-ws" is not a recognised flag (looks like a stuck-together short-flag-value, which this binary does not accept).
Use one of these forms instead:
  -w s              (short, space)
  -w=s              (short, equals)
  --workspace s     (long, space)
  --workspace=s     (long, equals)
Did you mean '--workspace s'?
exit status 2
```

The `exit status 2` line is Go's `go run` wrapper, not part of
the binary's stderr; the binary's own stderr is the 7 lines
above it. Process exit was non-zero (2).

`ls /tmp/probe-tw` after the run: **the directory does not
exist** — the preflight rejected the argv before any workspace
machinery initialised, no `ROKSBNKCTL_HOME` dir was created.
The "no side effect before RunE" guard holds.

Quoted text in `book/src/07-quick-start.md:62–68`:
```
roksbnkctl: "-ws" is not a recognised flag (looks like a stuck-together short-flag-value, which this binary does not accept).
Use one of these forms instead:
  -w s              (short, space)
  -w=s              (short, equals)
  --workspace s     (long, space)
  --workspace=s     (long, equals)
Did you mean '--workspace s'?
```

Byte-for-byte identical to the binary's stderr — same
punctuation, same two-space indent on the four shape lines,
same `'--workspace s'` quoting on the "did you mean".
**Acceptance criterion 4 (verbatim binary output in the
paragraph) → met.** No high finding here.

#### Drift surface 3 — `book/src/27-command-reference.md` regen

**Status**: CLEAN — current md5 matches a fresh regen.

`md5sum book/src/27-command-reference.md` → `bee798f5857c0b5d9fd46287d942c2fa`
(matches the architect's closure claim).

Fresh regen via `go run ./tools/refgen/cobra-md > /tmp/refgen-fresh.md`
→ same md5 `bee798f5857c0b5d9fd46287d942c2fa`, zero-line
`diff` against the on-disk chapter. Refgen renders cobra
`Use` / `Short` / `Long` / flag tables, not the `Args:`
constraint shape — so 32 newly-pinned `NoArgs` commands +
14 preserved positional constraints don't move the output,
consistent with architect's claim.

Sampled per-command sections (from staff's `NoArgs → ADDED`
audit table):

| Command | Chapter 27 line | Usage line | Verdict |
|---|---|---|---|
| `init` | `book/src/27-command-reference.md:508` | `roksbnkctl init [flags]` | No positional placeholder — consistent with `cobra.NoArgs` |
| `cluster up` | `book/src/27-command-reference.md:191` | `roksbnkctl cluster up [flags]` | No positional placeholder — consistent with `cobra.NoArgs` |
| `version` | `book/src/27-command-reference.md:1256` | (heading only — no `[flags]` block because no flags) | Consistent with `cobra.NoArgs` + zero-flag command |
| `apply` | `book/src/27-command-reference.md:34` | `roksbnkctl apply [flags]` | Consistent with `cobra.NoArgs` |
| `doctor` | `book/src/27-command-reference.md:412` | `roksbnkctl doctor [flags]` | Consistent with `cobra.NoArgs` |
| `workspaces delete` | `book/src/27-command-reference.md:1280–1282` | `roksbnkctl workspaces delete <name> [flags]` | Positional `<name>` preserved (per staff's KEEP row) |
| `cos bucket get` | `book/src/27-command-reference.md:228+` | `<bucket> <local-dir>` preserved (`ExactArgs(2)` KEEP) |

All five `NoArgs → ADDED` samples render as `<verb> [flags]`
(no positional placeholder), all `KEEP` samples preserve their
positional placeholders. **Acceptance criterion 2 (regen'd
against the post-staff cobra tree) → met.** No finding.

#### Drift surface 4 — cross-chapter sweep for stuck-together short-flag-value

**Status**: CLEAN — none found.

Sweep universe per the issue spec: value-requiring shorts
`{c, f, l, n, o, w}` (matches the architect's enumeration at
`issue_sprint21_architect.md:182` — `-A`, `-i`, `-q`, `-t`,
`-v` are booleans and bool-stacking is preserved by staff's
preflight via the `NoOptDefVal != ""` skip at
`cmd/roksbnkctl/main.go:119`).

Sweep query: grep `book/src/` for `roksbnkctl …` invocations
carrying `-X<value>` where `X ∈ {c, f, l, n, o, w}` and
`<value>[0]` is alphabetic. Hits triaged:

| Hit | Verdict |
|---|---|
| `book/src/07-quick-start.md:59` `roksbnkctl init -ws canada-roks --var-file ./terraform.tfvars` | False positive — architect's intentional demonstration of the rejected typo (the very example the paragraph quotes the rejection for) |
| `book/src/07-quick-start.md:62` `roksbnkctl: "-ws" is not a recognised flag …` | False positive — embedded in the quoted error text |
| `book/src/24-day-2-ops.md:319` `roksbnkctl k exec flo-controller-abc123 -n f5-bnk -- ls -la /` | False positive — `-n` carries `f5-bnk` in canonical space form; `-la` is the embedded `ls` command's bool-stack, not roksbnkctl flags |
| `book/src/27-command-reference.md:704` `roksbnkctl k exec my-pod -it -- bash` | False positive — `-i` and `-t` are booleans in `k exec` (`internal/cli/k_exec.go:47–49` per architect's verification); bool-stacking is preserved |
| `book/src/04-installation.md:140`, `book/src/31-building-from-source.md:55` | False positives — `go build -ldflags`, a Go toolchain flag, not a roksbnkctl flag |
| All other matches | False positives — hyphenated names (`jumphost-ca-tor-1`, `roksbnkctl-ops`), `~/.roksbnkctl/<dir>` paths, long flags (`--workspace`, `--on`, `--instance`), prose hyphens |

**No `roksbnkctl <verb> -X<value>` invocation survives in the
book where `X ∈ {c, f, l, n, o, w}` and `<value>` is a stuck-
together literal value.** The canonical space / equals forms
are used uniformly across the 30+ book chapters. Acceptance
criterion 1 → met (every drift would have been a high
finding; none found).

#### Drift surface 5 — `docs/PLAN.md` §"Sprint 21" closure — **MEDIUM FINDING 1**

**Status**: FINDING — missing closure subsection.

`docs/PLAN.md:1125–1144` is the Sprint 21 section. It carries
the per-role table (lines 1131–1136), the
`live-verify-high-issues` rationale ("does NOT apply — argv
shape doesn't need cloud validation; hermetic tests are
sufficient closure", line 1138), and a version-bump note
(line 1140). **It does not carry a "### Closure (2026-05-21)"
subsection** documenting the actual landed state — staff +
architect + validator closure summaries, gate results, version
cut.

Compare to Sprint 20's `### Closure (2026-05-20)` at
`docs/PLAN.md:1216` (which shipped in commit `50ac31b`): that
subsection enumerates the per-role outcomes, names the gate
results, and records the integrator's version decision. Sprint
21's section lacks the equivalent.

**Severity**: medium — the
`live-verify-high-issues`-not-applicable rationale appears at
line 1138 in the **scope** prose, not in a post-integration
closure summary; future readers of PLAN.md scanning for the
"what landed" record will find the role table but not the
"shipped, gates GREEN, version X" footer that Sprint 18's
1216 + Sprint 20's equivalent both carry.

**Recommendation** (integrator, at cut): insert a `### Closure
(2026-05-21)` subsection between the existing line 1142
(`Sprint launch: integrator dispatches…`) and the line 1144
`---` separator, with the shape Sprint 20's 1216 set. Suggested
wording, for the integrator to adapt:

```markdown
### Closure (2026-05-21)

All four scope items landed GREEN at commit `a3efcfd`; shipped
as `v<TBD>`. Hermetic test gate sufficient — argv shape doesn't
need cloud validation; the `live-verify-high-issues` rule does
not apply because the whole point of this sprint is to surface
typos BEFORE any cloud call.

- **Staff Issue 1** (argv preflight + `cobra.NoArgs` audit) —
  hermetic GREEN. Preflight at `cmd/roksbnkctl/main.go:24`
  walks the cobra tree (no hand-maintained typo list), rejects
  the stuck-together short-flag-value shape at parse time with
  the actionable error. 32 commands gained `cobra.NoArgs`; 14
  positional-shape constraints preserved; 5 passthroughs
  intentionally kept `DisableFlagParsing`-without-`Args:`.
- **Architect Issue 1** (book paragraph + chapter 27 regen +
  sweep) — paragraph at `book/src/07-quick-start.md:59–71`
  matches binary stderr byte-for-byte; chapter 27 regen byte-
  identical (refgen renders Use/Short/Long, not Args); cross-
  chapter sweep zero hits.
- **Validator Issue 1** (hermetic tests) — `internal/cli/argv_strictness_test.go`
  shipped (+542 LOC additive); 6 top-level tests + 30 NoArgs
  sweep sub-cases all PASS under `go test -race ./internal/cli/`.
- **Tech-writer Issue 1** — drift sweep GREEN; 0 high, 1
  medium (this PLAN.md closure subsection), 1 low (staff
  closure note line-number drift, source unaffected). No
  blockers to launch.

Version at cut: integrator-owned. The strictness change is
behavioural for any operator using `-fvalue` stuck-together
shorthand — a minor bump (`v1.7.0`) is defensible; if the
integrator judges disruption low and rides the next user-
facing change, patch is also defensible. CHANGELOG entry must
call out the change prominently (see this sprint's tech-writer
closure for proposed wording).
```

#### Drift surface 6 — `CHANGELOG.md` Sprint 21 entry — **MEDIUM FINDING 2 (recommendation only)**

**Status**: FINDING — entry not yet written; recommendation
follows.

`CHANGELOG.md`'s latest entry is `v1.6.4 — 2026-05-21`
(Sprint 19's release; lines 7–13). **No Sprint 21 / argv-
strictness entry exists yet** — consistent with the issue
spec's "IF the CHANGELOG isn't written yet, recommend the
wording the integrator should use" branch.

Severity: medium — the change is behavioural for any operator
who was relying on cobra/pflag's stuck-together-shorthand
parsing. Per `argv-strictness-prevents-resource-damage`
memory, the silent-misparse class was real-world enough to
nearly destroy 25 IBM Cloud resources in the integrator's
2026-05-21 live use; the CHANGELOG must surface the change
prominently so operators upgrading from `v1.6.4` recognise
the rejection error when they hit it (or pre-emptively update
their muscle memory).

**Recommendation** (integrator-owned, do not write at this
role — recommend wording only): the entry should be in
`### Changed`, and the bullet should warn operators who were
using `-fvalue` explicitly. The integrator may also choose to
add a `### BREAKING` note per `docs/PLAN.md:1140` ("CHANGELOG
entry must call out the change in `### Changed` (and arguably
`### BREAKING`)") — keep-a-changelog 1.1.0 doesn't have a
`### BREAKING` section officially, so either prepend a
`**BREAKING:**` label inside the `### Changed` bullet or use
a top-of-entry `### Removed` / `### Changed` callout per
project precedent.

Suggested wording (integrator may adapt freely; do not commit
this as-is — this role is recommendation-only on CHANGELOG):

```markdown
## v<TBD> — 2026-05-21

Sprint 21 — cobra/pflag argv-parser strictness, first regular
work cycle post-`v1.6.4`. **Behavioural change**: short flags
no longer accept the stuck-together `-fvalue` form; the binary
now rejects it at parse time with an actionable error before
any RunE / workspace mutation / IAM call. Motivated by an
integrator live-use observation 2026-05-21 in which
`roksbnkctl init -ws canada-roks --var-file …` silently parsed
as `-w s` + dropped positional, creating a workspace named
`s` and seeding a follow-on `cluster up` with a 25-destroy /
41-add plan against real cloud state (caught at plan review,
not after apply). See [PLAN.md §"Sprint 21"](docs/PLAN.md),
[`issues/issue_sprint21_staff.md`](issues/issue_sprint21_staff.md),
[`issues/issue_sprint21_architect.md`](issues/issue_sprint21_architect.md),
and [`issues/issue_sprint21_validator.md`](issues/issue_sprint21_validator.md).

### Changed

- **BREAKING for any operator using stuck-together short-flag-values: short flags now require `-f value` (space) or `-f=value` (equals); the `-fvalue` form is rejected at parse time** ([`issues/issue_sprint21_staff.md` Issue 1](issues/issue_sprint21_staff.md)) — pre-`v<TBD>`, cobra/pflag accepted `-fvalue` and silently parsed it as `-f` with value `value`. This produced a silent-misparse class: e.g. `roksbnkctl init -ws canada-roks --var-file …` parsed as `-w s` (workspace `s`) with the positional `canada-roks` silently dropped, allowing the typo to propagate through `init` and a follow-on `cluster up` before surfacing as a wrong-workspace destroy plan against real cloud state. A new pre-parse argv preflight in `cmd/roksbnkctl/main.go` walks the cobra tree at process start, collects the set of value-requiring short flags from the binary's live flag set (no hand-maintained typo list), and rejects any argv token of the shape `-X<suffix>` (where `X` is value-requiring, `<suffix>` is non-empty, and `<suffix>[0]` is not `=`) with an actionable error that names the offending token, both acceptable shapes (`-X value` / `-X=value`), the long-flag equivalents (`--longname value` / `--longname=value`), and a "did you mean" derived from the binary's own flag set. The rejection runs BEFORE `cli.Execute()`, so no workspace dir, IAM call, or filesystem mutation precedes the error. Operators who were typing `-wcanada-roks` / `-fpath/to/file` / `-nf5-bnk` / similar must switch to `-w canada-roks` / `-f path/to/file` / `-n f5-bnk` (space) or the equivalent `=` forms. Boolean shorts (`-v`, `-q`, `-i`, `-t`, `-A`) and bool-stacking (`-it`, `-vvv`) are unaffected — the preflight skips them via pflag's `NoOptDefVal != ""` marker. Passthroughs (`roksbnkctl kubectl …`, `oc`, `ibmcloud`, `terraform`, `exec`) keep `DisableFlagParsing` semantics — argv after the subcommand name flows to the wrapped tool untouched. See the new "Flag-value syntax" callout in [book chapter 7](book/src/07-quick-start.md) and the per-command reference in [book chapter 27](book/src/27-command-reference.md).
- **Commands that don't take positionals now reject stray positionals at parse time** ([`issues/issue_sprint21_staff.md` Issue 1](issues/issue_sprint21_staff.md)) — companion to the argv strictness change above. 32 cobra commands gained `Args: cobra.NoArgs` (full list in the staff closure audit table): `init`, `up`, `plan`, `apply`, `down`, `bnk up/down`, `cluster up/down/show`, `cos {instance,bucket} list`, `status`, `install`, `k apply`, `version`, `self update`, `doctor`, `ops {install,show,uninstall}`, `targets list`, `test {connectivity,dns,throughput,list}`, `tfvars`, `workspaces {list,current}`, `shell`, `kubeconfig`. Commands that DO accept positionals (`ws delete <name>`, `cos object get <key> <local>`, `cluster register`, `targets {show,add,remove}`, etc.) preserve their existing `Args:` constraints byte-identically. Catches the OTHER half of the original failure mode — the silently-dropped `canada-roks` positional in the integrator's observed misparse.
```

The integrator should:
1. Decide on the version (minor `v1.7.0` per PLAN.md:1140's
   "minor bump … is defensible" guidance, vs patch `v1.6.5`
   if disruption is judged low).
2. Adapt the wording above to project style — particularly
   the BREAKING callout shape (inline `**BREAKING:**` label
   vs separate section; project precedent is the
   inline-prefix form per `CHANGELOG.md:515` style).
3. Drop the proposed wording into the cut commit alongside
   the version bump.

#### Drift surface 7 — staff closure note line-number drift — **LOW FINDING 3**

**Status**: documentation-only, source unaffected.

`issues/issue_sprint21_staff.md:125–128` cites the preflight
helpers as `argvPreflight at line 54, collectValueRequiringShorts
at line 95, findCommandNameIndex at line 128, writePreflightError
at line 144`. The actual locations in the landed source are
54, 113, 151, 168 (verified via `grep -n` on
`cmd/roksbnkctl/main.go`). The 54 figure matches (the
`argvPreflight` func-decl line); the other three drifted by
+18 / +23 / +24 lines — likely the staff was counting from
an earlier draft of the file before the (long, useful)
doc-comments were added above each helper.

**Severity**: low — the issue ledger is a closure record, not
a source-of-truth pointer; the citation drift doesn't affect
the binary, the tests, or any operator. Flag for the
integrator's archive scan; no fix required for the launch
cut. The tech-writer closure's "Drift surface 1" table above
records the correct line numbers so future grep-for-cite
discoveries land on the right text.

---

#### Sanity-run: validator's argv strictness test

Not required by the issue spec ("you don't need to re-run
them"), but ran for confidence: `go test -race -run
TestArgvStrictness ./internal/cli/ -count=1` → PASS,
`ok github.com/jgruberf5/roksbnkctl/internal/cli 67.153s`.
6/6 top-level tests + 30/30 `NoArgs_Sweep` sub-cases green
on the integrated tree at `a3efcfd`. Test surface is consistent
with the landed code.

---

#### Findings summary

| # | Severity | Surface | Status |
|---|---|---|---|
| 1 | medium | `docs/PLAN.md` §"Sprint 21" missing `### Closure (2026-05-21)` subsection | recommendation drafted above; integrator inserts at cut |
| 2 | medium | `CHANGELOG.md` Sprint 21 entry not yet written | recommendation drafted above; integrator writes at cut |
| 3 | low | `issues/issue_sprint21_staff.md:125–128` cites three preflight-helper line numbers as 95/128/144; actual lines are 113/151/168 | author-closure drift; source/tests/operators unaffected |

No high findings. No blocker to release cut.

---

#### Launch verdict: **GREEN**

The behavioural surface — preflight, `cobra.NoArgs` audit,
book paragraph quoted text, chapter 27 regen, hermetic test
suite — is **clean**. The architect's paragraph quotes the
binary's stderr line-for-line; no stuck-together
short-flag-value invocation survives in the book; chapter 27
is byte-identical to a fresh regen; the validator's test
suite is green on the integrated tree.

The two medium findings (PLAN.md closure subsection, CHANGELOG
entry) are **integrator-cut deliverables** that this role
cannot write (read-only on `docs/PLAN.md` and `CHANGELOG.md`
per scope), so they don't block the launch — they're recorded
here with proposed wording so the integrator can drop them
into the cut commit. The one low finding (staff closure note
line-number drift) is a documentation hygiene item that
doesn't affect any user.

**Recommendation to integrator**: at cut, (a) insert the
PLAN.md `### Closure (2026-05-21)` subsection with the
wording proposed under Finding 1, (b) write the CHANGELOG
entry adapted from the wording proposed under Finding 2,
(c) optionally update the staff closure's line-number
citations in Finding 3 if archiving precision matters.

#### Discipline checks

- No commits, no `gh` invocations.
- Read-only on all repo content except this file
  (`issues/issue_sprint21_tech-writer.md`).
- No edits to `internal/`, `cmd/`, `book/src/`, `docs/`, or
  `CHANGELOG.md`.
- The binary stderr capture was run against the integrated
  tree at `a3efcfd` via `go run ./cmd/roksbnkctl`; no
  workspace dir was created under `/tmp/probe-tw`
  (confirmed via `ls /tmp/probe-tw` → "cannot access:
  No such file or directory").
- Every finding cites specific `file:line` and a severity
  tier.
