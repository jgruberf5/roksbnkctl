package k8s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

// FieldManager is the field-manager string used for server-side apply
// operations. Distinct from kubectl's "kubectl-client-side-apply" /
// "kubectl-server-side-apply" so an SSA conflict tells the user
// roksbnkctl owns the field.
const FieldManager = "roksbnkctl"

// ApplyOptions captures the flag-parsed inputs to `roksbnkctl k apply`.
//
// Filename can be a YAML file, a directory (recursive *.yaml; or
// kustomization.yaml-detected and built via krusty), or "-" for stdin.
// Force toggles SSA's force-conflicts flag.
type ApplyOptions struct {
	Filename       string
	Namespace      string
	Force          bool
	KubeconfigPath string

	IOStreams genericiooptions.IOStreams
}

// Run resolves Filename to a slice of unstructured objects and SSA-
// applies each via the dynamic client. Field-manager is hardcoded to
// FieldManager.
func (o *ApplyOptions) Run(ctx context.Context) error {
	if o.Filename == "" {
		return errors.New("`-f` filename is required (file, directory, or `-` for stdin)")
	}
	if o.IOStreams.Out == nil {
		o.IOStreams.Out = io.Discard
	}

	cfg, err := BuildRESTConfig(o.KubeconfigPath)
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("dynamic client: %w", err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return fmt.Errorf("discovery client: %w", err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memCacheClient{disc})

	objs, err := o.loadObjects()
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return errors.New("no resources found in input")
	}

	for _, obj := range objs {
		if err := o.applyOne(ctx, dyn, mapper, obj); err != nil {
			return err
		}
	}
	return nil
}

// loadObjects materialises the user's -f input into a slice of
// unstructured. Three modes:
//
//  1. "-": read stdin as a YAML stream
//  2. directory containing kustomization.yaml: build via krusty
//  3. directory: recurse all *.yaml / *.yml files
//  4. plain file: parse as a YAML stream
func (o *ApplyOptions) loadObjects() ([]*unstructured.Unstructured, error) {
	if o.Filename == "-" {
		return parseYAMLStream(os.Stdin)
	}
	st, err := os.Stat(o.Filename)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", o.Filename, err)
	}
	if !st.IsDir() {
		f, err := os.Open(o.Filename)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return parseYAMLStream(f)
	}
	// Directory: prefer kustomize if a kustomization.yaml is present.
	for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		if _, err := os.Stat(filepath.Join(o.Filename, name)); err == nil {
			return loadKustomization(o.Filename)
		}
	}
	// Plain directory: recurse all *.yaml/*.yml.
	var out []*unstructured.Unstructured
	walkErr := filepath.WalkDir(o.Filename, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		objs, err := parseYAMLStream(f)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, objs...)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// loadKustomization builds a kustomization base via the krusty API,
// matching `kubectl apply -k <dir>` semantics. Returns the resulting
// resources as a slice of unstructured.
func loadKustomization(path string) ([]*unstructured.Unstructured, error) {
	fs := filesys.MakeFsOnDisk()
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	rm, err := k.Run(fs, path)
	if err != nil {
		return nil, fmt.Errorf("kustomize build: %w", err)
	}
	yml, err := rm.AsYaml()
	if err != nil {
		return nil, fmt.Errorf("kustomize emit: %w", err)
	}
	return parseYAMLStream(bytes.NewReader(yml))
}

// parseYAMLStream splits a multi-document YAML reader into individual
// unstructured objects. Empty documents are skipped.
func parseYAMLStream(r io.Reader) ([]*unstructured.Unstructured, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []*unstructured.Unstructured
	for _, doc := range splitYAML(data) {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(doc, &u.Object); err != nil {
			return nil, fmt.Errorf("parsing yaml: %w", err)
		}
		if len(u.Object) == 0 {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// splitYAML splits a YAML stream on the document separator. Conservative:
// "\n---\n" only; doesn't try to handle every weird whitespace case.
// Matches the way kubectl/yaml.NewYAMLOrJSONDecoder splits streams in
// practice.
func splitYAML(data []byte) [][]byte {
	parts := bytes.Split(data, []byte("\n---\n"))
	// Handle leading "---\n" too.
	if len(parts) > 0 && bytes.HasPrefix(parts[0], []byte("---\n")) {
		parts[0] = parts[0][4:]
	}
	return parts
}

// applyOne dispatches one Unstructured to the right dynamic resource
// interface (namespaced vs cluster-scoped) and patches it via SSA.
func (o *ApplyOptions) applyOne(
	ctx context.Context,
	dyn dynamic.Interface,
	mapper meta.RESTMapper,
	obj *unstructured.Unstructured,
) error {
	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" {
		return fmt.Errorf("resource missing kind: %s", obj.GetName())
	}
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("REST mapping for %s: %w", gvk, err)
	}

	// Resolve effective namespace: object's own > flag > default.
	ns := obj.GetNamespace()
	if ns == "" {
		ns = o.Namespace
	}
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace && ns == "" {
		ns = "default"
	}
	obj.SetNamespace(ns)

	body, err := yaml.Marshal(obj.Object)
	if err != nil {
		return fmt.Errorf("marshaling %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}

	// Convert YAML → JSON; SSA Patch wants application/apply-patch+yaml
	// but the underlying transport accepts the YAML payload directly.
	var ri dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ri = dyn.Resource(mapping.Resource).Namespace(ns)
	} else {
		ri = dyn.Resource(mapping.Resource)
	}

	applied, err := ri.Patch(
		ctx,
		obj.GetName(),
		types.ApplyPatchType,
		body,
		applyPatchOptions(o.Force),
	)
	if err != nil {
		return fmt.Errorf("server-side apply %s %s/%s: %w",
			mapping.Resource.Resource, ns, obj.GetName(), err)
	}

	fmt.Fprintf(o.IOStreams.Out, "%s/%s %s\n",
		strings.ToLower(mapping.Resource.Resource),
		applied.GetName(),
		appliedVerb(applied))
	return nil
}

// appliedVerb mimics the trailing "configured"/"created"/"unchanged"
// label kubectl prints. We can't tell created vs configured purely
// from the SSA response, so we always emit "configured" — kubectl with
// --server-side does the same.
func appliedVerb(_ *unstructured.Unstructured) string {
	return "configured"
}

// applyPatchOptions returns the metav1.PatchOptions for SSA, optionally
// with force-conflicts.
func applyPatchOptions(force bool) metav1.PatchOptions {
	po := metav1.PatchOptions{
		FieldManager: FieldManager,
	}
	if force {
		t := true
		po.Force = &t
	}
	return po
}

// memCacheClient adapts a discovery.DiscoveryInterface into a
// CachedDiscoveryInterface (no actual caching — fine for one-shot CLI).
type memCacheClient struct{ discovery.DiscoveryInterface }

func (memCacheClient) Fresh() bool { return true }
func (memCacheClient) Invalidate() {}
