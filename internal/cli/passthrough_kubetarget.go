package cli

import "strings"

// pinPassthroughToWorkspaceCluster makes `roksbnkctl -w <ws> kubectl …` and
// `… oc …` address the SAME cluster the `k` verbs address.
//
// THE BUG (issue #55). The `k` verbs resolve workspaceKubeTarget() and pin both the
// kubeconfig file and the CONTEXT, so `-w` genuinely selects a cluster. The raw
// passthroughs did not: they took the ambient KUBECONFIG and then
// preferForgeKubeconfig() replaced it with ~/.roksbnkctl/forge/kubeconfig.yaml — a
// single file shared by every workspace. Two workspaces on different clusters
// therefore retargeted each other, and `-w a kubectl get nodes` could list the nodes
// of cluster b.
//
// The failure is silent and the output is believable, which is the dangerous
// combination: `-w a kubectl delete …` would delete in b. Downstream harnesses had
// grown an explicit "verify the cluster identity before asserting anything" guard to
// work around it.
//
// The fix reuses the resolver that already exists rather than adding a second
// mechanism — the passthroughs now go through exactly what `k get` goes through.
//
// It stays conservative in two ways, so nothing that works today starts failing:
//
//   - a workspace with no known cluster (no cluster-outputs.json) resolves to an
//     empty target, and the ambient behaviour is left alone;
//   - an explicit --context or --kubeconfig in the user's own arguments wins. Someone
//     who says which cluster they mean is not overridden.
func pinPassthroughToWorkspaceCluster(tool string, args []string) ([]string, string) {
	if tool != "kubectl" && tool != "oc" {
		return args, "" // ibmcloud and friends have no kubeconfig notion
	}
	if userPinnedCluster(args) {
		return args, ""
	}
	tgt, err := workspaceKubeTarget()
	if err != nil || tgt.Path == "" {
		// Unknown cluster for this workspace — historical ambient behaviour.
		return args, ""
	}
	// The context rides on argv because the wrapped tool owns its own flag parsing;
	// prepending keeps it ahead of any `--` the user passes. The kubeconfig path goes
	// back to the caller so it can be handed to the orchestration layer explicitly —
	// mutating the process env would be silently overridden further down.
	if tgt.Context != "" {
		args = append([]string{"--context", tgt.Context}, args...)
	}
	return args, tgt.Path
}

// userPinnedCluster reports whether the caller already said which cluster they mean.
// Both the space form (`--context foo`) and the equals form (`--context=foo`) count,
// as do the kubeconfig equivalents.
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
