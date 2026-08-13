package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// THE load-bearing property of this whole batch: a workspace that sets none of
// the new fields must render EXACTLY what it rendered before. Every new field is
// checked by name, so adding one without a nil-guard fails here.
func TestNoNewFieldRendersWhenUnset(t *testing.T) {
	var buf bytes.Buffer
	ws := &config.Workspace{
		Prefix:  "legacy",
		Cluster: config.ClusterCfg{Create: true, Name: "legacy"},
		BNK:     config.BNKCfg{ManifestVersion: "2.3.0-3.2598.3-0.0.170"},
	}
	if err := renderFullBody(&buf, ws, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// cluster_network_mode is the ONE deliberate exception — it is rendered
	// unconditionally so the tool and the HCL cannot disagree about how a cluster
	// is built. Everything else must be absent.
	for _, forbidden := range []string{
		"flo_trusted_profile_sa_name",
		"flo_trusted_profile_roles",
		"cneinstance_vlan_prefixlen",
		"cneinstance_vlan_prefixlen_external",
		"cneinstance_vlan_prefixlen_internal",
		"cneinstance_gtm_url",
		"cneinstance_gtm_username",
		"cneinstance_gtm_password",
		"use_existing_cluster_subnets",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("%s rendered for a workspace that never set it — the additive guarantee is broken", forbidden)
		}
	}
	if !strings.Contains(out, `cluster_network_mode = "single-nic"`) {
		t.Error("cluster_network_mode must always render, and must default to single-nic")
	}
}

// An invalid base64 password must be a clear error, not a silent empty password
// that makes GSLB fail to authenticate for reasons nobody can see.
func TestGTMBadBase64IsAnError(t *testing.T) {
	var buf bytes.Buffer
	ws := &config.Workspace{BNK: config.BNKCfg{GTM: &config.BNKGTMCfg{
		URL:         "https://gtm.example.com",
		PasswordB64: "!!!not base64!!!",
	}}}
	err := renderBNKFields(&buf, ws, nil)
	if err == nil {
		t.Fatal("invalid base64 must be reported, not silently rendered as empty")
	}
	if !strings.Contains(err.Error(), "password_b64") {
		t.Errorf("the error must name the field: %v", err)
	}
}

// A GTM block with no URL is not a GTM configuration — it must render nothing
// rather than half of one.
func TestGTMWithoutURLRendersNothing(t *testing.T) {
	var buf bytes.Buffer
	ws := &config.Workspace{BNK: config.BNKCfg{GTM: &config.BNKGTMCfg{
		Username: "admin", PasswordB64: "cGFzcw==",
	}}}
	if err := renderBNKFields(&buf, ws, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "cneinstance_gtm") {
		t.Errorf("a URL-less gtm block rendered credentials:\n%s", buf.String())
	}
}

// Per-VLAN masks must render as bare numbers; a quoted value is a type error at
// plan time.
func TestPerVLANMasksRenderAsNumbers(t *testing.T) {
	e, i := 23, 26
	var buf bytes.Buffer
	ws := &config.Workspace{BNK: config.BNKCfg{Network: &config.BNKNetworkCfg{
		VLANPrefixLenExternal: &e, VLANPrefixLenInternal: &i,
	}}}
	if err := renderBNKFields(&buf, ws, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"cneinstance_vlan_prefixlen_external = 23",
		"cneinstance_vlan_prefixlen_internal = 26",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `prefixlen_external = "23"`) {
		t.Error("mask rendered as a quoted string — terraform would reject it as a type mismatch")
	}
}
