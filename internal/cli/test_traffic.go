package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"

	// Side-effect import: ensures http-routing-e2e is registered.
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/httproutee2e"
)

var (
	flagTrafficConfig     string
	flagTrafficVIP        string
	flagTrafficIterations int
	flagTrafficTimeout    time.Duration
)

var testTrafficCmd = &cobra.Command{
	Use:   "traffic",
	Short: "Drive HTTP traffic through TMM from the test jumphost (alias for `scenarios run http-routing-e2e`)",
	Long: `awsbnkctl test traffic is an alias for:

  awsbnkctl scenarios run http-routing-e2e --config <config> [flags]

It exercises the BNK data plane end-to-end:
  1. Renders 5 manifests (Namespace, F5BnkGateway, nginx, Gateway, HTTPRoute).
  2. Applies via SSA (live RESTMapper).
  3. Waits for control-plane conditions (nginx Available, Gateway Programmed,
     HTTPRoute Accepted + ResolvedRefs).
  4. Calls pkg/bnk.ResyncHTTPRoutes (idempotent pool-member refresh).
  5. Pushes an ephemeral SSH key via EC2 Instance Connect.
  6. Opens an EICE tunnel and curls --interface <JUMPHOST_BNK_EXT_ENI_IP>
     http://<VIP>/ N times.
  7. Reports HTTP code distribution.

Reads JUMPHOST_* keys from state.env (provisioned by Phase 17b).
If the BNK pool member is stale, the ResyncHTTPRoutes call in step 4 handles it.

Exit 0 when every probe returns HTTP 200, non-zero on any miss.`,
	RunE: runTestTrafficCmd,
}

func init() {
	testTrafficCmd.Flags().StringVarP(&flagTrafficConfig, "config", "f", "", "path to cluster.yaml (required; state.env path is derived from it)")
	testTrafficCmd.Flags().StringVar(&flagTrafficVIP, "vip", "", "Gateway VIP to curl (default: <BNK_EXT_CIDR>.100 derived from cluster.yaml)")
	testTrafficCmd.Flags().IntVar(&flagTrafficIterations, "iterations", 5, "number of curl iterations against the VIP")
	testTrafficCmd.Flags().DurationVar(&flagTrafficTimeout, "timeout", 10*time.Second, "per-curl timeout")

	_ = testTrafficCmd.MarkFlagRequired("config")
	testCmd.AddCommand(testTrafficCmd)
}

// runTestTrafficCmd is an alias for `scenarios run http-routing-e2e`.
// It builds a scenarios.Context from the traffic-specific flags and delegates
// to the registered scenario, preserving the existing flag surface exactly.
func runTestTrafficCmd(cmd *cobra.Command, _ []string) error {
	cl, err := intent.Load(flagTrafficConfig)
	if err != nil {
		return fmt.Errorf("loading cluster.yaml: %w", err)
	}
	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("loading state.env: %w", err)
	}

	instanceID := st.Get("JUMPHOST_INSTANCE_ID")
	sourceIP := st.Get("JUMPHOST_BNK_EXT_ENI_IP")
	if !flagTestDryRun && (instanceID == "" || sourceIP == "") {
		return fmt.Errorf("jumphost not provisioned (JUMPHOST_INSTANCE_ID / JUMPHOST_BNK_EXT_ENI_IP missing from %s/state.env). "+
			"Enable testing.jumphost.enabled in cluster.yaml and run `awsbnkctl up` first", cl.StateDir())
	}

	kubeconfigPath := cl.StateDir() + "/kubeconfig"
	opts := map[string]string{
		"iterations": fmt.Sprintf("%d", flagTrafficIterations),
		"timeout":    flagTrafficTimeout.String(),
	}
	if flagTrafficVIP != "" {
		opts["vip"] = flagTrafficVIP
	}

	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		return fmt.Errorf("internal: http-routing-e2e scenario not registered")
	}

	sctx, err := scenarios.NewContext(
		cmd.Context(),
		kubeconfigPath,
		cl,
		st,
		os.Stderr,
		flagTestDryRun,
		opts,
	)
	if err != nil {
		// In dry-run mode we may not have a kubeconfig — that's OK,
		// we just need to render the plan.
		if !flagTestDryRun {
			return fmt.Errorf("building scenario context: %w", err)
		}
		// Dry-run fallback: print the plan manually without a live k8s context.
		vip := flagTrafficVIP
		if vip == "" {
			if v, e := cl.DefaultVIP(); e == nil {
				vip = v
			} else {
				vip = "(unknown — pass --vip)"
			}
		}
		fmt.Fprintf(os.Stderr, "→ traffic dry-run plan:\n")
		fmt.Fprintf(os.Stderr, "  cluster:       %s\n", cl.Metadata.Name)
		fmt.Fprintf(os.Stderr, "  jumphost:      %s (source-ip %s)\n", instanceID, sourceIP)
		fmt.Fprintf(os.Stderr, "  vip:           %s\n", vip)
		fmt.Fprintf(os.Stderr, "  curl:          %d × curl --interface %s --max-time %s http://%s/\n",
			flagTrafficIterations, sourceIP, flagTrafficTimeout, vip)
		return nil
	}

	sctx.Verbose = flagVerbose

	if flagTestDryRun {
		// Render manifests + print dry-run plan via the scenario framework.
		vip := flagTrafficVIP
		if vip == "" {
			if v, e := cl.DefaultVIP(); e == nil {
				vip = v
			}
		}
		fmt.Fprintf(os.Stderr, "→ traffic dry-run plan:\n")
		fmt.Fprintf(os.Stderr, "  cluster:       %s\n", cl.Metadata.Name)
		fmt.Fprintf(os.Stderr, "  jumphost:      %s (source-ip %s)\n", instanceID, sourceIP)
		fmt.Fprintf(os.Stderr, "  vip:           %s\n", vip)
		fmt.Fprintf(os.Stderr, "  curl:          %d × curl --interface %s --max-time %s http://%s/\n",
			flagTrafficIterations, sourceIP, flagTrafficTimeout, vip)
		// Also run the scenario in dry-run mode to render manifests.
		result := scenarios.Run(sctx, s)
		_ = result
		return nil
	}

	result := scenarios.Run(sctx, s)
	if result.EnvDiagram != "" {
		fmt.Fprintln(os.Stderr, result.EnvDiagram)
	}
	if !result.AllPassed() {
		os.Exit(1)
	}
	return nil
}
