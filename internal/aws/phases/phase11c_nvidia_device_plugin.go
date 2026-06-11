package phases

import (
	"context"
	"fmt"
	"os"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

const (
	// nvidiaDevicePluginVersion is the pinned NVIDIA k8s-device-plugin version.
	// Image: nvcr.io/nvidia/k8s-device-plugin:v0.17.1
	// Tag confirmed resolvable: https://catalog.ngc.nvidia.com/orgs/nvidia/containers/k8s-device-plugin/tags
	// (v0.17.1 released 2024-09-05, supports CUDA 12 / recent AL2023 NVIDIA AMI drivers)
	nvidiaDevicePluginVersion  = "v0.17.1"
	nvidiaDevicePluginYAMLPath = "nvidia-device-plugin/nvidia-device-plugin-v0.17.1.yaml.tmpl"
	nvidiaDevicePluginDSName   = "nvidia-device-plugin-daemonset"
	nvidiaDevicePluginNS       = "kube-system"
)

// Phase11cNvidiaDevicePlugin applies the NVIDIA k8s-device-plugin DaemonSet so
// that GPU nodes advertise the nvidia.com/gpu resource and GPU pods can be
// scheduled. Runs only when a GPU node group is declared (HasGPUNodeGroup).
//
// The DaemonSet is targeted via nodeSelector (awsbnkctl.io/gpu=true) so it
// lands only on GPU nodes. The BNK TMM nodes are unaffected.
//
// Phase placement: stage 3, after tmm-node-label (k8s clients attached), before
// phase12 (BNK k8s foundation). Phase11c naming follows the Phase11b precedent.
// D-005: CheckAuthOrDie at entry.
func Phase11cNvidiaDevicePlugin(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	// Self-gate: skip cleanly when no GPU node group is declared (R3).
	if !cl.HasGPUNodeGroup() {
		return nil
	}
	fmt.Fprintf(os.Stderr, "[phase 11c] NVIDIA device-plugin %s: cluster=%s\n", nvidiaDevicePluginVersion, cl.Metadata.Name)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 11c] dry-run: would apply NVIDIA device-plugin DaemonSet %s (version %s) in namespace %s\n",
			nvidiaDevicePluginDSName, nvidiaDevicePluginVersion, nvidiaDevicePluginNS)
		st.Set("NVIDIA_DEVICE_PLUGIN_APPLIED_AT", "dry-run")
		return nil
	}

	if clients.Dynamic == nil || clients.K8s == nil {
		return fmt.Errorf("phase11c: k8s clients nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// Read the embedded template.
	tmpl, err := k8smanifests.FS.ReadFile(nvidiaDevicePluginYAMLPath)
	if err != nil {
		return fmt.Errorf("phase11c: reading embedded NVIDIA device-plugin template: %w", err)
	}

	// Render: substitute image tag.
	rendered, err := render.RenderNvidiaDevicePlugin(tmpl, nvidiaDevicePluginVersion)
	if err != nil {
		return fmt.Errorf("phase11c: rendering NVIDIA device-plugin template: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[phase 11c] applying NVIDIA device-plugin DaemonSet (%d bytes)\n", len(rendered))
	if err := applyRawYAML(ctx, clients, rendered); err != nil {
		return fmt.Errorf("phase11c: applying NVIDIA device-plugin DaemonSet: %w", err)
	}

	st.Set("NVIDIA_DEVICE_PLUGIN_APPLIED_AT", nvidiaDevicePluginVersion)
	fmt.Fprintf(os.Stderr, "[phase 11c] NVIDIA device-plugin DaemonSet applied (version %s)\n", nvidiaDevicePluginVersion)
	return nil
}

// Phase11cNvidiaDevicePluginDown deletes the NVIDIA device-plugin DaemonSet.
// Tolerates not-found (idempotent). Self-gates on HasGPUNodeGroup().
// Called in the down sequence while kubeconfig is still valid (stage 3,
// before kubeconfig-down), so k8s clients are available.
func Phase11cNvidiaDevicePluginDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	// Self-gate: skip when no GPU node group (R3).
	if !cl.HasGPUNodeGroup() {
		return nil
	}
	fmt.Fprintf(os.Stderr, "[phase 11c down] NVIDIA device-plugin: cluster=%s\n", cl.Metadata.Name)

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 11c down] warning: k8s client not available, skipping k8s teardown")
		clearPhase11cState(st)
		return st.Save()
	}

	// Delete the DaemonSet — tolerate not-found.
	err := clients.K8s.AppsV1().DaemonSets(nvidiaDevicePluginNS).Delete(ctx, nvidiaDevicePluginDSName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 11c down] warning: delete nvidia-device-plugin DaemonSet: %v\n", err)
	} else if k8serrors.IsNotFound(err) {
		fmt.Fprintln(os.Stderr, "[phase 11c down] nvidia-device-plugin DaemonSet already gone")
	} else {
		fmt.Fprintln(os.Stderr, "[phase 11c down] nvidia-device-plugin DaemonSet deleted")
	}

	clearPhase11cState(st)
	return st.Save()
}

func clearPhase11cState(st *state.State) {
	st.Set("NVIDIA_DEVICE_PLUGIN_APPLIED_AT", "")
}
