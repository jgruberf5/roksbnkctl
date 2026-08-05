package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The `bnkforge` command group configures + drives BNK Forge (v3) cluster
// registration entirely from the CLI — so enabling the feature never requires
// hand-editing config.yaml. It mirrors the `registry target` / `test hosts`
// pattern: load → mutate → SaveWorkspace.
//
//	roksbnkctl bnkforge enable   [--url U --username N --project P --insecure]
//	roksbnkctl bnkforge disable
//	roksbnkctl bnkforge status
//	roksbnkctl bnkforge register [--url U --username N --password S --project P --insecure]
//
// Registration talks to BNK Forge v3's REST API directly (v3 has no CLI).
var (
	flagBNKForgeURL      string
	flagBNKForgeProject  string
	flagBNKForgeUser     string
	flagBNKForgePassword string
	flagBNKForgeInsecure bool
)

var bnkforgeCmd = &cobra.Command{
	Use:   "bnkforge",
	Short: "Configure + drive BNK Forge cluster registration",
	Long: `Configure and drive registration of this workspace's cluster with a
co-located BNK Forge (v3) install — without hand-editing config.yaml.

  enable    turn on auto-registration on ` + "`cluster up`" + ` (writes config.yaml)
  disable   turn it back off
  status    show the effective config + readiness
  register  register this workspace's cluster with BNK Forge right now

Registration uses BNK Forge v3's REST API (it has no CLI). It is credential-
backed: an IBM Cloud credential template is stored in Forge so it re-derives the
kubeconfig on demand — nothing perishable is stored. The password is read from
BNK_FORGE_PASSWORD or an interactive prompt (never persisted); the resulting
session token is cached in the OS keychain.`,
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

var bnkforgeUnregisterCmd = &cobra.Command{
	Use:   "unregister",
	Short: "Remove this workspace's cluster from its BNK Forge project",
	Long: `Removes the cluster this workspace registered, so a teardown can undo what
` + "`bnkforge register`" + ` did.

Without it a workspace can create a registration it has no way to remove:
` + "`bnkforge disable`" + ` only clears the local auto-register flag and never contacts
the server, so the cluster stays on Forge's Kubernetes page pointing at
something that may no longer exist.

Absence is success. No project, no cluster of that name, or a cluster already
gone all report and exit 0, so this is safe to run from a destroy path that may
run twice or run late.`,
	Args: cobra.NoArgs,
	RunE: runBNKForgeUnregister,
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
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeURL, "url", "", "BNK Forge server URL (e.g. https://forge.example.com)")
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeUser, "username", "", "BNK Forge login username")
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeProject, "project", "", "target BNK Forge project name (ensured-or-created)")
	bnkforgeEnableCmd.Flags().BoolVar(&flagBNKForgeInsecure, "insecure", false, "skip TLS verification (self-signed Forge cert)")

	// `register` accepts the same overrides (not persisted) plus --password.
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeURL, "url", "", "BNK Forge server URL (overrides config)")
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeUser, "username", "", "BNK Forge login username (overrides config)")
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgePassword, "password", "", "BNK Forge password (prefer BNK_FORGE_PASSWORD env or the prompt)")
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeProject, "project", "", "target BNK Forge project name (overrides config)")
	bnkforgeRegisterCmd.Flags().BoolVar(&flagBNKForgeInsecure, "insecure", false, "skip TLS verification (self-signed Forge cert)")
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgeURL, "url", "", "BNK Forge base URL")
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgeUser, "username", "", "BNK Forge username")
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgePassword, "password", "", "BNK Forge password (prefer BNK_FORGE_PASSWORD)")
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgeProject, "project", "", "BNK Forge project holding the cluster")
	bnkforgeUnregisterCmd.Flags().BoolVar(&flagBNKForgeInsecure, "insecure", false, "skip TLS verification (self-signed Forge cert)")

	bnkforgeCmd.AddCommand(bnkforgeEnableCmd, bnkforgeDisableCmd, bnkforgeStatusCmd, bnkforgeRegisterCmd, bnkforgeUnregisterCmd)
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
	if flagBNKForgeUser != "" {
		ws.BNKForge.Username = flagBNKForgeUser
	}
	if flagBNKForgeProject != "" {
		ws.BNKForge.Project = flagBNKForgeProject
	}
	if flagBNKForgeInsecure {
		ws.BNKForge.Insecure = true
	}
	if err := config.SaveWorkspace(name, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ BNK Forge auto-registration enabled for %q — the next `cluster up` will register the cluster.\n", name)
	if ws.BNKForge.URL == "" {
		fmt.Fprintf(os.Stderr, "  note: no URL set — provide --url (or %s) so registration knows where to connect.\n", envForgeURL)
	}
	fmt.Fprintf(os.Stderr, "  the auto-hook is non-interactive: set %s / %s in the environment for `cluster up`.\n", envForgeUser, envForgePassword)
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
	printFieldOr(bf != nil && bf.URL != "", "url", strVal(bf, func(b *config.BNKForgeCfg) string { return b.URL }), "(unset — set --url or "+envForgeURL+")")
	printFieldOr(bf != nil && bf.Username != "", "username", strVal(bf, func(b *config.BNKForgeCfg) string { return b.Username }), "(unset — set --username or "+envForgeUser+")")
	printFieldOr(bf != nil && bf.Project != "", "project", strVal(bf, func(b *config.BNKForgeCfg) string { return b.Project }), "(the workspace name)")
	fmt.Printf("insecure:    %v\n", bf != nil && bf.Insecure)
	if config.ForgeTokenFromKeychain(name) != "" {
		fmt.Printf("session:     cached token in OS keychain\n")
	} else {
		fmt.Printf("session:     none cached (will authenticate on register)\n")
	}
	if out, oerr := config.ReadClusterOutputs(name); oerr == nil && out.ClusterID != "" {
		fmt.Printf("cluster id:  %s (region %s)\n", out.ClusterID, out.Region)
	} else {
		fmt.Printf("cluster id:  none recorded yet — run `roksbnkctl cluster up`\n")
	}
	return nil
}

func strVal(bf *config.BNKForgeCfg, get func(*config.BNKForgeCfg) string) string {
	if bf == nil {
		return ""
	}
	return get(bf)
}

func printFieldOr(set bool, label, val, fallback string) {
	if set {
		fmt.Printf("%-12s %s\n", label+":", val)
	} else {
		fmt.Printf("%-12s %s\n", label+":", fallback)
	}
}

func runBNKForgeUnregister(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	var eff config.BNKForgeCfg
	if cctx.Workspace != nil && cctx.Workspace.BNKForge != nil {
		eff = *cctx.Workspace.BNKForge
	}
	if flagBNKForgeURL != "" {
		eff.URL = flagBNKForgeURL
	}
	if flagBNKForgeUser != "" {
		eff.Username = flagBNKForgeUser
	}
	if flagBNKForgeProject != "" {
		eff.Project = flagBNKForgeProject
	}
	if flagBNKForgeInsecure {
		eff.Insecure = true
	}
	if flagBNKForgePassword != "" {
		_ = os.Setenv(envForgePassword, flagBNKForgePassword)
	}
	return unregisterFromBNKForge(cmd.Context(), cctx, &eff, true)
}

func runBNKForgeRegister(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	// Effective config = stored block overlaid with any flags, without
	// persisting them (use `bnkforge enable` to persist).
	eff := config.BNKForgeCfg{}
	if cctx.Workspace != nil && cctx.Workspace.BNKForge != nil {
		eff = *cctx.Workspace.BNKForge
	}
	if flagBNKForgeURL != "" {
		eff.URL = flagBNKForgeURL
	}
	if flagBNKForgeUser != "" {
		eff.Username = flagBNKForgeUser
	}
	if flagBNKForgeProject != "" {
		eff.Project = flagBNKForgeProject
	}
	if flagBNKForgeInsecure {
		eff.Insecure = true
	}
	// --password is a transient override: expose it to resolveForgePassword via
	// the env it already consults (avoids widening the internal signature).
	if flagBNKForgePassword != "" {
		_ = os.Setenv(envForgePassword, flagBNKForgePassword)
	}
	return registerWithBNKForge(cmd.Context(), cctx, &eff, true)
}
