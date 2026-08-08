package orchestration

import (
	"errors"
	"strings"
	"testing"
)

// The pin must not override a caller who has already said which cluster they mean.
// Overriding an explicit --context would be its own silent-wrong-cluster bug, in the
// opposite direction.
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

// The resolver has THREE answers and the third must not collapse into the first.
//
// "known cluster, but nothing on disk can authenticate to it" is an error the user
// has to see — swallowing it falls through to the SHARED forge kubeconfig, which is
// how `-w a kubectl` reached cluster b in the first place (issue #55).
func TestResolveKubeTarget_ErrorIsNotSwallowed(t *testing.T) {
	want := errors.New("no kubeconfig addresses workspace")
	in := &ClusterInputs{ResolveKubeTarget: func() (string, string, error) { return "", "", want }}
	if _, err := resolveKubeTarget(in); !errors.Is(err, want) {
		t.Fatalf("resolver error must be propagated, got %v", err)
	}

	// No known cluster is NOT an error — the ambient behaviour is deliberate.
	in = &ClusterInputs{ResolveKubeTarget: func() (string, string, error) { return "", "", nil }}
	tgt, err := resolveKubeTarget(in)
	if err != nil || tgt.Path != "" || tgt.Context != "" {
		t.Fatalf("unknown cluster must resolve to a silent zero target, got %+v / %v", tgt, err)
	}

	// No resolver injected at all (ibmcloud, tests) must be a no-op, not a panic.
	if tgt, err := resolveKubeTarget(&ClusterInputs{}); err != nil || tgt.Path != "" {
		t.Fatalf("absent resolver must be a no-op, got %+v / %v", tgt, err)
	}
	if tgt, err := resolveKubeTarget(nil); err != nil || tgt.Path != "" {
		t.Fatalf("nil inputs must be a no-op, got %+v / %v", tgt, err)
	}
}

// The pin must never come at the cost of the expiring-credential self-heal.
// EnsureFreshKubeconfig re-mints admin client certs near expiry; skipping it on the
// pinned path trades a wrong-cluster bug for an auth failure where the tool used to
// heal itself. What the pin suppresses is only the SUBSTITUTION of the shared file.
func TestPinLocalKubeconfig_PinWinsWithoutLosingRefresh(t *testing.T) {
	// A pinned target wins over whatever the shared-forge preference would pick.
	env := []string{"PATH=/bin", "KUBECONFIG=/ambient/config"}
	got := pinLocalKubeconfig(t.Context(), &ClusterInputs{}, env, resolvedKubeTarget{Path: "/ws/config"}, false)
	if v := envValue(got, "KUBECONFIG"); v != "/ws/config" {
		t.Errorf("pinned kubeconfig must win, got %q", v)
	}
	// No target: the ambient value survives when there is nothing better.
	got = pinLocalKubeconfig(t.Context(), &ClusterInputs{}, env, resolvedKubeTarget{}, false)
	if v := envValue(got, "KUBECONFIG"); v != "/ambient/config" {
		t.Errorf("unpinned must leave the ambient value alone, got %q", v)
	}
}

// Injecting --context is only safe AFTER the leading `--` has been stripped, which
// applyWorkspaceFlag and extractOnFlag both do. Prepending earlier displaces the
// separator: it survives into kubectl, whose own cobra stops command lookup at it, so
// `roksbnkctl kubectl -- get pods` fails with a usage error instead of running.
func TestInjectKubeContext_AfterSeparatorStrip(t *testing.T) {
	// This is the argv the passthrough actually sees — `--` already gone.
	_, argv := ExtractWorkspaceFlag([]string{"-w", "b", "--", "get", "pods"})
	if strings.Join(argv, " ") != "get pods" {
		t.Fatalf("precondition: leading -- should be stripped, got %v", argv)
	}
	out := injectKubeContext(argv, "ctx-b")
	if strings.Join(out, " ") != "--context ctx-b get pods" {
		t.Errorf("got %v", out)
	}
	// An empty context injects nothing.
	if out := injectKubeContext(argv, ""); strings.Join(out, " ") != "get pods" {
		t.Errorf("empty context must inject nothing, got %v", out)
	}
}

// The pin resolves the workspace the user NAMED, not the one that happened to be
// current — the passthroughs disable flag parsing, so -w only becomes visible once
// applyWorkspaceFlag has pulled it out of argv. Resolving any earlier pins the wrong
// cluster, deterministically, which is worse than the ambient bug it replaces.
func TestApplyWorkspaceFlag_RunsBeforeTheResolver(t *testing.T) {
	seen := ""
	in := &ClusterInputs{
		Workspace:         "current",
		SetWorkspace:      func(string) {},
		ResolveKubeTarget: func() (string, string, error) { seen = "resolved"; return "", "", nil },
	}
	argv := applyWorkspaceFlag(in, []string{"-w", "named", "get", "nodes"})
	if in.Workspace != "named" {
		t.Fatalf("workspace must be taken from argv before any resolution, got %q", in.Workspace)
	}
	if seen != "" {
		t.Fatal("the resolver must not have run yet at this point")
	}
	if strings.Join(argv, " ") != "get nodes" {
		t.Errorf("argv should be cleaned of -w, got %v", argv)
	}
}
