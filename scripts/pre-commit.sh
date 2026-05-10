#!/usr/bin/env bash
# Pre-commit hook for roksbnkctl.
#
# Runs gofmt, go vet, and the short unit tests. Designed to stay well
# under 30s on a clean tree — it's the difference between developers
# leaving the hook on and developers running with --no-verify.
#
# Install with `make pre-commit-install`. Bypass with `git commit --no-verify`.

set -euo pipefail

# Move to repo root so relative paths in `go` invocations work even when
# the user runs `git commit` from a subdirectory.
repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

echo "pre-commit: gofmt"
diff=$(gofmt -d -l .)
if [ -n "$diff" ]; then
  printf 'gofmt found unformatted files:\n%s\n' "$diff" >&2
  echo "run: gofmt -w ." >&2
  exit 1
fi

echo "pre-commit: go vet"
go vet ./...

echo "pre-commit: go test -short ./internal/..."
go test -short ./internal/...

echo "pre-commit: ok"
