package tf

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/*.golden from the current render")

// #117. renderBNKFields was 298 lines emitting eleven unrelated groups of
// variables. Splitting it is only safe if the output is provably unchanged, and
// "the existing tests still pass" does not prove that — they assert individual
// lines, so a section dropped wholesale, emitted twice, or moved past another
// would slip through every one of them.
//
// This pins the WHOLE body byte-for-byte against a workspace that populates
// every section, which is the only assertion that can catch a reordering or an
// omission. It is also the guard for the next such change: a refactor that
// alters the render fails here with a diff rather than in someone's terraform.
func TestRenderTFVarsMatchesTheGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTFVars(&buf, fullyPopulatedWorkspace(), goldenMirror(), "/kdir", "/sdir"); err != nil {
		t.Fatalf("renderTFVars: %v", err)
	}

	path := filepath.Join("testdata", "render_full.golden")
	if *updateGolden {
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden (regenerate with -update-golden): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("the rendered tfvars body changed.\n"+
			"If that is intended, re-run with -update-golden and review the diff in the commit.\n\n"+
			"--- want ---\n%s\n--- got ---\n%s", want, buf.String())
	}
}

// Every section must actually appear, or the golden pins an empty render and
// proves nothing. This is the check that the fixture below still exercises what
// it claims to: a field renamed out from under it would otherwise leave a
// section silently unpopulated and the golden happily green.
func TestTheGoldenFixtureExercisesEverySection(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTFVars(&buf, fullyPopulatedWorkspace(), goldenMirror(), "/kdir", "/sdir"); err != nil {
		t.Fatal(err)
	}
	body := buf.String()

	// One representative variable per renderBNK* section.
	for section, marker := range map[string]string{
		"renderBNKNamespaces":     "flo_namespace",
		"renderBNKTrustedProfile": "flo_trusted_profile_sa_name",
		"renderBNKGTM":            "gtm_url",
		"renderBNKCertManager":    "cert_manager_namespace",
		"renderBNKCOS":            "ibmcloud_resources_cos_bucket",
		"renderBNKRegistryMirror": "use_registry_mirror",
		"renderBNKLicenseMode":    "license_mode",
		"renderBNKFLP":            "flp_namespace",
		"renderBNKNetwork":        "cneinstance_network_zones",
		"renderBNKCIS":            "bigip_url",
	} {
		if !contains(body, marker) {
			t.Errorf("%s contributed nothing to the golden (no %q) — the fixture no longer "+
				"exercises it, so the golden cannot detect a change there", section, marker)
		}
	}
}

func contains(hay, needle string) bool { return bytes.Contains([]byte(hay), []byte(needle)) }

// fullyPopulatedWorkspace sets every field the bnk.* renderers read, so the
// golden covers all of them. A section left unset renders nothing and would be
// pinned as absent.
func fullyPopulatedWorkspace() *config.Workspace {
	tr := true
	vlanLen := 24
	ws := &config.Workspace{Prefix: "gold", Resources: config.DefaultResources()}
	ws.IBMCloud.Region = "us-south"
	ws.COS = &config.COSCfg{Instance: "cos-inst", Bucket: "bnk-artifacts", Region: "us-south"}
	ws.BNK.FLONamespace = "f5-bnk-x"
	ws.BNK.FLOUtilsNamespace = "f5-utils-x"
	ws.BNK.GSLBDatacenterName = "dc1"
	ws.BNK.CNEInstanceSize = "small"
	ws.BNK.ManifestVersion = "v2.3.0"
	ws.BNK.FARRepoURL = "repo.f5.com"
	ws.BNK.LicenseMode = "f5licenseproxy"
	ws.BNK.TrustedProfile = &config.BNKTrustedProfileCfg{
		ServiceAccount: "sa",
		// A blank and a padded entry: the renderer filters both, and the golden
		// is where that filtering is visible.
		Roles: []string{"Viewer", " Editor ", ""},
	}
	ws.BNK.GTM = &config.BNKGTMCfg{URL: "https://gtm", Username: "u", PasswordB64: "cHc="}
	ws.BNK.CertManager = &config.BNKCertManagerCfg{Namespace: "cm", Version: "v1.2.3"}
	ws.BNK.CIS = &config.BNKCISCfg{BigIPURL: "https://1.2.3.4", BigIPUsername: "admin", BigIPPasswordB64: "cHc="}
	ws.BNK.FLP = &config.BNKFLPCfg{
		Mode: "vsi", Namespace: "flp-ns", ChartVersion: "v1", StorageClass: "sc",
		VSI: &config.BNKFLPVSICfg{
			NamePrefix: "flp", VPC: "vpc", Zone: "z", BootSizeGB: 100, Reach: "public",
			SSHKey: "k", FloatingIP: &tr, Profile: "bx2-2x8",
			ManagementAllowedCIDRs: []string{"10.0.0.0/8"},
			LicensingAllowedCIDRs:  []string{"10.1.0.0/16"},
		},
	}
	ws.BNK.Network = &config.BNKNetworkCfg{
		VLANPrefixLen: &vlanLen,
		Zones: []config.BNKZoneCfg{{
			ExtVLANCIDR: "10.0.1.0/24", IntVLANCIDR: "10.0.2.0/24",
			IntSNATCIDR: "10.0.3.0/24", IntVIPCIDR: "10.0.4.0/24",
			ExternalSelfIP: "10.0.1.5", InternalSelfIP: "10.0.2.5",
		}},
	}
	ws.Gateway = config.GatewayCfg{
		AppNamespace: "apps", ClassName: "gc", ControllerName: "cn",
		BackendService: "svc", BackendPort: 80, EgressMode: "snat",
		RouteExamples: []string{"GRPCRoute", "L4Route"}, L4ListenerPort: 8080,
	}
	return ws
}

func goldenMirror() *config.RegistryMirror {
	return &config.RegistryMirror{
		Target: "generic", Namespace: "bnk-mirror",
		ChartHost: "h/bnk-mirror", ImageHost: "h/bnk-mirror",
		RegistryHost: "h", CACert: "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n",
	}
}
