// Unit tests for KubeconfigWritePath + the DefaultKubeconfigPath
// roksbnkctl-base fallback added with the runner $HOME-independence fix.
// The write path must resolve a target even when nothing exists yet,
// preferring $KUBECONFIG, then $HOME/.kube, then $ROKSBNKCTL_HOME/.kube.

package k8s

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKubeconfigWritePath_HonorsKubeconfigEnv(t *testing.T) {
	want := filepath.Join(t.TempDir(), "explicit", "config")
	t.Setenv("KUBECONFIG", want)
	if got := KubeconfigWritePath(); got != want {
		t.Errorf("KubeconfigWritePath() = %q, want first $KUBECONFIG entry %q", got, want)
	}
}

func TestKubeconfigWritePath_FirstOfList(t *testing.T) {
	first := filepath.Join(t.TempDir(), "a", "config")
	second := filepath.Join(t.TempDir(), "b", "config")
	t.Setenv("KUBECONFIG", first+string(os.PathListSeparator)+second)
	if got := KubeconfigWritePath(); got != first {
		t.Errorf("KubeconfigWritePath() = %q, want first list entry %q", got, first)
	}
}

func TestKubeconfigWritePath_HomeDefault(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".kube", "config")
	if got := KubeconfigWritePath(); got != want {
		t.Errorf("KubeconfigWritePath() = %q, want $HOME default %q", got, want)
	}
}

// TestDefaultKubeconfigPath_BaseFallback — when neither $KUBECONFIG nor
// $HOME/.kube/config exist but $ROKSBNKCTL_HOME/.kube/config does, the
// read path must find it (the cross-invocation runner case).
func TestDefaultKubeconfigPath_BaseFallback(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	// Point HOME at an empty temp dir so ~/.kube/config doesn't exist.
	t.Setenv("HOME", t.TempDir())

	base := t.TempDir()
	t.Setenv("ROKSBNKCTL_HOME", base)
	want := filepath.Join(base, ".kube", "config")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte(minimalKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DefaultKubeconfigPath(); got != want {
		t.Errorf("DefaultKubeconfigPath() = %q, want base fallback %q", got, want)
	}
}
