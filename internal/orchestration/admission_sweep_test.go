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

func vapObj(kind string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "admissionregistration.k8s.io/v1",
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": admissionSweepName},
	}}
}

func TestRunAdmissionSweepLoop_DeletesBindingAndPolicy(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		admissionSweepGVRs[0]: "ValidatingAdmissionPolicyBindingList",
		admissionSweepGVRs[1]: "ValidatingAdmissionPolicyList",
	}
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		vapObj("ValidatingAdmissionPolicyBinding"), vapObj("ValidatingAdmissionPolicy"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAdmissionSweepLoop(ctx, dc, 5*time.Millisecond)
	}()

	// The loop deletes immediately on entry (before the first tick); give it a beat.
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sweep loop did not stop on context cancel")
	}

	// Both objects must be gone (fresh context — the loop's was cancelled).
	for _, gvr := range admissionSweepGVRs {
		_, err := dc.Resource(gvr).Get(context.Background(), admissionSweepName, metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("%s %q should have been swept, Get err = %v", gvr.Resource, admissionSweepName, err)
		}
	}
}

func TestRunAdmissionSweepLoop_NotFoundIsHarmless(t *testing.T) {
	// Empty cluster: the loop's delete-if-present must not panic or spin on NotFound.
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		admissionSweepGVRs[0]: "ValidatingAdmissionPolicyBindingList",
		admissionSweepGVRs[1]: "ValidatingAdmissionPolicyList",
	}
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	runAdmissionSweepLoop(ctx, dc, 5*time.Millisecond) // returns when ctx expires
}
