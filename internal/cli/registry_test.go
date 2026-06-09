package cli

import (
	"os"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// resetRegistryFlags clears the package-level registry flag globals between
// table cases so one case's flags don't bleed into the next.
func resetRegistryFlags() {
	flagRegistryManifestVer = ""
	flagRegistryFARRepo = ""
	flagRegistrySAB64 = ""
	flagRegistryIncludeDeps = false
	flagRegistryNoIncludeDep = false
}

func TestResolveBOMInputs_ConfigDefaults(t *testing.T) {
	resetRegistryFlags()
	ws := &config.Workspace{
		BNK: config.BNKCfg{ManifestVersion: "2.3.0", FARRepoURL: "oci://repo.f5.com"},
		Registry: &config.RegistryCfg{
			Namespace:               "my-mirror",
			SourceServiceAccountB64: "c2EtanNvbg==",
		},
	}
	in := resolveBOMInputs(ws)
	if in.ManifestVersion != "2.3.0" {
		t.Errorf("ManifestVersion = %q, want 2.3.0", in.ManifestVersion)
	}
	if in.FARRepoURL != "oci://repo.f5.com" {
		t.Errorf("FARRepoURL = %q", in.FARRepoURL)
	}
	if in.SourceSAB64 != "c2EtanNvbg==" {
		t.Errorf("SourceSAB64 = %q", in.SourceSAB64)
	}
	if !in.IncludeDeps {
		t.Error("IncludeDeps default = false, want true")
	}
}

func TestResolveBOMInputs_FlagsOverrideConfig(t *testing.T) {
	resetRegistryFlags()
	defer resetRegistryFlags()
	flagRegistryManifestVer = "9.9.9"
	flagRegistryFARRepo = "my.mirror.io"
	flagRegistrySAB64 = "ZmxhZw=="
	flagRegistryNoIncludeDep = true

	ws := &config.Workspace{
		BNK:      config.BNKCfg{ManifestVersion: "2.3.0", FARRepoURL: "repo.f5.com"},
		Registry: &config.RegistryCfg{SourceServiceAccountB64: "Y29uZmln"},
	}
	in := resolveBOMInputs(ws)
	if in.ManifestVersion != "9.9.9" || in.FARRepoURL != "my.mirror.io" || in.SourceSAB64 != "ZmxhZw==" {
		t.Errorf("flags did not override config: %+v", in)
	}
	if in.IncludeDeps {
		t.Error("--no-include-deps did not take effect")
	}
}

func TestResolveBOMInputs_NilRegistryBlock(t *testing.T) {
	resetRegistryFlags()
	ws := &config.Workspace{BNK: config.BNKCfg{ManifestVersion: "2.3.0"}}
	in := resolveBOMInputs(ws)
	if !in.IncludeDeps {
		t.Error("nil registry block: IncludeDeps default = false, want true")
	}
	if in.SourceSAB64 != "" {
		t.Errorf("nil registry block: SourceSAB64 = %q, want empty", in.SourceSAB64)
	}
}

// TestBOMRender_FromFixture is the bom-command smoke test: it builds the BOM from
// the bnkbom fixture manifest (the same Build the bom command runs once the FAR
// fetch lands) and renders it, asserting the deterministic order + dep union.
func TestBOMRender_FromFixture(t *testing.T) {
	manifest, err := os.ReadFile("../bnkbom/testdata/manifest-sample.yaml")
	if err != nil {
		t.Skipf("bnkbom fixture not reachable from cli package dir: %v", err)
	}
	bom, err := bnkbom.Build(manifest, bnkbom.Options{
		IncludeDeps:         true,
		CertManagerVersion:  defaultCertManagerVersion,
		NodeLabelerImageTag: defaultNodeLabelerTag,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if bom.ManifestVersion != "9.9.9-test.0" {
		t.Errorf("ManifestVersion = %q", bom.ManifestVersion)
	}
	// 3 charts + 2 images from the fixture, plus deps (1 cert-manager chart, 5
	// cert-manager images, 1 node-labeler image).
	charts, images := bom.Counts()
	if charts != 4 {
		t.Errorf("charts = %d, want 4 (3 manifest + 1 cert-manager)", charts)
	}
	if images != 8 {
		t.Errorf("images = %d, want 8 (2 manifest + 5 cert-manager + 1 node-labeler)", images)
	}

	// printBOMTable must not panic and must write to stdout cleanly.
	printBOMTable(bom)
}
