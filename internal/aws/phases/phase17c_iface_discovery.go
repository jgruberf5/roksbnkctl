package phases

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

const (
	ifaceDiscoveryYAMLPath = "host-device/iface-discovery-pod.yaml.tmpl"
	ifaceDiscoveryPodName  = "iface-discovery"
	ifaceDiscoveryPodNS    = "kube-system"
	ifaceDiscoveryTimeout  = 3 * time.Minute
)

// ifaceInfo holds the discovered Linux interface name and PCI bus address for
// one network interface, as emitted by the probe pod.
type ifaceInfo struct {
	Ifname string `json:"ifname"`
	Pci    string `json:"pci"`
}

// Phase17cIfaceDiscovery runs a privileged alpine pod on the TMM node to
// discover the Linux ifname + PCI bus address for the internal and external
// data-path ENIs by MAC matching. Writes EXTERNAL_IFNAME, INTERNAL_IFNAME,
// EXTERNAL_PCI, INTERNAL_PCI, CLOUD_HOST_DEVICE_NAME, IFACE_DISCOVERY_AT.
//
// HARD-FAILS on any error — there is no fallback to hardcoded constants for
// live runs. Dry-run sets constants from constants_hostdevice.go without any
// k8s or EC2 calls.
//
// Lifecycle order: Phase17 → Phase17b → Phase17c → Phase18 → ...
func Phase17cIfaceDiscovery(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintf(os.Stderr, "[phase 17c] iface-discovery: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 17c] dry-run: setting iface constants from architecture defaults")
		st.Set("EXTERNAL_IFNAME", ExternalIFName)
		st.Set("INTERNAL_IFNAME", InternalIFName)
		st.Set("EXTERNAL_PCI", ExternalPCI)
		st.Set("INTERNAL_PCI", InternalPCI)
		st.Set("CLOUD_HOST_DEVICE_NAME", ExternalIFName)
		st.Set("IFACE_DISCOVERY_AT", "dry-run")
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase17c: Clients.K8s is nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// Read prerequisites from state.
	intMAC := st.Get("INTERNAL_ENI_MAC")
	if intMAC == "" {
		return fmt.Errorf("phase17c: INTERNAL_ENI_MAC not in state (run phase17 first)")
	}
	extMAC := st.Get("EXTERNAL_ENI_MAC")
	if extMAC == "" {
		return fmt.Errorf("phase17c: EXTERNAL_ENI_MAC not in state (run phase17 first)")
	}
	nodeName := st.Get("TMM_NODE_NAME")
	if nodeName == "" {
		return fmt.Errorf("phase17c: TMM_NODE_NAME not in state (run phase16 first)")
	}

	// Best-effort delete any stale pod from a previous run.
	_ = clients.K8s.CoreV1().Pods(ifaceDiscoveryPodNS).Delete(ctx, ifaceDiscoveryPodName, metav1.DeleteOptions{})

	// Render and apply the discovery pod.
	tmplBytes, err := k8smanifests.FS.ReadFile(ifaceDiscoveryYAMLPath)
	if err != nil {
		return fmt.Errorf("phase17c: reading iface-discovery pod template: %w", err)
	}
	rendered, err := render.RenderIfaceDiscoveryPod(tmplBytes, ifaceDiscoveryPodNS, nodeName)
	if err != nil {
		return fmt.Errorf("phase17c: rendering iface-discovery pod: %w", err)
	}
	if err := applyRawYAML(ctx, clients, rendered); err != nil {
		return fmt.Errorf("phase17c: applying iface-discovery pod: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17c] waiting for iface-discovery pod (up to %s)\n", ifaceDiscoveryTimeout)

	// Wait for pod to complete.
	if err := k8swait.WaitForPodSucceeded(ctx, clients.K8s, ifaceDiscoveryPodNS, ifaceDiscoveryPodName, ifaceDiscoveryTimeout); err != nil {
		return fmt.Errorf("phase17c: iface-discovery pod did not succeed: %w", err)
	}

	// Collect pod logs — the probe emits a single JSON object to stdout.
	logBytes, err := podLogs(ctx, clients.K8s, ifaceDiscoveryPodNS, ifaceDiscoveryPodName)
	if err != nil {
		return fmt.Errorf("phase17c: reading iface-discovery pod logs: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17c] probe output: %s\n", strings.TrimSpace(string(logBytes)))

	// Parse MAC → ifaceInfo map.
	discovered := make(map[string]ifaceInfo)
	if err := json.Unmarshal(logBytes, &discovered); err != nil {
		return fmt.Errorf("phase17c: parsing probe JSON: %w\nraw output: %s", err, logBytes)
	}

	// Match MACs to discovered interfaces.
	extIf, extPCI, intIf, intPCI, err := matchInterfaces(discovered, extMAC, intMAC)
	if err != nil {
		return fmt.Errorf("phase17c: MAC matching: %w", err)
	}

	// Persist to state.
	st.Set("EXTERNAL_IFNAME", extIf)
	st.Set("INTERNAL_IFNAME", intIf)
	st.Set("EXTERNAL_PCI", extPCI)
	st.Set("INTERNAL_PCI", intPCI)
	st.Set("CLOUD_HOST_DEVICE_NAME", extIf)
	st.Set("IFACE_DISCOVERY_AT", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "[phase 17c] discovered: external=%s(%s) internal=%s(%s)\n",
		extIf, extPCI, intIf, intPCI)

	if err := st.Save(); err != nil {
		return fmt.Errorf("phase17c: saving state: %w", err)
	}

	// Best-effort delete the probe pod (cleanup).
	if dErr := clients.K8s.CoreV1().Pods(ifaceDiscoveryPodNS).Delete(ctx, ifaceDiscoveryPodName, metav1.DeleteOptions{}); dErr != nil && !k8serrors.IsNotFound(dErr) {
		fmt.Fprintf(os.Stderr, "[phase 17c] warning: delete probe pod: %v\n", dErr)
	}

	return nil
}

// Phase17cIfaceDiscoveryDown clears the iface-discovery state keys and
// best-effort deletes the probe pod if it still exists.
// Guards nil K8s client exactly like Phase11bEBSCSIHugepagesDown.
func Phase17cIfaceDiscoveryDown(ctx context.Context, _ *intent.Cluster, st *state.State, clients *Clients) error {
	fmt.Fprintln(os.Stderr, "[phase 17c down] iface-discovery: clearing state")

	if clients.K8s != nil {
		if dErr := clients.K8s.CoreV1().Pods(ifaceDiscoveryPodNS).Delete(ctx, ifaceDiscoveryPodName, metav1.DeleteOptions{}); dErr != nil && !k8serrors.IsNotFound(dErr) {
			fmt.Fprintf(os.Stderr, "[phase 17c down] warning: delete probe pod: %v\n", dErr)
		}
	}

	for _, key := range []string{
		"EXTERNAL_IFNAME",
		"INTERNAL_IFNAME",
		"EXTERNAL_PCI",
		"INTERNAL_PCI",
		"CLOUD_HOST_DEVICE_NAME",
		"IFACE_DISCOVERY_AT",
	} {
		st.Set(key, "")
	}
	return st.Save()
}

// matchInterfaces matches extMAC and intMAC (case-insensitive) against the
// discovered map[mac]ifaceInfo emitted by the probe pod. Returns the ifname
// and PCI bus address for each. Hard-fails (naming both MACs + dumping the map)
// if either MAC is not found.
func matchInterfaces(discovered map[string]ifaceInfo, extMAC, intMAC string) (extIf, extPCI, intIf, intPCI string, err error) {
	extKey := strings.ToLower(extMAC)
	intKey := strings.ToLower(intMAC)

	for mac, info := range discovered {
		lmac := strings.ToLower(mac)
		switch lmac {
		case extKey:
			extIf = info.Ifname
			extPCI = info.Pci
		case intKey:
			intIf = info.Ifname
			intPCI = info.Pci
		}
	}

	var missing []string
	if extIf == "" {
		missing = append(missing, fmt.Sprintf("external MAC %s", extMAC))
	}
	if intIf == "" {
		missing = append(missing, fmt.Sprintf("internal MAC %s", intMAC))
	}
	if len(missing) > 0 {
		return "", "", "", "", fmt.Errorf("MAC(s) not found on node: %s; discovered interfaces: %v",
			strings.Join(missing, ", "), discovered)
	}
	return extIf, extPCI, intIf, intPCI, nil
}

// podLogs retrieves the stdout logs from the named pod.
func podLogs(ctx context.Context, clientset kubernetes.Interface, ns, name string) ([]byte, error) {
	req := clientset.CoreV1().Pods(ns).GetLogs(name, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetLogs %s/%s: %w", ns, name, err)
	}
	defer stream.Close()
	return io.ReadAll(stream)
}
