package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// `tfx apply` replaces the modules' `curl` server-side-apply (PUT/POST) sites. It
// reads a manifest stream on stdin (or --filename), maps each object's Kind to its
// resource via discovery, and server-side-applies it with the given field manager.

var (
	flagApplyFilename string
	flagApplyFieldMgr string
	flagApplyForce    bool
)

var tfxApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Server-side apply a manifest stream (internal)",
	Long: `Reads a (possibly multi-document) YAML/JSON manifest from stdin (default, or
--filename -) or a file, and server-side-applies each object. Object Kind is
resolved to its resource via discovery, so no --gvr is needed.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXApplyCmd,
}

func init() {
	f := tfxApplyCmd.Flags()
	f.StringVar(&flagApplyFilename, "filename", "-", "manifest file, or - for stdin")
	f.StringVar(&flagApplyFieldMgr, "field-manager", "roksbnkctl", "server-side-apply field manager")
	f.BoolVar(&flagApplyForce, "force", false, "force ownership on field-manager conflicts")
	tfxCmd.AddCommand(tfxApplyCmd)
}

func runTFXApplyCmd(cmd *cobra.Command, _ []string) error {
	var raw []byte
	var err error
	if flagApplyFilename == "" || flagApplyFilename == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(flagApplyFilename)
	}
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}
	objs, err := parseManifestStream(raw)
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return fmt.Errorf("no objects in the manifest")
	}
	dc, mapper, err := tfxDynamicAndMapper()
	if err != nil {
		return err
	}
	return runTFXApply(cmdContext(cmd), dc, mapper, objs, flagApplyFieldMgr, flagApplyForce, os.Stderr)
}

// runTFXApply server-side-applies each object, resolving Kind→resource via mapper.
func runTFXApply(ctx context.Context, dc dynamic.Interface, mapper meta.RESTMapper, objs []*unstructured.Unstructured, fieldMgr string, force bool, logw io.Writer) error {
	for _, obj := range objs {
		gvk := obj.GroupVersionKind()
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", gvk, err)
		}
		var ri dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			ns := obj.GetNamespace()
			ri = dc.Resource(mapping.Resource).Namespace(ns)
		} else {
			ri = dc.Resource(mapping.Resource)
		}
		if _, err := ri.Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{FieldManager: fieldMgr, Force: force}); err != nil {
			return fmt.Errorf("applying %s %s/%s: %w", gvk.Kind, obj.GetNamespace(), obj.GetName(), err)
		}
		fmt.Fprintf(logw, "tfx apply: %s %s/%s applied\n", gvk.Kind, obj.GetNamespace(), obj.GetName())
	}
	return nil
}

// parseManifestStream splits a multi-document YAML stream into unstructured
// objects, skipping empty documents. Accepts JSON too (a JSON object is valid
// single-document YAML).
func parseManifestStream(raw []byte) ([]*unstructured.Unstructured, error) {
	var out []*unstructured.Unstructured
	for i, doc := range bytes.Split(raw, []byte("\n---")) {
		trimmed := bytes.TrimSpace(doc)
		// Strip a leading document separator left on the first chunk.
		trimmed = bytes.TrimPrefix(trimmed, []byte("---"))
		trimmed = bytes.TrimSpace(trimmed)
		if len(trimmed) == 0 {
			continue
		}
		m := map[string]interface{}{}
		if err := yaml.Unmarshal(trimmed, &m); err != nil {
			return nil, fmt.Errorf("parsing manifest document %d: %w", i+1, err)
		}
		if len(m) == 0 {
			continue
		}
		u := &unstructured.Unstructured{Object: m}
		if u.GetKind() == "" || u.GroupVersionKind().Version == "" {
			return nil, fmt.Errorf("manifest document %d is missing apiVersion/kind", i+1)
		}
		out = append(out, u)
	}
	return out, nil
}
