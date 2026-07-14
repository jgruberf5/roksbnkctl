package orchestration

// The optional FLP phase — the in-cluster F5 License Proxy — as an independent
// phase against state-flp/. It reuses the existing cluster (from
// cluster-outputs.json) and runs BEFORE `bnk up` in f5licenseproxy mode, so it
// is triggered ONLY by the standalone `roksbnkctl flp up/down`, never the
// composite up/down. Mirrors the Gateway-phase plumbing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hashicorp/terraform-exec/tfexec"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// openFLPTF mirrors openGatewayTF but targets state-flp/.
func openFLPTF(ctx context.Context, in *LifecycleInputs) (*config.Context, *tf.Workspace, error) {
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
	stateDir, err := config.WorkspaceFLPStateDir(cctx.WorkspaceName)
	if err != nil {
		return nil, nil, err
	}
	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, apiKey, os.Stdout, in.errOut())
	if err != nil {
		return nil, nil, err
	}
	return cctx, tfws, nil
}

// writeAndInitFLPPhase renders tfvars, writes the forced FLP-phase override, and
// inits. The FLP phase REQUIRES a cluster-outputs.json (it reuses an existing
// cluster); absent → an actionable error. Returns the override path to append
// LAST to the var-file chain.
func writeAndInitFLPPhase(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace, workspace string) ([]string, error) {
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
			"the FLP phase installs into an existing cluster, but no cluster-outputs.json was found for workspace %q — run `roksbnkctl cluster up` (or `roksbnkctl cluster register` for an existing cluster) first, then `roksbnkctl flp up`",
			workspace)
	}
	overridePath, werr := writeFLPPhaseOverride(tfws, co)
	if werr != nil {
		return nil, werr
	}
	fmt.Fprintf(w,
		"→ FLP-phase handoff: reusing cluster VPC %s — installing the F5 License Proxy. "+
			"The FLP phase manages ONLY the proxy (cluster/BNK/testing/gateway untouched).\n", co.VPCID)
	fmt.Fprintln(w, "→ terraform init")
	if err := tfws.Init(ctx); err != nil {
		return nil, err
	}
	return []string{overridePath}, nil
}

// RunFLPUp = plan + confirm + apply against state-flp/, then persist
// flp-outputs.json (root CA + endpoint) for the BNK phase to consume in
// f5licenseproxy mode. Standalone: the composite `up` never runs it.
func RunFLPUp(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("flp up"); err != nil {
		return err
	}
	cctx, tfws, err := openFLPTF(ctx, in)
	if err != nil {
		return err
	}
	extraVF, err := writeAndInitFLPPhase(ctx, tfws, cctx.Workspace, in.Workspace)
	if err != nil {
		return err
	}
	varFiles := append(append([]string{}, in.VarFiles...), extraVF...)

	w := in.errOut()
	fmt.Fprintln(w, "→ terraform plan (flp phase)")
	changes, err := tfws.Plan(ctx, varFiles...)
	if err != nil {
		return err
	}
	if !changes {
		fmt.Fprintln(w, "✓ no changes")
		return persistFLPOutputs(ctx, tfws, in.Workspace, w)
	}
	if !in.Auto && !in.PromptYesNo("Apply this plan?", false) {
		return errors.New("aborted")
	}
	fmt.Fprintln(w, "→ terraform apply (flp phase)")
	if err := applyWithRetry(ctx, tfws, varFiles); err != nil {
		return err
	}
	return persistFLPOutputs(ctx, tfws, in.Workspace, w)
}

// persistFLPOutputs reads the flp_* root outputs and writes flp-outputs.json so
// `bnk up` in f5licenseproxy mode can wire the License CR without re-deriving
// anything. A missing/empty output is a hard error — FLP mode can't proceed
// without the CA + endpoint.
func persistFLPOutputs(ctx context.Context, tfws *tf.Workspace, workspace string, w io.Writer) error {
	outputs, err := tfws.Output(ctx)
	if err != nil {
		return fmt.Errorf("reading flp phase outputs: %w", err)
	}
	rootCA := tfStringOutput(outputs, "flp_root_ca")
	endpoint := tfStringOutput(outputs, "flp_endpoint")
	if rootCA == "" || endpoint == "" {
		return fmt.Errorf("flp phase produced no root CA / endpoint output — the F5 License Proxy did not deploy")
	}
	out := &config.FLPOutputs{
		RootCAB64: rootCA,
		Endpoint:  endpoint,
		Namespace: tfStringOutput(outputs, "flp_namespace"),
		// Empty unless the proxy was exposed with --add-node-port-access.
		ExternalEndpoint:  tfStringOutput(outputs, "flp_external_endpoint"),
		ExternalEndpoints: tfStringListOutput(outputs, "flp_external_endpoints"),
	}
	if out.ExternalEndpoint != "" {
		fmt.Fprintf(w, "→ FLP reachable from other clusters at %s\n"+
			"  point the consuming workspace at it with:\n"+
			"    bnk:\n      flp:\n        external:\n          url: %s\n          root_ca_b64: <`roksbnkctl -w %s flp output` → root_ca_b64>\n",
			out.ExternalEndpoint, out.ExternalEndpoint, workspace)
	}
	if err := config.WriteFLPOutputs(workspace, out); err != nil {
		return fmt.Errorf("writing flp-outputs.json: %w", err)
	}
	fmt.Fprintf(w, "→ FLP ready at %s — recorded flp-outputs.json. Run `roksbnkctl bnk up` with bnk.license_mode: f5licenseproxy.\n", endpoint)
	return nil
}

// RunFLPDown = destroy state-flp/, leaving cluster + BNK + testing + gateway, and
// clear the flp-outputs.json handoff.
func RunFLPDown(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("flp down"); err != nil {
		return err
	}
	cctx, tfws, err := openFLPTF(ctx, in)
	if err != nil {
		return err
	}
	w := in.errOut()
	if !in.Auto {
		fmt.Fprintf(w, "This will destroy the F5 License Proxy for workspace %q (cluster + BNK + testing + gateway untouched).\n", cctx.WorkspaceName)
		if !in.PromptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
	}
	extraVF, err := writeAndInitFLPPhase(ctx, tfws, cctx.Workspace, in.Workspace)
	if err != nil {
		return err
	}
	appliedVF := LayerAppliedTFVars(in.Workspace, "flp")
	if err := RequireSnapshotOrVarFile(appliedVF, in.VarFiles, tfws.HasUserTFVars(), cctx.Workspace.Prefix != "", "flp", "flp down"); err != nil {
		return err
	}
	varFiles := append(append(append([]string{}, appliedVF...), in.VarFiles...), extraVF...)
	fmt.Fprintln(w, "→ terraform destroy (flp phase)")
	if err := destroyWithRetry(ctx, tfws, varFiles); err != nil {
		return err
	}
	if err := config.DeleteFLPOutputs(in.Workspace); err != nil {
		fmt.Fprintf(w, "⚠ could not remove flp-outputs.json: %v\n", err)
	}
	return nil
}

// tfStringOutput decodes a string-typed terraform output (empty when absent).
func tfStringOutput(outputs map[string]tfexec.OutputMeta, key string) string {
	v, ok := outputs[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v.Value, &s); err != nil {
		return ""
	}
	return s
}

// tfStringListOutput reads a terraform list(string) output. Missing or
// non-decodable → nil, matching tfStringOutput's forgiving contract.
func tfStringListOutput(outputs map[string]tfexec.OutputMeta, key string) []string {
	v, ok := outputs[key]
	if !ok {
		return nil
	}
	var s []string
	if err := json.Unmarshal(v.Value, &s); err != nil {
		return nil
	}
	return s
}
