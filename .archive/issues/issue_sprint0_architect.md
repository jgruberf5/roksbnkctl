# Sprint 0 — architect issues

## Issue 1: mdbook not installed in sandbox; local build not verified
**Severity**: low
**Status**: open
**Description**: `which mdbook` returned not-found in the sandbox shell. Therefore `mdbook build book/` was not executed locally to verify the book renders. All structural checks were performed instead: every link in `book/src/SUMMARY.md` resolves to an existing chapter file (32 chapters + preface), `book.toml` is well-formed TOML, `book.yml` parses as YAML and contains no tab characters. The first push that touches `book/**` will exercise the GitHub Actions workflow which installs `mdbook` via `peaceiris/actions-mdbook@v2` and will surface any rendering issues there.
**Files affected**: `book/`, `.github/workflows/book.yml`
**Proposed fix**: Either install `mdbook` locally (`cargo install mdbook` or download release) and run `mdbook build book/`, or rely on the first CI run on `main` to validate the build. No code changes required.

## Issue 2: gh-pages branch not yet provisioned
**Severity**: low
**Status**: open
**Description**: The `book.yml` workflow uses `peaceiris/actions-gh-pages@v4` which will create the `gh-pages` branch on first run, but the GitHub repository must have **Pages** enabled and the source set to "Deploy from a branch → gh-pages → /" for the published book URL (`https://jgruberf5.github.io/roksbnkctl/book/`) referenced in the README to resolve. This is a one-time repo-settings step that cannot be done from a PR.
**Files affected**: `README.md` (link target), `.github/workflows/book.yml`
**Proposed fix**: After the first successful run of `book.yml` on `main`, the maintainer must enable GitHub Pages in repo Settings → Pages, source = `gh-pages` branch, folder = `/`. Until then the README link will 404.

## Issue 3: README link path may need adjustment after Pages enables
**Severity**: low
**Status**: open
**Description**: The README links to `https://jgruberf5.github.io/roksbnkctl/book/`. Because mdBook is built into `./book/book` and `peaceiris/actions-gh-pages@v4` publishes that directory to the **root** of `gh-pages`, the actual served path will be `https://jgruberf5.github.io/roksbnkctl/` (no trailing `/book/` segment). The trailing `/book/` in the README link will likely be wrong and resolve to a 404 once Pages is live.
**Files affected**: `README.md`
**Proposed fix**: This is a deliberate choice in the spec — the user-provided README line was specified verbatim. Once Pages is live and the actual URL is observed, either (a) update README to drop `/book/`, or (b) change `publish_dir` in `book.yml` to a wrapper directory so the served path includes `/book/`. Flagging here so the integrator does not silently inherit a broken link.
