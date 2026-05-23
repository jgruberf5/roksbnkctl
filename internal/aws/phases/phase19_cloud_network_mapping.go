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
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

const (
	cloudNetworkMappingYAMLPath = "shared/cloud-network-mapping.yaml.tmpl"
	cloudNetworkMappingName     = "cloud-network-mapping"
	phase19FieldManager         = "awsbnkctl-phase19"
)

// Phase19CloudNetworkMapping renders and applies the cloud-network-mapping
// ConfigMap into f5-cne-system. This CM is required by the cne-controller before
// the CNEInstance CR is applied (Pass 3).
//
// Idempotent: server-side-apply via applyUnstructured (FORCE=true).
// Reads MGMT_SUBNET, BNK_EXT_SUBNET, BNK_INT_SUBNET from state (Phase 03).
// Reads CNE_IRSA_ROLE_ARN from state (Phase 18) for cluster observability.
//
// D-005: CheckAuthOrDie called at entry.
func Phase19CloudNetworkMapping(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 19] cloud-network-mapping: cluster=%s\n", name)

	// Persist host-device constants to state for observability + Pass 3.
	persistHostDeviceConstants(st)

	// Also alias MGMT_SUBNET from PUBLIC_SUBNETS[0] if not already set.
	if err := ensureMGMTSubnetAlias(st); err != nil {
		return fmt.Errorf("phase19: %w", err)
	}

	if dryRun {
		fmt.Fprintf(os.Stderr,
			"[phase 19] dry-run: would apply cloud-network-mapping CM in %s\n", InstanceNamespace)
		st.Set("CLOUD_NETWORK_MAPPING_APPLIED_AT", "dry-run")
		return nil
	}

	if clients.Dynamic == nil {
		return fmt.Errorf("phase19: Clients.Dynamic is nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// Load + render template.
	tmplBytes, err := k8smanifests.FS.ReadFile(cloudNetworkMappingYAMLPath)
	if err != nil {
		return fmt.Errorf("phase19: reading embedded cloud-network-mapping template: %w", err)
	}
	rendered, err := render.RenderCloudNetworkMapping(tmplBytes, cl, st.Get)
	if err != nil {
		return fmt.Errorf("phase19: rendering cloud-network-mapping: %w", err)
	}

	// Apply via dynamic client.
	fmt.Fprintln(os.Stderr, "[phase 19] applying cloud-network-mapping ConfigMap")
	if err := applyRawYAML(ctx, clients.Dynamic, rendered); err != nil {
		return fmt.Errorf("phase19: applying cloud-network-mapping: %w", err)
	}

	st.Set("CLOUD_NETWORK_MAPPING_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))
	return st.Save()
}

// Phase19CloudNetworkMappingDown deletes the cloud-network-mapping ConfigMap.
// Tolerates NotFound.
func Phase19CloudNetworkMappingDown(ctx context.Context, _ *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintf(os.Stderr, "[phase 19 down] cloud-network-mapping: deleting CM in %s\n", InstanceNamespace)

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 19 down] warning: dynamic client not available, skipping CM deletion")
		st.Set("CLOUD_NETWORK_MAPPING_APPLIED_AT", "")
		return st.Save()
	}

	cmGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	err := clients.Dynamic.Resource(cmGVR).Namespace(InstanceNamespace).Delete(ctx, cloudNetworkMappingName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 19 down] warning: delete ConfigMap %s/%s: %v\n",
			InstanceNamespace, cloudNetworkMappingName, err)
	} else if k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 19 down] ConfigMap %s/%s already gone\n",
			InstanceNamespace, cloudNetworkMappingName)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 19 down] deleted ConfigMap %s/%s\n",
			InstanceNamespace, cloudNetworkMappingName)
	}

	st.Set("CLOUD_NETWORK_MAPPING_APPLIED_AT", "")
	return st.Save()
}

// persistHostDeviceConstants writes the host-device architecture constants to
// state.env for observability by Pass 3 and future slice-8 doctor/inspect.
func persistHostDeviceConstants(st *state.State) {
	st.Set("INSTANCE_NS", InstanceNamespace)
	st.Set("OPERATOR_NS", OperatorNamespace)
	st.Set("EXTERNAL_NAD", ExternalNAD)
	st.Set("INTERNAL_NAD", InternalNAD)
	st.Set("EXTERNAL_IFNAME", ExternalIFName)
	st.Set("INTERNAL_IFNAME", InternalIFName)
	st.Set("EXTERNAL_PCI", ExternalPCI)
	st.Set("INTERNAL_PCI", InternalPCI)
	st.Set("CLOUD_HOST_DEVICE_TAG", CloudHostDeviceTag)
	st.Set("CLOUD_HOST_DEVICE_NAME", CloudHostDeviceName)
}

// ensureMGMTSubnetAlias writes MGMT_SUBNET = PUBLIC_SUBNETS[0]. ALWAYS
// recomputes from PUBLIC_SUBNETS — does NOT preserve a prior cached value,
// because a stale MGMT_SUBNET from a previous cluster run would land in the
// cloud-network-mapping ConfigMap and cne-controller would compute backend
// gateways against the wrong subnet → TMM has no valid route to the cluster
// pod CIDR → HTTP requests at the VIP hit "no_acl_match" and get RST.
// Caught live on syd-tracer 2026-05-23 where a leftover state.env value
// from a prior session shadowed the actual public subnet ID.
func ensureMGMTSubnetAlias(st *state.State) error {
	publicSubnets := st.Get("PUBLIC_SUBNETS")
	if publicSubnets == "" {
		return fmt.Errorf("PUBLIC_SUBNETS not in state (Phase 03 must run first)")
	}
	// Split on comma, take first entry.
	first := publicSubnets
	for i, c := range publicSubnets {
		if c == ',' {
			first = publicSubnets[:i]
			break
		}
	}
	st.Set("MGMT_SUBNET", first)
	return nil
}
