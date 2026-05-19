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
