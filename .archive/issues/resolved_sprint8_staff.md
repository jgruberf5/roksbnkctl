# Sprint 8 — staff issues (resolved)

## Issue 1: pre-existing `internal/exec/` WIP fails tests + gofmt
**Status**: deferred — not Sprint 8 surface; user's pre-session uncommitted modifications
**Resolution**: the `M internal/cli/cluster.go`, `M internal/exec/docker.go`, `M internal/exec/k8s.go`, `M internal/exec/k8s_install.yaml` modifications pre-date Sprint 8 (visible in the git status at sprint kickoff). Validator's regression sweep correctly identifies them as carry-in failures, not Sprint 8 regressions.

**Action for the integrator**: surface to the user before tagging `v1.1.0`. Options:
- Roll the exec WIP into a `v1.0.3` patch release with test updates folded in, then tag `v1.1.0` from a clean tree (validator's recommendation).
- Or repair-and-fold into Sprint 8: update `internal/exec/docker_test.go` + `internal/exec/docker_terraform_test.go` to match the new ibmcloud-login wrap shape, switch the iperf3 image expectation to `networkstatic/iperf3:latest`, restore `PATH=/usr/local/bin` in the env-passthrough, `gofmt -w internal/exec/docker.go`.
- Or revert the exec WIP entirely if it was a stale draft.

This is a user decision; the integrator does not unilaterally touch the user's WIP. Sprint 8 integration commit covers ONLY the Sprint 8 surface; the exec WIP remains uncommitted on main for the user to triage.

## Issue 2: deferred composite happy-path unit coverage
**Status**: accepted (covered by live verification)
**Resolution**: as filed. The composite-dispatcher cells that would dispatch to terraform-exec aren't unit-testable without mocking `tf.Workspace.Plan/Apply/Destroy` — that's a larger refactor (e.g. a `tfWorkspace` interface) deferred to a future sprint. Coverage falls back to the validator's live cycle verification against the real `canada-roks` workspace (refusal contract preserved; v1.0.x byte-for-byte prompt copy preserved).

## Issue 3: deferred `cluster up` happy-path unit coverage on Empty/ClusterOnly/Split
**Status**: accepted (same rationale as Issue 2)
**Resolution**: rolls into the same `tfWorkspace` interface refactor. Refusal cell IS covered by `TestClusterUp_LegacySingleRefuses`; non-refusal cells need integration-level coverage.
