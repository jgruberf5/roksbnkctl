package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/flp"
)

var (
	flagPostrenderStorageClass    string
	flagPostrenderNodePortCluster bool
)

// flpPostrenderCmd is the helm POST-RENDERER for the f5-license-proxy chart.
// It is not a user-facing command: terraform points helm's `postrender
// { binary_path }` at this binary, helm pipes the rendered manifests to stdin
// and installs whatever comes back on stdout. Hidden, because running it by hand
// does nothing useful.
//
// It exists as a command so that the FLP chart fix-ups need no interpreter on the
// host. They used to live in a generated python script, which made python3 an
// undeclared runtime dependency of `flp up` — invisible on a laptop that happens
// to have python, fatal in the tools-runner container, which has none.
var flpPostrenderCmd = &cobra.Command{
	Use:    "postrender",
	Short:  "helm post-renderer for the f5-license-proxy chart (internal)",
	Hidden: true,
	Long: `Reads a rendered helm manifest stream on stdin, applies the fix-ups the
f5-license-proxy chart needs to install on ROKS, and writes the result to stdout.

helm invokes this; you do not. It is wired in by the FLP terraform module as the
chart's post-renderer.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runFLPPostrender,
}

func init() {
	flpPostrenderCmd.Flags().StringVar(&flagPostrenderStorageClass, "storage-class", "",
		"StorageClass the chart's PVCs are repointed at (its hostPath PVs are dropped)")
	flpPostrenderCmd.Flags().BoolVar(&flagPostrenderNodePortCluster, "node-port-cluster", false,
		"rewrite the Service's externalTrafficPolicy from Local to Cluster, so every worker answers on the NodePort")
	flpCmd.AddCommand(flpPostrenderCmd)
}

func runFLPPostrender(_ *cobra.Command, _ []string) error {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading rendered manifests from stdin: %w", err)
	}
	out := flp.Render(in, flp.Options{
		StorageClass:    flagPostrenderStorageClass,
		NodePortCluster: flagPostrenderNodePortCluster,
	})
	if _, err := os.Stdout.Write(out); err != nil {
		return fmt.Errorf("writing post-rendered manifests to stdout: %w", err)
	}
	return nil
}
