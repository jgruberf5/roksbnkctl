package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The `bnkforge` command group configures + drives BNK Forge cluster
// registration entirely from the CLI — so enabling the feature never requires
// hand-editing config.yaml. It mirrors the `registry target` / `test hosts`
// pattern: load → mutate → SaveWorkspace.
//
//	roksbnkctl bnkforge enable [--url U] [--project P]   # writes bnkforge.register: true
//	roksbnkctl bnkforge disable                          # writes bnkforge.register: false
//	roksbnkctl bnkforge status                           # show the effective config + readiness
//	roksbnkctl bnkforge register [--url U] [--project P] # register this cluster NOW (no re-`up`)
var (
	flagBNKForgeURL     string
	flagBNKForgeProject string
)

var bnkforgeCmd = &cobra.Command{
	Use:   "bnkforge",
	Short: "Configure + drive BNK Forge cluster registration",
	Long: `Configure and drive registration of this workspace's cluster with a
co-located BNK Forge install — without hand-editing config.yaml.

  enable    turn on auto-registration on ` + "`cluster up`" + ` (writes config.yaml)
  disable   turn it back off
  status    show the effective config + whether the bnk-forge CLI / cluster id are ready
  register  register this workspace's cluster with BNK Forge right now

Registration is credential-backed: BNK Forge re-derives the kubeconfig on demand
from an IBM Cloud credential template, so nothing perishable is stored. The
` + "`bnk-forge`" + ` CLI (which ships with BNK Forge, not roksbnkctl) must be on PATH.`,
}

var bnkforgeEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable BNK Forge auto-registration on `cluster up` (writes config.yaml for you)",
	Args:  cobra.NoArgs,
	RunE:  runBNKForgeEnable,
}

var bnkforgeDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Turn off BNK Forge auto-registration",
	Args:  cobra.NoArgs,
	RunE:  runBNKForgeDisable,
}

var bnkforgeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show this workspace's BNK Forge registration config + readiness",
	Args:  cobra.NoArgs,
	RunE:  runBNKForgeStatus,
}

var bnkforgeRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register this workspace's cluster with BNK Forge now (no re-`up` needed)",
	Args:  cobra.NoArgs,
	RunE:  runBNKForgeRegister,
}

func init() {
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeURL, "url", "", "BNK Forge server URL (overrides the bnk-forge CLI's stored-session URL)")
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeProject, "project", "", "target BNK Forge project id")
	// `register` accepts the same two as transient overrides (not persisted).
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeURL, "url", "", "BNK Forge server URL (overrides config + stored session)")
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeProject, "project", "", "target BNK Forge project id (overrides config)")

	bnkforgeCmd.AddCommand(bnkforgeEnableCmd, bnkforgeDisableCmd, bnkforgeStatusCmd, bnkforgeRegisterCmd)
	rootCmd.AddCommand(bnkforgeCmd)
}

func runBNKForgeEnable(_ *cobra.Command, _ []string) error {
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	if ws.BNKForge == nil {
		ws.BNKForge = &config.BNKForgeCfg{}
	}
	ws.BNKForge.Register = true
	if flagBNKForgeURL != "" {
		ws.BNKForge.URL = flagBNKForgeURL
	}
	if flagBNKForgeProject != "" {
		ws.BNKForge.Project = flagBNKForgeProject
	}
	if err := config.SaveWorkspace(name, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ BNK Forge auto-registration enabled for %q — the next `cluster up` will register the cluster.\n", name)
	if _, lerr := exec.LookPath("bnk-forge"); lerr != nil {
		fmt.Fprintln(os.Stderr, "  note: the `bnk-forge` CLI isn't on PATH yet — install BNK Forge's CLI before the next `cluster up`.")
	}
	return nil
}

func runBNKForgeDisable(_ *cobra.Command, _ []string) error {
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	if ws.BNKForge == nil || !ws.BNKForge.Register {
		fmt.Fprintf(os.Stderr, "✓ BNK Forge auto-registration already off for %q.\n", name)
		return nil
	}
	ws.BNKForge.Register = false
	if err := config.SaveWorkspace(name, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ BNK Forge auto-registration disabled for %q.\n", name)
	return nil
}

func runBNKForgeStatus(_ *cobra.Command, _ []string) error {
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	bf := ws.BNKForge
	fmt.Printf("workspace:   %s\n", name)
	fmt.Printf("register:    %v\n", bf != nil && bf.Register)
	if bf != nil && bf.URL != "" {
		fmt.Printf("url:         %s\n", bf.URL)
	} else {
		fmt.Printf("url:         (bnk-forge CLI's stored-session URL)\n")
	}
	if bf != nil && bf.Project != "" {
		fmt.Printf("project:     %s\n", bf.Project)
	} else {
		fmt.Printf("project:     (CLI auto-selects: active / sole / prompt)\n")
	}
	if p, lerr := exec.LookPath("bnk-forge"); lerr == nil {
		fmt.Printf("bnk-forge:   %s\n", p)
	} else {
		fmt.Printf("bnk-forge:   not found on PATH (ships with BNK Forge)\n")
	}
	if out, oerr := config.ReadClusterOutputs(name); oerr == nil && out.ClusterID != "" {
		fmt.Printf("cluster id:  %s (region %s)\n", out.ClusterID, out.Region)
	} else {
		fmt.Printf("cluster id:  none recorded yet — run `roksbnkctl cluster up`\n")
	}
	return nil
}

func runBNKForgeRegister(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	// Effective config = stored block overlaid with any --url/--project flags,
	// without persisting them (use `bnkforge enable` to persist).
	eff := config.BNKForgeCfg{}
	if cctx.Workspace != nil && cctx.Workspace.BNKForge != nil {
		eff = *cctx.Workspace.BNKForge
	}
	if flagBNKForgeURL != "" {
		eff.URL = flagBNKForgeURL
	}
	if flagBNKForgeProject != "" {
		eff.Project = flagBNKForgeProject
	}
	return registerWithBNKForge(cmd.Context(), cctx, &eff)
}
