package orchestration

// Second-phase existing-resource handoff (issues/issue_sprint16_validator.md
// Issue 2). The `up` flow applies the same roks_cluster/testing terraform
// across two independent state files. The cluster phase (state-cluster/)
// creates the cluster VPC, the transit gateway, and the client VPC and
// tracks them. The second (bnk/testing) phase runs the same modules
// against its own state/ — without a handoff it plans to *create* those
// same-named resources and IBM Cloud rejects the duplicates
// (CreateVPCWithContext "is not unique" / "A gateway with the same name
// already exists").
//
// The reuse plumbing already exists in the terraform tree
// (use_existing_cluster_vpc / existing_cluster_vpc_id +
// data.ibm_is_vpc.existing_cluster_vpc; testing_create_client_vpc=false +
// data.ibm_is_vpc.existing_client_vpc; data.ibm_tg_gateway.transit_gateway
// for the client-VPC TGW connection) and cluster-outputs.json already
// records vpc_id. This file closes the Go half: when the second phase
// runs against a workspace that already has a cluster-outputs.json, it
// re-renders terraform.tfvars through tf.RenderTFVarsWithClusterOutputs
// so the reuse toggles are present (README decision 5 — wiring, not new
// design).
//
// Scope guard: this is ONLY invoked by the second/trial phase
// (RunTrialUp / RunApply). The first/cluster phase (cli runClusterUp,
// which has no cluster-outputs.json yet on a fresh create) is byte-
// identical — it keeps using the unchanged writeAndInit / WriteTFVars
// path. tf.RenderTFVars / WriteTFVars signatures are untouched (the
// frozen internal/tf/vars_test.go pins them); the new renderer is purely
// additive and falls back to the create path when there is no usable
// cluster-outputs.json.

import (
	"context"
	"fmt"
	"os"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// writeAndInitSecondPhase is the second/trial-phase preamble: it renders
// terraform.tfvars with the existing-resource reuse toggles when this
// workspace already has a cluster-outputs.json, then runs terraform init.
//
// It mirrors writeAndInit exactly (same user-tfvars cue, same init call)
// except for the tfvars render call: WriteTFVarsWithClusterOutputs vs
// WriteTFVars. When there is no usable cluster-outputs.json the rendered
// tfvars is byte-identical to writeAndInit's, so a first-phase / fresh
// workspace second-phase run is unchanged.
//
// config.ReadClusterOutputs is read here exactly the way
// internal/cli/cluster_phase.go writes it via config.WriteClusterOutputs
// — through internal/config — so internal/orchestration never imports
// internal/cli (the one-directional boundary asserted in Sprint 16).
func writeAndInitSecondPhase(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace, workspace string) error {
	co, err := loadReuseClusterOutputs(workspace)
	if err != nil {
		return err
	}
	if err := tfws.WriteTFVarsWithClusterOutputs(ws, co); err != nil {
		return fmt.Errorf("writing tfvars: %w", err)
	}
	if co != nil && co.VPCID != "" {
		fmt.Fprintf(os.Stderr,
			"→ Second-phase handoff: reusing cluster-phase VPC %s + transit gateway + client VPC (cluster-outputs.json)\n",
			co.VPCID)
	}
	if tfws.HasUserTFVars() {
		fmt.Fprintf(os.Stderr, "→ Layering user tfvars from %s (overrides config.yaml-derived values)\n", tfws.UserTFVarsPath())
	}
	fmt.Fprintln(os.Stderr, "→ terraform init")
	return tfws.Init(ctx)
}

// loadReuseClusterOutputs returns the workspace's ClusterOutputs when a
// cluster-outputs.json exists (the cluster phase completed → we are the
// SECOND phase reusing it), or (nil, nil) when there is none (first/
// cluster phase or a pre-handoff workspace — caller renders the create
// path unchanged). A genuine read/parse error is surfaced so the user
// sees a corrupt file rather than silently planning a duplicate create.
func loadReuseClusterOutputs(workspace string) (*config.ClusterOutputs, error) {
	co, err := config.ReadClusterOutputs(workspace)
	if err != nil {
		if err == config.ErrClusterOutputsMissing {
			return nil, nil
		}
		return nil, fmt.Errorf("reading cluster-outputs.json for second-phase reuse: %w", err)
	}
	return co, nil
}
