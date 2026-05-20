You are the **staff engineer** agent, **round 2**, for Sprint 18 of
the roksbnkctl project. Repo root: `/mnt/c/project/roksbnkctl`. You
run with no memory of prior conversation.

## Why you are being re-dispatched

Round 1 (committed at `4da221a`) shipped Issue 1 (`cos bucket get`)
hermetically green, but manual testing of the surrounding `cos`
group surfaced two pre-existing bugs in the **shared client setup**
that block the live `!` verify of Issue 1 (the new `cos bucket get`
inherits the same client and therefore the same defects):

- **Issue 2** (medium): every `roksbnkctl cos *` command runs ~10×
  slower than the equivalent `ibmcloud cos` CLI.
- **Issue 3** (high): `roksbnkctl cos object list <real-bucket>
  --instance <real-instance>` returns 404 on a populated bucket
  that `ibmcloud cos object-list` reads cleanly.

Both bugs are almost certainly in `internal/cos/client.go`. Your job
is to fix them so Issue 1's live verify can run; you should **not**
re-touch `internal/cos/bucket.go` or `internal/cli/cos.go`'s
`runCOSBucketGet` — Issue 1 inherits whatever client improvements
you make.

## Read first (in order)

1. `issues/issue_sprint18_staff.md` Issue 2 and Issue 3 — the
   **authoritative spec**. Each has ranked-likelihood root-cause
   hypotheses, numbered testable acceptance criteria, deliberate
   out-of-scope, and named files-likely-touched. Every AC binds you.
2. `internal/cos/client.go` — the shared COS client constructor, the
   IAM authenticator setup, the endpoint URL resolution, and how the
   bucket region is (or isn't) consulted today. The two bugs almost
   certainly live here.
3. `internal/cos/{bucket,object}.go` — the call sites that use the
   client. Read enough to understand what changes when the client
   contract changes (e.g. if you add a per-bucket region cache, who
   pays the lookup cost — one place or every call site?).
4. `internal/cli/cos.go` — the cobra wiring. Decide whether
   Issue 3's fix surfaces a new `--region` flag (option (b) in the
   spec) or stays internal (option (a) auto-resolve via the
   resource-controller). The issue spec asks you to pick + justify.
5. The IBM Go SDK shapes you'll likely call into:
   `s3manager`/`s3control`/`resourcecontrollerv2` — find the
   per-instance + per-bucket region lookup endpoint that
   `ibmcloud cos object-list` uses under the hood.

## Tasks

### Issue 3 — fix the 404 first (it gates the live verify)

Most likely root cause per the issue spec: the COS S3 client is
constructed against the **workspace cluster region's** endpoint URL
(e.g. `s3.ca-tor.cloud-object-storage.appdomain.cloud`), but the
bucket lives in a different region (e.g. `us-south`). Hence 404.

Pick **(a) auto-resolve via the COS extension API** unless you have
a concrete reason to prefer (b) `--region` flag. (a) is the
correct-by-default shape; (b) only as an explicit override for
edge cases. Whichever you pick, document the rejected option in 2-3
sentences in your closure.

If (a): introduce a small region-cache map keyed on `(instance,
bucket)` so the lookup runs once per bucket per CLI invocation, not
once per object. Cache lives on the `Client` struct so the entire
recursive `cos bucket get` runs share it.

The Issue 3 acceptance criteria all become testable once the region
resolution is in place. Add the hermetic test the spec names
(`internal/cos/client_region_test.go` or equivalent — *new* file,
no edits to existing tests).

### Issue 2 — fix the perf next

Most likely root cause per the issue spec: IAM token re-fetched per
call (and/or fresh client constructed per call). The `ibmcloud` CLI
caches the IAM bearer in `~/.bluemix/config.json` and re-uses it
until expiry; the Go SDK by default re-authenticates per
`New*Client()`.

Two concrete changes the issue's AC #2 implies:

1. The COS client is constructed exactly **once per roksbnkctl
   invocation**, not per verb or per object-iteration page. Add the
   hermetic assertion the spec names.
2. The IAM authenticator inside that client re-uses the token until
   expiry (the SDK's `IamAuthenticator` does this already if you
   don't tear it down — the bug is most likely a missing client
   pool / a fresh authenticator each time).

The benchmark micro-test (AC #3) measures `ListObjects` p50 + p95
against a stub; add it as a new file (`internal/cos/client_perf_test.go`
or similar). Threshold is documented in the test itself.

Caching the IAM token to disk across CLI invocations is **out of
scope** per the spec — don't add it; that's a separate cycle.

## Constraints

- **Do not edit any pre-existing `_test.go` file.** Sprint 18
  parity rule (the round-1 acceptance criterion #1 names it).
- **Do not touch `internal/cos/bucket.go`** or
  `internal/cli/cos.go`'s `runCOSBucketGet`. They inherit your
  client improvements automatically.
- **Do not touch `internal/orchestration/` or `internal/cli/cluster_phase.go`**
  — out of scope; the round-2 fix is `cos` shared-client only.
- **Do not commit.** Integrator commits. Do not push.
- **Do not run `gh issue create`.** No GitHub issues for in-flight
  work.

## Verify before reporting done

- `go build ./...` clean. `go vet ./...` clean.
  `gofmt -l internal/` empty.
- `go test -race ./internal/cos/` green incl. your new test files.
- `git diff --stat -- '*_test.go'` shows ONLY new files (parity
  discipline).
- Trace in your closure: a `cos object list <bucket-in-us-south>
  --instance <inst-in-default-rg>` invocation flows through the
  region-resolved client and hits `s3.us-south.cloud-object-storage…`,
  not `s3.ca-tor…`. Mirror trace for `cos bucket get`.

## Issue file

Append a **Round-2 Closure** section to
`issues/issue_sprint18_staff.md` covering Issues 2 + 3. Schema:
files changed, the option chosen for each issue (e.g. Issue 3:
chose (a) auto-resolve; rejected (b) because …), the
acceptance-criteria-by-name pass list per issue, hermetic test
results, and any judgement call worth integrator attention.

## Final report

≤200 words: files changed, option chosen per issue + 1-line
justification, acceptance-criteria pass lists, test results, and
the dataflow trace showing the right regional endpoint is used.
State explicitly you did not commit and did not touch the
forbidden files.
