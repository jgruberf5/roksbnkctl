You are the **staff engineer** agent, **round 5**, for Sprint 18
Issue 2 (cos perf) of the roksbnkctl project. Repo root:
`/mnt/c/project/roksbnkctl`. You run with no memory of prior
conversation.

## Context — three integrator-prescribed rounds have failed to move
## the live wall-clock

| Round | Approach | Live wall-clock on bnk-schematics-resources |
|---|---|---|
| 2 | Per-bucket region cache + shared creds + sequential HeadBucket-probe sweep | 89s |
| 3 | Swap probe for single `s3:GetBucketLocation` against home region | **404 (reverted)** — premise was wrong; IBM COS's S3 API is endpoint-scoped |
| 4 | Parallel HeadBucket fan-out (matches `ibmcloud cos` CLI's own shape) | 88s |

The architectural invariants are correct and hermetically pinned (single
Client per invocation, shared `*credentials.Credentials`, per-region S3
handle cache, per-bucket region cache). But the live `cos object list`
on a cross-region bucket still takes ~88 seconds vs `ibmcloud cos
objects`'s 1.4 seconds — **63×** of Issue 2 AC #1's `≤2×` target.

**Three prescription-first attempts produced ~1 second of improvement.**
This round is **investigation first**: you measure where the time
actually goes, *then* you fix the right thing.

## Task A — find the bottleneck

You have authority to make live IBM Cloud calls in this round. The
project's `./terraform.tfvars` carries a working `IBMCLOUD_API_KEY`
(load it the same way the integrator does — never echo, never write
into argv, only read into an env var):

```bash
export IBMCLOUD_API_KEY="$(grep -E '^[[:space:]]*ibmcloud_api_key' \
    ./terraform.tfvars | \
    sed -E 's/^[[:space:]]*ibmcloud_api_key[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/')"
```

Add **temporary** timing instrumentation to `internal/cos/client.go`
that records, with absolute `time.Now()` stamps + the elapsed delta
to stderr (or a `t.Logf` if you make a helper-test that drives the
live call), at least these points in the `cos object list <bucket>`
codepath:

- Process start (in main / cobra entry, OR just at `cos.New`'s first
  line — pick the earliest practical anchor).
- IAM token first-fetch (whenever the SDK actually hits the IAM
  endpoint — the `ibmiam.NewStaticCredentials` call itself is
  cheap; the token fetch happens lazily inside the SDK when the
  first S3 op runs; you may need to wrap `c.creds` in a tracing
  shim that prints when it does the network round-trip).
- Start of `DefaultBucketRegionResolver` (entry).
- Each HeadBucket probe: start + finish (per region).
- `DefaultBucketRegionResolver` returns the winning region.
- `s3ForBucket` returns the regional handle.
- `ListObjects` request start + response start + each page boundary.
- Process exit (timing from start).

Build the binary with the instrumentation; run **one live call** —

```bash
time /tmp/roksbnkctl-fixed cos object list bnk-schematics-resources \
    --instance bnk-orchestration
```

— and capture the per-step timing breakdown. Paste the breakdown
into your closure. The expected total is ~88s; the breakdown should
account for where that 88s is spent.

Suspects, ranked by my prior guesses (you confirm or reject with the
data):

1. Wrong-region HeadBucket probes don't fail fast; SDK retries
   each one with exponential backoff (default ~3 retries +
   per-call timeout ~30s), summing to many seconds before erroring.
   Parallel ctx-cancel may not actually terminate the in-flight
   HTTP requests inside the IBM Go SDK.
2. IAM token first-fetch is unexpectedly slow (network / IAM
   endpoint).
3. The "winner" (us-south) HeadBucket itself is slow.
4. Something else entirely — `ListObjects` paging timing, SDK
   default retry behaviour on a 2xx, …

## Task B — propose the fix, **with evidence**

Based on the breakdown, propose the smallest change that closes the
wall-clock gap to ≤2× of `ibmcloud cos objects` (1.4s baseline →
≤2.8s target).

Options the integrator has thought about but **does not prescribe**:

- Tighter per-probe HTTP timeout via `aws.Config.HTTPClient` (e.g.
  3-5s instead of the SDK default).
- Disable per-call SDK retries via `aws.Config.MaxRetries`
  (`int(0)` disables; verify the IBM fork honours this).
- Explicit context-with-timeout on the per-region probe so it does
  return fast and ctx-cancel actually fires.
- Wire `IBMCLOUD_REGION` / a `--region <bucket-region>` flag as an
  explicit override (the operator already knows the bucket lives
  in us-south for the case at hand) — gives a fast-path without
  any probing.
- Something the breakdown surfaces that nobody has considered.

You can pick multiple if they're complementary (e.g. tighter
per-probe timeout + ctx-with-timeout). Document each in your
closure.

## Task C — implement, verify, clean up

Implement your chosen fix. **Remove the Task A instrumentation
before reporting done** — that was investigation scaffolding; it
doesn't ship.

Then:

- `go test -race ./internal/cos/` green incl. round-1/2/4 hermetic
  invariants byte-unchanged AND any new test you add for your fix.
- One live re-verify: same `time roksbnkctl cos object list
  bnk-schematics-resources --instance bnk-orchestration`. Predicted
  vs measured wall-clock in your closure.

## Constraints

- Touch only `internal/cos/client.go` for the fix (same scope as
  round-3/4). The instrumentation goes there too and gets removed
  before close.
- Do not edit any pre-existing `_test.go`. Additive only.
- Do not commit. Integrator commits.
- Do not run `gh issue create`.
- Do not modify `go.mod` without strong justification documented.

## If the bottleneck is unfixable in this scope

If your investigation shows the 88s is dominated by IBM-side
factors that can't be worked around inside `internal/cos/client.go`
(e.g. IAM endpoint genuinely takes 80s on this account; SDK ignores
context cancellation in a way you can't override; etc.), say so
**plainly** in your closure. Issue 2 ships as `wontfix-this-cycle`
in v1.7. Honest "this isn't fixable in scope, here's the data
proving it" is a successful round-5 outcome.

## Final report

≤300 words: the breakdown (where the 88s actually goes), the fix
you chose with one-sentence justification per option, files
touched (instrumentation removed; only the fix remains), hermetic-
test pass list, **measured** live wall-clock (not predicted), and
either (a) "Issue 2 ready for closure" or (b) "Issue 2 cannot be
fixed in `internal/cos/client.go` scope — defer to v1.7" with a
data-grounded reason.
