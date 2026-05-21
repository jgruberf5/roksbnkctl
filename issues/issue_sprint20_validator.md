# Sprint 20 — validator issues (release-publish stale-PDF gate)

> **Sprint 20 frame.** First regular work sprint post-`v1.6.4`.
> Validator owns the hermetic shell-level test surface that
> proves staff Issue 1's rebuild-then-upload contract holds
> against the planted-stale-PDF scenario the Sprint 19 v1.6.4
> integrator cut hit live.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Hermetic test for `release-publish` stale-artifact gate

**Severity**: medium
**Status**: open

### Motivation

Staff Issue 1 hardens `make release-publish` so a stale
`book/book/pandoc/pdf/book.pdf` from a prior cycle cannot be
uploaded into the current cycle's GitHub Release. The proof
that the hardening holds is a hermetic test that walks the
target with a planted stale artifact + a stubbed `gh` binary,
and asserts (a) the planted bytes do not survive the rebuild
preamble, and (b) `gh release upload` is never invoked with
stale content. This is the artifact-shape analog of Sprint 16
round-2's `live-verify-high-issues` discipline: the high-risk
publish-step gate gets its own pin.

### Acceptance criteria

1. **Test file**: one new test file ships
   (e.g. `scripts/test-release-publish-staleness.sh`, mode +x,
   `set -euo pipefail`), or one new bats test under
   `tests/scripts/` if bats is already in the tree. Pick one
   shape per the prompt; do NOT ship both.
2. **Planted-bytes assertion**: a sentinel byte string written
   to `book/book/pandoc/pdf/book.pdf` before the test runs
   `make release-publish` (or the equivalent recipe shape)
   does NOT survive the rebuild step.
3. **gh-stub assertion**: a `gh` stub on `PATH` intercepts every
   `gh release upload …` invocation; the test asserts the stub
   was either NOT called (the rebuild failed first) OR was
   called with a freshly-built `book.pdf` (validated by
   sentinel-byte absence in the uploaded path).
4. **Hermetic**: the test never invokes the real `gh` binary,
   never `docker pull`s, never renders an actual PDF. If the
   test environment can't satisfy any of those (e.g. docker
   isn't installed on the test host), the test must SKIP with
   a clear message, NOT fail.
5. **`bash -n` clean** on the test file.
6. **Failure shape pinning**: run the test against the
   pre-staff-edit tree (e.g. `git stash` the Makefile edits)
   and confirm it FAILS with an assertion message that names
   what was wrong. Capture that output in the closure section.
7. **No edits to any pre-existing `_test.go` or `_test.sh`** —
   strict additive parity discipline carries forward.

### Out of scope

- A full live `release-publish` invocation against a real
  GitHub Release. Live gating remains the integrator's manual
  invocation at cut time, per the existing Makefile docstring.
- Testing the `goreleaser` half of the release pipeline. That
  side is fine.

### Files affected

- The one new test file (path TBD by the implementer's choice
  between bats / plain bash).

### Related

- Sprint 20 staff Issue 1 — the code-side; this issue's test
  proves it.
- Sprint 16 round-2 `live-verify-high-issues` discipline —
  this sprint's hermetic test is the artifact-shape equivalent
  (publish-step gates don't have a "live cloud account" surface
  to verify against, so the hermetic shape is sufficient).
