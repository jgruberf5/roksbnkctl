package cli

import (
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// stageEnvSplitWorkspace stages a minimal workspace whose API key
// resolves from the environment (api_key_source: env), so workspaceEnv()
// / workspaceEnvCore() run without keychain or prompt. Returns the
// workspace name; ROKSBNKCTL_HOME + IBMCLOUD_API_KEY are set via
// t.Setenv so the process env is restored after the test.
func stageEnvSplitWorkspace(t *testing.T) string {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	t.Setenv("IBMCLOUD_API_KEY", "test-api-key-value")
	const ws = "env-split-ws"
	w := &config.Workspace{}
	w.IBMCloud.Region = "us-south"
	w.IBMCloud.APIKeySource = "env"
	if err := config.SaveWorkspace(ws, w); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}
	prev := flagWorkspace
	t.Cleanup(func() { flagWorkspace = prev })
	flagWorkspace = ws
	return ws
}

func hasEnv(env []string, key string) bool {
	p := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, p) {
			return true
		}
	}
	return false
}

// TestWorkspaceEnvCore_OmitsKubeconfig_KeepsIBMCloud is the Sprint 13
// Issue 1 core assertion: the machine-portable core that crosses the
// --on SSH boundary carries the IBMCLOUD_* values and NEVER the local
// KUBECONFIG path.
func TestWorkspaceEnvCore_OmitsKubeconfig_KeepsIBMCloud(t *testing.T) {
	stageEnvSplitWorkspace(t)
	// Make sure a KUBECONFIG is present in the host env so the only way
	// it stays out of core is the split (not its absence).
	t.Setenv("KUBECONFIG", "/home/tester/.kube/config")

	_, core, err := workspaceEnvCore()
	if err != nil {
		t.Fatalf("workspaceEnvCore: %v", err)
	}
	for _, want := range []string{"IBMCLOUD_API_KEY", "IC_API_KEY", "IBMCLOUD_REGION", "IBMCLOUD_VERSION_CHECK"} {
		if !hasEnv(core, want) {
			t.Errorf("workspaceEnvCore missing %s (must forward to remote)", want)
		}
	}
	if hasEnv(core, "KUBECONFIG") {
		t.Errorf("workspaceEnvCore leaked KUBECONFIG — a local path must NOT cross the SSH boundary (Issue 1)")
	}
}

// TestWorkspaceEnv_LocalKeepsKubeconfig: the LOCAL exec env still
// carries KUBECONFIG (local kubectl/oc behaviour unchanged).
func TestWorkspaceEnv_LocalKeepsKubeconfig(t *testing.T) {
	stageEnvSplitWorkspace(t)
	t.Setenv("KUBECONFIG", "/home/tester/.kube/config")

	_, env, err := workspaceEnv()
	if err != nil {
		t.Fatalf("workspaceEnv: %v", err)
	}
	if !hasEnv(env, "KUBECONFIG") {
		t.Errorf("local workspaceEnv must keep KUBECONFIG (local kubectl/oc unchanged)")
	}
	if !hasEnv(env, "IBMCLOUD_API_KEY") {
		t.Errorf("local workspaceEnv missing IBMCLOUD_API_KEY")
	}
}

// TestRemoteSafeEnv_StripsLocalPathVars is the defense-in-depth backstop
// (dispatchRemote) — even if a future caller passes the full local env,
// remoteSafeEnv drops KUBECONFIG while preserving the IBMCLOUD_* values.
func TestRemoteSafeEnv_StripsLocalPathVars(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"KUBECONFIG=/home/jgruber/.kube/config",
		"IBMCLOUD_API_KEY=abc",
		"IC_API_KEY=abc",
		"IBMCLOUD_REGION=us-south",
		"IBMCLOUD_VERSION_CHECK=false",
	}
	out := remoteSafeEnv(in)
	if hasEnv(out, "KUBECONFIG") {
		t.Errorf("remoteSafeEnv did not strip KUBECONFIG: %v", out)
	}
	for _, want := range []string{"PATH", "IBMCLOUD_API_KEY", "IC_API_KEY", "IBMCLOUD_REGION", "IBMCLOUD_VERSION_CHECK"} {
		if !hasEnv(out, want) {
			t.Errorf("remoteSafeEnv dropped machine-portable var %s", want)
		}
	}
}

// TestRemoteSafeEnv_NilAndMalformed: empty input and malformed entries
// (no '=' / leading '=') don't panic and pass through unmangled.
func TestRemoteSafeEnv_NilAndMalformed(t *testing.T) {
	if got := remoteSafeEnv(nil); got != nil {
		t.Errorf("remoteSafeEnv(nil) = %v, want nil", got)
	}
	in := []string{"NOEQUALS", "=leadingeq", "OK=1", "KUBECONFIG=/x"}
	out := remoteSafeEnv(in)
	if hasEnv(out, "KUBECONFIG") {
		t.Errorf("remoteSafeEnv kept KUBECONFIG among malformed entries: %v", out)
	}
	if !hasEnv(out, "OK") {
		t.Errorf("remoteSafeEnv dropped a well-formed entry: %v", out)
	}
}
