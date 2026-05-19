package tf

import (
	"fmt"
	"io"
	"os"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// WriteTFVars renders the workspace config into terraform.tfvars at path.
// Excludes the API key, which is passed via TF_VAR_ibmcloud_api_key env
// var when terraform is invoked.
//
// kubeconfigDir / scratchDir are rendered when non-empty. Both require
// the upstream TF to declare matching root-level variables (kubeconfig_dir
// in v0.6.8+, scratch_dir in v0.6.9+); older versions warn but tolerate
// the undeclared values.
func WriteTFVars(path string, ws *config.Workspace, kubeconfigDir, scratchDir string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	return RenderTFVars(f, ws, kubeconfigDir, scratchDir)
}

// RenderTFVarsWithClusterOutputs renders the workspace tfvars and, when
// a cluster-phase ClusterOutputs is present, appends the second-phase
// existing-resource reuse toggles (issues/issue_sprint16_validator.md
// Issue 2 — phase handoff).
//
// The `up` flow applies the same roks_cluster/testing terraform across
// two independent state files. The cluster phase (state-cluster/) creates
// the cluster VPC / transit gateway / client VPC and records vpc_id in
// cluster-outputs.json. Without a handoff, the second (bnk/testing) phase
// runs the same modules against its own state/ and plans to *create*
// those same-named resources — IBM Cloud rejects the duplicates. These
// toggles make the second phase REUSE them (the terraform reuse plumbing
// already exists; this is wiring, not new design — README decision 5).
//
// Contract (asserted by internal/tf/secondphase_handoff_test.go, the
// validator's hermetic Issue 2 regression — the cross-agent seam):
//
//   - co == nil               → output is byte-identical to
//     RenderTFVars(w, ws, kubeconfigDir, scratchDir). The first/cluster
//     phase (no cluster-outputs.json yet) is unperturbed, keeping
//     validator Issue 1's parity gate GREEN.
//   - co != nil, co.VPCID==""  → defensive create path: a half-written
//     cluster-outputs.json must NOT silently flip
//     use_existing_cluster_vpc=true (an empty existing_cluster_vpc_id
//     would fail the submodule's data.ibm_is_vpc lookup).
//   - co != nil, co.VPCID!=""  → append the reuse toggles:
//     use_existing_cluster_vpc  = true
//     existing_cluster_vpc_id   = "<co.VPCID>"
//     create_roks_transit_gateway = false
//     testing_create_client_vpc = false
//
// Transit-gateway reuse: the cluster submodule has NO existing-TG data
// lookup (only the create_transit_gateway count toggle), so the smaller-
// surface, symmetric option is for the second phase to NOT manage the TG
// (create_roks_transit_gateway = false). The cluster phase already
// created the gateway and connected the cluster VPC; the testing module
// looks the gateway up by name (data.ibm_tg_gateway.transit_gateway) for
// its own client-VPC connection, so phase 2 needs the TG to *exist*, not
// to be managed here.
//
// testing_client_vpc_name is intentionally NOT emitted: ClusterOutputs
// carries no client-VPC name and config.Workspace has no field for it.
// The name only ever comes from the user's terraform.tfvars.user (or the
// module default) and that same value flows in BOTH phases, so flipping
// only testing_create_client_vpc = false makes module.testing look up
// the existing client VPC by the same name the cluster phase created it
// with — correct without guessing a name.
func RenderTFVarsWithClusterOutputs(w io.Writer, ws *config.Workspace, co *config.ClusterOutputs, kubeconfigDir, scratchDir string) error {
	if err := RenderTFVars(w, ws, kubeconfigDir, scratchDir); err != nil {
		return err
	}
	// No usable handoff (first/cluster phase, fresh workspace, or a
	// half-written cluster-outputs.json without a vpc_id) → leave the
	// render byte-identical to the create path.
	if co == nil || co.VPCID == "" {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# Second-phase existing-resource handoff (issue_sprint16_validator.md Issue 2):"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# cluster-outputs.json exists, so the cluster phase already created the"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# cluster VPC / transit gateway / client VPC. Reuse them instead of"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# planning duplicate same-named resources (IBM Cloud duplicate-name failure)."); err != nil {
		return err
	}
	fmt.Fprintf(w, "use_existing_cluster_vpc = true\n")
	fmt.Fprintf(w, "existing_cluster_vpc_id = %q\n", co.VPCID)
	fmt.Fprintf(w, "create_roks_transit_gateway = false\n")
	fmt.Fprintf(w, "testing_create_client_vpc = false\n")
	return nil
}

// RenderTFVars writes the tfvars body to w. Exposed for tests / callers
// that want to inspect the rendering before committing it to disk.
//
// Only fields the user has explicitly set in config.yaml are emitted —
// the rest fall through to the upstream TF module's own defaults.
//
// kubeconfigDir + scratchDir are roksbnkctl-managed paths threaded into the
// upstream TF; the upstream defaults for both target the bnk runner
// image's /work mount, which doesn't exist on a host filesystem.
//
// IMPORTANT: api_key is NEVER written. Pass it via the
// TF_VAR_ibmcloud_api_key env var on the terraform invocation.
func RenderTFVars(w io.Writer, ws *config.Workspace, kubeconfigDir, scratchDir string) error {
	if _, err := fmt.Fprintln(w, "# Generated by roksbnkctl. Do not edit by hand — run `roksbnkctl init` to update."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# IBMCLOUD_API_KEY is NOT written here; it's passed via TF_VAR env var."); err != nil {
		return err
	}
	fmt.Fprintln(w)

	// IBM Cloud
	if ws.IBMCloud.Region != "" {
		fmt.Fprintf(w, "ibmcloud_cluster_region = %q\n", ws.IBMCloud.Region)
	}
	if ws.IBMCloud.ResourceGroup != "" {
		fmt.Fprintf(w, "ibmcloud_resource_group = %q\n", ws.IBMCloud.ResourceGroup)
	}

	// Cluster — branching on Create chooses which TF variable name carries
	// the cluster identity. The upstream module uses different names for
	// new (openshift_cluster_name) vs existing (roks_cluster_id_or_name).
	fmt.Fprintf(w, "create_roks_cluster = %v\n", ws.Cluster.Create)
	if ws.Cluster.Create {
		if ws.Cluster.Name != "" {
			fmt.Fprintf(w, "openshift_cluster_name = %q\n", ws.Cluster.Name)
		}
		if ws.Cluster.OpenShiftVersion != "" {
			fmt.Fprintf(w, "openshift_cluster_version = %q\n", ws.Cluster.OpenShiftVersion)
		}
		if ws.Cluster.WorkersPerZone > 0 {
			fmt.Fprintf(w, "roks_workers_per_zone = %d\n", ws.Cluster.WorkersPerZone)
		}
	} else {
		if ws.Cluster.Name != "" {
			fmt.Fprintf(w, "roks_cluster_id_or_name = %q\n", ws.Cluster.Name)
		}
	}

	// BNK
	if ws.BNK.CNEInstanceSize != "" {
		fmt.Fprintf(w, "cneinstance_deployment_size = %q\n", ws.BNK.CNEInstanceSize)
	}
	if ws.BNK.FARRepoURL != "" {
		fmt.Fprintf(w, "far_repo_url = %q\n", ws.BNK.FARRepoURL)
	}
	if ws.BNK.ManifestVersion != "" {
		fmt.Fprintf(w, "f5_bigip_k8s_manifest_version = %q\n", ws.BNK.ManifestVersion)
	}

	// Kubeconfig scratch dir. Overrides the module-internal /work/.bnk/...
	// default which only works inside the bnk runner image. Threaded to
	// each submodule by the root TF (v0.6.8+); older TF versions print a
	// "variable not declared" warning but otherwise behave the same.
	if kubeconfigDir != "" {
		fmt.Fprintf(w, "kubeconfig_dir = %q\n", kubeconfigDir)
	}

	// FLO scratch dir for FAR auth tarball + f5-manifest extraction.
	// Same /work problem class as kubeconfig_dir. Threaded by the root
	// TF (v0.6.9+) to module.flo, which derives manifest_download_dir
	// as ${scratch_dir}/f5-manifest automatically.
	if scratchDir != "" {
		fmt.Fprintf(w, "scratch_dir = %q\n", scratchDir)
	}

	return nil
}
