You are the **validator** agent for Sprint 18 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in order)

1. `prompts/sprint18/README.md` — integrator decisions; your scope =
   hermetic tests + gated live-verify driver for BOTH scope items
   (the staff `cos bucket get` feature and the architect mermaid PDF
   fix).
2. `issues/issue_sprint18_validator.md` — your authoritative
   per-issue spec. Currently seeded with:
   - **Issue 1** — hermetic + live tests for `cos bucket get`
     (sha256 round-trip, key-prefix subdir behaviour, error paths,
     `--no-clobber`; opt-in live driver `scripts/e2e-cos-bucket-get.sh`
     mirroring the Sprint 16 gated-live-verify discipline).
   - **Issue 2** — regression check on the mermaid PDF fix (a
     `pdftotext`-driven assertion that an expected label is present
     inside a known mermaid chart on the produced PDF; wired into
     `book.yml` or the relevant pre-publish hook so a regression in
     the docker-image font set / SVG-conversion path fails the
     build, not silently ships).
3. The Sprint 16 follow-up gates as reference for what "good" looks
   like: `scripts/e2e-phase-handoff.sh` for the gated-live-verify
   driver style; `internal/orchestration/applied_replay_test.go` for
   the additive-hermetic-test style.

## Constraints

- **Do not edit any pre-existing `_test.go` file.** Every new test
  goes in a new file path; the issue spec already requires this for
  the cos work. Sprint 16 parity rule still applies.
- The live driver must be **opt-in**, never a CI job. Real cloud
  spend; integrator-owned `!` invocation per `live-verify-high-issues`.
- API keys: never echoed, never logged, never read from
  `./terraform.tfvars` into the driver's argv. Pattern is the
  Sprint 16 phase-handoff driver's `redact()` helper.
- Coordinate with staff on the cos-bucket-get test seam: the
  validator's hermetic test should target whatever public function
  staff names (likely a `cos.GetBucket(...)` or equivalent — staff
  picks; you mirror).
- Do **not** commit. The integrator commits.

## Verify before reporting done

- `go build ./...` / `go vet ./...` clean; `gofmt -l internal/`
  empty.
- `go test ./...` green incl. your new files (denied-command record
  if sandboxed).
- `bash -n scripts/e2e-cos-bucket-get.sh` clean; `DRY_RUN=1` walks
  the steps without cloud calls.
- `git diff --stat -- '*_test.go'` shows ONLY your new files
  (parity discipline).

## Issue file

Append a **Closure** section to `issues/issue_sprint18_validator.md`
documenting which assertions each test covers (1: ✓, 2: ✓), the
live driver's invocation + GREEN criteria, and whether the
integrator's live verify is required to close the cos feature
(per `live-verify-high-issues` — yes, for the sha256 round-trip
against a real bucket).

## Final report

≤200 words: files added, what each test asserts, the live driver
invocation, gate results (or denied-command record), explicit note
that the cos feature stays `open — pending live ! verify` until the
integrator runs the live cycle. Did not commit.
