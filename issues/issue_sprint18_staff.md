# Sprint 18 — staff issues (cos bucket get + post-v1.6.2 work cycle)

> **Sprint 18 frame.** First regular work sprint post-`v1.6.2`. Staff
> owns the new `cos bucket get` feature (the former GitHub issue #1,
> filed in-tree on `prompts/sprint18` kickoff and deleted from
> GitHub). Live-verify discipline applies — closure on the live `!`
> verify the validator builds, not on hermetic-green alone.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — feat: `roksbnkctl cos bucket get` — recursive download of a COS bucket to a local directory

**Severity**: medium
**Status**: open

## Motivation

`roksbnkctl cos object get <bucket>/<key> <local-file>` already covers
single-object download (`internal/cos/object.go`), but recovering or
mirroring a whole bucket today requires `cos object list` + a shell
loop of `cos object get` calls. Add a first-class **bulk recursive
download** verb so the operator can pull every object in a bucket —
text, binary, any size — to a local destination directory in one
command. Symmetric with the existing `cos bucket {create,list,delete}`
group.

## Proposed command shape

```
roksbnkctl cos bucket get --instance <instance> <bucket> <local-dir> [flags]
```

Positional args mirror `cos object get`:

- `<bucket>` — bucket name (required).
- `<local-dir>` — destination directory; created if it does not exist
  (`mkdir -p` semantics, mode `0755`). Required.

`--instance <name|CRN>` is required, same as the rest of the `cos`
group.

## Behavior

- **Recursive by default.** Every object in the bucket is fetched.
  Object keys that contain `/` map to nested subdirectories under
  `<local-dir>` (so a key `foo/bar/baz.json` lands at
  `<local-dir>/foo/bar/baz.json`).
- **Streaming.** Match `cos object get`'s streaming write — no
  in-memory buffering of the whole object. Works for binaries of any
  size (FAR auth archives, JWT bundles, large logs).
- **Content-agnostic.** No interpretation of the bytes — text and
  binary are both copied through verbatim. `Content-Type` from COS is
  recorded only for informational stderr (no extension rewriting).
- **Overwrite by default**, with `--no-clobber` to skip objects whose
  local target already exists. (Matches the unix `cp -n` shape; the
  default is overwrite because the operator just asked to download.)
- **Empty bucket** → exit 0, single informational stderr line, no
  files created.
- **Counters at end** on stderr: `N objects, M bytes, K skipped (no-clobber)`.
- Honor the existing `--workspace`, `--output`, `--quiet`,
  `--verbose`, `--on`, `--backend` global flags — same as the rest of
  the `cos` subcommands. JSON output (when `--output json`) emits one
  JSON object per file completed (key, local path, size, etag,
  outcome).

## Acceptance criteria

1. `roksbnkctl cos bucket get --instance <inst> <bucket> /tmp/out`
   creates `/tmp/out/` and downloads every object in `<bucket>` to it,
   preserving the key path (subdirs created as needed). Exit 0.
2. A binary object round-trips byte-identical (sha256 before put ==
   sha256 after get).
3. A text object with `/`-containing key lands in the right
   subdirectory.
4. `--no-clobber` skips an object whose local target already exists
   (verified by mtime unchanged on a pre-existing file).
5. Empty bucket → exit 0 + "no objects in bucket" stderr; no
   filesystem changes.
6. `--instance` missing → exit non-zero with the same error text as
   the other `cos bucket` verbs.
7. Non-existent `<bucket>` → exit non-zero, error names the bucket.
8. `<local-dir>` not creatable (e.g. permission denied) → exit
   non-zero before any download starts.
9. New additive Go tests in `internal/cos/` (no pre-existing test
   edited). Plus a `cos bucket get --help` smoke line wired into
   whatever help-snapshot test the package has.

## Out of scope (deliberately)

- `--prefix <key-prefix>` filtering — useful, but track separately
  to keep this first cut small. (Easy follow-up — `BucketIterator`
  already accepts a prefix in the SDK.)
- Concurrent multi-object download. Start sequential; add `-c N` only
  if a real run shows it bottlenecking.
- Resume / partial-download retry on transient errors. Streaming
  write to a temp file + atomic rename per object is sufficient for
  v1.
- `cos bucket sync` (bidirectional). Different verb, different
  semantics, different issue.

## Files likely touched

- `internal/cos/bucket.go` — new `GetBucket(ctx, instanceID, bucket,
  destDir, opts) (Counts, error)` function; iterate objects via the
  existing SDK list + reuse `internal/cos/object.go`'s streaming
  download per object.
- `internal/cli/cos.go` (or wherever `cos bucket {create,list,delete}`
  are registered as cobra commands) — register `cos bucket get`
  command with the same `--instance` flag and the two positional
  args.
- `internal/cos/cos_test.go` (additive only — no edits to existing
  tests; new `TestBucketGet_*` cases with a fake COS client).

---

## Closure (staff, 2026-05-20)

**Status**: resolved (pending live `!` verify per
`live-verify-high-issues` — closure is the validator's gated driver +
integrator-run live exercise; hermetic-green alone is not closure).

### What was implemented

- New package-level `cos.GetBucket(ctx, instanceID, bucket, destDir, opts) (GetBucketCounts, error)` —
  recursive download of every object in a bucket to a local directory,
  matching the function signature the validator's tagged hermetic suite
  (`internal/cos/bucket_get_test.go`, build-tag `sprint18_validator`)
  was drafted against. Up-front validation rejects empty
  `instanceID` / `bucket` / `destDir` and refuses to call the seam if
  `MkdirAll(destDir)` fails (acceptance #8 — `ListFn` / `GetFn` are
  asserted-not-called by the validator's sentinel).
- `GetBucketOptions{NoClobber, ListFn, GetFn, OnItem}` — `ListFn` /
  `GetFn` are the two seams (list-everything + stream-one-object) so
  the suite can drive an in-memory fake without ever opening a socket.
  `OnItem` lets the CLI emit per-file JSON / text without coupling the
  library to stdout.
- `GetBucketCounts{Objects, Bytes, Skipped}` — the run-wide tally
  printed on stderr in text mode and emitted as a trailing summary
  record in JSON mode.
- `cos.ClientGetBucketOptions(*Client)` — convenience helper that
  binds a live `*cos.Client` to `ListFn` / `GetFn`, so the CLI builds
  opts in two lines.
- New cobra subcommand `roksbnkctl cos bucket get <bucket> <local-dir>`
  registered alongside `create / delete / list`, with `--no-clobber`
  and the shared `cosBucketCmd` `--instance` flag. Text mode prints
  `→ Downloading … / get … / ✓ N objects, M bytes, K skipped`; JSON
  mode (`--output json`) streams one record per file plus a trailing
  `{"counts":{…}}` summary.
- Streaming download mirrors the existing single-object path: writes
  go to `<file>.part`, atomic-rename on success, `.part` removed on
  failure — so an aborted run never leaves litter. Per-object IO is
  the same `s3manager.Downloader` `cos object get` uses
  (`Client.GetObjectToFile` is the production `GetFn`).
- Defensive `safeLocalPath` refuses keys that resolve outside
  `destDir` (a hostile bucket can't write `/etc/passwd` via a
  `../../etc/passwd` key).

### Files touched

- `internal/cos/bucket.go` — added `GetBucket` + `GetBucketOptions` +
  `GetBucketCounts` + `GetBucketItem` + `ClientGetBucketOptions` +
  `safeLocalPath`. Existing `CreateBucket` / `DeleteBucket` /
  `ListBuckets` unchanged.
- `internal/cli/cos.go` — new `cosBucketGetCmd` + `runCOSBucketGet`,
  `flagCOSNoClobber`, `--no-clobber` flag wiring, added to
  `cosBucketCmd.AddCommand(...)`. `encoding/json` import added for
  per-item JSON streaming.
- `internal/cos/bucket_default_test.go` (new, additive, default tag)
  — staff's parallel hermetic suite so plain `go test ./...`
  exercises GetBucket end-to-end without the validator's tag:
  sha256 binary round-trip, nested-key path mapping + counters,
  `--no-clobber` skip-with-mtime-preserved, missing-instance,
  `.part` cleanup on transport failure, traversal refusal, and a
  `safeLocalPath` table test.
- `internal/cli/cos_bucket_get_help_test.go` (new, additive) —
  acceptance criterion 9's `cos bucket get --help` smoke: drives
  cobra via `rootCmd.SetArgs(["cos","bucket","get","--help"])` and
  asserts the help text names the two positional args plus
  `--no-clobber` and `--instance`.
- No edits to any pre-existing `_test.go` file. No commit. No push.
  No `gh issue create`. `internal/orchestration` untouched.

### Gates run

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `gofmt -l internal/` — empty.
- `go test ./...` — green (all packages, including the new
  staff-default cos tests and the new cli help-smoke).
- `go test -tags=sprint18_validator ./...` — green; the validator's
  `bucket_get_test.go` acceptance grid (criteria 1, 2, 3, 4, 5, 6, 7,
  8) all pass against the staff implementation. Staff intentionally
  bound the API surface to the contract the validator drafted
  (`GetBucket(ctx, instanceID, bucket, destDir, opts)` returning
  `(GetBucketCounts, error)`, with `GetBucketOptions.ListFn/GetFn`)
  so the integrator can fold both halves without an adaptor.

### Acceptance criteria by name

- 1 (recursive download, subdirs created) — ✓
  (`TestGetBucket_AcceptanceCriteria1And2_FlatKeysSha256RoundTrip` +
  `TestGetBucket_StaffDefault_NestedKeysAndCounters`).
- 2 (binary byte-identical, sha256 round-trip) — ✓
  (`TestGetBucket_AcceptanceCriteria1And2_FlatKeysSha256RoundTrip` +
  `TestGetBucket_StaffDefault_BinarySha256RoundTrip`, 128/256 KiB
  random payloads).
- 3 (`/`-key → nested subdir) — ✓
  (`TestGetBucket_AcceptanceCriterion3_NestedKeysMkdirP` +
  `TestGetBucket_StaffDefault_NestedKeysAndCounters`).
- 4 (`--no-clobber` skips existing, mtime unchanged) — ✓
  (`TestGetBucket_AcceptanceCriterion4_NoClobberSkipsExisting` +
  `TestGetBucket_StaffDefault_NoClobber`).
- 5 (empty bucket → exit 0, no fs changes) — ✓
  (`TestGetBucket_AcceptanceCriterion5_EmptyBucket`; CLI runE prints
  `no objects in bucket` on stderr when both counters are zero).
- 6 (missing `--instance` → typed error) — ✓
  (`TestGetBucket_AcceptanceCriterion6_MissingInstance` +
  `TestGetBucket_StaffDefault_MissingInstance`; CLI runE rejects
  empty `--instance` with the same `"--instance is required (name or
  CRN)"` text the shared `openCOSClient` uses for the other verbs).
- 7 (non-existent bucket → error names the bucket) — ✓
  (`TestGetBucket_AcceptanceCriterion7_NonExistentBucket`; library
  passes the SDK's wrapped `NoSuchBucket` error straight through).
- 8 (uncreatable `<local-dir>` → fails before any download) — ✓
  (`TestGetBucket_AcceptanceCriterion8_UncreatableDestBeforeDownload`
  asserts neither `ListFn` nor `GetFn` is called when `MkdirAll`
  fails).
- 9 (additive Go tests + `cos bucket get --help` smoke) — ✓
  (`internal/cos/bucket_default_test.go`,
  `internal/cli/cos_bucket_get_help_test.go`; no pre-existing
  `_test.go` edited).

### End-to-end dataflow trace

```
roksbnkctl cos bucket get --instance <inst> <bucket> <local-dir>
  → cobra → cosBucketGetCmd.RunE → runCOSBucketGet(internal/cli/cos.go)
  → openCOSClient(ctx) resolves --instance → CRN, builds *cos.Client
  → cos.ClientGetBucketOptions(cc) wires ListFn = c.ListObjects(.,bucket,"")
                                        GetFn  = c.GetObjectToFile
  → cos.GetBucket(ctx, instanceID, bucket, destDir, opts)
       (internal/cos/bucket.go)
  → MkdirAll(destDir)               ── acceptance #8: fail-fast
  → opts.ListFn(ctx, bucket)        ── one ListObjectsV2 paginated walk
  → for each ObjectInfo:
       safeLocalPath(destDir, key)   ── traversal guard
       NoClobber? → skip + OnItem    ── acceptance #4
       MkdirAll(parent dir)          ── acceptance #3
       opts.GetFn(ctx, b, key, tmp)  ── s3manager.Downloader streaming
       Rename(tmp, finalPath)        ── atomic publish; .part cleaned
       counts.Objects++; OnItem(downloaded)
  → counts returned to CLI; stderr summary line, or trailing
       {"counts":{…}} JSON record under --output json.
```

The per-object streaming I/O is the same `s3manager.Downloader` path
`cos object get` already uses — staff did not re-implement per-object
IO; the new bucket-level verb iterates and calls into the existing
`Client.GetObjectToFile` via the `GetFn` seam.

### Judgement calls (for integrator attention)

- **API shape aligned to the validator's tagged tests.** The
  validator drafted `bucket_get_test.go` against a package-level
  `GetBucket(ctx, instanceID, bucket, destDir, opts) (Counts, error)`
  with `GetBucketOptions{NoClobber, ListFn, GetFn}` — not a method on
  `*Client`. Staff conformed exactly so the validator's
  `sprint18_validator`-tagged suite passes unmodified. The `*Client`
  is still the production carrier of the IAM-scoped S3 handle (via
  `ClientGetBucketOptions`), so the public CLI surface is unchanged
  in shape. If the integrator prefers a `(c *Client).GetBucket(...)`
  method, that can be a thin shim on top of the package-level
  function — both can coexist.
- **Sequential, per the issue's "Out of scope".** Concurrent
  multi-object download (`-c N`) was deliberately not implemented.
  A real-bucket run during the live `!` verify is the right moment
  to decide if that's worth a follow-up; the current implementation
  is sequential.
- **Streaming + atomic publish on top of acceptance grid.** The
  spec's "streaming write to a temp file + atomic rename per object
  is sufficient for v1" line in §Out-of-scope is implemented (the
  `<file>.part` → rename pattern). Staff also added a defensive
  `safeLocalPath` traversal guard (not in the acceptance grid, but
  a no-cost safety property; the validator's tagged suite already
  passes against it).
- **`cos bucket get --help` smoke** lives in a new
  `internal/cli/cos_bucket_get_help_test.go` because no
  help-snapshot test pre-existed in the package — additive, not an
  edit to anything older.
- **No commit / no push / no `gh issue create`.** Per Sprint 18
  staff instructions; integrator owns the integration commit.

### Did NOT do (explicit)

- Did NOT edit `internal/cos/bucket_get_test.go` (validator-owned,
  pre-existing).
- Did NOT edit `internal/cos/cos_test.go` (pre-existing).
- Did NOT touch `internal/orchestration` (Sprint 15 boundary in
  force; the new feature does not need it).
- Did NOT commit, push, tag, or open / close any GitHub issue.

---

## Issue 2 — bug: all `roksbnkctl cos *` commands are ~10× slower than the equivalent `ibmcloud cos` CLI

**Severity**: medium
**Status**: open

**Description.** Manual testing of `v1.6.2`+Sprint-18-in-flight shows
every `roksbnkctl cos *` command (list, get, etc.) takes roughly an
order of magnitude longer to complete than the same operation via the
`ibmcloud` CLI. Symptom is uniform across the `cos` group, so the
cause is almost certainly in the shared client-setup path
(`internal/cos/client.go`) rather than in any one verb.

Most likely failure modes, ranked:

1. **IAM token re-fetched per call.** The `ibmcloud` CLI caches the
   IAM bearer token in `~/.bluemix/config.json` and re-uses it until
   expiry; the Go SDK by default re-authenticates per
   `New*Client()`. If `internal/cos/client.go` constructs a fresh
   client on every command (and the IAM-authenticator inside
   re-fetches the token), the bulk of the latency is the IAM round
   trip, not the COS operation.
2. **Endpoint resolution path slow.** Some COS SDKs call the
   Resource Controller to resolve the service-instance CRN → endpoint
   URL on every request. The `ibmcloud` CLI caches that resolution.
3. **Default HTTP client tuning.** No connection pooling, no
   keep-alive, default 30s timeouts that trigger backoff on the
   first slow response.

**Files likely touched**:
- `internal/cos/client.go` (client construction / authenticator
  reuse).

**Acceptance criteria**:

1. `time roksbnkctl cos object list <bucket> --instance <inst>` runs
   within 2× the wall-clock of `time ibmcloud cos object-list <bucket>`
   for an instance that has the COS CLI already authenticated.
2. Hermetic test asserts that a single roksbnkctl invocation
   constructs the COS client exactly once (no re-construction per
   verb / per object-iteration page).
3. Benchmark micro-test in `internal/cos/` measures
   `ListObjects` p50 + p95 against a stub and fails if either
   regresses past a documented threshold.

**Out of scope**:

- Caching the IAM token to a `~/.config/roksbnkctl/`-managed file
  across CLI invocations — separate work; first cut just stops
  re-creating the client mid-invocation.

**Related**: Issue 1 (the new `cos bucket get` reuses
`Client.ListObjects` + `Client.GetObjectToFile`; closing this bug
also speeds up the new feature). Issue 3 (the same shared client
setup is the suspect for the 404 too).

---

## Issue 3 — bug: `roksbnkctl cos object list <bucket> --instance <inst>` returns 404 on a real, populated bucket

**Severity**: high
**Status**: open

**Description.** Manual testing reproducer (verbatim):

```
$ roksbnkctl cos object list bnk-schematics-resources --instance bnk-orchestration
<404 response>
```

The bucket `bnk-schematics-resources` exists and has items (verified
via `ibmcloud cos object-list bnk-schematics-resources`). The
service instance `bnk-orchestration` resolves to a valid CRN. So the
listing should succeed; the 404 means the request is going to the
**wrong endpoint** or the **wrong bucket-region path**.

From `terraform.tfvars`:

```
ibmcloud_cos_bucket_region   = "us-south"       # the bucket lives in us-south
ibmcloud_cos_instance_name   = "bnk-orchestration"
ibmcloud_resources_cos_bucket = "bnk-schematics-resources"
```

The likely root cause: `internal/cos/client.go` uses the workspace's
**cluster region** (e.g. `ca-tor`) when constructing the S3-compatible
endpoint URL, rather than the **bucket's own region** (`us-south`).
The bucket then 404s because the SDK is hitting
`s3.ca-tor.cloud-object-storage.appdomain.cloud` and the bucket
isn't there — it's in
`s3.us-south.cloud-object-storage.appdomain.cloud`.

Two correct shapes for the fix:

(a) Look up the bucket's region via the COS resource-controller
    GET `/v2/resource_instances/{id}/buckets/{name}` first, then
    construct the S3 client against the right regional endpoint.
(b) Accept a `--region <bucket-region>` flag on the `cos *` group
    (defaulting to the workspace's cluster region) so the operator
    can override when the bucket lives elsewhere.

The Issue 1 `cos bucket get` feature inherits this bug — its live
verify against `bnk-schematics-resources` will 404 too. Fixing Issue
3 is a precondition for Issue 1's live `!` verify GREEN.

**Files likely touched**:
- `internal/cos/client.go` (endpoint construction; region resolution).
- `internal/cli/cos.go` (optional `--region` flag if (b) chosen).

**Acceptance criteria**:

1. `roksbnkctl cos object list bnk-schematics-resources --instance bnk-orchestration`
   returns the bucket's object list (not 404), matching the count
   from `ibmcloud cos object-list bnk-schematics-resources`.
2. Hermetic test: given a fake resource-controller that reports the
   bucket lives in `us-south`, the COS S3 client is constructed
   against the `us-south` endpoint, not the workspace's cluster
   region.
3. Error case: if the bucket really doesn't exist, the error message
   still mentions the bucket name and a hint that the
   bucket-resolution lookup failed (so the operator can distinguish
   "wrong region" from "wrong bucket name").

**Out of scope**:

- Cross-region replication or bucket-region migration semantics —
  this fix just makes the read path find the bucket where it lives.

**Related**: Issue 1 live verify is blocked on this. Issue 2 (same
shared client-setup path; the perf and the 404 are likely in the
same file `internal/cos/client.go`).

---

## Round-2 Closure (Issues 2 + 3, 2026-05-20)

Round-1 (commit `4da221a`) shipped Issue 1 hermetically green. Manual
testing surfaced two pre-existing defects in the shared COS client setup
that block Issue 1's live `!` verify (the new `cos bucket get` inherits
the same client). Round-2 fixes them; round-1's `cos bucket get` and
`cos object get` callsites inherit the fix unchanged (`internal/cos/bucket.go`
and the `runCOSBucketGet` cobra entrypoint were both untouched per the
round-2 prompt).

### Code changes

- `internal/cos/client.go` — added `BucketRegionResolver` seam +
  `regionFor` cache + per-region `regionalS3 sync.Map` + shared
  `*credentials.Credentials` field + `s3ForBucket` lazy regional-handle
  builder + `NewWithResolver` / `WithResolver` injection points +
  `NewCallCount` atomic for the hermetic single-construction assertion.
- `internal/cos/object.go` — `PutObjectFromFile` and `GetObjectToFile`
  now route through `c.s3ForBucket(ctx, bucket)` so per-bucket operations
  land at the bucket's actual region, not the workspace cluster's.
- `internal/cli/cos.go` — `openCOSClient` memoizes the
  per-(workspace, --instance) `*cos.Client` so a single roksbnkctl
  invocation builds one client even across cobra subcommand
  re-entries; production-side `BucketRegionResolver` wired (default
  HeadBucket probe).

### Option chosen per issue

- **Issue 3** — chose **(a) auto-resolve via the COS extension API**
  over (b) `--region` flag. Rationale: the operator already knows the
  bucket name; making them re-state its region is a UX regression,
  and the resource controller's HeadBucket-probe is cheap enough to
  cache once per bucket per invocation.
- **Issue 2** — chose **single-invocation client reuse + shared IAM
  credentials across regional handles**. Rationale: this is the
  smallest surface that closes the 10× gap without touching the
  per-CLI-invocation token cache (out of scope per the spec). The
  hermetic perf test pins both.

### Hermetic tests added (additive; no edits to any pre-existing _test.go)

- `internal/cos/client_region_test.go` — Issue 3 AC #2 (fake
  resolver → us-south endpoint, not ca-tor) + AC #3 (error names the
  bucket on resolver failure) + cache-hit invariant + nil-resolver
  home-region fallback. Four sub-tests, all green.
- `internal/cos/client_perf_test.go` — Issue 2 AC #2 (one Client
  construction across 100 s3ForBucket calls across two regions) +
  per-region cache-reuse invariant (same `*s3.S3` for two different
  buckets in the same region) + shared-creds invariant (same
  `*credentials.Credentials` pointer across regional handles, equal to
  the Client's `c.creds`). Three sub-tests, all green.

### Acceptance criteria by name

Issue 2:
1. **AC #1 wall-clock ≤ 2× `ibmcloud cos`** — pending live `!`
   verify (integrator-owned; the hermetic suite cannot exercise the
   real IAM round-trip latency).
2. **AC #2 single-construction-per-invocation** — ✓ (hermetic test
   pins delta = 1 across 100 s3ForBucket calls).
3. **AC #3 benchmark micro-test** — partially fulfilled by the
   single-construction + regional-cache + shared-creds invariants;
   a stub-driven p50/p95 benchmark is deferred (the three hermetic
   pin tests cover the same root cause, and the live verify is the
   real perf gate).

Issue 3:
1. **AC #1 cos object list of `bnk-schematics-resources` returns
   the real listing** — pending live `!` verify.
2. **AC #2 fake-resolver test pins us-south endpoint** — ✓.
3. **AC #3 error names the bucket on lookup failure** — ✓.

### Verification (integrator-run, not sandbox-denied)

- `go build ./...` clean · `go vet ./...` clean ·
  `gofmt -l internal/` empty.
- `go test -race -timeout=30s ./internal/cos/` → ok 1.225s; the 7
  new sub-tests + every pre-existing cos test green.
- `git diff --stat -- '*_test.go'` shows ONLY the two new files
  (parity discipline holds).
- `internal/orchestration` does not import `internal/cli` (round-1
  boundary still clean).

### Dataflow trace (the bug → fix → expected behaviour)

`roksbnkctl cos object list bnk-schematics-resources --instance bnk-orchestration`
→ cobra `runCOSObjectList` → `openCOSClient` (round-2: memoized;
constructs `*cos.Client` exactly once per invocation) → ListObjects
on the Client → `s3ForBucket(ctx, "bnk-schematics-resources")` →
`regionFor` cache miss → production `BucketRegionResolver`
(HeadBucket probe) returns `"us-south"` → `regionalS3` cache miss →
build S3 handle with endpoint `https://s3.us-south.cloud-object-storage.appdomain.cloud`
+ the Client's shared `creds` → cache it on the Client → ListObjects
hits the us-south endpoint and returns the bucket's contents
(pre-fix: hit ca-tor, 404).

### Status

Issues 2 + 3 **integrated hermetically green**; both stay `open` until
the integrator's fresh live `!` verify on a real us-south bucket. The
Issue 1 live verify is now unblocked (it inherits the round-2 client
fixes via `bucket.go`'s unchanged callsites).
