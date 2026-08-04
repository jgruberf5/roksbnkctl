package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Pins the bnk.network render: the per-zone list plus the two TMM-wide knobs
// (vlan_prefixlen / tmm_k8s_routes) each emit ONLY when config supplies them, so an
// unset network block stays byte-identical to the terraform install-guide defaults.

func baseWS() *config.Workspace {
	return &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Create: true, Name: "bnk-demo"},
	}
}

func TestRenderTFVars_PublicGateway(t *testing.T) {
	// nil → omit (terraform default true, current behavior).
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, baseWS(), "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	if strings.Contains(buf.String(), "cluster_public_gateway") {
		t.Errorf("unset cluster.public_gateway must NOT emit the tfvar\n%s", buf.String())
	}
	// false → private/disconnected cluster.
	f := false
	ws := baseWS()
	ws.Cluster.PublicGateway = &f
	buf.Reset()
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	if !strings.Contains(buf.String(), "cluster_public_gateway = false") {
		t.Errorf("public_gateway=false must emit cluster_public_gateway = false\n%s", buf.String())
	}
	// true → explicit current behavior.
	tr := true
	ws.Cluster.PublicGateway = &tr
	buf.Reset()
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	if !strings.Contains(buf.String(), "cluster_public_gateway = true") {
		t.Errorf("public_gateway=true must emit cluster_public_gateway = true\n%s", buf.String())
	}
}

func TestRenderTFVars_Network_UnsetOmitsAll(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, baseWS(), "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	for _, k := range []string{"cneinstance_network_zones", "cneinstance_vlan_prefixlen", "cneinstance_tmm_k8s_routes"} {
		if strings.Contains(buf.String(), k) {
			t.Errorf("unset bnk.network must NOT emit %s\noutput:\n%s", k, buf.String())
		}
	}
}

func TestRenderTFVars_Network_PrefixLenAndRoutesWithoutZones(t *testing.T) {
	prefix := 25
	ws := baseWS()
	ws.BNK.Network = &config.BNKNetworkCfg{VLANPrefixLen: &prefix, TMMK8SRoutes: "10.128.0.0/14"}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "cneinstance_vlan_prefixlen = 25") {
		t.Errorf("want cneinstance_vlan_prefixlen = 25\noutput:\n%s", out)
	}
	if !strings.Contains(out, `cneinstance_tmm_k8s_routes = "10.128.0.0/14"`) {
		t.Errorf("want cneinstance_tmm_k8s_routes = \"10.128.0.0/14\"\noutput:\n%s", out)
	}
	// Prefix/routes are independent of zones — no zone block should appear.
	if strings.Contains(out, "cneinstance_network_zones") {
		t.Errorf("no zones set, so cneinstance_network_zones must be absent\noutput:\n%s", out)
	}
	// Each knob exactly once (a dup is a terraform error).
	if n := strings.Count(out, "cneinstance_vlan_prefixlen ="); n != 1 {
		t.Errorf("cneinstance_vlan_prefixlen emitted %d times, want 1", n)
	}
}

func TestRenderTFVars_Network_ZonesPlusKnobs(t *testing.T) {
	prefix := config.DefaultVLANPrefixLen
	ws := baseWS()
	ws.BNK.Network = &config.BNKNetworkCfg{
		Zones:         config.DefaultBNKNetworkZones,
		VLANPrefixLen: &prefix,
		TMMK8SRoutes:  config.DefaultTMMK8SRoutes,
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"cneinstance_network_zones = [",
		"cneinstance_vlan_prefixlen = 24",
		`cneinstance_tmm_k8s_routes = "172.17.0.0/18"`,
		"10.155.15.101", // first zone's external self-IP
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\noutput:\n%s", want, out)
		}
	}
}
