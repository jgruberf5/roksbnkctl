You are the **staff engineer** agent, **round 3**, for Sprint 18
Issue 2 (cos perf) of the roksbnkctl project. Repo root:
`/mnt/c/project/roksbnkctl`. You run with no memory of prior
conversation.

## Why you are being re-dispatched

Round-2 (commit `5a1b13b`) shipped a sound architectural fix for the
cos client setup — `BucketRegionResolver` seam, shared
`*credentials.Credentials`, per-region S3-handle cache, single
`Client` construction per invocation. Hermetic tests all pin those
invariants and pass.

**Live verify caught a residual.** `roksbnkctl cos object list
bnk-schematics-resources --instance bnk-orchestration` took **89
seconds** wall-clock vs **1.4 seconds** for the equivalent
`ibmcloud cos objects`. Same bucket, same network, same fresh-IAM
state — **~63× of the `≤2×` target in Issue 2 AC #1**. The cost is
in the **first cloud round-trips per invocation**: the
`DefaultBucketRegionResolver` in `internal/cos/client.go` does a
HeadBucket-probe sweep across candidate regions, paying a full
round-trip per probe-miss (plus the SDK's default retry/backoff)
before it finds the bucket's actual region.

## The fix — swap the resolver to `s3:GetBucketLocation`

IBM COS supports the S3 `GetBucketLocation` op. **One call against
the home-region S3 endpoint** returns the bucket's actual region in
the response body (`LocationConstraint`, e.g. `"us-south-standard"`
or just `"us-south"` depending on the bucket's creation flag). That
replaces the N-call HeadBucket sweep with a single call — should
cut the ~80s region-resolution overhead to ~1–2s.

Replace **only** `DefaultBucketRegionResolver` (or whatever the
exported production resolver in `internal/cos/client.go` is named)
with a `GetBucketLocation`-based implementation. The
`BucketRegionResolver` function-type signature stays the same so
all the round-2 hermetic tests + the `regionFor` cache + the
per-region S3-handle cache all keep working unchanged.

Edge cases the new resolver must handle:

- **Same-region buckets** (the home region IS the bucket's region):
  `GetBucketLocation` returns the same region; cache it; fast path
  preserved.
- **`LocationConstraint` parse**: IBM COS returns shapes like
  `"us-south-standard"` / `"us-south-smart"` / bare `"us-south"`.
  Strip the storage-class suffix to get the canonical region.
- **Bucket genuinely not found**: `GetBucketLocation` returns
  `NoSuchBucket` (404 from the home-region endpoint regardless of
  the bucket's true region). Wrap that into an error that names
  both the bucket and (hint) the operator's `--instance`, so the
  message helps distinguish "wrong bucket" from "wrong instance" —
  Issue 3 AC #3's clarity goal stays satisfied.
- **Cross-region behaviour**: `GetBucketLocation` against a bucket
  whose home region differs from the client's: IBM COS still
  answers from the home-region endpoint (the call is
  instance-scoped via IAM, not bucket-region-scoped). No 301
  redirect handling needed.

## Read first (in order)

1. `issues/issue_sprint18_staff.md` — Issue 2's spec (the AC #1
   wall-clock target is the gate; AC #2 single-construction +
   AC #3 hint behaviour stay in force).
2. `internal/cos/client.go` — current `BucketRegionResolver` type,
   `regionFor` cache, the round-2 `Client` shape, and (most
   importantly) wherever `DefaultBucketRegionResolver` is defined
   (round-2 staff said "default HeadBucket probe"; that's what
   you're replacing).
3. `internal/cos/client_region_test.go` and
   `internal/cos/client_perf_test.go` — the hermetic invariants
   round-2 pinned. **Do not edit either file.** They must keep
   passing against the new resolver.
4. The IBM Go SDK's `s3.GetBucketLocation` shape and
   `LocationConstraint` field — that's the SDK call you're wiring.

## Constraints

- **Touch only `internal/cos/client.go`.** Specifically, only the
  `DefaultBucketRegionResolver` definition. Don't touch the
  `BucketRegionResolver` type, the `regionFor` cache, the
  `regionalS3` map, the `Client` struct, or `s3ForBucket`.
- **Do not edit any pre-existing `_test.go`.** Round-1 / round-2
  parity discipline still applies. Add ONE new test file
  (`internal/cos/client_default_resolver_test.go` or similar) that
  pins:
  - The new resolver calls `GetBucketLocation` exactly once per
    bucket per call (use a fake S3 in `internal/cos/`'s
    same-package scope to count calls).
  - The `LocationConstraint` parser strips the
    `-{standard,smart,vault,cold}` suffix correctly.
  - The NoSuchBucket error path names the bucket.
- Do **not** commit. Integrator commits.
- Do **not** run `gh issue create`.

## Verify before reporting done

- `go build ./...` clean. `go vet ./...` clean.
  `gofmt -l internal/` empty.
- `go test -race ./internal/cos/` green — including the new file
  AND the round-2 hermetic tests
  (`client_region_test.go` + `client_perf_test.go`) which must NOT
  need to change.
- `git diff --stat -- '*_test.go'` shows ONLY the one new file.

## Issue file

Append a **Round-3 Closure** section to
`issues/issue_sprint18_staff.md` documenting: the SDK call you
wired, the `LocationConstraint`-parsing rule, the file changes
(should be ONLY `internal/cos/client.go` + the one new test file),
the hermetic test result, and the expected live wall-clock change
(your closure can state the prediction — the integrator runs the
single-command live re-verify after).

## Final report

≤200 words: the one-line code change essence ("swap
`DefaultBucketRegionResolver` from HeadBucket-probe sweep to
`s3:GetBucketLocation` single-call"), the `LocationConstraint`
parsing rule you implemented, files touched (should be exactly two:
client.go modified + one new test file), hermetic-test pass list,
and the predicted live wall-clock. State explicitly: did not
commit, did not touch any file outside the scope above, did not
modify round-1/round-2 tests.
