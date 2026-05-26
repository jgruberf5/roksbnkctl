package cli

// Top-level aliases for the most-common kubectl-internalised verbs so
// users typing `awsbnkctl get pods` don't have to learn the `k`
// prefix.
//
// PRD 02 §"Top-level shortcuts" lists three: get, apply, logs.
// In practice we ship two:
//
//   - `awsbnkctl get`  → fresh top-level command, instantiates a
//     second cobra.Command bound to the same flag vars as `k get`.
//
//   - `awsbnkctl logs` already exists (Sprint 0) as the BNK-component
//     helper; we extended its handler in inspect.go so an unknown
//     "component" name falls through to the kubectl-style raw pod-name
//     path. So `awsbnkctl logs my-pod-name` works without users
//     needing the `k` prefix.
//
//   - `awsbnkctl apply` is NOT aliased: a bare top-level `apply` would
//     be ambiguous against `awsbnkctl up` (the cluster lifecycle
//     surface) and break muscle memory. Users get the k8s apply via
//     `awsbnkctl k apply` explicitly. Tracked in
//     issues/issue_sprint2_staff.md.
//
// `exec` is intentionally NOT aliased: host-side `awsbnkctl exec
// <cmd>` already exists (Sprint 1), and shadowing it would break user
// muscle memory. Cluster-side exec is `awsbnkctl k exec <pod>` only.
//
// Each top-level alias instantiates a fresh cobra.Command (rather
// than sharing the same instance with k_*.go) because cobra disallows
// the same Command being added to two parents.

func init() {
	rootCmd.AddCommand(newKGetCmd())
	// `logs` is shared with the existing component-aware command —
	// see runLogs() in inspect.go for the unknown-name fallthrough.
}
