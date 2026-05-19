#!/usr/bin/env bash
# Render A_Project_Managers_Guide_to_Agentic_Developed_Products.md to PDF.
#
# Uses the same docker image (ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev)
# that the book pipeline uses — pandoc + XeLaTeX + DejaVu fonts +
# mermaid-cli all bundled. No host pandoc/LaTeX install required, only
# docker.
#
# Output: .archive/A_Project_Managers_Guide_to_Agentic_Developed_Products.pdf
# (alongside the archived source). Run from the repo root.
#
# Override the image with PMGUIDE_IMAGE=ghcr.io/.../mdbook:tag if needed.

set -euo pipefail

SRC=".archive/A_Project_Managers_Guide_to_Agentic_Developed_Products.md"
OUT=".archive/A_Project_Managers_Guide_to_Agentic_Developed_Products.pdf"
IMAGE="${PMGUIDE_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev}"

# ---------------------------------------------------------------------------
# Prereqs
# ---------------------------------------------------------------------------

if ! command -v docker >/dev/null 2>&1; then
    echo "ERROR: docker is not on PATH. Install docker, then re-run." >&2
    exit 1
fi

if [[ ! -f "$SRC" ]]; then
    echo "ERROR: source markdown not found: $SRC" >&2
    echo "       (run this script from the repo root)" >&2
    exit 1
fi

if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
    echo "==> Pulling $IMAGE (first run only)"
    docker pull "$IMAGE"
fi

# ---------------------------------------------------------------------------
# Temp files (auto-cleanup on exit)
# ---------------------------------------------------------------------------

TMPDIR=$(mktemp -d -t pmguide-pdf.XXXXXX)
trap 'rm -rf "$TMPDIR"' EXIT

HEADER="$TMPDIR/header.tex"
LUA="$TMPDIR/parts.lua"

# Unicode-glyph mappings — XeLaTeX with DejaVu fonts doesn't carry a few
# glyphs the guide uses (checkmark/cross/warning/math/hourglass). Map each
# to a LaTeX-renderable equivalent so missing-character warnings don't
# leave blanks in the PDF.
cat > "$HEADER" <<'EOF'
\usepackage{pifont}
\usepackage{newunicodechar}
\newunicodechar{✓}{\ding{52}}
\newunicodechar{✗}{\ding{56}}
\newunicodechar{⚠}{\textbf{!}}
\newunicodechar{≫}{\ensuremath{\gg}}
\newunicodechar{≪}{\ensuremath{\ll}}
\newunicodechar{⇒}{\ensuremath{\Rightarrow}}
\newunicodechar{↔}{\ensuremath{\leftrightarrow}}
\newunicodechar{≥}{\ensuremath{\geq}}
\newunicodechar{≤}{\ensuremath{\leq}}
\newunicodechar{≠}{\ensuremath{\neq}}
\newunicodechar{≈}{\ensuremath{\approx}}
\newunicodechar{⏳}{\textbf{[wait]}}
\newunicodechar{️}{}
EOF

# Lua filter — promote H1 headings that start with "Part <roman>" to
# LaTeX \part{} commands. Without this, the part dividers in the source
# (e.g. "# Part I — The Product Layer") render as empty \chapter pages
# between real chapters and shift the chapter numbering by one.
cat > "$LUA" <<'EOF'
local function latex_escape(s)
  s = s:gsub("\\", "\\textbackslash{}")
  s = s:gsub("&",  "\\&")
  s = s:gsub("%%", "\\%%")
  s = s:gsub("%$", "\\$")
  s = s:gsub("#",  "\\#")
  s = s:gsub("_",  "\\_")
  s = s:gsub("{",  "\\{")
  s = s:gsub("}",  "\\}")
  s = s:gsub("~",  "\\textasciitilde{}")
  s = s:gsub("%^", "\\textasciicircum{}")
  return s
end

function Header(elem)
  if elem.level == 1 then
    local title = pandoc.utils.stringify(elem.content)
    if title:match("^Part%s+[IVXLCDM]+") then
      return pandoc.RawBlock("latex", "\\part{" .. latex_escape(title) .. "}")
    end
  end
  return elem
end
EOF

# ---------------------------------------------------------------------------
# Render
# ---------------------------------------------------------------------------

echo "==> Rendering $SRC → $OUT via $IMAGE"

# Stage the temp files inside the bind-mounted workdir so pandoc inside
# the container can read them. They land under $TMPDIR; we expose them
# via a second bind mount.
docker run --rm \
    -v "$(pwd):/work" \
    -v "$TMPDIR:/cfg:ro" \
    -w /work \
    --entrypoint pandoc \
    "$IMAGE" \
    "$SRC" \
    -o "$OUT" \
    --pdf-engine=xelatex \
    --toc \
    --number-sections \
    --lua-filter=/cfg/parts.lua \
    --include-in-header=/cfg/header.tex \
    -V documentclass=report \
    -V classoption=openany \
    -V papersize=letter \
    -V geometry:margin=1in \
    -V mainfont="DejaVu Serif" \
    -V sansfont="DejaVu Sans" \
    -V monofont="DejaVu Sans Mono" \
    -V colorlinks=true \
    -V linkcolor=NavyBlue \
    -V urlcolor=NavyBlue \
    -V toccolor=NavyBlue

echo "==> Done: $(ls -la "$OUT" | awk '{print $5, $NF}')"
