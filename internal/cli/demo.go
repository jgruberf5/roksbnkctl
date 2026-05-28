package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	// Side-effect imports: each use-case package registers into demo.registry via init().
	// Alphabetical; one line per use-case as new slices ship.
	_ "github.com/JLCode-tech/awsbnkctl/internal/demo/diameter" // registers "diameter" use-case via init()
	_ "github.com/JLCode-tech/awsbnkctl/internal/demo/http2"    // registers "http2" use-case via init()
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	"github.com/JLCode-tech/awsbnkctl/pkg/bnk"
)

// flagDemo* are the demo subcommand's OWN package-level flag variables.
// NEVER share these with scenarios.go's flagScenario* vars — same package cli,
// binding two cobra commands to the same var address is the cobra flag-var
// poisoning bug (see project_slice01_real_account_validation.md).
var (
	flagDemoConfig string
	flagDemoDryRun bool
	flagDemoAll    bool
)

// demoResyncFn is the injectable seam for ResyncHTTPRoutes. Tests replace this
// with a no-op or a recording stub to prove "auto-resync is called after ok"
// without needing a live cluster (scenarios.NewContext requires a real kubeconfig).
var demoResyncFn = func(ctx context.Context, dyn dynamic.Interface, opts bnk.ResyncOptions) (bnk.ResyncResult, error) {
	return bnk.ResyncHTTPRoutes(ctx, dyn, opts)
}

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "List, run, or clean BNK protocol demo use-cases",
	Long: `awsbnkctl demo manages the catalogue of BNK protocol demo use-cases.

Demo use-cases are curated operator walkthroughs (HTTP/2, Diameter, etc.) that
run against a demo cluster provisioned with ` + "`" + `awsbnkctl up --demo` + "`" + `.

Subcommands:
  list           Print registered demo use-cases (name, rating, title, description)
  run <name>     Run one use-case (or --all); requires a demo cluster
  clean <name>   Invoke a use-case's idempotent Cleanup hook`,
}

var demoListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print registered demo use-cases and Green scenarios",
	RunE: func(cmd *cobra.Command, _ []string) error {
		all := demo.Catalogue()
		if len(all) == 0 {
			fmt.Fprintln(os.Stdout, "no demo use-cases registered")
			return nil
		}
		fmt.Fprintf(os.Stdout, "%-30s  %-8s  %-6s  %-30s  %s\n", "NAME", "KIND", "RATING", "TITLE", "DESCRIPTION")
		fmt.Fprintf(os.Stdout, "%-30s  %-8s  %-6s  %-30s  %s\n", "----", "----", "------", "-----", "-----------")
		for _, s := range all {
			kind := "scenario"
			if demo.IsDemoEntry(s) {
				kind = "demo"
			}
			fmt.Fprintf(os.Stdout, "%-30s  %-8s  %-6s  %-30s  %s\n",
				s.Name(), kind, string(s.Rating()), s.Title(), s.Description())
		}
		return nil
	},
}

var demoRunCmd = &cobra.Command{
	Use:   "run [name]",
	Short: "Run a demo use-case (or --all); requires a demo cluster",
	Long: `awsbnkctl demo run <name> runs one registered demo use-case:
  1. Checks DEMO_MODE=true in cluster state (refuses non-demo clusters)
  2. Narrates intent, renders + applies manifests, verifies data-plane
  3. Auto-resyncs pool members after a successful apply (no manual bnk resync)
  4. Prints the ASCII environment diagram

Use --dry-run to render manifests only, without touching the cluster.
Use --all to run every registered use-case in topo-sorted dependency order.

A demo cluster is provisioned with: awsbnkctl up --demo`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDemoRunCmd,
}

var demoCleanCmd = &cobra.Command{
	Use:   "clean [name]",
	Short: "Invoke a demo use-case's idempotent Cleanup hook (or --all)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDemoCleanCmd,
}

func init() {
	demoRunCmd.Flags().StringVarP(&flagDemoConfig, "config", "f", "", "path to cluster.yaml (required)")
	demoRunCmd.Flags().BoolVar(&flagDemoDryRun, "dry-run", false, "render manifests only; do not apply or verify")
	demoRunCmd.Flags().BoolVar(&flagDemoAll, "all", false, "run every registered demo use-case in topo-sorted order")
	_ = demoRunCmd.MarkFlagRequired("config")

	demoCleanCmd.Flags().StringVarP(&flagDemoConfig, "config", "f", "", "path to cluster.yaml (required)")
	demoCleanCmd.Flags().BoolVar(&flagDemoAll, "all", false, "invoke Cleanup for every registered demo use-case")
	_ = demoCleanCmd.MarkFlagRequired("config")

	demoCmd.AddCommand(demoListCmd, demoRunCmd, demoCleanCmd)
	rootCmd.AddCommand(demoCmd)
}

// shouldResync reports whether the auto-resync step should fire for a given
// result. The logic lives here (not inline in runDemoRunCmd) so tests can
// exercise the production gate directly — changes to the condition are caught
// by TestDemoRun_ResyncCalledOnOkResult without duplicating the logic there.
func shouldResync(dryRun bool, status string) bool {
	return !dryRun && status == "ok"
}

// runDemoRunCmd implements `demo run [name] | --all`.
func runDemoRunCmd(cmd *cobra.Command, args []string) error {
	cl, err := intent.Load(flagDemoConfig)
	if err != nil {
		return fmt.Errorf("loading cluster.yaml: %w", err)
	}
	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// AC #5 — refuse if this is not a demo cluster.
	if st.Get("DEMO_MODE") != "true" {
		fmt.Fprintln(os.Stderr, "error: demo run requires a demo cluster — run `awsbnkctl up --demo` first")
		os.Exit(1)
	}

	var useCases []scenarios.Scenario
	if flagDemoAll {
		all := demo.Catalogue()
		if len(all) == 0 {
			fmt.Fprintln(os.Stderr, "[demo] no use-cases registered")
			return nil
		}
		ordered, err := topoSort(all)
		if err != nil {
			return err
		}
		useCases = ordered
	} else {
		if len(args) == 0 {
			return fmt.Errorf("usage: awsbnkctl demo run <name> [--dry-run] or --all")
		}
		name := args[0]
		s := demo.FindInCatalogue(name)
		if s == nil {
			return fmt.Errorf("use-case %q not found in demo catalogue (try `awsbnkctl demo list` to see registered demos and Green scenarios)", name)
		}
		useCases = []scenarios.Scenario{s}
	}

	kubeconfigPath := cl.StateDir() + "/kubeconfig"
	sctx, err := scenarios.NewContext(
		cmd.Context(),
		kubeconfigPath,
		cl,
		st,
		os.Stderr,
		flagDemoDryRun,
		nil,
	)
	if err != nil {
		return fmt.Errorf("building demo context: %w", err)
	}
	sctx.ReportStamp = time.Now().UTC().Format("2006-01-02T15-04-05Z")
	sctx.Verbose = flagVerbose

	var failed []string
	for _, s := range useCases {
		demo.Intent(os.Stderr, s.Name(), s.Description())
		result := scenarios.Run(sctx, s)
		demo.Proof(os.Stderr, s.Name(), result)

		// Auto-resync pool members after a successful apply — built in so the
		// operator never needs to run `bnk resync` by hand.
		// shouldResync encapsulates the gate; tests call it directly so a
		// condition change here is caught without duplicating the logic in tests.
		if shouldResync(flagDemoDryRun, result.Status) {
			if _, resyncErr := demoResyncFn(sctx.Ctx, sctx.Dynamic, bnk.ResyncOptions{
				Namespace:      s.Namespace(sctx),
				AllInNamespace: true,
			}); resyncErr != nil {
				// Warn but do not hard-fail — proof already printed; exit code
				// is driven by result.AllPassed() below.
				fmt.Fprintf(os.Stderr, "[demo] warn: resync: %v\n", resyncErr)
			}
		}

		if !result.AllPassed() {
			failed = append(failed, s.Name())
			break
		}
	}

	if len(failed) > 0 {
		os.Exit(1)
	}
	return nil
}

// cleanAllUseCases invokes Cleanup on every use-case in the provided slice.
// Per-use-case errors are logged and collected; an aggregate error is returned
// if any Cleanup failed. This is extracted from runDemoCleanCmd so tests can
// drive the --all enumeration logic without a real kubeconfig.
func cleanAllUseCases(sctx *scenarios.Context, useCases []scenarios.Scenario) error {
	var errs []string
	for _, s := range useCases {
		if cleanErr := scenarios.Cleanup(sctx, s); cleanErr != nil {
			fmt.Fprintf(os.Stderr, "[demo] clean %s: %v\n", s.Name(), cleanErr)
			errs = append(errs, s.Name()+": "+cleanErr.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("demo clean --all: %d use-case(s) failed cleanup", len(errs))
	}
	return nil
}

// runDemoCleanCmd implements `demo clean [name] | --all`.
func runDemoCleanCmd(cmd *cobra.Command, args []string) error {
	// Require either a positional name or --all.
	if !flagDemoAll && len(args) == 0 {
		return fmt.Errorf("usage: awsbnkctl demo clean <name> or --all")
	}

	cl, err := intent.Load(flagDemoConfig)
	if err != nil {
		return fmt.Errorf("loading cluster.yaml: %w", err)
	}
	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	kubeconfigPath := cl.StateDir() + "/kubeconfig"
	sctx, err := scenarios.NewContext(
		cmd.Context(),
		kubeconfigPath,
		cl,
		st,
		os.Stderr,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("building demo context: %w", err)
	}

	if flagDemoAll {
		return cleanAllUseCases(sctx, demo.Catalogue())
	}

	// Single named use-case.
	name := args[0]
	s := demo.FindInCatalogue(name)
	if s == nil {
		return fmt.Errorf("use-case %q not found in demo catalogue (try `awsbnkctl demo list` to see registered demos and Green scenarios)", name)
	}
	return scenarios.Cleanup(sctx, s)
}
