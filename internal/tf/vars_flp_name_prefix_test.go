package tf

import (
	"bytes"
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
