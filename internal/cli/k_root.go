package cli

import "github.com/spf13/cobra"

// kCmd is the parent command for kubectl-internalised verbs. The
// disambiguation pattern (PRD 02 §"Disambiguating roksbnkctl exec",
// Option B): host-side `roksbnkctl exec` stays as-is; cluster-side
// equivalent is `roksbnkctl k exec`. Same shape for `k get`, `k apply`,
// `k logs`, `k describe`, `k delete`, `k port-forward`.
//
// Top-level shortcuts for the most common verbs (`get`, `apply`,
// `logs`) are added in k_aliases.go so users don't need to learn the
// `k` prefix.
var kCmd = &cobra.Command{
	Use:   "k",
	Short: "Kubernetes verbs (kubectl-internalised; no host kubectl required)",
	Long: `roksbnkctl k <verb> runs the BNK-relevant kubectl/oc verb subset
natively in-process via client-go, with no host kubectl/oc binary
required. Output formatting matches kubectl byte-for-byte for
-o yaml/json/wide/name/jsonpath/go-template.

Verbs:
  k get          list/show resources
  k describe     human-friendly resource detail (delegates to kubectl/pkg/describe)
  k apply        server-side apply for files, dirs, kustomize bases, or stdin
  k delete       delete with cascade + grace period control
  k logs         pod or component logs (extends roksbnkctl logs)
  k exec         exec into a pod via SPDY
  k port-forward forward a local port to a pod via SPDY

The existing roksbnkctl kubectl / roksbnkctl oc passthroughs remain as
escape hatches for verbs not internalised here (edit, patch, rollout,
scale, etc.) — they require kubectl/oc on PATH.`,
}

func init() {
	rootCmd.AddCommand(kCmd)
}
