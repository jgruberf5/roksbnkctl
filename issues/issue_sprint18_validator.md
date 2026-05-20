# Sprint 18 — validator issues (cos bucket get + post-v1.6.2 work cycle)

> **Sprint 18 frame.** First regular work sprint post-`v1.6.2`.
> Validator owns hermetic tests + gated live-verify driver for both
> scope items: the staff `cos bucket get` feature (sha256
> round-trip, key-prefix subdir behaviour, error paths, `--no-clobber`;
> opt-in live driver) and the architect mermaid PDF fix (a
> `pdftotext`-driven assertion that wires into the book pipeline so
> a regression in the docker image's font set / SVG-conversion path
> fails the build, not silently ships).

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Hermetic + gated-live tests for `cos bucket get`

**Severity**: medium
**Status**: open

**Description.** Sprint 18 staff Issue 1 introduces
`roksbnkctl cos bucket get --instance <inst> <bucket> <local-dir>`
(recursive streaming download). The validator deliverable is the
test surface that proves the staff feature meets its acceptance
criteria — both hermetically (fast, CI-runnable) and live (the
sha256 round-trip the staff acceptance criterion #2 specifies, which
requires a real COS bucket).

**Hermetic tests** (additive, new file, no edits to existing tests):

- `internal/cos/bucket_get_test.go` — table-driven against a fake
  COS client/iterator; cases for (a) empty bucket → exit 0 no
  files; (b) flat keys → flat files; (c) `/`-containing keys →
  nested subdirs created `mkdir -p`-style; (d) `--no-clobber`
  skips an existing local file (mtime unchanged); (e) `--instance`
  missing → typed error; (f) non-existent bucket → typed error;
  (g) `<local-dir>` uncreatable (permission-denied) → typed error
  *before* any download starts.

**Gated live-verify driver** (operator-run, NOT CI):

- `scripts/e2e-cos-bucket-get.sh` mirroring
  `scripts/e2e-phase-handoff.sh`'s style: `set -euo pipefail`,
  `DRY_RUN=1` walk, `redact()` over any echo'd command, `LOG_DIR`,
  `trap` cleanup. Provisions a temporary bucket on a workspace's
  registry COS instance, uploads a small fixture set (one text +
  one binary + one nested-key), runs `roksbnkctl cos bucket get`,
  asserts sha256 round-trip per file, cleans up bucket on `EXIT`.

**Acceptance criteria**:

1. `go test -race ./internal/cos/` green incl. the new
   `bucket_get_test.go`; pre-existing `internal/cos/cos_test.go`
   byte-unchanged (`git diff --stat -- '*_test.go'` shows only the
   new file).
2. The seven hermetic cases (a)–(g) each have a sub-test named for
   the acceptance criterion they cover; sub-test names appear in
   `go test -v` output.
3. `bash -n scripts/e2e-cos-bucket-get.sh` clean; `DRY_RUN=1`
   walks every step with **zero cloud calls** and **zero key
   leaks** (planted-sentinel check on stdout + log).
4. Sphere-of-responsibility split is honoured: the validator never
   reaches into `internal/cli/` (that's a staff change in this
   sprint); the validator's test seam is the public function staff
   exposes from `internal/cos/`.

**Out of scope**:

- Concurrency-stress test of the recursive download — staff Issue 1
  §"Out of scope" deferred concurrent download; the validator
  doesn't pre-test something that won't exist.

**Files affected**: `internal/cos/bucket_get_test.go` (new);
`scripts/e2e-cos-bucket-get.sh` (new).

**Related**: staff Issue 1; the Sprint 16 `scripts/e2e-phase-handoff.sh`
as the gated-live-verify reference pattern.

---

## Issue 2 — Regression check on the mermaid PDF fix

**Severity**: medium
**Status**: open

**Description.** Sprint 18 architect Issue 1 fixes the mermaid PDF
text-missing rendering (most likely a docker-image font / SVG-to-PDF
conversion issue). The validator deliverable is a smoke check that
runs as part of the PDF build and **fails the build** if a future
contributor regresses the fix.

**Acceptance criteria**:

1. After `make book-pdf BOOK_BACKEND=docker`, an automated check
   extracts text from a known-mermaid page (e.g. page 120 of the
   produced PDF) via `pdftotext` and greps for a label the
   architect chose as the canary. Missing label → non-zero exit.
2. The check is wired into the book pipeline so a CI / release
   build that ships a PDF without the canary label visibly fails
   (the architect's Issue 1 acceptance criterion #2 names this
   gate; the validator implements it).
3. The check survives a docker-image tag bump — i.e. the image
   reference in `book.toml` can change without the check needing
   to be edited.

**Out of scope**:

- Restyling the mermaid blocks or moving them between chapters —
  the architect's fix is pipeline-side; the validator's check is
  also pipeline-side.
- A full visual-diff test of the rendered PDF — overkill for the
  single defect class.

**Files affected**: `scripts/check-pdf-mermaid-labels.sh` (new) or
equivalent; whatever `Makefile` target or `book.yml` workflow step
invokes the PDF build (additive call).

**Related**: architect Issue 1 (the fix this check guards).
