package cli

// Top-level aliases for the most-common kubectl-internalised verbs so
// users typing `awsbnkctl get pods` don't have to learn the `k`
// prefix.
//
// Top-level shortcuts cover two verbs:
//
//   - `awsbnkctl get`  → fresh top-level command, instantiates a
//     second cobra.Command bound to the same flag vars as `k get`.
//
//   - `awsbnkctl logs` already exists as the BNK-component helper; its
//     handler in inspect.go falls through to the kubectl-style raw
//     pod-name path for unknown "component" names. So
//     `awsbnkctl logs my-pod-name` works without the `k` prefix.
//
//   - `awsbnkctl apply` is NOT aliased: a bare top-level `apply` would
//     be ambiguous against `awsbnkctl up` (the cluster lifecycle
//     surface) and break muscle memory. Users get the k8s apply via
//     `awsbnkctl k apply` explicitly.
//
// `exec` is intentionally NOT aliased: host-side `awsbnkctl exec
// <cmd>` already exists, and shadowing it would break user muscle
// memory. Cluster-side exec is `awsbnkctl k exec <pod>` only.
//
// Each top-level alias instantiates a fresh cobra.Command (rather
// than sharing the same instance with k_*.go) because cobra disallows
// the same Command being added to two parents.

func init() {
	rootCmd.AddCommand(newKGetCmd())
	// `logs` is shared with the existing component-aware command —
	// see runLogs() in inspect.go for the unknown-name fallthrough.
}
