.PHONY: build test vet tidy run clean

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BIN     := bin/roksbnkctl
PKG     := github.com/jgruberf5/roksbnkctl

LDFLAGS := -s -w \
	-X $(PKG)/internal/cli.Version=$(VERSION) \
	-X $(PKG)/internal/cli.Commit=$(COMMIT) \
	-X $(PKG)/internal/cli.BuildDate=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/roksbnkctl

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

run: build
	$(BIN) --help

clean:
	rm -rf bin/

.PHONY: book book-pdf book-test book-serve book-clean release \
        stamp-changelog goreleaser-check goreleaser-snapshot pages-assure

# Release date stamped into CHANGELOG.md's `## v1.0.0 — 2026-MM-DD`
# placeholder. Defaults to today; override with RELEASE_DATE=YYYY-MM-DD
# for testing or back-dated releases.
RELEASE_DATE ?= $(shell date +%Y-%m-%d)

# Pinned goreleaser image (matches `goreleaser/goreleaser:latest` on
# Docker Hub). Override via GORELEASER_IMAGE=... if the integrator wants
# to pin a specific release.
GORELEASER_IMAGE ?= goreleaser/goreleaser:latest

# Book backend: `host` (default) uses mdbook + mdbook-mermaid from PATH;
# `docker` routes through the tools/docker/mdbook image, which also
# bundles pandoc + LaTeX + mermaid-cli for the PDF output path. The CI
# workflow at .github/workflows/book.yml installs mdbook + mdbook-mermaid
# directly on the runner for the HTML-only build (faster cold-start than
# pulling the 1.2 GB image).
BOOK_BACKEND ?= host
BOOK_IMAGE   ?= ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev

ifeq ($(BOOK_BACKEND),docker)
MDBOOK = docker run --rm -v $(CURDIR)/book:/book $(BOOK_IMAGE)
MDBOOK_SERVE = docker run --rm -p 3000:3000 -v $(CURDIR)/book:/book $(BOOK_IMAGE) serve -n 0.0.0.0
else
MDBOOK = mdbook
MDBOOK_SERVE = mdbook serve book/ --open
endif

book:
ifeq ($(BOOK_BACKEND),docker)
	$(MDBOOK) build
else
	$(MDBOOK) build book/
endif

# book-pdf: PDF-only build. Requires BOOK_BACKEND=docker since the
# host-install path doesn't include pandoc + LaTeX + mermaid-cli (and we
# don't want to ask contributors to install all that for the HTML
# iteration loop).
#
# The `[output.pandoc.profile.pdf]` block in book/book.toml defines the
# PDF profile; mdbook-pandoc walks the rendered markdown, the Lua filter
# at /opt/render-mermaid.lua pre-renders Mermaid blocks to SVG via mmdc,
# pandoc invokes XeLaTeX to produce the final PDF.
#
# Output lands at book/book/pandoc/pdf/book.pdf.
book-pdf:
ifeq ($(BOOK_BACKEND),docker)
	$(MDBOOK) build
	@echo ""
	@echo "PDF written to:  book/book/pandoc/pdf/book.pdf"
	@echo "HTML written to: book/book/html/index.html"
else
	@echo "make book-pdf requires BOOK_BACKEND=docker:" >&2
	@echo "  PDF generation needs pandoc + LaTeX + mermaid-cli, all of" >&2
	@echo "  which are bundled in the tools/docker/mdbook image." >&2
	@echo "  The host install path covers HTML only." >&2
	@echo "" >&2
	@echo "  Re-run as:" >&2
	@echo "    make book-pdf BOOK_BACKEND=docker" >&2
	@echo "" >&2
	@echo "  Or if the image hasn't been built yet:" >&2
	@echo "    make -C tools/docker build-mdbook" >&2
	@echo "    make book-pdf BOOK_BACKEND=docker" >&2
	@exit 2
endif

# stamp-changelog: replace the `2026-MM-DD` placeholder in CHANGELOG.md
# with $(RELEASE_DATE) (defaults to today). Idempotent — no-op if the
# placeholder is already gone (i.e., the CHANGELOG has been stamped or
# manually dated).
stamp-changelog:
	@if grep -q '2026-MM-DD' CHANGELOG.md; then \
	    sed -i "s/2026-MM-DD/$(RELEASE_DATE)/" CHANGELOG.md; \
	    echo "    CHANGELOG.md v1.0.0 date stamped: $(RELEASE_DATE)"; \
	else \
	    echo "    CHANGELOG.md v1.0.0 date already stamped (skip)"; \
	fi

# goreleaser-check: lint .goreleaser.yml via the official goreleaser
# docker image. Fast — pure YAML + schema validation.
goreleaser-check:
	docker run --rm -v $(CURDIR):/work -w /work $(GORELEASER_IMAGE) check

# goreleaser-snapshot: end-to-end dry-run of the release pipeline.
# Cross-compiles for all goos/goarch combinations defined in
# .goreleaser.yml, produces archives + checksums in dist/, validates the
# release.extra_files paths (incl. the PDF book artifact). Does NOT
# tag, push, or publish — that's the integrator's tag-cut step.
goreleaser-snapshot:
	docker run --rm \
	    -v $(CURDIR):/work \
	    -w /work \
	    $(GORELEASER_IMAGE) release --snapshot --clean

# pages-assure: confirm GitHub Pages is enabled for this repo (publishing
# from the gh-pages branch under /). Idempotent — checks first, only
# POSTs if missing. Requires `gh` on PATH and an authenticated session
# (gh auth status). The `{owner}/{repo}` placeholders in the gh api
# endpoint are auto-resolved from the current repo's remote.
pages-assure:
	@if gh api repos/{owner}/{repo}/pages >/dev/null 2>&1; then \
	    url=$$(gh api repos/{owner}/{repo}/pages --jq '.html_url'); \
	    echo "    GitHub Pages already enabled: $$url"; \
	else \
	    echo "    Enabling GitHub Pages (source: gh-pages branch / )"; \
	    gh api -X POST repos/{owner}/{repo}/pages \
	        -f 'source[branch]=gh-pages' \
	        -f 'source[path]=/' \
	        --silent; \
	    url=$$(gh api repos/{owner}/{repo}/pages --jq '.html_url'); \
	    echo "    GitHub Pages enabled: $$url"; \
	fi

# release: full release-prep driver. Run before `git tag v1.0` to verify
# every release artifact builds cleanly and every publish surface is
# wired. Steps:
#
#   1. Stamp today's date into CHANGELOG.md's v1.0.0 placeholder
#   2. Build HTML + PDF book via tools/docker/mdbook (HTML for Pages,
#      PDF for the GitHub Release page)
#   3. Lint .goreleaser.yml via docker
#   4. Cross-compile snapshot build via goreleaser docker (writes dist/)
#   5. Confirm GitHub Pages is enabled (publishing from gh-pages branch)
#
# After this completes successfully, the integrator's tag-cut sequence is:
#
#   git add -A && git commit -m "chore: prep v1.0.0 release"
#   git tag v1.0.0 && git push origin main --tags
#
# Pushing to main triggers .github/workflows/book.yml's build-deploy job
# (publishes HTML to gh-pages branch); pushing the tag triggers
# .github/workflows/release.yml (runs goreleaser for-real, attaches the
# PDF via release.extra_files, publishes the GitHub Release).
release:
	@echo "==> [1/5] Stamping CHANGELOG.md v1.0.0 date"
	@$(MAKE) stamp-changelog
	@echo ""
	@echo "==> [2/5] Building HTML + PDF book via $(BOOK_IMAGE)"
	@$(MAKE) book-pdf BOOK_BACKEND=docker
	@echo ""
	@echo "==> [3/5] Linting .goreleaser.yml via $(GORELEASER_IMAGE)"
	@$(MAKE) goreleaser-check
	@echo ""
	@echo "==> [4/5] Snapshot build (multi-platform binaries → dist/)"
	@$(MAKE) goreleaser-snapshot
	@echo ""
	@echo "==> [5/5] Verifying GitHub Pages is enabled"
	@$(MAKE) pages-assure
	@echo ""
	@echo "==> Release artifacts ready:"
	@ls -la book/book/html/index.html book/book/pandoc/pdf/book.pdf 2>/dev/null || true
	@echo ""
	@echo "    dist/:"
	@ls -la dist/checksums.txt dist/*.tar.gz dist/*.zip 2>/dev/null | head -20 || true
	@echo ""
	@echo "==> Next: review the diff, commit, tag, push:"
	@echo "    git add -A && git commit -m 'chore: prep v1.0.0 release'"
	@echo "    git tag v1.0.0 && git push origin main --tags"

book-test:
ifeq ($(BOOK_BACKEND),docker)
	@echo "make book-test does not support BOOK_BACKEND=docker:" >&2
	@echo "  mdbook test invokes rustdoc to validate Rust code fences." >&2
	@echo "  The release image drops the rust toolchain after the cargo" >&2
	@echo "  install of mdbook + mdbook-mermaid + mdbook-pandoc. CI runs" >&2
	@echo "  mdbook test separately with the full toolchain (see" >&2
	@echo "  .github/workflows/book.yml)." >&2
	@echo "  For local mdbook test, install mdbook on the host:" >&2
	@echo "    cargo install mdbook mdbook-mermaid" >&2
	@echo "  then re-run: make book-test" >&2
	@exit 2
else
	$(MDBOOK) test book/
endif

book-serve:
	$(MDBOOK_SERVE)

book-clean:
	rm -rf book/book

# --- Sprint 0 staff additions ---
# Note: `build` and `test` already exist above and are kept verbatim
# (their existing recipes are richer than the Sprint 0 spec — build wires
# ldflags for version stamping). See issues/issue_sprint0_staff.md for
# the rationale.

.PHONY: test-short test-integration test-live test-cred-audit lint pre-commit-install

test-short:
	go test -short ./...

# test-cred-audit runs the security-spine regression suite from
# `internal/exec/audit_test.go` (Sprint 3 / PRD 04 §"Acceptance criteria"
# item 5). Quick: < 5s on a clean tree. Run before tagging a release —
# a leaked credential in any backend is a stop-ship.
#
# Run -v to see exactly which audit cases fired:
#   make test-cred-audit ARGS="-v"
test-cred-audit:
	go test -run CredAudit $(ARGS) ./...

# test-integration runs the testcontainers-go-backed suites (currently
# only internal/remote/integration_test.go — adds an sshd container to
# exercise the SSH client). Requires Docker on the host. Not invoked by
# the default CI matrix on every PR — see .github/workflows/ci.yml's
# `integration` job, which gates this on Linux + same-repo PRs only.
# Run locally before pushing SSH-related changes.
test-integration:
	go test -tags integration -timeout 5m ./...

# test-live runs golden-file byte-equivalence tests for
# `roksbnkctl k get -o yaml` against `kubectl get -o yaml`. Requires:
#
#   - $KUBECONFIG (or ~/.kube/config) pointing at a real cluster
#   - kubectl on PATH for the comparison side
#   - roksbnkctl built and on PATH (or $ROKSBNKCTL set to its path)
#
# Tests skip cleanly (rather than fail) when prerequisites are missing,
# so it's safe to invoke from CI as a manual-trigger job. Recommended:
# run before tagging v0.8 — the byte-equivalence is part of PRD 02's
# acceptance criteria.
test-live:
	go test -tags live -timeout 5m ./internal/k8s/...

lint:
	gofmt -d -l . && go vet ./... && (command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not on PATH; skipping")

pre-commit-install:
	ln -sf ../../scripts/pre-commit.sh .git/hooks/pre-commit && echo "Pre-commit hook installed."
