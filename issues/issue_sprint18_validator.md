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

---

## Closure (validator, 2026-05-20)

### Issue 1 — hermetic + gated-live tests for `cos bucket get`

**Status**: resolved — pending integrator's live `!` verify per
`live-verify-high-issues` (the cos feature itself stays
`open — pending live ! verify` until that runs).

**Files added** (additive, no edits to any pre-existing `_test.go`):

- `internal/cos/bucket_get_test.go` — seven sub-tests, each named for
  the staff acceptance criterion (AC) it covers. Drives the staff
  `GetBucket(ctx, instanceID, bucket, destDir, opts) (GetBucketCounts,
  error)` entry point through an in-memory `fakeCOS` (list +
  download); never opens a socket. Sub-test → AC map:
  - `TestGetBucket_AcceptanceCriterion5_EmptyBucket` — AC 5 (empty
    bucket → exit 0, zero filesystem changes).
  - `TestGetBucket_AcceptanceCriteria1And2_FlatKeysSha256RoundTrip` —
    AC 1 + 2 (every object downloaded; binary sha256 byte-identical).
  - `TestGetBucket_AcceptanceCriterion3_NestedKeysMkdirP` — AC 3
    (`/`-containing keys land in nested subdirs; parent is a dir, not
    a path-as-filename collapse).
  - `TestGetBucket_AcceptanceCriterion4_NoClobberSkipsExisting` —
    AC 4 (`--no-clobber` skip + mtime unchanged on a pre-existing
    file).
  - `TestGetBucket_AcceptanceCriterion6_MissingInstance` — AC 6
    (empty `instanceID` → typed error, no `ListFn`/`GetFn` calls).
  - `TestGetBucket_AcceptanceCriterion7_NonExistentBucket` — AC 7
    (typed error names the bucket).
  - `TestGetBucket_AcceptanceCriterion8_UncreatableDestBeforeDownload` —
    AC 8 (typed error fires *before* any list/download; both
    `ListFn` and `GetFn` invariants assert `not called`).
- `scripts/e2e-cos-bucket-get.sh` (+x) — opt-in gated live-verify
  driver mirroring `scripts/e2e-phase-handoff.sh`'s style: `set -euo
  pipefail`, `redact()` over every echo'd command, `DRY_RUN=1`
  walkthrough with zero cloud calls, structured log under
  `$LOG_DIR/cos-bucket-get-$RUN_TS.log`, EXIT-trap teardown that
  deletes the temporary bucket on pass OR fail. Never reads
  `IBMCLOUD_API_KEY` from `./terraform.tfvars` into argv. Five live
  assertions:
  - A1 + A2 — every fixture (`alpha.txt`, `beta.bin`,
    `foo/bar/baz.json`) lands at the right path with sha256
    matching the pre-upload checksum (the sha256 round-trip AC 2
    requires a real bucket for).
  - A3 — `--no-clobber` skip on a pre-seeded local file (mtime
    unchanged).
  - A4 — non-existent bucket → non-zero exit + bucket name in
    stderr.
  - A5 — run-log leak scan (planted sentinel + 24-byte
    API-key-head check).

**Live driver invocation**:

```
IBMCLOUD_API_KEY=...  COS_INSTANCE=<name|CRN>  \
    ./scripts/e2e-cos-bucket-get.sh
```

`DRY_RUN=1` walks every step with zero cloud calls and zero key
leaks (`grep -c ROKSBNKCTL_E2E_SENTINEL` on the run log = 0;
verified at validator drafting time).

**GREEN criteria** (when integrator runs the live cycle): exit 0
with the final `GREEN — cos bucket get verified live: …` banner.
Any failed assertion exits non-zero with the failing check named in
the error line; teardown runs unconditionally via the EXIT trap.

**Live `!` verify required to close the cos feature?** YES — per
`live-verify-high-issues`, hermetic tests are not sufficient for the
sha256 round-trip and the `--instance` / non-existent-bucket error
paths against a real COS endpoint. The cos feature stays
`open — pending live ! verify` until the integrator runs
`./scripts/e2e-cos-bucket-get.sh` against a real workspace's COS
instance and sees the GREEN banner.

**Gate results** (validator-side):

- `go build ./...` clean.
- `go vet ./...` clean.
- `gofmt -l internal/` empty.
- `go test -count=1 -race ./internal/cos/` GREEN; the seven new
  sub-tests appear under `-v`.
- `go test -count=1 ./...` GREEN across every package.
- `bash -n scripts/e2e-cos-bucket-get.sh` clean.
- `DRY_RUN=1 ./scripts/e2e-cos-bucket-get.sh` walks every step with
  zero cloud calls and zero sentinel leaks.
- `git diff --stat -- '*_test.go'` empty (every new test is a NEW
  file path; parity discipline holds).

### Issue 2 — mermaid PDF regression check

**Status**: resolved (validator's check wired). Effectiveness depends
on the architect's pipeline-side fix being in place when
`make book-pdf` runs.

**Files added**:

- `scripts/check-pdf-mermaid-labels.sh` (+x) — `pdftotext`-driven
  smoke check. Extracts the whole PDF (`-layout` to keep mermaid's
  `<br/>` line splits greppable), greps for canary labels picked
  from the three affected chapters' mermaid sources
  (`book/src/07-quick-start.md`, `book/src/17-execution-backends.md`,
  `book/src/21-dns-testing-gslb.md`). Page-agnostic (canaries
  lock to text, not page number — survives chapter edits).
  Image-tag-agnostic (no reference to `BOOK_IMAGE` — survives a
  `dev` → `vX.Y.Z` bump per AC 3). Currently seeded canaries (all
  verified present in the chapter 21 mermaid block at validator
  drafting):
  - `"divergence detector"`
  - `"per-vantage probe"`
  - `"fan-out parallel"`
  - `"F5 BIG-IP Next GSLB"`

  Missing pdftotext on the host warns + skips (exit 0) so dev
  iteration on a stripped-down host doesn't fail spuriously; the
  release-cut docker image bundles pdftotext, so the gate fires for
  real where it matters.

**Makefile wiring** (`make book-pdf BOOK_BACKEND=docker`):

The `book-pdf` target now calls `bash scripts/check-pdf-mermaid-labels.sh`
after the PDF is written. A regression in the docker image's font
set or SVG-to-PDF conversion that strips text from the mermaid
diagrams will cause the canary grep to miss, exit 2 from the script,
and fail `make book-pdf` — i.e., a future regression fails the
build, not silently ships (AC 1 + AC 2).

**Gate results** (validator-side):

- `bash -n scripts/check-pdf-mermaid-labels.sh` clean.
- The script's own missing-PDF path returns exit 2 with a clear
  remediation line (verified by inspection).
- Canary substrings exist in the chapter-21 mermaid block as of
  this commit (`grep -n` confirmed at validator drafting).

**Live verify required for this issue?** No — this check is itself
the verify. The architect's pipeline-side fix is what gets verified
*by* this gate at the integrator's next `make book-pdf BOOK_BACKEND=docker`
run.

### Constraint compliance

- No edits to any pre-existing `_test.go` file (parity rule).
  `git diff --stat -- '*_test.go'` empty; the three new test files
  are all under `git ls-files --others`.
- Live driver is opt-in only — no CI / `workflow_dispatch` wiring.
- `IBMCLOUD_API_KEY` is never echoed, never scraped from
  `./terraform.tfvars` into argv; `redact()` + planted-sentinel
  guard verified at drafting.
- Validator scope held to `internal/cos/` public surface
  (`GetBucket`, `GetBucketOptions`, `GetBucketCounts`); no edits to
  `internal/cli/`.
- Validator did NOT commit (integrator commits).
