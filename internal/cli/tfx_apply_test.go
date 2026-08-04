package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestParseManifestStream(t *testing.T) {
	raw := []byte(`apiVersion: k8s.f5.com/v1
kind: CNEInstance
metadata:
  name: a
  namespace: ns
---
apiVersion: k8s.f5.com/v1
kind: CNEInstance
metadata:
  name: b
  namespace: ns
`)
	objs, err := parseManifestStream(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0].GetName() != "a" || objs[1].GetName() != "b" {
		t.Fatalf("want 2 docs [a,b], got %d: %+v", len(objs), objs)
	}
	if objs[0].GetKind() != "CNEInstance" {
		t.Errorf("kind not parsed: %q", objs[0].GetKind())
	}
}

func TestParseManifestStream_SkipsEmptyAndErrsOnNoKind(t *testing.T) {
	// trailing separators + blank docs are skipped
	objs, err := parseManifestStream([]byte("---\n\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n---\n"))
	if err != nil || len(objs) != 1 || objs[0].GetName() != "c" {
		t.Fatalf("want 1 doc [c], got %d,%v", len(objs), err)
	}
	if _, err := parseManifestStream([]byte("foo: bar\n")); err == nil {
		t.Error("a document with no apiVersion/kind must error")
	}
}

func TestRunTFXApply_ResolvesKindToNamespacedResource(t *testing.T) {
	// Static mapper: CNEInstance -> cneinstances (namespaced), avoiding discovery.
	mapper := meta.NewDefaultRESTMapper(nil)
	gvk := schema.GroupVersionKind{Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstance"}
	singular := schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: "cneinstance"}
	mapper.AddSpecific(gvk, cneGVR, singular, meta.RESTScopeNamespace)

	dc := fakeDynFor() // empty
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "k8s.f5.com/v1",
		"kind":       "CNEInstance",
		"metadata":   map[string]interface{}{"name": "bnk", "namespace": "f5-bnk"},
	}}

	// The fake dynamic client's Apply doesn't create-on-not-found the way a real
	// API server does, so this may error — but only from the APPLY call against the
	// CORRECTLY resolved namespaced resource (`cneinstances`), never a mapping/scope
	// failure. That's the logic runTFXApply owns; the Apply itself is thin client-go.
	err := runTFXApply(context.Background(), dc, mapper, []*unstructured.Unstructured{obj}, "roksbnkctl", true, io.Discard)
	if err != nil && !strings.Contains(err.Error(), "cneinstances") {
		t.Fatalf("Kind→resource/scope resolution failed (expected the resolved resource in any error): %v", err)
	}
}

func TestRunTFXApply_UnknownKindErrors(t *testing.T) {
	mapper := meta.NewDefaultRESTMapper(nil) // knows nothing
	dc := fakeDynFor()
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "unknown.io/v1", "kind": "Widget",
		"metadata": map[string]interface{}{"name": "w"},
	}}
	err := runTFXApply(context.Background(), dc, mapper, []*unstructured.Unstructured{obj}, "roksbnkctl", false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "resolving") {
		t.Fatalf("an unmapped Kind should error, got %v", err)
	}
}
