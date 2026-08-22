package orchestration

// The optional Gateway phase — the BNK data-plane ingress/egress config
// (Gateway API + SnatPool + Egress + static routes + the VXLAN security-group
// rule) as an independent phase against state-gateway/. It reuses the existing
// cluster (from cluster-outputs.json) and requires a healthy BNK, so it is
// triggered ONLY by the standalone `roksbnkctl gateway up/down` — never the
// composite up/down. Mirrors the Sprint 28 Testing-phase plumbing.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// openGatewayTF mirrors openTestingTF but targets state-gateway/.
func openGatewayTF(ctx context.Context, in *LifecycleInputs) (*config.Context, *tf.Workspace, error) {
	cctx, err := config.New(in.Workspace)
	if err != nil {
		return nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving API key: %w", err)
	}
	stateDir, err := config.WorkspaceGatewayStateDir(cctx.WorkspaceName)
	if err != nil {
		return nil, nil, err
	}
	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, apiKey, os.Stdout, in.errOut())
	if err != nil {
		return nil, nil, err
	}
	return cctx, tfws, nil
}

// writeAndInitGatewayPhase renders tfvars, writes the forced gateway-phase
// override, and inits. The Gateway phase REQUIRES a cluster-outputs.json (it
// reuses an existing cluster + BNK); absent → an actionable error. Returns the
// override path to append LAST to the var-file chain.
func writeAndInitGatewayPhase(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace, workspace string) ([]string, error) {
	w := os.Stderr
	if err := tfws.WriteTFVars(ws); err != nil {
		return nil, fmt.Errorf("writing tfvars: %w", err)
	}
	if tfws.HasUserTFVars() {
		fmt.Fprintf(w, "→ Layering user tfvars from %s (overrides config.yaml-derived values)\n", tfws.UserTFVarsPath())
	}
	co, err := loadReuseClusterOutputs(workspace)
	if err != nil {
		return nil, err
	}
	if co == nil || co.VPCID == "" {
		return nil, fmt.Errorf(
			"the Gateway phase configures an existing cluster + BNK, but no cluster-outputs.json was found for workspace %q — run `roksbnkctl up` (the cluster + BNK phases) first, then `roksbnkctl gateway up`",
			workspace)
	}
	overridePath, werr := writeGatewayPhaseOverride(tfws, co)
	if werr != nil {
		return nil, werr
	}
	fmt.Fprintf(w,
		"→ Gateway-phase handoff: reusing cluster VPC %s — applying the data-plane config "+
			"(Gateway API + SnatPool + Egress + static routes) and the VXLAN security-group rule. "+
			"The gateway phase manages ONLY this config (cluster/BNK/testing untouched).\n", co.VPCID)
	fmt.Fprintln(w, "→ terraform init")
	if err := tfws.Init(ctx); err != nil {
		return nil, err
	}
	return []string{overridePath}, nil
}

// RunGatewayUp = plan + confirm + apply against state-gateway/. Standalone:
// the composite `up` never runs it (the Gateway phase needs a healthy BNK).
func RunGatewayUp(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("gateway up"); err != nil {
		return err
	}
	cctx, tfws, err := openGatewayTF(ctx, in)
	if err != nil {
		return err
	}
	// The release line is a create-time setting, and this phase can reach a
	// cluster without going through prepareBNKUp — which is where the other
	// create-time guards live (#177). The gateway phase is not incidental to a
	// line flip: 2.3 points the Gateway at k8s.f5net.com/F5BnkGateway and 2.4 at
	// gateway.k8s.f5.com/GatewaySettings, so `gateway up` after a manifest bump
	// would rewrite the data-plane surface against an install built for the other
	// line.
	if applied, aerr := config.ReadAppliedTFVarsReplayAssignments(cctx.WorkspaceName, bnkPhaseSnapshotLabel); aerr == nil {
		if err := config.CheckLineChange(cctx.Workspace, applied); err != nil {
			return err
		}
	}
	// PRD 12: fill empty gateway client subnets from the deployed Testing
	// phase's jumphost private IPs before tfvars render. Best-effort —
	// config/user values always win, and a missing test rig just warns.
	tryAutoGatewayClientSubnets(cctx.Workspace, in.Workspace, in.errOut())
	extraVF, err := writeAndInitGatewayPhase(ctx, tfws, cctx.Workspace, in.Workspace)
	if err != nil {
		return err
	}
	varFiles := append(append([]string{}, in.VarFiles...), extraVF...)

	w := in.errOut()
	fmt.Fprintln(w, "→ terraform plan (gateway phase)")
	changes, err := tfws.Plan(ctx, varFiles...)
	if err != nil {
		return err
	}
	if !changes {
		fmt.Fprintln(w, "✓ no changes")
		return nil
	}
	if !in.Auto && !in.PromptYesNo("Apply this plan?", false) {
		return errors.New("aborted")
	}
	fmt.Fprintln(w, "→ terraform apply (gateway phase)")
	return applyWithRetry(ctx, tfws, varFiles)
}

// RunGatewayDown = destroy state-gateway/, leaving cluster + BNK + testing.
func RunGatewayDown(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("gateway down"); err != nil {
		return err
	}
	cctx, tfws, err := openGatewayTF(ctx, in)
	if err != nil {
		return err
	}
	w := in.errOut()
	if !in.Auto {
		fmt.Fprintf(w, "This will destroy the Gateway data-plane config (Gateway API + SnatPool + Egress + static routes + the VXLAN SG rule) for workspace %q (cluster + BNK + testing untouched).\n", cctx.WorkspaceName)
		if !in.PromptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
	}
	extraVF, err := writeAndInitGatewayPhase(ctx, tfws, cctx.Workspace, in.Workspace)
	if err != nil {
		return err
	}
	appliedVF := LayerAppliedTFVars(in.Workspace, "gateway")
	if err := RequireSnapshotOrVarFile(appliedVF, in.VarFiles, tfws.HasUserTFVars(), cctx.Workspace.Prefix != "", "gateway", "gateway down"); err != nil {
		return err
	}
	varFiles := append(append(append([]string{}, appliedVF...), in.VarFiles...), extraVF...)
	fmt.Fprintln(w, "→ terraform destroy (gateway phase)")
	return destroyWithRetry(ctx, tfws, varFiles)
}
