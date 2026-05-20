You are the **staff engineer** agent for Sprint 18 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in order)

1. `prompts/sprint18/README.md` — integrator decisions; especially
   §"Four-agent dispatch" (your scope = the `cos bucket get` feature)
   and integrator decision 3 (`live-verify-high-issues` — closure is
   gated on the live verify the validator builds; you draft the
   feature so the validator's hermetic test + live driver can both
   exercise it).
2. `issues/issue_sprint18_staff.md` Issue 1 — the **authoritative
   spec** for what to build. Every acceptance criterion in that file
   is binding; if you find one ambiguous, note it in your closure but
   do not relax the spec yourself.
3. The existing `cos` command surface for shape mirroring:
   `internal/cos/{bucket,object,client}.go` (the SDK plumbing),
   `internal/cli/cos.go` (the cobra commands —
   `cos bucket {create,list,delete}` already exist; you are adding
   `cos bucket get`).
4. `internal/cos/object.go` — `cos object get` is the existing
   *single*-object streaming download; the new bucket-level verb
   should iterate-and-call into the same per-object streaming path
   rather than re-implement it.

## Constraints

- **Do not edit any pre-existing `_test.go` file.** Additive new
  tests are welcome (and the issue's acceptance criteria require
  them).
- `internal/orchestration` must not import `internal/cli` (sprint-15
  boundary still in force; you shouldn't need to touch
  `internal/orchestration` for this feature anyway).
- Do **not** commit. The integrator commits. Do not push.
- Do **not** run `gh issue create` (there are no GitHub issues — the
  work is in the local ledger).

## Verify before reporting done

- `go build ./...` / `go vet ./...` clean.
- `gofmt -l internal/` empty.
- `go test ./...` green — including your new additive test(s) (if
  `go test` is sandbox-denied, record the exact denied command in
  your closure; do not fabricate results — Sprint 15/16 precedent).
- Trace the dataflow end-to-end in your closure: how a `cos bucket
  get --instance <inst> <bucket> <local-dir>` invocation flows from
  the cobra command → the new `internal/cos/bucket.go` recursive
  download → per-object streaming via the existing `object.go`
  helper → the on-disk subdirs.

## Issue file

Append a **Closure** section to `issues/issue_sprint18_staff.md`
(the file is pre-seeded with the spec; do not delete anything).
Schema: what you implemented, files touched, gates run + their
results (or denied-command record), the acceptance-criteria-by-name
checklist (1: ✓, 2: ✓, …), notable judgement calls (e.g. whether
you implemented concurrent download or kept it sequential per the
issue's §"Out of scope").

## Final report

≤200 words: files touched, the acceptance-criteria-by-name pass
list, test results (or denied-command record), one-line dataflow
trace, any judgement-call worth integrator attention. State
explicitly you did not commit.
