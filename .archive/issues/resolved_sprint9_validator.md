# Sprint 9 — validator issues (resolved)

## Issues 1-3: blocker-class resolved mid-sprint
**Status**: resolved — closed during sprint
**Resolution**: flag wiring, k8s skip removal, CI/Makefile gate additions all landed mid-sprint via direct fixes against staff/architect/validator surfaces. No carry-over.

## Issue 4: live trusted-profile sandbox verification deferred
**Status**: deferred to integrator (sandbox-time gated)
**Resolution**: validator confirmed binary surface ready (`--trusted-profile=auto|on|off` flag works; bogus values rejected at PreRunE; default is `auto`; `internal/ibm/trusted_profile_test.go` covers perm-classification logic via httptest mock). End-to-end verification against a live ROKS cluster + IBM Cloud account is integrator-owned and gated on sandbox sandbox availability. Recommended procedure documented in the issue file. Not a v1.2.0 tag blocker — the unit + integration tests cover the code paths; the live verify is a post-tag confidence check.

## Issue 5: live refusal verification evidence
**Status**: resolved — informational log only
**Resolution**: as filed.

## Issue 6: optional e2e patch deferred
**Status**: deferred to Sprint 10
**Resolution**: as filed (low-priority per validator prompt). Tracked in PLAN.md deferred list.

## Issue 7: chapter 19 warning text drift (HIGH)
**Status**: resolved — chapter rewritten to match staff's actual three warning shapes
**Resolution**: applied during integration. Chapter 19's "`--trusted-profile=auto` falling back" subsection rewritten to mirror staff's `internal/cli/ops.go:272/293/305` warnings in a three-row table (cluster-missing / cluster-lookup-failed / iam-perm-missing) with the IAM-perm warning verbatim as the lead sample. The "ask your IAM admin to grant iam-identity Operator role" actionable moved from inside the stderr line (where it had been prescribed) into chapter prose where it belongs. Closes validator Issue 7 + architect Issue 1 together.
