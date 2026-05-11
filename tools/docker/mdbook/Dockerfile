# syntax=docker/dockerfile:1
# roksbnkctl-tools-mdbook — release-time build tool for the book.
#
# This is a heavyweight (~1.2 GB) image deliberately. It bundles:
#
#   - mdbook + mdbook-mermaid             (HTML book build with rendered diagrams)
#   - mdbook-pandoc + pandoc + texlive    (PDF book build via XeLaTeX)
#   - @mermaid-js/mermaid-cli + Chromium  (pre-renders Mermaid blocks to SVG
#                                          so the PDF has real diagrams rather
#                                          than literal `sequenceDiagram ...`
#                                          code-block fallback text)
#
# The image runs **once per release** (via `make release` from the repo
# root) to produce both `book/book/index.html` (for GitHub Pages) and
# `book/book/pandoc/pdf/book.pdf` (for the GitHub Release page).
#
# Size is not a hot-path concern because the image is a build-time tool,
# not a runtime dependency. CI's lightweight HTML-only build at
# .github/workflows/book.yml still uses the `peaceiris/actions-mdbook`
# action + `cargo install mdbook-mermaid` (faster cold-start on the
# runner than pulling a 1.2 GB image).
#
# Build via tools/docker/Makefile (from the repo root):
#
#	make -C tools/docker build-mdbook
#
# Use via the root Makefile (from the repo root):
#
#	make book BOOK_BACKEND=docker        # HTML only
#	make book-pdf BOOK_BACKEND=docker    # PDF only
#	make release BOOK_BACKEND=docker     # both HTML + PDF
#	make book-serve BOOK_BACKEND=docker  # local preview at localhost:3000
#
# `mdbook test` is NOT supported by this image: mdbook test invokes
# rustdoc to validate Rust code fences, which needs the full Rust
# toolchain. CI runs `mdbook test` separately on the host. For local
# `mdbook test`, install mdbook on the host (`cargo install mdbook
# mdbook-mermaid`) and use `make book-test` (without BOOK_BACKEND=docker).

# ---------------------------------------------------------------------
# Stage 1: build the Rust-based binaries (mdbook, mdbook-mermaid, mdbook-pandoc)
# ---------------------------------------------------------------------
FROM rust:slim-bookworm AS builder

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc pkg-config libssl-dev \
    && rm -rf /var/lib/apt/lists/*

# --root /out keeps binaries in a clean tree the runtime stage can copy
# from without dragging in cargo's registry/cache.
RUN cargo install --root /out --locked mdbook mdbook-mermaid mdbook-pandoc

# ---------------------------------------------------------------------
# Stage 2: runtime — pandoc + LaTeX + mermaid-cli + the Rust binaries
# ---------------------------------------------------------------------
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8

# Core: tini for signal handling, pandoc for markdown → PDF conversion,
# texlive-xetex + recommended/extra LaTeX packages for the XeLaTeX engine
# pandoc uses to render the PDF. We also include latex-recommended fonts
# (Latin Modern via lmodern) and the `framed` / `fancyvrb` / `upquote` /
# `xcolor` LaTeX packages pandoc needs for code blocks and syntax
# highlighting.
#
# nodejs + chromium are for @mermaid-js/mermaid-cli's puppeteer-based
# rendering of Mermaid diagrams to SVG. We pin to system Chromium and
# tell puppeteer not to download its own (the bundled Chromium ships
# ~280 MB; the system package is ~200 MB and reusable for other
# headless-browser work).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        tini \
        pandoc \
        texlive-xetex \
        texlive-fonts-recommended \
        texlive-fonts-extra \
        texlive-latex-recommended \
        texlive-latex-extra \
        lmodern \
        librsvg2-bin \
        nodejs \
        npm \
        chromium \
        fonts-noto \
        fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/*

# mermaid-cli (mmdc): renders Mermaid code blocks to SVG/PNG. Configured
# below via puppeteer-config.json to use the system Chromium with
# --no-sandbox (required in unprivileged Docker containers).
RUN npm install -g --no-fund --no-audit @mermaid-js/mermaid-cli@latest \
    && npm cache clean --force

ENV PUPPETEER_SKIP_CHROMIUM_DOWNLOAD=true \
    PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium

COPY puppeteer-config.json /opt/puppeteer-config.json
ENV MERMAID_PUPPETEER_CONFIG=/opt/puppeteer-config.json

# Rust binaries from the builder stage.
COPY --from=builder /out/bin/mdbook /usr/local/bin/mdbook
COPY --from=builder /out/bin/mdbook-mermaid /usr/local/bin/mdbook-mermaid
COPY --from=builder /out/bin/mdbook-pandoc /usr/local/bin/mdbook-pandoc

# Pandoc Lua filter that pre-renders Mermaid code blocks to SVG via
# mmdc during the PDF build. Configured via book.toml's
# [output.pandoc.profile.pdf]::lua-filter setting (which references the
# in-container path `/opt/render-mermaid.lua`).
COPY render-mermaid.lua /opt/render-mermaid.lua

WORKDIR /book

ENTRYPOINT ["/usr/bin/tini", "--", "mdbook"]
CMD ["build"]
