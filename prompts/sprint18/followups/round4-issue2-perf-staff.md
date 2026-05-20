You are the **staff engineer** agent, **round 4**, for Sprint 18
Issue 2 (cos perf) of the roksbnkctl project. Repo root:
`/mnt/c/project/roksbnkctl`. You run with no memory of prior
conversation.

## Context — round 3 was reverted; the integrator's premise was wrong

Round-2 (commit `5a1b13b`) shipped a sound architectural fix
(single Client per invocation, shared creds, regional cache,
per-bucket region cache via the `BucketRegionResolver` seam). It
left `DefaultBucketRegionResolver` as a sequential HeadBucket
sweep across candidate regions — correct but slow (89s for a
single `cos object list` against a cross-region bucket; baseline
`ibmcloud cos objects` is 1.4s; Issue 2 AC #1 wants ≤2×).

Round-3 was an integrator-prescribed swap to `s3:GetBucketLocation`
against the home-region endpoint, based on the integrator's
**incorrect** premise that "IBM COS answers `GetBucketLocation`
from any home-region endpoint regardless of the bucket's true
region (instance-scoped via IAM)". That premise is **false** — the
S3 API is endpoint-scoped, so `GetBucketLocation` against
`s3.ca-tor…` 404s when the bucket lives in us-south. Round-3
shipped + live-verified RED + was reverted on `main` at commit
`39a9af5`. The integrator's two attempts to root-cause this
particular bug have both been wrong. **Round 4's contract is
investigate first, then implement.**

## Task — pick the right approach, don't take the integrator's word

You are NOT prescribed a specific IBM SDK call. Read the source
material below, then **pick** the lookup mechanism, implement it
in `DefaultBucketRegionResolver`, and explain why in your closure.

**Investigation sources (read at least these before deciding):**

1. **IBM Cloud Resource Controller v2 API docs** — search for the
   `service_instances/{id}/buckets` shape and / or the COS
   Extensions endpoint that exposes bucket metadata (location,
   storage class, etc.). The COS docs site has a "Find a bucket's
   location" page. The IBM Cloud SDK for Go's
   `resourcecontrollerv2` package may expose this.
2. **The `ibmcloud cos` CLI's source** — open-source Go binary
   under `github.com/IBM-Cloud/ibm-cloud-cli`. Trace what
   `ibmcloud cos object-list <bucket>` does to discover the
   bucket's region before issuing the listing. (The CLI is fast,
   so whatever it does is the right shape.)
3. **The IBM COS SDK for Go documentation** for any
   `service/s3control` or `service/configmanager` package that
   exposes per-bucket location without a region probe.
4. The current `DefaultBucketRegionResolver` in
   `internal/cos/client.go` — you are replacing this and only this.
   `BucketRegionResolver` type stays. `regionFor` / `regionalS3` /
   `Client` struct / `s3ForBucket` all stay.

**Candidate approaches** (you decide which is correct; don't trust
the integrator's labels):

- (a) IBM Resource Controller HTTP API call.
- (b) IBM COS Extensions endpoint (`s3.cloud-object-storage.…/configuration/{instance}/buckets/{bucket}`
  or similar — verify the exact URL).
- (c) A different S3 op against a region-agnostic endpoint
  (verify whether IBM COS has one; AWS S3 doesn't but IBM may).
- (d) A faster HeadBucket-probe loop (parallelize the probes,
  cap timeouts at 1-2s each) — keeps round-2's design but cuts the
  latency by an order of magnitude.

For each candidate, your closure should state why you picked it
AND why you rejected the others.

## Verify your premise before you finish

After implementing, do a sanity check against the live
documentation — does the SDK call you wired actually behave the
way the docs say it does for cross-region buckets? If your
implementation is integrator-style "trust the API surface and
move on", **stop and re-read the docs.** Round-3's failure was
exactly that.

Your hermetic test (new file
`internal/cos/client_default_resolver_test.go` — the old file got
deleted in the revert; you can write it fresh) must pin:

- The new resolver completes successfully for a cross-region
  bucket (mock the lookup; assert one call per bucket).
- A genuinely non-existent bucket → error names the bucket
  (Issue 3 AC #3 clarity preserved).
- `LocationConstraint` / region-string parsing (if your chosen
  approach returns a `region-storageclass` shape, strip the
  suffix; if it returns a bare region, pass through).

## Constraints (unchanged from round-3)

- **Touch only `internal/cos/client.go`** — only the
  `DefaultBucketRegionResolver` definition + any small helpers it
  needs. **No changes to** `BucketRegionResolver` type, `regionFor`,
  `regionalS3`, `Client` struct, `s3ForBucket`, or any other
  function.
- **Add ONE new test file** at
  `internal/cos/client_default_resolver_test.go`. **Do not edit**
  any pre-existing `_test.go` (including the round-2 invariants in
  `client_region_test.go` and `client_perf_test.go`, which must
  keep passing against your new resolver byte-unchanged).
- Do **not** commit. Integrator commits.
- Do **not** run `gh issue create`.
- Do **not** modify `go.mod` or pull in a new dependency unless
  the chosen approach genuinely requires it; if it does, document
  why in your closure.

## Verify before reporting done

- `go build ./...` clean. `go vet ./...` clean.
  `gofmt -l internal/` empty.
- `go test -race ./internal/cos/` green — round-1 / round-2
  invariants byte-unchanged and still passing, plus your new test
  file.
- `git diff --stat -- '*_test.go'` shows ONLY the one new file.

## Final report

≤250 words: the approach you chose (a/b/c/d/other), **why** —
specifically what docs or source you read that decided it; the
file changes (should be exactly two: `client.go` + the new test
file); the hermetic-test pass list; your **predicted** live
wall-clock for `roksbnkctl cos object list bnk-schematics-resources
--instance bnk-orchestration` (baseline 1.4s for `ibmcloud cos
objects`; target ≤2×); the candidate approaches you **rejected**
and one-line why; and an honest statement of remaining risk if any.
State explicitly: did not commit, did not push, did not touch any
file outside the named scope.
