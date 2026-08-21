package orchestration

import (
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #150, after review. The completeness check lives in guardRegistryMirror
// rather than in the tfvars render, because the render is the shared preamble
// for up AND down and refusing there blocked every teardown path.
//
// guardRegistryMirror is called only from prepareBNKUp, and returns nil
// immediately when the workspace is not on the mirror path — both properties
// this check needs and neither of which the render had.

func mirrorWS() *config.Workspace {
	return &config.Workspace{Registry: &config.RegistryCfg{
		Target:            "generic",
		GenericHost:       "artifactory.example.com",
		GenericRepoPrefix: "docker-local",
	}}
}

func rec(missing int) *config.RegistryMirror {
	return &config.RegistryMirror{
		Target:       "generic",
		Namespace:    "docker-local",
		ChartHost:    "artifactory.example.com/docker-local",
		ImageHost:    "artifactory.example.com/docker-local",
		MissingCount: missing,
	}
}

func TestGuardRefusesAnIncompleteMirror(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", rec(3)); err != nil {
		t.Fatal(err)
	}

	err := guardRegistryMirror("ws", mirrorWS())
	if err == nil {
		t.Fatal("installing from a mirror missing artifacts must be refused before apply")
	}
	if !strings.Contains(err.Error(), "3 artifact") {
		t.Errorf("the refusal should say how many, so a one-artifact flake reads differently "+
			"from a mirror that barely copied:\n%v", err)
	}
	// All three recovery routes, including the one that works with no access to
	// the FAR source — the situation this whole feature exists for. The sibling
	// mismatch error already offers exactly this set.
	for _, want := range []string{"registry diff", "registry replicate", "registry adopt", "no source access needed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should offer %q:\n%v", want, err)
		}
	}
}

func TestGuardAcceptsACompleteMirror(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", rec(0)); err != nil {
		t.Fatal(err)
	}
	if err := guardRegistryMirror("ws", mirrorWS()); err != nil {
		t.Fatalf("a complete mirror must install: %v", err)
	}
}

// Review finding D6. A workspace with no registry: block is not on the mirror
// path at all, and a stale record left on disk must not gate it. guardRegistryMirror
// already returns nil for that case; the render-time check did not.
func TestGuardIgnoresAStaleRecordWhenNoMirrorIsConfigured(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", rec(3)); err != nil {
		t.Fatal(err)
	}
	if err := guardRegistryMirror("ws", &config.Workspace{}); err != nil {
		t.Errorf("a workspace with no registry configured is not on the mirror path; "+
			"a leftover record must not block it:\n%v", err)
	}
}

// A clean re-replicate clears the flag, so recovery does not require editing
// the record by hand.
func TestACleanRecordClearsAPreviousPartial(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", rec(5)); err != nil {
		t.Fatal(err)
	}
	if err := guardRegistryMirror("ws", mirrorWS()); err == nil {
		t.Fatal("precondition: the partial record should be refused")
	}
	if err := config.WriteRegistryMirror("ws", rec(0)); err != nil {
		t.Fatal(err)
	}
	if err := guardRegistryMirror("ws", mirrorWS()); err != nil {
		t.Errorf("a clean replicate must clear the refusal without the file being edited:\n%v", err)
	}
}
