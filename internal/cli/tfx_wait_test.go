package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var cneGVR = schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: "cneinstances"}

func cneObject(conditionStatus, state string) *unstructured.Unstructured {
	status := map[string]interface{}{}
	if conditionStatus != "" {
		status["conditions"] = []interface{}{
			map[string]interface{}{"type": "CNEControllerAvailable", "status": conditionStatus},
		}
	}
	if state != "" {
		status["state"] = state
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "k8s.f5.com/v1",
		"kind":       "CNEInstance",
		"metadata":   map[string]interface{}{"name": "bnk", "namespace": "f5-bnk"},
		"status":     status,
	}}
}

func fakeDynFor(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{cneGVR: "CNEInstanceList"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func TestRunTFXWait_ConditionAlreadyReady(t *testing.T) {
	dc := fakeDynFor(cneObject("True", ""))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m := conditionMatcher{typ: "CNEControllerAvailable", status: "True"}
	if err := runTFXWaitPoll(context.Background(), ri, "bnk", m, time.Second, 5*time.Millisecond, io.Discard); err != nil {
		t.Fatalf("wait on a ready object should succeed, got %v", err)
	}
}

func TestRunTFXWait_JSONPathAlreadyReady(t *testing.T) {
	dc := fakeDynFor(cneObject("", "Active"))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m, err := parseWaitFor("jsonpath=status.state=Active")
	if err != nil {
		t.Fatal(err)
	}
	if err := runTFXWaitPoll(context.Background(), ri, "bnk", m, time.Second, 5*time.Millisecond, io.Discard); err != nil {
		t.Fatalf("jsonpath wait on Active should succeed, got %v", err)
	}
}

func TestRunTFXWait_TimesOutWhenNeverReady(t *testing.T) {
	dc := fakeDynFor(cneObject("False", "")) // condition present but False
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m := conditionMatcher{typ: "CNEControllerAvailable", status: "True"}
	var buf strings.Builder
	err := runTFXWaitPoll(context.Background(), ri, "bnk", m, 40*time.Millisecond, 5*time.Millisecond, &buf)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got %v", err)
	}
	if !strings.Contains(buf.String(), "not ready") {
		t.Errorf("expected a 'not ready' log line, got:\n%s", buf.String())
	}
}

func TestRunTFXWait_TimesOutWhenMissing(t *testing.T) {
	dc := fakeDynFor() // no object exists
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m := conditionMatcher{typ: "CNEControllerAvailable", status: "True"}
	var buf strings.Builder
	err := runTFXWaitPoll(context.Background(), ri, "bnk", m, 40*time.Millisecond, 5*time.Millisecond, &buf)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error for a missing object, got %v", err)
	}
	if !strings.Contains(buf.String(), "not found yet") {
		t.Errorf("expected a 'not found yet' log line, got:\n%s", buf.String())
	}
}

func TestRunTFXWait_BecomesReady(t *testing.T) {
	dc := fakeDynFor(cneObject("False", "")) // starts not-ready
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m := conditionMatcher{typ: "CNEControllerAvailable", status: "True"}

	// Flip it to ready shortly after the wait starts.
	go func() {
		time.Sleep(25 * time.Millisecond)
		_, _ = ri.Update(context.Background(), cneObject("True", ""), metav1.UpdateOptions{})
	}()

	if err := runTFXWaitPoll(context.Background(), ri, "bnk", m, 3*time.Second, 5*time.Millisecond, io.Discard); err != nil {
		t.Fatalf("wait should succeed once the object becomes ready, got %v", err)
	}
}
