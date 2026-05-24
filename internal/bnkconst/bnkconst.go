// Package bnkconst holds BNK-wide constants shared across packages that would
// otherwise create import cycles (e.g. phases ↔ render).
package bnkconst

const (
	// InstanceNamespace is the k8s namespace where the CNE instance resources
	// live (cloud-network-mapping CM, IRSA SA, CNEInstance CR, NADs).
	InstanceNamespace = "f5-cne-system"
)
