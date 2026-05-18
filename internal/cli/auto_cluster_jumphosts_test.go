package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

func om(v string) tfexec.OutputMeta {
	return tfexec.OutputMeta{Value: json.RawMessage(v)}
}

// TestMapOutput_ParseMatrix covers the Sprint 13 Issue 3 mapOutput
// contract: a real {zone => fip} map parses; the absent key, the
// `[]`-default empty map terraform emits, an explicit `{}`, and a
// non-object value all collapse to nil ("no cluster jumphosts, skip").
func TestMapOutput_ParseMatrix(t *testing.T) {
	outputs := map[string]tfexec.OutputMeta{
		"multi":      om(`{"us-south-1":"52.1.2.3","us-south-2":"52.4.5.6","us-south-3":"52.7.8.9"}`),
		"empty_list": om(`[]`),
		"empty_obj":  om(`{}`),
		"scalar":     om(`"TGW jumphost not created"`),
		"bad":        om(`not json`),
		"nullval":    om(`null`),
	}

	got := mapOutput(outputs, "multi")
	if len(got) != 3 || got["us-south-2"] != "52.4.5.6" {
		t.Errorf("mapOutput(multi) = %v, want 3-entry zone=>fip map", got)
	}
	for _, key := range []string{"empty_list", "empty_obj", "scalar", "bad", "nullval", "absent"} {
		if m := mapOutput(outputs, key); m != nil {
			t.Errorf("mapOutput(%s) = %v, want nil (skip)", key, m)
		}
	}
}

// TestTryAutoClusterJumphosts_GuardsAreNonFatal: the nil-arg guards
// return cleanly (best-effort posture — never panic, never fail `up`).
func TestTryAutoClusterJumphosts_GuardsAreNonFatal(t *testing.T) {
	tryAutoClusterJumphosts(context.TODO(), nil, nil)
	tryAutoClusterJumphosts(context.TODO(), &config.Context{}, nil)
}

// TestSetTarget_PerZoneUpsertIdempotent exercises the registration shape
// tryAutoClusterJumphosts uses: N zones → N `jumphost-<zone>` targets,
// and a re-run with a rotated FIP upserts in place (no duplicate, host
// refreshed) — the documented option (a) upsert-only behaviour.
func TestSetTarget_PerZoneUpsertIdempotent(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "auto-cj-ws"
	if err := config.SaveWorkspace(ws, &config.Workspace{}); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}

	zones := map[string]string{
		"us-south-1": "52.1.1.1",
		"us-south-2": "52.2.2.2",
		"us-south-3": "52.3.3.3",
	}
	for z, fip := range zones {
		cfg := config.TargetCfg{Host: fip, User: "ubuntu", KeySource: "tf-output:jumphost_shared_key"}
		if err := remote.SetTarget(ws, "jumphost-"+z, cfg); err != nil {
			t.Fatalf("SetTarget %s: %v", z, err)
		}
	}
	w, err := config.LoadWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	for z, fip := range zones {
		got, ok := w.Targets["jumphost-"+z]
		if !ok {
			t.Errorf("missing target jumphost-%s", z)
			continue
		}
		if got.Host != fip || got.User != "ubuntu" || got.KeySource != "tf-output:jumphost_shared_key" {
			t.Errorf("jumphost-%s = %+v, want host=%s ubuntu tf-output:jumphost_shared_key", z, got, fip)
		}
	}

	// FIP rotation on us-south-2 → upsert in place, no duplicate target.
	if err := remote.SetTarget(ws, "jumphost-us-south-2",
		config.TargetCfg{Host: "52.9.9.9", User: "ubuntu", KeySource: "tf-output:jumphost_shared_key"}); err != nil {
		t.Fatalf("re-SetTarget: %v", err)
	}
	w2, err := config.LoadWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(w2.Targets) != 3 {
		t.Errorf("after rotation got %d targets, want 3 (upsert, not duplicate)", len(w2.Targets))
	}
	if w2.Targets["jumphost-us-south-2"].Host != "52.9.9.9" {
		t.Errorf("rotated FIP not refreshed in place: %+v", w2.Targets["jumphost-us-south-2"])
	}
}
