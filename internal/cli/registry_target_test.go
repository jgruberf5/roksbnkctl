package cli

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func TestRegistryTargetKind(t *testing.T) {
	old := flagRegistryTarget
	defer func() { flagRegistryTarget = old }()

	flagRegistryTarget = ""
	if k := registryTargetKind(&config.Workspace{}); k != "icr" {
		t.Errorf("default kind = %q, want icr", k)
	}
	if k := registryTargetKind(&config.Workspace{Registry: &config.RegistryCfg{Target: "generic"}}); k != "generic" {
		t.Errorf("config target kind = %q, want generic", k)
	}
	flagRegistryTarget = "icr" // flag wins over config
	if k := registryTargetKind(&config.Workspace{Registry: &config.RegistryCfg{Target: "generic"}}); k != "icr" {
		t.Errorf("flag-override kind = %q, want icr", k)
	}
}

func TestBuildGenericTarget(t *testing.T) {
	if _, err := buildGenericTarget(&config.Workspace{}); err == nil {
		t.Error("want error when generic_host is unset")
	}

	tgt, err := buildGenericTarget(&config.Workspace{Registry: &config.RegistryCfg{
		GenericHost: "art.example.com", GenericRepoPrefix: "bnk",
	}})
	if err != nil {
		t.Fatalf("anonymous generic target: %v", err)
	}
	if tgt.PushHost() != "art.example.com" || tgt.ImageHostPath() != "art.example.com/bnk" {
		t.Errorf("host=%q hostpath=%q", tgt.PushHost(), tgt.ImageHostPath())
	}

	pw := base64.StdEncoding.EncodeToString([]byte("tok"))
	tgt2, err := buildGenericTarget(&config.Workspace{Registry: &config.RegistryCfg{
		GenericHost: "h", GenericUsername: "u", GenericPasswordB64: pw,
	}})
	if err != nil {
		t.Fatalf("basic-auth generic target: %v", err)
	}
	ac, err := tgt2.PushAuth().Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if ac.Username != "u" || ac.Password != "tok" {
		t.Errorf("auth = %+v, want u/tok", ac)
	}

	if _, err := buildGenericTarget(&config.Workspace{Registry: &config.RegistryCfg{
		GenericHost: "h", GenericPasswordB64: "!!!not-base64",
	}}); err == nil {
		t.Error("want a decode error for malformed generic_password_b64")
	}
}

func TestRunRegistryTarget(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("rt", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("rt"); err != nil {
		t.Fatal(err)
	}
	oldWS, oldStdin := flagWorkspace, flagRegistryPasswordStdin
	flagWorkspace, flagRegistryPasswordStdin = "", false
	defer func() { flagWorkspace, flagRegistryPasswordStdin = oldWS, oldStdin }()

	steps := [][]string{
		{"icr"},                       // set kind
		{"icr_namespace", "bnk-test"}, // set fields
		{"generic_host", "art.example.com"},
		{"generic_password", "tok"},
	}
	for _, args := range steps {
		if err := runRegistryTarget(nil, args); err != nil {
			t.Fatalf("runRegistryTarget(%v): %v", args, err)
		}
	}

	ws, err := config.LoadWorkspace("rt")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Registry == nil {
		t.Fatal("registry block not created")
	}
	if ws.Registry.Target != "icr" {
		t.Errorf("target = %q, want icr", ws.Registry.Target)
	}
	if ws.Registry.ICRNamespace != "bnk-test" {
		t.Errorf("icr_namespace = %q", ws.Registry.ICRNamespace)
	}
	if ws.Registry.GenericHost != "art.example.com" {
		t.Errorf("generic_host = %q", ws.Registry.GenericHost)
	}
	if want := base64.StdEncoding.EncodeToString([]byte("tok")); ws.Registry.GenericPasswordB64 != want {
		t.Errorf("generic_password_b64 = %q, want %q", ws.Registry.GenericPasswordB64, want)
	}

	if err := runRegistryTarget(nil, []string{"icr_namespace"}); err == nil {
		t.Error("a field with no value must error")
	}
	if err := runRegistryTarget(nil, []string{"bogus", "x"}); err == nil {
		t.Error("an unknown field must error")
	}
}

func TestRunRegistryDelete_NoMirror(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("d", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("d"); err != nil {
		t.Fatal(err)
	}
	oldWS := flagWorkspace
	flagWorkspace = ""
	defer func() { flagWorkspace = oldWS }()

	// No registry-mirror.json → "nothing to delete", returns nil before any
	// target build (so a nil cobra.Command is safe here).
	if err := runRegistryDelete(nil, nil); err != nil {
		t.Fatalf("runRegistryDelete with no mirror: %v", err)
	}
}

func TestBuildICRTarget_Errors(t *testing.T) {
	// Unknown region + no icr_host → cannot derive a host (fails before any
	// credential resolution).
	_, err := buildICRTarget(context.Background(), "ws", &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "mars-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "ICR host") {
		t.Errorf("unknown region: want an ICR-host error, got %v", err)
	}

	// Known region but no namespace/prefix → namespace error (still before the
	// API-key resolution).
	_, err = buildICRTarget(context.Background(), "ws", &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "eu-de"},
	})
	if err == nil || !strings.Contains(err.Error(), "icr_namespace") {
		t.Errorf("missing namespace: want an icr_namespace error, got %v", err)
	}
}
