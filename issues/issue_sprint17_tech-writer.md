# Sprint 17 tech-writer — drift/consistency/clarity review

Read-only pass over the 19 integrated drafts under
`prompts/sprint17/staging/{staff,architect,validator}/`. Each
finding names the specific draft file the integrator should
act on. Severity is `low` for cosmetic / consolidation,
`medium` for ambiguous-but-actionable, `high` for "the draft
is materially wrong about the repo."

Summary: **9 findings — 0 high / 4 medium / 5 low.** None block
filing the staged drafts; medium items should be addressed
either by the integrator before `gh issue create` or as
small edits the integrator applies pre-file. Verdict for the
final report: **GREEN with minor edits** — see findings 1, 4,
5, 8 for the four that warrant a 1-line fix before filing.

> **Note on operating-snapshot.** This review reflects the
> three-way integrated tree at commit 207acfa per the
> dispatcher prompt. The sprint has since been abandoned
> (commit 0df1ce4 archived the staging tree to
> `.archive/prompts/sprint17/`); the findings remain useful
> as a retrospective on what would-have-shipped quality
> looked like, and as input to any successor ad-hoc backlog-
> grooming pass.

---

## Finding 1 — title-shape compliance: 14 of 19 titles exceed 80 chars

**Severity**: medium
**Status**: open
**Files**:
- `prompts/sprint17/staging/architect/03-spellcheck-soft-gate.md` (~81 chars)
- `prompts/sprint17/staging/architect/01-book-broken-anchor-crossrefs.md` (~87 chars)
- `prompts/sprint17/staging/validator/02-ci-orchestration-cli-boundary-grep.md` (~86 chars)
- `prompts/sprint17/staging/validator/04-orphan-check-billing-guard.md` (~101 chars)
- `prompts/sprint17/staging/validator/01-ci-test-not-hermetic-home-kubeconfig.md` (~109 chars)
- `prompts/sprint17/staging/validator/03-pretag-live-verify-runid-gate.md` (~124 chars)
- `prompts/sprint17/staging/staff/01-doctor-target-backend-prefix.md` (~131 chars)
- `prompts/sprint17/staging/architect/05-release-footer-pdf-race.md` (~140 chars)
- `prompts/sprint17/staging/validator/06-e2e-drivers-a5-a6-parity.md` (~140 chars)
- `prompts/sprint17/staging/architect/06-release-rerelease-opaque-failure.md` (~140 chars)
- `prompts/sprint17/staging/staff/04-skip-cluster-refresh-flag.md` (~151 chars)
- `prompts/sprint17/staging/staff/05-roksbnkctl-migrate-legacy-single.md` (~177 chars)
- `prompts/sprint17/staging/staff/07-openshift-typed-client-k-get.md` (~179 chars)
- `prompts/sprint17/staging/staff/03-per-az-jumphost-reconcile-option-b.md` (~185 chars)
- `prompts/sprint17/staging/staff/06-ops-install-uninstall-snapshot.md` (~188 chars)
- `prompts/sprint17/staging/staff/02-bnk-phase-override-source-label.md` (~217 chars)
- `prompts/sprint17/staging/validator/05-orchestration-lifecycle-coverage-hole.md` (~234 chars)

**Description**: the Sprint 17 README says titles must be one-line
≤80 chars; the bug/feat prefix counts toward the limit. Every
title above blows past 80. GitHub's issue-list view truncates
at ~70 chars, so the long titles render as `bug: <leading-fragment>…`
in `gh issue list`; the rationale ("falls through to the raw
basename, breaking the human-friendly labelling sibling…") moves
into the body where it belongs.

**Suggested fix**: integrator trims each title to ≤80 chars before
`gh issue create --title "…"`. The frontmatter `title:` is
informational for the integrator's grep but the actual GH issue
title is the `--title` argument. For example:

- staff/02 → `bug: snapshot section header for bnk-phase-override.tfvars uses raw basename`
- staff/03 → `feat: per-AZ jumphost stale-target reconcile (PRD 09 option (b))`
- staff/05 → `feat: roksbnkctl migrate — convert ShapeLegacySingle to two-phase split`
- staff/06 → `feat: ops install/uninstall snapshot (closes PRD 07 open question #1)`
- staff/07 → `feat: OpenShift typed-client for roksbnkctl k get (PRD 02 Phase 2.1)`
- validator/05 → `bug: internal/orchestration 12.1% coverage — lifecycle.go + cluster.go untested`

Body text stays as drafted; this is a one-line `--title` edit per
issue, no body edit needed.

---

## Finding 2 — near-duplicate: validator/03 + validator/04 both touch the pre-tag release ritual

**Severity**: low
**Status**: accepted (each draft is distinct; coordinate via the
"Notes" sections they already carry)
**Files**:
- `prompts/sprint17/staging/validator/03-pretag-live-verify-runid-gate.md`
- `prompts/sprint17/staging/validator/04-orphan-check-billing-guard.md`

**Description**: two validator drafts both land scripts the
integrator runs from the pre-tag release ritual. validator/03
adds `scripts/release-precheck.sh` (CHANGELOG run-id gate);
validator/04 adds `scripts/orphan-check.sh` (IBM Cloud
residue scan) and explicitly references calling it from
release-precheck as an optional step. The cross-link is
intentional and well-framed; no code overlap. The integrator
can land them as two independent issues and they compose at
filing time.

**Suggested fix**: file both; ensure the integrator records
the resulting GH issue numbers as cross-links between the
two issues when filing (`gh issue create` returns a URL;
mention "tracks alongside issue #N" in the comments rather
than retro-edit the bodies).

---

## Finding 3 — near-duplicate: architect/02 + architect/04 + architect/06 all CI-tighten release.yml / book.yml

**Severity**: low
**Status**: accepted (each is a distinct surface — book anchors vs
goreleaser snapshot vs release re-run preflight)
**Files**:
- `prompts/sprint17/staging/architect/02-ci-intra-md-anchor-check.md`
- `prompts/sprint17/staging/architect/04-goreleaser-snapshot-presmoke.md`
- `prompts/sprint17/staging/architect/06-release-rerelease-opaque-failure.md`

**Description**: three CI-tightening drafts in one role. Each
already calls out its sibling in the "Notes" section and frames
the orthogonality crisply — 02 is content-side anchor gate, 04
is pre-tag template gate, 06 is post-tag re-run preflight. No
file overlap (02 touches book.yml + tools/book; 04 touches
ci.yml; 06 touches release.yml).

**Suggested fix**: none — keep as three issues. If the integrator
wants to land them in one PR cycle, file the three issues and
schedule them together; but as backlog drafts they read
independently.

---

## Finding 4 — broken self-reference: validator/03 mentions a `06-pretag-no-piling-checklist.md` companion that doesn't exist

**Severity**: medium
**Status**: open
**File**: `prompts/sprint17/staging/validator/03-pretag-live-verify-runid-gate.md` (Notes section, last paragraph)

**Description**: the Notes section reads "This issue's companion
is `06-pretag-no-piling-checklist.md` (the
`no-piling-into-active-release` mechanical backstop)" but the
staged validator drafts are `01..06` and `06` is
`06-e2e-drivers-a5-a6-parity.md`. The companion never landed.
The reader who follows the path either to the staging dir or
to a `roksbnkctl/issues` filed name will draw a blank.

**Suggested fix**: integrator edits the Notes paragraph before
filing — either (a) drop the companion sentence entirely, or
(b) replace with "the no-piling-into-active-release discipline
is tracked in the integrator's memory note and may earn its
own issue in a later sprint." One-line edit; body stays the
same otherwise.

---

## Finding 5 — bug-template "Files likely touched" is optional, but 5 bug drafts could carry it usefully

**Severity**: low
**Status**: accepted (template-compliant as-drafted; not blocking)
**Files** (bug drafts without the optional section):
- `prompts/sprint17/staging/architect/01-book-broken-anchor-crossrefs.md`
- `prompts/sprint17/staging/architect/03-spellcheck-soft-gate.md`
- `prompts/sprint17/staging/architect/05-release-footer-pdf-race.md`
- `prompts/sprint17/staging/validator/01-ci-test-not-hermetic-home-kubeconfig.md`
- `prompts/sprint17/staging/validator/06-e2e-drivers-a5-a6-parity.md`

**Description**: `.github/ISSUE_TEMPLATE/bug_report.md` does NOT
include `## Files likely touched` (it's a feature-request-only
section). The 5 bug drafts above omit it correctly per template;
but each has acceptance criteria that name specific file paths,
so the section would compress nicely from the body. Not a
template-compliance defect — a stylistic note.

**Suggested fix**: none required. Listed here for the
integrator's optional pass; if they prefer to add a "Files
likely touched" trailing section to each bug for grep
convenience, it doesn't break the template. Skip otherwise.

---

## Finding 6 — acceptance-criteria-quality: a few "Stretch goal — not blocking" criteria muddy the gate

**Severity**: low
**Status**: open
**Files**:
- `prompts/sprint17/staging/staff/01-doctor-target-backend-prefix.md`
  (criterion 4 — golden snapshot — is fine but framing is OK)
- `prompts/sprint17/staging/validator/05-orchestration-lifecycle-coverage-hole.md`
  (criterion 4 — "≥35% after landing — measurably more than
  today's 12.1%. (Stretch: ≥50%.)")

**Description**: criteria with explicit "Stretch:" / "not
blocking" hedges are good faith but blur the integrator's
yes/no gate at close-time. Either the gate is required (drop
the hedge) or it's optional (move to Notes).

**Suggested fix**: integrator strips the parenthetical stretch
notes during the close pass on the issue. Doesn't block
filing; mention in the close-out comment when each lands.

---

## Finding 7 — drift: architect/05 (release-footer PDF race) "fix space is open" leaves 3 alternative paths in the acceptance criteria

**Severity**: low
**Status**: accepted (the draft openly names this — `(a)/(b)/(c)`
— as integrator's PR-review pick)
**File**: `prompts/sprint17/staging/architect/05-release-footer-pdf-race.md`

**Description**: criteria 2 and 3 are gated on "if the fix is
route (b)…" / "if the fix is route (c)…". This is unusual
shape — normally an issue picks the fix and the acceptance
criteria pin it. Here the bug is "the footer lies" and the
route depends on whether the integrator wants to keep the
manual `make release-publish` step.

**Suggested fix**: none — the draft is explicit that the route
choice belongs to the PR review, not the issue body. Reader-
friendly; acceptable shape. Listed here so the integrator
knows the divergence from convention is intentional.

---

## Finding 8 — drift: integrator-inventory claim "≥3 numbered acceptance criteria" — every staged draft has ≥3, but some are dense

**Severity**: low
**Status**: accepted
**Files** (densest acceptance lists):
- `prompts/sprint17/staging/staff/03-per-az-jumphost-reconcile-option-b.md` (8 criteria)
- `prompts/sprint17/staging/staff/05-roksbnkctl-migrate-legacy-single.md` (9 criteria)
- `prompts/sprint17/staging/validator/03-pretag-live-verify-runid-gate.md` (7 criteria)

**Description**: the template says "Aim for 4-10 items" for
features. The dense ones are at the upper end of that range
but stay actionable. Listed here only so the integrator can
challenge any single criterion that's actually two criteria
hiding in one bullet when reviewing at file-time.

**Suggested fix**: none required. If a criterion compresses two
gates, split during the PR cycle when the issue is picked up,
not at filing time.

---

## Finding 9 — drift: validator/02 (boundary-grep CI gate) cross-refs four doc-comments by line number; line numbers may drift before fix lands

**Severity**: medium
**Status**: open
**File**: `prompts/sprint17/staging/validator/02-ci-orchestration-cli-boundary-grep.md`

**Description**: criterion 5 names "doc-comment claims in
`internal/orchestration/{lifecycle,cluster,chokepoint,
second_phase_reuse}.go` at lines 57, 47, 12, 70". Line numbers
in source files drift on every refactor between Sprint 17 and
the PR pick-up. A future implementer reading "line 12 of
chokepoint.go" may find an unrelated comment.

**Suggested fix**: integrator edits criterion 5 (and any
similar prose in the body) to drop literal line numbers,
naming the doc-comment by content phrase instead: "the
doc-comment claiming `this package never imports
internal/cli` in each of the four files." One-line edit per
mention. Body text otherwise stable.

---

## Verification (per the prompt's "Verify before reporting done")

- `grep -l '<!--' prompts/sprint17/staging/tech-writer/*.md` — empty
  (verified before reporting).
- Each finding above names a specific draft file path the
  integrator can grep against.
- Filename slugs in `prompts/sprint17/staging/tech-writer/`
  are kebab-case, ≤6 words each.

## Verdict for the final report

**GREEN with one-line nits.** Findings 1 (long titles), 4 (broken
companion ref), 9 (line-numbered doc-comment refs) are
one-line edits the integrator can apply at `gh issue create`
time; none block the filing. Findings 2, 3, 5, 6, 7, 8 are
accepted-as-drafted notes.
