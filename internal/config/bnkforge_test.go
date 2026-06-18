package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBNKForgeCfg_RoundTrip pins that the optional bnkforge block parses, and
// that its absence leaves BNKForge nil (legacy config.yaml is unaffected).
func TestBNKForgeCfg_RoundTrip(t *testing.T) {
	const withBlock = `
ibmcloud:
  region: us-south
  resource_group: default
cluster:
  create: true
  name: demo
tf_source:
  type: embedded
bnkforge:
  register: true
  project: "7"
  url: https://forge.local
`
	var ws Workspace
	if err := yaml.Unmarshal([]byte(withBlock), &ws); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ws.BNKForge == nil {
		t.Fatal("BNKForge is nil; expected the block to parse")
	}
	if !ws.BNKForge.Register || ws.BNKForge.Project != "7" || ws.BNKForge.URL != "https://forge.local" {
		t.Errorf("BNKForge = %+v; want {Register:true Project:7 URL:https://forge.local}", *ws.BNKForge)
	}

	// Round-trip back out, and confirm omitempty keeps a disabled block terse.
	out, err := yaml.Marshal(&ws)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var ws2 Workspace
	if err := yaml.Unmarshal(out, &ws2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if ws2.BNKForge == nil || !ws2.BNKForge.Register || ws2.BNKForge.Project != "7" {
		t.Errorf("round-tripped BNKForge = %+v; want preserved", ws2.BNKForge)
	}

	// Absent block → nil pointer (no behavior change for old workspaces).
	var legacy Workspace
	if err := yaml.Unmarshal([]byte("ibmcloud:\n  region: r\ncluster:\n  name: c\ntf_source:\n  type: embedded\n"), &legacy); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if legacy.BNKForge != nil {
		t.Errorf("absent bnkforge block: BNKForge = %+v; want nil", legacy.BNKForge)
	}
}
