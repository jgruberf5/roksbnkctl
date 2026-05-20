# Sprint 18 — architect resolution log

## Issue 1 — mermaid diagrams in the PDF book render shapes/arrows but no text → **resolved; shipped in `v1.6.3`**

Root cause confirmed live via the docker image's intermediate render: mermaid-cli v11 emits node/edge labels via SVG `<foreignObject>` + embedded XHTML, and librsvg (pandoc's SVG-to-PDF rasteriser) does not implement `<foreignObject>`. Geometry survived; labels rendered as empty boxes. Rejected hypothesis (1) font availability — fonts referenced fine in the SVG; rejected hypothesis (3) output-format mismatch — mermaid-cli's configured output was SVG as expected; the real cause was a refinement of hypothesis (2) "SVG `<text>` not rasterised" — specifically that the labels are in `<foreignObject>` blocks, not native `<text>` elements.

**Fix.** Pivoted `tools/docker/mdbook/render-mermaid.lua` from SVG to PNG output. mermaid-cli's PNG path uses Puppeteer + Chromium directly — which renders `<foreignObject>` natively and has the mermaid browser-font stack baked in. Retina `-s 2`, `-b white` background, sized for readable embed at print resolution. HTML book pipeline (client-side `mermaid.min.js`) unaffected.

**Image rebuild.** Docker image `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev` rebuilt locally via `make -C tools/docker build-mdbook` (transient containerd lock race on first attempt; clean on retry — image layers cached). Push to ghcr.io deferred until the live verify passed; not required for v1.6.3 ship (the integrator's local image was used for the PDF build embedded in the Release).

**Live verify (2026-05-20)**: `make book-pdf BOOK_BACKEND=docker` + `scripts/check-pdf-mermaid-labels.sh` (validator Issue 2's smoke check) → **3 raster images embedded matching 3 mermaid blocks; every image ≥ 600 px wide; PASS**. Visual confirmation: chapter-7 (p41), chapter-17 (p121), chapter-21 (p171) all render full label text. HTML book at `book/book/html/` still renders mermaid normally (client-side path untouched).

**Files affected**: `tools/docker/mdbook/render-mermaid.lua` (the only code change; SVG→PNG pivot). Sibling docs (`book/book.toml`, `Makefile`, `tools/docker/mdbook/Dockerfile` × 3 spots) had stale SVG references — corrected in the tech-writer integration commit (`e357025`), not architect scope.

## Status

Issue 1: **resolved**. Live-`!`-verify gate satisfied per `live-verify-high-issues`.
