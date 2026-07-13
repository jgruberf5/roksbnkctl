package k8s

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
)

func infosFor(t *testing.T, objs ...runtime.Object) []*resource.Info {
	t.Helper()
	out := make([]*resource.Info, 0, len(objs))
	for _, o := range objs {
		out = append(out, &resource.Info{Object: o})
	}
	return out
}

// serverTable builds the shape the API server actually returns for
// `Accept: application/json;as=Table` — an unstructured of kind Table whose rows
// carry the row object as raw JSON under `object`.
func serverTable(t *testing.T, namespaces ...string) *unstructured.Unstructured {
	t.Helper()
	rows := make([]any, 0, len(namespaces))
	for i, ns := range namespaces {
		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata":   map[string]any{"name": "pod", "namespace": ns},
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("marshal row %d: %v", i, err)
		}
		rows = append(rows, map[string]any{
			"cells":  []any{"pod", "1/1", "Running"},
			"object": json.RawMessage(raw),
		})
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "meta.k8s.io/v1",
		"kind":       "Table",
		"columnDefinitions": []any{
			map[string]any{"name": "Name", "type": "string"},
			map[string]any{"name": "Ready", "type": "string"},
			map[string]any{"name": "Status", "type": "string"},
		},
		"rows": rows,
	}}
	return u
}

// The printer decorates rows (e.g. the NAMESPACE column under -A) by reading
// row.Object.Object. FromUnstructured alone only fills row.Object.Raw and leaves
// .Object nil, so without a per-row decode every namespace cell prints blank.
func TestAsTableDecodesRowObjects(t *testing.T) {
	got, ok := asTable(serverTable(t, "kube-system", "default")).(*metav1.Table)
	if !ok {
		t.Fatal("asTable did not return a *metav1.Table")
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(got.Rows))
	}
	for i, row := range got.Rows {
		if row.Object.Object == nil {
			t.Fatalf("row %d: Object.Object is nil — the printer cannot get an accessor, "+
				"so `k get -A` prints a blank NAMESPACE cell", i)
		}
		u, ok := row.Object.Object.(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("row %d: decoded into %T, want *unstructured.Unstructured", i, row.Object.Object)
		}
		if u.GetNamespace() == "" {
			t.Errorf("row %d: decoded object has no namespace", i)
		}
	}
	if ns := got.Rows[0].Object.Object.(*unstructured.Unstructured).GetNamespace(); ns != "kube-system" {
		t.Errorf("row 0 namespace = %q, want kube-system", ns)
	}
}

// An empty result comes back as ONE Table with zero rows — not zero Infos — so the
// "No resources found" path must key off the row count. Before this, `k get pods -n
// empty-ns` printed nothing at all and exited 0.
func TestTableRowCountDetectsEmptyResult(t *testing.T) {
	rows, allTables := tableRowCount(infosFor(t, serverTable(t)))
	if !allTables {
		t.Fatal("allTables = false for a Table-only result")
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want 0 — an empty result must be detectable", rows)
	}

	rows, allTables = tableRowCount(infosFor(t, serverTable(t, "default", "kube-system")))
	if !allTables || rows != 2 {
		t.Fatalf("rows = %d allTables = %v, want 2/true", rows, allTables)
	}
}

// A non-Table object (the fallback path, e.g. a server that ignored the Accept
// header) must be reported so the caller keeps the len(infos)==0 semantics.
func TestTableRowCountFlagsNonTable(t *testing.T) {
	pod := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "p", "namespace": "default"},
	}}
	if _, allTables := tableRowCount(infosFor(t, pod)); allTables {
		t.Error("allTables = true for a non-Table object; the empty-result check would misfire")
	}
}

// asTable must pass non-Tables through untouched, so -o yaml/json keep the real object.
func TestAsTablePassesThroughNonTable(t *testing.T) {
	pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod"}}
	if got := asTable(pod); got != runtime.Object(pod) {
		t.Errorf("asTable mutated a non-Table object: %T", got)
	}
}
