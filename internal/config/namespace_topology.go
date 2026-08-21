package config

import (
	"fmt"
	"strings"
)

// THE BNK NAMESPACES ARE CREATE-TIME SETTINGS.
//
// BNK installs into two namespaces by default — flo_namespace for the controller
// and TMM, flo_utils_namespace for the shared components (CWC, RabbitMQ,
// Coremond, CRDConversion, Observer, Fluentd, OTEL, IPAM). Pointing both at one
// name is supported and is what a customer asks for when they want a single RBAC
// surface (#66).
//
// THE HARM THIS PREVENTS. Choosing one namespace is safe on a NEW install and
// destructive on an existing one. kubernetes_namespace_v1.f5_utils is guarded by
// `count = ... && utils != flo`, so collapsing the two on a live workspace takes
// that count from one to zero — and terraform deletes the namespace. Everything
// inside it goes with it: every shared component, none of which terraform
// manages and none of which it will recreate, plus the bnk-license CR. The
// License CR's finalizers and the license ResourceQuota can then leave the
// namespace stuck Terminating, so the failure is not even clean.
//
// None of that reads as destruction in a plan. It reads as one namespace being
// removed.
//
// The same applies to renaming either namespace, and to expanding one back into
// two: the components do not move, so the result is an install whose parts are
// somewhere FLO is no longer looking.
//
// So both names are fixed at install time. This refuses rather than warns —
// following network_mode, not vpc_cidr. Nothing can be relying on changing these
// today, because changing them has never worked; there is no existing contract
// to deprecate.

const (
	// DefaultFLONamespace and DefaultFLOUtilsNamespace mirror the flo_namespace /
	// flo_utils_namespace terraform defaults. An empty bnk.flo_namespace means
	// "whatever terraform defaults to", so a comparison against a recorded
	// install has to resolve the empty case the same way terraform does or a
	// workspace that never set the field would look like a change from the one
	// that did.
	DefaultFLONamespace      = "f5-bnk"
	DefaultFLOUtilsNamespace = "f5-utils"
)

// BNKNamespaces returns the namespaces this workspace will install into,
// resolving empty values to the terraform defaults.
func (w *Workspace) BNKNamespaces() (flo, utils string) {
	flo, utils = DefaultFLONamespace, DefaultFLOUtilsNamespace
	if w == nil {
		return flo, utils
	}
	if v := strings.TrimSpace(w.BNK.FLONamespace); v != "" {
		flo = v
	}
	if v := strings.TrimSpace(w.BNK.FLOUtilsNamespace); v != "" {
		utils = v
	}
	return flo, utils
}

// CheckNamespaceTopology refuses a change to either BNK namespace on a workspace
// that has already installed BNK.
//
// applied is the previously-applied tfvars for the BNK phase. A nil or empty map
// means nothing has been applied yet — a first install can choose freely, which
// is the whole point of calling these create-time rather than immutable.
//
// Silent when the record carries neither namespace: a snapshot written before
// these were rendered says nothing about what was built, and inventing a
// comparison from that would refuse installs that are fine.
func CheckNamespaceTopology(w *Workspace, applied map[string]string) error {
	if w == nil || len(applied) == 0 {
		return nil
	}
	priorFLO := tfvarString(applied["flo_namespace"])
	priorUtils := tfvarString(applied["flo_utils_namespace"])
	if priorFLO == "" && priorUtils == "" {
		return nil
	}
	// A snapshot that recorded only one of the pair still pins that one; the
	// other resolves to its default exactly as terraform would have.
	if priorFLO == "" {
		priorFLO = DefaultFLONamespace
	}
	if priorUtils == "" {
		priorUtils = DefaultFLOUtilsNamespace
	}

	flo, utils := w.BNKNamespaces()
	if flo == priorFLO && utils == priorUtils {
		return nil
	}

	// The collapse gets its own message because it is the one that destroys
	// something, and because it is the one someone will reach for deliberately
	// after reading that one namespace is supported. It is — on a new install.
	if utils == flo && priorUtils != priorFLO {
		return fmt.Errorf("BNK is already installed into two namespaces (%s and %s), and this "+
			"workspace now asks for one (%s).\n\n"+
			"  Collapsing them would DELETE the %s namespace and everything in it — CWC,\n"+
			"  RabbitMQ, Coremond, CRDConversion, Observer, Fluentd, OTEL, IPAM, and the\n"+
			"  bnk-license CR. None of those are managed by terraform, so none come back.\n\n"+
			"  One namespace is a create-time choice. To use it, install into a new\n"+
			"  workspace with bnk.flo_namespace and bnk.flo_utils_namespace both set to %s,\n"+
			"  or set bnk.flo_utils_namespace back to %s to keep this deployment",
			priorFLO, priorUtils, flo, priorUtils, flo, priorUtils)
	}

	return fmt.Errorf("the BNK namespaces are CREATE-time settings and they have changed.\n\n"+
		"  Installed as:  flo_namespace=%s  flo_utils_namespace=%s\n"+
		"  Now asking for: flo_namespace=%s  flo_utils_namespace=%s\n\n"+
		"  Nothing moves an installed component between namespaces — terraform would\n"+
		"  create the new namespace and delete the old one, taking the components with\n"+
		"  it. Either restore the recorded values, or install into a new workspace",
		priorFLO, priorUtils, flo, utils)
}

// tfvarString unquotes a value read from a tfvars snapshot. The parser keeps
// values verbatim, so a string arrives with its quotes still on.
func tfvarString(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	return strings.TrimSpace(v)
}
