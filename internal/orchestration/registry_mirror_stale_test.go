package orchestration

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #112. The registry subcommands learned to distrust a record describing a
// different mirror (#109/#110). The BNK-phase paths did not, and they are the
// ones that act on it without asking.
//
// These assert the WIRING, at the exported behaviour of each path — a guard
// that exists but is never called is the shape that let #100 ship.

func mismatchedWorkspace(t *testing.T) *config.Workspace {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	// Recorded against one Artifactory repository...
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		Target:       "generic",
		Namespace:    "bnk-mirror",
		ChartHost:    "artifactory.example.com/bnk-mirror",
		ImageHost:    "artifactory.example.com/bnk-mirror",
		RegistryHost: "artifactory.example.com",
		CACert:       "-----BEGIN CERTIFICATE-----\nnot-a-real-cert\n-----END CERTIFICATE-----\n",
	}); err != nil {
		t.Fatal(err)
	}
	// ...while the workspace is configured for another.
	return &config.Workspace{Registry: &config.RegistryCfg{
		Target:            "generic",
		GenericHost:       "artifactory.example.com",
		GenericRepoPrefix: "docker-local",
	}}
}

// guardRegistryMirror gates the whole BNK phase. A record that is present and
// complete is not necessarily current: the config can have been repointed at a
// different repository since it was written, and the install would be rendered
// against the mirror in the record.
func TestGuardRegistryMirrorRefusesARecordForAnotherMirror(t *testing.T) {
	ws := mismatchedWorkspace(t)

	err := guardRegistryMirror("ws", ws)
	if err == nil {
		t.Fatal("a record describing a different mirror must not pass the phase guard")
	}
	for _, want := range []string{"bnk-mirror", "docker-local", "registry replicate", "registry adopt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should mention %q, got:\n%s", want, err)
		}
	}
}

// ensureRegistryCATrust installs the record's CA into EVERY node's container
// runtime trust store. Acting on a record for another mirror puts the wrong CA
// on every node and probes the wrong registry for reachability.
//
// The record here carries no RegistryHost, so without the guard the function
// returns nil early — the failure mode this pins is silence, not a crash.
func TestEnsureRegistryCATrustRefusesARecordForAnotherMirror(t *testing.T) {
	ws := mismatchedWorkspace(t)
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		Target:    "generic",
		Namespace: "bnk-mirror",
		ChartHost: "artifactory.example.com/bnk-mirror",
		ImageHost: "artifactory.example.com/bnk-mirror",
		// RegistryHost deliberately empty: the unguarded path returns nil here.
	}); err != nil {
		t.Fatal(err)
	}

	cctx := &config.Context{WorkspaceName: "ws", Workspace: ws}
	err := ensureRegistryCATrust(context.Background(), cctx, nil, io.Discard)
	if err == nil {
		t.Fatal("a record describing a different mirror must not be used to install node CA trust")
	}
	if !strings.Contains(err.Error(), "bnk-mirror") || !strings.Contains(err.Error(), "docker-local") {
		t.Errorf("the refusal should name both repositories, got:\n%s", err)
	}
}

// A record that matches the configured mirror still passes — the guard must not
// break the air-gap path it is protecting.
func TestGuardRegistryMirrorAcceptsTheConfiguredMirror(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		Target:    "generic",
		Namespace: "docker-local",
		ChartHost: "artifactory.example.com/docker-local",
		ImageHost: "artifactory.example.com/docker-local",
	}); err != nil {
		t.Fatal(err)
	}
	ws := &config.Workspace{Registry: &config.RegistryCfg{
		Target:            "generic",
		GenericHost:       "artifactory.example.com",
		GenericRepoPrefix: "docker-local",
	}}
	if err := guardRegistryMirror("ws", ws); err != nil {
		t.Fatalf("the configured mirror must pass: %v", err)
	}
}
