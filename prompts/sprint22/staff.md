You are the **staff** agent for Sprint 22 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Important: this is a closure-only prompt

Both staff Issues for this sprint **already shipped** before
dispatch — fixed in-conversation by the integrator on 2026-05-27
during a demo.sh re-verify cycle. Your scope is **read-only
audit + closure documentation**, not active work. Do NOT
edit any Go code or test files. Mirrors inversely Sprint 20's
"no architect deliverables (release-tooling-only)" precedent.

## Read first

1. `prompts/sprint22/README.md` — integrator decisions; the
   three-deliverable-stream framing.
2. `issues/issue_sprint22_staff.md` — already filed; both
   Issues documented in its "Fix as shipped" section.
3. Commits `18415eb` (down-prompt composite UX) and `cbb9c1b`
   (DetectShape correctness) — the diffs you're auditing.
4. `internal/orchestration/lifecycle.go` `RunDown` Split branch
   — the in-flight Auto-flip mechanism.
5. `internal/cli/lifecycle.go` `lifecycleInputs()` — the
   closure that mirrors `in.Auto` onto `flagAuto` for the
   cli-resident `runClusterDown` to see.
6. `internal/config/tfstate.go` `trialStateHasClusterModules` —
   the strengthened heuristic (managed + ibm_container_vpc_cluster
   + cluster-phase-prefix match).
7. `internal/config/testdata/tfstate_split_data_in_trial.json`
   — new fixture for the post-`up` Split shape.

## Tasks (read-only audit)

1. **Confirm the diffs match the issue file's claims.** Run
   `git show 18415eb -- internal/orchestration/lifecycle.go
   internal/cli/lifecycle.go book/src/11-tearing-down.md` and
   `git show cbb9c1b -- internal/config/tfstate.go
   internal/config/tfstate_test.go
   internal/config/testdata/tfstate_split_data_in_trial.json`.
   Verify the actual code changes match what
   `issues/issue_sprint22_staff.md` "Fix as shipped" describes.
2. **Confirm tests pass on current `main`.** Run
   `go test ./internal/config/... ./internal/orchestration/...
   ./internal/cli/...`. Should be green.
3. **Confirm `go vet ./...` is clean.**
4. **Audit the live-verify gate.** Confirm that
   `issues/issue_sprint22_staff.md` accurately notes that the
   DetectShape fix's live verify is GATED on Sprint 23 (per
   `issues/issue_sprint23_staff.md`). If the note is missing
   or unclear, edit the issue file to make the gate explicit
   (this is the one allowed edit — a clarification, not a
   code change).
5. **Do NOT relitigate the fix shape.** If your audit raises
   "I would have done it differently" questions, document
   them in the closure section as future-sprint candidates,
   not as Sprint 22 follow-ups.

## Out of scope

- ANY edit to Go code, test code, fixtures, or HCL. The fixes
  shipped and must not be touched.
- `.github/workflows/`, `CONTRIBUTING.md`, `book/src/` —
  validator/architect/tech-writer territory.
- `issues/issue_sprint23_staff.md`, `issues/issue_sprint24_staff.md`
  — forward-looking placeholders the integrator already filed;
  do not edit.

## Acceptance criteria

1. `go test ./...` and `go vet ./...` are green at current `main`.
2. The diffs match the issue file's claims (no drift between
   what the issue says shipped and what actually shipped).
3. The live-verify gate to Sprint 23 is explicit in
   `issues/issue_sprint22_staff.md`.

## Closure

Write your closure to
`issues/issue_sprint22_staff.md` §"Closure — staff, <date>"
appended at the end of the existing file. Include: the two
commit SHAs (`18415eb`, `cbb9c1b`), the `go test` + `go vet`
results, the audit verdict (claims match diffs / drift found
+ what), and any future-sprint candidates the audit raised
(do NOT file new sprints — just list them for the integrator).
The top-of-file `**Status**:` field is already set to
`resolved` and should NOT be flipped further.
