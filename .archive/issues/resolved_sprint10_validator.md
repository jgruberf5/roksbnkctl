# Sprint 10 — validator issues (resolved)

Three passes: initial regression sweep + blocker discovery, live re-verify after staff/architect remediation, final pre-tag regression sweep. 8 resolved, 0 open at sprint close. Gate verdict: **GREEN** for `v1.3.0` tag.

## Issue 1: in-pod login wrap used non-existent `--trusted-profile-id` flag (blocker)
**Status**: resolved
**Resolution**: staff landed the three-edit fix per validator's proposed-fix patch. Live re-verify against `canada-roks` sandbox: `oauth-tokens` returned a Bearer token on first attempt (no retries fired), JWT decoded with `grant_type: cr-token`, `sub_type: ComputeResource`. `audience: iam` empirically accepted by IBM IAM.

## Issue 2: chapter 19 `ops show` profile NAME vs ID
**Status**: resolved
**Resolution**: architect updated chapter 19 lines 195, 209, 316 to the `Profile-<uuid>` shape with audit-trail rationale.

## Issue 3: chapter 24 LegacySingle `(<age> ago)` suffix
**Status**: resolved
**Resolution**: architect added the suffix to the chapter 24 LegacySingle sample at line 98.

## Issue 4: `scripts/integration-test.sh` preflight-fail trap polish
**Status**: resolved
**Resolution**: trap relocated inside `main()` after `bring_up_kind` succeeds. `sh -n` clean; preflight-exit on kind-less host no longer prints "deleting kind cluster" line.

## Issue 5: regression sweep — all seven steps green
**Status**: resolved (informational)
**Resolution**: Go 1.26.3 host; all seven gate steps green across passes 1 and 3. Pass 2 (re-verify) didn't re-run regression — preconditions confirmed via spot-check.

## Issue 6: local-gate hardening (option a) — landed cleanly
**Status**: resolved (informational)
**Resolution**: `make release` step count renumbered to [1/8]→[8/8] with new integration-test step. Option (a) per PLAN.md §"Sprint 10 → Code deliverable 3": kind-availability check + confirmation prompt + `SKIP_INTEGRATION_TEST=1` bypass.

## Issue 7: live re-verify trace (post-staff blocker fix)
**Status**: resolved
**Resolution**: full evidence body in `issue_sprint10_validator.md` §"Live trusted-profile verdict (re-verify pass)". All four exit conditions PASS; `--trusted-profile=off` regression also PASS (JWT `grant_type: apikey`, v1.0.x path preserved).

## Issue 8: final pre-tag regression sweep (pass 3)
**Status**: resolved
**Resolution**: 15 files / 806+/69− working-tree footprint matches Sprint 10 plan. `grep -rn "trusted-profile-id"` returns only legitimate hits (v1.2.0 CHANGELOG history + regression-guard assertions). All four issue files at `Status: resolved` / `wontfix` / `accepted`. Cleared for tag.
