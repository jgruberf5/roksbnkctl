// Unit tests for prepareToolEnv — the runner $HOME-independence fix that
// points Helm's repo cache/config and the admin kubeconfig at writable,
// ROKSBNKCTL_HOME-relative paths and pre-creates the dirs.

package tf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareToolEnv_SetsHelmAndPrecreates(t *testing.T) {
	base := t.TempDir()
	t.Setenv("ROKSBNKCTL_HOME", base)
	// Clear any inherited values so we observe what prepareToolEnv sets.
	for _, k := range []string{
		"HELM_CACHE_HOME", "HELM_CONFIG_HOME", "HELM_DATA_HOME",
		"HELM_REPOSITORY_CACHE", "HELM_REPOSITORY_CONFIG", "HELM_REGISTRY_CONFIG",
	} {
		t.Setenv(k, "")
	}

	if err := prepareToolEnv(); err != nil {
		t.Fatalf("prepareToolEnv: %v", err)
	}

	// HELM_CACHE_HOME is the var that actually governs the anonymous
	// `repository=<url>` chart download (helmpath.CachePath reads it); its
	// repository/ leaf must be pre-created and writable.
	cacheHome := os.Getenv("HELM_CACHE_HOME")
	wantCacheHome := filepath.Join(base, ".helm", "cache")
	if cacheHome != wantCacheHome {
		t.Errorf("HELM_CACHE_HOME = %q, want %q", cacheHome, wantCacheHome)
	}
	repoDir := filepath.Join(cacheHome, "repository")
	if st, err := os.Stat(repoDir); err != nil || !st.IsDir() {
		t.Errorf("HELM_CACHE_HOME/repository not pre-created: %v", err)
	}
	if got, want := os.Getenv("HELM_CONFIG_HOME"), filepath.Join(base, ".helm", "config"); got != want {
		t.Errorf("HELM_CONFIG_HOME = %q, want %q", got, want)
	}
	if got, want := os.Getenv("HELM_DATA_HOME"), filepath.Join(base, ".helm", "data"); got != want {
		t.Errorf("HELM_DATA_HOME = %q, want %q", got, want)
	}
	if got, want := os.Getenv("HELM_REPOSITORY_CACHE"), repoDir; got != want {
		t.Errorf("HELM_REPOSITORY_CACHE = %q, want %q", got, want)
	}
}

// TestPrepareToolEnv_CleanRegistryConfigs — the helm registry config and
// DOCKER_CONFIG must be fresh files with an empty `auths` and NO credsStore, so the
// helm provider's OCI login stores inline (the Windows credential-helper fix).
func TestPrepareToolEnv_CleanRegistryConfigs(t *testing.T) {
	base := t.TempDir()
	t.Setenv("ROKSBNKCTL_HOME", base)
	for _, k := range []string{"HELM_REGISTRY_CONFIG", "DOCKER_CONFIG"} {
		t.Setenv(k, "")
	}
	if err := prepareToolEnv(); err != nil {
		t.Fatalf("prepareToolEnv: %v", err)
	}

	reg := os.Getenv("HELM_REGISTRY_CONFIG")
	if reg == "" {
		t.Fatal("HELM_REGISTRY_CONFIG not set")
	}
	dockerDir := os.Getenv("DOCKER_CONFIG")
	if dockerDir == "" {
		t.Fatal("DOCKER_CONFIG not set")
	}
	for _, p := range []string{reg, filepath.Join(dockerDir, "config.json")} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		s := string(b)
		if !strings.Contains(s, `"auths"`) {
			t.Errorf("%s missing auths: %q", p, s)
		}
		// The whole point: no credsStore/credHelpers, so storage stays inline.
		if strings.Contains(s, "credsStore") || strings.Contains(s, "credHelpers") {
			t.Errorf("%s must not configure a credential helper: %q", p, s)
		}
	}
}

// TestPrepareToolEnv_RespectsOperatorOverride — a pre-set value must win
// so an operator can point the helm cache at an air-gap mirror.
func TestPrepareToolEnv_RespectsOperatorOverride(t *testing.T) {
	t.Setenv("ROKSBNKCTL_HOME", t.TempDir())
	custom := filepath.Join(t.TempDir(), "mirror-cache")
	t.Setenv("HELM_CACHE_HOME", custom)

	if err := prepareToolEnv(); err != nil {
		t.Fatalf("prepareToolEnv: %v", err)
	}
	if got := os.Getenv("HELM_CACHE_HOME"); got != custom {
		t.Errorf("HELM_CACHE_HOME = %q, want operator override %q", got, custom)
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
