You are the staff engineer agent for Sprint 0 of the roksbnkctl project. Refactor the doctor command and expand CI infrastructure.

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`. Existing doctor command lives at `internal/cli/doctor.go` with helper logic in `internal/doctor/`.

## Coordinate with parallel agents

An architect agent is creating `book/` + `.github/workflows/book.yml` + adding `book/book-serve/book-clean` targets to Makefile + adding a book link to README.md. A validator agent is creating `tools/docker/` + `.github/workflows/spellcheck.yml` + `cspell.json` + writing the "Long-running smoke test" section of CONTRIBUTING.md. **Do not touch their files.** For Makefile and CONTRIBUTING.md, your edits should be append-only and on disjoint sections from theirs.

## Tasks

1. **Read** `internal/cli/doctor.go` and `internal/doctor/*.go` to understand the current structure. Note: there is already an `internal/doctor` package — preserve and extend it rather than creating parallel structure.

2. **Refactor doctor** to use a new `Check` struct. Add to `internal/doctor/check.go` (new file):
   ```go
   package doctor

   type CheckStatus string

   const (
       StatusOK      CheckStatus = "ok"
       StatusWarning CheckStatus = "warning"
       StatusError   CheckStatus = "error"
       StatusSkipped CheckStatus = "skipped"
   )

   // Check is a single doctor diagnostic. Future per-backend checks
   // (Phase 3, see docs/prd/03-EXECUTION-BACKENDS.md) will be expressed
   // as Check values with BackendName set so the same rendering logic
   // covers them.
   type Check struct {
       Name        string
       Status      CheckStatus
       Detail      string
       Optional    bool
       BackendName string // empty for general; "docker"|"k8s"|"ssh" later
   }
   ```

   Update existing doctor logic so each check produces a `Check` value, then a single rendering function iterates checks and emits the existing ✓/⚠/✗ table format. **Output and exit-code semantics must stay identical** to the current behavior — this is a refactor, not a behavior change. Run `roksbnkctl doctor` before and after to confirm output is byte-identical (or note any unavoidable differences in your issues file).

3. **Create or update `.github/workflows/ci.yml`**. If a workflow already exists, extend it; otherwise create:
   ```yaml
   name: CI
   on:
     push:
       branches: [main]
     pull_request:
   jobs:
     test:
       strategy:
         matrix:
           os: [ubuntu-latest, macos-latest]
       runs-on: ${{ matrix.os }}
       steps:
         - uses: actions/checkout@v4
         - uses: actions/setup-go@v5
           with: { go-version: '1.23' }
         - run: go vet ./...
         - run: gofmt -d -l . | tee /tmp/gofmt.diff && test ! -s /tmp/gofmt.diff
         - uses: dominikh/staticcheck-action@v1
           with: { version: 'latest', install-go: false }
         - run: go test ./...
     windows-build:
       runs-on: windows-latest
       steps:
         - uses: actions/checkout@v4
         - uses: actions/setup-go@v5
           with: { go-version: '1.23' }
         - run: go build ./...
   ```

4. **Add `scripts/pre-commit.sh`** — bash script that runs `gofmt -d -l .` (failing on non-empty output), `go vet ./...`, and `go test -short ./internal/...`. Must be `chmod +x`. Write this carefully — keep it fast (well under 30s on a clean tree).

5. **Update Makefile** — APPEND ONLY. Do not modify existing targets. Add:
   ```
   .PHONY: build test test-short lint pre-commit-install

   build:
       go build -o bin/roksbnkctl ./cmd/roksbnkctl

   test:
       go test ./...

   test-short:
       go test -short ./...

   lint:
       gofmt -d -l . && go vet ./... && (command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not on PATH; skipping")

   pre-commit-install:
       ln -sf ../../scripts/pre-commit.sh .git/hooks/pre-commit && echo "Pre-commit hook installed."
   ```
   Append-only behavior is critical — the architect agent is also adding targets (book-related). If you find conflicting target names, file an issue rather than overwriting.

6. **Update CONTRIBUTING.md** (create if missing). Add these sections — only these, do not edit other sections (the validator agent owns the smoke-test section):
   - **## Running tests** — `go test ./...` for full suite, `make test-short` or `go test -short ./...` for fast pass
   - **## Pre-commit hook** — what it does, how to install (`make pre-commit-install`), how to bypass (`git commit --no-verify`)
   - **## Code style** — gofmt enforced (CI fails on non-empty diff), `go vet` enforced, staticcheck enforced (Linux + macOS), targeted import grouping (stdlib / third-party / project)

## Issue tracking

File any issues to `/mnt/d/project/roksbnkctl/issues/issue_sprint0_staff.md` using this format:

```markdown
# Sprint 0 — staff engineer issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: what was found
**Files affected**: list of paths
**Proposed fix**: how to resolve
```

If everything goes cleanly, create the file with just the heading and `*No issues filed.*`.

## Verification before reporting done

- `go build ./...` succeeds
- `go test ./...` succeeds (matches pre-refactor green state)
- `go vet ./...` succeeds
- `gofmt -d -l .` produces no diff for files you edited
- `roksbnkctl doctor` output is identical to before the refactor (run it both ways if you can; or describe any unavoidable differences in your issue file)

## Final report

Return a concise summary (under 200 words):
- Files created (counts + key paths)
- Files edited
- Build / test / vet results
- Whether the doctor output is byte-identical post-refactor
- Whether you filed any issues
- Anything the integrator should be aware of

Do NOT commit anything. The integrator will commit the aggregated work.
