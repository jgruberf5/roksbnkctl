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
