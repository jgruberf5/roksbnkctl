package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// Local flag values for `targets add`. Reset every invocation; cobra's
// flag binding writes into these vars when --host etc. is parsed.
var (
	flagTargetHost      string
	flagTargetUser      string
	flagTargetPort      int
	flagTargetKeyPath   string
	flagTargetKeySource string
)

var targetsCmd = &cobra.Command{
	Use:   "targets",
	Short: "Manage SSH targets used by --on",
	Long: `Targets are named SSH endpoints stored under the workspace's
` + "`targets:`" + ` block. They become reachable via the persistent --on flag
on commands like ` + "`roksbnkctl exec`" + `, ` + "`roksbnkctl shell`" + `, ` + "`roksbnkctl kubectl`" + `, etc.

A jumphost target is auto-populated after a successful ` + "`roksbnkctl up`" + `
when the upstream HCL provisions one (testing_tgw_jumphost outputs).`,
}

var targetsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all targets in the current workspace",
	RunE:  runTargetsList,
}

var targetsShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show detail for one target",
	Args:  cobra.ExactArgs(1),
	RunE:  runTargetsShow,
}

var targetsAddCmd = &cobra.Command{
	Use:   "add <name> --host H --user U [--port P] [--key-path P | --key-source S]",
	Short: "Add or update a target",
	Args:  cobra.ExactArgs(1),
	RunE:  runTargetsAdd,
}

var targetsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a target",
	Args:  cobra.ExactArgs(1),
	RunE:  runTargetsRemove,
}

func init() {
	targetsAddCmd.Flags().StringVar(&flagTargetHost, "host", "", "host or IP")
	targetsAddCmd.Flags().StringVar(&flagTargetUser, "user", "", "remote user")
	targetsAddCmd.Flags().IntVar(&flagTargetPort, "port", 0, "ssh port (default 22)")
	targetsAddCmd.Flags().StringVar(&flagTargetKeyPath, "key-path", "", "path to a PEM private key")
	targetsAddCmd.Flags().StringVar(&flagTargetKeySource, "key-source", "", `key source — "agent" or "tf-output:<name>"`)
	_ = targetsAddCmd.MarkFlagRequired("host")
	_ = targetsAddCmd.MarkFlagRequired("user")

	targetsCmd.AddCommand(targetsListCmd, targetsShowCmd, targetsAddCmd, targetsRemoveCmd)
	rootCmd.AddCommand(targetsCmd)
}

func runTargetsList(_ *cobra.Command, _ []string) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return err
	}
	ts, err := remote.ListTargets(cctx.WorkspaceName)
	if err != nil {
		return err
	}
	if len(ts) == 0 {
		fmt.Fprintf(os.Stderr, "no targets in workspace %q (add one with `roksbnkctl targets add`)\n", cctx.WorkspaceName)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOST\tUSER\tKEY")
	for _, t := range ts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Name, hostPort(t), t.User, t.KeySourceDescription())
	}
	return tw.Flush()
}

func runTargetsShow(_ *cobra.Command, args []string) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return err
	}
	t, err := remote.LoadTarget(cctx.WorkspaceName, args[0])
	if err != nil {
		return err
	}
	fmt.Printf("name:        %s\n", t.Name)
	fmt.Printf("host:        %s\n", t.Host)
	fmt.Printf("port:        %d\n", t.Port)
	fmt.Printf("user:        %s\n", t.User)
	if t.KeyPath != "" {
		fmt.Printf("key_path:    %s\n", t.KeyPath)
	}
	if t.KeySource != "" {
		fmt.Printf("key_source:  %s\n", t.KeySource)
	}
	return nil
}

func runTargetsAdd(_ *cobra.Command, args []string) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return err
	}
	if flagTargetKeyPath == "" && flagTargetKeySource == "" {
		return errors.New("one of --key-path or --key-source is required")
	}
	cfg := config.TargetCfg{
		Host:      flagTargetHost,
		Port:      flagTargetPort,
		User:      flagTargetUser,
		KeyPath:   flagTargetKeyPath,
		KeySource: flagTargetKeySource,
	}
	if err := remote.SetTarget(cctx.WorkspaceName, args[0], cfg); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ wrote target %q to workspace %q\n", args[0], cctx.WorkspaceName)
	return nil
}

func runTargetsRemove(_ *cobra.Command, args []string) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return err
	}
	if err := remote.RemoveTarget(cctx.WorkspaceName, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ removed target %q\n", args[0])
	return nil
}

// requireWorkspace loads the workspace context and errors if the
// workspace hasn't been initialised yet — every targets sub-command
// needs a real config.yaml to read/write.
func requireWorkspace() (*config.Context, error) {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return nil, err
	}
	if cctx.Workspace == nil {
		return nil, fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}
	return cctx, nil
}

// hostPort renders Host or Host:Port for the targets list.
func hostPort(t *remote.Target) string {
	if t.Port == 0 || t.Port == 22 {
		return t.Host
	}
	return fmt.Sprintf("%s:%d", t.Host, t.Port)
}
