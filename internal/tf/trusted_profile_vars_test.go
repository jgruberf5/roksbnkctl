package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Absent block renders NOTHING, so the shipped HCL defaults stand. This is the
// additive guarantee: an existing workspace's tfvars are byte-identical.
func TestTrustedProfileUnsetRendersNothing(t *testing.T) {
	var buf bytes.Buffer
	ws := &config.Workspace{}
	if err := renderBNKFields(&buf, ws, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "flo_trusted_profile") {
		t.Errorf("unset block still rendered:\n%s", buf.String())
	}
}

func TestTrustedProfileRenders(t *testing.T) {
	var buf bytes.Buffer
	ws := &config.Workspace{BNK: config.BNKCfg{TrustedProfile: &config.BNKTrustedProfileCfg{
		ServiceAccount: "f5-cne-controller",
		Roles:          []string{"Viewer", "Editor"},
	}}}
	if err := renderBNKFields(&buf, ws, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `flo_trusted_profile_sa_name = "f5-cne-controller"`) {
		t.Errorf("sa name missing:\n%s", out)
	}
	// A list, in HCL syntax — not a comma string, which terraform would reject
	// as a type mismatch at plan time.
	if !strings.Contains(out, `flo_trusted_profile_roles = ["Viewer", "Editor"]`) {
		t.Errorf("roles not rendered as an HCL list:\n%s", out)
	}
}

// Empty strings inside the list must not reach terraform as "" roles.
func TestTrustedProfileSkipsBlankRoles(t *testing.T) {
	var buf bytes.Buffer
	ws := &config.Workspace{BNK: config.BNKCfg{TrustedProfile: &config.BNKTrustedProfileCfg{
		Roles: []string{"Viewer", "  ", ""},
	}}}
	if err := renderBNKFields(&buf, ws, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `flo_trusted_profile_roles = ["Viewer"]`) {
		t.Errorf("blank roles not filtered:\n%s", buf.String())
	}
}
