package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/naming"
)

func TestRenderTFVars_CreateMode(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster: config.ClusterCfg{
			Create:           true,
			Name:             "bnk-demo",
			OpenShiftVersion: "4.18",
			WorkersPerZone:   2,
		},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}

	want := []string{
		`ibmcloud_cluster_region = "us-south"`,
		`ibmcloud_resource_group = "default"`,
		`create_roks_cluster = true`,
		`openshift_cluster_name = "bnk-demo"`,
		`openshift_cluster_version = "4.18"`,
		`roks_workers_per_zone = 2`,
	}
	out := buf.String()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing line: %s\noutput:\n%s", w, out)
		}
	}

	// Critical safety check: no api_key field (env-var path is mandatory).
	if strings.Contains(out, "api_key") {
		t.Errorf("api_key leaked into tfvars; must be passed via env var only.\noutput:\n%s", out)
	}
}

func TestRenderTFVars_AttachMode(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		Cluster:  config.ClusterCfg{Create: false, Name: "existing-cluster"},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `roks_cluster_id_or_name = "existing-cluster"`) {
		t.Errorf("attach mode missing roks_cluster_id_or_name\noutput:\n%s", out)
	}
	if strings.Contains(out, "openshift_cluster_name") {
		t.Errorf("attach mode should not emit openshift_cluster_name\noutput:\n%s", out)
	}
	if !strings.Contains(out, "create_roks_cluster = false") {
		t.Errorf("missing create_roks_cluster = false\noutput:\n%s", out)
	}
}

func TestRenderTFVars_OmitsEmptyFields(t *testing.T) {
	ws := &config.Workspace{
		Cluster: config.ClusterCfg{Create: true, Name: "demo"},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Region/RG were unset — should not appear.
	if strings.Contains(out, "ibmcloud_cluster_region") {
		t.Errorf("region should be omitted when empty\noutput:\n%s", out)
	}
}

func TestRenderTFVars_KubeconfigDir(t *testing.T) {
	ws := &config.Workspace{
		Cluster: config.ClusterCfg{Create: true, Name: "demo"},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "/home/user/.roksbnkctl/default/state/kubeconfig", "/home/user/.roksbnkctl/default/state/scratch"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`kubeconfig_dir = "/home/user/.roksbnkctl/default/state/kubeconfig"`,
		`scratch_dir = "/home/user/.roksbnkctl/default/state/scratch"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\noutput:\n%s", want, out)
		}
	}

	// Empty strings should NOT emit the lines — keeps tfvars clean for
	// callers that don't want this rendering.
	buf.Reset()
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"kubeconfig_dir", "scratch_dir"} {
		if strings.Contains(buf.String(), k) {
			t.Errorf("empty %s should not emit a line\noutput:\n%s", k, buf.String())
		}
	}
}

func TestRenderTFVars_BNKFields(t *testing.T) {
	ws := &config.Workspace{
		Cluster: config.ClusterCfg{Create: true, Name: "demo"},
		BNK: config.BNKCfg{
			CNEInstanceSize: "Medium",
			FARRepoURL:      "repo.f5.com",
			ManifestVersion: "2.3.0-foo",
		},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	want := []string{
		`cneinstance_deployment_size = "Medium"`,
		`far_repo_url = "repo.f5.com"`,
		`f5_bigip_k8s_manifest_version = "2.3.0-foo"`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing: %s", w)
		}
	}
}

// TestRenderTFVars_NetworkZones: a bnk.network.zones block renders the
// cneinstance_network_zones HCL list-of-objects with matching field names.
func TestRenderTFVars_NetworkZones(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		BNK: config.BNKCfg{
			Network: &config.BNKNetworkCfg{
				Zones: []config.BNKZoneCfg{
					{ExtVLANCIDR: "10.155.15.0/24", IntVLANCIDR: "10.254.99.0/24", IntSNATCIDR: "10.10.11.0/24", IntVIPCIDR: "10.135.15.0/24", ExternalSelfIP: "10.155.15.101", InternalSelfIP: "10.254.99.101"},
					{ExtVLANCIDR: "10.156.16.0/24", IntVLANCIDR: "10.254.100.0/24", IntSNATCIDR: "10.10.21.0/24", IntVIPCIDR: "10.136.16.0/24", ExternalSelfIP: "10.156.16.101", InternalSelfIP: "10.254.100.101"},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	for _, w := range []string{
		"cneinstance_network_zones = [",
		`ext_vlan_cidr   = "10.155.15.0/24"`,
		`int_vlan_cidr   = "10.254.100.0/24"`,
		`external_selfip = "10.156.16.101"`,
		`internal_selfip = "10.254.99.101"`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing line: %s\noutput:\n%s", w, out)
		}
	}
}

// TestRenderTFVars_NetworkZones_OmittedWhenNil: no bnk.network → nothing
// emitted, so the terraform module's install-guide defaults apply.
func TestRenderTFVars_NetworkZones_OmittedWhenNil(t *testing.T) {
	ws := &config.Workspace{IBMCloud: config.IBMCloudCfg{Region: "us-south"}}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	if strings.Contains(buf.String(), "cneinstance_network_zones") {
		t.Errorf("network zones emitted when config has none — should defer to terraform defaults")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Sprint 26 — full prefix-driven render (validator Issue 1, item 2).
//
// When ws.Prefix != "" the renderer emits the COMPLETE de-duplicated name
// set derived from the prefix via internal/naming.Derive, alongside each
// resource's create/reuse toggle. These cases pin:
//   - every derived name + create_* toggle present EXACTLY ONCE
//     (no duplicate variable lines — terraform rejects in-file dupes),
//   - no upstream tf-* module default name leaks,
//   - the existing-resource path (create_* = false + *_name = "<existing>"),
//   - the api key is never rendered.
// The legacy sparse path (Prefix == "") is pinned byte-unchanged below.
// ─────────────────────────────────────────────────────────────────────

// varName extracts the left-hand-side variable name from a "name = value"
// tfvars line, or "" if the line is not an assignment (blank/comment).
func varName(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	if i := strings.Index(line, "="); i > 0 {
		return strings.TrimSpace(line[:i])
	}
	return ""
}

// assertEachVarOnce fails if any tfvars variable is assigned more than once
// in out (an in-file duplicate is a terraform error).
func assertEachVarOnce(t *testing.T, out string) {
	t.Helper()
	seen := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		if name := varName(line); name != "" {
			seen[name]++
		}
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("variable %q assigned %d times; each tfvars variable must appear exactly once\noutput:\n%s", name, n, out)
		}
	}
}

// fullRenderWorkspace is an all-create prefix-driven workspace.
func fullRenderWorkspace(prefix string) *config.Workspace {
	return &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster: config.ClusterCfg{
			Create:           true,
			Name:             prefix, // staff derives this from prefix; mirror it
			OpenShiftVersion: "4.18",
			WorkersPerZone:   2,
		},
		Prefix: prefix,
		Resources: &config.ResourcesCfg{
			TransitGateway:   config.ResourceToggle{Create: true},
			RegistryCOS:      config.ResourceToggle{Create: true},
			CertManager:      config.ResourceToggle{Create: true},
			BNK:              config.ResourceToggle{Create: true},
			TGWJumphost:      config.ResourceToggle{Create: true},
			ClusterJumphosts: config.ResourceToggle{Create: true},
			ClientVPC:        config.ResourceToggle{Create: true},
		},
	}
}

func TestRenderTFVars_FullPrefixRender_AllCreate(t *testing.T) {
	const prefix = "demo"
	ws := fullRenderWorkspace(prefix)
	plan := naming.Derive(prefix)

	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()

	// Every derived name is rendered under its tfvars variable, derived
	// from naming.Derive (not hard-coded) so the test tracks the scheme.
	wantNames := []string{
		`openshift_cluster_name = "` + plan.ClusterName + `"`,
		`roks_cluster_vpc_name = "` + plan.ClusterVPCName + `"`,
		`roks_cos_instance_name = "` + plan.COSInstanceName + `"`,
		`roks_transit_gateway_name = "` + plan.TransitGatewayName + `"`,
		`testing_tgw_jumphost_name = "` + plan.TGWJumphostName + `"`,
		`testing_client_vpc_name = "` + plan.ClientVPCName + `"`,
		`testing_cluster_jumphost_name_prefix = "` + plan.ClusterJumphostPrefix + `"`,
	}
	for _, w := range wantNames {
		if !strings.Contains(out, w) {
			t.Errorf("full render missing derived name line: %s\noutput:\n%s", w, out)
		}
	}

	// Every create_* toggle present (true for all-create).
	wantToggles := []string{
		`create_roks_cluster = true`,
		`create_roks_registry_cos_instance = true`,
		`create_roks_transit_gateway = true`,
		`install_cert_manager = true`,
		`deploy_bnk = true`,
		`testing_create_tgw_jumphost = true`,
		`testing_create_client_vpc = true`,
		`testing_create_cluster_jumphosts = true`,
	}
	for _, w := range wantToggles {
		if !strings.Contains(out, w) {
			t.Errorf("full render missing toggle line: %s\noutput:\n%s", w, out)
		}
	}

	// No upstream module default names leak (the collision class this
	// sprint closes). The defaults are all tf-* prefixed.
	for _, leak := range []string{"tf-cluster-vpc", "tf-openshift-cluster", "tf-tgw", "tf-testing", "tf-registry"} {
		if strings.Contains(out, leak) {
			t.Errorf("full render leaked upstream default %q; names must be prefix-derived\noutput:\n%s", leak, out)
		}
	}
	// Belt-and-braces: no bare "tf-" token at all in a rendered value.
	if strings.Contains(out, `"tf-`) {
		t.Errorf("full render contains a tf-* default name value\noutput:\n%s", out)
	}

	// Each variable exactly once — an in-file duplicate is a terraform error.
	assertEachVarOnce(t, out)

	// The api key must NEVER be rendered.
	if strings.Contains(out, "api_key") {
		t.Errorf("api_key leaked into full-render tfvars; env-var path is mandatory\noutput:\n%s", out)
	}
}

// TestRenderTFVars_FullPrefixRender_ExistingResources pins the
// declined-but-depended-on path: a create_* = false toggle pairs with the
// operator's existing-resource name in the matching *_name variable, and the
// derived name is NOT used for that resource.
func TestRenderTFVars_FullPrefixRender_ExistingResources(t *testing.T) {
	const prefix = "demo"
	plan := naming.Derive(prefix)
	ws := fullRenderWorkspace(prefix)
	// Decline TGW, registry COS, and client VPC, supplying existing names.
	ws.Resources.TransitGateway = config.ResourceToggle{Create: false, Existing: "shared-tgw"}
	ws.Resources.RegistryCOS = config.ResourceToggle{Create: false, Existing: "shared-cos"}
	ws.Resources.ClientVPC = config.ResourceToggle{Create: false, Existing: "shared-client-vpc"}

	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()

	wantPairs := []struct{ toggle, name string }{
		{`create_roks_transit_gateway = false`, `roks_transit_gateway_name = "shared-tgw"`},
		{`create_roks_registry_cos_instance = false`, `roks_cos_instance_name = "shared-cos"`},
		{`testing_create_client_vpc = false`, `testing_client_vpc_name = "shared-client-vpc"`},
	}
	for _, p := range wantPairs {
		if !strings.Contains(out, p.toggle) {
			t.Errorf("existing-resource render missing toggle %q\noutput:\n%s", p.toggle, out)
		}
		if !strings.Contains(out, p.name) {
			t.Errorf("existing-resource render missing existing name %q\noutput:\n%s", p.name, out)
		}
	}

	// The DERIVED names for the declined resources must NOT appear — the
	// operator's existing name wins.
	for _, derived := range []string{plan.TransitGatewayName, plan.COSInstanceName, plan.ClientVPCName} {
		if strings.Contains(out, `"`+derived+`"`) {
			t.Errorf("declined resource still rendered its derived name %q; the existing name must win\noutput:\n%s", derived, out)
		}
	}

	assertEachVarOnce(t, out)
	if strings.Contains(out, "api_key") {
		t.Errorf("api_key leaked into existing-resource render\noutput:\n%s", out)
	}
}

// TestRenderTFVars_LegacySparse_ByteUnchanged is the backward-compat pin:
// an empty-Prefix (pre-Sprint-26) workspace must render the OLD sparse body
// byte-for-byte, so existing workspaces are unaffected by the full-render
// addition. The expected bytes are spelled out literally (not regenerated
// from the renderer) so a regression in the sparse path is caught against a
// fixed baseline rather than against itself.
func TestRenderTFVars_LegacySparse_ByteUnchanged(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster: config.ClusterCfg{
			Create:           true,
			Name:             "bnk-demo",
			OpenShiftVersion: "4.18",
			WorkersPerZone:   2,
		},
		// Prefix intentionally empty → legacy sparse render.
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}

	const want = `# Generated by roksbnkctl. Do not edit by hand — run ` + "`roksbnkctl init`" + ` to update.
# IBMCLOUD_API_KEY is NOT written here; it's passed via TF_VAR env var.

ibmcloud_cluster_region = "us-south"
ibmcloud_resource_group = "default"
create_roks_cluster = true
openshift_cluster_name = "bnk-demo"
openshift_cluster_version = "4.18"
roks_workers_per_zone = 2
`
	if got := buf.String(); got != want {
		t.Errorf("legacy sparse render is NOT byte-unchanged\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}

	// Sanity: a legacy render emits none of the prefix-only variables.
	for _, prefixOnly := range []string{
		"roks_cluster_vpc_name",
		"roks_transit_gateway_name",
		"create_roks_transit_gateway",
		"testing_cluster_jumphost_name_prefix",
	} {
		if strings.Contains(buf.String(), prefixOnly) {
			t.Errorf("legacy sparse render leaked prefix-only variable %q", prefixOnly)
		}
	}
}

// TestRenderTFVars_LegacyAttach_ByteUnchanged pins the existing-cluster
// (attach) legacy sparse body byte-for-byte too, since the attach branch is
// a distinct code path from the create branch above.
func TestRenderTFVars_LegacyAttach_ByteUnchanged(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		Cluster:  config.ClusterCfg{Create: false, Name: "existing-cluster"},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	const want = `# Generated by roksbnkctl. Do not edit by hand — run ` + "`roksbnkctl init`" + ` to update.
# IBMCLOUD_API_KEY is NOT written here; it's passed via TF_VAR env var.

ibmcloud_cluster_region = "us-south"
create_roks_cluster = false
roks_cluster_id_or_name = "existing-cluster"
`
	if got := buf.String(); got != want {
		t.Errorf("legacy attach render is NOT byte-unchanged\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}
