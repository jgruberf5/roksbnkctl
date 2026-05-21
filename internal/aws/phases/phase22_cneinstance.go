package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

const (
	cneinstanceYAMLPath  = "shared/cneinstance.yaml.tmpl"
	phase22ReconcileWait = 2 * time.Minute
	phase22PollInterval  = 5 * time.Second
	phase22FinalizerWait = 30 * time.Second

	// cneInstanceGVR key components — kept as locals so resolveGVR handles dispatch.
	cneInstanceGroup    = "k8s.f5.com"
	cneInstanceVersion  = "v1"
	cneInstanceResource = "cneinstances"
)

// cneinstanceGVR is the GVR for the CNEInstance CR.
var cneinstanceGVR = schema.GroupVersionResource{
	Group:    cneInstanceGroup,
	Version:  cneInstanceVersion,
	Resource: cneInstanceResource,
}

// Phase22CNEInstance renders and applies the CNEInstance CR into f5-cne-system.
// After apply it polls status.conditions to be non-empty for up to 2 min —
// the "reconcile started" gate — to catch "reconcile never started" pathology
// early (per Architect mandate).
//
// State keys written:
//   - CNEINSTANCE_NAME
//   - CNEINSTANCE_APPLIED_AT
//   - CNEINSTANCE_RECONCILE_STARTED_AT
//
// D-005: CheckAuthOrDie called at entry.
func Phase22CNEInstance(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	crName := name + "-bnk"
	fmt.Fprintf(os.Stderr, "[phase 22] CNEInstance: cluster=%s cr=%s\n", name, crName)

	if dryRun {
		fmt.Fprintf(os.Stderr,
			"[phase 22] dry-run: would apply CNEInstance %s in %s with deploymentSize=%s\n",
			crName, InstanceNamespace, cl.Bnk.DeploymentSize)
		st.Set("CNEINSTANCE_NAME", crName)
		st.Set("CNEINSTANCE_APPLIED_AT", "dry-run")
		st.Set("CNEINSTANCE_RECONCILE_STARTED_AT", "dry-run")
		return nil
	}

	if clients.Dynamic == nil {
		return fmt.Errorf("phase22: Clients.Dynamic is nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// Load + render template.
	tmplBytes, err := k8smanifests.FS.ReadFile(cneinstanceYAMLPath)
	if err != nil {
		return fmt.Errorf("phase22: reading embedded cneinstance template: %w", err)
	}
	rendered, err := render.RenderCNEInstance(tmplBytes, cl, st.Get)
	if err != nil {
		return fmt.Errorf("phase22: rendering cneinstance: %w", err)
	}

	// Apply via dynamic client (SSA).
	fmt.Fprintf(os.Stderr, "[phase 22] applying CNEInstance %s in %s\n", crName, InstanceNamespace)
	if err := applyRawYAML(ctx, clients.Dynamic, rendered); err != nil {
		return fmt.Errorf("phase22: applying CNEInstance: %w", err)
	}

	st.Set("CNEINSTANCE_NAME", crName)
	st.Set("CNEINSTANCE_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))
	if err := st.Save(); err != nil {
		return fmt.Errorf("phase22: saving state after apply: %w", err)
	}

	// Poll status.conditions to be non-empty — the "reconcile started" gate.
	// We use an inline loop (not WaitForUnstructuredCondition) because we need
	// NestedSlice non-empty check, not a field equality check.
	fmt.Fprintf(os.Stderr, "[phase 22] polling CNEInstance %s for reconcile start (up to %s)\n",
		crName, phase22ReconcileWait)
	deadline := time.Now().Add(phase22ReconcileWait)
	for {
		obj, err := clients.Dynamic.Resource(cneinstanceGVR).Namespace(InstanceNamespace).Get(ctx, crName, metav1.GetOptions{})
		if err != nil {
			// Not found yet — keep polling.
			fmt.Fprintf(os.Stderr, "[phase 22] CNEInstance not yet visible: %v\n", err)
		} else {
			conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
			if len(conditions) > 0 {
				fmt.Fprintf(os.Stderr, "[phase 22] CNEInstance %s reconcile started (conditions=%d)\n",
					crName, len(conditions))
				st.Set("CNEINSTANCE_RECONCILE_STARTED_AT", time.Now().UTC().Format(time.RFC3339))
				return st.Save()
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("phase22: timeout waiting for CNEInstance %s/%s reconcile to start (status.conditions stayed empty for %s)",
				InstanceNamespace, crName, phase22ReconcileWait)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("phase22: context cancelled while waiting for CNEInstance reconcile: %w", ctx.Err())
		case <-time.After(phase22PollInterval):
		}
	}
}

// Phase22CNEInstanceDown deletes the CNEInstance CR from f5-cne-system.
// Tolerates NotFound. Waits up to 30 s for finalizer cleanup before returning.
func Phase22CNEInstanceDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	crName := cl.Metadata.Name + "-bnk"
	fmt.Fprintf(os.Stderr, "[phase 22 down] CNEInstance: deleting %s from %s\n", crName, InstanceNamespace)

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 22 down] warning: dynamic client not available, skipping CNEInstance deletion")
		clearPhase22State(st)
		return st.Save()
	}

	err := clients.Dynamic.Resource(cneinstanceGVR).Namespace(InstanceNamespace).Delete(ctx, crName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 22 down] warning: delete CNEInstance %s/%s: %v\n",
			InstanceNamespace, crName, err)
	} else if k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 22 down] CNEInstance %s/%s already gone\n",
			InstanceNamespace, crName)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 22 down] deleted CNEInstance %s/%s — waiting %s for finalizer cleanup\n",
			InstanceNamespace, crName, phase22FinalizerWait)
		// Brief wait for controller finalizers to drop before returning.
		select {
		case <-time.After(phase22FinalizerWait):
		case <-ctx.Done():
		}
	}

	clearPhase22State(st)
	return st.Save()
}

// clearPhase22State zeroes all phase 22 state keys.
func clearPhase22State(st *state.State) {
	for _, k := range []string{
		"CNEINSTANCE_NAME",
		"CNEINSTANCE_APPLIED_AT",
		"CNEINSTANCE_RECONCILE_STARTED_AT",
		"CNEINSTANCE_READY_AT",
	} {
		st.Set(k, "")
	}
}
