package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

const (
	sriovNodePrepYAMLPath = "sriov-external/vfio-node-prep.yaml.tmpl"
	sriovDPYAMLPath       = "sriov-external/sriovdp.yaml.tmpl"
	sriovNADYAMLPath      = "sriov-external/network-attachment-defs.yaml.tmpl"
	// SriovExternalNAD is the passthru NAD name TMM's CNEInstance references.
	SriovExternalNAD = "external-sriov"
	// sriovResourceName is the device-plugin resource exposing the vfio ENA.
	sriovResourceName = "intel.com/ens8"
	sriovResourceWait = 100 * time.Second
)

// Phase20bSriovDataplane sets up the SR-IOV/vfio-pci DPDK dataplane for the
// sriov-external pattern. Proven live (docs/spikes/sriov-ena-vfio/README.md):
//
//  1. vfio-node-prep DaemonSet binds the external ENA to vfio-pci (No-IOMMU).
//  2. sriov-network-device-plugin exposes it as the intel.com/ens8 resource.
//  3. the external-sriov NAD (type: passthru — NOT sriov-cni) couples the
//     resource to TMM's networkAttachments.
//
// The device plugin must scan AFTER the vfio bind (it registers 0 devices
// otherwise), so this phase applies node-prep, then the plugin, then deletes the
// plugin pod to force a fresh scan, then waits for the resource to advertise.
//
// Skipped unless DataplaneBinding()=="sriov". The host-device NAD (phase 20) is
// skipped for sriov; this phase applies the sriov NAD instead.
func Phase20bSriovDataplane(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	if cl.DataplaneBinding() != "sriov" {
		return nil
	}
	pci := orStr(st.Get("EXTERNAL_PCI"), ExternalPCI)
	fmt.Fprintf(os.Stderr, "[phase 20b] sriov dataplane: cluster=%s ena-pci=%s\n", cl.Metadata.Name, pci)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 20b] dry-run: would bind ENA %s to vfio-pci (node-prep), deploy sriov-device-plugin (resource %s), apply %s NAD (type passthru)\n",
			pci, sriovResourceName, SriovExternalNAD)
		st.Set("SRIOV_DATAPLANE_APPLIED_AT", "dry-run")
		return nil
	}

	if clients.Dynamic == nil || clients.K8s == nil {
		return fmt.Errorf("phase20b: k8s clients nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// 1. node-prep: bind the ENA to vfio-pci on the BNK node.
	if err := applySriovTmpl(ctx, clients, sriovNodePrepYAMLPath, pci, InstanceNamespace); err != nil {
		return fmt.Errorf("phase20b: node-prep: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 20b] vfio-node-prep applied; waiting for vfio bind before device-plugin scan")
	time.Sleep(20 * time.Second)

	// 2. sriov-network-device-plugin.
	if err := applySriovTmpl(ctx, clients, sriovDPYAMLPath, pci, InstanceNamespace); err != nil {
		return fmt.Errorf("phase20b: device-plugin: %w", err)
	}
	// Force a fresh scan now the ENA is on vfio-pci (plugin registers 0 devices
	// if it scanned while the ENA was still on the ena driver).
	_ = clients.K8s.CoreV1().Pods("kube-system").DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: "app=sriovdp"})

	// 3. sriov passthru NAD in f5-cne-system + default (mirrors host-device NADs).
	for _, ns := range nadNamespaces {
		if err := applySriovTmpl(ctx, clients, sriovNADYAMLPath, pci, ns); err != nil {
			return fmt.Errorf("phase20b: sriov NAD in %s: %w", ns, err)
		}
	}

	// 4. Wait for the device-plugin to advertise the resource on the BNK node.
	node := st.Get("TMM_NODE_NAME")
	if node != "" {
		if err := waitSriovResource(ctx, clients, node, sriovResourceWait); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 20b] warning: %v (CNEInstance phase will surface a clear error if the resource never appears)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 20b] resource %s advertised on %s\n", sriovResourceName, node)
		}
	}

	st.Set("SRIOV_DATAPLANE_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))
	return st.Save()
}

// applySriovTmpl reads, renders and applies one sriov-external template.
func applySriovTmpl(ctx context.Context, clients *Clients, path, pci, ns string) error {
	raw, err := k8smanifests.FS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	rendered, err := render.RenderSriov(raw, pci, ns, SriovExternalNAD)
	if err != nil {
		return fmt.Errorf("rendering %s: %w", path, err)
	}
	return applyRawYAML(ctx, clients, rendered)
}

// waitSriovResource polls the node's allocatable until intel.com/ens8 is >0.
func waitSriovResource(ctx context.Context, clients *Clients, node string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n, err := clients.K8s.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
		if err == nil {
			if q, ok := n.Status.Allocatable[sriovResourceName]; ok && !q.IsZero() {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(6 * time.Second):
		}
	}
	return fmt.Errorf("device-plugin did not advertise %s on %s within %s", sriovResourceName, node, timeout)
}

// orStr returns v if non-empty, else def.
func orStr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
