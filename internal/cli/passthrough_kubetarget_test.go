package cli

import (
	"strings"
	"testing"
)

// The passthrough must not override a caller who has already said which cluster they
// mean. Overriding an explicit --context would be its own silent-wrong-cluster bug,
// in the opposite direction.
func TestUserPinnedCluster(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"get", "nodes"}, false},
		{[]string{"--context", "other", "get", "nodes"}, true},
		{[]string{"--context=other", "get", "nodes"}, true},
		{[]string{"--kubeconfig", "/tmp/kc", "get", "nodes"}, true},
		{[]string{"--kubeconfig=/tmp/kc", "get", "nodes"}, true},
		// Not a cluster selector — must NOT count as pinning.
		{[]string{"get", "pods", "--all-namespaces"}, false},
		{[]string{"config", "current-context"}, false},
	}
	for _, c := range cases {
		if got := userPinnedCluster(c.args); got != c.want {
			t.Errorf("userPinnedCluster(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

// Only kubectl and oc have a kubeconfig notion; ibmcloud must pass through untouched.
func TestPinPassthrough_OnlyKubeTools(t *testing.T) {
	args, pinned := pinPassthroughToWorkspaceCluster("ibmcloud", []string{"is", "vpcs"})
	if pinned != "" {
		t.Errorf("ibmcloud must not be pinned to a kubeconfig, got %q", pinned)
	}
	if strings.Join(args, " ") != "is vpcs" {
		t.Errorf("ibmcloud args must be untouched, got %v", args)
	}
}

// An explicit --context short-circuits before any workspace resolution, so the
// caller's choice survives even when the workspace has a cluster of its own.
func TestPinPassthrough_RespectsExplicitContext(t *testing.T) {
	in := []string{"--context", "mine", "get", "nodes"}
	args, pinned := pinPassthroughToWorkspaceCluster("kubectl", in)
	if pinned != "" {
		t.Errorf("an explicit --context must not be overridden with a pinned kubeconfig, got %q", pinned)
	}
	if strings.Join(args, " ") != strings.Join(in, " ") {
		t.Errorf("args must be untouched, got %v", args)
	}
}
