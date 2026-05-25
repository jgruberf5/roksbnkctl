package egresssnat_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios/egresssnat"

	// Side-effect import: registers egress-snat in init().
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/egresssnat"
)

func readFileHelper(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 — test helper, path from WriteManifest
}

// minimalCtx builds a scenarios.Context sufficient for render-only tests
// (no Cluster, no Clientset/Dynamic needed).
func minimalCtx(dir string) *scenarios.Context {
	return &scenarios.Context{
		Ctx:          context.Background(),
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}
}

func TestRegistered(t *testing.T) {
	s := scenarios.Find("egress-snat")
	if s == nil {
		t.Fatal("egress-snat not registered — init() not called?")
	}
	if s.Rating() != scenarios.Amber {
		t.Errorf("rating = %q, want amber", s.Rating())
	}
	if len(s.Dependencies()) != 0 {
		t.Errorf("dependencies = %v, want empty", s.Dependencies())
	}
}

func TestManifestsRendered(t *testing.T) {
	dir := t.TempDir()
	sctx := minimalCtx(dir)

	s := scenarios.Find("egress-snat")
	if s == nil {
		t.Fatal("egress-snat not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if len(paths) != 3 {
		t.Errorf("expected 3 manifest paths, got %d: %v", len(paths), paths)
	}

	for _, p := range paths {
		if p == "" {
			t.Error("empty path in manifest list")
			continue
		}
		raw, readErr := readFileHelper(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		content := string(raw)

		// No leftover template directives.
		if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
			t.Errorf("manifest %s still contains template directives:\n%s", p, content)
		}

		// The F5SPKEgress manifest must carry the required fields.
		if strings.HasSuffix(p, "03-f5spkegress.yaml") {
			checks := []struct {
				want string
				desc string
			}{
				{"SRC_TRANS_AUTOMAP", "snatType SRC_TRANS_AUTOMAP"},
				{"create: true", "vxlan.create: true"},
				{"int-vlan", "tmmInterfaceName int-vlan"},
				{"awsbnkctl-scn-egress", "scenario namespace in pseudoCNIConfig.namespaces"},
				{"f5-cne-system", "egress namespace in metadata"},
			}
			for _, ch := range checks {
				if !strings.Contains(content, ch.want) {
					t.Errorf("03-f5spkegress.yaml missing %s (%q):\n%s", ch.desc, ch.want, content)
				}
			}
		}
	}
}

// TestVerifyCallOrder exercises Verify with injected stub deps. The stubs
// record which functions were called so the test asserts ordering and
// short-circuit behaviour.
func TestVerifyCallOrder(t *testing.T) {
	var calls []string

	// Stub: WaitPodReady succeeds immediately.
	stubWaitPodReady := func(_ context.Context, _ *scenarios.Context, ns, name string, _ time.Duration) error {
		calls = append(calls, "WaitPodReady:"+ns+"/"+name)
		return nil
	}

	// Stub: GetF5SPKEgress returns present=true.
	stubGetF5SPKEgress := func(_ context.Context, _ *scenarios.Context, ns, name string) (bool, string, error) {
		calls = append(calls, "GetF5SPKEgress:"+ns+"/"+name)
		return true, "present", nil
	}

	d := egresssnat.VerifyDeps{
		WaitPodReadyFn:   stubWaitPodReady,
		GetF5SPKEgressFn: stubGetF5SPKEgress,
	}
	s := egresssnat.NewScenarioForTest(d)

	sctx := &scenarios.Context{
		Ctx:     context.Background(),
		Out:     io.Discard,
		Options: map[string]string{},
	}

	res := s.Verify(sctx)

	// All assertions must pass.
	if !res.AllPassed() {
		t.Errorf("Verify failed: %s; assertions: %+v", res.Summary, res.Assertions)
	}
	if len(res.Assertions) != 3 {
		t.Errorf("expected 3 assertions, got %d", len(res.Assertions))
	}

	// Call order: WaitPodReady before GetF5SPKEgress.
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 stub calls, got %d: %v", len(calls), calls)
	}
	if !strings.HasPrefix(calls[0], "WaitPodReady") {
		t.Errorf("first call should be WaitPodReady, got %q", calls[0])
	}
	if !strings.HasPrefix(calls[1], "GetF5SPKEgress") {
		t.Errorf("second call should be GetF5SPKEgress, got %q", calls[1])
	}

	// The informational assertion is always OK=true.
	last := res.Assertions[2]
	if !last.OK {
		t.Errorf("informational assertion should be OK=true, got OK=false")
	}
	if !strings.Contains(last.Description, "deferred") {
		t.Errorf("informational assertion description should mention 'deferred', got %q", last.Description)
	}
}

// TestVerifyPodFailure confirms that a failing WaitPodReady propagates as
// OK=false and the result is not AllPassed.
func TestVerifyPodFailure(t *testing.T) {
	stubFail := func(_ context.Context, _ *scenarios.Context, _, _ string, _ time.Duration) error {
		return errors.New("pod not Ready after 3m0s")
	}
	stubGetEgress := func(_ context.Context, _ *scenarios.Context, _, _ string) (bool, string, error) {
		return true, "present", nil
	}

	d := egresssnat.VerifyDeps{
		WaitPodReadyFn:   stubFail,
		GetF5SPKEgressFn: stubGetEgress,
	}
	s := egresssnat.NewScenarioForTest(d)

	sctx := &scenarios.Context{
		Ctx:     context.Background(),
		Out:     io.Discard,
		Options: map[string]string{},
	}

	res := s.Verify(sctx)
	if res.AllPassed() {
		t.Error("expected AllPassed=false when pod not Ready")
	}
	if res.Status != "failed" {
		t.Errorf("expected status=failed, got %q", res.Status)
	}
	if res.Assertions[0].OK {
		t.Error("first assertion (pod Ready) should be OK=false")
	}
}

// TestVerifyEgressNotFound confirms that a missing F5SPKEgress CR yields OK=false.
func TestVerifyEgressNotFound(t *testing.T) {
	stubPodOK := func(_ context.Context, _ *scenarios.Context, _, _ string, _ time.Duration) error {
		return nil
	}
	stubNotFound := func(_ context.Context, _ *scenarios.Context, _, _ string) (bool, string, error) {
		return false, "not found", nil
	}

	d := egresssnat.VerifyDeps{
		WaitPodReadyFn:   stubPodOK,
		GetF5SPKEgressFn: stubNotFound,
	}
	s := egresssnat.NewScenarioForTest(d)

	sctx := &scenarios.Context{
		Ctx:     context.Background(),
		Out:     io.Discard,
		Options: map[string]string{},
	}

	res := s.Verify(sctx)
	if res.AllPassed() {
		t.Error("expected AllPassed=false when F5SPKEgress not found")
	}
	if res.Assertions[1].OK {
		t.Error("second assertion (F5SPKEgress present) should be OK=false")
	}
}

// TestOptionsOverrides confirms egress-namespace and tmm-int-vlan overrides
// are reflected in the rendered manifests.
func TestOptionsOverrides(t *testing.T) {
	dir := t.TempDir()
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options: map[string]string{
			"egress-namespace": "custom-ns",
			"tmm-int-vlan":     "custom-vlan",
		},
	}

	s := scenarios.Find("egress-snat")
	if s == nil {
		t.Fatal("egress-snat not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}

	for _, p := range paths {
		raw, readErr := readFileHelper(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		content := string(raw)
		if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
			t.Errorf("manifest %s still contains template directives:\n%s", p, content)
		}
		if strings.HasSuffix(p, "03-f5spkegress.yaml") {
			if !strings.Contains(content, "custom-ns") {
				t.Errorf("03-f5spkegress.yaml should use custom-ns, got:\n%s", content)
			}
			if !strings.Contains(content, "custom-vlan") {
				t.Errorf("03-f5spkegress.yaml should use custom-vlan, got:\n%s", content)
			}
		}
	}
}
