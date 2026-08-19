package tf

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func flpVSIWorkspace(prefix string) *config.Workspace {
	return &config.Workspace{
		BNK: config.BNKCfg{
			ManifestVersion: config.DefaultManifestVersion,
			FLP: &config.BNKFLPCfg{
				Mode: "vsi",
				VSI:  &config.BNKFLPVSICfg{VPC: "r014-abc", NamePrefix: prefix},
			},
		},
	}
}

// The default must stay UNSET in the rendered tfvars. Renaming a terraform
// resource REPLACES it, so an existing proxy that never asked for a prefix must
// plan clean — emitting flp_vsi_name_prefix = "" would also make any future
// change to what "" means silently destroy and rebuild every running proxy.
func TestFLPNamePrefixOmittedWhenUnset(t *testing.T) {
	var buf bytes.Buffer
	if err := renderBNKFields(&buf, flpVSIWorkspace(""), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "flp_vsi_name_prefix") {
		t.Errorf("must not be rendered when unset — the legacy names are the default:\n%s", buf.String())
	}
}

func TestFLPNamePrefixRendersWhenSet(t *testing.T) {
	var buf bytes.Buffer
	if err := renderBNKFields(&buf, flpVSIWorkspace("bnk-ci"), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `flp_vsi_name_prefix = "bnk-ci"`) {
		t.Errorf("missing flp_vsi_name_prefix:\n%s", buf.String())
	}
}

// End-to-end through the REAL tfvars writer the FLP phase uses, not just the
// BNK-fields helper — proves the setting survives the whole render path, which
// is where a new setting most often gets dropped.
func TestFLPNamePrefixReachesRenderedTfvars(t *testing.T) {
	ws := &config.Workspace{
		Prefix:   "flptest",
		IBMCloud: config.IBMCloudCfg{Region: "us-east", ResourceGroup: "default", APIKeyB64: "dGVzdA=="},
		BNK: config.BNKCfg{
			ManifestVersion: config.DefaultManifestVersion,
			FLP:             &config.BNKFLPCfg{Mode: "vsi", VSI: &config.BNKFLPVSICfg{VPC: "r014-abc", NamePrefix: "flptest"}},
		},
	}
	p := filepath.Join(t.TempDir(), "terraform.tfvars")
	if err := WriteTFVars(p, ws, t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("WriteTFVars: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `flp_vsi_name_prefix = "flptest"`) {
		t.Fatalf("name_prefix did not survive the full render path:\n%s", b)
	}
}
