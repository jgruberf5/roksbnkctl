package k8s

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// Server-side apply for callers that already hold a manifest in memory and a
// client, rather than a file path and a set of CLI flags.
//
// ApplyOptions.Run above is the `roksbnkctl k apply` command: it resolves flags,
// builds its own clients from a kubeconfig PATH and reads the manifest off disk.
// The Gateway API bundle (#185) has none of those — it arrives as bytes, pulled
// out of the mirror or fetched over HTTPS, and is applied with a client the
// caller already built for the admission-policy sweep. Splitting the two apart
// here keeps that path from having to write the manifest to a temp file just to
// hand it back to a reader.

// ParseManifest splits a multi-document YAML stream into unstructured objects,
// skipping empty documents (a leading licence-comment block is one).
func ParseManifest(raw []byte) ([]*unstructured.Unstructured, error) {
	return parseYAMLStream(bytes.NewReader(raw))
}

// ApplyObjects server-side-applies every object through dc, resolving each
// Kind to its resource via mapper. force sets --force-conflicts.
//
// Returns on the FIRST failure rather than continuing. A partially-applied CRD
// bundle is not a usable outcome — the objects that did land make the cluster
// look further along than it is — so the caller gets to decide whether to retry
// the whole thing.
func ApplyObjects(ctx context.Context, dc dynamic.Interface, mapper meta.RESTMapper, objs []*unstructured.Unstructured, fieldManager string, force bool, logw io.Writer) error {
	for _, obj := range objs {
		gvk := obj.GroupVersionKind()
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", gvk, err)
		}
		var ri dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			ri = dc.Resource(mapping.Resource).Namespace(obj.GetNamespace())
		} else {
			ri = dc.Resource(mapping.Resource)
		}
		if _, err := ri.Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{FieldManager: fieldManager, Force: force}); err != nil {
			return fmt.Errorf("server-side apply %s %s: %w", gvk.Kind, obj.GetName(), err)
		}
		if logw != nil {
			fmt.Fprintf(logw, "  ✓ %s/%s applied\n", gvk.Kind, obj.GetName())
		}
	}
	return nil
}

// DynamicAndMapperFromKubeconfigBytes builds both a dynamic client and a
// discovery-backed REST mapper from raw kubeconfig bytes — what an apply needs
// when it holds objects rather than an explicit GroupVersionResource, and holds
// the kubeconfig only in memory (a freshly-fetched admin kubeconfig).
func DynamicAndMapperFromKubeconfigBytes(b []byte) (dynamic.Interface, meta.RESTMapper, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(b)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("creating dynamic client: %w", err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("creating discovery client: %w", err)
	}
	return dc, restmapper.NewDeferredDiscoveryRESTMapper(memCacheClient{disc}), nil
}
