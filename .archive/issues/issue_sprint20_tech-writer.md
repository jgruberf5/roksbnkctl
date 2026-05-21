# Sprint 20 — tech-writer issues (release-publish hardening drift sweep)

> **Sprint 20 frame.** First regular work sprint post-`v1.6.4`.
> Tech-writer runs **after** the integrator has landed staff +
> validator's deliverables. Drift sweep over the Makefile +
> docs/PLAN.md closure note. GREEN/RED launch verdict.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Post-integration drift sweep for `release-publish` hardening

**Severity**: low
**Status**: resolved

### Motivation

Sprint 20's surface is small (one Makefile target + a hermetic
test), but the same drift classes that bit Sprint 18 / 19 still
apply: a docstring that doesn't match the as-landed recipe, a
symmetric staleness risk on a neighbouring target that staff's
scope didn't anticipate, a `docs/PLAN.md` closure that omits the
hermetic-test runbook. Tech-writer catches them before the
v1.6.5 (or whatever-next-tag) cut.

### Drift surface to walk

For the `release-publish` hardening:

- **`Makefile` `release-publish` recipe** — verify the staff
  edit landed (rm-then-rebuild + exit-code gate). Cite line
  numbers.
- **`Makefile` `release-publish` docstring** — verify it
  mentions the Sprint 19 v1.6.4 stale-upload event in the
  rationale section.
- **Symmetric staleness risks** — read `book-publish`,
  `book-pdf`, `book`, `release`. Look for the same shape;
  flag any that didn't get hardened (severity tag per finding).
- **`docs/PLAN.md` §"Sprint 20"** — verify the closure
  subsection is present, names the hermetic-test runbook,
  and names the v1.6.4 stale-upload event as the precipitating
  trigger.
- **`README.md` / contributing docs** — verify they don't
  carry stale instructions referencing the un-hardened recipe.

### Acceptance criteria

1. Every finding in this issue's Closure section names a
   specific file path + line number.
2. Findings tagged by severity; each `high` finding blocks the
   release cut.
3. A final GREEN / RED launch verdict ends the closure.

### Out of scope

- Restyling Makefile targets; rewriting recipe bodies. Drift
  sweep only — recommend fixes, don't apply them.
- Touching any non-`issues/` file. Read-only on existing repo
  content.

### Optional Part B (≤2 issues)

If the integrated work surfaces a cross-cutting release-tooling
gap the other roles didn't close, file it as Issue 2 (or 2+3)
here. Strict cap.

### Files affected

- `issues/issue_sprint20_tech-writer.md` (this file's Closure
  section). Read-only on the integrated tree.

### Related

- Staff Issue 1, validator Issue 1 — both reviewed for drift.
- Sprint 19 tech-writer Issue 1 — the precedent shape for this
  work (8 low-sev findings → all informational → GREEN).

---

## Closure — tech-writer, 2026-05-21

Drift sweep over the post-integration tree (commit `973fc67` —
"sprint20: three-way integration — release-publish stale-artifact
gate"). Hermetic gate GREEN: 8/8 PASS, run-id `20260521-145005`
(log present at `/tmp/roksbnkctl-test-release-publish-staleness/run-20260521-145005.log`).

### Findings — drift surface walked

#### F1 — `Makefile` `release-publish` recipe: staff edits landed correctly (informational)

**Severity**: low (positive confirmation)
**File**: `Makefile:420-458`

The `release-publish` recipe now carries the rm-then-rebuild
shape on both publish steps:

- **HTML side** (`Makefile:429-436`): `==> [1/2] Rebuilding +
  pushing HTML book to gh-pages` echo → 4-line `@#` rationale
  comment (lines 430-433) → un-`@`-prefixed `rm -rf
  book/book/html/` (line 434) → un-`@`-prefixed `$(MAKE) book
  BOOK_BACKEND=docker` (line 435) → `@$(MAKE) book-publish`
  (line 436).
- **PDF side** (`Makefile:438-454`): `==> [2/2] Rebuilding +
  uploading PDF book to GitHub Release $(VERSION)` echo (line
  438) → 5-line `@#` rationale comment (lines 439-443) →
  un-`@`-prefixed `rm -rf book/book/pandoc/` (line 444) →
  un-`@`-prefixed `$(MAKE) book-pdf BOOK_BACKEND=docker` (line
  445) → existing `gh release upload` block (lines 451-454).

Exit-code gate: the `$(MAKE) book` / `$(MAKE) book-pdf` lines
are un-`@`-prefixed (visible in recipe echo) and rely on
recursive-make's default exit-propagation. A non-zero from
either rebuild aborts `release-publish` before any `gh release
upload` runs — pinned hermetically by validator's S1 + S2
scenarios (`scripts/test-release-publish-staleness.sh:367-383`,
both asserting `gh release upload was never called`).

Confirmed GONE: the `@if [ ! -f book/book/pandoc/pdf/book.pdf
]; then ... exit 2; fi` existence prereq that lived in the
pre-edit recipe (visible in the `git diff e357025 973fc67 --
Makefile` removal block) — its removal is correct, since the
new `rm -rf` + `$(MAKE) book-pdf` step replaces it (a rebuild
that fails to produce the file exits non-zero anyway).

No drift between recipe and what staff's audit table at
`issues/issue_sprint20_staff.md:177-184` claims landed.

#### F2 — `Makefile` `release-publish` docstring: rationale + Sprint 19 v1.6.4 event named (informational)

**Severity**: low (positive confirmation)
**File**: `Makefile:384-419`

The docstring block now carries three relevant additions vs
the pre-edit version:

- **Step-1/2 summary** updated to `Clean + rebuild` framing
  (`Makefile:388-389`).
- **Publish-contract framing paragraph** (`Makefile:395-402`):
  names the target as the **publish-contract gate**, not the
  build-contract gate, and explains that "a developer
  iterating locally may legitimately have a stale `book/book/`
  tree lying around, and the publish gate must not propagate
  it".
- **Sprint 19 v1.6.4 event narrative paragraph**
  (`Makefile:404-413`): names the date (2026-05-21), the
  stale-artifact path (`book/book/pandoc/pdf/book.pdf`), the
  v1.6.3 vintage, the silent `book-pdf` build failure, the
  old `[ -f book.pdf ]` shape of the prereq, and the
  symmetric HTML "nothing to push" event. Closes with a
  cross-reference: "See `prompts/sprint20/README.md` for the
  full event narrative".
- **Prereq list** (`Makefile:415-419`) correctly drops the
  pre-edit "book/book/html/ and book/book/pandoc/pdf/book.pdf
  exist" item (the rebuild-here contract obsoletes it) and
  gains a `BOOK_BACKEND=docker` prereq line.

Meets staff Issue 1 AC #3 and the README §"Per-role scope"
Staff-row deliverable. No drift.

#### F3 — `book-publish` standalone retains symmetric staleness risk by design (informational)

**Severity**: low (informational — design choice, not a defect)
**File**: `Makefile:359-382`

`book-publish` standalone (i.e., invoked directly, not via
`release-publish`) carries the **same** "publish whatever is
on disk" shape that bit `release-publish` in the v1.6.4 cut:

- `Makefile:360-363` — existence-only prereq `[ ! -d
  book/book/html ]`. Cannot distinguish "fresh" from
  "stale-but-present".
- `Makefile:374` — `git diff --cached --quiet` may say
  "nothing to push" when the local `book/book/html/` is a
  prior cycle's bytes (the actual v1.6.4 event reported in
  `prompts/sprint20/README.md:34`).

Staff's per-target audit table at
`issues/issue_sprint20_staff.md:181-182` explicitly chose to
leave the `book-publish` recipe body untouched on the
rationale that "`book-publish` is also a standalone
contributor entrypoint (a contributor iterating on the book
may want to push the current `book/book/html/` without an
unconditional docker rebuild)", and instead placed the
symmetric hardening in `release-publish`'s call site
(`Makefile:434-435`).

This is a **defensible design decision**, not a defect — but
worth noting in the closure because a maintainer running
`make book-publish` standalone post-tag-cut (outside the
`release-publish` flow) reproduces the v1.6.4 stale-upload
shape exactly. **Recommendation** (not a blocker; integrator
discretion): consider a future low-cost addition — either
(a) a one-line stderr warning in `book-publish` (`Makefile:359`
recipe header) like `"note: book-publish does not rebuild;
run 'make book BOOK_BACKEND=docker' first if you intend to
publish current content"`, or (b) a docstring sentence above
`book-publish` (`Makefile:347-358`) explicitly stating "no
rebuild — caller's responsibility to ensure
`book/book/html/` reflects the tagged tree". No code-body
change needed; pure ergonomic nudge. Defer to a future polish
sprint or fold into the same release if cheap.

#### F4 — `book` / `book-pdf` build targets carry no publish-path staleness (informational)

**Severity**: low (positive confirmation)
**Files**: `Makefile:64-69` (book), `Makefile:82-110` (book-pdf)

Neither `book` nor `book-pdf` is on a publish path (neither
invokes `gh release upload` nor pushes to gh-pages). Both
correctly exit non-zero on build failure (verified for
`book-pdf` at `prompts/sprint20/README.md` Integrator decision
#1: "the v1.6.4 cycle's `book-pdf` invocation correctly
returned exit 101 with the pandoc error"). They do not clean
their own output trees pre-build, but that is **build-contract
shape**, not **publish-contract shape** — a developer running
`make book` locally legitimately wants incremental rebuilds.
The publish-side hardening lives in `release-publish`
(`Makefile:434, 444`) which is the correct call site per
integrator decision #2 (README §"Integrator decisions baked
in"). No symmetric publish-side staleness here.

#### F5 — `release` target consumes book-pdf output but does NOT publish (informational)

**Severity**: low (positive confirmation)
**File**: `Makefile:277-345`

`release` step `[5/8]` invokes `$(MAKE) book-pdf
BOOK_BACKEND=docker` (`Makefile:313-314`) and step `[7/8]`
runs `goreleaser-snapshot` (`Makefile:319-320`).
`goreleaser-snapshot` no longer consumes `book.pdf` via
`extra_files` (confirmed at `.goreleaser.yml:108`: "Earlier
versions of this config had `extra_files` pointing at the PDF.
[removed]"; also documented in `CHANGELOG.md:377` for v1.0.1).
Step `[5/8]`'s output is consumed only by the integrator's
eyes via the `ls -la` at `Makefile:326`, and downstream the
upload happens exclusively in `release-publish` (now
hardened). `release` is a local-only driver; no symmetric
publish-path staleness exists here.

#### F6 — `docs/PLAN.md` §"Sprint 20" lacks a `Closure` subsection (medium)

**Severity**: medium
**File**: `docs/PLAN.md:1146-1163` (Sprint 20 section ends at line 1163; Sprint 19 begins at line 1167)

The Sprint 20 section in `docs/PLAN.md` consists of the
header (line 1146), motivating paragraph (line 1148), per-role
scope table (lines 1150-1157), the live-verify caveat (line
1159), the version-at-cut sentence (line 1161), and the
sprint-launch sentence (line 1163). There is **no `### Closure
— 2026-05-21` subsection** of the shape the Sprint 19 entry
carries (`docs/PLAN.md:1186` — "Closure — 2026-05-21 (cut as
`v1.6.4`)") or the Sprint 18 entry (`docs/PLAN.md:1212` —
"Closure (2026-05-20)").

The issue spec (this file, lines 40-43) explicitly requires
the closure subsection to:

- be present
- name the hermetic-test runbook (`scripts/test-release-publish-staleness.sh`
  + the 20260521-145005 run-id)
- name the v1.6.4 stale-upload event as the precipitating
  trigger

**Recommendation** (integrator owns the patch — tech-writer is
read-only on `docs/PLAN.md` per the discipline rules): add
between `docs/PLAN.md:1163` and the `---` at line 1165 a
`### Closure — 2026-05-21` subsection naming:

- The integration commit hash (`973fc67`).
- The hermetic-test runbook: `scripts/test-release-publish-staleness.sh`,
  run-id `20260521-145005`, 8/8 PASS (S1 HTML-side + S2
  PDF-side, each with A1+A2+A3+A4 assertions per the test's
  internal numbering at lines 290-357).
- The precipitating trigger: the v1.6.4 stale-PDF-upload
  event (already described in this section's motivating
  paragraph at line 1148, so a cross-back-reference is fine).
- The `Makefile:420-458` line range as the as-landed recipe
  surface.
- Optional: F3 above as a deferred follow-up note (the
  `book-publish` standalone ergonomic nudge).

This is the only `medium`-sev finding in the sweep. It does
not block the next release cut on its own (no user-facing
surface — `docs/PLAN.md` is sprint-archive prose), but the
issue spec named the absence as a finding the integrator must
address, and the precedent shape from Sprint 18 / Sprint 19
makes the addition cheap.

#### F7 — `README.md` carries no `release-publish` references → no drift (informational)

**Severity**: low (positive confirmation)
**File**: `README.md` (verified clean via `grep -n
"release-publish\|book-publish\|book-pdf" README.md`,
zero matches)

The top-level `README.md` does not reference the release
pipeline at all. No stale instructions to drift. The release
pipeline narrative lives in `CONTRIBUTING.md` (see F8 below).

#### F8 — `CONTRIBUTING.md` §"Releasing" prose still accurate post-edit (low)

**Severity**: low
**File**: `CONTRIBUTING.md:459-529`

The §"Releasing" section (lines 459-485) describes `make
release` as the canonical driver and does NOT describe the
`release-publish` post-tag step in detail — it stops at the
push step (line 478: `git tag v1.0.0 && git push origin main
--tags`) and then describes what the CI workflows do
(`release.yml` publishes; line 484 mentions `release.extra_files`
for the PDF, which contradicts the `.goreleaser.yml:105-108`
+ `CHANGELOG.md:377` removal — but this is a **pre-existing
drift, NOT Sprint 20 surface**: the PDF-via-`extra_files` line
at `CONTRIBUTING.md:484` was already stale before Sprint 20,
referring to a v1.0.x era arrangement that v1.0.1 changed).

The §"Individual targets" subsection at `CONTRIBUTING.md:502-516`
lists `make book-pdf BOOK_BACKEND=docker` and friends but
makes no claim about `release-publish`'s pre-edit prereq
shape, so the Sprint 20 staff edit does not orphan any prose
here.

**Pre-existing-drift note** (NOT a Sprint 20 blocker): the
`release.extra_files` reference at `CONTRIBUTING.md:484` has
been stale since the v1.0.1 cut per `CHANGELOG.md:377`. Out
of Sprint 20's drift surface (the goreleaser-side change
predates this sprint), but worth surfacing as informational
for whoever next edits §"Releasing". Recommendation: replace
the `release.extra_files` clause at `CONTRIBUTING.md:484`
with "and `make release-publish VERSION=…` uploads the PDF
asset separately from the integrator's machine, per the
Makefile's `release-publish` target". Strictly informational
— integrator may defer.

#### F9 — `CHANGELOG.md` lacks an Unreleased / Sprint 20 entry (low)

**Severity**: low
**File**: `CHANGELOG.md:1-9`

`CHANGELOG.md`'s topmost block is v1.6.4 (Sprint 19). There
is no `## Unreleased` block carrying Sprint 20's hardening,
which is consistent with `docs/PLAN.md:1161`'s integrator-
owned version-cut guidance ("Internal tooling fix → likely a
patch bump (`v1.6.5`) bundled with whatever user-facing
changes ride along; could also defer to the next user-facing-
feature cut and ride that release").

**Recommendation** (per the spec's drift-surface item #6 —
"if you have a strong recommendation either way, name it as
a finding"): when the integrator cuts the next user-facing
release (whether that's `v1.6.5`-as-tooling-only or
`v1.7.0`-with-features), add a `### Internal` or `### Build`
subsection bullet noting "`make release-publish` is now
rebuild-then-upload; a prior cycle's stale book/book/ tree
cannot survive into a publish" with a cross-link to
`issues/issue_sprint20_staff.md`. Sprint 20 was internal
tooling, but the v1.6.4 stale-upload incident WAS user-
observable (a wrong PDF sat on a published Release for ~30
minutes), and the v1.6.5+ cycle's CHANGELOG should briefly
name that incident's resolution so a user comparing PDFs
across releases has the context. This is a **`low`-sev nudge,
not a blocker** — defer if the next cut bundles enough
user-facing work that this is noise; surface it if Sprint 20
ships as a standalone tooling patch.

### Findings summary

| # | Severity | Finding | File:line | Status |
|---|---|---|---|---|
| F1 | low (positive) | `release-publish` recipe: staff edits landed | `Makefile:420-458` | informational |
| F2 | low (positive) | `release-publish` docstring: rationale + v1.6.4 event | `Makefile:384-419` | informational |
| F3 | low | `book-publish` standalone retains symmetric staleness by design | `Makefile:359-382` | informational; future nudge |
| F4 | low (positive) | `book` / `book-pdf` no publish-path staleness | `Makefile:64-69, 82-110` | informational |
| F5 | low (positive) | `release` target consumes but does NOT publish | `Makefile:277-345` | informational |
| F6 | **medium** | `docs/PLAN.md` §"Sprint 20" lacks Closure subsection | `docs/PLAN.md:1146-1163` | integrator-applies |
| F7 | low (positive) | `README.md` has no release-pipeline references | `README.md` (zero matches) | informational |
| F8 | low | `CONTRIBUTING.md:484` pre-existing-drift on PDF/extra_files | `CONTRIBUTING.md:459-529` | informational; defer |
| F9 | low | `CHANGELOG.md` Sprint 20 entry deferred to next cut | `CHANGELOG.md:1-9` | integrator discretion |

**Totals**: 9 findings — 0 high, 1 medium, 8 low (5 positive
confirmations + 3 informational/recommendations).

### Launch verdict

**GREEN — READY TO CUT.**

- Zero `high`-sev findings. No blockers on the next release
  cut.
- The single `medium`-sev finding (F6 — `docs/PLAN.md`
  closure subsection missing) is sprint-archive prose, not a
  user-facing surface; the issue spec named it as a finding
  the integrator must address but it does not block the
  release.
- The hermetic gate is GREEN (8/8 PASS, run-id
  `20260521-145005`) — the staff edit + validator's test +
  the as-landed Makefile shape match the contract.
- Integrator's live test (per `docs/PLAN.md:1159`: "whoever
  cuts the next release runs `make release-publish
  VERSION=v…` against a tree with a planted stale
  `book.pdf` and confirms the rebuild fires + the right PDF
  gets uploaded") remains owed at the next-tag-cut moment;
  Sprint 20's closure does NOT require that live verify in
  advance per the same line ("the hermetic test is
  sufficient closure").

### Part B

None filed. The drift sweep surfaced no cross-cutting
release-tooling gap the other roles didn't close. The two
`low`-sev recommendations (F3 ergonomic nudge on
`book-publish`, F8 pre-existing-drift on `CONTRIBUTING.md:484`)
are out-of-scope for Sprint 20's surface and small enough to
fold into the next polish cycle or the next release's
tech-writer pass — neither warrants its own issue. The F9
CHANGELOG recommendation is integrator-owned at cut time,
not a separate issue.

### Status flip

`**Status**: open` → `**Status**: resolved` (above). The
single `medium`-sev finding (F6) is a recommendation for the
integrator's discretion, not a tech-writer-fixable blocker;
per the discipline rules ("Read-only on every non-`issues/`
file ... tech-writer recommends, doesn't apply"), the flip
to `resolved` is correct.
