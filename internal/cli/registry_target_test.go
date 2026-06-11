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
	if k := registryTargetKind(&config.Workspace{Registry: &config.RegistryCfg{Target: "openshift"}}); k != "openshift" {
		t.Errorf("config target kind = %q, want openshift", k)
	}
	flagRegistryTarget = "generic" // flag wins over config
	if k := registryTargetKind(&config.Workspace{Registry: &config.RegistryCfg{Target: "openshift"}}); k != "generic" {
		t.Errorf("flag-override kind = %q, want generic", k)
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
