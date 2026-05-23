package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/k8s"
	"github.com/JLCode-tech/awsbnkctl/pkg/bnk"
)

var (
	flagBnkResyncNamespace    string
	flagBnkResyncAllInNS      bool
	flagBnkResyncGatewayClass string
	flagBnkResyncDryRun       bool
	flagBnkResyncKubeconfig   string
)

var bnkCmd = &cobra.Command{
	Use:   "bnk",
	Short: "BNK runtime helpers (resync pool members, show pool state, …)",
	Long: `awsbnkctl bnk provides runtime helpers for BNK (F5 BIG-IP Next for Kubernetes)
cluster management.

Subcommands:
  resync   Force the F5 cne-controller to re-resolve stale TMM pool members`,
}

var bnkResyncCmd = &cobra.Command{
	Use:   "resync [httproute-name]",
	Short: "Force F5 cne-controller to re-resolve stale TMM pool members",
	Long: `awsbnkctl bnk resync forces the F5 cne-controller to re-push pool members
for the targeted HTTPRoute(s) by toggling each backendRef weight ±1.

This is necessary because the cne-controller only reconciles pool members at
HTTPRoute spec-reconcile time — it does NOT watch EndpointSlice changes. When
a backend pod is rescheduled, TMM retains the old pod IP and returns HTTP 500.

Target selection (exactly one required):
  httproute-name -n <namespace>   resync a single HTTPRoute
  --all-in-ns <namespace>         resync every HTTPRoute in the namespace
  --gateway-class <name>          resync HTTPRoutes whose parent Gateway uses
                                  gatewayClassName: <name> (all namespaces)

Live-validated on syd-tracer 2026-05-23 — controller logs
"GatewayReconciler: handling http route update" within 5s of the first patch.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runBnkResync,
}

func init() {
	bnkResyncCmd.Flags().StringVarP(&flagBnkResyncNamespace, "namespace", "n", "", "namespace of the HTTPRoute (required with a route name or --all-in-ns)")
	bnkResyncCmd.Flags().BoolVar(&flagBnkResyncAllInNS, "all-in-ns", false, "resync every HTTPRoute in the namespace given by -n")
	bnkResyncCmd.Flags().StringVar(&flagBnkResyncGatewayClass, "gateway-class", "", "resync HTTPRoutes whose parent Gateway uses this gatewayClassName (all namespaces)")
	bnkResyncCmd.Flags().BoolVar(&flagBnkResyncDryRun, "dry-run", false, "print what would be patched without making any API writes")
	bnkResyncCmd.Flags().StringVar(&flagBnkResyncKubeconfig, "kubeconfig", "", "path to kubeconfig (default: standard kubectl lookup: $KUBECONFIG → ~/.kube/config)")

	bnkCmd.AddCommand(bnkResyncCmd)
	rootCmd.AddCommand(bnkCmd)
}

func runBnkResync(cmd *cobra.Command, args []string) error {
	opts, err := resolveBnkResyncOpts(args)
	if err != nil {
		return err
	}

	dyn, err := k8s.BuildDynamicClient(flagBnkResyncKubeconfig)
	if err != nil {
		return fmt.Errorf("building kube client: %w", err)
	}

	result, runErr := bnk.ResyncHTTPRoutes(cmd.Context(), dyn, opts)

	if flagOutput == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			return err
		}
	}

	if runErr != nil {
		return runErr
	}
	return nil
}

// resolveBnkResyncOpts validates flags and positional args, returning a
// ResyncOptions or an error with a user-friendly message.
func resolveBnkResyncOpts(args []string) (bnk.ResyncOptions, error) {
	opts := bnk.ResyncOptions{
		DryRun: flagBnkResyncDryRun,
	}

	hasName := len(args) == 1
	hasAllInNS := flagBnkResyncAllInNS
	hasGWClass := flagBnkResyncGatewayClass != ""

	// Exactly one target selector must be active.
	selectors := 0
	if hasName {
		selectors++
	}
	if hasAllInNS {
		selectors++
	}
	if hasGWClass {
		selectors++
	}
	if selectors == 0 {
		return opts, fmt.Errorf("target required: provide a route name, --all-in-ns, or --gateway-class")
	}
	if selectors > 1 {
		return opts, fmt.Errorf("only one target selector may be used at a time (route name, --all-in-ns, or --gateway-class)")
	}

	switch {
	case hasName:
		if flagBnkResyncNamespace == "" {
			return opts, fmt.Errorf("-n / --namespace is required when providing a route name")
		}
		opts.Namespace = flagBnkResyncNamespace
		opts.Name = args[0]

	case hasAllInNS:
		if flagBnkResyncNamespace == "" {
			return opts, fmt.Errorf("-n / --namespace is required with --all-in-ns")
		}
		opts.Namespace = flagBnkResyncNamespace
		opts.AllInNamespace = true

	case hasGWClass:
		opts.GatewayClass = flagBnkResyncGatewayClass
	}

	return opts, nil
}
