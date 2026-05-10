package k8s

import (
	"errors"
)

// BuildOpenShiftClient is the reserved entry point for Phase 2.1
// (PRD 02 §"OpenShift extensions"). When implemented, this function
// will return a typed openshift/client-go *clientset.Clientset built
// from the same kubeconfig discovery as BuildClientset, plus a scheme
// registration step that wires Project/Route/ImageStream/etc. into the
// resource.Builder so `roksbnkctl k get projects` works against ROKS
// clusters without a hardcoded list.
//
// Phase 2.0 ships without this — the dynamic-client + RESTMapper path
// in get.go already discovers OpenShift CRDs late-bound (the cluster
// advertises them via the API discovery doc and our DeferredDiscovery
// mapper picks them up). The typed-client path is purely an optimisation
// for Phase 2.1.
//
// Tracked in issues/issue_sprint2_staff.md.
func BuildOpenShiftClient(_ string) (interface{}, error) {
	return nil, errors.New("BuildOpenShiftClient not implemented (Phase 2.1; see issue_sprint2_staff.md)")
}
