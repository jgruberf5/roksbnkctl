package tf

import (
	"bytes"
	"io"
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

	// TWO deliberate exceptions, both rendered unconditionally so the tool and
	// the HCL cannot disagree about a decision that changes what gets built:
	// cluster_network_mode (how a cluster is attached) and bnk_line (which BNK
	// release's resources a module creates). Everything else must be absent.
	//
	// Keep this list at two. Every addition is a field that renders for a
	// workspace that never asked for it, which is the whole thing this test
	// guarantees against.
	for _, forbidden := range []string{
		"flo_trusted_profile_sa_name",
		"flo_trusted_profile_roles",
		"cneinstance_vlan_prefixlen",
		"cneinstance_vlan_prefixlen_external",
		"cneinstance_vlan_prefixlen_internal",
		"use_existing_cluster_subnets",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("%s rendered for a workspace that never set it — the additive guarantee is broken", forbidden)
		}
	}
	if !strings.Contains(out, `cluster_network_mode = "single-nic"`) {
		t.Error("cluster_network_mode must always render, and must default to single-nic")
	}
	// Derived from the workspace's manifest version above, not from a config
	// field — a 2.3 manifest must render the 2.3 line.
	if !strings.Contains(out, `bnk_line = "2.3"`) {
		t.Error("bnk_line must always render, derived from bnk.manifest_version")
	}
}

// The trap this guards: cluster_network_mode is rendered ONLY by renderFullBody,
// so following it as the precedent for bnk_line would leave the sparse path — a
// legacy workspace with no prefix — silently taking terraform's default. On a 2.4
// manifest that means planning 2.3 resources with no error anywhere.
func TestBNKLineRendersOnBothBodies(t *testing.T) {
	for name, render := range map[string]func(io.Writer, *config.Workspace, *config.RegistryMirror) error{
		"full":   renderFullBody,
		"sparse": renderSparseBody,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			ws := &config.Workspace{
				Cluster: config.ClusterCfg{Create: true, Name: "c"},
				BNK:     config.BNKCfg{ManifestVersion: "2.4.0-EA"},
			}
			if name == "full" {
				ws.Prefix = "p"
			}
			if err := render(&buf, ws, nil); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), `bnk_line = "2.4"`) {
				t.Errorf("%s body did not render bnk_line for a 2.4 manifest:\n%s", name, buf.String())
			}
		})
	}
}

// An unparseable manifest version renders nothing rather than guessing a line.
// Guessing would pick a release for the operator; the support-matrix guard
// refuses the run instead, which is the behaviour that reports the real problem.
func TestBNKLineOmittedWhenTheManifestVersionIsUnparseable(t *testing.T) {
	var buf bytes.Buffer
	ws := &config.Workspace{BNK: config.BNKCfg{ManifestVersion: "not-a-version"}}
	renderBNKLine(&buf, ws)
	if strings.Contains(buf.String(), "bnk_line") {
		t.Errorf("an unparseable manifest version must not render a guessed line, got %q", buf.String())
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
