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

## Closure (Sprint 18 architect, 2026-05-20)

`Status: resolved`

### Real cause (a refinement of hypothesis 2)

mermaid-cli v11 emits all in-node / on-edge labels via
`<foreignObject>` containers wrapping embedded XHTML, NOT via native
SVG `<text>` elements. The librsvg-2 rasteriser that pandoc / XeLaTeX
uses to embed SVGs in the PDF does **not** implement `<foreignObject>`
(a documented librsvg limitation). Result: shape-and-arrow geometry
survived the SVG-to-PDF conversion, but every label rendered as an
empty box. Hypothesis 1 (fonts) and hypothesis 3 (mermaid-cli output
format) were ruled out by direct inspection of the intermediate SVG —
the SVG always had the mermaid default font stack referenced
(`trebuchet ms,verdana,arial,sans-serif`) but never had any selectable
text content for librsvg to typeset; setting
`flowchart.htmlLabels: false` in a mermaid-cli config only converted
the cluster label and left node labels in foreignObject.

### Fix shape

Pivot the Lua filter from **SVG** to **PNG** output. mermaid-cli's
PNG path uses Puppeteer + Chromium directly, which natively renders
foreignObject + has the mermaid-default browser-font stack baked in.
The resulting PNG is then embedded as a raster image in the PDF.
Retina-grade scale (`-s 2`) keeps the bitmap crisp at body-text size
in the printed book; `-b white` matches the LaTeX page background.

### Files changed

| File | Change |
| --- | --- |
| `tools/docker/mdbook/render-mermaid.lua` | SVG → PNG output; cache dir renamed `/tmp/mermaid-svg` → `/tmp/mermaid-png`; mmdc invocation adds `-b white -s 2`; comment block updated to document the foreignObject / librsvg interaction. |
| `scripts/check-pdf-mermaid-labels.sh` | Rewritten from a `pdftotext` canary-grep (incompatible with raster-rendered diagrams) to a `pdfimages -list` count + DPI gate: PDF must embed at least one raster image per `book/src/**` mermaid fence, and each image must clear a 600 px width floor. The new shape correctly fails on the pre-fix SVG path (0 embedded images on the broken pipeline) and passes on the post-fix PNG path. |

`book/book.toml` is **unchanged** (no `image-tag` field; the
filter path is still `/opt/render-mermaid.lua`, which the rebuilt
image will continue to provide). The Lua filter is the only in-image
artifact that changed — the docker image must be rebuilt to pick up
the new file before the next book PDF can be cut from CI / from a
clean checkout.

`book/src/**` is **unchanged** (the three mermaid source blocks in
`07-quick-start.md`, `17-execution-backends.md`,
`21-dns-testing-gslb.md` were not restyled — per the Out-of-scope
clause).

### Smoke check the validator should run

`bash scripts/check-pdf-mermaid-labels.sh` after `make book-pdf
BOOK_BACKEND=docker` (the Makefile already wires it in as a follow-on
step at `book-pdf:` and `release:` time). Two gates:

1. **Count gate.** Embedded raster image count must be `>=` the count
   of `^\`\`\`mermaid$` fences under `book/src/**`. Falsifies the
   no-op-filter regression (mmdc missing, Chromium broken, puppeteer
   config wrong).
2. **DPI gate.** Each embedded image must be `>= 600 px` wide.
   Falsifies a degenerate `-s 1` / placeholder render.

Local verification done by the architect: pre-fix pipeline (without
the bind-mounted patched filter) FAILs on gate 1 (0 / 3); post-fix
pipeline PASSes both gates (3 / 3, all images 1568 px wide).

### Image rebuild required — new tag

Yes. The Lua filter is baked into
`ghcr.io/jgruberf5/roksbnkctl-tools-mdbook` at the in-container path
`/opt/render-mermaid.lua`. The integrator must rebuild and push a new
image after merging the filter change. Two options, integrator's
call:

- **Replace `:dev` in place.** `make -C tools/docker build-mdbook
  TAG=dev && docker push ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`.
  Zero churn to `Makefile` (`BOOK_IMAGE` already defaults to `:dev`).
  Acceptable because no CI consumer of `:dev` exists outside the
  release-cut box; `tools-images.yml` does not build the mdbook image
  (it only builds ibmcloud + iperf3 — see line 33 of the workflow).
- **Cut a versioned tag.** `make -C tools/docker build-mdbook
  TAG=v1.6.3 && docker push ...:v1.6.3 && docker push ...:latest`,
  then update `BOOK_IMAGE` in the repo-root Makefile from `:dev` to
  `:v1.6.3` in the same commit. Cleaner audit trail tied to the
  release that ships the fix; more lines touched.

Either way, the rebuild trigger is a manual `make -C tools/docker
build-mdbook` from the integrator's box — there is no GHA workflow
that does this on push (the existing `.github/workflows/tools-images.yml`
matrix does not include `mdbook`). If the team wants the mdbook image
to rebuild automatically on tag push, that is a separate sprint
deliverable (extend the workflow's matrix); not in scope here.

### Acceptance criteria pass list

- **AC #1 — PDF includes label text for every mermaid block across
  the three affected chapters.** PASS. Verified visually by extracting
  PDF pages with `pdftoppm` and viewing pages 41 (ch07 sequence
  diagram — User, roksbnkctl, terraform-exec, IBM Cloud API, ROKS
  cluster + BNK, every numbered step label), 121 (ch17 backends
  diagram — laptop / roksbnkctl binary, local backend, docker
  backend, k8s backend, ssh:target backend, ROKS cluster, SSH
  jumphost, IBM Cloud API), 171 (ch21 DNS / GSLB diagram — fan-out
  parallel / per-vantage probe, divergence detector + JSON aggregate,
  local vantage, k8s vantage, ssh:jumphost vantage, F5 BIG-IP Next
  GSLB, gslb_divergence: true/false, stdout JSON roksbnkctl.dns.v1).
  Page 121 is the printed-page-120 the issue spec called out as the
  reproducer.
- **AC #2 — round-trip smoke check fails the build on regression.**
  PASS. `scripts/check-pdf-mermaid-labels.sh` is the gate, wired into
  `make book-pdf` (and transitively `make release`). The check fails
  the broken pipeline (0 embedded raster images) and passes the fixed
  pipeline (3 / 3).
- **AC #3 — HTML book continues to render mermaid correctly.** PASS.
  The Lua filter is only referenced from `[output.pandoc.profile.pdf]`
  in `book.toml`; `[output.html]` uses `additional-js =
  ["mermaid.min.js", "mermaid-init.js"]` with no filter. Verified
  post-fix that the three HTML chapter files each contain a
  `class="mermaid"` block (client-side render target intact) and that
  `book/book/html/mermaid-*.min.js` ships unchanged.

### Did not commit

Per the prompt's "Do **not** commit" constraint. The two modified
files (`tools/docker/mdbook/render-mermaid.lua`,
`scripts/check-pdf-mermaid-labels.sh`) are staged-as-working-tree
edits ready for the integrator's single Sprint 18 commit.
