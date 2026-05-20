# Sprint 18 — staff resolution log

## Issue 1 — `roksbnkctl cos bucket get` → **integrated + LIVE-VERIFIED GREEN, shipped in `v1.6.3`**

Round-1 shipped the cobra wiring + recursive download in `internal/cli/cos.go` + `internal/cos/bucket.go` (commit `4da221a`). Reuses `cos object get`'s s3manager-based streaming path; per-object `<file>.part` + atomic rename. Defensive traversal guard (`safeLocalPath`). JSON-mode per-item + summary record honouring `--output json`. `--no-clobber` flag implemented; concurrency-stress + `--prefix` filtering deliberately out of scope per the issue spec.

**Live verify (2026-05-20)**: `roksbnkctl cos bucket get bnk-schematics-resources /tmp/sprint18-out --instance bnk-orchestration` against the real us-south bucket → 9 objects downloaded byte-identical (sha256 match per file), total payload 14,644 bytes, exit 0. End-of-run stderr counters present. Inherits the round-2/4 cross-region resolver + the round-6 perf fix.

## Issue 2 — cos perf (~10× slower than `ibmcloud cos`) → **resolved (round-6); shipped in `v1.6.3`**

Three integrator-prescribed rounds (round-2 architecture / round-3 `GetBucketLocation` shortcut [reverted] / round-4 parallel HeadBucket fan-out) moved the live wall-clock by ~1 second total despite all hermetic invariants passing. **Round-5 was dispatched investigate-first with live-cloud-call authority** — instrumented the actual code path, found 76.4s of the 88s lived in `internal/ibm/cos_instance.go::ListCOSInstances` paginating the IBM Cloud Resource Controller v2 over every resource in the account. Round-6's fix narrowed the SDK call server-side via `ListResourceInstancesOptions.SetResourceID(<COS catalog offering UUID>)` + `SetName(<instance name>)`.

**Live re-verify (2026-05-20)**: same command, same bucket, fresh-IAM state → **1.86s** wall-clock (under the 1.4s `ibmcloud cos` baseline; ~47× speedup; well inside Issue 2 AC #1's ≤2.8s target).

**Lesson banked** as `~/.claude/.../memory/investigate-first-on-non-obvious-bugs.md` and linked from PLAN §"Sprint 18".

## Issue 3 — cos `object list/get` 404 on cross-region buckets → **resolved (round-2/4); shipped in `v1.6.3`**

Round-2 (commit `5a1b13b`) added the `BucketRegionResolver` seam + per-bucket region cache + per-region S3-handle cache + shared `*credentials.Credentials` across all regional handles. Round-3's integrator-prescribed `s3:GetBucketLocation` shortcut shipped + live-verified RED (the S3 API is endpoint-scoped — `GetBucketLocation` against the home-region endpoint 404s on a cross-region bucket) and was reverted on `main`. Round-4 (commit `ff1e871`) swapped in a parallel HeadBucket fan-out mirroring IBM's own `ibmcloud cos` CLI coordinator (`bucket_class_location.go::getBucketLocationCoordinator`).

**Live verify (2026-05-20)**: pre-fix → `Error: listing bnk-schematics-resources/: NoSuchBucket: ... status code: 404`. Post-fix → returns the 9-object listing. Issue 3 AC #3 (error names the bucket on resolver failure) preserved.

---

## Hermetic test surface (additive only — zero pre-existing `_test.go` edits across all four rounds)

`internal/cos/bucket_get_test.go`, `internal/cos/bucket_default_test.go`, `internal/cli/cos_bucket_get_help_test.go`, `internal/cos/client_region_test.go`, `internal/cos/client_perf_test.go`, `internal/cos/client_default_resolver_test.go`, `internal/ibm/cos_instance_filter_test.go`. Round-3's `client_default_resolver_test.go` was deleted in the revert and replaced by round-4's version. Full `go test -race ./internal/cos/ ./internal/ibm/ ./internal/cli/` green at each integration commit.

## Status

Issue 1: **resolved**.
Issue 2: **resolved** (round-6).
Issue 3: **resolved** (round-4).

Live-`!`-verify gate satisfied for all three per `live-verify-high-issues`.
