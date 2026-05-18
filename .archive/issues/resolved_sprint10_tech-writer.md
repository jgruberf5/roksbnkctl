# Sprint 10 — tech-writer issues (resolved)

Three passes: initial review (15 issues), pass-2 audit of remediation (4 net-new issues), pass-3 final verdict (closure of pass-2 Issue 16 + Issue 19, GREEN verdict). 17 resolved, 4 wontfix, 1 open (Issue 9 — cosmetic tabwriter alignment, post-tag polish).

## Issue 1: validator file missing (blocker, pass 1)
**Status**: resolved
**Resolution**: validator filed and completed live trusted-profile sandbox re-verify against `canada-roks`. All four exit conditions PASS; gate verdict GREEN.

## Issue 2: chapter 24 `TF source: embedded@v1.3.0` drift (high)
**Status**: resolved
**Resolution**: architect replaced five `embedded@v1.3.0` instances with verbatim binary outputs (`github`-type `<Repo>@<Ref>` for the four post-Sprint-8 shapes; `(unset)` for ShapeLegacySingle).

## Issue 3: `statusCmd.Long` doc drift (medium)
**Status**: resolved
**Resolution**: staff replaced the fourth bullet with two bullets (per-phase + Legacy fallback).

## Issue 4: chapter 19 §"4. Create or update the credential Secret" YAML drift (low)
**Status**: wontfix
**Resolution**: pre-existing drift from before Sprint 10; carried into v1.4 backlog.

## Issue 5: chapter 19 line 116 `last cred rotation` vs `secret: rotated` (low)
**Status**: wontfix
**Resolution**: pre-existing drift; carried into v1.4 backlog.

## Issue 6: chapter 19 retry-failure stderr text drift (medium)
**Status**: resolved
**Resolution**: architect rewrote prose to quote the wrap's actual prefix and document the `3 attempts × 20s backoff = ~40s` retry shape.

## Issue 7: chapter 24 cross-links to chapter 10/11 (low)
**Status**: resolved
**Resolution**: architect added cross-references in chapter 24.

## Issue 8: CHANGELOG four-vs-five framing (low)
**Status**: resolved
**Resolution**: architect reframed CHANGELOG intro to "four of the five Sprint-9-deferred polish issues."

## Issue 9: chapter 24 tabwriter cosmetic alignment (low)
**Status**: deferred to v1.4.x polish (open)
**Resolution**: cosmetic one-column offset between the tabwriter-padded header and the hardcoded 8-space `Cluster:` trailer. Not a stuck point — content matches per-line. Carry into v1.4 chapter-polish backlog.

## Issue 10: staff Issues 3 + 4 are accepted-not-resolved (low)
**Status**: resolved (informational)
**Resolution**: integrator-facing flag — staff's `Status: open (acceptable for v1.3.0)` lines are accepted-not-resolved per their explicit framing. Not blockers.

## Issue 11: PRD 04 §"Resolved in Sprint 10" companion section (low)
**Status**: wontfix
**Resolution**: architect deferred to v1.4 cycle per Sprint 10 PRD/PLAN-edit boundary. CHANGELOG carries the full chronology.

## Issue 12: chapter 24 dual `Cluster:` lines (low)
**Status**: resolved
**Resolution**: architect added a one-paragraph callout documenting the identity-vs-reachability dual use.

## Issue 13: CHANGELOG missing integration-test gate bullet (medium)
**Status**: resolved
**Resolution**: architect added the `### Changed` bullet covering `scripts/integration-test.sh`, kind-availability check, docker-daemon abort, and `SKIP_INTEGRATION_TEST=1` bypass.

## Issue 14: chapter 19 §"5" YAML env block expansion (low)
**Status**: wontfix
**Resolution**: architect deferred to v1.4 chapter-polish pass — not local, requires restructuring.

## Issue 15: chapter 24 intro paragraph framing (low)
**Status**: resolved
**Resolution**: architect updated the chapter 24 intro to open with `roksbnkctl status` before pivoting to kubectl-equivalent verbs.

## Issue 16: validator-evidence-body gating (pass 2 blocker)
**Status**: resolved
**Resolution**: validator's re-verify pass completed; live trace + GREEN gate verdict landed in `issue_sprint10_validator.md` §"Live trusted-profile verdict (re-verify pass)".

## Issue 17: stale `--trusted-profile-id` doc-comment at `internal/cli/ops_test.go:131` (pass 2 low)
**Status**: resolved
**Resolution**: staff rewrote the doc-comment to describe the current `--cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID"` shape. `grep -rn "trusted-profile-id" internal/cli/` empty.

## Issue 18: clean drift sweep (pass 2 informational)
**Status**: resolved
**Resolution**: cross-document drift on the new `--cr-token` + `--profile` wrap shape clean across all four user-visible surfaces.

## Issue 19: audience-iam acceptance unverified (pass 2 medium)
**Status**: resolved
**Resolution**: validator's re-verify confirmed `audience: iam` empirically accepted by IBM IAM. JWT decoded with `sub_type: ComputeResource`. No five-surface swap needed.
