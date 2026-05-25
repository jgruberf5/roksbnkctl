package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"

	// Side-effect import: registers http-routing-e2e in init().
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/httproutee2e"
	// Side-effect import: registers http-traffic-split in init().
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/httptrafficsplit"
	// Side-effect import: registers external-resource-pool in init().
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/externalresourcepool"
	// Side-effect import: registers proxy-protocol-l4 in init().
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/proxyprotocoll4"
	// Side-effect import: registers ai-token-counting in init().
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/aitokencounting"
	// Side-effect import: registers ai-semantic-cache in init().
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/aisemanticcache"
	// Side-effect import: registers multi-vip in init().
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/multivip"
)

// topoSort returns the registered scenarios in dependency order using Kahn's
// algorithm. Returns an error if the dependency graph contains a cycle.
func topoSort(all []scenarios.Scenario) ([]scenarios.Scenario, error) {
	// Build name→scenario index and in-degree count.
	byName := make(map[string]scenarios.Scenario, len(all))
	for _, s := range all {
		byName[s.Name()] = s
	}

	inDegree := make(map[string]int, len(all))
	for _, s := range all {
		if _, seen := inDegree[s.Name()]; !seen {
			inDegree[s.Name()] = 0
		}
		for _, dep := range s.Dependencies() {
			inDegree[s.Name()]++
			// Ensure dep is tracked even if it has no deps of its own.
			if _, seen := inDegree[dep]; !seen {
				inDegree[dep] = 0
			}
		}
	}

	// Queue: all nodes with no incoming edges.
	var queue []string
	for _, s := range all {
		if inDegree[s.Name()] == 0 {
			queue = append(queue, s.Name())
		}
	}

	var sorted []scenarios.Scenario
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		s, ok := byName[name]
		if !ok {
			// Dependency references an unregistered scenario; skip.
			continue
		}
		sorted = append(sorted, s)
		// For every scenario that depends on this one, reduce its in-degree.
		for _, candidate := range all {
			for _, dep := range candidate.Dependencies() {
				if dep == name {
					inDegree[candidate.Name()]--
					if inDegree[candidate.Name()] == 0 {
						queue = append(queue, candidate.Name())
					}
				}
			}
		}
	}

	// Any scenario not yet in sorted has unresolved deps — cycle.
	if len(sorted) < len(all) {
		var unresolved []string
		inSorted := make(map[string]bool, len(sorted))
		for _, s := range sorted {
			inSorted[s.Name()] = true
		}
		for _, s := range all {
			if !inSorted[s.Name()] {
				unresolved = append(unresolved, s.Name())
			}
		}
		return nil, fmt.Errorf("scenarios run --all: dep cycle among %v", unresolved)
	}

	return sorted, nil
}

// runAllScenarios runs every registered scenario in topo-sorted dependency order.
func runAllScenarios(cmd *cobra.Command, cl *intent.Cluster, st *state.State) error {
	all := scenarios.All()
	if len(all) == 0 {
		fmt.Fprintln(os.Stderr, "[scenarios] no scenarios registered")
		return nil
	}

	ordered, err := topoSort(all)
	if err != nil {
		return err
	}

	kubeconfigPath := cl.StateDir() + "/kubeconfig"
	opts := make(map[string]string)
	if flagScenarioVIP != "" {
		opts["vip"] = flagScenarioVIP
	}

	sctx, err := scenarios.NewContext(
		cmd.Context(),
		kubeconfigPath,
		cl,
		st,
		os.Stderr,
		flagScenarioDryRun,
		opts,
	)
	if err != nil {
		return fmt.Errorf("building scenario context: %w", err)
	}
	sctx.ReportStamp = time.Now().UTC().Format("2006-01-02T15-04-05Z")
	sctx.Verbose = flagVerbose

	var failed []string
	for _, s := range ordered {
		fmt.Fprintf(os.Stderr, "[scenarios] running %s\n", s.Name())
		result := scenarios.Run(sctx, s)
		if result.EnvDiagram != "" {
			fmt.Fprintln(os.Stderr, result.EnvDiagram)
		}
		if !result.AllPassed() {
			fmt.Fprintf(os.Stderr, "[scenarios] FAIL %s: %s\n", s.Name(), result.Summary)
			failed = append(failed, s.Name())
			break
		}
		fmt.Fprintf(os.Stderr, "[scenarios] PASS %s\n", s.Name())
	}

	if len(failed) > 0 {
		return fmt.Errorf("scenarios run --all: scenario %q failed", failed[0])
	}
	fmt.Fprintf(os.Stderr, "[scenarios] all %d scenarios green\n", len(ordered))
	return nil
}

var (
	flagScenarioConfig string
	flagScenarioVIP    string
	flagScenarioDryRun bool
	flagScenarioAll    bool
)

var scenariosCmd = &cobra.Command{
	Use:   "scenarios",
	Short: "List, run, or clean BNK end-to-end validation scenarios",
	Long: `awsbnkctl scenarios manages the catalogue of end-to-end BNK validation scenarios.

Each scenario applies manifests, asserts control-plane conditions, drives
real traffic through the data plane, and emits a JSON report + ASCII env diagram.

Subcommands:
  list           Print registered scenarios with name, rating, and description
  run <name>     Run one scenario (or --all for every registered scenario)
  clean <name>   Invoke the scenario's Cleanup hook`,
}

var scenariosListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print registered scenarios",
	RunE: func(cmd *cobra.Command, _ []string) error {
		all := scenarios.All()
		if len(all) == 0 {
			fmt.Fprintln(os.Stderr, "no scenarios registered")
			return nil
		}
		fmt.Fprintf(os.Stdout, "%-30s  %-6s  %s\n", "NAME", "RATING", "TITLE")
		fmt.Fprintf(os.Stdout, "%-30s  %-6s  %s\n", "----", "------", "-----")
		for _, s := range all {
			fmt.Fprintf(os.Stdout, "%-30s  %-6s  %s\n", s.Name(), string(s.Rating()), s.Title())
		}
		return nil
	},
}

var scenariosRunCmd = &cobra.Command{
	Use:   "run [name]",
	Short: "Run a scenario (or --all)",
	Long: `awsbnkctl scenarios run <name> runs one registered scenario:
  1. Renders manifests into <workspace>/artifacts/scenarios/<name>/
  2. Applies manifests via SSA (live RESTMapper)
  3. Verifies control-plane conditions + data-plane traffic
  4. Emits a JSON report + ASCII env diagram

Use --dry-run to render manifests only, without touching the cluster.
Use --all to run every registered scenario in topo-sorted dependency order.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runScenariosRunCmd,
}

var scenariosCleanCmd = &cobra.Command{
	Use:   "clean <name>",
	Short: "Invoke a scenario's Cleanup hook",
	Args:  cobra.ExactArgs(1),
	RunE:  runScenariosCleanCmd,
}

func init() {
	scenariosRunCmd.Flags().StringVar(&flagScenarioConfig, "config", "", "path to cluster.yaml (required)")
	scenariosRunCmd.Flags().StringVar(&flagScenarioVIP, "vip", "", "Gateway VIP to use (default: derived from cluster.yaml)")
	scenariosRunCmd.Flags().BoolVar(&flagScenarioDryRun, "dry-run", false, "render manifests only; do not apply or verify")
	scenariosRunCmd.Flags().BoolVar(&flagScenarioAll, "all", false, "run every registered scenario in topo-sorted order")
	_ = scenariosRunCmd.MarkFlagRequired("config")

	scenariosCleanCmd.Flags().StringVar(&flagScenarioConfig, "config", "", "path to cluster.yaml (required)")
	_ = scenariosCleanCmd.MarkFlagRequired("config")

	scenariosCmd.AddCommand(scenariosListCmd, scenariosRunCmd, scenariosCleanCmd)
	rootCmd.AddCommand(scenariosCmd)
}

func runScenariosRunCmd(cmd *cobra.Command, args []string) error {
	if flagScenarioAll {
		cl, err := intent.Load(flagScenarioConfig)
		if err != nil {
			return fmt.Errorf("loading cluster.yaml: %w", err)
		}
		st, err := state.Load(cl.StateDir())
		if err != nil {
			return fmt.Errorf("loading state.env: %w", err)
		}
		return runAllScenarios(cmd, cl, st)
	}

	if len(args) == 0 {
		return fmt.Errorf("usage: awsbnkctl scenarios run <name> [--dry-run]")
	}
	name := args[0]
	s := scenarios.Find(name)
	if s == nil {
		return fmt.Errorf("scenario %q not found (use `awsbnkctl scenarios list` to see registered scenarios)", name)
	}

	cl, err := intent.Load(flagScenarioConfig)
	if err != nil {
		return fmt.Errorf("loading cluster.yaml: %w", err)
	}
	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("loading state.env: %w", err)
	}

	kubeconfigPath := cl.StateDir() + "/kubeconfig"
	opts := make(map[string]string)
	if flagScenarioVIP != "" {
		opts["vip"] = flagScenarioVIP
	}

	sctx, err := scenarios.NewContext(
		cmd.Context(),
		kubeconfigPath,
		cl,
		st,
		os.Stderr,
		flagScenarioDryRun,
		opts,
	)
	if err != nil {
		return fmt.Errorf("building scenario context: %w", err)
	}
	sctx.ReportStamp = time.Now().UTC().Format("2006-01-02T15-04-05Z")
	sctx.Verbose = flagVerbose

	result := scenarios.Run(sctx, s)

	// Print env diagram to stderr.
	if result.EnvDiagram != "" {
		fmt.Fprintln(os.Stderr, result.EnvDiagram)
	}

	if !result.AllPassed() {
		os.Exit(1)
	}
	return nil
}

func runScenariosCleanCmd(cmd *cobra.Command, args []string) error {
	name := args[0]
	s := scenarios.Find(name)
	if s == nil {
		return fmt.Errorf("scenario %q not found", name)
	}

	cl, err := intent.Load(flagScenarioConfig)
	if err != nil {
		return fmt.Errorf("loading cluster.yaml: %w", err)
	}
	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("loading state.env: %w", err)
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
		return fmt.Errorf("building scenario context: %w", err)
	}

	return scenarios.Cleanup(sctx, s)
}
