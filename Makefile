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

.PHONY: book book-pdf book-test book-serve book-clean release release-publish \
        book-publish stamp-changelog goreleaser-check goreleaser-snapshot \
        pages-assure staticcheck build-integration-tags integration-test

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
# workflow at .github/workflows/book.yml validates only (HTML build +
# rust doctest) — it never publishes. Publishing is local-driven via
# `make release` + `make release-publish` so the multi-GB pandoc /
# XeLaTeX / mermaid-cli toolchain stays off the runner.
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

# staticcheck: run honnef.co/go/tools/cmd/staticcheck against the whole
# module. Sprint 9 / PLAN.md §"Sprint 9" code deliverable 5: this is the
# pre-tag gate step the v1.1.0 → v1.1.1 → v1.1.2 cascade exposed as
# missing — staticcheck was running in CI but not as a local pre-tag
# requirement. Auto-installs the binary into $(GOBIN)/staticcheck if
# it's not on PATH; idempotent on re-runs.
staticcheck:
	@if ! command -v staticcheck >/dev/null 2>&1 && [ ! -x "$$(go env GOPATH)/bin/staticcheck" ]; then \
	    echo "    installing honnef.co/go/tools/cmd/staticcheck@latest"; \
	    go install honnef.co/go/tools/cmd/staticcheck@latest; \
	fi
	@if command -v staticcheck >/dev/null 2>&1; then \
	    staticcheck ./...; \
	else \
	    "$$(go env GOPATH)/bin/staticcheck" ./...; \
	fi

# build-integration-tags: compile-check the whole tree under the
# `integration` build tag without executing any tests. Sprint 9 /
# PLAN.md §"Sprint 9" code deliverable 5: closes the
# v1.1.0 → v1.1.1 → v1.1.2 gap where `internal/exec/*_integration_test.go`
# files compiled fine on `go test ./...` (which skips integration-tagged
# files) but broke under `go test -tags integration ./...`. Running the
# build alone is faster than the full integration test sweep and catches
# the same shape of compile-time gap (unused imports, undefined symbols
# behind the tag, drift between the production code and the
# tag-gated test code).
build-integration-tags:
	go build -tags integration ./...

# integration-test: actually-run the `-tags integration` test sweep
# against an ephemeral kind cluster. Sprint 10 / PLAN.md §"Sprint 10"
# code deliverable 3: closes the v1.2.0 → v1.2.1 cascade gap where the
# Sprint 9 local gate ran `go build -tags integration ./...` (compile
# check, caught by build-integration-tags above) but not the actual
# `go test -tags integration` execution — which surfaces image-level
# issues (e.g., the v1.2.1 USER 1000 / $HOME breakage on the tools
# ibmcloud Dockerfile) that compile-check can't see.
#
# Delegates to scripts/integration-test.sh, which provisions a kind
# cluster named `roksbnkctl-it`, runs `go test -tags integration` for
# `internal/exec/...` (k8s + docker tests) and `internal/remote/...`
# (sshd-via-testcontainers), then tears down the cluster on exit.
# Preflight checks for kind + docker availability; bails with a clear
# diagnostic if either is missing.
#
# Env knobs (forwarded to the script):
#
#   KIND_CLUSTER_NAME (default: roksbnkctl-it)
#   KEEP_KIND=1       — leave the cluster up after the run (debug)
#   SKIP_REMOTE=1     — skip internal/remote tests
#   SKIP_K8S=1        — skip internal/exec k8s tests (docker tests still run)
#
# Invoked from `make release` (Sprint 10 gate step) when kind is on
# PATH; standalone invocation is the iterative-debug entrypoint.
integration-test:
	@bash scripts/integration-test.sh

# release: full release-prep driver. Run before `git tag vX.Y.Z` to verify
# every release artifact builds cleanly and every publish surface is
# wired. Steps:
#
#   1. Stamp today's date into CHANGELOG.md's vX.Y.Z placeholder
#   2. Run staticcheck ./... (Sprint 9 pre-tag gate)
#   3. Compile-check under -tags integration (Sprint 9 pre-tag gate)
#   4. Run -tags integration tests against ephemeral kind (Sprint 10
#      pre-tag gate) — if kind+docker are reachable. If kind is missing,
#      surfaces a strong warning + confirmation prompt rather than
#      blocking (option (a) per PLAN.md §"Sprint 10 → Code deliverable 3":
#      a contributor without kind can still cut a tag; they just get the
#      warning that the integration-test execution gate didn't run).
#   5. Build HTML + PDF book via tools/docker/mdbook (HTML for Pages,
#      PDF for the GitHub Release page)
#   6. Lint .goreleaser.yml via docker
#   7. Cross-compile snapshot build via goreleaser docker (writes dist/)
#   8. Confirm GitHub Pages is enabled (publishing from gh-pages branch)
#
# Steps 2 + 3 are Sprint 9 additions per PLAN.md §"Sprint 9" code
# deliverable 5 — they catch the shape of gap that produced the v1.1.0 →
# v1.1.1 → v1.1.2 cascade (staticcheck-clean fail in CI between tags,
# and -tags integration compile-fail in CI between tags).
#
# Step 4 is the Sprint 10 addition per PLAN.md §"Sprint 10" code
# deliverable 3 — closes the v1.2.0 → v1.2.1 cascade gap where the
# Sprint 9 gate ran `-tags integration` build only, missing the
# image-level USER 1000 / $HOME issue surfaced when `go test -tags
# integration` actually ran the ibmcloud `--version` probe inside a
# pod. Why option (a) (warn-not-block on missing kind): the local gate
# stays usable by contributors who don't have kind installed (e.g.
# CI-only integration), while the warning prevents the v1.2.x cascade
# from re-occurring silently — every contributor sees the gap before
# they tag. Override `SKIP_INTEGRATION_TEST=1` to bypass even when kind
# is available (useful for hotfix-cycle iteration where the change
# clearly doesn't touch backend code).
#
# Running them locally before the tag commit means the integrator finds
# the breakage before goreleaser publishes the binaries, not after.
#
# After this completes successfully, the integrator's tag-cut sequence is:
#
#   git add -A && git commit -m "chore: prep vX.Y.Z release"
#   git tag vX.Y.Z && git push origin main --tags
#
# Pushing the tag triggers .github/workflows/release.yml (goreleaser
# builds the multi-platform binaries and publishes the GitHub Release).
# Once that workflow completes, attach the book artifacts locally:
#
#   make release-publish VERSION=vX.Y.Z
#
# That single step pushes the locally-built HTML to the gh-pages branch
# AND uploads book.pdf to the GitHub Release as roksbnkctl-book-vX.Y.Z.pdf.
# No CI image pulls, no pandoc/LaTeX on the runner.

# SKIP_INTEGRATION_TEST=1 lets a contributor bypass step 4 explicitly
# (e.g., on a doc-only change where they're confident no backend code
# moved). Default is unset — step 4 runs.
SKIP_INTEGRATION_TEST ?=

release:
	@echo "==> [1/8] Stamping CHANGELOG.md release-date placeholder (one-time, was for v1.0.0)"
	@$(MAKE) stamp-changelog
	@echo ""
	@echo "==> [2/8] Running staticcheck ./... (Sprint 9 pre-tag gate)"
	@$(MAKE) staticcheck
	@echo ""
	@echo "==> [3/8] Compile-checking under -tags integration (Sprint 9 pre-tag gate)"
	@$(MAKE) build-integration-tags
	@echo ""
	@echo "==> [4/8] Running integration tests against kind (Sprint 10 pre-tag gate)"
	@if [ -n "$(SKIP_INTEGRATION_TEST)" ]; then \
	    echo "    SKIP_INTEGRATION_TEST=$(SKIP_INTEGRATION_TEST) — skipped explicitly (contributor opt-out)"; \
	elif ! command -v kind >/dev/null 2>&1; then \
	    echo "    ⚠️  kind not on PATH — integration-test execution gate cannot run."; \
	    echo "    The Sprint 10 gate (PLAN.md §\"Sprint 10 → Code deliverable 3\") wants this"; \
	    echo "    step to run before tag-cut. The v1.2.0 → v1.2.1 cascade traced to skipping it."; \
	    echo ""; \
	    echo "    Install kind via:"; \
	    echo "        go install sigs.k8s.io/kind@latest"; \
	    echo "    or download a binary from https://kind.sigs.k8s.io/."; \
	    echo ""; \
	    printf "    Proceed without running integration tests? [y/N]: "; \
	    read -r ans; \
	    case "$$ans" in \
	        y|Y|yes|YES) echo "    proceeding without integration-test execution (warning logged)" ;; \
	        *)           echo "    aborting — install kind and re-run, or set SKIP_INTEGRATION_TEST=1 to bypass"; exit 2 ;; \
	    esac; \
	elif ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then \
	    echo "    ⚠️  docker daemon not reachable — kind needs docker to host nodes."; \
	    echo "    Start docker and re-run, or set SKIP_INTEGRATION_TEST=1 to bypass."; \
	    exit 2; \
	else \
	    $(MAKE) integration-test; \
	fi
	@echo ""
	@echo "==> [5/8] Building HTML + PDF book via $(BOOK_IMAGE)"
	@$(MAKE) book-pdf BOOK_BACKEND=docker
	@echo ""
	@echo "==> [6/8] Linting .goreleaser.yml via $(GORELEASER_IMAGE)"
	@$(MAKE) goreleaser-check
	@echo ""
	@echo "==> [7/8] Snapshot build (multi-platform binaries → dist/)"
	@$(MAKE) goreleaser-snapshot
	@echo ""
	@echo "==> [8/8] Verifying GitHub Pages is enabled"
	@$(MAKE) pages-assure
	@echo ""
	@echo "==> Release artifacts ready:"
	@ls -la book/book/html/index.html book/book/pandoc/pdf/book.pdf 2>/dev/null || true
	@echo ""
	@echo "    dist/:"
	@ls -la dist/checksums.txt dist/*.tar.gz dist/*.zip 2>/dev/null | head -20 || true
	@echo ""
	@echo "==> Next: review the diff, commit, tag, push:"
	@if [ "$(VERSION)" = "dev" ]; then \
	    echo "    (re-run with VERSION=vX.Y.Z to get tag-cut commands tailored to a real release)"; \
	    echo "    git add -A && git commit -m 'chore: prep vX.Y.Z release'"; \
	    echo "    git tag vX.Y.Z && git push origin main --tags"; \
	    echo ""; \
	    echo "    Once .github/workflows/release.yml has published the Release:"; \
	    echo "    make release-publish VERSION=vX.Y.Z"; \
	else \
	    echo "    git add -A && git commit -m 'chore: prep $(VERSION) release'"; \
	    echo "    git tag $(VERSION) && git push origin main --tags"; \
	    echo ""; \
	    echo "    Once .github/workflows/release.yml has published the Release:"; \
	    echo "    make release-publish VERSION=$(VERSION)"; \
	fi

# book-publish: push the locally-built book/book/html/ tree to the
# gh-pages branch under /book/. Replaces what .github/workflows/book.yml
# used to do via peaceiris/actions-gh-pages — but with no runner, no
# image pull, just a git worktree + push from the integrator's machine.
#
# Preserves anything already on gh-pages outside the /book/ subdirectory
# (.nojekyll, CNAME, etc.) — only the /book/ subtree is replaced.
#
# Prereqs:
#   - book/book/html/ exists (run `make book` or `make book-pdf` first)
#   - origin remote points at the publish target
#   - git push access to gh-pages
book-publish:
	@if [ ! -d book/book/html ]; then \
	    echo "book/book/html missing — run 'make book' or 'make book-pdf BOOK_BACKEND=docker' first" >&2; \
	    exit 2; \
	fi
	@echo "==> Fetching origin/gh-pages"
	@git fetch origin gh-pages
	@tmp=$$(mktemp -d -t roksbnkctl-gh-pages.XXXXXX) && \
	    trap "git worktree remove --force $$tmp >/dev/null 2>&1 || true" EXIT && \
	    git worktree add --detach $$tmp origin/gh-pages && \
	    rm -rf $$tmp/book && \
	    mkdir -p $$tmp/book && \
	    cp -r book/book/html/. $$tmp/book/ && \
	    cd $$tmp && \
	    git add -A book/ && \
	    if git diff --cached --quiet; then \
	        echo "    gh-pages /book/ already up to date — nothing to push"; \
	    else \
	        git -c user.name="$$(git -C $(CURDIR) config user.name)" \
	            -c user.email="$$(git -C $(CURDIR) config user.email)" \
	            commit -m "book: publish $$(git -C $(CURDIR) describe --tags --always)" && \
	        git push origin HEAD:gh-pages && \
	        echo "    Pushed to gh-pages — https://jgruberf5.github.io/roksbnkctl/book/"; \
	    fi

# release-publish: post-tag publish step. Run after .github/workflows/release.yml
# has finished creating the GitHub Release for $(VERSION). Does the work
# we deliberately keep off CI:
#
#   1. Push the locally-built HTML book to the gh-pages branch
#   2. Upload the locally-built PDF book to the GitHub Release as
#      roksbnkctl-book-$(VERSION).pdf
#
# Requires VERSION to match the tag you cut (e.g. VERSION=v1.0.0). Will
# refuse to run with VERSION=dev to prevent accidental publishes.
#
# Prereqs:
#   - book/book/html/ and book/book/pandoc/pdf/book.pdf exist (i.e.
#     `make release` was run from the repo root)
#   - tag $(VERSION) exists on origin and has an associated GitHub Release
#   - `gh` is authenticated (gh auth status)
release-publish:
	@if [ "$(VERSION)" = "dev" ]; then \
	    echo "VERSION=dev refuses to publish — re-run as 'make release-publish VERSION=v1.0.0'" >&2; \
	    exit 2; \
	fi
	@if [ ! -f book/book/pandoc/pdf/book.pdf ]; then \
	    echo "book/book/pandoc/pdf/book.pdf missing — run 'make book-pdf BOOK_BACKEND=docker' first" >&2; \
	    exit 2; \
	fi
	@if ! gh release view $(VERSION) >/dev/null 2>&1; then \
	    echo "No GitHub Release found for tag $(VERSION) — wait for release.yml to finish, then retry" >&2; \
	    exit 2; \
	fi
	@echo "==> [1/2] Pushing HTML book to gh-pages"
	@$(MAKE) book-publish
	@echo ""
	@echo "==> [2/2] Uploading PDF book to GitHub Release $(VERSION)"
	@# The asset's filename (not just display label) needs to be
	@# roksbnkctl-book-$(VERSION).pdf so the download URL is predictable
	@# and unique-per-release. gh release upload's `path#label` syntax
	@# only sets the display label — the asset name stays as the source
	@# filename. So we copy to a properly-named tempfile and upload that.
	@tmp=$$(mktemp -d -t roksbnkctl-pdf.XXXXXX) && \
	    trap "rm -rf $$tmp" EXIT && \
	    cp book/book/pandoc/pdf/book.pdf "$$tmp/roksbnkctl-book-$(VERSION).pdf" && \
	    gh release upload $(VERSION) "$$tmp/roksbnkctl-book-$(VERSION).pdf" --clobber
	@echo ""
	@echo "==> Published:"
	@echo "    HTML: https://jgruberf5.github.io/roksbnkctl/book/"
	@echo "    PDF:  $$(gh release view $(VERSION) --json url --jq '.url')"

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
