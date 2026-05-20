# Sprint 16 — architect resolution log

## Issue 2 — CHANGELOG/PLAN follow-up → **integrated**

**Disposition: accepted, integrated as-is.**

**What landed.**

- `CHANGELOG.md` — new `## v1.6.2 — 2026-05-19` section above
  `## v1.6.1`, `### Fixed` block (correctly not `### Changed`: `v1.6.1`
  was "no user-visible behavior change"; this is a user-facing fix).
  Describes `up` no longer failing with the IBM Cloud duplicate-name
  error; cross-links PLAN §"Sprint 16" and validator Issue 2.
- `docs/PLAN.md` — additive `### Follow-up (post-v1.6.1)` subsection in
  §"Sprint 16"; `git diff` confirms pure insertion (no existing text
  rewritten).

**Version tag — integrator-owned, deferred.** The `v1.6.2` heading is
written for the expected patch shape, but the tag is **not cut here**
and is gated on the live `!` verify of validator Issue 2
(`live-verify-high-issues`). No release tagged in this dispatch;
tagging is a separate integrator step after the live run.

**Integrator checks.** CHANGELOG markdown valid, dated, ordered above
`v1.6.1`, `### Fixed` used; PLAN note additive and cross-linked. Light
scope respected — only `CHANGELOG.md` + `docs/PLAN.md` touched.

**Status: integrated.** Docs-only follow-up; tracks the live-`!`-gated
validator Issue 2 (final close + tag are integrator/operator-owned).

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
