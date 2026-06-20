package orchestration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

func jwt(exp time.Time) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix()})
	return hdr + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func writeForge(t *testing.T, jwtTok string) string {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	path, err := config.ForgeKubeconfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: https://x:1, certificate-authority-data: Q0E=}
users:
- name: c-token
  user: {token: %s}
`, jwtTok)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnsureFreshKubeconfig_NoFileFallsBack(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	in := &ClusterInputs{OpenIBMClient: func() (*config.Context, *ibm.Client, error) {
		t.Fatal("OpenIBMClient must not be called when there is no forge kubeconfig")
		return nil, nil, nil
	}}
	if got := EnsureFreshKubeconfig(context.Background(), in, false); got != "" {
		t.Errorf("want empty (fall back), got %q", got)
	}
}

func TestEnsureFreshKubeconfig_FreshIsNoOp(t *testing.T) {
	path := writeForge(t, jwt(time.Now().Add(48*time.Hour)))
	before, _ := os.ReadFile(path)
	in := &ClusterInputs{OpenIBMClient: func() (*config.Context, *ibm.Client, error) {
		t.Fatal("OpenIBMClient must not be called when the token is still fresh (no network)")
		return nil, nil, nil
	}}
	got := EnsureFreshKubeconfig(context.Background(), in, false)
	if got != path {
		t.Errorf("want %q, got %q", path, got)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("fresh kubeconfig must not be rewritten")
	}
}

func TestEnsureFreshKubeconfig_StaleRefreshFailureFallsBack(t *testing.T) {
	// Expired token + a refresh that can't reach IBM → fall back ("") so
	// the caller uses the admin/default config rather than a dead token.
	writeForge(t, jwt(time.Now().Add(-time.Hour)))
	called := false
	in := &ClusterInputs{OpenIBMClient: func() (*config.Context, *ibm.Client, error) {
		called = true
		return nil, nil, fmt.Errorf("offline")
	}}
	if got := EnsureFreshKubeconfig(context.Background(), in, false); got != "" {
		t.Errorf("expired + failed refresh should fall back to \"\", got %q", got)
	}
	if !called {
		t.Error("a stale token should have triggered a refresh attempt")
	}
}

func TestEnsureFreshKubeconfig_ForceTriggersRefresh(t *testing.T) {
	// Even a fresh token must attempt a refresh when force is set.
	writeForge(t, jwt(time.Now().Add(48*time.Hour)))
	called := false
	in := &ClusterInputs{OpenIBMClient: func() (*config.Context, *ibm.Client, error) {
		called = true
		return nil, nil, fmt.Errorf("offline")
	}}
	// Refresh fails (offline) but token is still valid → returns path.
	if got := EnsureFreshKubeconfig(context.Background(), in, true); got == "" {
		t.Error("force-refresh with a still-valid token should keep returning the path")
	}
	if !called {
		t.Error("force should trigger a refresh attempt even when fresh")
	}
}

func TestSetEnvKV(t *testing.T) {
	// replace existing
	got := setEnvKV([]string{"A=1", "KUBECONFIG=/old", "B=2"}, "KUBECONFIG", "/new")
	want := []string{"A=1", "KUBECONFIG=/new", "B=2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("replace: got %v want %v", got, want)
	}
	// append when absent
	got = setEnvKV([]string{"A=1"}, "KUBECONFIG", "/new")
	if fmt.Sprint(got) != fmt.Sprint([]string{"A=1", "KUBECONFIG=/new"}) {
		t.Errorf("append: got %v", got)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "kc.yaml")
	if err := writeFileAtomic(p, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "hello" {
		t.Fatalf("content = %q err = %v", b, err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", fi.Mode().Perm())
	}
	// no leftover temp files
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the target file, got %d entries", len(entries))
	}
}
