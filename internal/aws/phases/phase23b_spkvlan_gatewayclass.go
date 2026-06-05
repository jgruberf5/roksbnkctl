package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

const (
	f5spkvlanCRDName = "f5-spk-vlans.k8s.f5net.com"
	// Why: CRD applies are sub-second; 3 min is generous. See docs/audits/slice-12-cold-start-audit.md §4.
	f5spkvlanCRDWait     = 3 * time.Minute
	f5spkvlanYAMLPath    = "host-device/f5spkvlan.yaml.tmpl"
	gatewayClassYAMLPath = "host-device/gatewayclass.yaml.tmpl"

	gatewayClassCRDName = "gatewayclasses.gateway.networking.k8s.io"
	// Why: installed by the same FLO crd-installer Job as the F5SPKVlan CRD; 3 min is generous.
	gatewayClassCRDWait = 3 * time.Minute
)

// f5spkvlanGVR is the GVR for the F5SPKVlan CR (used by Phase23bDown to delete).
var f5spkvlanGVR = schema.GroupVersionResource{
	Group:    "k8s.f5net.com",
	Version:  "v1",
	Resource: "f5-spk-vlans",
}

// gatewayClassGVR is the GVR for the cluster-scoped GatewayClass CR.
var gatewayClassGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gatewayclasses",
}

// Phase23bSPKVlanGatewayClass applies the F5SPKVlan + GatewayClass CRs that
// complete TMM data-plane plumbing (host-device pattern).
//
//   - F5SPKVlan binds TMM trunks 1.1 (external) / 1.2 (internal) to named
//     virtual interfaces inside the TMM pod netns, announcing the SelfIPs
//     that Phase 17 assigned as secondary IPs on the AWS ENIs.
//   - GatewayClass registers the BNK cne-controller as a Gateway API
//     implementation so operator-facing Gateway CRs can target it via
//     spec.gatewayClassName.
//
// Runs AFTER Phase 23 (License) and BEFORE Phase 24 (CWC heal). Both the
// F5SPKVlan and GatewayClass CRDs are installed by FLO once the CNEInstance
// reaches Reconciled, so we wait for BOTH CRDs before applying either CR
// (FLO can take 10+ min on a cold cluster).
//
// The int-vlan is applied only for dual-interface; single-interface patterns
// (external-only) get just ext-vlan + GatewayClass.
//
// Skipped when the cluster is not a BNK pattern.
// SSO sentinel: CheckAuthOrDie at entry.
func Phase23bSPKVlanGatewayClass(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	if !cl.IsBNKPattern() {
		fmt.Fprintf(os.Stderr, "[phase 23b] skipped: pattern=%q (BNK patterns only)\n", cl.Pattern)
		return nil
	}
	hasInternal := cl.HasInternalInterface()
	fmt.Fprintf(os.Stderr, "[phase 23b] F5SPKVlan + GatewayClass: cluster=%s\n", name)

	if dryRun {
		if hasInternal {
			fmt.Fprintln(os.Stderr, "[phase 23b] dry-run: would wait for F5SPKVlan CRD then apply ext-vlan + int-vlan + GatewayClass")
		} else {
			fmt.Fprintln(os.Stderr, "[phase 23b] dry-run: would wait for F5SPKVlan CRD then apply ext-vlan + GatewayClass (single-interface)")
		}
		st.Set("F5SPKVLAN_APPLIED_AT", "dry-run")
		st.Set("GATEWAYCLASS_NAME", name+"-gatewayclass")
		return nil
	}

	if clients.Dynamic == nil {
		return fmt.Errorf("phase23b: Clients.Dynamic is nil — call clients.AttachK8s(kubeconfigPath) first")
	}
	if cl.Network.DataPath == nil || cl.Network.DataPath.SelfIPs == nil {
		return fmt.Errorf("phase23b: cl.Network.DataPath.SelfIPs not set (intent.applyDefaults should derive from CIDRs)")
	}
	selfIPs := cl.Network.DataPath.SelfIPs
	if selfIPs.External == "" {
		return fmt.Errorf("phase23b: external SelfIP not derivable — DataPath external subnet must be /24 (see DeriveSelfIP)")
	}
	if hasInternal && selfIPs.Internal == "" {
		return fmt.Errorf("phase23b: internal SelfIP not derivable — DataPath internal subnet must be /24 (see DeriveSelfIP)")
	}

	// Wait for the F5SPKVlan CRD (installed by FLO after CNEInstance reconciles).
	fmt.Fprintf(os.Stderr, "[phase 23b] waiting for CRD %s (up to %s)\n", f5spkvlanCRDName, f5spkvlanCRDWait)
	if err := k8swait.WaitForCRDExists(ctx, clients.Dynamic, f5spkvlanCRDName, f5spkvlanCRDWait); err != nil {
		return fmt.Errorf("phase23b: waiting for F5SPKVlan CRD: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 23b] CRD %s ready\n", f5spkvlanCRDName)

	// Wait for the GatewayClass CRD too (installed by the same FLO crd-installer
	// Job). We wait for BOTH CRDs before applying EITHER CR: the GatewayClass
	// apply otherwise races the RESTMapper cache when the CRD landed <2s earlier
	// (Cycle-2 Finding #1, docs/audits/2026-05-24-h4-rev-live-cycle). The apply
	// path's reset-and-retry (applyUnstructured, C-6) re-queries discovery on the
	// next call once the CRD is present.
	fmt.Fprintf(os.Stderr, "[phase 23b] waiting for CRD %s (up to %s)\n", gatewayClassCRDName, gatewayClassCRDWait)
	if err := k8swait.WaitForCRDExists(ctx, clients.Dynamic, gatewayClassCRDName, gatewayClassCRDWait); err != nil {
		return fmt.Errorf("phase23b: waiting for GatewayClass CRD: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 23b] CRD %s ready\n", gatewayClassCRDName)

	// Render + apply F5SPKVlan.
	spkTmpl, err := k8smanifests.FS.ReadFile(f5spkvlanYAMLPath)
	if err != nil {
		return fmt.Errorf("phase23b: reading f5spkvlan template: %w", err)
	}
	spkRendered, err := render.RenderF5SPKVlan(spkTmpl, selfIPs.External, selfIPs.Internal, selfIPs.PrefixLen, hasInternal)
	if err != nil {
		return fmt.Errorf("phase23b: rendering f5spkvlan: %w", err)
	}
	if hasInternal {
		fmt.Fprintf(os.Stderr, "[phase 23b] applying F5SPKVlan ext-vlan (selfip=%s) + int-vlan (selfip=%s)\n",
			selfIPs.External, selfIPs.Internal)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 23b] applying F5SPKVlan ext-vlan (selfip=%s)\n", selfIPs.External)
	}
	if err := applyRawYAML(ctx, clients, spkRendered); err != nil {
		return fmt.Errorf("phase23b: applying F5SPKVlan: %w", err)
	}
	st.Set("F5SPKVLAN_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))

	// Render + apply GatewayClass.
	gwcTmpl, err := k8smanifests.FS.ReadFile(gatewayClassYAMLPath)
	if err != nil {
		return fmt.Errorf("phase23b: reading gatewayclass template: %w", err)
	}
	gwcRendered, err := render.RenderGatewayClass(gwcTmpl, cl)
	if err != nil {
		return fmt.Errorf("phase23b: rendering gatewayclass: %w", err)
	}
	gwcName := name + "-gatewayclass"
	fmt.Fprintf(os.Stderr, "[phase 23b] applying GatewayClass %s\n", gwcName)
	if err := applyRawYAML(ctx, clients, gwcRendered); err != nil {
		return fmt.Errorf("phase23b: applying GatewayClass: %w", err)
	}
	st.Set("GATEWAYCLASS_NAME", gwcName)

	return st.Save()
}

// Phase23bSPKVlanGatewayClassDown deletes the F5SPKVlan CRs (ext-vlan + int-vlan)
// and the cluster-scoped GatewayClass. Tolerates NotFound everywhere.
// Skipped silently when the cluster is not a BNK pattern. Deleting a
// non-existent int-vlan (single-interface clusters) is tolerated via NotFound.
func Phase23bSPKVlanGatewayClassDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	if !cl.IsBNKPattern() {
		return nil
	}
	fmt.Fprintf(os.Stderr, "[phase 23b down] F5SPKVlan + GatewayClass: cluster=%s\n", cl.Metadata.Name)

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 23b down] warning: dynamic client unavailable, skipping CR deletes")
		clearPhase23bState(st)
		return st.Save()
	}

	// Delete F5SPKVlan CRs.
	for _, vlan := range []string{"ext-vlan", "int-vlan"} {
		err := clients.Dynamic.Resource(f5spkvlanGVR).Namespace(InstanceNamespace).Delete(ctx, vlan, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 23b down] warning: delete F5SPKVlan %s: %v\n", vlan, err)
		} else if err == nil {
			fmt.Fprintf(os.Stderr, "[phase 23b down] deleted F5SPKVlan %s\n", vlan)
		}
	}

	// Delete GatewayClass (cluster-scoped).
	gwcName := cl.Metadata.Name + "-gatewayclass"
	err := clients.Dynamic.Resource(gatewayClassGVR).Delete(ctx, gwcName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 23b down] warning: delete GatewayClass %s: %v\n", gwcName, err)
	} else if err == nil {
		fmt.Fprintf(os.Stderr, "[phase 23b down] deleted GatewayClass %s\n", gwcName)
	}

	clearPhase23bState(st)
	return st.Save()
}

func clearPhase23bState(st *state.State) {
	for _, k := range []string{"F5SPKVLAN_APPLIED_AT", "GATEWAYCLASS_NAME"} {
		st.Set(k, "")
	}
}
