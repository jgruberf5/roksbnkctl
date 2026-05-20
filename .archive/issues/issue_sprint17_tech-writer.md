# Sprint 17 — tech-writer issues (backlog grooming via GitHub issue drafts)

> **Sprint 17 frame.** Backlog-grooming sprint, post-`v1.6.2`. The
> tech-writer agent runs **after** the other three integrate, then
> does both (A) a drift / consistency / clarity review of every draft
> under `prompts/sprint17/staging/{staff,architect,validator}/` and
> (B) draft up to **≤5** of their own cross-cutting documentation
> issues into `prompts/sprint17/staging/tech-writer/`. Quality >
> volume.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

# Part A — drift / consistency / clarity findings

Severity scale: `low` (cosmetic / consolidation), `medium`
(material claim mismatched to the codebase / non-actionable acceptance
criterion), `high` (would file an incorrect GitHub issue if left).
Status scale: `open` (integrator action expected) or `accepted` (we
considered it and pass — no action needed).

Findings are followed by a Summary block at the end of Part A.

---

## Finding 1 — 14 of 19 draft titles exceed 80 chars (template shape says ≤80, one line)

**Severity:** medium
**Status:** open
**Description:**

The Sprint 17 README + the bug/feature templates pin the title shape as
`bug: <one-line symptom>` / `feat: <one-line summary>` — one-line, ≤80
chars. Fourteen of the 19 staged drafts ship titles longer than 80
characters (measured against the literal `title:` frontmatter value,
not including the leading `'`/`"` quote):

| Draft | Title chars |
|---|---|
| `staff/01-doctor-target-backend-prefix.md` | 132 |
| `staff/02-bnk-phase-override-source-label.md` | 218 |
| `staff/03-per-az-jumphost-reconcile-option-b.md` | 186 |
| `staff/04-skip-cluster-refresh-flag.md` | 152 |
| `staff/05-roksbnkctl-migrate-legacy-single.md` | 180 |
| `staff/06-ops-install-uninstall-snapshot.md` | 192 |
| `staff/07-openshift-typed-client-k-get.md` | 180 |
| `architect/01-book-broken-anchor-crossrefs.md` | 90 |
| `architect/05-release-footer-pdf-race.md` | 141 |
| `architect/06-release-rerelease-opaque-failure.md` | 141 |
| `validator/01-ci-test-not-hermetic-home-kubeconfig.md` | 110 |
| `validator/02-ci-orchestration-cli-boundary-grep.md` | 91 |
| `validator/03-pretag-live-verify-runid-gate.md` | 127 |
| `validator/05-orchestration-lifecycle-coverage-hole.md` | 237 |
| `validator/06-e2e-drivers-a5-a6-parity.md` | 141 |

Only four drafts come in cleanly ≤80 (`architect/02`, `architect/03`,
`architect/04`, `validator/04`).

GitHub's issue-list view truncates titles around the 70-80 char range;
titles longer than that are also painful to paste into commit
messages, PR titles, and CHANGELOG bullets.

**Suggested fix:**

The integrator condenses each over-length title down to a ≤80-char
one-liner at `gh issue create --title` time. Suggested rewrites
(integrator's call — each draft's body already carries the long-form
detail in §Symptom / §Motivation):

- `staff/01` → `bug: doctor --target row missing [ssh] backend prefix`
- `staff/02` → `bug: applied.tfvars section header for bnk-phase-override.tfvars uses raw basename`
- `staff/03` → `feat: per-AZ jumphost reconcile + auto: ownership marker (PRD 09 option (b))`
- `staff/04` → `feat: --skip-cluster-refresh on composite up for stable ShapeSplit workspaces`
- `staff/05` → `feat: roksbnkctl migrate — legacy-single → two-phase state move`
- `staff/06` → `feat: ops.applied.json snapshot — record ops install/uninstall (PRD 07 Q1)`
- `staff/07` → `feat: OpenShift typed-client for k get projects/routes/imagestreams (PRD 02 Phase 2.1)`
- `architect/01` → `bug: book — seven intra-chapter anchor cross-refs are silent dead links`
- `architect/05` → `bug: release.footer advertises PDF as attached before make release-publish runs`
- `architect/06` → `bug: release.yml workflow_dispatch re-release on existing tag fails opaquely`
- `validator/01` → `bug: CI go test step does not match the documented hermetic HOME/KUBECONFIG shape`
- `validator/02` → `feat: CI gate — internal/orchestration must not import internal/cli`
- `validator/03` → `feat: pre-tag gate — require live-verify run-id for every high-sev CHANGELOG fix`
- `validator/05` → `bug: internal/orchestration 12.1% coverage — lifecycle.go + cluster.go untested directly`
- `validator/06` → `bug: e2e-test{,-full}.sh lack the A5/A6 applied-tfvars replay gates phase-handoff ships`

Each suggested title preserves the `bug:`/`feat:` prefix and the
load-bearing nouns; the full claim/scope detail moves to the body
where it already lives.

---

## Finding 2 — `staff/07-openshift-typed-client-k-get.md` references a non-existent test file

**Severity:** medium
**Status:** open
**Description:**

Acceptance criterion #2 reads:

> "verifiable by setting a debug log line + asserting in a hermetic
> test against a fake `restclient` per the existing
> `internal/cli/k_get_test.go` pattern, additive — not editing a
> pre-existing test"

`internal/cli/k_get_test.go` does not exist on the current tip of
`main`. `internal/cli/k_get.go` is present but has no co-located test
file; the k8s-side tests live under `internal/k8s/get_test.go` and
neighbours. The "existing pattern" the criterion points at is
fictitious.

`Files likely touched` also lists `internal/cli/k_get_test.go (additive
new test functions)` — that's fine as a *to-be-created* file, but the
acceptance criterion's "per the existing … pattern" wording reads as
if the pattern already exists in the cli package.

**Suggested fix:**

Rewrite the criterion's last clause at filing time. Suggested:

> "…hermetic test against a fake `restclient`, mirroring the
> `internal/k8s/get_test.go` fake pattern. New test file
> (`internal/cli/k_get_test.go`); no edits to existing tests."

This swaps the phantom reference for the real existing pattern (which
the implementer will read anyway), keeps the additive-new-file
constraint intact, and unblocks the acceptance criterion from a
file-not-found objection on review.

---

## Finding 3 — `architect/01-book-broken-anchor-crossrefs.md` undercount: 8 dead anchors in the slug-collapse class, not 7

**Severity:** low
**Status:** open
**Description:**

The draft title, §Symptom, and §Reproduction all assert "seven" dead
anchors and enumerate seven `[..](./X.md#anchor)` cross-file refs. A
re-run of the draft's own enumeration probe — extended to cover
*same-file* `[..](#anchor)` refs — surfaces an eighth instance of the
identical slug-collapse defect class in chapter 10 itself:

```
('10-deploying-bnk-trials.md', 5, 'the-bnk-up--bnk-down-command-group')
```

Chapter 10 line 5 links to its own §"The `bnk up` / `bnk down` command
group" subsection via `(#the-bnk-up--bnk-down-command-group)`, but the
heading at line 202 slugifies to
`the-bnk-up-bnk-down-command-group` (single dash). Same
author-typed-`--`-collapses-to-`-` defect class as the seven
cross-file links. The draft's probe regex
(`\[[^\]]+\]\(\.\/([^)#]+\.md)#([^)]+)\)`) is anchored on `./` so it
skips same-file refs by design — which is why the eighth instance was
missed.

Low-severity because (a) the draft's fix-class is correct, (b) the
companion CI-gate (`architect/02`) acceptance criterion #6 explicitly
covers `[..](#anchor)` same-file form ("**Anchor on the *same* file
(`[...](#anchor)`):** also checked — same slug rules"), and (c) the
eighth instance is trivially picked up alongside the seven in the
content PR.

**Suggested fix:**

The integrator either (a) appends one sentence to the §Symptom block
just before filing — "Update: re-running the same probe with a
same-file `\[[^\]]+\]\(#[^)]+\)` pattern surfaces an 8th instance at
`10-deploying-bnk-trials.md:5`; same defect class, fix in the same
PR" — or (b) leaves the issue as filed and the implementer's PR
naturally widens scope per criterion #1. Either is fine; (a) is more
honest to the title.

---

## Finding 4 — `validator/02` + `validator/05` overlap (orchestration-package gates) — paired, not duplicates

**Severity:** low
**Status:** accepted
**Description:**

Both `validator/02-ci-orchestration-cli-boundary-grep.md` and
`validator/05-orchestration-lifecycle-coverage-hole.md` operate on
`internal/orchestration/` and ratify the post-Sprint-16 package as a
first-class testable surface. The two are explicitly noted as paired
in validator/05 §Notes ("This issue's lift is most cost-effective
when paired with the boundary-grep gate (issue 02)").

They are NOT duplicates — validator/02 is a static-import-boundary
guard test (~80 LoC), validator/05 is a coverage-uplift work-item
(~500 LoC of new tests). Different invariants, different failure
modes, different implementer effort.

**Suggested fix:**

File as two separate GitHub issues. Accepted as-is. No consolidation
needed; the cross-link in validator/05 §Notes is sufficient. The
integrator may want to add a `Notes` line on the filed validator/05
GitHub issue saying "depends on validator/02 landing first" if the
implementer should pick them up in order.

---

## Finding 5 — `architect/04` + `architect/05` + `architect/06`: three release-pipeline issues — distinct, not duplicates

**Severity:** low
**Status:** accepted
**Description:**

The three architect drafts all touch the release workflow, but each is
a distinct defect class:

- `architect/04` — `ci.yml` lacks a pre-tag `goreleaser release
  --snapshot` smoke job. Catches template breaks before tag-push.
- `architect/05` — `.goreleaser.yml` `release.footer` references a
  PDF asset goreleaser doesn't attach (PDF is uploaded post-hoc by
  `make release-publish`). Lying-footer race.
- `architect/06` — `release.yml` `workflow_dispatch` re-run on an
  existing tag fails opaquely (raw goreleaser "Release already
  exists"; no preflight check).

The drafts cross-link each other accurately (architect/04 §Notes ↔
architect/06 §Notes; architect/05 §Notes ↔ architect/04 §Notes). No
overlap in load-bearing acceptance criteria — each touches a different
file path or a different region of `release.yml`.

**Suggested fix:**

File as three separate GitHub issues. Accepted as-is. Recommended PR
landing order if the integrator wants minimum churn: 04 (gate) →
06 (recovery-path opacity) → 05 (content asymmetry resolution).

---

## Finding 6 — `staff/03` per-AZ jumphost field naming ambiguity (`Auto` vs `auto:`/`managed_by:`)

**Severity:** low
**Status:** open
**Description:**

The draft title proposes `auto:`/`managed_by:` as alternatives, then
§"Proposed surface" commits firmly to `Auto bool` (yaml key
`auto: true|false`), and §"Out of scope" item 1 explicitly rejects
`ManagedBy: "roksbnkctl"` / `Owner: "auto"` as future shapes. The
reader has to read all the way to §"Out of scope" to discover the
title's `/managed_by:` alternative is *rejected*, not proposed.

**Suggested fix:**

Drop the `/managed_by:` fragment from the title at filing time (see
Finding 1's suggested rewrite: `feat: per-AZ jumphost reconcile +
auto: ownership marker (PRD 09 option (b))`). Body wording is fine —
the §"Out of scope" call-out is a load-bearing decision and stays
unchanged.

---

## Finding 7 — All 19 drafts: HTML placeholder comments deleted, sections present, AC/OOS counts compliant

**Severity:** low
**Status:** accepted
**Description:**

Verification sweep across all 19 drafts (`prompts/sprint17/staging/{staff,architect,validator}/`):

- `grep -l '<!--' prompts/sprint17/staging/{staff,architect,validator}/*.md` → empty (clean).
- Every bug-report draft has the required template sections: Symptom,
  Reproduction, Expected behavior, Actual behavior, Environment,
  Acceptance criteria, Out of scope, Notes — and most also include the
  optional Suspect-pipeline section. All AC blocks have ≥4 numbered
  items (range: 4-9). All OOS blocks have ≥3 items (range: 3-6).
- Every feature-request draft has Motivation, Proposed surface,
  Behavior, Acceptance criteria, Out of scope, Files likely touched,
  Notes. All AC blocks have ≥6 numbered items (range: 6-9). All OOS
  blocks have ≥4 items (range: 4-6).
- All frontmatter blocks are valid YAML with the correct `name: Bug
  report` / `name: Feature request` shape from the template.
- All `Files likely touched` paths in the four drafts where they are
  load-bearing for the spec (`staff/01-07`, `architect/04`,
  `validator/02`, `validator/03`, `validator/04`, `validator/05`,
  `validator/06`) name real on-disk files at the current tip — except
  the staff/07 phantom file flagged in Finding 2. Spot-verified:
  `internal/cli/meta.go:150`, `internal/config/applied_tfvars.go:298-310`,
  `internal/cli/cluster_phase.go:305,368`,
  `internal/cli/bnk_phase.go:99,138`, `internal/cli/root.go:131`,
  `internal/k8s/openshift.go`, `internal/orchestration/lifecycle.go:132`,
  `internal/orchestration/cluster.go` (functions named in
  validator/05 acceptance criteria all present),
  `internal/orchestration/applied_replay.go`, `.goreleaser.yml:86-95`,
  `.github/workflows/{ci,book,release,spellcheck}.yml`,
  `scripts/e2e-phase-handoff.sh:253-272,362-368` — all confirmed.

**Suggested fix:**

No action. Template compliance is clean across the corpus modulo the
staff/07 phantom test-file (Finding 2).

---

## Finding 8 — Cross-role book-adjacent touches (staff drafts that update book chapters as follow-on doc work) — not duplicates with architect/tech-writer

**Severity:** low
**Status:** accepted
**Description:**

Three staff drafts ask for a one-line / one-subsection book update as
part of a code-change PR:

- `staff/02` criterion #4 — review `book/src/06-workspaces.md`
  §"`terraform.applied.tfvars` — what's deployed right now" for any
  stale list of section-label strings.
- `staff/03` Files-likely-touched — update
  `book/src/15-ssh-targets.md` + `book/src/16-on-flag-ssh-jumphosts.md`
  to drop the v1.6.x "orphan caveat" wording.
- `staff/06` criterion #7 — add a §"`ops.applied.json` — what's
  installed on cluster right now" subsection to chapter 6 mirroring
  the existing `terraform.applied.tfvars` subsection.

The two architect book-side issues (`architect/01` seven dead anchor
fixes, `architect/02` CI gate for anchors) and the tech-writer's own
drafts (Part B below) are *book-quality work in its own right*.

The two surfaces are complementary, not overlapping — the staff
touches are *follow-on doc updates accompanying a code change*; the
architect/tech-writer touches are *book-side primary deliverables*.

**Suggested fix:**

No action. Accepted as-is. The integrator may want to add a `book`
label to each of the three staff drafts at filing time so book-PR
reviewers see them in the cross-label view, but that's a labelling
decision, not an issue-consolidation decision.

---

## Finding 9 — `validator/03` (pre-tag run-id gate) run-id regex shapes verified against real CHANGELOG prose

**Severity:** low
**Status:** accepted
**Description:**

Validator/03 §"Acceptance criteria" #5 names three real-world run-id
shapes the script must tolerate:

- `run-id \`20260519-202202\``
- `run-id 20260520-035616`
- `(\`20260519-234554\`)` inside parens

All three shapes are grep-confirmed present in `CHANGELOG.md` v1.6.2
section and the Sprint 16 closure prose. The acceptance criterion is
testable against the current tip without invention.

**Suggested fix:**

No action. Accepted as-is.

---

## Finding 10 — `staff/04` reference to `internal/orchestration/lifecycle.go` line numbers — close but slightly drifted

**Severity:** low
**Status:** accepted
**Description:**

Staff/04 §Motivation references "`internal/orchestration/lifecycle.go::RunUp`,
lines 126-134" for the `in.RunClusterUp(ctx)` call site. The actual
location at the current tip is `lifecycle.go:132` (the
`in.RunClusterUp(ctx)` line itself sits inside the `case
config.ShapeEmpty, config.ShapeSplit:` arm at lines 126-135). The
draft is off by a few lines but lands the reader inside the right
switch-case block, and the function name + the descriptive context
(`ShapeSplit` arm) is correct.

Low-severity because the line range is illustrative, not load-bearing
— the implementer reads the function name and lands in the right
place regardless. Mentioned for completeness.

**Suggested fix:**

No action. Accepted as-is. Line numbers in issue bodies stale on
every code-touch by definition; the function-name + switch-arm
reference is the durable handle.

---

## Part A Summary

- **Total findings:** 10
- **By severity:** low × 8, medium × 2, high × 0
- **By status:** open × 4 (Findings 1, 2, 3, 6), accepted × 6
  (Findings 4, 5, 7, 8, 9, 10)
- **Action-blocking for the integrator?** No. The two `medium`-severity
  findings (1: title-length compress, 2: staff/07 phantom test-file
  reference) can both be handled at `gh issue create --title` time
  (Finding 1) + a one-line tweak to staff/07 acceptance criterion #2
  just before piping the body to GitHub (Finding 2). None of the 19
  drafts is unsalvageable; none needs to be rejected.

Verdict to the integrator (also in the agent's final report): **GREEN
with two small surgical edits at filing time** — title compression
per Finding 1's table, and staff/07 criterion #2 wording fix per
Finding 2. The eight `low`-severity findings document acceptance /
small clarity improvements that do not block filing.

---

# Part B — closure (tech-writer's own drafts)

The tech-writer agent drafted ≤5 cross-cutting documentation issues
under `prompts/sprint17/staging/tech-writer/`. See those files for the
full bodies; this section is a one-line index that the integrator can
use as a TOC alongside the staff/architect/validator dirs.

(populated by the tech-writer's closure step — see
`prompts/sprint17/staging/tech-writer/*.md`.)
