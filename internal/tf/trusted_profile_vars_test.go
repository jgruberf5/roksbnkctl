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

// The service account default must DERIVE FLO's own long name, not pin a short
// one. A confirmed BNK 2.3 install runs as
// f5-cne-controller-<ns>-f5-cne-controller-serviceaccount, and the IAM rule is a
// matcher — a short name matches nothing and silently makes the profile
// unassumable. So the tool must render NOTHING when unset and let the HCL derive.
func TestTrustedProfileSANotPinnedWhenUnset(t *testing.T) {
	var buf bytes.Buffer
	ws := &config.Workspace{BNK: config.BNKCfg{TrustedProfile: &config.BNKTrustedProfileCfg{
		Roles: []string{"Viewer", "Editor"}, // roles set, account deliberately not
	}}}
	if err := renderBNKFields(&buf, ws, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "flo_trusted_profile_sa_name") {
		t.Errorf("an unset service account was pinned into tfvars — the HCL must derive it:\n%s", out)
	}
	if strings.Contains(out, "f5-cne-controller\"") {
		t.Error("the short name was rendered; it matches no service account FLO creates")
	}
	if !strings.Contains(out, `flo_trusted_profile_roles = ["Viewer", "Editor"]`) {
		t.Errorf("roles should still render:\n%s", out)
	}
}
