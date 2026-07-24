package orchestration

// The optional Transit Gateway connection phase — attaches the workspace's
// cluster VPC to an EXISTING Transit Gateway, so multiple clusters can share
// one gateway. Runs against state-tgw/, reusing the cluster from
// cluster-outputs.json, so it works for a created OR a registered cluster.
// Triggered only by the standalone `roksbnkctl tgw connect/disconnect`.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// openTGWTF mirrors openFLPTF but targets state-tgw/.
func openTGWTF(ctx context.Context, in *LifecycleInputs) (*config.Context, *tf.Workspace, error) {
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
	stateDir, err := config.WorkspaceTGWStateDir(cctx.WorkspaceName)
	if err != nil {
		return nil, nil, err
	}
	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, apiKey, os.Stdout, in.errOut())
	if err != nil {
		return nil, nil, err
	}
	return cctx, tfws, nil
}

// tgwTarget resolves the Transit Gateway to attach to, from the workspace config
// (resources.transit_gateway.existing — set by `tgw connect <name-or-id>` or the
// init interview). Empty is an actionable error.
func tgwTarget(ws *config.Workspace) (string, error) {
	if ws.Resources != nil && ws.Resources.TransitGateway.Existing != "" {
		return ws.Resources.TransitGateway.Existing, nil
	}
	return "", errors.New("no Transit Gateway to connect to — run `roksbnkctl tgw connect <name-or-id>`, or set resources.transit_gateway.existing in config.yaml")
}

// tgwConnectionName is this cluster's connection name on the shared gateway.
// Unique per gateway: the workspace prefix (unique by design) when set, else the
// module falls back to the VPC name.
func tgwConnectionName(ws *config.Workspace) string {
	return ws.Prefix
}

// writeAndInitTGWPhase renders tfvars, writes the forced TGW-phase override, and
// inits. Requires cluster-outputs.json (it reuses an existing cluster) and a
// resolved gateway target. Returns the override path to append LAST.
func writeAndInitTGWPhase(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace, workspace string) ([]string, string, error) {
	w := os.Stderr
	if err := tfws.WriteTFVars(ws); err != nil {
		return nil, "", fmt.Errorf("writing tfvars: %w", err)
	}
	if tfws.HasUserTFVars() {
		fmt.Fprintf(w, "→ Layering user tfvars from %s (overrides config.yaml-derived values)\n", tfws.UserTFVarsPath())
	}
	target, err := tgwTarget(ws)
	if err != nil {
		return nil, "", err
	}
	co, err := loadReuseClusterOutputs(workspace)
	if err != nil {
		return nil, "", err
	}
	if co == nil || co.VPCID == "" {
		return nil, "", fmt.Errorf(
			"the transit gateway phase attaches an existing cluster's VPC, but no cluster-outputs.json was found for workspace %q — run `roksbnkctl cluster up` (or `roksbnkctl cluster register`) first",
			workspace)
	}
	overridePath, werr := writeTGWPhaseOverride(tfws, co, target, tgwConnectionName(ws))
	if werr != nil {
		return nil, "", werr
	}
	fmt.Fprintf(w,
		"→ TGW-phase handoff: attaching cluster VPC %s to Transit Gateway %q. "+
			"This phase manages ONLY the connection (cluster/BNK/testing/gateway/FLP untouched).\n", co.VPCID, target)
	fmt.Fprintln(w, "→ terraform init")
	if err := tfws.Init(ctx); err != nil {
		return nil, "", err
	}
	return []string{overridePath}, target, nil
}

// RunTGWConnect = plan + confirm + apply against state-tgw/, then persist
// tgw-outputs.json (gateway + connection identity). Standalone.
func RunTGWConnect(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("tgw connect"); err != nil {
		return err
	}
	cctx, tfws, err := openTGWTF(ctx, in)
	if err != nil {
		return err
	}

	// Shared-VPC idempotency: an IBM VPC holds exactly one Transit Gateway
	// attachment, so when a second cluster shares the first cluster's VPC, that VPC
	// is ALREADY attached to the target gateway (the first cluster connected it).
	// Detect that up front and record the live connection instead of running an
	// apply that IBM rejects with "the requested network is already connected to an
	// existing transit gateway". Best-effort: any lookup error falls through to the
	// normal plan/apply, which surfaces a genuine conflict (VPC on a DIFFERENT
	// gateway) loudly.
	if target, terr := tgwTarget(cctx.Workspace); terr == nil {
		if done, aerr := recordIfVPCAlreadyAttached(ctx, cctx, in.Workspace, target, in.errOut()); aerr == nil && done {
			return nil
		}
	}

	extraVF, target, err := writeAndInitTGWPhase(ctx, tfws, cctx.Workspace, in.Workspace)
	if err != nil {
		return err
	}
	varFiles := append(append([]string{}, in.VarFiles...), extraVF...)

	w := in.errOut()
	fmt.Fprintln(w, "→ terraform plan (tgw phase)")
	changes, err := tfws.Plan(ctx, varFiles...)
	if err != nil {
		return err
	}
	if !changes {
		fmt.Fprintln(w, "✓ no changes — already connected")
		return persistTGWOutputs(ctx, tfws, in.Workspace, target, w)
	}
	if !in.Auto && !in.PromptYesNo(fmt.Sprintf("Attach this cluster's VPC to Transit Gateway %q?", target), false) {
		return errors.New("aborted")
	}
	fmt.Fprintln(w, "→ terraform apply (tgw phase)")
	if err := applyWithRetry(ctx, tfws, varFiles); err != nil {
		return err
	}
	return persistTGWOutputs(ctx, tfws, in.Workspace, target, w)
}

// recordIfVPCAlreadyAttached checks whether this workspace's cluster VPC is
// already attached to the target Transit Gateway (the shared-VPC case). When it is,
// it records the LIVE connection to tgw-outputs.json and returns (true, nil) so the
// caller skips the apply entirely. Returns (false, nil) when the VPC is not
// attached to this gateway (the normal first-cluster path — apply proceeds), and
// (false, err) on a lookup failure so the caller can fall through resiliently.
func recordIfVPCAlreadyAttached(ctx context.Context, cctx *config.Context, workspace, target string, w io.Writer) (bool, error) {
	co, err := loadReuseClusterOutputs(workspace)
	if err != nil || co == nil || co.VPCID == "" {
		return false, fmt.Errorf("no cluster VPC to check")
	}
	ic, err := tgwIBMClient(ctx, cctx)
	if err != nil {
		return false, err
	}
	gw, err := ic.ResolveTransitGateway(ctx, target)
	if err != nil {
		return false, err
	}
	conns, err := ic.ListTGWConnections(ctx, gw.ID)
	if err != nil {
		return false, err
	}
	conn := tgwConnectionForVPC(conns, co.VPCID)
	if conn == nil {
		return false, nil // not attached to THIS gateway — let the apply create it
	}
	out := &config.TGWOutputs{
		GatewayID:      gw.ID,
		GatewayName:    gw.Name,
		GatewayCRN:     gw.CRN,
		ConnectionID:   conn.ID,
		ConnectionName: conn.Name,
		VPCID:          co.VPCID,
		VPCCRN:         conn.NetworkID,
	}
	if err := config.WriteTGWOutputs(workspace, out); err != nil {
		return false, fmt.Errorf("writing tgw-outputs.json: %w", err)
	}
	fmt.Fprintf(w, "✓ cluster VPC %s is already attached to Transit Gateway %s (%s) as connection %q.\n"+
		"  A VPC holds one TGW attachment, so this shared-VPC cluster reuses the existing one — recorded tgw-outputs.json.\n",
		co.VPCID, gw.Name, gw.ID, conn.Name)
	return true, nil
}

// tgwIBMClient builds an IBM client from the workspace's resolved API key + region.
func tgwIBMClient(ctx context.Context, cctx *config.Context) (*ibm.Client, error) {
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	return ibm.New(apiKey, cctx.Workspace.IBMCloud.Region)
}

// tgwConnectionForVPC returns the gateway's live VPC connection for vpcID, or nil.
// A connection's NetworkID is the attached VPC's CRN (…:vpc:<id>), so a substring
// match on the unique VPC id identifies it. Only attached/pending connections
// count as "already connected"; a failed/deleting one should be re-created.
func tgwConnectionForVPC(conns []ibm.TGWConnection, vpcID string) *ibm.TGWConnection {
	for i := range conns {
		c := &conns[i]
		if c.NetworkType != "" && c.NetworkType != "vpc" {
			continue
		}
		if !strings.Contains(c.NetworkID, vpcID) {
			continue
		}
		if c.Status == "attached" || c.Status == "pending" {
			return c
		}
	}
	return nil
}

// persistTGWOutputs reads the tgw_* root outputs and records tgw-outputs.json.
func persistTGWOutputs(ctx context.Context, tfws *tf.Workspace, workspace, target string, w io.Writer) error {
	outputs, err := tfws.Output(ctx)
	if err != nil {
		return fmt.Errorf("reading tgw phase outputs: %w", err)
	}
	gwID := tfStringOutput(outputs, "tgw_gateway_id")
	if gwID == "" {
		return fmt.Errorf("tgw phase produced no gateway id — check that Transit Gateway %q exists and matched exactly one gateway", target)
	}
	out := &config.TGWOutputs{
		GatewayID:      gwID,
		GatewayName:    tfStringOutput(outputs, "tgw_gateway_name"),
		GatewayCRN:     tfStringOutput(outputs, "tgw_gateway_crn"),
		ConnectionID:   tfStringOutput(outputs, "tgw_connection_id"),
		ConnectionName: tfStringOutput(outputs, "tgw_connection_name"),
		VPCID:          tfStringOutput(outputs, "tgw_vpc_id"),
		VPCCRN:         tfStringOutput(outputs, "tgw_vpc_crn"),
	}
	if err := config.WriteTGWOutputs(workspace, out); err != nil {
		return fmt.Errorf("writing tgw-outputs.json: %w", err)
	}
	fmt.Fprintf(w, "→ Connected to Transit Gateway %s (%s) as connection %q — recorded tgw-outputs.json.\n"+
		"  `roksbnkctl -w %s tgw status` shows the live connection state.\n",
		out.GatewayName, out.GatewayID, out.ConnectionName, workspace)
	return nil
}

// RunTGWDisconnect = destroy state-tgw/, removing only THIS cluster's connection
// (the gateway and every other cluster's connection stay), and clear tgw-outputs.json.
func RunTGWDisconnect(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("tgw disconnect"); err != nil {
		return err
	}
	cctx, tfws, err := openTGWTF(ctx, in)
	if err != nil {
		return err
	}
	w := in.errOut()
	if !in.Auto {
		fmt.Fprintf(w, "This will detach workspace %q's VPC from its Transit Gateway (the gateway and other clusters' connections are untouched).\n", cctx.WorkspaceName)
		if !in.PromptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
	}
	extraVF, _, err := writeAndInitTGWPhase(ctx, tfws, cctx.Workspace, in.Workspace)
	if err != nil {
		return err
	}
	appliedVF := LayerAppliedTFVars(in.Workspace, "tgw")
	if err := RequireSnapshotOrVarFile(appliedVF, in.VarFiles, tfws.HasUserTFVars(), cctx.Workspace.Prefix != "", "tgw", "tgw disconnect"); err != nil {
		return err
	}
	varFiles := append(append(append([]string{}, appliedVF...), in.VarFiles...), extraVF...)
	fmt.Fprintln(w, "→ terraform destroy (tgw phase)")
	if err := destroyWithRetry(ctx, tfws, varFiles); err != nil {
		return err
	}
	if err := config.DeleteTGWOutputs(in.Workspace); err != nil {
		fmt.Fprintf(w, "⚠ could not remove tgw-outputs.json: %v\n", err)
	}
	fmt.Fprintln(w, "✓ Detached from the Transit Gateway.")
	return nil
}
