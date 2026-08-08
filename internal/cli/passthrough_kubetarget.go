package cli

// resolveWorkspaceKubeTarget is the cli-side half of the workspace kubeconfig pin
// (issue #55). The orchestration layer owns WHEN the pin is applied — after the
// DisableFlagParsing `-w` extraction, and only on the local-exec side of the `--on`
// boundary; see internal/orchestration/kubetarget.go. This is only the lookup.
//
// It has to be a function rather than a resolved value: `-w` arrives inside argv on
// the passthrough verbs, so flagWorkspace is not yet correct at the time
// clusterInputs() is built. Orchestration calls this once flagWorkspace holds the
// workspace the user actually named.
//
// The three answers are distinct and must stay so:
//
//   - ("", "", nil)   — this workspace has no known cluster. Historical ambient
//     behaviour, deliberately: nothing that works today starts failing.
//   - (path, ctx, nil) — pin both.
//   - ("", "", err)   — a cluster IS known but no kubeconfig on disk can authenticate
//     to it. Surfaced, not swallowed: treating it as "no known cluster" falls back to
//     the SHARED forge kubeconfig, which is how `-w a kubectl` reached cluster b in
//     the first place. The `k` verbs already fail this way and tell the user to run
//     `kubeconfig --download`.
func resolveWorkspaceKubeTarget() (string, string, error) {
	tgt, err := workspaceKubeTarget()
	if err != nil {
		return "", "", err
	}
	return tgt.Path, tgt.Context, nil
}
