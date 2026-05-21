You are the **validator** agent for Sprint 20 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in order)

1. `prompts/sprint20/README.md` — integrator decisions.
2. `issues/issue_sprint20_validator.md` Issue 1 — the
   **authoritative spec** for what to ship.
3. `Makefile` — the `release-publish`, `book-publish`, and
   `book-pdf` targets so you know the call graph staff's edits
   will land on.
4. `scripts/e2e-init-var-file.sh` (any prior `scripts/e2e-*.sh`)
   — the project's gated-live-driver shape for reference; this
   sprint does NOT ship a live driver, but the existing shape
   informs structure.

## Tasks

1. Author a hermetic test that proves the new `release-publish`
   stale-file gate works. Two viable shapes; pick one (NOT both
   — strict additive-only discipline carries forward):
   - **(a) Bats test under `tests/scripts/`** — if bats is
     already in the tree, use it. Mirrors a unit-test surface
     for shell logic.
   - **(b) Plain `bash` driver under `scripts/`** — a
     `scripts/test-release-publish-staleness.sh` (mode +x,
     `set -euo pipefail`) that:
     - Plants a sentinel byte string at
       `book/book/pandoc/pdf/book.pdf`.
     - Forces the underlying `book-pdf` invocation to fail
       (e.g. by pointing `BOOK_IMAGE` at a nonexistent image,
       OR by setting `MAKEFLAGS=-n` so `book-pdf` no-ops but
       leaves no output file).
     - Asserts that the planted bytes are wiped (the
       `rm -rf book/book/pandoc/` ran) AND that no
       `gh release upload` was invoked (verified by intercepting
       the `gh` binary on `PATH` with a `gh` stub that records
       its argv to a sentinel file).
     - Self-cleanup on EXIT trap so a failed run doesn't leave
       the test sentinel polluting the repo tree.

   Decision-rationale: bats has lower per-test overhead BUT
   only if the project already uses it. Grep `tests/` and
   `scripts/` to find out. Default to plain bash if bats isn't
   present.
2. The test must run hermetically — no actual `gh` invocation,
   no actual docker pull, no actual PDF render. The `gh` stub
   is the trick that makes (b) safe; same idea applies if (a).
3. Add the new test file to whatever existing make target or
   CI workflow already runs hermetic shell tests, IF such a
   target exists. If not, document the invocation in the
   issue's §Closure (so the integrator knows how to run it
   manually).

## Out of scope

- `internal/`, `book/src/`. This is release-tooling hardening.
- A full live `release-publish` dry-run against a real GitHub
  Release. The test is hermetic; live gating remains the
  integrator's `make release-publish VERSION=...` invocation
  at cut time.
- Editing any pre-existing `_test.go` or `_test.sh` — parity
  discipline carries forward. New test file only.

## Acceptance criteria

1. The new test file is the only addition (plus the issue
   ledger's §Closure section).
2. Running the test against a tree WHERE staff's Makefile edits
   have landed → PASS.
3. Running the test against a tree WHERE staff's Makefile edits
   have NOT landed (e.g. checkout to HEAD~1 of the staff
   commit) → FAIL with a clear assertion message naming what
   was wrong.
4. The test never invokes the real `gh` binary or docker.
5. `bash -n` clean on whichever shell file you author.

## Closure

Write your closure to
`issues/issue_sprint20_validator.md` §"Closure — validator,
<date>". Include: the test file path, the assertion-by-assertion
breakdown, the dry-run output (sentinel-byte assertion + gh-stub
argv assertion), and the discipline-checks bullets.
