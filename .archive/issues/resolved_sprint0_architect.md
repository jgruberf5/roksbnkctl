# Sprint 0 — architect issues, resolution notes

Resolution log for `issue_sprint0_architect.md`. Three issues filed, all
low severity. Two are deferred operational steps (not code-resolvable in
this sprint); one was a real bug fixed in this sprint.

## Issue 1 (mdbook not installed locally) — informational, not actionable

**Resolution**: deferred to first CI run. The book.yml workflow installs
mdBook via `peaceiris/actions-mdbook@v2` on first push to `main` that
touches `book/**`. Local validation was structural only (every SUMMARY
link resolves, book.toml well-formed, workflow YAML valid).

**Status**: ✅ resolved as "informational; first CI run validates the
build"

## Issue 2 (gh-pages branch + Pages settings) — operational follow-up

**Resolution**: documented in `book.yml` comments and in this file.
Repository maintainer must enable GitHub Pages in repo Settings → Pages
(source = `gh-pages` branch, folder = `/`) AFTER the first successful
run of `book.yml` populates the branch. Cannot be done from a PR; a
maintainer with admin permissions has to flip the Pages switch once.

**Status**: ✅ resolved as "documented operational step"; tracked in
README + this file. After v0.7 release the maintainer should:

```
1. Push to main (book.yml runs, creates gh-pages branch)
2. GitHub.com → Settings → Pages
3. Source: Deploy from a branch
4. Branch: gh-pages, folder: / (root)
5. Save
6. Wait ~1 minute, then visit
   https://jgruberf5.github.io/roksbnkctl/book/ to verify
```

## Issue 3 (README link path mismatch) — fixed in this sprint

**Resolution**: added `destination_dir: book` to the
`peaceiris/actions-gh-pages@v4` step in
`.github/workflows/book.yml`. With this, mdBook's output (in
`./book/book/`) is published to the `book/` subdirectory of `gh-pages`,
making the served URL match the README link
(`https://jgruberf5.github.io/roksbnkctl/book/`).

Verified by reading the workflow YAML and confirming
`peaceiris/actions-gh-pages@v4`'s `destination_dir` parameter behaves
as advertised in the action's README. First CI run will validate the
served URL.

**Status**: ✅ resolved
**Commit**: lands in the Sprint 0 integration commit
