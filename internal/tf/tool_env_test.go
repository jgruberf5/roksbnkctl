// Unit tests for prepareToolEnv — the runner $HOME-independence fix that
// points Helm's repo cache/config and the admin kubeconfig at writable,
// ROKSBNKCTL_HOME-relative paths and pre-creates the dirs.

package tf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareToolEnv_SetsHelmAndPrecreates(t *testing.T) {
	base := t.TempDir()
	t.Setenv("ROKSBNKCTL_HOME", base)
	// Clear any inherited values so we observe what prepareToolEnv sets.
	t.Setenv("HELM_REPOSITORY_CACHE", "")
	t.Setenv("HELM_REPOSITORY_CONFIG", "")
	t.Setenv("HELM_REGISTRY_CONFIG", "")

	if err := prepareToolEnv(); err != nil {
		t.Fatalf("prepareToolEnv: %v", err)
	}

	cache := os.Getenv("HELM_REPOSITORY_CACHE")
	wantCache := filepath.Join(base, ".helm", "cache")
	if cache != wantCache {
		t.Errorf("HELM_REPOSITORY_CACHE = %q, want %q", cache, wantCache)
	}
	if st, err := os.Stat(cache); err != nil || !st.IsDir() {
		t.Errorf("HELM_REPOSITORY_CACHE dir not pre-created: %v", err)
	}
	wantCfg := filepath.Join(base, ".helm", "config", "repositories.yaml")
	if got := os.Getenv("HELM_REPOSITORY_CONFIG"); got != wantCfg {
		t.Errorf("HELM_REPOSITORY_CONFIG = %q, want %q", got, wantCfg)
	}
	if st, err := os.Stat(filepath.Dir(wantCfg)); err != nil || !st.IsDir() {
		t.Errorf("HELM_REPOSITORY_CONFIG parent dir not pre-created: %v", err)
	}
}

// TestPrepareToolEnv_RespectsOperatorOverride — a pre-set value must win
// so an operator can point the helm cache at an air-gap mirror.
func TestPrepareToolEnv_RespectsOperatorOverride(t *testing.T) {
	t.Setenv("ROKSBNKCTL_HOME", t.TempDir())
	custom := filepath.Join(t.TempDir(), "mirror-cache")
	t.Setenv("HELM_REPOSITORY_CACHE", custom)

	if err := prepareToolEnv(); err != nil {
		t.Fatalf("prepareToolEnv: %v", err)
	}
	if got := os.Getenv("HELM_REPOSITORY_CACHE"); got != custom {
		t.Errorf("HELM_REPOSITORY_CACHE = %q, want operator override %q", got, custom)
	}
}

// TestPrepareToolEnv_KubeconfigFallbackOnUnwritableHome — when $HOME is
// not writable (the runner case), $KUBECONFIG is redirected to the
// workspace tree; on a writable $HOME it is left unset (preserving the
// conventional ~/.kube/config location).
func TestPrepareToolEnv_KubeconfigFallbackOnUnwritableHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("ROKSBNKCTL_HOME", base)
	t.Setenv("KUBECONFIG", "")
	// A HOME under a file (not a dir) makes MkdirAll($HOME/.kube) fail.
	bad := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(bad, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", bad)

	if err := prepareToolEnv(); err != nil {
		t.Fatalf("prepareToolEnv: %v", err)
	}
	want := filepath.Join(base, ".kube", "config")
	if got := os.Getenv("KUBECONFIG"); got != want {
		t.Errorf("KUBECONFIG = %q, want workspace fallback %q", got, want)
	}
}

func TestPrepareToolEnv_KubeconfigUnsetOnWritableHome(t *testing.T) {
	t.Setenv("ROKSBNKCTL_HOME", t.TempDir())
	t.Setenv("KUBECONFIG", "")
	t.Setenv("HOME", t.TempDir()) // a real, writable dir

	if err := prepareToolEnv(); err != nil {
		t.Fatalf("prepareToolEnv: %v", err)
	}
	if got := os.Getenv("KUBECONFIG"); got != "" {
		t.Errorf("KUBECONFIG = %q, want unset (writable HOME keeps ~/.kube/config)", got)
	}
}
