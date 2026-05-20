You are the **architect** agent for Sprint 18 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in order)

1. `prompts/sprint18/README.md` — integrator decisions; your scope =
   the mermaid PDF text-missing rendering bug.
2. `issues/issue_sprint18_architect.md` Issue 1 — the **authoritative
   spec** for the bug (symptom, three ranked hypotheses, reproduction
   recipe, acceptance criteria). Binding.
3. `book/book.toml` — the PDF backend's `filters = ["/opt/render-mermaid.lua"]`
   line is the suspect pipeline; the Lua filter lives inside the
   docker image `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`.
4. `.github/workflows/tools-images.yml` — how that image is built and
   tagged on tag-pushes (the fix may require an image rebuild and a
   re-tag).
5. The three book chapters carrying mermaid: `book/src/07-quick-start.md`,
   `book/src/17-execution-backends.md`, `book/src/21-dns-testing-gslb.md`.
   Page 120 of `book/book/pandoc/pdf/book.pdf` is most likely a chart
   from 17 or 21 — the reproducer named in the issue spec confirms.

## Approach

The issue spec ranks three hypotheses (font availability inside the
SVG-to-PDF step → SVG `<text>` not rasterized → mermaid-cli output
format mismatch). **Do not skip the diagnostic step**: build the PDF
locally with `make book-pdf BOOK_BACKEND=docker`, open page 120,
and inspect the intermediate mermaid-cli SVG that the Lua filter
produces before deciding which hypothesis is the real one. The fix
shape depends on which it is:

- Hypothesis 1 (fonts) → image-side change: add the mermaid-default
  fonts (Trebuchet MS / Helvetica fallback set) to the image, or
  configure mermaid-cli to use DejaVu (already in the image).
- Hypothesis 2 (SVG `<text>` lost in conversion) → Lua filter +
  conversion-tool change: ensure SVGs reach pandoc with text intact;
  consider switching to PNG raster via mermaid-cli `-b transparent`
  if the SVG path is fundamentally broken.
- Hypothesis 3 (output format) → mermaid-cli flag change in the Lua
  filter.

Add a smoke check that fails the build instead of silently shipping a
broken PDF — the issue's acceptance criterion #2 names this; choose
the smallest viable form (a one-shot pre-published-PDF grep for an
expected text label inside a known mermaid block, or an extracted-page
text-presence check via pdftotext).

If the fix requires a new image tag, document the tag + the rebuild
trigger in your closure so the integrator can update `book.toml` and
trigger `tools-images.yml` as part of the integration commit.

## Constraints

- **Do not edit any pre-existing `_test.go` file** (sprint discipline).
- HTML book pipeline must keep working (client-side `mermaid.min.js`
  is unaffected; do not regress it).
- The three mermaid blocks in `book/src/**` should **not** be
  restyled or simplified to work around the bug — fix the rendering
  pipeline.
- Do **not** commit. The integrator commits. Do not push.

## Verify before reporting done

- `make book-pdf BOOK_BACKEND=docker` (or whatever your sandbox
  permits — if denied, record the exact denied command + cite the
  pipeline change you proposed and the test the validator must run).
- Page 120 of the produced PDF visibly contains the in-node /
  on-edge text for the mermaid block there (eyeball + optional
  `pdftotext` grep for a known label).
- HTML book at `book/book/html/` still renders mermaid (the
  client-side path is untouched).

## Issue file

Append a **Closure** section to `issues/issue_sprint18_architect.md`
documenting which hypothesis was the real cause, the fix shape,
files changed (including any `book.toml` image-tag update), the
smoke-check the validator should run, and the acceptance-criteria-
by-name pass list.

## Final report

≤200 words: hypothesis identified, fix shape, files touched, smoke
check proposed, whether an image rebuild is required (and if so the
new tag), acceptance-criteria pass list. State explicitly you did
not commit.
