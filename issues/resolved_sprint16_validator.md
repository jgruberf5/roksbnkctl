# Sprint 16 — validator resolution log

## Issue 2 — closure deliverables → **integrated** (Issue 2 stays open, live-`!`-gated)

**Disposition: accepted, integrated as-is.**

**What landed.**

- _Hermetic regression test_ —
  `internal/tf/secondphase_handoff_test.go` (new, additive). Pins the
  cross-agent seam `tf.RenderTFVarsWithClusterOutputs`: cluster
  outputs with a `VPCID` → second-phase tfvars carry
  `use_existing_cluster_vpc = true` + `existing_cluster_vpc_id` +
  `create_roks_transit_gateway = false` + `testing_create_client_vpc =
  false`; no outputs → byte-identical create-path parity; empty
  `VPCID` → defensive create path; also asserts no `api_key` leak in
  the rendered body. `go test -race ./internal/tf/` green within the
  full suite (exit 0).
- _Gated live-verify driver_ — `scripts/e2e-phase-handoff.sh` (new):
  operator-run via `!` only, `set -euo pipefail`, `DRY_RUN=1` support,
  EXIT-trap self-teardown, real `up` (cluster then bnk phase) + the
  four Issue-2 assertions, never echoes/logs the API key or
  `./terraform.tfvars` contents. No `.github/workflows` change.
- `docs/E2E_TEST.md` §"Phase-handoff regression (Issue 2)" added
  (operator runbook + GREEN criteria).

**Integrator checks.** `bash -n scripts/e2e-phase-handoff.sh` clean;
header correctly marks real-spend / opt-in / not-CI; `git diff --stat
-- '*_test.go'` shows only the new file (parity gate intact — Sprint
14/15 guards byte-unchanged & green in the `-race` run).

**Status: Issue 2 remains `open — pending live \`!\` verify`.** Per
`live-verify-high-issues` + README decision 3, the `high`-severity
Issue 2 is **not** closed on the hermetic GREEN. The fix + the
regression test + the live driver are integrated; the live `!` run on a
real account and the final flip to `resolved` are integrator/
operator-owned and not done in this dispatch. The validator correctly
did not mark it resolved.

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

## 2026-05-20 — Issue 3 LIVE-VERIFIED GREEN (round-3 fix, folded into `v1.6.2`)

The round-2 fold-in of Issue 3 (point terraform at the canonical
snapshot) was caught broken by live verify run-id `20260519-220236`:
terraform rejected the snapshot's multi-section duplicate keys
(`Each argument may be set only once`), and the redacted-secret line
would have overridden `TF_VAR_ibmcloud_api_key` from env. Round-3
derives a deduped, secret-free `<phase state dir>/.applied-replay.tfvars`
at lifecycle-op time (canonical snapshot unchanged) and points
terraform at that. **Live-verified GREEN run-id `20260519-234554`**
(A1–A5 ✓ including A5 bare `plan -w e2e-handoff` succeeded via the
replay; cluster phase `72 added`; bnk phase `60 added`; two-phase
self-teardown ✓; canada-* residual check ✓; live recheck 0/0/0/0).
Issue 3 → `resolved`; v1.6.2 superset (Issue 2 + 3 + 4 all live-verified
GREEN). `live-verify-high-issues` discipline cycled twice for Issue 3
(round-2 RED → round-3 GREEN) — proving the rule for medium-severity
issues too once they touch the same lifecycle code path.
