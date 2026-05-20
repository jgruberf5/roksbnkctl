# Sprint 18 — architect issues (cos bucket get + post-v1.6.2 work cycle)

> **Sprint 18 frame.** First regular work sprint post-`v1.6.2`.
> Architect owns the mermaid PDF text-missing bug (the former
> GitHub issue #2, filed in-tree on `prompts/sprint18` kickoff and
> deleted from GitHub). Diagnostic-then-fix: identify which of the
> three ranked hypotheses is the real cause before changing pipeline.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — bug: mermaid diagrams in the PDF book render shapes/arrows but no text (e.g. page 120)

**Severity**: high
**Status**: open

## Symptom

In the **PDF** rendering of the book (the
`roksbnkctl-book-vX.Y.Z.pdf` attached to each GitHub Release —
e.g. the v1.6.2 PDF at 937 900 bytes, rendered 2026-05-19 01:09 UTC),
mermaid diagrams show **shapes and arrows but the label text inside
nodes / on edges is missing**. Example: **page 120**.

The HTML book at <https://jgruberf5.github.io/roksbnkctl/book/> is
believed unaffected (browser-side `mermaid.min.js` renders normally) —
this looks PDF-pipeline-specific.

## Chapters affected

`grep -l '\`\`\`mermaid' book/src/` finds three chapters with mermaid
fenced blocks:

- `book/src/07-quick-start.md`
- `book/src/17-execution-backends.md`
- `book/src/21-dns-testing-gslb.md`

Page 120 falls inside the back half of the book, so the example is
most likely a chart in **17** or **21**; reproducer should confirm.

## Suspect pipeline

`book/book.toml` shows the **PDF backend uses a Lua filter**
(`/opt/render-mermaid.lua`) to pre-render each mermaid code block to
SVG via mermaid-cli inside the bundled image
`ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev` — pandoc + XeLaTeX
then embeds the SVGs. The HTML backend keeps the mermaid block intact
and lets `mermaid.min.js` render it client-side, which is why it
works there.

Most likely failure modes, in priority order:

1. **Fonts unavailable inside the SVG-to-PDF step.** mermaid-cli's
   default SVG references browser fonts (Trebuchet MS, Helvetica,
   sans-serif). When XeLaTeX or pandoc's SVG-conversion path lacks
   those (the image bundles DejaVu, not the mermaid defaults), the
   `<text>` glyphs drop to empty.
2. **SVG `<text>` is not being rasterised at all** — pandoc may be
   passing the SVG to `svg2pdf` / `rsvg-convert` / `inkscape` that
   doesn't ship text rendering in the image; only the geometry survives.
3. **mermaid-cli is producing PNG instead of SVG** (or vice versa) and
   the resulting raster has whitespace where text should be — less
   likely, but worth ruling out by inspecting one rendered chart's
   intermediate artifact.

## Reproduce / diagnostics

```
# Build the PDF locally with the same image the CI uses:
make book-pdf BOOK_BACKEND=docker

# Open book/book/pandoc/pdf/book.pdf, jump to page 120, confirm the
# "shapes-but-no-text" symptom.

# Inspect the intermediate render of a mermaid block — the Lua filter
# at /opt/render-mermaid.lua in the docker image writes to a temp dir
# during pandoc's run. Re-running with --trace and keeping the temp
# dir will surface the SVG/PDF mermaid-cli emitted; eyeball it
# directly to see whether <text> elements are present in the source
# (rule out failure mode 1 vs 2 vs 3).
```

## Acceptance criteria

1. PDF rendered by `make book-pdf BOOK_BACKEND=docker` includes the
   in-node / on-edge text for every mermaid block across the three
   affected chapters.
2. A round-trip render of one chapter's mermaid block (build → open
   PDF → visually verify text) is added to whatever smoke check the
   book pipeline runs at release time, so a regression in the docker
   image's font set or SVG-conversion stack fails the build instead
   of silently shipping a broken PDF.
3. HTML book at `/book/` continues to render mermaid correctly (no
   regression).

## Out of scope

- Restyling the diagrams themselves — keep the existing mermaid
  source as-is; the fix lives in the PDF pipeline (image font set /
  SVG conversion / Lua filter), not in `book/src/*.md`.

## Notes

- The image is rebuilt by `.github/workflows/tools-images.yml` on
  every tag push; the most recent build is the `dev` tag from
  `2026-05-20T05:08:54Z` (v1.6.2 push). Whatever fix lands here will
  need a new image tag and a follow-up `make release-publish
  VERSION=v1.6.2` to overwrite the PDF asset on the existing v1.6.2
  Release. The currently-published PDF has the bug.
