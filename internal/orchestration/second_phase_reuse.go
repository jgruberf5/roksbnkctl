package orchestration

// Second-phase cluster-shared-infra handoff (issues/issue_sprint16_validator.md
// Issue 2 — corrected, round 2). The `up` flow applies the SAME root
// terraform (module.roks_cluster + module.testing + the bnk-layer
// modules) across two independent state files. The cluster phase
// (state-cluster/) forces deploy_bnk=false and creates the entire
// cluster-shared network: the cluster VPC, the cluster subnets + public
// gateways, the transit gateway, AND module.testing's client VPC /
// jumphost subnets / jumphost SG. The second (bnk/testing) phase runs
// the same root against its own near-empty state/ with deploy_bnk=true —
// but create_roks_cluster and the testing_create_* toggles are still on,
// so it re-PLANS every cluster-shared resource the cluster phase already
// built, and IBM Cloud rejects the duplicate names.
//
// Round 1 (commit 27f7a02) tried per-resource "use existing" toggles
// (use_existing_cluster_vpc / create_roks_transit_gateway=false /
// testing_create_client_vpc=false). The live `!` verify (run-id
// 20260519-181511) proved that wrong: the VPC reuse worked but the
// second phase still re-created the cluster subnets, the cluster public
// gateways, the transit gateway, the client VPC, the jumphost subnets,
// and the jumphost SG. Chasing a growing list of per-resource flags
// across two modules is the wrong model.
//
// The corrected model is architectural and symmetric with the existing,
// live-proven cluster-phase-override.tfvars mechanism
// (internal/cli/cluster_phase.go): just as the cluster phase has a
// FORCED override that turns the bnk-layer OFF (deploy_bnk=false), the
// second/bnk phase gets a FORCED override that turns ALL cluster-shared
// creation OFF when this workspace already has a cluster-outputs.json
// (i.e. the cluster phase has completed). With that override:
//
//   create_roks_cluster              = false
//     → module.roks_cluster.module.cluster resolves the cluster by name
//       via data.ibm_container_vpc_cluster.existing_cluster (count flips
//       create→data); ZERO cluster subnet / public-gateway / cluster
//       creates; null_resource.cluster_ready count→0.
//   roks_cluster_id_or_name          = "<cluster-outputs.json id/name>"
//     → identity for that existing-cluster data lookup + the downstream
//       roks_cluster_name output (already create_roks_cluster-aware).
//   use_existing_cluster_vpc         = true
//   existing_cluster_vpc_id          = "<cluster-outputs.json vpc_id>"
//     → ibm_is_vpc.cluster_vpc[0] (count = use_existing_cluster_vpc?0:1,
//       NOT gated by create_cluster) flips to
//       data.ibm_is_vpc.existing_cluster_vpc[0]. This is the one round-1
//       piece that genuinely works and is still needed here.
//   create_roks_transit_gateway      = false
//     → ibm_tg_gateway.transit_gateway[0] count→0 (cluster phase already
//       created + connected it).
//   testing_create_cluster_jumphosts = false
//   testing_create_tgw_jumphost      = false
//   testing_create_client_vpc        = false
//     → module.testing plans NO client VPC, NO jumphost subnets, NO
//       jumphost SG, NO cluster-jumphost subnets/SG. Those are
//       cluster-shared singletons the cluster phase already created.
//
// Net: the second/bnk phase plan contains the bnk-layer modules
// (cert_manager / flo / cne_instance / license) + existing-cluster DATA
// lookups ONLY — no module.roks_cluster / module.testing cluster-shared
// CREATE at all. Not per-toggle whack-a-mole: the second phase no longer
// MANAGES cluster-shared infra.
//
// Scope guard: this override is ONLY layered by the second/trial phase
// (RunTrialUp / RunApply) and ONLY when cluster-outputs.json exists. A
// fresh/empty or legacy-single-state workspace has no cluster-outputs.json
// → no override → the create path is byte-identical to before (validator
// Issue 1's parity gate stays GREEN; cluster-only and bnk-only sub-flows
// unchanged). config.ReadClusterOutputs is read through internal/config
// exactly the way internal/cli/cluster_phase.go writes it, so
// internal/orchestration never imports internal/cli (the one-directional
// boundary asserted in Sprint 16).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// bnkPhaseOverrideFile is the second/bnk-phase forced override tfvars,
// written into the trial state dir and appended LAST to the var-file
// chain so it wins over config.yaml-derived tfvars and any user
// terraform.tfvars.user — exactly the precedence
// cluster-phase-override.tfvars uses for the cluster phase.
const bnkPhaseOverrideFile = "bnk-phase-override.tfvars"

// writeAndInitSecondPhase is the second/trial-phase preamble. It renders
// the normal terraform.tfvars (unchanged create-path render — the round-1
// per-toggle renderer is gone), then, when this workspace already has a
// cluster-outputs.json (the cluster phase completed → we are the SECOND
// phase), writes a bnk-phase-override.tfvars that forces ALL
// cluster-shared creation off and returns its path so the caller appends
// it to the plan/apply var-file chain.
//
// Returns (extraVarFiles, error). extraVarFiles is non-empty ONLY when a
// usable cluster-outputs.json (with a vpc_id) exists; otherwise it is nil
// and the run is byte-identical to the pre-Issue-2 second-phase flow
// (which is itself identical to the create path).
func writeAndInitSecondPhase(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace, workspace string) ([]string, error) {
	if err := tfws.WriteTFVars(ws); err != nil {
		return nil, fmt.Errorf("writing tfvars: %w", err)
	}
	if tfws.HasUserTFVars() {
		fmt.Fprintf(os.Stderr, "→ Layering user tfvars from %s (overrides config.yaml-derived values)\n", tfws.UserTFVarsPath())
	}

	co, err := loadReuseClusterOutputs(workspace)
	if err != nil {
		return nil, err
	}

	var extra []string
	if co != nil && co.VPCID != "" {
		overridePath, werr := writeBnkPhaseOverride(tfws, co)
		if werr != nil {
			return nil, werr
		}
		extra = []string{overridePath}
		fmt.Fprintf(os.Stderr,
			"→ Second-phase handoff: cluster-outputs.json present — forcing cluster-shared infra OFF "+
				"(create_roks_cluster=false, reuse cluster VPC %s + transit gateway + jumphosts). "+
				"The bnk phase deploys only the BNK trial layer onto the existing cluster.\n",
			co.VPCID)
	}

	fmt.Fprintln(os.Stderr, "→ terraform init")
	if err := tfws.Init(ctx); err != nil {
		return nil, err
	}
	return extra, nil
}

// clusterIdentity returns the value to feed roks_cluster_id_or_name for
// the existing-cluster data lookup: the cluster ID if recorded, else the
// cluster name (data.ibm_container_vpc_cluster.existing_cluster accepts
// either as `name`). Empty only if cluster-outputs.json is degenerate,
// in which case the caller has already gated on VPCID != "".
func clusterIdentity(co *config.ClusterOutputs) string {
	if co.ClusterID != "" {
		return co.ClusterID
	}
	return co.ClusterName
}

// writeBnkPhaseOverride writes the forced bnk-phase override tfvars into
// the trial state dir and returns its path. The content turns OFF every
// cluster-shared CREATE so module.roks_cluster + module.testing resolve
// the cluster identity by data source instead of re-provisioning the
// network the cluster phase already built. No api_key is ever written
// (it flows via TF_VAR_ibmcloud_api_key, same as every other path).
func writeBnkPhaseOverride(tfws *tf.Workspace, co *config.ClusterOutputs) (string, error) {
	return writeBnkPhaseOverrideAt(tfws.StateDir(), co)
}

// writeBnkPhaseOverrideAt is the pure (dir-only) core of
// writeBnkPhaseOverride — production passes the trial state dir; the
// additive regression test passes a temp dir so it can assert the exact
// forced content without standing up a full terraform workspace.
func writeBnkPhaseOverrideAt(stateDir string, co *config.ClusterOutputs) (string, error) {
	overridePath := filepath.Join(stateDir, bnkPhaseOverrideFile)
	content := fmt.Sprintf(`# Generated by roksbnkctl. Do not edit by hand.
# Second/bnk-phase override (issues/issue_sprint16_validator.md Issue 2,
# round 2). cluster-outputs.json exists, so the cluster phase already
# created the ENTIRE cluster-shared network (cluster VPC + subnets +
# public gateways + transit gateway + the testing client VPC / jumphost
# subnets / jumphost SG). This phase must NOT manage any of it — it
# deploys only the BNK trial layer onto the already-provisioned cluster,
# consuming the cluster identity from cluster-outputs.json via the
# existing-cluster terraform data sources. Forced (wins over config.yaml
# tfvars + terraform.tfvars.user), symmetric with cluster-phase-override.
create_roks_cluster = false
roks_cluster_id_or_name = %q
use_existing_cluster_vpc = true
existing_cluster_vpc_id = %q
create_roks_transit_gateway = false
testing_create_cluster_jumphosts = false
testing_create_tgw_jumphost = false
testing_create_client_vpc = false
`, clusterIdentity(co), co.VPCID)
	if err := os.WriteFile(overridePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing bnk-phase override: %w", err)
	}
	return overridePath, nil
}

// loadReuseClusterOutputs returns the workspace's ClusterOutputs when a
// cluster-outputs.json exists (the cluster phase completed → we are the
// SECOND phase reusing it), or (nil, nil) when there is none (first/
// cluster phase, a fresh workspace, or a legacy single-state workspace —
// caller renders/plans the create path unchanged). A genuine read/parse
// error is surfaced so the user sees a corrupt file rather than silently
// planning a duplicate create.
func loadReuseClusterOutputs(workspace string) (*config.ClusterOutputs, error) {
	co, err := config.ReadClusterOutputs(workspace)
	if err != nil {
		if err == config.ErrClusterOutputsMissing {
			return nil, nil
		}
		return nil, fmt.Errorf("reading cluster-outputs.json for second-phase handoff: %w", err)
	}
	return co, nil
}
