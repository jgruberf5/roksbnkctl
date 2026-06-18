package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// flagOutputJSON / flagOutputShowSensitive back the flags shared by every phase
// `output` command (only one runs per invocation, so single vars are fine).
var (
	flagOutputJSON          bool
	flagOutputShowSensitive bool
)

// runPhaseOutput is the shared body of the four `<phase> output` commands —
// the sibling of runPhaseStatus. It prints the phase's terraform outputs, read
// from the phase's terraform.tfstate, with two modes mirroring `terraform output`:
//
//   - no argument: the phase's FULL output set (text key=value, or --json).
//     Sensitive values are redacted ("<sensitive>") unless --show-sensitive.
//   - one NAME argument: that single output's RAW value (strings bare, complex
//     values JSON-encoded) — for `$(roksbnkctl cluster output cluster_id)` capture
//     in scripts/CI. Sensitive included: the caller asked for that exact key.
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
	dir, err := stateDir(cctx.WorkspaceName)
	if err != nil {
		return err
	}
	outs, err := config.ReadStateOutputs(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			outs = map[string]config.StateOutput{} // not deployed → no outputs
		} else {
			return err
		}
	}
	w := cmd.OutOrStdout()

	// Single named output → raw value, for capture. Sensitive included (the
	// caller named this exact key).
	if len(args) == 1 {
		o, ok := outs[args[0]]
		if !ok {
			return fmt.Errorf("no output %q in the %s phase — run `roksbnkctl %s output` to list", args[0], phase, phase)
		}
		if s, isStr := o.Value.(string); isStr {
			fmt.Fprintln(w, s)
			return nil
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(o.Value)
	}

	// Full set. Redact sensitive unless --show-sensitive.
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

var clusterOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the Cluster phase's terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "cluster", config.WorkspaceClusterStateDir, args)
	},
}

var bnkOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the BNK phase's terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "bnk", config.WorkspaceStateDir, args)
	},
}

var testingOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the Testing phase's terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "testing", config.WorkspaceTestingStateDir, args)
	},
}

var gatewayOutputCmd = &cobra.Command{
	Use:   "output [name]",
	Short: "Print the Gateway phase's terraform outputs (text or --json; [name] = one raw value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPhaseOutput(cmd, "gateway", config.WorkspaceGatewayStateDir, args)
	},
}

func init() {
	for _, c := range []*cobra.Command{clusterOutputCmd, bnkOutputCmd, testingOutputCmd, gatewayOutputCmd} {
		c.Flags().BoolVar(&flagOutputJSON, "json", false, "output JSON (CI-friendly)")
		c.Flags().BoolVar(&flagOutputShowSensitive, "show-sensitive", false, "reveal sensitive output values (default redacted)")
	}
	clusterCmd.AddCommand(clusterOutputCmd)
	bnkCmd.AddCommand(bnkOutputCmd)
	testingCmd.AddCommand(testingOutputCmd)
	gatewayCmd.AddCommand(gatewayOutputCmd)
}
