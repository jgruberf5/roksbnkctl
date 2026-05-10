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

.PHONY: book book-serve book-clean

book:
	mdbook build book/

book-serve:
	mdbook serve book/ --open

book-clean:
	rm -rf book/book

# --- Sprint 0 staff additions ---
# Note: `build` and `test` already exist above and are kept verbatim
# (their existing recipes are richer than the Sprint 0 spec — build wires
# ldflags for version stamping). See issues/issue_sprint0_staff.md for
# the rationale.

.PHONY: test-short lint pre-commit-install

test-short:
	go test -short ./...

lint:
	gofmt -d -l . && go vet ./... && (command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not on PATH; skipping")

pre-commit-install:
	ln -sf ../../scripts/pre-commit.sh .git/hooks/pre-commit && echo "Pre-commit hook installed."
