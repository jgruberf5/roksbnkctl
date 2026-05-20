#!/usr/bin/env bash
# scripts/check-pdf-mermaid-labels.sh — Sprint 18 architect Issue 1 +
# validator Issue 2 (regression guard for the "shapes-but-no-text"
# mermaid bug in the PDF book).
#
# Background. mermaid-cli (v10+/v11) emits all in-node / on-edge labels
# via `<foreignObject>` + embedded XHTML, NOT native SVG `<text>`. The
# librsvg-2 rasteriser pandoc / XeLaTeX uses to embed SVGs in the PDF
# does not implement `<foreignObject>` — so an SVG-route PDF gets
# shape-and-arrow geometry with empty label boxes. The architect's
# Sprint 18 fix moves the Lua filter to render mermaid → PNG via
# Puppeteer + Chromium (which renders foreignObject natively + has the
# mermaid-default font stack baked in). The PNG is then embedded as
# a raster image in the PDF.
#
# What this script checks. Two complementary gates:
#
#   1. Count gate. The PDF must contain at least one embedded raster
#      image per mermaid fenced block under book/src/**. If the filter
#      silently falls back to the no-op CodeBlock return (mmdc missing,
#      Chromium broken, puppeteer config wrong), the diagram is dropped
#      and the image count drops below the expected floor — the build
#      fails here.
#
#   2. Image-DPI gate. Each embedded mermaid PNG must be wider than a
#      conservative pixel floor (>= 600 px). A retina-grade `-s 2` mmdc
#      render of even the smallest diagram in the three affected
#      chapters exceeds this; a regressed SVG-fallback or a busted
#      mermaid-cli that emits a degenerate placeholder PNG falls under
#      it. Prose figures unrelated to mermaid (zero in the book today)
#      would also be admitted by this gate — that's fine; the count
#      gate covers the "no diagrams" case.
#
# Why NOT pdftotext canary-grep. PNG-rasterised labels are not
# selectable text in the PDF — pdftotext can't see them. An OCR-driven
# check would catch the labels but pulls in tesseract + the labels are
# also identifiable indirectly via the image-count + DPI floor. The
# smaller hammer wins.
#
# Usage:
#   ./scripts/check-pdf-mermaid-labels.sh
#   make book-pdf BOOK_BACKEND=docker   # wired in via the Makefile
#
# Knobs:
#   PDF_PATH       default book/book/pandoc/pdf/book.pdf
#   PDFIMAGES      default pdfimages (poppler-utils; bundled in the
#                  mdbook docker image)
#   BOOK_SRC_DIR   default book/src
#   MIN_DIAGRAM_PX default 600 (pixel-width floor per diagram image)
#
# Exit codes: 0 = pass. 2 = a gate failed (do NOT ship the PDF).

set -e
set -u
set -o pipefail

PDF_PATH=${PDF_PATH:-book/book/pandoc/pdf/book.pdf}
PDFIMAGES=${PDFIMAGES:-pdfimages}
BOOK_SRC_DIR=${BOOK_SRC_DIR:-book/src}
MIN_DIAGRAM_PX=${MIN_DIAGRAM_PX:-600}

red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }

if [[ ! -f "$PDF_PATH" ]]; then
    red "FAIL  PDF not found at $PDF_PATH"
    red "      Run \`make book-pdf BOOK_BACKEND=docker\` first."
    exit 2
fi

if ! command -v "$PDFIMAGES" >/dev/null 2>&1; then
    # Don't fail the build for missing tooling on a stripped-down dev
    # host — but DO warn loudly so it's not mistaken for a silent pass.
    # The release-cut path runs inside the docker image which bundles
    # pdfimages.
    yellow "WARN  pdfimages not on PATH — skipping mermaid-PDF regression check."
    yellow "      Install poppler-utils to enable this gate locally."
    yellow "      (skipped, not failed; the release-cut docker image bundles pdfimages.)"
    exit 0
fi

# Count mermaid fenced blocks under the book source tree. A diagram is
# one fenced code block starting with ```mermaid.
if ! command -v grep >/dev/null 2>&1; then
    red "FAIL  grep not on PATH (unexpected)."
    exit 2
fi

expected=$(grep -rE '^```mermaid$' "$BOOK_SRC_DIR" --include='*.md' | wc -l | tr -d ' ')
if [[ "$expected" -eq 0 ]]; then
    green "OK    no mermaid blocks in $BOOK_SRC_DIR (skip)"
    exit 0
fi

# pdfimages -list emits a header + one row per embedded image. We want
# the rows; skip the two header lines, then count + read the `width`
# column (column 4 in the fixed-width layout).
mapfile -t image_rows < <("$PDFIMAGES" -list "$PDF_PATH" | tail -n +3)

actual=${#image_rows[@]}

if [[ "$actual" -lt "$expected" ]]; then
    red "FAIL  PDF embeds $actual raster image(s) but $expected mermaid block(s) exist."
    red "      The render-mermaid Lua filter likely fell back to the no-op"
    red "      branch (mmdc missing / Chromium broken / puppeteer config wrong)"
    red "      — diagrams have been dropped from the PDF."
    red "      Architect Sprint 18 Issue 1 regression. Do NOT ship this PDF."
    exit 2
fi

green "OK    $actual raster image(s) embedded, >= $expected mermaid block(s) expected"

# Image-DPI gate. Each row's column 4 is the image width in pixels. The
# `pdfimages -list` table is column-aligned — `awk '{print $4}'` is
# robust to the leading space in the page column.
under=0
i=0
for row in "${image_rows[@]}"; do
    width=$(echo "$row" | awk '{print $4}')
    if ! [[ "$width" =~ ^[0-9]+$ ]]; then
        # Skip rows we can't parse (defensive — pdfimages output is
        # stable but a future poppler bump could rearrange columns).
        continue
    fi
    i=$((i + 1))
    if [[ "$width" -lt "$MIN_DIAGRAM_PX" ]]; then
        red "      image #$i width=$width px is below the $MIN_DIAGRAM_PX px floor"
        under=$((under + 1))
    fi
done

if [[ "$under" -ne 0 ]]; then
    red "FAIL  $under embedded image(s) fell below the $MIN_DIAGRAM_PX px width floor."
    red "      A retina-grade mmdc render (-s 2) of any diagram in the three"
    red "      affected chapters exceeds this floor; a sub-floor image"
    red "      suggests a degenerate render (or a non-mermaid figure was"
    red "      added — in which case bump MIN_DIAGRAM_PX intentionally)."
    exit 2
fi

green "OK    every embedded image >= $MIN_DIAGRAM_PX px wide (retina-grade render holds)"
green ""
green "PASS  mermaid-PDF regression guard — architect Sprint 18 Issue 1 fix holds."
exit 0
