---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: e2e drivers `e2e-test.sh` / `e2e-test-full.sh` lack the A5/A6 applied-tfvars replay + option-(b) gates that `e2e-phase-handoff.sh` ships'
labels: []
assignees: ''
---

## Symptom

The Sprint 16 Issue 3 round-3 fix
(`internal/orchestration/applied_replay.go::LayerAppliedTFVars` —
bare `roksbnkctl plan/apply/down -w <ws>` now replays the
`terraform.applied.tfvars` snapshot) is live-probed by **assertion
A5** in `scripts/e2e-phase-handoff.sh`. The Issue 3 option (b)
follow-up (actionable error when neither snapshot nor `--var-file`
is available) is live-probed by **assertion A6** in the same
driver. Both assertions live *only* in `e2e-phase-handoff.sh`. The
two other live drivers in the repo —
`scripts/e2e-test.sh` and `scripts/e2e-test-full.sh` — never run a
bare `roksbnkctl plan -w <ws>` or `roksbnkctl down -w <ws>` (they
always supply `--var-file "$TFVARS"`), so a future regression in
the applied-tfvars replay path or in the option-(b) gate passes
**every** non-Issue-2 live verify and only surfaces when an
operator types the bare form themselves.

This is the validator-prompt-named gap ("Issue 3 round-3 added A5/A6
to one driver; the other drivers may need analogous gates"). Today
the discipline is one-shaped: phase-handoff verify catches Issue 3
regressions, all the other live drivers don't.

`grep -n "plan -w \"\\$WORKSPACE\"" scripts/e2e-*.sh` shows two
occurrences, both inside `e2e-phase-handoff.sh` (A5 line 362, A6
line 261). Neither `e2e-test.sh` nor `e2e-test-full.sh` exercises
the bare-`-w` shape on any phase.

## Reproduction

```
# 1. take the round-3 fix offline on a throwaway branch — revert
#    internal/orchestration/applied_replay.go::LayerAppliedTFVars to
#    a no-op (returns "" with no replay).

# 2. run the existing e2e-test.sh driver against a real account:
IBMCLOUD_API_KEY=... ./scripts/e2e-test.sh
#    Result: GREEN. Phases A-H all pass. The regression is invisible —
#    every roksbnkctl invocation in the driver carries --var-file.

# 3. run the existing e2e-test-full.sh driver:
IBMCLOUD_API_KEY=... ./scripts/e2e-test-full.sh --teardown
#    Same outcome — GREEN. The Issue 3 regression is unobserved.

# 4. only e2e-phase-handoff.sh catches it:
IBMCLOUD_API_KEY=... ./scripts/e2e-phase-handoff.sh
#    A5 fails with the message the validator pinned in
#    issues/issue_sprint16_validator.md Issue 3:
#    "A5 bare \`plan -w $WORKSPACE\` (no --var-file) failed — Issue 3 NOT fixed"
```

## Expected behavior

The Issue 3 round-3 fix and its option-(b) follow-up are protected
by live assertions in **every** driver that exercises a workspace
post-`up`. Specifically:

- `e2e-test.sh` Phase C (post-cluster-up, pre-bnk-up) or Phase D
  (post-bnk-up) gains a bare `roksbnkctl plan -w "$WORKSPACE"` step
  with the same A5 contract: succeed AND emit the
  `Replaying applied tfvars from <path>` log line.
- `e2e-test-full.sh` either inherits it via the existing baseline-
  driver invocation or adds an explicit step in the post-up window.
- The option-(b) probe (A6 — pre-up bare `plan -w <ws>` returns the
  actionable error, not terraform's raw missing-var stack) gains an
  analogous step in `e2e-test.sh`'s very first phase (between `init`
  and the first `--var-file`-bearing apply).

A regression in `LayerAppliedTFVars` or in
`orchestration.RequireSnapshotOrVarFile` (`internal/orchestration/applied_replay.go`)
fails every live driver, not just one.

## Actual behavior

`e2e-test.sh` and `e2e-test-full.sh` will go green against a binary
where the Issue 3 fix has silently regressed, because they never
hit the regression-bearing code path. The `live-verify-high-issues`
discipline depends on operators running `e2e-phase-handoff.sh`
specifically — which is the *phase-handoff* driver, not the
applied-tfvars-replay driver. The two concerns live in the same
script for the historical reason that Issue 3 was filed and fixed
alongside Issue 2.

## Environment

- `roksbnkctl version`: N/A — this is e2e-driver coverage, not a
  binary behavior.
- OS / arch: Linux x86_64 (operator workstation).
- IBM Cloud region: ca-tor (the integrator's standing test region).
- Backend: local terraform — the e2e drivers' default.

## Suspect pipeline / hypotheses (optional)

1. **Most likely:** A5/A6 were added to `e2e-phase-handoff.sh`
   because that's where Issue 2's repro lived and the integrator
   was already wiring its assertions; the assertions are
   semantically about Issue 3 (the applied-tfvars replay) and would
   apply identically to any driver that runs a workspace through
   `up`. The split is historical, not principled.
2. **Second:** `e2e-test.sh` carries an explicit-`--var-file`
   convention in every step ("Phase D8 down" runs
   `down --auto -w "$WORKSPACE" --var-file "$TFVARS"`) that
   pre-dates the Issue 3 fix; the convention wasn't revisited when
   the bare-`-w` shape became a contract.

## Acceptance criteria

1. `scripts/e2e-test.sh` gains a new phase or sub-step (proposed:
   `Phase C5 bare plan -w` after the cluster phase comes up and
   before the bnk apply, OR `Phase D5 bare plan -w` post-bnk-up).
   The step runs `roksbnkctl plan -w "$WORKSPACE"` with **no**
   `--var-file`, asserts exit 0, and greps the run log for
   `Replaying applied tfvars from` — exactly the A5 contract from
   `e2e-phase-handoff.sh:362-368`.
2. `scripts/e2e-test.sh` gains an analogous A6-style step *between*
   `init` and the first `--var-file`-bearing apply: bare
   `roksbnkctl plan -w "$WORKSPACE"` is expected to **fail
   non-zero** with the actionable roksbnkctl-level error and the
   `--var-file` remedy hint — exactly the A6 contract from
   `e2e-phase-handoff.sh:253-272`.
3. `scripts/e2e-test-full.sh` inherits the new steps via its
   existing chain into `e2e-test.sh` (one-line change if the
   inheritance Just Works, or a parallel block if it doesn't).
4. Both new assertions are guarded by the same `DRY_RUN=1`
   convention every existing assertion uses — `DRY_RUN=1
   ./scripts/e2e-test.sh` prints the planned step and exits 0
   without an API call.
5. Run-id stamps in the driver's final summary line include the
   new assertion ids (so a future closure can grep "A5 ✓" / "A6 ✓"
   across drivers, not just the phase-handoff one).
6. Regression: a throwaway revert of
   `internal/orchestration/applied_replay.go::LayerAppliedTFVars`
   to no-op makes `e2e-test.sh` fail at the new step on a live run,
   not green. (Confirmed via the operator's normal pre-tag verify
   loop — the validator does not run live.)
7. The exact line-text of the new assertions matches the existing
   A5/A6 in `e2e-phase-handoff.sh` byte-for-byte where possible
   (lift via a shell helper into a new
   `scripts/lib/assert-applied-replay.sh` if both drivers can
   source it; otherwise duplicate with a comment naming the
   canonical site). One shape for one contract.

## Out of scope (deliberately)

- Adding A5/A6 to `scripts/e2e-test-backends.sh` — that driver
  focuses on backend-matrix coverage (local/docker/k8s/ssh) and
  runs against an already-up cluster; its concerns are orthogonal.
  File a follow-up if a backend-specific replay regression ever
  appears.
- Promoting the assertions into Go-level integration tests under
  `internal/cli/lifecycle_e2e_integration_test.go`. The cli-layer
  test catches hermetic regressions in the replay path; the live
  driver catches the bare-`-w` invocation shape against a real
  workspace. Different tier, different bug class — keep both.
- Refactoring the existing `e2e-test.sh` Phase numbering. The new
  sub-step slots into the existing layout; renumbering everything
  is a separate cleanup.
- Touching `e2e-phase-handoff.sh` — its A5/A6 are the canonical
  reference shape and must stay byte-identical (the Issue 2/3 GREEN
  evidence chain refers to them by name).

## Notes

- The exact A5 assertion shape from
  `scripts/e2e-phase-handoff.sh:362-368` (the reference site):
  bare `plan -w "$WORKSPACE"`, exit 0 required, log must contain
  `Replaying applied tfvars from`. Greppable, copy-paste-ready
  into the new driver step.
- The exact A6 assertion shape from
  `scripts/e2e-phase-handoff.sh:253-272` (the reference site):
  bare `plan -w "$WORKSPACE"` *pre-snapshot*, exit non-zero
  required, log must contain the actionable error string AND the
  `--var-file` remedy hint.
- This issue is filed under the validator-prompt's explicit hint:
  "Issue 3 round-3 added A5/A6 to one driver; the other drivers
  may need analogous gates." The bug is that they do, and they
  don't.
- Pairs naturally with the live-verify-runid pre-tag gate (issue
  03) — both live in the same family of discipline. The runid gate
  closes the *bookkeeping* side; this issue closes the *evidence
  coverage* side.
