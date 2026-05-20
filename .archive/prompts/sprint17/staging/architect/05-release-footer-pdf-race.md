---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: GitHub Release footer advertises `roksbnkctl-book-vX.Y.Z.pdf` as attached, but the PDF lands later (or never) via a separate manual step'
labels: []
assignees: ''
---

## Symptom

`.goreleaser.yml` `release.footer` (lines 86–95) renders this sentence on **every** GitHub Release page at goreleaser-publish time:

> The attached `roksbnkctl-book-{{ .Tag }}.pdf` is the same book published at <https://jgruberf5.github.io/roksbnkctl/book/>, packaged for offline reading. Build it locally any time via `make release` from the source tree.

But `release.yml` does not attach the PDF — the comment block at `.goreleaser.yml:97–110` says so explicitly:

> The PDF book artifact is NOT attached by goreleaser. The CI runner for release.yml doesn't have pandoc/XeLaTeX/mermaid-cli installed (deliberately ...). Instead, after this workflow finishes, the integrator runs: `make release-publish VERSION=vX.Y.Z` from their machine, which uploads ./book/book/pandoc/pdf/book.pdf to this Release as roksbnkctl-book-vX.Y.Z.pdf via gh release upload.

For the gap between (a) `release.yml` finishing and (b) the integrator running `make release-publish`, the Release page reads "the attached PDF is …" while no such asset exists. If the integrator forgets `make release-publish` (or runs it from a tree where `book/book/pandoc/pdf/book.pdf` doesn't exist — the Makefile guards `[ ! -f book.pdf ] && echo missing >&2`, but the workflow's success has already published the misleading footer), the PDF is never attached and the Release page ships in a permanently lying state.

A spot-check on the publicly visible Releases catalog can quickly confirm whether any past tag is in the "footer claims PDF, no PDF attached" state — if so this is not theoretical; if not, the race window has been short enough that nobody's been bitten yet, but the lying-footer-on-Release-page race is real on every tag cut.

## Reproduction

```
# 1. cut a fresh tag, push it (mirrors a real release cycle)
git tag v9.9.9-pdftest
git push origin v9.9.9-pdftest

# 2. wait for release.yml to finish (the goreleaser job)
gh run watch --exit-status

# 3. fetch the Release body and the asset list at the moment the workflow goes green
gh release view v9.9.9-pdftest --json body,assets --jq '{body: .body, assets: [.assets[].name]}'

# expected post-fix:
#   body:    no "the attached roksbnkctl-book-v9.9.9-pdftest.pdf" sentence — OR it's there
#            and so is roksbnkctl-book-v9.9.9-pdftest.pdf in assets.
# actual pre-fix:
#   body:    "...The attached roksbnkctl-book-v9.9.9-pdftest.pdf is the same book ..."
#   assets:  ["checksums.txt", "roksbnkctl_9.9.9-pdftest_linux_amd64.tar.gz", ...]
#                       — NO roksbnkctl-book-v9.9.9-pdftest.pdf entry.

# 4. the Release page is now public, lying about an asset that does not exist.
#    Clean up (yes, this leaves a real Release object behind — pick a clearly-test tag):
gh release delete v9.9.9-pdftest -y
git push --delete origin v9.9.9-pdftest
```

## Expected behavior

Either (a) the footer text only renders when the PDF is actually attached (e.g. moved out of the static footer and into a goreleaser hook that runs after the integrator's `make release-publish`), or (b) the footer text changes to honestly describe the two-step process ("The book PDF is uploaded separately by the maintainer shortly after this Release publishes — check back in a few minutes, or read the HTML at <link>"), or (c) `release.yml` is extended to build the PDF on the runner (via the existing `tools/docker/mdbook/Dockerfile` image pulled from GHCR) so the PDF is attached at the same moment as the binaries. The choice between (a) / (b) / (c) is for the issue's PR review — the bug is the lying footer, the fix space is open. The choice the integrator picks will inform how Issue ND-CHANGELOG-PDF-LINK / installation-chapter-PDF-link (already filed in the book) read after the fix.

## Actual behavior

The footer text always renders verbatim at goreleaser-publish time; the PDF asset is uploaded later (manually) or not at all. The GitHub Release page presents a contradiction the reader has no way to resolve.

## Environment

- `roksbnkctl version`: any tag cut from `release.yml` since the v1.0.x PDF-attached `extra_files` was removed in the v1.0.1 recovery cut.
- OS / arch: (n/a — the defect is on the GitHub Release page, not the binary).
- IBM Cloud region: (n/a)
- Backend: (n/a)
- Affected files: `.goreleaser.yml` (footer), `.github/workflows/release.yml`, `Makefile` (`release-publish` target — the asymmetric companion).

## Suspect pipeline / hypotheses (optional)

1. **Most likely:** the v1.0.1 recovery cut removed `extra_files` (the goreleaser-time PDF attachment) but did NOT remove the footer sentence that referenced the attached PDF. The two changes need to be coupled; only one was made. This is a documented-in-comments oversight, not a design choice — the inline comment block at lines 108–110 calls it out (`extra_files` was removed, see `make release-publish`), but the footer that depended on `extra_files` was left.
2. Less likely: the maintainer is comfortable with the gap because the typical workflow is "tag → workflow runs (~3 min) → integrator runs `make release-publish` (~30s) — total window of misinformation is ~30 seconds and acceptable". If so, criterion 1 still holds (the lying-footer state must be unreachable, not "rare"), and the fix is (b) above (honest footer text), not nothing.

## Acceptance criteria

1. Cutting a fresh tag through `release.yml` produces a Release page where the `release.footer` body and the asset list are consistent — either both reference the PDF and the PDF is attached, or neither references the PDF. The reproduction in step 3 of the recipe above can verify this with `gh release view --json body,assets` and a check that the substring `roksbnkctl-book-` either appears in both `body` and `assets[*].name` or in neither.
2. If the fix is route (b) (honest footer text), the footer explicitly names the manual-publish step or the expected delay window, so a reader who arrives 30s after the tag-push isn't confused.
3. If the fix is route (c) (CI builds the PDF), `release.yml` either pulls the existing `ghcr.io/.../mdbook` tools image or runs the `make book-pdf` target inside it; the Release page has the PDF asset at the same moment the binaries land; `make release-publish` either becomes a no-op or is removed entirely (Makefile cleanup in the same PR).
4. `make release-publish` and `.goreleaser.yml`'s footer no longer have orthogonal-implicit ordering. Whichever sequence the chosen fix prescribes is in one place (a comment in `.goreleaser.yml` pointing at the make target, or the make target removed entirely, or both reconciled into a single workflow step).
5. Past Releases (audit-only, not in-scope to backfix): the integrator runs `gh release list --limit 20` + a one-liner that inspects each Release's body+assets for the substring `roksbnkctl-book-` and confirms no past Release is currently in the lying-footer state — or, if any are, files a one-off cleanup issue. This criterion is a check, not a deliverable; if past releases turn out to be consistent the audit is a five-minute one-off.
6. Regression check: a follow-up PR that tries to re-introduce the gap (e.g. adds a `release.footer` line referencing a not-yet-attached asset) gets caught by either (a) a `Makefile` target / lint that asserts the footer's referenced assets are produced by goreleaser itself, or (b) explicit reviewer-attention via a comment in `.goreleaser.yml` warning future editors. (a) is preferred; (b) is the minimum.

## Out of scope (deliberately)

- Reworking the entire PDF build pipeline (mermaid-cli, pandoc, XeLaTeX layout). The defect is the footer/asset asymmetry; the toolchain is fine.
- Signing the PDF (cosign attestation). Same rationale as the goreleaser-snapshot issue: signing is post-v1.0 deferred.
- Auto-uploading the PDF on a schedule (e.g. nightly to a `:dev`-tagged Release). Different concern; file separately if there's demand.
- Backfilling past Releases with a PDF if any are in the no-PDF state. Audit only (criterion 5); backfill is a one-off the integrator can do or skip per-tag without changing any code.
- Touching the existing book HTML publish flow (`make release-publish`'s `gh-pages` push). The HTML side is correct; only the PDF side has the asymmetry.

## Notes

- This is a textbook example of the Sprint 17 README's "release-pipeline correctness" scope item — the release happens, no log is red, but the Release page is in an internally inconsistent state. The lying-footer race is exactly the "is the failure mode discoverable from the workflow run logs?" question, answered "no": the workflow logs say success, the Release page contradicts itself.
- Cross-link: this issue assumes the new `goreleaser-snapshot` CI gate (separate issue) is in place — that gate catches the *template* defects (`{{ .NonExistent }}` substitutions); this issue catches the *content* defect (a syntactically-valid template that lies). Both are needed.
- If route (c) is chosen, the v1.0.1 recovery-cut comment in `.goreleaser.yml` ("the multi-GB tools/docker/mdbook image on every tag-push is wasteful") needs to be re-examined: tag-push is rare (~once per minor cycle), so the "wasteful" framing is doing a lot of work for a small win and should be re-litigated on this issue's PR.
