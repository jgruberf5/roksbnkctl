package tf

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/naming"
	"github.com/jgruberf5/roksbnkctl/internal/registry/source"
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
	return WriteTFVarsForWorkspace(path, "", ws, kubeconfigDir, scratchDir)
}

// WriteTFVarsForWorkspace is WriteTFVars with the workspace NAME, so the
// render can resolve the Sprint-29 air-gap registry-mirror record
// (registry-mirror.json) and redirect the BNK install off far_repo_url onto
// the in-cluster mirror. An empty workspaceName (or a workspace with no
// record) renders exactly as WriteTFVars did — byte-identical off the
// air-gap path.
func WriteTFVarsForWorkspace(path, workspaceName string, ws *config.Workspace, kubeconfigDir, scratchDir string) error {
	// Resolve the mirror record whenever one exists for this workspace. Its
	// presence — written by `registry replicate` — IS the opt-in to redirect the
	// install onto the in-cluster mirror; it does not require a registry: block in
	// config.yaml (replicate runs flag-driven too, as `--target icr`). A
	// workspace with no record reads ErrNoRegistryMirror, leaving mirror nil so
	// the render stays behavior-identical to the legacy far_repo_url path.
	//
	// Resolved BEFORE the file is created: a rejected record must leave the
	// existing tfvars untouched rather than truncated, since the caller aborts
	// and the operator is left holding whatever is on disk.
	var mirror *config.RegistryMirror
	if workspaceName != "" {
		if m, err := config.ReadRegistryMirror(workspaceName); err == nil {
			// Only redirect onto a mirror the workspace is actually configured
			// for. A record that describes a registry the config has since been
			// repointed away from would rewrite every image and chart reference
			// in the install to a mirror nobody asked for — and terraform would
			// apply it without complaint.
			if err := config.MirrorRecordMismatchError(workspaceName, ws, m); err != nil {
				return err
			}
			mirror = m
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	return renderTFVars(f, ws, mirror, kubeconfigDir, scratchDir)
}

// RenderTFVars writes the tfvars body to w. Exposed for tests / callers
// that want to inspect the rendering before committing it to disk.
//
// Two render modes, selected by ws.Prefix:
//
//   - ws.Prefix == "" (legacy config): the SPARSE render — only the fields
//     the user explicitly set in config.yaml are emitted; every resource
//     name falls through to the upstream TF module defaults. This path is
//     is unchanged from before prefixes existed, and is what an un-migrated
//     config.yaml renders.
//   - ws.Prefix != "" (Sprint 26 prefix-driven config): the FULL render —
//     every account-scoped resource NAME is derived from the prefix via
//     internal/naming.Derive and emitted, alongside each resource's
//     create/reuse toggle, so two workspaces that both create infra no
//     longer collide on the upstream module's default names. Each variable
//     is emitted EXACTLY ONCE (in-file duplicates are a terraform error);
//     the separate bnk-phase-override.tfvars (live second-phase handoff)
//     and any terraform.tfvars.user still layer LAST as their own
//     -var-file and override these via terraform's last-wins precedence —
//     they are cross-file, never in-file duplicates.
//
// kubeconfigDir + scratchDir are roksbnkctl-managed paths threaded into the
// upstream TF; the upstream defaults for both target the bnk runner
// image's /work mount, which doesn't exist on a host filesystem.
//
// IMPORTANT: api_key is NEVER written. Pass it via the
// TF_VAR_ibmcloud_api_key env var on the terraform invocation.
func RenderTFVars(w io.Writer, ws *config.Workspace, kubeconfigDir, scratchDir string) error {
	return renderTFVars(w, ws, nil, kubeconfigDir, scratchDir)
}

// flpVSI returns ws.BNK.FLP.VSI without nil-checking at every call site.
func flpVSI(ws *config.Workspace) *config.BNKFLPVSICfg {
	if ws == nil || ws.BNK.FLP == nil {
		return nil
	}
	return ws.BNK.FLP.VSI
}

// renderTFVars is the shared implementation. mirror is the resolved air-gap
// registry-mirror record (nil off the air-gap path → far_repo_url stands).
func renderTFVars(w io.Writer, ws *config.Workspace, mirror *config.RegistryMirror, kubeconfigDir, scratchDir string) error {
	// Contradictory network settings are refused HERE, before a byte is written:
	// this is where config becomes terraform input, and every path that renders
	// tfvars goes through it. Both of these fail SILENTLY in the modules — the
	// ignored half simply never gets read — so the operator would find out from a
	// proxy on the wrong network or a cluster in subnets they did not choose.
	if vsi := flpVSI(ws); vsi != nil && vsi.CreateVPC && vsi.VPC != "" {
		return fmt.Errorf("bnk.flp.vsi: `vpc` and `create_vpc` are mutually exclusive — "+
			"`vpc: %s` adopts an existing network, `create_vpc: true` builds a new one. "+
			"With both set the new VPC silently wins and the proxy comes up with no route "+
			"to the clusters meant to license against it. Remove whichever you did not mean", vsi.VPC)
	}
	if len(ws.Cluster.ExistingSubnetIDs) > 0 && (ws.Resources == nil || ws.Resources.ClusterVPC.Create || ws.Resources.ClusterVPC.Existing == "") {
		return fmt.Errorf("cluster.existing_subnet_ids requires resources.cluster_vpc = { create: false, existing: <vpc-id> } — " +
			"a subnet cannot be adopted independently of the VPC that contains it")
	}
	if _, err := fmt.Fprintln(w, "# Generated by roksbnkctl. Do not edit by hand — run `roksbnkctl init` to update."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# IBMCLOUD_API_KEY is NOT written here; it's passed via TF_VAR env var."); err != nil {
		return err
	}
	fmt.Fprintln(w)

	if ws.Prefix != "" {
		if err := renderFullBody(w, ws, mirror); err != nil {
			return err
		}
	} else {
		if err := renderSparseBody(w, ws, mirror); err != nil {
			return err
		}
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

// renderSparseBody is the legacy (empty-Prefix) render body: only fields the
// user explicitly set in config.yaml are emitted; resource names fall
// through to the upstream module defaults.
func renderSparseBody(w io.Writer, ws *config.Workspace, mirror *config.RegistryMirror) error {
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
		renderClusterSizing(w, ws.Cluster)
	} else {
		if ws.Cluster.Name != "" {
			fmt.Fprintf(w, "roks_cluster_id_or_name = %q\n", ws.Cluster.Name)
		}
	}

	// Security-group source CIDRs render on BOTH paths: the sparse (legacy
	// empty-Prefix) body can still create a cluster via create_roks_cluster, and
	// an override the CLI reported as applied must not silently leave the module
	// default (fail-open) standing.
	renderSGCIDRFields(w, ws.Resources)

	if err := renderBNKFields(w, ws, mirror); err != nil {
		return err
	}
	return nil
}

// renderSGCIDRFields emits the security-group source CIDR lists, shared by the
// sparse and full render bodies. Each is emitted only when set, so an unset
// list leaves the module's own default standing — the defaults differ per
// plane and that knowledge lives in the modules.
func renderSGCIDRFields(w io.Writer, res *config.ResourcesCfg) {
	if res == nil {
		return
	}
	if len(res.TestingJumphostAllowedCIDRs) > 0 {
		fmt.Fprintf(w, "testing_jumphost_allowed_cidrs = %s\n", hclStringList(res.TestingJumphostAllowedCIDRs))
	}
	if len(res.TestingClientVPCInboundCIDRs) > 0 {
		fmt.Fprintf(w, "testing_client_vpc_inbound_cidrs = %s\n", hclStringList(res.TestingClientVPCInboundCIDRs))
	}
	if len(res.ClusterHTTPAllowedCIDRs) > 0 {
		fmt.Fprintf(w, "cluster_http_allowed_cidrs = %s\n", hclStringList(res.ClusterHTTPAllowedCIDRs))
	}
	if len(res.ClusterVPCDefaultSGInboundCIDRs) > 0 {
		fmt.Fprintf(w, "cluster_vpc_default_sg_inbound_cidrs = %s\n", hclStringList(res.ClusterVPCDefaultSGInboundCIDRs))
	}
}

// renderFullBody is the Sprint 26 prefix-driven render body: it derives the
// full account-scoped name set from ws.Prefix and emits each resource's
// create/reuse toggle + name EXACTLY ONCE. The operator's first-phase
// intent is captured here; the separate phase-override + user var-files
// layer last and override via terraform precedence (cross-file, not
// in-file).
//
// A resource's name carrier depends on its create toggle: a CREATED
// resource gets the prefix-derived name (so two workspaces don't collide);
// a DECLINED-but-depended-on resource gets the operator's Existing name/ID
// so the dependent looks it up by name.
func renderFullBody(w io.Writer, ws *config.Workspace, mirror *config.RegistryMirror) error {
	plan := naming.Derive(ws.Prefix)

	// Resources may be nil on a prefix-set config that predates the
	// resources block (defensive); treat a nil block as all-create with no
	// existing refs, matching the --var-file default.
	res := ws.Resources
	if res == nil {
		// ONE definition of "the default", shared with init. This used to inline
		// its own all-true set, which quietly disagreed once the testing client
		// (TGW jumphost + client VPC) was defaulted off: a legacy or hand-edited
		// config.yaml with no `resources:` block would still have built both here.
		res = config.DefaultResources()
	}

	// IBM Cloud region + resource group.
	if ws.IBMCloud.Region != "" {
		fmt.Fprintf(w, "ibmcloud_cluster_region = %q\n", ws.IBMCloud.Region)
	}
	if ws.IBMCloud.ResourceGroup != "" {
		fmt.Fprintf(w, "ibmcloud_resource_group = %q\n", ws.IBMCloud.ResourceGroup)
	}

	// Cluster. Created → openshift_cluster_name = <prefix>; existing →
	// roks_cluster_id_or_name = <Cluster.Name>. Reuses ClusterCfg, where
	// Name doubles as the existing id/name when Create=false.
	fmt.Fprintf(w, "create_roks_cluster = %v\n", ws.Cluster.Create)
	if ws.Cluster.Create {
		fmt.Fprintf(w, "openshift_cluster_name = %q\n", plan.ClusterName)
		if ws.Cluster.OpenShiftVersion != "" {
			fmt.Fprintf(w, "openshift_cluster_version = %q\n", ws.Cluster.OpenShiftVersion)
		}
		if ws.Cluster.WorkersPerZone > 0 {
			fmt.Fprintf(w, "roks_workers_per_zone = %d\n", ws.Cluster.WorkersPerZone)
		}
		renderClusterSizing(w, ws.Cluster)
	} else if ws.Cluster.Name != "" {
		fmt.Fprintf(w, "roks_cluster_id_or_name = %q\n", ws.Cluster.Name)
	}

	// Cluster VPC — always created by the cluster phase under the derived
	// name (no standalone toggle; it follows the cluster).
	fmt.Fprintf(w, "roks_cluster_vpc_name = %q\n", plan.ClusterVPCName)

	// BYO cluster VPC: adopt an existing VPC by ID instead of creating one.
	// Emitted only when adopting; absent ⇒ use_existing_cluster_vpc defaults false
	// (create a new VPC) — old configs without a cluster_vpc block are unchanged.
	if !res.ClusterVPC.Create && res.ClusterVPC.Existing != "" {
		fmt.Fprintln(w, "use_existing_cluster_vpc = true")
		fmt.Fprintf(w, "existing_cluster_vpc_id = %q\n", res.ClusterVPC.Existing)
	}

	// The worker attachment mode. Rendered ALWAYS, unlike most settings here,
	// because the terraform default and the tool default must never be able to
	// disagree about something that decides how a cluster is built. A single-nic
	// value reproduces today's behaviour exactly.
	fmt.Fprintf(w, "cluster_network_mode = %q\n", ws.ClusterNetworkMode())

	// BYO cluster SUBNETS (#61). Adopting the VPC alone still creates subnets
	// inside it, which defeats the point when the subnets are the thing carrying
	// the ACLs and routing. Emitted only when ids are supplied; absent leaves
	// use_existing_cluster_subnets false and the create path byte-identical.
	//
	// The pairing with the VPC is enforced in terraform (a precondition on the
	// subnet data source) rather than dropped silently here, so a half-configured
	// workspace fails at plan with a message naming the missing half.
	if len(ws.Cluster.ExistingSubnetIDs) > 0 {
		fmt.Fprintln(w, "use_existing_cluster_subnets = true")
		quoted := make([]string, 0, len(ws.Cluster.ExistingSubnetIDs))
		for _, id := range ws.Cluster.ExistingSubnetIDs {
			quoted = append(quoted, fmt.Sprintf("%q", id))
		}
		fmt.Fprintf(w, "existing_cluster_subnet_ids = [%s]\n", strings.Join(quoted, ", "))
	}

	// Registry COS instance. Only meaningful when the cluster is created;
	// the upstream count gate is (create_cluster && create_cos_instance).
	fmt.Fprintf(w, "create_roks_registry_cos_instance = %v\n", res.RegistryCOS.Create)
	if res.RegistryCOS.Create {
		fmt.Fprintf(w, "roks_cos_instance_name = %q\n", plan.COSInstanceName)
	} else if res.RegistryCOS.Existing != "" {
		fmt.Fprintf(w, "roks_cos_instance_name = %q\n", res.RegistryCOS.Existing)
	}

	// Transit gateway.
	fmt.Fprintf(w, "create_roks_transit_gateway = %v\n", res.TransitGateway.Create)
	if res.TransitGateway.Create {
		fmt.Fprintf(w, "roks_transit_gateway_name = %q\n", plan.TransitGatewayName)
	} else if res.TransitGateway.Existing != "" {
		fmt.Fprintf(w, "roks_transit_gateway_name = %q\n", res.TransitGateway.Existing)
	}

	// In-cluster services (no prefixed names; namespaces stay fixed).
	fmt.Fprintf(w, "install_cert_manager = %v\n", res.CertManager.Create)
	fmt.Fprintf(w, "deploy_bnk = %v\n", res.BNK.Create)

	// TGW test jumphost.
	fmt.Fprintf(w, "testing_create_tgw_jumphost = %v\n", res.TGWJumphost.Create)
	if res.TGWJumphost.Create {
		fmt.Fprintf(w, "testing_tgw_jumphost_name = %q\n", plan.TGWJumphostName)
	}

	// Client VPC (for the TGW jumphost).
	fmt.Fprintf(w, "testing_create_client_vpc = %v\n", res.ClientVPC.Create)
	if res.ClientVPC.Create {
		// A config-supplied name (resources.testing_client_vpc_name) wins over the
		// prefix-derived default.
		vpcName := plan.ClientVPCName
		if res.TestingClientVPCName != "" {
			vpcName = res.TestingClientVPCName
		}
		fmt.Fprintf(w, "testing_client_vpc_name = %q\n", vpcName)
	} else if res.ClientVPC.Existing != "" {
		fmt.Fprintf(w, "testing_client_vpc_name = %q\n", res.ClientVPC.Existing)
	}
	// Testing client region (chosen at init); empty → terraform default.
	if res.ClientRegion != "" {
		fmt.Fprintf(w, "testing_client_vpc_region = %q\n", res.ClientRegion)
	}

	renderSGCIDRFields(w, res)

	// IBM Cloud VPC SSH key attached to the jumphosts (resolved by `init`).
	if res.TestingSSHKeyName != "" {
		fmt.Fprintf(w, "testing_ssh_key_name = %q\n", res.TestingSSHKeyName)
	}
	// Jumphost sizing. An explicit profile wins; otherwise the min vCPU/memory
	// drive the auto-select. Emitted only when set → terraform defaults stand.
	if res.TestingJumphostProfile != "" {
		fmt.Fprintf(w, "testing_jumphost_profile = %q\n", res.TestingJumphostProfile)
	}
	if res.TestingMinVCPUCount > 0 {
		fmt.Fprintf(w, "testing_min_vcpu_count = %d\n", res.TestingMinVCPUCount)
	}
	if res.TestingMinMemoryGB > 0 {
		fmt.Fprintf(w, "testing_min_memory_gb = %d\n", res.TestingMinMemoryGB)
	}

	// Per-zone cluster jumphosts (the module appends -<zone> to the prefix).
	fmt.Fprintf(w, "testing_create_cluster_jumphosts = %v\n", res.ClusterJumphosts.Create)
	if res.ClusterJumphosts.Create {
		fmt.Fprintf(w, "testing_cluster_jumphost_name_prefix = %q\n", plan.ClusterJumphostPrefix)
	}

	if err := renderBNKFields(w, ws, mirror); err != nil {
		return err
	}
	renderGatewayFields(w, ws)
	return nil
}

// renderGatewayFields emits gateway_* overrides for the Gateway phase. Each is
// emitted only when config.yaml sets it; unset → the terraform gateway module's
// install-guide defaults apply. Harmless in the other phases' tfvars (their
// override forces deploy_gateway=false, so the gateway module is count=0).
func renderGatewayFields(w io.Writer, ws *config.Workspace) {
	g := ws.Gateway
	if g.AppNamespace != "" {
		fmt.Fprintf(w, "gateway_app_namespace = %q\n", g.AppNamespace)
	}
	if g.ClassName != "" {
		fmt.Fprintf(w, "gateway_class_name = %q\n", g.ClassName)
	}
	// Unset stays unset: an empty gateway_controller_name is the DERIVE sentinel
	// the module resolves against flo_namespace, not a missing value.
	if g.ControllerName != "" {
		fmt.Fprintf(w, "gateway_controller_name = %q\n", g.ControllerName)
	}
	if g.BackendService != "" {
		fmt.Fprintf(w, "gateway_backend_service = %q\n", g.BackendService)
	}
	if g.BackendPort != 0 {
		fmt.Fprintf(w, "gateway_backend_port = %d\n", g.BackendPort)
	}
	if g.EgressMode != "" {
		fmt.Fprintf(w, "gateway_egress_mode = %q\n", g.EgressMode)
	}
	if len(g.ClientSubnetLocal) > 0 {
		fmt.Fprintf(w, "gateway_client_subnet_local = %s\n", hclStringList(g.ClientSubnetLocal))
	}
	if len(g.ClientSubnetRemote) > 0 {
		fmt.Fprintf(w, "gateway_client_subnet_remote = %s\n", hclStringList(g.ClientSubnetRemote))
	}
	if g.VXLANPort != 0 {
		fmt.Fprintf(w, "gateway_vxlan_port = %d\n", g.VXLANPort)
	}
	if len(g.RouteExamples) > 0 {
		fmt.Fprintf(w, "gateway_route_examples = %s\n", hclStringList(g.RouteExamples))
	}
	if g.L4ListenerPort != 0 {
		fmt.Fprintf(w, "gateway_l4_listener_port = %d\n", g.L4ListenerPort)
	}
}

// hclStringList renders a string slice as an HCL list literal: ["a", "b"].
func hclStringList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// renderClusterSizing emits the worker-flavor auto-select minimums. Emitted
// only when set (0 → the terraform defaults stand), so old configs render
// byte-identically. Shared by both render modes; called inside the
// Cluster.Create branch since the minimums only drive flavor selection when the
// cluster is being created.
func renderClusterSizing(w io.Writer, c config.ClusterCfg) {
	if c.MinWorkerVCPUCount > 0 {
		fmt.Fprintf(w, "roks_min_worker_vcpu_count = %d\n", c.MinWorkerVCPUCount)
	}
	if c.MinWorkerMemoryGB > 0 {
		fmt.Fprintf(w, "roks_min_worker_memory_gb = %d\n", c.MinWorkerMemoryGB)
	}
	// Public-gateway toggle. Emitted only when explicitly set: nil → the terraform
	// default (true, current behavior); false → a private/disconnected cluster with
	// no worker Internet egress.
	if c.PublicGateway != nil {
		fmt.Fprintf(w, "cluster_public_gateway = %v\n", *c.PublicGateway)
	}
	// Cluster VPC addressing. Emitted only when set: empty leaves terraform on IBM's
	// "auto" prefixes, which is the pre-existing behaviour and — importantly — the
	// only one that produces no plan diff for a workspace whose subnets already
	// exist. Moving a live subnet's CIDR would replace it, and the cluster with it.
	if strings.TrimSpace(c.VPCCIDR) != "" {
		fmt.Fprintf(w, "cluster_vpc_cidr = %q\n", strings.TrimSpace(c.VPCCIDR))
	}
}

// renderBNKFields emits the BNK tuning fields shared by both render modes.
// Each is emitted only when set in config.yaml, so neither render path
// duplicates a variable.
//
// One section per renderer below, following the grouping the output already
// had: each renderer owns one block of related variables, so a change to the
// GTM fields touches renderBNKGTM and nothing else.
//
// Order is load-bearing — terraform tolerates any order, but a stable one
// keeps a rendered tfvars diffable across runs, so these are called in the
// same sequence the single function emitted them.
func renderBNKFields(w io.Writer, ws *config.Workspace, mirror *config.RegistryMirror) error {
	renderBNKNamespaces(w, ws)
	renderBNKTrustedProfile(w, ws)
	if err := renderBNKGTM(w, ws); err != nil {
		return err
	}
	renderBNKCertManager(w, ws)
	renderBNKCOS(w, ws)
	renderBNKDeployment(w, ws)
	if err := renderBNKRegistryMirror(w, ws, mirror); err != nil {
		return err
	}
	renderBNKSupplyChainFiles(w, ws)
	if err := renderBNKLocalSupplyChain(w, ws); err != nil {
		return err
	}
	renderBNKLicenseMode(w, ws)
	renderBNKFLP(w, ws)
	renderBNKNetwork(w, ws)
	renderBNKCIS(w, ws)
	return nil
}

// renderBNKNamespaces emits the FLO / utility namespaces.
func renderBNKNamespaces(w io.Writer, ws *config.Workspace) {
	// FLO / utility namespaces. Emitted only when set; unset leaves the
	// terraform defaults (f5-bnk / f5-utils). The GSLB datacenter name is NOT
	// emitted here — renderBNKGTM owns it, and emitting it from two renderers
	// would render the key twice, which terraform rejects at plan time
	// ("Attribute redefined") for the whole tfvars.
	if ws.BNK.FLONamespace != "" {
		fmt.Fprintf(w, "flo_namespace = %q\n", ws.BNK.FLONamespace)
	}
	if ws.BNK.FLOUtilsNamespace != "" {
		fmt.Fprintf(w, "flo_utils_namespace = %q\n", ws.BNK.FLOUtilsNamespace)
	}
}

// renderBNKTrustedProfile emits FLO's Trusted Profile service account and roles.
func renderBNKTrustedProfile(w io.Writer, ws *config.Workspace) {
	// Trusted Profile. Emitted only when set; absent leaves the HCL defaults,
	// which reproduce today's behaviour exactly — the service account derives
	// FLO's own long name rather than a static short one.
	if tp := ws.BNK.TrustedProfile; tp != nil {
		if sa := strings.TrimSpace(tp.ServiceAccount); sa != "" {
			fmt.Fprintf(w, "flo_trusted_profile_sa_name = %q\n", sa)
		}
		if len(tp.Roles) > 0 {
			// Trim and drop blanks first — a comma-separated env value like
			// "Viewer, Editor," would otherwise render a "" role, which
			// terraform accepts and IAM rejects. hclStringList only quotes, so
			// the filtering has to happen here rather than in the shared helper,
			// whose other callers do not want it.
			roles := make([]string, 0, len(tp.Roles))
			for _, r := range tp.Roles {
				if r = strings.TrimSpace(r); r != "" {
					roles = append(roles, r)
				}
			}
			if len(roles) > 0 {
				fmt.Fprintf(w, "flo_trusted_profile_roles = %s\n", hclStringList(roles))
			}
		}
	}
}

// renderBNKGTM emits the GTM / BIG-IP DNS connection (#51) and the GSLB
// datacenter name (emitted whenever set, even with no GTM URL).
func renderBNKGTM(w io.Writer, ws *config.Workspace) error {
	// GTM / BIG-IP DNS connection (#51). Emitted only when a URL is set, so a
	// workspace that does not use GSLB renders exactly what it did before.
	if g := ws.BNK.GTM; g != nil && strings.TrimSpace(g.URL) != "" {
		fmt.Fprintf(w, "cneinstance_gtm_url = %q\n", strings.TrimSpace(g.URL))
		if g.Username != "" {
			fmt.Fprintf(w, "cneinstance_gtm_username = %q\n", g.Username)
		}
		if g.PasswordB64 != "" {
			raw, err := base64.StdEncoding.DecodeString(g.PasswordB64)
			if err != nil {
				return fmt.Errorf("bnk.gtm.password_b64 is not valid base64: %w", err)
			}
			fmt.Fprintf(w, "cneinstance_gtm_password = %q\n", string(raw))
		}
	}

	if ws.BNK.GSLBDatacenterName != "" {
		fmt.Fprintf(w, "cneinstance_gslb_datacenter_name = %q\n", ws.BNK.GSLBDatacenterName)
	}
	return nil
}

// renderBNKCertManager emits cert-manager's namespace and chart version.
func renderBNKCertManager(w io.Writer, ws *config.Workspace) {
	// cert-manager namespace / chart version. Emitted only when a cert_manager
	// block is present; the install/skip toggle stays on resources.cert_manager.
	if cm := ws.BNK.CertManager; cm != nil {
		if cm.Namespace != "" {
			fmt.Fprintf(w, "cert_manager_namespace = %q\n", cm.Namespace)
		}
		if cm.Version != "" {
			fmt.Fprintf(w, "cert_manager_version = %q\n", cm.Version)
		}
	}
}

// renderBNKCOS emits the orchestration COS coordinates — the FAR auth key and JWT source.
func renderBNKCOS(w io.Writer, ws *config.Workspace) {
	// Orchestration COS coordinates (the FAR auth key + JWT source). Emitted only
	// when a cos block supplies them; unset leaves the terraform defaults. The
	// `registry` FAR resolver reads the same cos block, so a customer-owned bucket
	// is used consistently across terraform and roksbnkctl.
	if cos := ws.COS; cos != nil {
		if cos.Instance != "" {
			fmt.Fprintf(w, "ibmcloud_cos_instance_name = %q\n", cos.Instance)
		}
		if cos.Bucket != "" {
			fmt.Fprintf(w, "ibmcloud_resources_cos_bucket = %q\n", cos.Bucket)
		}
		if cos.Region != "" {
			fmt.Fprintf(w, "ibmcloud_cos_bucket_region = %q\n", cos.Region)
		}
	}
}

// renderBNKDeployment emits the CNE deployment size and the FAR repo URL —
// the base coordinates every deployment renders, mirror or not. Kept apart
// from renderBNKRegistryMirror below on purpose: far_repo_url is the fallback
// the modules' coalesce() lands on when the mirror record is absent or
// partial, so it must render regardless of what the mirror section decides.
func renderBNKDeployment(w io.Writer, ws *config.Workspace) {
	if ws.BNK.CNEInstanceSize != "" {
		fmt.Fprintf(w, "cneinstance_deployment_size = %q\n", ws.BNK.CNEInstanceSize)
	}
	if ws.BNK.FARRepoURL != "" {
		fmt.Fprintf(w, "far_repo_url = %q\n", ws.BNK.FARRepoURL)
	}
}

// renderBNKRegistryMirror emits the air-gap registry-mirror redirect.
func renderBNKRegistryMirror(w io.Writer, ws *config.Workspace, mirror *config.RegistryMirror) error {
	// Sprint 29 air-gap registry-mirror redirect. When a populated mirror
	// record exists, point chart pulls at the registry route (ChartHost) and
	// image pulls at the in-cluster registry service (ImageHost), and flip
	// use_registry_mirror so the modules drop the FAR dockerconfigjson secret
	// and render imagePullSecrets: [] (RBAC handles pulls). far_repo_url above
	// still renders untouched — the modules' coalesce() leaves it as the
	// fallback, so a missing/partial record is harmless. Emitted only when the
	// record carries both hosts, so an incomplete record never half-redirects.
	if mirror != nil && mirror.ChartHost != "" && mirror.ImageHost != "" {
		fmt.Fprintf(w, "far_chart_repo_url = %q\n", mirror.ChartHost)
		fmt.Fprintf(w, "far_image_repo_url = %q\n", mirror.ImageHost)
		fmt.Fprintln(w, "use_registry_mirror = true")

		// An external "generic" mirror (e.g. Harbor) is neither ICR nor the
		// in-cluster OpenShift registry, so chart+image pulls must authenticate
		// with its own basic-auth credentials (the same ones `registry replicate`
		// used). Emit them so the FLO/FLP modules log in to the mirror instead of
		// presenting the kube token. Absent for icr/in-cluster mirrors, which keep
		// their IAM-key / kube-token auth.
		if mirror.Target == "generic" && ws.Registry != nil && ws.Registry.GenericPasswordB64 != "" {
			// Fail loudly. Swallowing the decode error silently drops BOTH mirror
			// credential lines while still emitting use_registry_mirror = true, so the
			// modules fall back to the kube token and the run dies mid-apply with an
			// opaque 401 from `helm registry login` — with nothing anywhere saying the
			// mirror password never made it through.
			raw, derr := base64.StdEncoding.DecodeString(ws.Registry.GenericPasswordB64)
			if derr != nil {
				return fmt.Errorf("registry.generic_password_b64 is not valid base64 (%w) — "+
					"set it with `roksbnkctl registry target generic_password --password-stdin`, "+
					"which encodes it for you", derr)
			}
			fmt.Fprintf(w, "registry_mirror_username = %q\n", ws.Registry.GenericUsername)
			fmt.Fprintf(w, "registry_mirror_password = %q\n", string(raw))
		}
	}
	return nil
}

// renderBNKSupplyChainFiles emits the manifest version and the COS object
// names for the FAR auth tarball + subscription JWT. Every workspace that
// deploys BNK renders these — they were previously tail lines of the
// mirror-only renderer above, where a future early return on `mirror == nil`
// would have silently dropped them from every non-air-gapped workspace.
func renderBNKSupplyChainFiles(w io.Writer, ws *config.Workspace) {
	if ws.BNK.ManifestVersion != "" {
		fmt.Fprintf(w, "f5_bigip_k8s_manifest_version = %q\n", ws.BNK.ManifestVersion)
	}
	if ws.BNK.FarAuthFile != "" {
		fmt.Fprintf(w, "f5_cne_far_auth_file = %q\n", ws.BNK.FarAuthFile)
	}
	if ws.BNK.SubscriptionJWTFile != "" {
		fmt.Fprintf(w, "f5_cne_subscription_jwt_file = %q\n", ws.BNK.SubscriptionJWTFile)
	}
}

// renderBNKLocalSupplyChain emits the local-file supply chain, read here rather than downloaded.
func renderBNKLocalSupplyChain(w io.Writer, ws *config.Workspace) error {
	// Local-file supply chain (no COS). When both local files are set, read them
	// HERE (Go — no curl/tar/grep) and inject the FAR service account + JWT
	// content directly, disabling the COS download path. Fail loudly: the operator
	// explicitly pointed at these files, so an unreadable path is a config error,
	// not a silent fall-through to a COS bucket that may not exist.
	if far, jwt := ws.BNK.FarAuthLocalFile, ws.BNK.SubscriptionJWTLocalFile; far != "" && jwt != "" {
		saB64, err := source.ExtractServiceAccountFromTarball(far)
		if err != nil {
			return fmt.Errorf("reading FAR auth tarball %q (bnk.far_auth_local_file): %w", far, err)
		}
		jwtBytes, err := os.ReadFile(jwt)
		if err != nil {
			return fmt.Errorf("reading subscription JWT %q (bnk.subscription_jwt_local_file): %w", jwt, err)
		}
		fmt.Fprintln(w, "use_cos_bucket = false")
		fmt.Fprintf(w, "far_service_account_b64 = %q\n", strings.TrimSpace(saB64))
		fmt.Fprintf(w, "f5_cne_subscription_jwt = %q\n", strings.TrimSpace(string(jwtBytes)))
	}
	return nil
}

// renderBNKLicenseMode emits the license operation mode.
func renderBNKLicenseMode(w io.Writer, ws *config.Workspace) {
	// License operation mode. Emitted only when set; empty leaves the terraform
	// default ("connected"), keeping existing JWT configs byte-identical. The FLP
	// endpoint + root CA needed for "f5licenseproxy" mode are NOT rendered here —
	// they come from the flp phase outputs (flp-outputs.json), layered into the
	// bnk-phase override when `bnk up` runs in FLP mode.
	if ws.BNK.LicenseMode != "" {
		fmt.Fprintf(w, "license_mode = %q\n", ws.BNK.LicenseMode)
	}
}

// renderBNKFLP emits the F5 License Proxy phase settings.
func renderBNKFLP(w io.Writer, ws *config.Workspace) {
	// F5 License Proxy phase settings. Emitted only when an flp block is present;
	// deploy_flp itself is forced by the phase override (true for `flp up`, false
	// everywhere else), so these lines are harmless no-ops in the other phases.
	if flp := ws.BNK.FLP; flp != nil {
		if flp.Namespace != "" {
			fmt.Fprintf(w, "flp_namespace = %q\n", flp.Namespace)
		}
		if flp.ChartVersion != "" {
			fmt.Fprintf(w, "flp_chart_version = %q\n", flp.ChartVersion)
		}
		if flp.StorageClass != "" {
			fmt.Fprintf(w, "flp_storage_class = %q\n", flp.StorageClass)
		}
		// Expose the proxy outside its own cluster (the shared-licensing-cluster
		// topology). Emitted only when opted in, so the single-cluster render is
		// byte-identical.
		if flp.NodePortAccess {
			fmt.Fprintln(w, "flp_node_port_access = true")
		}
		if len(flp.NodePortSourceCIDRs) > 0 {
			fmt.Fprintf(w, "flp_node_port_source_cidrs = %s\n", hclStringList(flp.NodePortSourceCIDRs))
		}
		// mode: vsi — the standalone-VSI backend. The deploy_flp_vsi toggle itself is
		// forced by the FLP-phase override; these render the VSI's shape from config.
		if vsi := flp.VSI; vsi != nil {
			// Unset stays unset: an empty flp_vsi_name_prefix is the module's
			// LEGACY-NAMES sentinel, and emitting it explicitly would make a
			// future change to what "" means silently rename — i.e. replace —
			// every proxy that never asked for a prefix (#88).
			if vsi.NamePrefix != "" {
				fmt.Fprintf(w, "flp_vsi_name_prefix = %q\n", vsi.NamePrefix)
			}
			// The proxy's own network (#60) — a sibling of the shape settings below,
			// not nested under any of them: opting in without also pinning a profile
			// is the common case, and the terraform defaults cover the rest.
			if vsi.CreateVPC {
				fmt.Fprintln(w, "flp_vsi_create_vpc = true")
				// vpc and create_vpc are mutually exclusive, and the failure mode of
				// letting both through is silent: create_vpc wins in the module (the
				// data source is count=0), so the proxy comes up in a NEW VPC with no
				// route to the clusters meant to license against it, while the config
				// still names the VPC the operator intended. The obvious migration —
				// add create_vpc, forget to remove vpc — walks straight into it.
				if vsi.VPCName != "" {
					fmt.Fprintf(w, "flp_vsi_vpc_name = %q\n", vsi.VPCName)
				}
				if vsi.SubnetCIDR != "" {
					fmt.Fprintf(w, "flp_vsi_subnet_cidr = %q\n", vsi.SubnetCIDR)
				}
			}
			if vsi.Profile != "" {
				fmt.Fprintf(w, "flp_vsi_profile = %q\n", vsi.Profile)
			}
			if vsi.Zone != "" {
				fmt.Fprintf(w, "flp_vsi_zone = %q\n", vsi.Zone)
			}
			if vsi.BootSizeGB > 0 {
				fmt.Fprintf(w, "flp_vsi_boot_size_gb = %d\n", vsi.BootSizeGB)
			}
			if vsi.Reach != "" {
				fmt.Fprintf(w, "flp_vsi_reach = %q\n", vsi.Reach)
			}
			// Per-plane SG source CIDRs: management (:80, default open) + licensing
			// (:8443/:22, default RFC-1918). The legacy allowed_cidrs seeds both when set.
			if len(vsi.ManagementAllowedCIDRs) > 0 {
				fmt.Fprintf(w, "flp_vsi_management_allowed_cidrs = %s\n", hclStringList(vsi.ManagementAllowedCIDRs))
			}
			if len(vsi.LicensingAllowedCIDRs) > 0 {
				fmt.Fprintf(w, "flp_vsi_licensing_allowed_cidrs = %s\n", hclStringList(vsi.LicensingAllowedCIDRs))
			}
			if len(vsi.AllowedCIDRs) > 0 {
				fmt.Fprintf(w, "flp_vsi_allowed_cidrs = %s\n", hclStringList(vsi.AllowedCIDRs))
			}
			if vsi.SSHKey != "" {
				fmt.Fprintf(w, "flp_vsi_ssh_key = %q\n", vsi.SSHKey)
			}
			// Operator floating IP (management access). nil → the tf default (true);
			// render only when explicitly set so opting out (false) is honored.
			if vsi.FloatingIP != nil {
				fmt.Fprintf(w, "flp_vsi_floating_ip = %t\n", *vsi.FloatingIP)
			}
			// flp-status web UI (optional): image + mirror-trust so the VSI can pull it.
			if vsi.StatusImage != "" {
				fmt.Fprintf(w, "flp_status_image = %q\n", vsi.StatusImage)
			}
			if vsi.StatusRegistryHost != "" {
				fmt.Fprintf(w, "flp_status_registry_host = %q\n", vsi.StatusRegistryHost)
			}
			if vsi.StatusRegistryCAB64 != "" {
				fmt.Fprintf(w, "flp_status_registry_ca_b64 = %q\n", vsi.StatusRegistryCAB64)
			}
			if fp := vsi.ForwardProxy; fp != nil {
				if fp.Host != "" {
					fmt.Fprintf(w, "flp_forward_proxy_host = %q\n", fp.Host)
				}
				if fp.Port > 0 {
					fmt.Fprintf(w, "flp_forward_proxy_port = %d\n", fp.Port)
				}
				if fp.Protocol != "" {
					fmt.Fprintf(w, "flp_forward_proxy_protocol = %q\n", fp.Protocol)
				}
			}
		}
	}
}

// renderBNKNetwork emits the cloud-network mapping, VLAN zones and TMM VLAN/route knobs.
func renderBNKNetwork(w io.Writer, ws *config.Workspace) {
	// Cloud-network-mapping + VLAN zones + TMM VLAN/route knobs (BNK install-guide
	// "Configuration"). Each is emitted only when config.yaml supplies it; absent →
	// the terraform module's install-guide defaults apply (existing configs unchanged).
	if net := ws.BNK.Network; net != nil {
		if len(net.Zones) > 0 {
			renderNetworkZones(w, net.Zones)
		}
		if net.VLANPrefixLen != nil {
			fmt.Fprintf(w, "cneinstance_vlan_prefixlen = %d\n", *net.VLANPrefixLen)
		}
		// Emitted only when set; the HCL reads 0 as "inherit the shared value", so
		// an unset per-VLAN override renders nothing and changes nothing.
		if net.VLANPrefixLenExternal != nil {
			fmt.Fprintf(w, "cneinstance_vlan_prefixlen_external = %d\n", *net.VLANPrefixLenExternal)
		}
		if net.VLANPrefixLenInternal != nil {
			fmt.Fprintf(w, "cneinstance_vlan_prefixlen_internal = %d\n", *net.VLANPrefixLenInternal)
		}
		if net.TMMK8SRoutes != "" {
			fmt.Fprintf(w, "cneinstance_tmm_k8s_routes = %q\n", net.TMMK8SRoutes)
		}
	}
}

// renderBNKCIS emits the BNK CIS controller's BIG-IP target.
func renderBNKCIS(w io.Writer, ws *config.Workspace) {
	// BNK CIS controller's BIG-IP target. Emitted only when configured; absent →
	// the bigip_* vars stay at their terraform defaults (blank = BNK without CIS).
	if cis := ws.BNK.CIS; cis != nil {
		if cis.BigIPURL != "" {
			fmt.Fprintf(w, "bigip_url = %q\n", cis.BigIPURL)
		}
		if cis.BigIPUsername != "" {
			fmt.Fprintf(w, "bigip_username = %q\n", cis.BigIPUsername)
		}
		if cis.BigIPPasswordB64 != "" {
			if raw, derr := base64.StdEncoding.DecodeString(cis.BigIPPasswordB64); derr == nil {
				fmt.Fprintf(w, "bigip_password = %q\n", string(raw))
			}
		}
	}
}

// renderNetworkZones emits the cneinstance_network_zones HCL list-of-objects
// from the bnk.network.zones config block. Field names match the terraform
// object type exactly so the render is a drop-in override.
func renderNetworkZones(w io.Writer, zones []config.BNKZoneCfg) {
	fmt.Fprintln(w, "cneinstance_network_zones = [")
	for _, z := range zones {
		fmt.Fprintln(w, "  {")
		fmt.Fprintf(w, "    ext_vlan_cidr   = %q\n", z.ExtVLANCIDR)
		fmt.Fprintf(w, "    int_vlan_cidr   = %q\n", z.IntVLANCIDR)
		fmt.Fprintf(w, "    int_snat_cidr   = %q\n", z.IntSNATCIDR)
		fmt.Fprintf(w, "    int_vip_cidr    = %q\n", z.IntVIPCIDR)
		fmt.Fprintf(w, "    external_selfip = %q\n", z.ExternalSelfIP)
		fmt.Fprintf(w, "    internal_selfip = %q\n", z.InternalSelfIP)
		fmt.Fprintln(w, "  },")
	}
	fmt.Fprintln(w, "]")
}
