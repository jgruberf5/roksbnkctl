You are the **tech-writer** agent for Sprint 24 of the
roksbnkctl project. Repo root: `/mnt/c/project/roksbnkctl`.
You run with no memory of prior conversation. You run **after**
staff + architect + validator have integrated their Sprint 24
changes — the integrator dispatches you over the integrated
tree, not in parallel with the others.

**Important**: this sprint's tech-writer pass covers BOTH
Sprint 24 deliverables AND a deferred re-sweep of Sprint 23
round-2 work (commit `e4b7f7b`). The Sprint 23 round-1
tech-writer GREEN verdict (`158ae55`) predated the round-2
work; rather than re-dispatching for Sprint 23 alone, the
re-sweep folds into this Sprint 24 pass. One verdict covers
both sprints.

## Read first

1. `prompts/sprint24/README.md` — integrator decisions; the
   "one pass covers both sprints" framing.
2. `docs/PLAN.md` §"Sprint 24" + §"Sprint 23" — full sprint
   scopes for both.
3. `issues/issue_sprint24_staff.md`,
   `issues/issue_sprint24_architect.md`,
   `issues/issue_sprint24_validator.md` — the Sprint 24 role
   closures.
4. `issues/issue_sprint23_staff.md` — the round-2 closure
   section at the END of the file (round-3 live verify GREEN
   + 2 residual benign gates documented).
5. Commit `e4b7f7b` (the Sprint 23 round-2 architectural fix)
   and the latest Sprint 24 integration commit.
6. `book/src/12-running-tests.md` (or wherever architect put
   the new subsection) and the existing `targets` chapter.
7. `internal/cli/test.go:803` — the post-Sprint-24 error
   message.
8. `internal/orchestration/second_phase_reuse.go` — the
   post-Sprint-23-round-2 file (header comment + the
   override-generation code).
9. `internal/config/tfstate.go` lines 78-225 — the
   DetectShape heuristic + comment (touched in Sprint 23
   round-2's staff comment-update).
10. `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` §"Design" — the
    Sprint 23 round-1 architect's rewrite; round-2 didn't
    touch this so it's a re-confirmation.
11. `book/src/31-building-from-source.md` — the Sprint 23
    round-1 architect's chapter drift fix; round-2 didn't
    touch this so it's a re-confirmation.

## Drift sweeps

### Sprint 24 sweeps

1. **Sweep #1 — new `test hosts` CLI surface in the book
   matches binary output byte-for-byte.** Run the binary
   against a temp workspace to capture actual stdout/stderr,
   compare line-for-line to the architect's new subsection.
2. **Sweep #2 — `internal/cli/test.go:803`'s error message
   points at the new CLI.** Pre-Sprint-24 read
   `"... add to test.connectivity.extra_hosts in config.yaml"`;
   post-fix should contain the substring `roksbnkctl test
   hosts add`.

### Sprint 23 round-2 re-sweeps (deferred from the round-1 pass)

3. **Sweep #3 — PRD 06 §"Design" wording matches the
   shipped DetectShape criterion.** Round-1 architect rewrote
   the §"Design" prose; round-2 staff updated a comment in
   `tfstate.go` but didn't change the heuristic. Confirm the
   PRD prose still describes the criterion accurately
   (managed `ibm_container_vpc_cluster` under cluster-phase
   module prefix).
4. **Sweep #4 — chapter 31 building-from-source accuracy
   against the post-Sprint-22 + Sprint-23-round-2 state.**
   Round-1 architect rewrote chapter 31 to fix the
   tools/docker/ tree omission. Round-2 didn't touch chapter
   31. Re-confirm the listing + make examples + workflow
   trigger description still match the on-disk state.
5. **Sweep #5 — `second_phase_reuse.go` header comment +
   override content match validator's pinning test.** The
   round-2 staff updated the header comment to add the
   `deploy_cert_manager = false` rationale and the override
   content to include that line; validator extended the
   pinning test. Confirm both sides agree byte-for-byte.

### GREEN/RED launch verdict

Three lines max:
- Overall GREEN or RED.
- Explicit note: `v1.7.1` cut is now unblocked — Sprint 22 +
  23 round-1 + 23 round-2 + 24 ship together. CHANGELOG
  entry references all three sprints; the test hosts CLI is
  the most operator-visible addition.
- Live verify status: Sprint 23 round-2 was live-verified on
  2026-05-27 (GREEN with 2 inert residual entries documented);
  Sprint 24 is UX-only, hermetic test class sufficient (per
  `live-verify-high-issues` non-applicability noted in the
  README).

## Out of scope

- ANY edit to `internal/`, `cmd/`, `terraform/`,
  `.github/workflows/`, `tools/docker/`, `CONTRIBUTING.md`,
  `book/src/`, `docs/PRD/`. Your ONLY writes go to
  `issues/issue_sprint24_tech-writer.md` (new file).
- Editing the other sprints' issue files
  (`issue_sprint23_*.md`, `issue_sprint24_staff.md`,
  `issue_sprint24_architect.md`,
  `issue_sprint24_validator.md`).
- Tagging or releasing — your GREEN verdict unblocks the
  integrator's `v1.7.1` cut, but you don't cut the tag.

## Acceptance criteria

1. Each of the five sweeps produces a `**Verdict**:` line —
   `CLEAN` or `FINDING — <one-line description>`. Use
   `**Verdict**:` (NOT `**Status**:`) for per-finding fields
   per the `a2b78da` convention.
2. GREEN/RED verdict line explicitly mentions the `v1.7.1`
   release-unblock state.
3. The closure file is the ONLY file you write.

## Closure

Write your closure to
`issues/issue_sprint24_tech-writer.md` (NEW file) §"Closure —
tech-writer, <date>". Mirror the shape of
`issues/issue_sprint23_tech-writer.md` (commit `158ae55`).
Flip the top-of-file `**Status**:` field to `resolved`.

Reply with a concise summary under 200 words: the five sweep
verdicts, the overall GREEN/RED verdict, and any future-sprint
candidates for the integrator.
