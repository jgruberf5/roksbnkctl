package orchestration

import (
	"context"
	"fmt"
	"io"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// BNK TEARDOWN LEAVES STATE THAT BREAKS THE *NEXT* INSTALL (#172).
//
// Three kinds of leftover, all of which fail the reinstall rather than the
// teardown — the failure mode hardest to attribute, because the run that
// created the problem reported success.

// LicenseSecretNames are the CWC secrets that survive a teardown.
//
// Re-licensing, or reinstalling on a reused cluster, requires deleting them from
// the CWC namespace and restarting CWC; the licensing flow reads them, finds a
// previous activation, and does not re-activate. 28 of these were present on the
// live cluster after a teardown.
//
// The list is the guide's, verbatim. It is spelled out rather than matched by
// prefix because these sit in a namespace with unrelated secrets — a prefix
// sweep would be a wildcard delete against someone else's data, and the blast
// radius of getting that wrong is worse than the inconvenience of a list.
var LicenseSecretNames = []string{
	"activationcontext",
	"activationmessage",
	"activationstatus",
	"certificate",
	"certificatechain",
	"configreport",
	"configreportsignedackresponse",
	"context",
	"cpcl-config-secret",
	"cpcl-key-secret",
	"csr",
	"customerprovidedid",
	"cwcstate",
	"cwcswitchstate",
	"digitalassetid",
	"digitalassetname",
	"digitalassetversion",
	"entitlements",
	"initialregistrationstatus",
	"jwtsecret",
	"licensekey",
	"licensestatus",
	"modeofoperation",
	"previousreportname",
	"previousreportverifieddate",
	"privatekey",
	"productname",
	"publickey",
	"statehistory",
	"switchlicensestatus",
	"telemetryreport",
	"telemetryreports",
	"telemetryreportsignedackresponse",
	"telemetrystatus",
}

// BNKCRDGroups are the API groups whose CRDs a full uninstall removes.
//
// TWO CORRECTIONS TO THE GUIDE'S OWN CLEANUP BLOCK, both of which leave objects
// behind if copied as written:
//
//   - it greps `metrics.f5.net`, but the group on the cluster is
//     `metrics.f5.com`, so that line matches nothing;
//   - it never mentions `gateway.k8s.f5.com`, so 2.4's six new gateway CRDs are
//     left behind entirely.
//
// Full inventory:
// /mnt/d/roksbnkctl-gap-2-3-to-2-4/archived-staging-small-8c-20g/crd-inventory.md
var BNKCRDGroups = []string{
	"k8s.f5.com",         // CNEInstance and friends, both lines
	"k8s.f5net.com",      // the 2.3 F5SPK* family
	"gateway.k8s.f5.com", // 2.4: Infra, GatewaySettings, EgressGateway, ... (6 kinds)
	"fic.f5.com",         // 2.4: IPAM, IPAMRange — controller-generated (2 kinds)
	"metrics.f5.com",     // 2.4: telemetry (3 kinds). NOT metrics.f5.net.
}

// UninstallOrder is the sequence the 2.4 guide requires, and the reason for each
// step's position.
//
// The IPAM checkpoint is the one that is easy to skip and expensive to skip: the
// guide is explicit that IPAM/IPAMRange must be CONFIRMED gone before the
// CNEInstance is removed, "to avoid any leftover state that might cause issues
// during product reinstallation". They are controller-generated, so removing the
// CNEInstance first takes away the thing that would have cleaned them up.
var UninstallOrder = []string{
	"use-case CRs (Gateway, HTTPRoute/GRPCRoute/L4Route, EgressGateway)",
	"GatewaySettings",
	"Infra",
	"License CR + the CWC license secrets",
	"VERIFY IPAM and IPAMRange are gone before continuing",
	"CNEInstance",
	"helm uninstall flo",
	"namespaces",
	"CRDs",
}

// GTMNamingChangedBetweenLines records why a 2.3 -> 2.4 move needs manual
// cleanup on the external BIG-IP, and why CheckLineChange refuses one.
//
//	2.3: server_<tmm_selfip>
//	2.4: server_<digitalassetid>_<cluster-name>_<ns>_<ip>
//
// The objects live outside the cluster and outside terraform state, so nothing
// here deletes them, and the guide warns that leaving both formats in place
// causes device IP conflicts. The same applies to changing CLUSTER_IDENTIFIER on
// a deployed workspace.
const GTMNamingChangedBetweenLines = true

// sweepLicenseSecrets deletes the CWC licence secrets from the shared-components
// namespace after a successful BNK destroy.
//
// Namespace-scoped and name-exact: it deletes only the names in
// LicenseSecretNames, in the namespace BNK actually used. It never touches CRDs
// — those are cluster-scoped, shared with any other BNK install on the cluster,
// and removing them is a decision an operator makes explicitly rather than a
// side effect of one workspace's teardown. BNKCRDGroups documents the list for
// when they do.
//
// Silent on every failure. A cluster already gone, a kubeconfig that no longer
// resolves, or an absent secret are all NORMAL after a destroy; reporting them
// would turn a clean teardown into a wall of noise about things that are
// correctly absent.
// freeStuckBNKNamespace clears F5 finalizers left on a BNK namespace that is
// stuck Terminating after a destroy.
//
// The 2.4 teardown can leave the namespace unfinalizable. The CNEInstance
// deletion times out ("context deadline exceeded"), the operator that owns the
// finalizer is removed anyway, and from then on nothing exists that could ever
// clear it. The namespace sits in Terminating forever, and the NEXT `bnk up`
// fails on every object with
//
//	forbidden: unable to create new content in namespace f5-bnk because it is
//	being terminated
//
// which is exactly what the lifecycle demos' "swap BNK, keep the cluster" phase
// does. Observed on a live 2.4 cluster, twice.
//
// This is deliberately a REPAIR and not a workaround for an ordering bug we
// should also fix: the right prevention is for the CNEInstance to finish
// finalizing before its operator goes. But an operator that is already gone
// cannot be brought back, so something has to clear what it left, or the
// cluster is permanently unusable for a reinstall.
//
// Tightly scoped on purpose:
//   - only after a SUCCESSFUL destroy, so a failed teardown is never "tidied"
//   - only this workspace's cluster, resolved by id from its own state
//   - only namespaces already in Terminating — never a live one
//   - only finalizers in F5's own API groups, left on F5's own kinds
//   - it SAYS what it did; a silent repair teaches nobody that the bug exists
func freeStuckBNKNamespace(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) {
	if cctx == nil || cctx.Workspace == nil {
		return
	}
	body, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		return
	}
	kc, err := k8s.NewFromKubeconfigBytes(body)
	if err != nil {
		return
	}
	flo, utils := cctx.Workspace.BNKNamespaces()

	tctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	for _, ns := range []string{flo, utils} {
		if ns == "" {
			continue
		}
		n, err := kc.Clientset().CoreV1().Namespaces().Get(tctx, ns, metav1.GetOptions{})
		if err != nil || n.Status.Phase != corev1.NamespaceTerminating {
			continue
		}
		cleared := freeF5Finalizers(tctx, body, ns)
		if cleared == 0 {
			continue
		}
		fmt.Fprintf(w, "  ⚠ namespace %q was stuck Terminating; cleared F5 finalizers on %d object(s).\n", ns, cleared)
		fmt.Fprintf(w, "    Their operator was removed before they finished finalizing, so nothing was left that could\n")
		fmt.Fprintf(w, "    clear them. Without this the next `bnk up` fails on every object with \"namespace is being\n")
		fmt.Fprintf(w, "    terminated\".\n")
	}
}

// freeF5Finalizers removes finalizers from the F5-owned kinds known to hold a
// BNK namespace open, and reports how many objects it touched. Errors are
// swallowed per object: a kind whose CRD is already gone is the normal case
// after a destroy, not a problem to report.
func freeF5Finalizers(ctx context.Context, kubeconfig []byte, ns string) int {
	dc, err := k8s.DynamicFromKubeconfigBytes(kubeconfig)
	if err != nil {
		return 0
	}
	cleared := 0
	for _, gvr := range stuckNamespaceGVRs {
		list, lerr := dc.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if lerr != nil || list == nil {
			continue
		}
		for i := range list.Items {
			item := list.Items[i]
			if len(item.GetFinalizers()) == 0 {
				continue
			}
			patch := []byte(`{"metadata":{"finalizers":null}}`)
			if _, perr := dc.Resource(gvr).Namespace(ns).Patch(
				ctx, item.GetName(), types.MergePatchType, patch, metav1.PatchOptions{},
			); perr == nil {
				cleared++
			}
		}
	}
	return cleared
}

// The kinds observed holding a 2.4 BNK namespace open. Each was seen with a
// real finalizer on a live cluster: CNEInstanceFinalizer on the CNEInstance,
// k8s.f5net.com/uninstall on the dssm, and handletmmconfig_inconsistency on the
// F5SPKVlans (which 2.4 still creates even though its controller ignores them).
var stuckNamespaceGVRs = []schema.GroupVersionResource{
	{Group: "k8s.f5.com", Version: "v1", Resource: "cneinstances"},
	{Group: "k8s.f5.com", Version: "v1", Resource: "dssms"},
	{Group: "k8s.f5net.com", Version: "v1", Resource: "f5-spk-vlans"},
}

func sweepLicenseSecrets(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) {
	if cctx == nil || cctx.Workspace == nil {
		return
	}
	// THIS WORKSPACE'S cluster, resolved by id from its own state — not the
	// ambient kubeconfig. The sweep issues unconditional deletes of 34 named
	// secrets, and ~/.kube/config points wherever its current context was last
	// pointed, which is not workspace-scoped and may well be a peer's live
	// cluster. Tearing down workspace A must never delete workspace B's
	// licence secrets and leave it unable to re-license.
	body, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		return
	}
	kc, err := k8s.NewFromKubeconfigBytes(body)
	if err != nil {
		return
	}
	// The shared components — CWC among them — live in the utils namespace, which
	// is the FLO namespace on a one-namespace install (#66).
	_, ns := cctx.Workspace.BNKNamespaces()
	if ns == "" {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	deleted := 0
	for _, name := range LicenseSecretNames {
		if err := kc.Clientset().CoreV1().Secrets(ns).Delete(tctx, name, metav1.DeleteOptions{}); err == nil {
			deleted++
		}
	}
	if deleted > 0 {
		fmt.Fprintf(w, "✓ removed %d CWC licence secret(s) from %s so the next install can license\n", deleted, ns)
	}
}
