package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// flagOutputJSON / flagOutputShowSensitive back the flags shared by the phase
// `output` commands and the top-level aggregate (only one runs per invocation).
var (
	flagOutputJSON          bool
	flagOutputShowSensitive bool
)

// phaseOutputOwnership maps each phase to the terraform root outputs
// (terraform/outputs.tf) that phase's apply actually MANAGES. Every phase
// applies the SAME root into its own state dir, so each phase's terraform.tfstate
// carries the full output schema — with blanks / "… not created" placeholders for
// resources that phase doesn't own. Showing the whole set per phase produced
// confusing, conflicting views (e.g. testing_* blank under `bnk output`), so each
// `<phase> output` is scoped to the keys that phase owns, and `roksbnkctl output`
// merges the owned set from every phase (disjoint, so never conflicting).
//
// Source of truth is terraform/outputs.tf (`value = module.<owner>...`):
// roks_cluster → cluster, flo → bnk, testing → testing. Gateway promotes no
// outputs to the root set. TestPhaseOutputOwnershipPartitionsRootOutputs pins
// that this map partitions every root output exactly once, so it can't drift.
var phaseOutputOwnership = map[string][]string{
	"cluster": {
		"roks_cluster_id",
		"roks_cluster_name",
		"openshift_cluster_public_endpoint",
		"openshift_cluster_private_endpoint",
		"roks_transit_gateway_name",
		"registry_cos_name",
		"registry_cos_crn",
	},
	"bnk": {
		"flo_namespace",
		"flo_utils_namespace",
		"flo_trusted_profile_id",
	},
	"testing": {
		"testing_tgw_jumphost_ip",
		"jumphost_shared_key",
		"testing_ssh_key_name",
		"testing_tgw_jumphost_ssh_command",
		"testing_cluster_jumphost_ips",
		"testing_cluster_jumphost_ssh_commands",
		"testing_tgw_jumphost_subnet_cidr",
		"testing_cluster_jumphost_subnet_cidrs",
	},
	"gateway": {
		"gateway_enabled",
		"gateway_app_namespace",
		"gateway_flo_namespace",
		"gateway_name",
		"gateway_class_name",
		"gateway_bnkgateway_name",
		"gateway_route_name",
		"gateway_backend",
		"gateway_listener_networks",
		"gateway_egress_mode",
		"gateway_snatpool_name",
		"gateway_snat_addresses",
		"gateway_egress_cr_names",
		"gateway_vxlan_port",
		"gateway_static_routes",
	},
	"flp": {
		"flp_root_ca",
		"flp_endpoint",
		"flp_namespace",
	},
}

// phaseStateDirs is the read order for the aggregate: each phase's owned outputs
// come from that phase's own state (the populated copy).
var phaseStateDirs = []struct {
	phase    string
	stateDir func(string) (string, error)
}{
	{"cluster", config.WorkspaceClusterStateDir},
	{"bnk", config.WorkspaceStateDir},
	{"testing", config.WorkspaceTestingStateDir},
	{"gateway", config.WorkspaceGatewayStateDir},
	{"flp", config.WorkspaceFLPStateDir},
}

// outputOwner returns the phase that manages output name, or "" if unowned.
func outputOwner(name string) string {
	for phase, names := range phaseOutputOwnership {
		for _, n := range names {
			if n == name {
				return phase
			}
		}
	}
	return ""
}

// ownedSubset keeps only the outputs phase manages (terraform/outputs.tf owner),
// dropping the shared-schema placeholders for resources owned by other phases.
func ownedSubset(phase string, outs map[string]config.StateOutput) map[string]config.StateOutput {
	owned := make(map[string]config.StateOutput)
	for _, name := range phaseOutputOwnership[phase] {
		if o, ok := outs[name]; ok {
			owned[name] = o
		}
	}
	return owned
}

// readPhaseOutputs reads a phase's state outputs, treating a missing tfstate
// (phase not deployed) as the empty set.
func readPhaseOutputs(workspace string, stateDir func(string) (string, error)) (map[string]config.StateOutput, error) {
	dir, err := stateDir(workspace)
	if err != nil {
		return nil, err
	}
	outs, err := config.ReadStateOutputs(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]config.StateOutput{}, nil
		}
		return nil, err
	}
	return outs, nil
}

// renderOutputs prints an output set as text (tabwriter key=value) or --json,
// redacting sensitive values unless --show-sensitive.
func renderOutputs(cmd *cobra.Command, outs map[string]config.StateOutput) error {
	w := cmd.OutOrStdout()
	m := make(map[string]any, len(outs))
	for k, o := range outs {
		if o.Sensitive && !flagOutputShowSensitive {
			m[k] = "<sensitive>"
		} else {
			m[k] = o.Value
		}
	}
	if flagOutputJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, k := range sortedKeys(m) {
		fmt.Fprintf(tw, "%s:\t%s\n", k, formatOutputValue(m[k]))
	}
	return tw.Flush()
}

// printNamedOutput prints one output's RAW value (string bare, else JSON) — for
// `$(... output <name>)` capture. Sensitive included: the caller named the key.
func printNamedOutput(cmd *cobra.Command, o config.StateOutput) error {
	w := cmd.OutOrStdout()
	if s, isStr := o.Value.(string); isStr {
		fmt.Fprintln(w, s)
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(o.Value)
}

// runPhaseOutput is the shared body of the four `<phase> output` commands. It
// prints ONLY the outputs that phase manages (ownedSubset), read from the phase's
// terraform.tfstate:
//
//   - no argument: the phase's owned output set (text or --json; sensitive
//     redacted unless --show-sensitive).
//   - one NAME: that owned output's raw value, for capture. A name owned by a
//     DIFFERENT phase errors with a pointer to the right command.
//
// A phase that isn't deployed has no tfstate → empty set / `{}`, exit 0.
func runPhaseOutput(cmd *cobra.Command, phase string, stateDir func(string) (string, error), args []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return config.WorkspaceNotReady(cctx.WorkspaceName)
	}
	outs, err := readPhaseOutputs(cctx.WorkspaceName, stateDir)
	if err != nil {
		return err
	}
	owned := ownedSubset(phase, outs)

	if len(args) == 1 {
		name := args[0]
		o, ok := owned[name]
		if !ok {
			if owner := outputOwner(name); owner != "" && owner != phase {
				return fmt.Errorf("output %q is managed by the %s phase, not %s — try `roksbnkctl %s output %s` or `roksbnkctl output %s`", name, owner, phase, owner, name, name)
			}
			return fmt.Errorf("no output %q managed by the %s phase — run `roksbnkctl %s output` to list, or `roksbnkctl output` for all phases", name, phase, phase)
		}
		return printNamedOutput(cmd, o)
	}
	return renderOutputs(cmd, owned)
}

// runAggregateOutput is the top-level `roksbnkctl output`: the union of every
// phase's OWNED outputs, each read from its owning phase's state (the populated
// copy). Ownership is disjoint, so the merged set never conflicts. A name arg
// returns that output's raw value from whichever phase owns it.
func runAggregateOutput(cmd *cobra.Command, args []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return config.WorkspaceNotReady(cctx.WorkspaceName)
	}

	merged := make(map[string]config.StateOutput)
	for _, p := range phaseStateDirs {
		outs, err := readPhaseOutputs(cctx.WorkspaceName, p.stateDir)
		if err != nil {
			return err
		}
		for k, o := range ownedSubset(p.phase, outs) {
			merged[k] = o
		}
	}

	if len(args) == 1 {
		name := args[0]
		o, ok := merged[name]
		if !ok {
			if owner := outputOwner(name); owner != "" {
				return fmt.Errorf("output %q (managed by the %s phase) is not populated — has that phase been deployed?", name, owner)
			}
			return fmt.Errorf("no output %q in any phase — run `roksbnkctl output` to list", name)
		}
		return printNamedOutput(cmd, o)
	}
	return renderOutputs(cmd, merged)
}

var clusterOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the Cluster phase's own terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "cluster", config.WorkspaceClusterStateDir, args)
	},
}

var bnkOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the BNK phase's own terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "bnk", config.WorkspaceStateDir, args)
	},
}

var testingOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the Testing phase's own terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "testing", config.WorkspaceTestingStateDir, args)
	},
}

var gatewayOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the Gateway phase's own terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "gateway", config.WorkspaceGatewayStateDir, args)
	},
}

var flpOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the FLP phase's own terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "flp", config.WorkspaceFLPStateDir, args)
	},
}

var outputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the merged outputs across all phases (cluster + bnk + testing + gateway + flp)",
	Long: `Print the union of every phase's own outputs — each read from its owning
phase's state, so values are the populated ones and never conflict. This is the
"everything" view; the per-phase ` + "`<phase> output`" + ` commands scope to just that
phase's managed attributes.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAggregateOutput,
}

func init() {
	cmds := []*cobra.Command{clusterOutputCmd, bnkOutputCmd, testingOutputCmd, gatewayOutputCmd, flpOutputCmd, outputCmd}
	for _, c := range cmds {
		c.Flags().BoolVar(&flagOutputJSON, "json", false, "output JSON (CI-friendly)")
		c.Flags().BoolVar(&flagOutputShowSensitive, "show-sensitive", false, "reveal sensitive output values (default redacted)")
	}
	clusterCmd.AddCommand(clusterOutputCmd)
	bnkCmd.AddCommand(bnkOutputCmd)
	testingCmd.AddCommand(testingOutputCmd)
	gatewayCmd.AddCommand(gatewayOutputCmd)
	flpCmd.AddCommand(flpOutputCmd)
	rootCmd.AddCommand(outputCmd)
}

// allOwnedOutputNames returns every root output name the ownership map assigns,
// sorted — used by the partition test.
func allOwnedOutputNames() []string {
	var all []string
	for _, names := range phaseOutputOwnership {
		all = append(all, names...)
	}
	sort.Strings(all)
	return all
}
