package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #185. The mirror only carries what the BOM enumerates, so this is where "the
// bundle is needed on 2.4 with mTLS" turns into an artifact that actually gets
// replicated. A mirror built without it is a mirror an mTLS install cannot
// install from, and the omission would not surface until the cluster was up.
//
// Driven from a WORKSPACE through the real resolve→options→build path against
// the manifest fixture, so a field mapped to the wrong option is caught here
// rather than against FAR.
func TestTheBOMCarriesTheBundleForATwoFourMTLSWorkspace(t *testing.T) {
	manifest, err := os.ReadFile("../bnkbom/testdata/manifest-sample.yaml")
	if err != nil {
		t.Skipf("bnkbom fixture not reachable: %v", err)
	}

	for _, tc := range []struct {
		name      string
		ws        *config.Workspace
		wantFiles int
	}{
		{
			name: "2.4 with mTLS",
			ws: &config.Workspace{BNK: config.BNKCfg{
				ManifestVersion: "2.4.0-3.2600.1-0.0.1", GatewayAPIMTLS: true,
			}},
			wantFiles: 1,
		},
		{
			name:      "2.4 without mTLS",
			ws:        &config.Workspace{BNK: config.BNKCfg{ManifestVersion: "2.4.0-3.2600.1-0.0.1"}},
			wantFiles: 0,
		},
		{
			name: "2.3, mTLS set anyway",
			ws: &config.Workspace{BNK: config.BNKCfg{
				ManifestVersion: "2.3.0-3.2598.3-0.0.170", GatewayAPIMTLS: true,
			}},
			wantFiles: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := resolveBOMInputs(tc.ws)
			opts := bomOptions(&in)
			// The fixture declares its own release; the workspace's manifest
			// version selects a release this fixture does not have.
			opts.ManifestVersion = ""
			bom, err := bnkbom.Build(manifest, opts)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			_, _, files := bom.Counts()
			if files != tc.wantFiles {
				t.Fatalf("BOM carries %d files, want %d", files, tc.wantFiles)
			}
			if tc.wantFiles == 0 {
				return
			}
			for _, a := range bom.Artifacts {
				if a.Kind != bnkbom.KindFile {
					continue
				}
				if a.Name != bnkbom.GatewayAPIBundleName {
					t.Errorf("file artifact is %q, want %q", a.Name, bnkbom.GatewayAPIBundleName)
				}
				if a.SHA256 == "" {
					t.Error("the bundle entered the BOM unpinned; `registry replicate` refuses those, " +
						"so the mirror would silently never carry it")
				}
			}
		})
	}
}

// The source override has to reach the BOM, or an estate that proxies github.com
// has configured something the replicate ignores — and the fetch fails against a
// host it was told not to use.
func TestTheBundleSourceOverrideReachesTheBOM(t *testing.T) {
	ws := &config.Workspace{BNK: config.BNKCfg{
		ManifestVersion:     "2.4.0-3.2600.1-0.0.1",
		GatewayAPIMTLS:      true,
		GatewayAPIBundleURL: "https://proxy.internal/gw/standard-install.yaml",
	}}
	in := resolveBOMInputs(ws)
	if in.GatewayAPIBundleURL != ws.BNK.GatewayAPIBundleURL {
		t.Fatalf("the override did not reach the BOM inputs: %q", in.GatewayAPIBundleURL)
	}
	art, err := bnkbom.GatewayAPIBundle(in.GatewayAPIBundleVersion, in.GatewayAPIBundleURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(art.SourceURL, "https://proxy.internal/") {
		t.Errorf("the artifact would be fetched from %q", art.SourceURL)
	}
}
