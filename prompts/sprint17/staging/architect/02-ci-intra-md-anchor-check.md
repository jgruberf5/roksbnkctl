---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: book.yml — gate intra-chapter anchor / cross-ref validity in CI'
labels: []
assignees: ''
---

## Motivation

`mdbook build` validates that a `[...](./other.md)` link points at an existing file, but it does **not** validate that a `[...](./other.md#anchor)` link points at an existing *heading anchor* on that file. The seven dead `#anchor` links uncovered in the companion bug issue (the `--`/`--` slug-collapse class) all rendered cleanly through `book.yml`'s `mdbook build book/` step and shipped to readers as silent dead links — the failure mode is invisible to reviewers, invisible to CI, and only catches a reader who clicks the link in the published HTML.

The book is the project's primary user-facing surface; a CI gate that fails the build on the first dead intra-MD anchor would catch this defect class at PR time instead of post-merge. The same gate is also the right place to ensure no future "Chapter NN" reference points at a chapter that was renamed or renumbered — the second-order class the slug-collapse defect hints at.

## Proposed surface

No new `roksbnkctl` verb — this is a CI gate, not a binary feature. The surface is a new step in `.github/workflows/book.yml` (the existing book-validation workflow) plus an optional `make book-anchor-check` target so contributors can run it locally with the same wiring.

```
# CI step (illustrative):
make book-anchor-check    # exits 0 on clean tree, non-zero on first dead anchor
```

- The check is **mandatory** (no `continue-on-error: true` — that pattern's risks are filed separately as the spellcheck-soft-gate bug).
- The check runs on every PR and push to `main` that touches `book/**`, matching `book.yml`'s existing `paths:` filter.
- Implementation: a short Python script under `tools/book/check-anchors.py` (or inline in the workflow YAML if it stays <30 lines), using only the stdlib — no new toolchain dependency on the runner.
- Slugifier: GitHub-style (lowercase, strip non-`\w\s-`, replace runs of whitespace with one `-`, collapse runs of `-`, strip leading/trailing `-`). Match mdBook's behavior — the script must agree with what the rendered HTML actually produces, so it's worth landing a 5-case fixture test next to the script (e.g. headings with em-dashes, leading punctuation, numbers, accents) that asserts the slugifier output.

## Behavior

- **Happy path:** clean tree → step exits 0, no stdout other than a one-line "N anchors checked, 0 broken" summary.
- **One dead anchor:** step exits non-zero; stderr lists every broken ref as `book/src/<source>.md:<line>: -> <target>.md#<anchor> (anchor not found)`. Multiple broken refs all surface in one run — don't fail on the first; surface all of them so a contributor with five typos sees five lines, not five PR cycles.
- **Missing target file:** treated as broken (covers the case where the file-existence check itself fails — defense in depth on top of mdBook's check).
- **External `https://` / mailto:** ignored (different defect class).
- **Anchor on the *same* file (`[...](#anchor)`):** also checked — same slug rules.
- **`make book-anchor-check` on contributor laptop:** runs the same Python script with no setup beyond `python3` (which contributors already have for the existing book toolchain). Output identical to CI.
- **Interaction with existing global flags:** none — this is build-time tooling, not the `roksbnkctl` binary.
- **Side-effects:** none on the filesystem. The script reads `book/src/*.md`, prints to stderr, and exits.

## Acceptance criteria

1. `.github/workflows/book.yml` gains a `Validate intra-MD anchors` step (post `mdbook build`) that runs the new checker; a deliberately-broken anchor (e.g. `[x](./01-what-is-bnk.md#does-not-exist)` introduced on a throwaway branch) fails the workflow with the exact filename:line:href triple in the log.
2. On a clean tree (companion bug fix applied) the step exits 0 and prints `0 broken` with a non-zero `N anchors checked` count.
3. `make book-anchor-check` runs the same script with the same exit semantics; documented in `book/README.md` or `CONTRIBUTING.md` (whichever holds the existing book toolchain doc).
4. The slugifier has a fixture test (`tools/book/check-anchors_test.py` or equivalent) asserting at least these five inputs map to the expected slugs: a simple ASCII heading, a heading with an em-dash (`—`), a heading with a leading flag like `--mode east-west`, a heading with `_underscore_` characters, and a heading with a parenthetical aside.
5. The step's `paths:` filter on the workflow matches `book.yml`'s existing `book/**` filter — a docs-only PR that doesn't touch the book runs no new minutes.
6. Regression: a follow-up PR that re-introduces one of the seven slug-collapse anchors fails this step at PR review, not at reader-click time.

## Out of scope (deliberately)

- Validating external `https://` URLs (markdown-link-check territory; separate concern, separate failure-rate envelope).
- Auto-fixing dead anchors. The script reports; the contributor fixes by hand. Auto-fix would couple a CI gate to a heuristic that gets one of the seven wrong.
- Replacing `mdbook build`'s own link check. This is additive — `mdbook build` still runs first and still catches missing target files.
- Validating chapter numbers in prose ("see Chapter 17"). That's a different drift class — file later if it bites.
- Spellcheck gating (`spellcheck.yml`'s `continue-on-error: true`). Filed separately so the two CI-tightening changes don't dogpile into one PR.

## Files likely touched

- `.github/workflows/book.yml` — add `Validate intra-MD anchors` step after the existing `Build HTML` step; reuse the existing checkout + python (no setup-python needed on `ubuntu-latest` since `python3` is pre-installed).
- `tools/book/check-anchors.py` (new) — the checker. ~50 lines stdlib-only.
- `tools/book/check-anchors_test.py` (new) — slugifier fixture test (criterion 4).
- `Makefile` — add `book-anchor-check` target (~5 lines), wire into `.PHONY`.
- `book/README.md` or `CONTRIBUTING.md` — one paragraph documenting the local-run path.

## Notes

- The companion bug issue (seven dead anchors today) should land first and stay in-scope; this feature lands after, so the gate goes green on its first run instead of red. If they land in either order the integrator can revert and re-land; the gate is a small change.
- mdBook's slugifier source is in the `mdbook` crate (`src/utils/mod.rs::normalize_id`) — keep the Python implementation aligned with the version of mdBook `book.yml` pins (currently `'latest'`); the fixture test in criterion 4 will catch a drift if the upstream rules ever change.
- This issue is deliberately the *CI gate*, not the seven content fixes. Splitting them keeps the PR diffs reviewable: content PR is markdown-only, CI PR is YAML + a small script.
