package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// readRootVariableDefault returns the `default` of a root terraform variable.
//
// Scoped to the named variable's own block rather than grepping the file: a
// bare search for `default = "1.5.0"` would match any variable that happens to
// carry that string and would keep passing after the one under test changed.
func readRootVariableDefault(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRootForDemoTest(t), "terraform/variables.tf"))
	if err != nil {
		t.Fatalf("reading root variables.tf: %v", err)
	}
	block := regexp.MustCompile(`(?s)variable\s+"` + regexp.QuoteMeta(name) + `"\s*\{(.*?)\n\}`).FindStringSubmatch(string(body))
	if block == nil {
		t.Fatalf("no root variable %q — this comparison can no longer detect drift", name)
	}
	def := regexp.MustCompile(`(?m)^\s*default\s*=\s*"([^"]*)"`).FindStringSubmatch(block[1])
	if def == nil {
		t.Fatalf("root variable %q has no string default", name)
	}
	return def[1]
}

// The bundle is needed on exactly one combination, and the cost of getting it
// wrong runs both ways: installing it on 2.3 applies a second copy of CRDs the
// FLO crd-installer already forces, and NOT installing it on a 2.4 mTLS cluster
// leaves the CNE controller configured for a Gateway API the cluster does not
// carry — which fails behaviourally, later, and points nowhere near here.
func TestGatewayAPIBundleIsNeededOnlyOnTwoFourWithMTLS(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		mtls     bool
		want     bool
	}{
		{"2.4 with mTLS", "2.4.0-3.2600.1-0.0.1", true, true},
		{"2.4 without mTLS", "2.4.0-3.2600.1-0.0.1", false, false},
		{"2.3 with mTLS set", "2.3.0-3.2598.3-0.0.170", true, false},
		{"2.3 plain", "2.3.0-3.2598.3-0.0.170", false, false},
		{"unparseable manifest, mTLS asked for", "not-a-version", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := &Workspace{BNK: BNKCfg{ManifestVersion: tc.manifest, GatewayAPIMTLS: tc.mtls}}
			if got := ws.GatewayAPIBundleNeeded(); got != tc.want {
				t.Errorf("GatewayAPIBundleNeeded() = %v, want %v", got, tc.want)
			}
		})
	}
	if (*Workspace)(nil).GatewayAPIBundleNeeded() {
		t.Error("a nil workspace asked for the bundle")
	}
}

// The version the bundle is installed at and the version the CNE controller is
// told to expect (GATEWAY_API_VERSION) must be the same number. An empty field
// renders no tfvar, so terraform's own default applies — and this has to return
// that same default, or an untouched config installs one release and configures
// the controller for another.
func TestGatewayAPIBundleVersionFallsBackToTheTerraformDefault(t *testing.T) {
	empty := &Workspace{}
	if got := empty.GatewayAPIBundleVersion(); got != DefaultGatewayAPIVersion {
		t.Errorf("unset version = %q, want the terraform default %q", got, DefaultGatewayAPIVersion)
	}
	set := &Workspace{BNK: BNKCfg{GatewayAPIVersion: "  1.6.0 "}}
	if got := set.GatewayAPIBundleVersion(); got != "1.6.0" {
		t.Errorf("configured version = %q, want 1.6.0 (trimmed)", got)
	}
}

// The Go default and the terraform default are two declarations of one value, in
// two languages, and nothing else ties them. Read the HCL and compare.
func TestGoAndTerraformAgreeOnTheDefaultGatewayAPIVersion(t *testing.T) {
	hcl := readRootVariableDefault(t, "cneinstance_gateway_api_version")
	if hcl != DefaultGatewayAPIVersion {
		t.Errorf("terraform defaults cneinstance_gateway_api_version to %q, Go's DefaultGatewayAPIVersion is %q — "+
			"an untouched config would install one Gateway API release and configure the controller for another",
			hcl, DefaultGatewayAPIVersion)
	}
}
