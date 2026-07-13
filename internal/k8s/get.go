package k8s

import (
	"errors"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1beta1 "k8s.io/apimachinery/pkg/apis/meta/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/scheme"
)

// tableAcceptHeader asks the API server to return a metav1.Table instead of the
// raw objects. This is how kubectl gets its columns: the server builds the table,
// so a CRD's `additionalPrinterColumns` (License's STATE/MODE/ENTITLEMENT,
// CNEInstance's, ...) come back as real columns. Without it the response is a
// plain object list and the table printer can only fall back to NAME/AGE.
func transformTableRequests(req *rest.Request) {
	req.SetHeader("Accept", strings.Join([]string{
		fmt.Sprintf("application/json;as=Table;v=%s;g=%s", metav1.SchemeGroupVersion.Version, metav1.GroupName),
		fmt.Sprintf("application/json;as=Table;v=%s;g=%s", metav1beta1.SchemeGroupVersion.Version, metav1beta1.GroupName),
		"application/json",
	}, ","))
}

// asTable converts the server's Table — which the Unstructured() builder hands
// back as an *unstructured.Unstructured of kind "Table" — into the *metav1.Table
// the cli-runtime table printer knows how to render. Anything that is not a Table
// is returned untouched, so the NAME-column fallback still applies.
func asTable(obj runtime.Object) runtime.Object {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok || u.GetKind() != "Table" {
		return obj
	}
	t := &metav1.Table{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, t); err != nil {
		return obj
	}

	// Decode each row's embedded object, exactly as kubectl's decodeIntoTable
	// does. FromUnstructured only fills RawExtension.Raw and leaves .Object nil,
	// but cli-runtime's decorateTable reads per-row metadata via
	// `row.Object.Object` — with it nil it cannot get an accessor and blanks the
	// cell. That is what makes `k get pods -A` print an empty NAMESPACE column.
	for i := range t.Rows {
		row := &t.Rows[i]
		if row.Object.Raw == nil || row.Object.Object != nil {
			continue
		}
		decoded, err := runtime.Decode(unstructured.UnstructuredJSONScheme, row.Object.Raw)
		if err != nil {
			continue // leave the row undecorated rather than failing the whole get
		}
		row.Object.Object = decoded
	}
	return t
}

// tableRowCount reports how many rows the server-rendered Tables carry, and
// whether every Info actually was a Table. An empty result now arrives as one
// Table with zero rows (not zero Infos), so the "No resources found" path has to
// be driven off the row count rather than len(infos).
func tableRowCount(infos []*resource.Info) (rows int, allTables bool) {
	allTables = true
	for _, info := range infos {
		t, ok := asTable(info.Object).(*metav1.Table)
		if !ok {
			allTables = false
			continue
		}
		rows += len(t.Rows)
	}
	return rows, allTables
}

// GetOptions captures the flag-parsed inputs to `roksbnkctl k get`.
//
// Args is the trailing positional list (e.g. ["pods"], ["pod", "foo"],
// or ["pods,services"]). Namespace + AllNamespaces are mutually
// exclusive at the CLI; LabelSelector is the kubectl `-l` value.
//
// Output is the kubectl `-o` value (yaml, json, wide, name,
// jsonpath=..., go-template=..., or "" for the human tabular default).
//
// KubeconfigPath is passed through to BuildRESTConfig — empty for the
// host default, "in-cluster" for in-pod use.
type GetOptions struct {
	Args           []string
	Namespace      string
	AllNamespaces  bool
	LabelSelector  string
	Output         string
	KubeconfigPath string

	// IOStreams is the destination for printer output and human-only
	// stderr noise. Defaults applied in Run() if zero-valued.
	IOStreams genericiooptions.IOStreams
}

// Run executes a kubectl-equivalent `get` against the configured
// kubeconfig. Output formatting is delegated to cli-runtime's
// PrintFlags so `-o yaml/json/wide/name/jsonpath/go-template` matches
// kubectl byte-for-byte.
func (o *GetOptions) Run() error {
	if len(o.Args) == 0 {
		return errors.New("at least one resource type required (e.g. `pods`, `nodes`, `pods/foo`)")
	}
	if o.IOStreams.Out == nil {
		o.IOStreams.Out = io.Discard
	}
	if o.IOStreams.ErrOut == nil {
		o.IOStreams.ErrOut = io.Discard
	}

	getter := newRESTClientGetter(o.KubeconfigPath, o.Namespace)

	// Build a printer that matches the kubectl output flags. PrintFlags
	// defaults to a no-op printer; we only set it for non-default
	// outputs and use the table path otherwise.
	pf := genericclioptions.NewPrintFlags("").WithTypeSetter(scheme.Scheme)
	out := o.Output
	pf.OutputFormat = &out

	// resource.Builder lifts the heavy lifting: discovery, RESTMapper,
	// plural/singular/short-name resolution, label selectors, the
	// AllNamespaces flag and the type/name positional grammar.
	b := resource.NewBuilder(getter).
		Unstructured().
		NamespaceParam(o.Namespace).
		DefaultNamespace().
		AllNamespaces(o.AllNamespaces).
		LabelSelectorParam(o.LabelSelector).
		ResourceTypeOrNameArgs(true, o.Args...).
		ContinueOnError().
		Latest().
		Flatten()

	// Only the human tabular outputs want a server-rendered Table; -o yaml/json/
	// jsonpath/go-template must keep receiving the real objects.
	serverPrint := o.Output == "" || o.Output == "wide"
	if serverPrint {
		b = b.TransformRequests(transformTableRequests)
	}

	r := b.Do()
	if err := r.Err(); err != nil {
		return err
	}

	infos, err := r.Infos()
	if err != nil {
		return err
	}

	// Empty result: kubectl prints "No resources found" (with the namespace if
	// scoped). Zero Infos covers the non-table paths, but a server-rendered Table
	// never yields zero Infos — an empty list comes back as ONE Table carrying zero
	// rows, so that has to be detected on the row count or the message is dead code
	// and the user gets silent, blank output.
	empty := len(infos) == 0
	if !empty && serverPrint {
		if rows, allTables := tableRowCount(infos); allTables && rows == 0 {
			empty = true
		}
	}
	if empty {
		if o.Namespace != "" && !o.AllNamespaces {
			fmt.Fprintf(o.IOStreams.ErrOut, "No resources found in %s namespace.\n", o.Namespace)
		} else {
			fmt.Fprintln(o.IOStreams.ErrOut, "No resources found")
		}
		return nil
	}

	// For -o yaml/json/name/jsonpath/template: print each Info's
	// runtime.Object via the configured printer. Build a List wrapper
	// when there are multiple Infos so the output matches kubectl
	// "List" semantics.
	switch o.Output {
	case "", "wide":
		// Tabular fallback (default + wide). cli-runtime's table
		// printer renders v1.Table objects when the server returns
		// them; otherwise it falls back to a NAME column.
		tp := printers.NewTablePrinter(printers.PrintOptions{
			WithNamespace: o.AllNamespaces,
			Wide:          o.Output == "wide",
		})
		for _, info := range infos {
			if err := tp.PrintObj(asTable(info.Object), o.IOStreams.Out); err != nil {
				return err
			}
		}
		return nil
	default:
		printer, err := pf.ToPrinter()
		if err != nil {
			return fmt.Errorf("setting up output format %q: %w", o.Output, err)
		}
		if len(infos) == 1 {
			return printer.PrintObj(infos[0].Object, o.IOStreams.Out)
		}
		// Multiple objects — wrap in an UnstructuredList so the printer
		// produces a single List document.
		list := &unstructured.UnstructuredList{}
		list.SetAPIVersion("v1")
		list.SetKind("List")
		for _, info := range infos {
			if u, ok := info.Object.(*unstructured.Unstructured); ok {
				list.Items = append(list.Items, *u)
				continue
			}
			// resource.Builder().Unstructured() should always give us
			// Unstructured; defensive copy just in case.
			u := &unstructured.Unstructured{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(asMap(info.Object), u); err == nil {
				list.Items = append(list.Items, *u)
			}
		}
		return printer.PrintObj(list, o.IOStreams.Out)
	}
}

// asMap is a defensive helper that pulls a map[string]interface{} out
// of a runtime.Object that wasn't already Unstructured. The expected
// path goes through Unstructured() above; this exists so the function
// degrades gracefully rather than panicking.
func asMap(obj runtime.Object) map[string]interface{} {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u.UnstructuredContent()
	}
	return map[string]interface{}{}
}

// IsNotFound reports whether err is a kubernetes "not found" error.
// Helper for callers (CLI) that want to surface a clean exit code.
func IsNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}
