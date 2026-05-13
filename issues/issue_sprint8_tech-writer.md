# Sprint 8 — tech-writer issues

Sprint 8 is the first post-v1.0 feature cycle (PRD 06 cluster/trial phase
split → `v1.1.0`). Tech-writer scope: readability check of architect's
chapter 8 / 10 / 11 edits, dogfooding the "keep my cluster" loop, drift
sweep across PRD 06 ↔ staff source ↔ chapter 11 catalogue ↔ CHANGELOG, and
the launch-readiness verdict.

**Headline verdict: blocked-with-specifics on a single carry-in gate.** The
Sprint 8 surface itself is clean — chapters 8/10/11 read well, the dispatch
matrix and refusal catalogue match the implementation verbatim, and the
integrator's two follow-up fixes (heading rename + `runBnkDown`
reassurance footer) are landed and correct. The only blocker on `v1.1.0`
is the pre-existing `internal/exec/` WIP that's already been triaged by
the staff and validator agents as outside Sprint 8 scope. Read-only review
adds no fresh blockers.

Chapters reviewed: **08**, **10**, **11** in reading order; also spot-read
the CHANGELOG `v1.1.0` entry, PRD 06 (source of truth for refusal text +
dispatch table), PLAN.md §"Sprint 8" (gate criteria), and the three Sprint
8 issue + resolved pairs.

Six issues filed below: 1 blocker (carry-in surfaced for the integrator's
attention before tagging; not a Sprint 8 regression), 0 high, 4 low
(readability nits / one optional improvement to the legacy-shape
detection prose), 1 informational (verification log).

---

## Issue 1 (BLOCKER — carry-in `internal/exec/` WIP must be triaged before tagging `v1.1.0`)

**Files**: `internal/exec/docker.go`, `internal/exec/k8s.go`,
`internal/exec/k8s_install.yaml`, `internal/cli/cluster.go` (all
uncommitted on the working tree at integration time)

**Owner**: integrator (user) — pre-existing WIP, not Sprint 8 surface

**Severity**: blocker (against the PLAN.md §"Sprint 8 — Gate to `v1.1.0`
tag" line that requires `go build/test/vet/gofmt` green on the whole
tree)

**Status**: open — surfaced here for the integrator; resolution is a
v1.0.3 patch (or fold-into-v1.1.0), per validator's recommendation

### Sweep results on the post-integration tree

```
go build ./...     → clean
go vet ./...       → clean
gofmt -d -l .      → DIRTY: internal/exec/docker.go (struct-tag alignment)
go test ./...      → FAIL on internal/exec only (4 tests):
  - TestRunOpts_TFVarsEnvPassthrough
  - TestResolveDockerImageAndArgv/ibmcloud_prepends_binary
  - TestResolveDockerImageAndArgv/iperf3_keeps_legacy_shape (image ENTRYPOINT picks the binary)
  - TestDockerImageBinary_MirrorsK8sOverrides/ibmcloud
```

Validator filed this as their Issue 1 (blocker) and staff filed it as
their Issue 1 (high). The integrator's `resolved_sprint8_staff.md` Issue
1 + `resolved_sprint8_validator.md` Issue 1 both correctly defer it to
the user as a v1.0.3 candidate. Re-surfaced here because Sprint 8 gate
criterion "Gate to `v1.1.0` tag … `go build/test/vet/gofmt` green" is
explicitly whole-tree, and the tag-cut step happens after the Sprint 8
integration commit lands.

### Resolution paths (integrator's decision)

Three options, in validator's order of preference:

1. **Roll exec WIP into a `v1.0.3` patch release first** (validator's
   preferred route). Refresh `internal/exec/docker_test.go` /
   `internal/exec/docker_terraform_test.go` to match the new
   ibmcloud-login wrap shape + the iperf3 public-image switch + the
   PATH=/usr/local/bin env-passthrough delta; `gofmt -w
   internal/exec/docker.go`. Tag `v1.0.3`, then cut `v1.1.0` from a
   clean tree.
2. Fold the same test updates into the v1.1.0 surface and cut a single
   `v1.1.0` tag. Higher cognitive load for the release notes since the
   exec changes are unrelated to PRD 06's phase split.
3. Revert the exec WIP if it was a stale draft, then tag `v1.1.0` from
   the clean tree.

This is read-only review's only blocker against `v1.1.0`. Once the
carry-in is triaged, the Sprint 8 surface itself is tag-ready.

---

## Issue 2 (LOW — chapter 8 §"Legacy single-state workspaces" sample output wraps the refusal across two lines; the real binary emits one)

**Files**: `book/src/08-cluster-phase.md:275-282`

**Owner**: architect (chapter prose)

**Severity**: low (visual / pedagogical only; does not affect refusal
matching or grep behavior)

**Status**: open — flagged for next-sprint follow-up; not a v1.1.0
blocker

### What

Chapter 8 line 275-282 shows the legacy-single-state refusals as:

```
$ roksbnkctl -w canada-roks cluster down
this workspace is legacy single-state; cluster and BNK trial share one state.
Use `roksbnkctl down` to tear down both, or migrate the state first

$ roksbnkctl -w canada-roks bnk down
this workspace is legacy single-state; `bnk down` can't isolate the trial phase.
Use `roksbnkctl down` to tear down both, or migrate the state first
```

The actual `errors.New(...)` literals in `cluster_phase.go:360` and
`bnk_phase.go:134` emit **one** line each — there's no newline between
"... share one state." and "Use `roksbnkctl down` ...". The chapter's
two-line wrap is a typesetting choice (the line gets long) but readers
who paste-grep against `cluster and BNK trial share one state.` will get
a partial-line match and have to scroll/grep again for the resolution
half. A reader who grep'd `share one state. Use` won't match anything
in their terminal output.

Chapter 11's catalogue table (line 134, 136) keeps both halves on one
table-cell row, so chapter 11 grep behavior is clean. Just chapter 8's
illustrative quote is wrapped.

### Proposed fix (next sprint, low priority)

Re-flow chapter 8's sample to one logical line per refusal (table or
inline `code` rather than fenced block; or drop the `.` between "state"
and "Use" to flow as the binary actually emits). Validator's live
evidence (Issue 5 of `issue_sprint8_validator.md`) shows the real
single-line shape against the `canada-roks` workspace and can be
verbatim'd in.

### Why not high

The chapter 11 catalogue is the canonical user-facing surface (linked
from chapter 8 line 284 + chapter 10 line 272 + the CHANGELOG). A reader
who grep'd against the wrapped chapter-8 quote and didn't get a match
will land on the chapter 11 catalogue within two scroll-pages — the
text is too distinctive to mis-grep entirely. Low-severity polish.

---

## Issue 3 (LOW — chapter 10's bnk-up sample says "Two prompts fire" but the empty-workspace path actually fires three)

**Files**: `book/src/10-deploying-bnk-trials.md:230`

**Owner**: architect (chapter prose)

**Severity**: low (off-by-one in a parenthetical aside; does not affect
the user's path through the chapter)

**Status**: open — flagged for next-sprint follow-up

### What

Chapter 10 line 230 says:

> Two prompts fire in the empty-workspace case — one for "do you want to
> bootstrap the cluster phase," and one for "apply this terraform plan"
> inside the nested `cluster up` (and a third when the trial-phase apply
> also prompts unless `--auto` is set). For a 30-minute operation we
> kept the prompts explicit rather than collapsing them. `--auto` skips
> all three:

The opening clause says "**Two** prompts fire" and the parenthetical
correctly notes "**a third**" when the trial-phase apply prompt also
fires. The closing line then says `--auto` skips **all three**. The
arithmetic is right; the opening "Two" undercounts by one. A reader
who skims the opening sentence will be surprised at the third prompt.

The actual flow in `runBnkUp` (`internal/cli/bnk_phase.go:99-110`):

1. `bnk up` bootstrap confirm prompt (line 102 `promptYesNo("Continue?", false)`)
2. nested `runClusterUp` apply confirm (`cluster_phase.go:324 promptYesNo("Apply this plan?", false)`)
3. `runTrialUp` apply confirm (`lifecycle.go:193 promptYesNo("Apply this plan?", false)`)

So three prompts in the empty-workspace path without `--auto`. Chapter
10's count of three (in the parenthetical + the `--auto` line) is the
right number; the opening "Two" is the off-by-one.

### Proposed fix (next sprint)

```diff
-Two prompts fire in the empty-workspace case — one for "do you want to
-bootstrap the cluster phase," and one for "apply this terraform plan"
-inside the nested `cluster up` (and a third when the trial-phase apply
-also prompts unless `--auto` is set).
+Three prompts fire in the empty-workspace case — one for "do you want to
+bootstrap the cluster phase," one for "apply this terraform plan" inside
+the nested `cluster up`, and a third when the trial-phase apply prompts.
+(Without the cluster-bootstrap prompt — i.e. on a non-empty workspace
+where `bnk up` skips straight to the trial — there are only two.)
```

### Why not medium

The closing `--auto` sentence (line 232 "`--auto` skips all three") tells
the user the correct total. A reader who hits the surprise-third-prompt
will look back at the closing line and find the `--auto` answer. The
nit doesn't change the path through the chapter.

---

## Issue 4 (LOW — chapter 11 decision tree's "v1.0.x workspace" branch makes the reader hop to chapter 8 to confirm their shape)

**Files**: `book/src/11-tearing-down.md:9-22`

**Owner**: architect (chapter prose)

**Severity**: low (one extra page-hop in the diagnostic loop; not a
blocker)

**Status**: open — flagged for next-sprint follow-up

### What

Chapter 11's phase-aware decision tree (line 9-22) ends with:

```
I'm on a v1.0.x workspace (cluster + trial in one state):
    → roksbnkctl down       (tears down everything in one shot)
    → see Chapter 8 §"Legacy single-state workspaces" to confirm your shape
```

A reader who landed on chapter 11 because they hit a `bnk down` legacy
refusal already knows their workspace is legacy single-state — the
refusal text told them so. But a reader who landed on chapter 11
**before** trying a phase-scoped command and wants to choose the right
verb has no quick "am I legacy?" check in chapter 11 itself; they have
to hop to chapter 8 §"Legacy single-state workspaces" (chapter 8 line
251).

The dogfooding-stuck-point: a reader who's iterated on BNK against an
old workspace (likely upgraded from v1.0.x) and is reading chapter 11
to plan their teardown wants a one-line "look for `state-cluster/` —
present means split, absent means legacy" right in chapter 11. The
chapter-8 §"Legacy single-state workspaces" already has this exact
test (lines 256-263 — `ls ~/.roksbnkctl/<ws>/`).

### Proposed fix (next sprint)

Inline a single-line shape-check into chapter 11's decision tree, right
after the v1.0.x branch:

```diff
 I'm on a v1.0.x workspace (cluster + trial in one state):
     → roksbnkctl down       (tears down everything in one shot)
     → see Chapter 8 §"Legacy single-state workspaces" to confirm your shape
+
+ Quick shape check: `ls ~/.roksbnkctl/<workspace>/` — if you see
+ `state-cluster/`, you're on the v1.1.0 split shape; if you see only
+ `state/`, you're on the legacy single-state shape.
```

### Why not medium

Dogfooding loop 3 ("v1.0.x user lands on chapter 11 decision matrix —
do they figure out they're on legacy single-state without reading the
entire chapter?") completes within two hops (decision tree → chapter
8). Two hops is not great but not failure. The proposed fix collapses
it to one chapter.

---

## Issue 5 (LOW — chapter 10's dispatch matrix introduces shape names without inline glosses; reader hops to chapter 8 for definitions)

**Files**: `book/src/10-deploying-bnk-trials.md:263-270`

**Owner**: architect (chapter prose)

**Severity**: low (mitigated by parenthetical column-header glosses in
the matrix itself)

**Status**: open — flagged for next-sprint follow-up

### What

Chapter 10's dispatch matrix (line 263-270) uses column headers:

```
| **Empty** (nothing applied) | **ClusterOnly** (`cluster up` ran) | **Split** (cluster + trial both applied) | **LegacySingle** (v1.0.x state) |
```

Each column header carries a one-phrase parenthetical gloss, which is
enough for a reader who's already in the trial-vs-cluster mental model.
The dogfooding loop 2 reader (lands on chapter 10 from a "bnk" search,
hasn't read chapter 8) gets the shape names without much friction.

But the chapter-10 prose before the matrix (line 261) says "they detect
the on-disk shape of the workspace and delegate to the right phase
commands underneath" without ever explicitly listing the four shapes
the chapter is about to enumerate. A reader who didn't read chapter 8
sees "shape" → matrix → wonders if "Empty" means "no workspace
config.yaml" or "no terraform applied yet" (the latter; chapter 8 makes
this explicit at line 257).

### Proposed fix (next sprint)

A one-paragraph "the four shapes" pre-amble before the matrix would
sharpen this. Or a parenthetical mention in line 261 like "...the
on-disk shape of the workspace (one of four: Empty, ClusterOnly, Split,
LegacySingle — see Chapter 8 §'Legacy single-state workspaces' for the
ls-test that distinguishes them)..."

### Why not medium

The column-header parentheticals do enough work for the immediate
question ("what does each shape mean?"). The reader's question after
that ("which shape am I on?") points them at chapter 8 — which they
should be reading anyway for the underlying two-phase concept.
Low-severity polish.

---

## Issue 6 (INFORMATIONAL — drift sweep and dogfooding pass results)

**Files**: n/a (verification log)

**Owner**: tech-writer

**Severity**: informational

**Status**: complete

### Refusal-text drift sweep (high-severity surface; zero drift found)

Per Sprint 8 prompt task 3: PRD 06 §"Refusal messages" ↔ staff source
literals (`internal/cli/bnk_phase.go` lines 96, 134, 136 and
`internal/cli/cluster_phase.go` lines 299, 360, 362, 364) ↔ chapter 11
§"Refusal messages catalogue" quotes (`book/src/11-tearing-down.md`
lines 134-141). Compared character-by-character:

| Refusal | PRD | Source code | Chapter 11 | Drift |
|---|---|---|---|---|
| `bnk up` on LegacySingle | line 141 | `bnk_phase.go:96` | ch.11:141 | none |
| `bnk down` on LegacySingle | line 142 | `bnk_phase.go:134` | ch.11:134 | none |
| `bnk down` on Empty/ClusterOnly | line 143 | `bnk_phase.go:136` | ch.11:135 | none |
| `cluster up` on LegacySingle | line 144 | `cluster_phase.go:299` | ch.11:140 | none |
| `cluster down` on LegacySingle | line 145 | `cluster_phase.go:360` | ch.11:136 | none |
| `cluster down` on Split | line 146 | `cluster_phase.go:362` | ch.11:137 | none |
| `cluster down` on Empty | line 147 | `cluster_phase.go:364` | ch.11:138 | none |
| `down` on Empty | line 148 | `lifecycle.go:281` | ch.11:139 | none |

All eight refusals byte-identical across the three surfaces. Validator's
Issue 5 reached the same conclusion via live runs against the
`canada-roks` workspace + a handcrafted empty workspace.

### Dispatch-table drift sweep

PRD 06 §"Dispatch table" (line 90-97 of the PRD) ↔ chapter 10
§"The shape dispatch matrix" (line 263-270). Verified against the live
source (`runUp` switch lines 133-153 + `runDown` switch lines 277-291
in `lifecycle.go`; `runBnkUp` flow lines 95-110 + `runBnkDown` switch
lines 132-137 in `bnk_phase.go`; `runClusterUp` line 298 + `runClusterDown`
switch lines 358-365 in `cluster_phase.go`). All 24 cells match.

The user-facing chapter-10 matrix correctly translates the PRD's
engineering vocabulary ("`cluster up` (no-op refresh)" → "`cluster up`
(refresh)"; "monolithic trial up" → "monolithic trial up (v1.0.x
behaviour)"); the semantics are identical.

### CHANGELOG ↔ binary surface

`go build -o /tmp/rbk-s8 ./cmd/roksbnkctl && /tmp/rbk-s8 --help` lists
`bnk` alongside `cluster` (verified — line 21 of root help). `bnk --help`
lists `up` and `down` (verified). `bnk up --help` and `bnk down --help`
list the flags claimed by CHANGELOG line 241 (`--auto`, `--var-file`,
`--no-kubeconfig` on `up` only). No drift.

### PLAN.md §"Sprint 8" gate criteria status

| Gate criterion | Status |
|---|---|
| All four agents' issue files at Status: resolved or accepted | met (3 agents resolved + tech-writer this file) |
| `go build/test/vet/gofmt` green | **blocked by carry-in** — see Issue 1; Sprint 8 surface itself is clean |
| Live verification documented | met (validator Issue 5 in `issue_sprint8_validator.md`) |
| Chapter 8/10/11 render cleanly in `mdbook build`; cross-links resolve | met (validator confirmed; integrator's heading-rename fix landed) |
| `roksbnkctl --help` lists `bnk` alongside `cluster` | met (verified above) |
| CHANGELOG `v1.1.0` entry final | met as `## Unreleased (v1.x)` — needs rename to `## v1.1.0 — <date>` at tag time per PLAN.md line 680 |

### Dogfooding loops — summary

Four scenarios traced; **zero stuck-points that would cause a user to
give up**, two minor friction points filed as Issues 4 and 5 (low):

| Loop | Path | Result |
|---|---|---|
| "I have a cluster, trial broke, redeploy without rebuilding" | ch.8 §"two state directories" → ch.10 §"`bnk up` / `bnk down`" → ch.10 §"Worked example — iterating on a BNK trial" | clean; the worked example IS the answer, 7-minute trial cycle vs 50-minute fresh-cluster cost |
| New reader lands on ch.10 `bnk` section first | ch.10 line 5 forward-links to ch.8; §"What deploying BNK means" defines trial layer; dispatch matrix glosses each shape | clean with one polish opportunity (Issue 5) |
| v1.0.x user lands on ch.11 decision matrix | decision tree branch → ch.8 §"Legacy single-state workspaces" → `ls ~/.roksbnkctl/<ws>/` shape check | two-hop; collapsible to one (Issue 4) |
| Grep'd a `bnk down` legacy refusal in the wild | exact-text match against ch.11 catalogue line 134, resolution column reads in <10s | clean; refusal text is verbatim |
