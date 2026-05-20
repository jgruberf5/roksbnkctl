# Sprint 17 — architect issues (backlog grooming via GitHub issue drafts)

> **Sprint 17 frame.** Backlog-grooming sprint, post-`v1.6.2`. Each
> role surveys its area and drafts GitHub-issue markdown files into
> `prompts/sprint17/staging/<role>/`. Cap: **≤6 drafts**.
> Quality > volume.

`Status: open | in-progress | resolved | wontfix | accepted`.

_Seeded at kickoff — the architect agent appends its Closure section
below when reporting done._

## Closure

- Surveyed:
  - `book/src/**` — grep for `out of scope` / `future work` / `TODO` / `will be covered` / `not covered here`; intra-MD `[..](./*.md#anchor)` cross-references against per-chapter heading-slug sets; `book/src/SUMMARY.md` diffed against on-disk chapter files; mermaid code-block inventory (3 chapters carry blocks: 07/17/21).
  - `.github/workflows/**` — `ci.yml`, `release.yml`, `book.yml`, `tools-images.yml`, `spellcheck.yml`, `e2e-full.yml`. Cross-checked against `.goreleaser.yml` `release.{header,footer}` + `extra_files` history, and `Makefile` `release-publish` target.
  - Infra-y deferred surface — `CHANGELOG.md` `### Deferred (v1.x roadmap, post-v1.6.0)` block + `docs/PLAN.md` §"What's deliberately deferred to post-v1.0" Book/Code subsections.
- Candidates considered: 10 (7 book-side: 7 dead intra-MD anchors as one bug + 6 versioned-deferral mentions; 5 CI/release: snapshot smoke, snapshot-anchor gate, spellcheck soft-gate, rerelease opacity, footer/PDF race; 2 stale-tag / orphan-target items already in prior ledgers; 1 SUMMARY.md gap that turned out clean on full diff).
- Dedupes against existing backlog/ledger: 4 — (a) GitHub Issue #2 (mermaid PDF) overlaps any "mermaid rendering" candidate, skipped — Issue #2 is the canonical mermaid issue; (b) per-AZ jumphost stale-target reconcile is in the CHANGELOG v1.5.0 + v1.6.0 `### Deferred` list, skipped; (c) `ops install` / `ops uninstall` snapshot (PRD 07 §"Open questions" item 1) is deferred forward from v1.4.x, skipped; (d) `internal/cli` decomposition phases 2+ is explicitly tracked in CHANGELOG v1.6.0 `### Deferred`, skipped (and was anyway out of architect scope — Code subsection).
- Drafted to staging/architect/: 6.
  - `01-book-broken-anchor-crossrefs.md` — bug, seven dead intra-MD anchors (slug double-dash class).
  - `02-ci-intra-md-anchor-check.md` — feat, `book.yml` gate to prevent class regression.
  - `03-spellcheck-soft-gate.md` — bug, `spellcheck.yml` `continue-on-error: true` makes the check decorative.
  - `04-goreleaser-snapshot-presmoke.md` — feat, `ci.yml` gains a `goreleaser release --snapshot` job; catches the v1.0.1-recovery-cut class.
  - `05-release-footer-pdf-race.md` — bug, `.goreleaser.yml` footer asserts a PDF asset that `release.yml` does not attach (manual `make release-publish` is the asymmetric companion).
  - `06-release-rerelease-opaque-failure.md` — bug, `release.yml` `workflow_dispatch` re-run on an existing tag fails with a raw goreleaser error; no preflight, no actionable recovery hint.
- Notable choices: Drafts 01 + 02 are deliberately a bug/feat pair — the seven dead anchors land as a content-only PR (small, reviewable) and the CI gate to prevent the next one lands as a YAML+script PR (small, reviewable); coupling them into one issue would have produced a noisy multi-concern diff. Drafts 04 + 05 + 06 form a release-pipeline-hardening triad: 04 prevents `.goreleaser.yml` defects pre-tag, 05 fixes the lying-footer-vs-asset asymmetry, 06 makes the documented re-run fallback actually navigable when something does break — together they close the discoverability and correctness gaps the Sprint 17 README's release-pipeline scope item names. The seven-dead-anchor count is the live `grep | python` enumeration, not a guess; the probe is reproducible from the issue's reproduction recipe.

Did not commit. Did not call `gh issue create`.
