package orchestration

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// The workspace-scoped kubeconfig pin (issue #55).
//
// THE BUG. The `k` verbs resolve a workspace-scoped kubeconfig AND context, so `-w`
// genuinely selects a cluster. The local-exec verbs — shell, exec, and the raw
// kubectl/oc passthroughs — did not: they took the ambient KUBECONFIG and then
// preferForgeKubeconfig() replaced it with ~/.roksbnkctl/forge/kubeconfig.yaml, a
// single file every workspace shares. Two workspaces on different clusters therefore
// retargeted each other, and `-w a kubectl get nodes` could list cluster b's nodes.
// Silent, with believable output; `-w a kubectl delete …` deleted in b.
//
// WHERE THE PIN BELONGS. Every local-exec verb funnels through this one place, for
// three reasons that each cost a bug when the pin lived at the cobra call sites:
//
//   - ORDER. The passthroughs run with DisableFlagParsing, so cobra never binds the
//     root persistent -w; applyWorkspaceFlag pulls it out of argv here, inside
//     orchestration. Resolving the target any earlier reads the CURRENT workspace and
//     pins the wrong cluster — deterministically, which is worse than the ambient bug
//     it replaces.
//   - THE SSH BOUNDARY. A pin is a local filesystem path and a local context name.
//     Neither means anything on an --on target, which is why WorkspaceEnvCore exists.
//     Resolving after the local/remote decision keeps it local by construction.
//   - COVERAGE. shell and exec reach a cluster too. Pinning only kubectl and oc left
//     the same cross-workspace retarget reachable through two other verbs.
//
// The resolver itself stays in internal/cli (it reads the flag globals and the
// workspace layout); it arrives here as ClusterInputs.ResolveKubeTarget so this
// package never imports internal/cli.

// resolvedKubeTarget is what the cli-side resolver answers with. All-empty means
// "this workspace has no known cluster" — the historical ambient behaviour applies.
type resolvedKubeTarget struct {
	Path    string
	Context string
}

// resolveKubeTarget asks the injected resolver for THIS workspace's cluster.
//
// The error is propagated rather than swallowed, because the resolver distinguishes
// two cases that must not be conflated:
//
//   - no known cluster → zero value, nil error → ambient behaviour, deliberately;
//   - known cluster, but no kubeconfig on disk can authenticate to it → error.
//
// Treating the second as the first is how the shared forge kubeconfig gets used: the
// caller ends up talking to whatever cluster that file happens to point at, which is
// the whole of issue #55 wearing a different hat. The `k` verbs surface this error and
// tell the user to run `kubeconfig --download`; so do we.
func resolveKubeTarget(in *ClusterInputs) (resolvedKubeTarget, error) {
	if in == nil || in.ResolveKubeTarget == nil {
		return resolvedKubeTarget{}, nil
	}
	path, kubeCtx, err := in.ResolveKubeTarget()
	if err != nil {
		return resolvedKubeTarget{}, err
	}
	return resolvedKubeTarget{Path: path, Context: kubeCtx}, nil
}

// pinLocalKubeconfig composes the env for a LOCAL exec, preferring this workspace's
// own kubeconfig over the shared forge file.
//
// When refresh is set, the credential self-heal runs on BOTH paths.
// EnsureFreshKubeconfig re-mints admin client certs when the session is close to
// expiry; skipping it on the pinned path would trade the wrong-cluster bug for an
// auth failure where the tool used to heal itself and carry on. What the pin
// suppresses is only the SUBSTITUTION of the shared forge file — not the refresh.
//
// refresh is false for verbs that never refreshed before (`exec`), so pinning does
// not quietly add a credential round-trip to `roksbnkctl exec ls`.
func pinLocalKubeconfig(ctx context.Context, in *ClusterInputs, env []string, tgt resolvedKubeTarget, refresh bool) []string {
	fresh := ""
	if refresh {
		fresh = EnsureFreshKubeconfig(ctx, in, false)
	}
	if tgt.Path != "" {
		return setEnvKV(env, "KUBECONFIG", tgt.Path)
	}
	if fresh == "" {
		return env
	}
	return setEnvKV(env, "KUBECONFIG", fresh)
}

// injectKubeContext puts `--context <name>` in front of the wrapped tool's arguments.
//
// Safe to prepend here and nowhere earlier: argv has already been through
// applyWorkspaceFlag and extractOnFlag, both of which strip a leading `--`. Injecting
// before that strip displaces the separator, it survives into kubectl, and kubectl's
// own cobra stops command lookup at it — so `roksbnkctl kubectl -- get pods` fails
// with a usage error instead of running.
func injectKubeContext(args []string, kubeCtx string) []string {
	if kubeCtx == "" {
		return args
	}
	return append([]string{"--context", kubeCtx}, args...)
}

// userPinnedCluster reports whether the caller already said which cluster they mean.
// Someone explicit about the target is never overridden. Both the space form
// (`--context foo`) and the equals form (`--context=foo`) count, as do the kubeconfig
// equivalents.
func userPinnedCluster(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--context" || a == "--kubeconfig":
			return true
		case strings.HasPrefix(a, "--context=") || strings.HasPrefix(a, "--kubeconfig="):
			return true
		}
	}
	return false
}

// noteUnpinnableContext warns when a resolved context cannot be expressed.
//
// shell and exec run arbitrary commands, so there is no argv slot to put `--context`
// in and no environment variable kubectl reads for it. Pinning KUBECONFIG still puts
// the right FILE in place, which is the larger half of the fix; when that file's
// current-context is not the workspace's cluster, say so rather than let the next
// command quietly address a different one.
func noteUnpinnableContext(in *ClusterInputs, tgt resolvedKubeTarget, verb string) {
	if tgt.Path == "" || tgt.Context == "" {
		return
	}
	cur := k8s.KubeconfigCurrentContext(tgt.Path)
	if cur == "" || cur == tgt.Context {
		return
	}
	ws := ""
	if in != nil {
		ws = in.Workspace
	}
	fmt.Fprintf(os.Stderr,
		"! %s: %s is current-context %q, but workspace %q addresses context %q.\n"+
			"  Run `kubectl config use-context %s`, or pass --context explicitly.\n",
		verb, tgt.Path, cur, ws, tgt.Context, tgt.Context)
}
