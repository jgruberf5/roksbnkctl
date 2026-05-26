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
	nadsYAMLPath    = "host-device/network-attachment-defs.yaml.tmpl"
	phase20FieldMgr = "awsbnkctl-phase20"
)

// nadNamespaces lists the namespaces where the host-device NADs must be applied.
// Per aws-gpu-setup/deploy-bnk.sh:143: NAD_NS_SET=("$INSTANCE_NS" "default").
var nadNamespaces = []string{InstanceNamespace, "default"}

// Phase20NADs renders and applies the host-device NetworkAttachmentDefinitions
// into both f5-cne-system AND default namespaces. The NADs are required before
// the CNEInstance CR webhook will accept the CR (Pass 3).
//
// Idempotent: server-side-apply via applyUnstructured.
// D-005: CheckAuthOrDie called at entry.
func Phase20NADs(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 20] NADs: cluster=%s\n", name)

	if dryRun {
		fmt.Fprintf(os.Stderr,
			"[phase 20] dry-run: would apply NADs (%s + %s) in %s and default\n",
			ExternalNAD, InternalNAD, InstanceNamespace)
		st.Set("NADS_APPLIED_AT", "dry-run")
		return nil
	}

	if clients.Dynamic == nil {
		return fmt.Errorf("phase20: Clients.Dynamic is nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// Load template once — render once per namespace.
	tmplBytes, err := k8smanifests.FS.ReadFile(nadsYAMLPath)
	if err != nil {
		return fmt.Errorf("phase20: reading embedded NADs template: %w", err)
	}

	for _, ns := range nadNamespaces {
		rendered, err := render.RenderNADs(tmplBytes, ns, st.Get)
		if err != nil {
			return fmt.Errorf("phase20: rendering NADs for namespace %s: %w", ns, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 20] applying NADs in namespace %s\n", ns)
		if err := applyRawYAML(ctx, clients, rendered); err != nil {
			return fmt.Errorf("phase20: applying NADs in %s: %w", ns, err)
		}
	}

	st.Set("NADS_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))
	return st.Save()
}

// Phase20NADsDown deletes both NADs from both namespaces. Tolerates NotFound.
func Phase20NADsDown(ctx context.Context, _ *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintf(os.Stderr, "[phase 20 down] NADs: deleting from %v\n", nadNamespaces)

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 20 down] warning: dynamic client not available, skipping NAD deletion")
		st.Set("NADS_APPLIED_AT", "")
		return st.Save()
	}

	nadGVR := schema.GroupVersionResource{
		Group:    "k8s.cni.cncf.io",
		Version:  "v1",
		Resource: "network-attachment-definitions",
	}

	for _, ns := range nadNamespaces {
		for _, nadName := range []string{ExternalNAD, InternalNAD} {
			err := clients.Dynamic.Resource(nadGVR).Namespace(ns).Delete(ctx, nadName, metav1.DeleteOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				fmt.Fprintf(os.Stderr, "[phase 20 down] warning: delete NAD %s/%s: %v\n", ns, nadName, err)
			} else if k8serrors.IsNotFound(err) {
				fmt.Fprintf(os.Stderr, "[phase 20 down] NAD %s/%s already gone\n", ns, nadName)
			} else {
				fmt.Fprintf(os.Stderr, "[phase 20 down] deleted NAD %s/%s\n", ns, nadName)
			}
		}
	}

	st.Set("NADS_APPLIED_AT", "")
	return st.Save()
}
