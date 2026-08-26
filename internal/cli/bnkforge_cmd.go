package cli

import (
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The `bnkforge` command group configures + drives BNK Forge (v3) cluster
// registration entirely from the CLI — so enabling the feature never requires
// hand-editing config.yaml. It mirrors the `registry target` / `test hosts`
// pattern: load → mutate → SaveWorkspace.
//
//	roksbnkctl bnkforge enable   [--url U --username N --project P --forge-ca F | --insecure]
//	roksbnkctl bnkforge disable
//	roksbnkctl bnkforge status
//	roksbnkctl bnkforge register [--url U --username N --password S --project P --forge-ca F | --insecure]
//
// Registration talks to BNK Forge v3's REST API directly (v3 has no CLI).
var (
	flagBNKForgeURL      string
	flagBNKForgeProject  string
	flagBNKForgeUser     string
	flagBNKForgePassword string
	flagBNKForgeInsecure bool
	flagBNKForgeCAFile   string
	flagBNKForgeForce    bool

	// `bnkforge ssh-credential` (#222)
	flagBNKForgeSSHHost        string
	flagBNKForgeSSHUser        string
	flagBNKForgeSSHKey         string
	flagBNKForgeSSHName        string
	flagBNKForgeSSHPort        int
	flagBNKForgeSSHFingerprint string
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
	Short: "Turn off BNK Forge auto-registration (LOCAL only — does not unregister)",
	Long: `Clears the workspace's auto-register flag, so ` + "`cluster up`" + ` stops registering
with BNK Forge.

This is a LOCAL setting and never contacts the server. A cluster already
registered stays on Forge's Kubernetes page — to remove it, use
` + "`roksbnkctl bnkforge unregister`" + `.`,
	Args: cobra.NoArgs,
	RunE: runBNKForgeDisable,
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

var bnkforgeSSHCredentialCmd = &cobra.Command{
	Use:   "ssh-credential",
	Short: "Give BNK Forge the SSH private key for an appliance this workspace built",
	Long: `Stores an appliance's SSH private key in BNK Forge and attaches it to the
workspace's project, so Forge can reach the appliance itself.

` + "`bnk.flp.vsi.ssh_key`" + ` names an IBM Cloud VPC key, which puts the PUBLIC half on
the VSI — that is operator access, and it already works. Forge separately needs
the PRIVATE half, and nothing supplied it, so a healthy FLP reports:

    infrastructure_private_key_available: false
    infrastructure_access_status:         recovery_required

Nothing can be recovered there — the credential was never created.

--host must be the address FORGE can reach, which for an FLP is the FLOATING IP.
` + "`flp status`" + ` reports a services-VPC endpoint that Forge sits outside of, so a
credential built from it can never connect.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runBNKForgeSSHCredential(cmdContext(cmd), true)
	},
}

func init() {
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeURL, "url", "", "BNK Forge server URL (e.g. https://forge.example.com)")
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeUser, "username", "", "BNK Forge login username")
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeProject, "project", "", "target BNK Forge project name (ensured-or-created)")
	bnkforgeEnableCmd.Flags().BoolVar(&flagBNKForgeInsecure, "insecure", false, insecureFlagHelp)
	bnkforgeEnableCmd.Flags().StringVar(&flagBNKForgeCAFile, "forge-ca", "", forgeCAFlagHelp)

	// `register` accepts the same overrides (not persisted) plus --password.
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeURL, "url", "", "BNK Forge server URL (overrides config)")
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeUser, "username", "", "BNK Forge login username (overrides config)")
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgePassword, "password", "", "BNK Forge password (prefer BNK_FORGE_PASSWORD env or the prompt)")
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeProject, "project", "", "target BNK Forge project name (overrides config)")
	bnkforgeRegisterCmd.Flags().BoolVar(&flagBNKForgeInsecure, "insecure", false, insecureFlagHelp)
	bnkforgeRegisterCmd.Flags().StringVar(&flagBNKForgeCAFile, "forge-ca", "", forgeCAFlagHelp)
	bnkforgeRegisterCmd.Flags().BoolVar(&flagBNKForgeForce, "force", false,
		"take over a cluster registered to ANOTHER Forge project (moves it, and its cluster id changes)")
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgeURL, "url", "", "BNK Forge base URL")
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgeUser, "username", "", "BNK Forge username")
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgePassword, "password", "", "BNK Forge password (prefer BNK_FORGE_PASSWORD)")
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgeProject, "project", "", "BNK Forge project holding the cluster")
	bnkforgeUnregisterCmd.Flags().BoolVar(&flagBNKForgeInsecure, "insecure", false, insecureFlagHelp)
	bnkforgeUnregisterCmd.Flags().StringVar(&flagBNKForgeCAFile, "forge-ca", "", forgeCAFlagHelp)

	sc := bnkforgeSSHCredentialCmd.Flags()
	sc.StringVar(&flagBNKForgeSSHHost, "host", "", "address FORGE reaches the appliance on — the FLOATING IP, not the endpoint `flp status` prints")
	// --ssh-username, NOT --username. Every other bnkforge subcommand uses
	// --username for the FORGE login, so overloading it here meant
	// `--username admin` silently set the APPLIANCE's SSH user while the
	// operator believed they had authenticated to Forge — storing a credential
	// that cannot log in, which is the failure --expect-fingerprint exists to
	// prevent, arriving through the flag names instead.
	sc.StringVar(&flagBNKForgeSSHUser, "ssh-username", "ubuntu", "SSH user on the appliance")
	sc.StringVar(&flagBNKForgeSSHKey, "key", "", "PRIVATE key file Forge will use (unencrypted; Forge stores it and cannot prompt)")
	sc.StringVar(&flagBNKForgeSSHName, "name", "", "credential name in Forge (default: <project>-ssh)")
	sc.IntVar(&flagBNKForgeSSHPort, "port", 22, "SSH port on the appliance")
	sc.StringVar(&flagBNKForgeSSHFingerprint, "expect-fingerprint", "",
		"SHA256 fingerprint the key must match (e.g. the VPC key's) — a credential that cannot log in is worse than none")
	sc.StringVar(&flagBNKForgeProject, "project", "", "BNK Forge project to attach the credential to")
	sc.StringVar(&flagBNKForgeURL, "url", "", "BNK Forge server URL (overrides config)")
	sc.StringVar(&flagBNKForgeUser, "username", "", "BNK Forge login username (overrides config)")
	sc.StringVar(&flagBNKForgePassword, "password", "", "BNK Forge password (prefer BNK_FORGE_PASSWORD env or the prompt)")
	sc.BoolVar(&flagBNKForgeInsecure, "insecure", false, insecureFlagHelp)
	sc.StringVar(&flagBNKForgeCAFile, "forge-ca", "", forgeCAFlagHelp)

	bnkforgeCmd.AddCommand(bnkforgeEnableCmd, bnkforgeDisableCmd, bnkforgeStatusCmd, bnkforgeRegisterCmd, bnkforgeUnregisterCmd, bnkforgeSSHCredentialCmd)
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
	if flagBNKForgeCAFile != "" {
		b64, err := readForgeCAFile(flagBNKForgeCAFile)
		if err != nil {
			return err
		}
		ws.BNKForge.CAB64 = b64
		// Config hygiene, not enforcement: forge.New already ignores Insecure
		// whenever a CA is pinned. But `enable` is the only CLI path that writes
		// this block, so leaving a stale `insecure: true` here would make every
		// later command print the "insecure is ignored" notice with no way to
		// quiet it short of hand-editing config.yaml.
		ws.BNKForge.Insecure = false
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
	fmt.Printf("tls:         %s\n", forgeTLSDescription(bf))
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
	if flagBNKForgeCAFile != "" {
		b64, cerr := readForgeCAFile(flagBNKForgeCAFile)
		if cerr != nil {
			return cerr
		}
		eff.CAB64 = b64
	}
	if flagBNKForgePassword != "" {
		_ = os.Setenv(envForgePassword, flagBNKForgePassword)
	}
	return unregisterFromBNKForge(cmdContext(cmd), cctx, &eff, true)
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
	if flagBNKForgeCAFile != "" {
		b64, cerr := readForgeCAFile(flagBNKForgeCAFile)
		if cerr != nil {
			return cerr
		}
		eff.CAB64 = b64
	}
	// --password is a transient override: expose it to resolveForgePassword via
	// the env it already consults (avoids widening the internal signature).
	if flagBNKForgePassword != "" {
		_ = os.Setenv(envForgePassword, flagBNKForgePassword)
	}
	return registerWithBNKForge(cmdContext(cmd), cctx, &eff, true, flagBNKForgeForce)
}

const (
	insecureFlagHelp = "DISABLE TLS verification — the API token is then sent over an unauthenticated connection; prefer --forge-ca"
	forgeCAFlagHelp  = "PEM file holding the CA the Forge server's certificate must chain to (pins a self-signed lab cert; supersedes --insecure)"
)

// readForgeCAFile loads a PEM CA and returns it base64-encoded for config.yaml.
// It parses before storing so a wrong file (a key, a DER blob, a typo'd path)
// is rejected here rather than at the next connection.
func readForgeCAFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading --forge-ca %s: %w", path, err)
	}
	if !x509.NewCertPool().AppendCertsFromPEM(body) {
		return "", fmt.Errorf("--forge-ca %s: no PEM certificate found in the file", path)
	}
	return base64.StdEncoding.EncodeToString(body), nil
}

// forgeTLSDescription renders the connection's trust for `bnkforge status`, so
// a workspace running unverified says so where someone will read it. The
// CA-before-insecure ordering mirrors the precedence forge.New enforces — if
// forge.New's rules ever change, this text must follow.
func forgeTLSDescription(bf *config.BNKForgeCfg) string {
	switch {
	case bf == nil:
		return "verified (system roots)"
	case strings.TrimSpace(bf.CAB64) != "":
		return "verified (pinned CA from bnkforge.ca_b64)"
	case bf.Insecure:
		return "NOT VERIFIED (insecure: true) — the API token is sent over an unauthenticated connection; pin a CA with `bnkforge enable --forge-ca <file>`"
	default:
		return "verified (system roots)"
	}
}
