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
