# Sprint 10 — staff issues (resolved)

Two coordinated workstreams landed: PRD 04 runtime closure (in-pod `ibmcloud login` wrap with `--cr-token` + projected SA-token volume) and PRD 06 `status` per-phase integration. 8 resolved, 2 accepted-as-open for v1.3.0.

## Issue 1: Sprint 9 staff Issue 2 (in-pod ibmcloud login wrap) — closed
**Status**: resolved
**Resolution**: three coordinated edits landed. (1) `internal/exec/k8s_install.yaml` gained a projected SA-token volume (audience `iam`, expirationSeconds 3600, mountPath `/var/run/secrets/tokens`) and the `${IAM_PROFILE_ID_ENV_ENTRY}` placeholder. (2) `internal/exec/k8s.go::ibmcloudLoginWrapScript`'s trusted-profile branch invokes `ibmcloud login -a https://cloud.ibm.com --cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}" --quiet` with 3-attempt × 20s retry. (3) `internal/exec/k8s_test.go` asserts new flag shape + regression guard against `--trusted-profile-id` reappearing. Validator's live re-verify confirmed first-attempt success against `canada-roks` (JWT `grant_type: cr-token`, `sub_type: ComputeResource`).

## Issue 2: PRD 06 `status` per-shape deployment lines — landed
**Status**: resolved
**Resolution**: `runStatus` consumes `config.DetectShape` and emits per-phase lines for Empty/ClusterOnly/Split; LegacySingle preserves the v1.0.x `Last apply` line for script-compat plus a one-line shape callout. Four-shape table test in `internal/cli/inspect_test.go` against the Sprint 8 fixture set.

## Issue 3: in-wrap retry backoff fixed 20s × 3
**Status**: accepted for v1.3.0 (open)
**Resolution**: validator's live re-verify hit first-attempt success — retry pathology hasn't materialized. Acceptable for v1.3.0 per the explicit framing; revisit if a real environment exceeds the 60s OIDC propagation window. Carry into v1.4.x backlog.

## Issue 4: wrap script stderr interleaving on triple-fail
**Status**: accepted for v1.3.0 (open)
**Resolution**: validator's live re-verify hit first-attempt success — triple-fail path wasn't exercised live. Acceptable for v1.3.0; carry into v1.4.x polish list.

## Issue 5: Sprint 9 tech-writer Issues 4, 7, 8, 9, 13 (deferred polish) — architect surface
**Status**: resolved (architect-deliverable, closed in architect file)
**Resolution**: architect closed all five Sprint-9-deferred polish items in pass 1 (Issues 4, 5, 7, 8 → resolved; Issue 6 → wontfix). No staff-side action needed.

## Issue 6: smoke verify status — sprint 10 staff scope
**Status**: resolved
**Resolution**: all seven build/test gates green locally (`go build`, `go vet`, `gofmt`, `staticcheck`, integration build, four-shape status smoke).

## Issue 7: `t.Context()` Go 1.24+ — min-Go pinned correctly
**Status**: resolved
**Resolution**: project's `go.mod` directive is Go 1.25+; the new status test uses `t.Context()` without portability risk.

## Issue 8: tech-writer pass-2 Issue 3 — `statusCmd.Long` doc drift
**Status**: resolved
**Resolution**: replaced the v1.0.x `Last apply` bullet in `statusCmd.Long` (`internal/cli/inspect.go:34-44`) with two bullets (per-phase + Legacy fallback).

## Issue 10: tech-writer pass-2 Issue 17 — stale `--trusted-profile-id` doc-comment
**Status**: resolved
**Resolution**: rewrote the doc-comment above `TestDecodeOpsManifests_TrustedProfile_InjectsIAMProfileID` at `internal/cli/ops_test.go:128-135` to describe the new `--cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID"` shape. `grep -rn "trusted-profile-id" internal/cli/` empty.
