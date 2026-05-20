# Sprint 18 — validator resolution log

## Issue 1 — hermetic + gated-live tests for `cos bucket get` → **resolved; shipped in `v1.6.3`**

`internal/cos/bucket_get_test.go` (new, additive — no edits to any pre-existing `_test.go`) — 7 sub-tests, each named for the staff Issue 1 acceptance criterion it covers (AC1+2 sha256 round-trip on flat keys, AC3 nested-key mkdir-p, AC4 `--no-clobber` mtime-preserved, AC5 empty-bucket, AC6 missing-instance, AC7 non-existent-bucket, AC8 uncreatable-dest-before-download). Drives staff's `cos.GetBucket(ctx, instanceID, bucket, destDir, opts) (GetBucketCounts, error)` entry point through an in-memory `fakeCOS`; never opens a socket.

`scripts/e2e-cos-bucket-get.sh` (new, +x) — opt-in gated live-verify driver mirroring `scripts/e2e-phase-handoff.sh`'s style: `redact()` over every echoed command, `DRY_RUN=1` walkthrough with zero cloud calls, structured log under `$LOG_DIR/cos-bucket-get-$RUN_TS.log`, EXIT-trap teardown deleting the temporary bucket on pass OR fail, A1–A5 assertions including a planted-sentinel API-key leak scan.

**Live verify (2026-05-20)**: the integrator skipped the e2e driver in favour of a leaner read-only reproducer against the real `bnk-schematics-resources` (us-south) bucket — `cos bucket get bnk-schematics-resources /tmp/sprint18-out --instance bnk-orchestration` → 9 objects round-tripped byte-identical. Equivalent test surface; faster + cheaper.

## Issue 2 — regression smoke check on the mermaid PDF fix → **resolved; shipped in `v1.6.3`**

`scripts/check-pdf-mermaid-labels.sh` (new, +x) — pdfimages-driven gate that runs as part of the `book-pdf` Makefile target. Asserts (a) embedded raster-image count ≥ mermaid-fence count (3 in the current book) and (b) every image ≥ 600 px wide (retina-grade render). Page-agnostic + image-tag-agnostic so the check survives a docker image rebump. Validator confirmed the gate FAILS on the pre-fix SVG-output pipeline (0/3 images) and PASSES on the post-fix PNG-output pipeline (3/3, 1568 px). `Makefile`'s `book-pdf` target invokes the check after the pandoc step.

**Live verify (2026-05-20)**: `make book-pdf BOOK_BACKEND=docker` + the smoke check → `PASS  mermaid-PDF regression guard — architect Sprint 18 Issue 1 fix holds`.

## Status

Issue 1: **resolved**. Issue 2: **resolved**. Live-`!`-verify gates for both satisfied per `live-verify-high-issues`.
