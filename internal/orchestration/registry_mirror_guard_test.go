package orchestration

import (
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// TestGuardRegistryMirror_NoMirrorConfigured: off the air-gap path
// (ws.Registry == nil) the guard is a no-op, so every non-air-gap workspace
// is unaffected.
func TestGuardRegistryMirror_NoMirrorConfigured(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := guardRegistryMirror("ws", &config.Workspace{}); err != nil {
		t.Fatalf("no registry block should pass: %v", err)
	}
}

// TestGuardRegistryMirror_ConfiguredButUnpopulated: a registry: block with no
// registry-mirror.json errors and points at `registry replicate`.
func TestGuardRegistryMirror_ConfiguredButUnpopulated(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	ws := &config.Workspace{Registry: &config.RegistryCfg{}}
	err := guardRegistryMirror("ws", ws)
	if err == nil {
		t.Fatal("configured-but-unpopulated mirror should error")
	}
	if !strings.Contains(err.Error(), "registry replicate") {
		t.Errorf("error should point at `registry replicate`: %v", err)
	}
}

// TestGuardRegistryMirror_Incomplete: a record missing a host errors and
// names the missing host.
func TestGuardRegistryMirror_Incomplete(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		ChartHost: "chart/bnk-mirror", // ImageHost missing
	}); err != nil {
		t.Fatal(err)
	}
	ws := &config.Workspace{Registry: &config.RegistryCfg{}}
	err := guardRegistryMirror("ws", ws)
	if err == nil || !strings.Contains(err.Error(), "image_host") {
		t.Errorf("incomplete record should name the missing image_host: %v", err)
	}
}

// TestGuardRegistryMirror_Populated: a complete record passes.
func TestGuardRegistryMirror_Populated(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		ChartHost: "chart/bnk-mirror",
		ImageHost: "image:5000/bnk-mirror",
	}); err != nil {
		t.Fatal(err)
	}
	ws := &config.Workspace{Registry: &config.RegistryCfg{}}
	if err := guardRegistryMirror("ws", ws); err != nil {
		t.Fatalf("populated record should pass: %v", err)
	}
}
