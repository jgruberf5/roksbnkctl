package tf

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
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
// This pins the body byte-for-byte against a workspace that populates every
// section, on BOTH render paths: the prefix-driven full body and the legacy
// empty-Prefix sparse body, which also calls renderBNKFields and whose
// byte-identity is just as load-bearing. It is also the guard for the next
// such change: a refactor that alters the render fails here with a diff rather
// than in someone's terraform.
func TestRenderTFVarsMatchesTheGolden(t *testing.T) {
	for name, ws := range map[string]*config.Workspace{
		"render_full.golden":   fullyPopulatedWorkspace(t),
		"render_sparse.golden": sparseWorkspace(t),
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderTFVars(&buf, ws, goldenMirror(), "/kdir", "/sdir"); err != nil {
				t.Fatalf("renderTFVars: %v", err)
			}

			path := filepath.Join("testdata", name)
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
		})
	}
}

// Every section must actually appear, or the golden pins an empty render and
// proves nothing. This is the check that the fixture below still exercises what
// it claims to: a field renamed out from under it would otherwise leave a
// section silently unpopulated and the golden happily green.
//
// A marker also covers a section's conditional branch when that branch is its
// own failure mode: the mirror credentials render only for a generic mirror
// with a stored password, and dropping just those two lines while keeping
// use_registry_mirror is precisely the regression (an opaque `helm registry
// login` 401 mid-apply) the in-code comment says the branch exists to prevent.
func TestTheGoldenFixtureExercisesEverySection(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTFVars(&buf, fullyPopulatedWorkspace(t), goldenMirror(), "/kdir", "/sdir"); err != nil {
		t.Fatal(err)
	}
	body := buf.String()

	// One representative variable per renderBNK* section (plus the credential
	// branch of the mirror section).
	for section, marker := range map[string]string{
		"renderBNKNamespaces":     "flo_namespace",
		"renderBNKTrustedProfile": "flo_trusted_profile_sa_name",
		// renderBNKGTM emitted the GTM connection until #227 removed it as inert;
		// what remains is the datacenter name, which IS read by the controller.
		"renderBNKGTM":                    "gslb_datacenter_name",
		"renderBNKCertManager":            "cert_manager_namespace",
		"renderBNKCOS":                    "ibmcloud_resources_cos_bucket",
		"renderBNKDeployment":             "cneinstance_deployment_size",
		"renderBNKRegistryMirror":         "use_registry_mirror",
		"renderBNKRegistryMirror (creds)": "registry_mirror_username",
		"renderBNKSupplyChainFiles":       "f5_bigip_k8s_manifest_version",
		"renderBNKLocalSupplyChain":       "use_cos_bucket = false",
		"renderBNKLicenseMode":            "license_mode",
		"renderBNKFLP":                    "flp_namespace",
		"renderBNKNetwork":                "cneinstance_network_zones",
		"renderBNKCIS":                    "bigip_url",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("%s contributed nothing to the golden (no %q) — the fixture no longer "+
				"exercises it, so the golden cannot detect a change there", section, marker)
		}
	}
}

// fullyPopulatedWorkspace sets every field the bnk.* renderers read, so the
// golden covers all of them. A section left unset renders nothing and would be
// pinned as absent.
//
// The cluster/resources half comes from fullRenderWorkspace — the package's
// one definition of an all-create workspace — so a field added there reaches
// this golden without a second fixture needing to remember it.
func fullyPopulatedWorkspace(t *testing.T) *config.Workspace {
	t.Helper()
	tr := true
	vlanLen := 24
	ws := fullRenderWorkspace("gold")
	ws.COS = &config.COSCfg{Instance: "cos-inst", Bucket: "bnk-artifacts", Region: "us-south"}
	ws.BNK.FLONamespace = "f5-bnk-x"
	ws.BNK.FLOUtilsNamespace = "f5-utils-x"
	ws.BNK.GSLBDatacenterName = "dc1"
	ws.BNK.CNEInstanceSize = "small"
	// A REAL manifest version, not "v2.3.0". The published strings carry no "v"
	// (2.3.0-3.2598.3-0.0.170, 2.4.0-EA), and the line regex is anchored to a
	// leading digit — so the synthetic value parsed to no line at all and this
	// fixture silently exercised none of the per-line rendering.
	ws.BNK.ManifestVersion = config.DefaultManifestVersion
	ws.BNK.FARRepoURL = "repo.f5.com"
	ws.BNK.FarAuthFile = "far-auth.tgz"
	ws.BNK.SubscriptionJWTFile = "subscription.jwt"
	ws.BNK.LicenseMode = "f5licenseproxy"
	// The local-file supply chain reads real files at render time, so the
	// fixture materialises them. Their CONTENT is fixed — it lands in the
	// golden (far_service_account_b64 / f5_cne_subscription_jwt) — while their
	// paths land nowhere, so the per-run t.TempDir keeps the golden stable.
	ws.BNK.FarAuthLocalFile = writeFARAuthTarball(t, "eyJzYSI6ImdvbGQifQ==")
	ws.BNK.SubscriptionJWTLocalFile = writeTempFile(t, "subscription.jwt", "jwt-gold\n")
	ws.BNK.TrustedProfile = &config.BNKTrustedProfileCfg{
		ServiceAccount: "sa",
		// A blank and a padded entry: the renderer filters both, and the golden
		// is where that filtering is visible.
		Roles: []string{"Viewer", " Editor ", ""},
	}
	ws.BNK.CertManager = &config.BNKCertManagerCfg{Namespace: "cm", Version: "v1.2.3"}
	ws.BNK.CIS = &config.BNKCISCfg{BigIPURL: "https://1.2.3.4", BigIPUsername: "admin", BigIPPasswordB64: "cHc="}
	ws.BNK.FLP = &config.BNKFLPCfg{
		Mode: "vsi", Namespace: "flp-ns", ChartVersion: "v1", StorageClass: "sc",
		VSI: &config.BNKFLPVSICfg{
			NamePrefix: "flp", VPC: "vpc", Zone: "z", BootSizeGB: 100,
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
	// The mirror-credential branch (goldenMirror is a generic target): without
	// these the only error path in renderBNKRegistryMirror never renders, and
	// the golden cannot see the registry_mirror_* lines.
	ws.Registry = &config.RegistryCfg{
		GenericUsername:    "mirror-admin",
		GenericPasswordB64: "bWlycm9yLXB3", // "mirror-pw"
	}
	return ws
}

// sparseWorkspace is the same fixture on the legacy empty-Prefix path, which
// dispatches to renderSparseBody instead of renderFullBody.
func sparseWorkspace(t *testing.T) *config.Workspace {
	t.Helper()
	ws := fullyPopulatedWorkspace(t)
	ws.Prefix = ""
	return ws
}

func goldenMirror() *config.RegistryMirror {
	return &config.RegistryMirror{
		Target: "generic", Namespace: "bnk-mirror",
		ChartHost: "h/bnk-mirror", ImageHost: "h/bnk-mirror",
		RegistryHost: "h", CACert: "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n",
	}
}

// writeFARAuthTarball builds the minimal .tgz ExtractServiceAccountFromTarball
// accepts: a gzipped tar with one .json member holding the service account.
func writeFARAuthTarball(t *testing.T, serviceAccount string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "far-auth.tgz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "service-account.json", Mode: 0o600, Size: int64(len(serviceAccount)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(serviceAccount)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ONE NAMESPACE (#66). A customer requirement, supported since v1.44.0, and
// until now pinned by nothing: the goldens above set flo_namespace and
// flo_utils_namespace to two DIFFERENT values, so a renderer change that broke
// the collapsed case would pass every test in this package.
//
// This is deliberately a render-level pin rather than a plan-level one. The
// terraform side already guards the collapse with `count = ... && utils != flo`;
// what was unguarded is the step before it — that the tool still emits both
// names, identically, when they are equal. Emitting one, or silently dropping
// the utils name because it "matches the default", would take the count guard
// out of the picture entirely and recreate the AlreadyExists failure #66 fixed.
func TestOneNamespaceRendersBothNamesIdentically(t *testing.T) {
	const shared = "f5-bnk"
	for name, render := range map[string]func(io.Writer, *config.Workspace, *config.RegistryMirror) error{
		"full":   renderFullBody,
		"sparse": renderSparseBody,
	} {
		t.Run(name, func(t *testing.T) {
			ws := fullyPopulatedWorkspace(t)
			ws.BNK.FLONamespace = shared
			ws.BNK.FLOUtilsNamespace = shared
			if name == "sparse" {
				ws.Prefix = ""
			}

			var buf bytes.Buffer
			if err := render(&buf, ws, nil); err != nil {
				t.Fatal(err)
			}
			out := buf.String()

			for _, want := range []string{
				`flo_namespace = "` + shared + `"`,
				`flo_utils_namespace = "` + shared + `"`,
			} {
				if !strings.Contains(out, want) {
					t.Errorf("collapsed namespaces must still render %s\ngot:\n%s", want, out)
				}
			}
		})
	}
}

// The other half of the same guarantee: an unset namespace pair renders NEITHER
// name, so terraform's defaults apply and the two-namespace install is
// unchanged. Rendering a name here would be equally wrong — it would pin a
// default the tool has no business owning.
func TestUnsetNamespacesRenderNeitherName(t *testing.T) {
	ws := fullyPopulatedWorkspace(t)
	ws.BNK.FLONamespace = ""
	ws.BNK.FLOUtilsNamespace = ""

	var buf bytes.Buffer
	if err := renderFullBody(&buf, ws, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, forbidden := range []string{"flo_namespace", "flo_utils_namespace"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("%s rendered for a workspace that never set it", forbidden)
		}
	}
}
