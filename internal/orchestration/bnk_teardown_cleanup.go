package orchestration

import (
	"context"
	"fmt"
	"io"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"
	"strings"
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

// freeStuckBNKNamespace clears F5 finalizers left on a BNK namespace that is
// stuck Terminating, and reports whether it freed anything.
//
// On 2.4 the CNEInstance deletion times out, the operator that owns the
// finalizer is removed anyway, and from then on nothing exists that could ever
// clear it. The namespace sits in Terminating forever, and every later `bnk up`
// fails on every object with
//
//	forbidden: unable to create new content in namespace f5-bnk because it is
//	being terminated
//
// so the cluster is permanently unusable for a reinstall. Seen twice on live
// 2.4 clusters.
//
// WHERE THIS RUNS, and why that is the whole design. A first version ran it
// only after a SUCCESSFUL destroy, which made it unreachable in its own
// scenario: both BNK namespaces are terraform-managed, the kubernetes provider
// blocks on namespace deletion, so a namespace stuck Terminating is exactly
// what makes the destroy FAIL. It now runs on the failure path and the destroy
// is retried, which is the only ordering where it can help.
//
// It is a REPAIR, not a substitute for the ordering fix that would stop the
// CNEInstance being orphaned. But an operator that is already gone cannot be
// brought back, so something has to clear what it left.
//
// Scoped: only this workspace's cluster resolved by id from its own state, only
// namespaces already Terminating, only finalizers in F5's own API groups, and
// it verifies the namespace actually left Terminating before claiming success —
// a repair that reports a fix it did not make is worse than no repair.
func freeStuckBNKNamespace(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) bool {
	if cctx == nil || cctx.Workspace == nil {
		return false
	}
	body, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		return false
	}
	kc, err := k8s.NewFromKubeconfigBytes(body)
	if err != nil {
		return false
	}
	flo, utils := cctx.Workspace.BNKNamespaces()

	tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	freed := false
	seen := map[string]bool{}
	for _, ns := range []string{flo, utils} {
		if ns == "" || seen[ns] {
			continue
		}
		seen[ns] = true

		n, gerr := kc.Clientset().CoreV1().Namespaces().Get(tctx, ns, metav1.GetOptions{})
		if gerr != nil || n.Status.Phase != corev1.NamespaceTerminating {
			continue
		}
		cleared := freeF5Finalizers(tctx, body, ns, w)
		if cleared == 0 {
			continue
		}

		// Did it actually work? The namespace disappears within seconds once the
		// last finalizer goes; if it does not, say so rather than claim a fix.
		gone := false
		for i := 0; i < 20; i++ {
			if _, e := kc.Clientset().CoreV1().Namespaces().Get(tctx, ns, metav1.GetOptions{}); e != nil {
				gone = true
				break
			}
			select {
			case <-tctx.Done():
				i = 20
			case <-time.After(3 * time.Second):
			}
		}
		if gone {
			freed = true
			fmt.Fprintf(w, "  ⚠ namespace %q was stuck Terminating; cleared F5 finalizers on %d object(s) and it drained.\n", ns, cleared)
			fmt.Fprintf(w, "    Their operator was removed before they finished finalizing, so nothing was left that\n")
			fmt.Fprintf(w, "    could clear them. Without this every later `bnk up` fails with \"namespace is being\n")
			fmt.Fprintf(w, "    terminated\".\n")
		} else {
			fmt.Fprintf(w, "  ⚠ namespace %q is stuck Terminating; cleared F5 finalizers on %d object(s) but it has NOT\n", ns, cleared)
			fmt.Fprintf(w, "    drained. Something else is holding it. Inspect:\n")
			fmt.Fprintf(w, "      kubectl get ns %s -o jsonpath='{.status.conditions}'\n", ns)
		}
	}
	return freed
}

// freeF5Finalizers strips F5-group finalizers from every namespaced object in
// F5's API groups, and reports how many objects it changed.
//
// The kinds are DISCOVERED, not listed. A hardcoded list had three entries; the
// live 2.4 capture shows sixteen finalizer-bearing F5 CRs across the two
// namespaces (Afm, CNEController, Downloader, ExternalBigIPController, F5Tmm,
// Coremond, CRDConversion, CRDInstaller, CSRC, Cwc, Fluentd, IPAMController,
// Observer, OtelCollector, Rabbitmq, plus the CNEInstance). Listing them by hand
// meant the repair cleared three and left thirteen, then announced success.
//
// Only F5's own finalizers are removed. The object's other finalizers are
// written back, because something else's finalizer is something else's business
// — a Velero or pvc-protection entry dropped here would leak whatever it was
// protecting.
func freeF5Finalizers(ctx context.Context, kubeconfig []byte, ns string, w io.Writer) int {
	dc, err := k8s.DynamicFromKubeconfigBytes(kubeconfig)
	if err != nil {
		return 0
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return 0
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return 0
	}

	groups := map[string]bool{}
	for _, g := range BNKCRDGroups {
		groups[g] = true
	}

	lists, err := disc.ServerPreferredNamespacedResources()
	// Partial discovery failure is NORMAL here: CRDs are being deleted while we
	// look. Use whatever resolved rather than giving up on all of it.
	if lists == nil && err != nil {
		return 0
	}

	cleared := 0
	for _, rl := range lists {
		gv, perr := schema.ParseGroupVersion(rl.GroupVersion)
		if perr != nil || !groups[gv.Group] {
			continue
		}
		for _, r := range rl.APIResources {
			if !strings.Contains(strings.Join(r.Verbs, ","), "list") {
				continue
			}
			gvr := gv.WithResource(r.Name)
			list, lerr := dc.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
			if lerr != nil || list == nil {
				continue
			}
			for i := range list.Items {
				item := list.Items[i]
				keep, removed := retainNonF5Finalizers(item.GetFinalizers())
				if removed == 0 {
					continue
				}
				item.SetFinalizers(keep)
				if _, uerr := dc.Resource(gvr).Namespace(ns).Update(ctx, &item, metav1.UpdateOptions{}); uerr == nil {
					cleared++
				} else if w != nil {
					fmt.Fprintf(w, "    could not clear finalizers on %s/%s: %v\n", r.Name, item.GetName(), uerr)
				}
			}
		}
	}
	return cleared
}

// retainNonF5Finalizers splits a finalizer list into the entries that should
// stay and a count of the F5 ones removed.
//
// A finalizer is F5's if its domain part is one of BNKCRDGroups or a subdomain
// of f5.com / f5net.com. Everything else is someone else's and is preserved:
// the first version of this wrote `finalizers: null`, which dropped every
// finalizer on the object while its own comment promised it only touched F5's.
func retainNonF5Finalizers(in []string) (keep []string, removed int) {
	for _, f := range in {
		domain := f
		if i := strings.Index(f, "/"); i >= 0 {
			domain = f[:i]
		}
		isF5 := domain == "f5.com" || domain == "f5net.com" ||
			strings.HasSuffix(domain, ".f5.com") || strings.HasSuffix(domain, ".f5net.com")
		if !isF5 {
			for _, g := range BNKCRDGroups {
				if domain == g {
					isF5 = true
					break
				}
			}
		}
		if isF5 {
			removed++
			continue
		}
		keep = append(keep, f)
	}
	return keep, removed
}
