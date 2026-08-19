package orchestration

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// The exact message BNK puts on the condition when it loses the race, copied
// from the CNEInstance on the cluster where this was diagnosed (#96). The
// repair keys on it, so a test that invented its own wording would prove
// nothing about the real failure.
const blockedMsg = `failed to create CRD backendtlspolicies.gateway.networking.k8s.io: ` +
	`admission webhook denied the request: this cluster manages Gateway API CRDs via an admission policy`

func cneInstance(ns, name string, conds ...map[string]any) *unstructured.Unstructured {
	cs := make([]any, 0, len(conds))
	for _, c := range conds {
		cs = append(cs, c)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "k8s.f5.com/v1",
		"kind":       "CNEInstance",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"status":     map[string]any{"conditions": cs},
	}}
}

func cond(t, s, m string) map[string]any {
	return map[string]any{"type": t, "status": s, "message": m}
}

func TestCRDInstallerBlockedMatchesOnlyTheAdmissionRace(t *testing.T) {
	// The healthy list is the real one, read off a converged BNK cluster —
	// including Available=False, because most of an install is spent there and
	// firing the repair on it would restart FLO mid-converge.
	healthy := cneInstance("f5-bnk", "f5-bnk-f5-cne-controller",
		cond("Accepted", "True", "Initial processing performed"),
		cond("Available", "False", "pod f5-bnk/f5-tmm-vcxqt: containers with unready status: [f5-tmm]"),
		cond("CRDInstallerAvailable", "True", ""),
		cond("CNEControllerAvailable", "True", ""),
	)

	cases := []struct {
		name string
		obj  *unstructured.Unstructured
		want bool
	}{
		{"a converged cluster is not blocked", healthy, false},
		{
			"the admission race is blocked",
			cneInstance("f5-bnk", "x", cond("CRDInstallerAvailable", "False", blockedMsg)),
			true,
		},
		{
			// The installer failing for an unrelated reason must NOT trigger a
			// repair: deleting the policy and restarting FLO would not fix it,
			// and would hide the real error behind a restart.
			"an unrelated installer failure is not blocked",
			cneInstance("f5-bnk", "x", cond("CRDInstallerAvailable", "False", "ImagePullBackOff")),
			false,
		},
		{
			"still starting up is not blocked",
			cneInstance("f5-bnk", "x", cond("Accepted", "True", "")),
			false,
		},
		{"no status at all is not blocked", &unstructured.Unstructured{Object: map[string]any{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := crdInstallerBlocked(tc.obj.Object); got != tc.want {
				t.Fatalf("crdInstallerBlocked = %v, want %v", got, tc.want)
			}
		})
	}
}

func repairFake(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		admissionSweepGVRs[0]: "ValidatingAdmissionPolicyBindingList",
		admissionSweepGVRs[1]: "ValidatingAdmissionPolicyList",
		admissionSweepGVRs[2]: "ValidatingWebhookConfigurationList",
		crdInstallerJobGVR:    "JobList",
		deploymentGVR:         "DeploymentList",
		cneInstanceGVR:        "CNEInstanceList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func nsObj(apiVersion, kind, ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion, "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": ns},
		"spec":     map[string]any{"template": map[string]any{"metadata": map[string]any{}}},
	}}
}

// The names here are not invented: all three were read off the live cluster —
// deployment flo-f5-lifecycle-operator in the FLO namespace, Job crd-installer
// in the utils namespace, CNEInstance <flo-ns>-f5-cne-controller. A repair that
// targets the wrong name silently does nothing, which is the failure mode this
// guards.
func TestWatchAndRepairClearsTheBlockAndRestartsFLO(t *testing.T) {
	dc := repairFake(
		vapObj("ValidatingAdmissionPolicy"),
		vapObj("ValidatingAdmissionPolicyBinding"),
		nsObj("batch/v1", "Job", "f5-utils", "crd-installer"),
		nsObj("apps/v1", "Deployment", "f5-bnk", "flo-f5-lifecycle-operator"),
		cneInstance("f5-bnk", "f5-bnk-f5-cne-controller", cond("CRDInstallerAvailable", "False", blockedMsg)),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	watchAndRepairCRDInstaller(ctx, dc, "f5-bnk", "f5-utils", time.Millisecond)
	// It returns of its own accord once repaired — a return only because the
	// context expired would mean it never fired.
	if ctx.Err() != nil {
		t.Fatal("watchAndRepairCRDInstaller did not return until the context expired; it never repaired")
	}

	for _, gvr := range admissionSweepGVRs[:2] {
		if _, err := dc.Resource(gvr).Get(ctx, admissionSweepName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Errorf("%s still present after repair (err=%v)", gvr.Resource, err)
		}
	}
	if _, err := dc.Resource(crdInstallerJobGVR).Namespace("f5-utils").
		Get(ctx, "crd-installer", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("failed crd-installer Job was not deleted (err=%v)", err)
	}

	dep, err := dc.Resource(deploymentGVR).Namespace("f5-bnk").
		Get(ctx, "flo-f5-lifecycle-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the FLO deployment: %v", err)
	}
	ann, found, _ := unstructured.NestedStringMap(dep.Object, "spec", "template", "metadata", "annotations")
	if !found || ann["roksbnkctl/crd-installer-repair"] == "" {
		t.Fatalf("FLO pod template was not stamped, so the operator never restarted: %v", dep.Object["spec"])
	}
}

// The negative control. A converged cluster must come out of the watch
// completely untouched — this is what stops the repair from bouncing FLO
// during a normal install.
func TestWatchLeavesAHealthyClusterAlone(t *testing.T) {
	dc := repairFake(
		vapObj("ValidatingAdmissionPolicy"),
		nsObj("apps/v1", "Deployment", "f5-bnk", "flo-f5-lifecycle-operator"),
		cneInstance("f5-bnk", "f5-bnk-f5-cne-controller",
			cond("CRDInstallerAvailable", "True", ""),
			cond("Available", "False", "pod f5-bnk/f5-tmm-x: containers with unready status: [f5-tmm]")),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	watchAndRepairCRDInstaller(ctx, dc, "f5-bnk", "f5-utils", time.Millisecond)

	gctx := context.Background()
	if _, err := dc.Resource(admissionSweepGVRs[1]).Get(gctx, admissionSweepName, metav1.GetOptions{}); err != nil {
		t.Errorf("the admission policy was deleted on a healthy cluster: %v", err)
	}
	dep, err := dc.Resource(deploymentGVR).Namespace("f5-bnk").
		Get(gctx, "flo-f5-lifecycle-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the FLO deployment: %v", err)
	}
	if _, found, _ := unstructured.NestedStringMap(dep.Object, "spec", "template", "metadata", "annotations"); found {
		t.Error("FLO was restarted on a healthy cluster")
	}
}
