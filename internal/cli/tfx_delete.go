package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

// `tfx delete` replaces the modules' 41 `curl -X DELETE ... || true` sites — the
// destroy-time cleanups (SCC bindings, clusterrolebindings, the CNEInstance, the
// admission policy/binding). Idempotent by default (--ignore-not-found), matching
// the shell `|| true`.

var (
	flagDeleteGVR           string
	flagDeleteNS            string
	flagDeleteName          string
	flagDeleteIgnoreMissing bool
)

var tfxDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a resource by GVR/name (internal)",
	Long: `Deletes one resource identified by --gvr + --name (+ --ns when namespaced).

With --ignore-not-found (the common case, replacing the modules' ` + "`curl ... || true`" + `),
a missing resource exits 0.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXDeleteCmd,
}

func init() {
	f := tfxDeleteCmd.Flags()
	f.StringVar(&flagDeleteGVR, "gvr", "", "group/version/resource (required)")
	f.StringVar(&flagDeleteNS, "ns", "", "namespace (empty = cluster-scoped)")
	f.StringVar(&flagDeleteName, "name", "", "resource name (required)")
	f.BoolVar(&flagDeleteIgnoreMissing, "ignore-not-found", false, "exit 0 when the resource is already gone")
	tfxCmd.AddCommand(tfxDeleteCmd)
}

func runTFXDeleteCmd(cmd *cobra.Command, _ []string) error {
	if flagDeleteGVR == "" || flagDeleteName == "" {
		return fmt.Errorf("--gvr and --name are required")
	}
	gvr, err := parseGVR(flagDeleteGVR)
	if err != nil {
		return err
	}
	dc, err := tfxDynamic()
	if err != nil {
		return err
	}
	ri := tfxResource(dc, gvr, flagDeleteNS)
	return runTFXDelete(cmdContext(cmd), ri, flagDeleteName, flagDeleteIgnoreMissing, os.Stderr)
}

// runTFXDelete is the testable core: delete name, optionally swallowing NotFound.
func runTFXDelete(ctx context.Context, ri dynamic.ResourceInterface, name string, ignoreMissing bool, logw io.Writer) error {
	err := ri.Delete(ctx, name, metav1.DeleteOptions{})
	switch {
	case err == nil:
		fmt.Fprintf(logw, "tfx delete: %s deleted\n", name)
		return nil
	case apierrors.IsNotFound(err) && ignoreMissing:
		fmt.Fprintf(logw, "tfx delete: %s already gone (ignored)\n", name)
		return nil
	default:
		return fmt.Errorf("deleting %s: %w", name, err)
	}
}
