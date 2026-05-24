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
	irsaSAYAMLPath  = "shared/irsa-sa.yaml.tmpl"
	phase21FieldMgr = "awsbnkctl-phase21"
)

// cneSAName returns the CNE ServiceAccount name derived from the cluster name.
// Convention: f5-cne-controller-<cluster>-bnk-serviceaccount.
func cneSAName(clusterName string) string {
	return "f5-cne-controller-" + clusterName + "-bnk-serviceaccount"
}

// Phase21IRSASA renders and applies the IRSA ServiceAccount into f5-cne-system.
// The SA is required so the CNEInstance controller can assume the IRSA role
// (Pass 3). This is pre-creation — the operator creates the SA before the
// CNEInstance CR so that the role-ARN annotation is present before the
// cne-controller pod starts.
//
// Reads CNE_IRSA_ROLE_ARN from state (Phase 18); errors clearly if missing.
// Idempotent: server-side-apply upserts the annotation on re-run.
// D-005: CheckAuthOrDie called at entry.
func Phase21IRSASA(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 21] IRSA SA: cluster=%s\n", name)

	// Validate prerequisite state key.
	roleARN := st.Get("CNE_IRSA_ROLE_ARN")
	if roleARN == "" {
		return fmt.Errorf("phase21: CNE_IRSA_ROLE_ARN not in state — Phase 18 (IRSA/OIDC) must run first")
	}

	saName := cneSAName(name)

	if dryRun {
		fmt.Fprintf(os.Stderr,
			"[phase 21] dry-run: would pre-create SA %s in %s with IRSA annotation\n",
			saName, InstanceNamespace)
		st.Set("IRSA_SA_APPLIED_AT", "dry-run")
		st.Set("CNE_SA_NAME", saName)
		return nil
	}

	if clients.Dynamic == nil {
		return fmt.Errorf("phase21: Clients.Dynamic is nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// Load + render template.
	tmplBytes, err := k8smanifests.FS.ReadFile(irsaSAYAMLPath)
	if err != nil {
		return fmt.Errorf("phase21: reading embedded irsa-sa template: %w", err)
	}
	rendered, err := render.RenderIRSASA(tmplBytes, cl, st.Get)
	if err != nil {
		return fmt.Errorf("phase21: rendering irsa-sa: %w", err)
	}

	// Apply via dynamic client (idempotent annotation upsert via SSA).
	fmt.Fprintf(os.Stderr, "[phase 21] applying IRSA SA %s in %s\n", saName, InstanceNamespace)
	if err := applyRawYAML(ctx, clients, rendered); err != nil {
		return fmt.Errorf("phase21: applying IRSA SA: %w", err)
	}

	st.Set("IRSA_SA_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))
	st.Set("CNE_SA_NAME", saName)
	return st.Save()
}

// Phase21IRSASADown deletes the IRSA ServiceAccount from f5-cne-system.
// Tolerates NotFound.
func Phase21IRSASADown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	saName := cneSAName(name)
	fmt.Fprintf(os.Stderr, "[phase 21 down] IRSA SA: deleting %s from %s\n", saName, InstanceNamespace)

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 21 down] warning: dynamic client not available, skipping SA deletion")
		clearPhase21State(st)
		return st.Save()
	}

	saGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}
	err := clients.Dynamic.Resource(saGVR).Namespace(InstanceNamespace).Delete(ctx, saName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 21 down] warning: delete SA %s/%s: %v\n", InstanceNamespace, saName, err)
	} else if k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 21 down] SA %s/%s already gone\n", InstanceNamespace, saName)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 21 down] deleted SA %s/%s\n", InstanceNamespace, saName)
	}

	clearPhase21State(st)
	return st.Save()
}

// clearPhase21State zeroes all phase 21 state keys.
func clearPhase21State(st *state.State) {
	for _, k := range []string{"IRSA_SA_APPLIED_AT", "CNE_SA_NAME"} {
		st.Set(k, "")
	}
}
