# Sprint 16 — tech-writer resolution log

## Issue 2 follow-up — doc/example review → **integrated** (both findings accepted as-is)

Tech-writer verdict: **GREEN** — CHANGELOG `### Fixed`, PLAN follow-up,
and `docs/E2E_TEST.md` §"Phase-handoff regression" accurately describe
the fix/driver and do not imply Issue 2 is verified/resolved (closure
explicitly live-`!`-gated). No API-key / `./terraform.tfvars` leak in
any doc, script comment, example, or test.

**Finding 1 (low) — cross-links use bare-file + `§"Sprint 16"` labels,
not heading-slug anchors.** _Disposition: accepted, no change._ All
targets resolve and this matches the repo's established CHANGELOG
linking convention; changing it would be drift *away* from convention.

**Finding 2 (low) — CHANGELOG could let a code-reader assume
`create_roks_transit_gateway` is a new symmetric passthrough.**
_Disposition: accepted, no change._ It is pre-existing; TG reuse works
via `module.testing`'s by-name `data.ibm_tg_gateway` lookup. The
authoritative `tf.RenderTFVarsWithClusterOutputs` doc-comment and the
Issue 2 closure already state this precisely; CHANGELOG is intentionally
user-facing (no module-internal detail) — overspecifying it would hurt
the user-facing tone the architect prompt required. No reader of the
authoritative surfaces is misled.

**Status: integrated.** Read-only review; tech-writer touched only
`issues/issue_sprint16_tech-writer.md`. No doc edits required — GREEN
with both lows accepted. (GREEN = docs sound; it does **not** close
Issue 2 — that remains live-`!`-gated.)

---

## 2026-05-19 — SUPERSEDED by live `!` verify RED (Issue 2 reopened)

The Issue 2 disposition above is **superseded**. The live `!` verify
(run-id `20260519-181511`) came back **RED**: the first fix attempt
(`27f7a02`) is necessary-but-insufficient — the second/bnk phase
re-creates the *entire* cluster-shared network (cluster subnets +
public gateways + transit gateway + testing client VPC + jumphost
subnets/SG), not just the cluster VPC. The `v1.6.2` CHANGELOG
`### Fixed` claim was reverted; no tag cut. Issue 2 is reopened &
expanded and staff re-dispatched for the corrected (not-per-toggle)
fix. See `issues/issue_sprint16_validator.md` §"Issue 2 — live `!`
verify result: RED — reopened & expanded" and new Issue 4
(e2e-driver teardown strands the cluster phase).

## 2026-05-19 — Issue 2 LIVE-VERIFIED GREEN (round-2 fix, `v1.6.2` cut)

Integrator re-ran `scripts/e2e-phase-handoff.sh` against a real account
with the round-2 binary. **Run-id `20260519-202202` GREEN** —
cluster phase `Apply complete! Resources: 72 added`; bnk phase
`Apply complete! Resources: 60 added` (the apply that failed in every
prior run); A1–A4 ✓; two-phase self-teardown ✓; live recheck
cluster/VPCs/TGW/COS = 0/0/0/0. Issue 2 + Issue 4 → `resolved`.
`v1.6.2` CHANGELOG `### Fixed` reinstated (revised for the round-2
architecture) and tagged. See `issues/issue_sprint16_validator.md`
§"Issue 2 — live `!` verify result: GREEN — RESOLVED".
