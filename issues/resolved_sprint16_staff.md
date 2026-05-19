# Sprint 16 — staff resolution log

## Issue 2 — phase-handoff fix → **integrated** (live-`!`-gated for final close)

**Disposition: fix accepted, integrated as-is.** Reviewed independently
by the integrator; no rework needed.

**What landed.** Both halves of the existing-resource handoff:

- _Half A (terraform):_ root `use_existing_cluster_vpc` (bool, default
  `false`) + `existing_cluster_vpc_id` (string, default `""`) added to
  `terraform/variables.tf`, threaded root → `module "roks_cluster"`
  (`terraform/main.tf`) → wrapper (`roks_cluster/variables.tf`) →
  `module "cluster"` (`roks_cluster/main.tf`), reaching the
  pre-existing-but-unreachable `data.ibm_is_vpc.existing_cluster_vpc`
  count-toggle. Defaults keep the cluster phase byte-identical.
- _Half B (Go):_ additive `tf.RenderTFVarsWithClusterOutputs` /
  `Workspace.WriteTFVarsWithClusterOutputs`; new
  `internal/orchestration/second_phase_reuse.go`
  (`writeAndInitSecondPhase`) read via `internal/config`
  (`ReadClusterOutputs`). `RunTrialUp`/`RunApply` call it; the cluster
  phase keeps the unchanged `writeAndInit`/`WriteTFVars` seam.

**Transit-gateway decision — integrator-verified sound.** Staff chose
"second phase does not manage the TG" (`create_roks_transit_gateway =
false`) over adding a new existing-TG data branch. Integrator
independently confirmed this is safe: `terraform/modules/roks_cluster/
outputs.tf` `transit_gateway_name` is the **plan-time name variable**
(`var.roks_transit_gateway_name`), not the created-resource attribute,
so `module.testing`'s `data.ibm_tg_gateway.transit_gateway` name lookup
still resolves with `create_roks_transit_gateway = false`. Smaller
parity surface; accepted.

**`testing_client_vpc_name` not emitted — accepted.** ClusterOutputs /
`config.Workspace` carry no client-VPC name; the same user-tfvars/
default name flows in both phases, so flipping only
`testing_create_client_vpc = false` looks the existing client VPC up by
the correct name. No name guessing. Accepted.

**Verification (integrator-run, not sandbox-denied this session).**
`go build ./...` ✓ · `go vet ./...` ✓ · `gofmt -l internal/` empty ·
`go test -race ./...` → all 13 test packages `ok`, exit 0
(`internal/orchestration`, `internal/tf` incl. the new regression test
green) · `internal/orchestration` does not import `internal/cli`
(grep-clean) · no pre-existing `_test.go` edited
(`git diff --stat -- '*_test.go'` shows only the validator's new file).
Staff's session had `terraform validate` denied (documented Sprint
15/16 precedent); module wiring arity/type integrator-eyeballed +
dataflow traced end to end — sound.

**Status: integrated; final close gated on the live `!` verify** of
validator Issue 2 per `live-verify-high-issues` (a `high` issue is not
closed on hermetic green alone).

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

---

## 2026-05-19 — Issue 2 round-2 fix INTEGRATED (closure still gated on fresh live `!` verify)

Staff round-2 (`prompts/sprint16/followup2-issue2-staff.md`) delivered
the corrected, not-per-toggle architecture: `writeAndInitSecondPhase`
now writes a forced `state/bnk-phase-override.tfvars` (only when
`cluster-outputs.json` exists) — `create_roks_cluster=false` +
`roks_cluster_id_or_name` + `use_existing_cluster_vpc=true` +
`existing_cluster_vpc_id` + `create_roks_transit_gateway=false` + all
three `testing_create_*=false` — appended LAST to the plan/apply
var-file chain (`RunTrialUp`/`RunApply`). Symmetric with the
already-proven `cluster-phase-override.tfvars`. The second/bnk phase no
longer manages the cluster-shared network at all; no
cluster-outputs.json → nil → fresh/legacy path byte-identical (round-1
hermetic test stays green & unedited). Task B done: driver `teardown()`
now runs trial `down` THEN `cluster down` + a loud `canada-*` residual
assertion (Issue 4).

**Integrator actions.** Repointed driver assertion **A3**
(validator-owned, staff correctly did not touch) from
`state/terraform.tfvars` → `state/bnk-phase-override.tfvars`, now also
asserting `create_roks_cluster = false` (the real architectural
guarantee, not just the VPC toggle).

**Gates (integrator-run, NOT sandbox-denied):** `go build`/`vet` clean;
`gofmt -l internal/` empty; `internal/orchestration` ⊄ `internal/cli`;
`go test -race ./...` all packages `ok`; zero pre-existing `_test.go`
diffs (only the new additive `second_phase_reuse_test.go`);
`bash -n` clean; `DRY_RUN=1` driver shows A3 + the two-phase teardown +
residual check, exits 0, no key leak.

**Status: round-2 fix integrated; Issue 2 + Issue 4 remain `open`.**
Per `live-verify-high-issues`, closure (and any version tag) is gated
on a FRESH live `!` re-run of `scripts/e2e-phase-handoff.sh` — hermetic
GREEN is proven insufficient for this issue (round-1 precedent).

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
