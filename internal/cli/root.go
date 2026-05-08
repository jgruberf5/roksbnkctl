// Package cli wires the cobra command tree for roksctl.
//
// Build-time variables (Version, Commit, BuildDate) are populated via
// -ldflags by goreleaser / Makefile.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// Build metadata, populated via -ldflags at link time.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Persistent flag values, bound on the root command.
var (
	flagWorkspace string
	flagVerbose   bool
	flagQuiet     bool
	flagOutput    string
	flagNoColor   bool
)

var rootCmd = &cobra.Command{
	Use:   "roksctl",
	Short: "Deploy and validate F5 BIG-IP Next for Kubernetes (BNK) on IBM Cloud ROKS",
	Long: `roksctl deploys F5 BIG-IP Next for Kubernetes (BNK) onto IBM Cloud ROKS,
manages the COS supply chain BNK depends on, and runs built-in connectivity,
DNS, and throughput tests against the deployed environment.

The 3-command happy path:
  roksctl init    Interactive setup; writes the workspace config
  roksctl up      Provision (or attach) and deploy BNK
  roksctl test    Run connectivity, DNS, and throughput tests

See docs/PRD.md or https://github.com/jgruberf5/roksctl for the full surface.`,
	SilenceUsage:      true,
	PersistentPreRunE: warnLegacyState,
}

// warnLegacyState nudges users with leftover ~/.bnkctl/ state from
// the previous binary name. Single-line, idempotent — printed every
// invocation until the user moves the directory. No auto-migration:
// state moves are user decisions (multiple workspaces, kubeconfig
// linkage, etc.) so we just point at the path and let them act.
func warnLegacyState(_ *cobra.Command, _ []string) error {
	if os.Getenv("ROKSCTL_HOME") != "" {
		// Custom home — legacy detection isn't meaningful.
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	legacy := filepath.Join(home, ".bnkctl")
	current := filepath.Join(home, ".roksctl")
	if _, err := os.Stat(legacy); err != nil {
		return nil // no legacy dir → nothing to warn about
	}
	if _, err := os.Stat(current); err == nil {
		return nil // both exist → user has already started the new layout, leave them be
	}
	fmt.Fprintf(os.Stderr, "warning: found legacy state at %s — move it to %s to keep it (we won't auto-migrate).\n",
		legacy, current)
	return nil
}

// Execute runs the root command. Wires SIGINT (Ctrl+C) to a cancellable
// context so long-running operations like terraform apply terminate
// promptly and child processes get cleaned up.
//
// Loads $PWD/.env at startup if present — godotenv's Load does NOT
// overwrite existing env vars, so anything already in the shell wins.
// Lets users keep IBMCLOUD_API_KEY, GITHUB_TOKEN, TF_VAR_* etc. in a
// project-scoped file instead of shell profiles.
func Execute() {
	loadDotenv()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roksctl: %v\n", err)
		os.Exit(1)
	}
}

// loadDotenv reads ./.env if present. Missing file is silent (the
// common case for users who don't use one). Parse errors are loud —
// otherwise a typo in the file would leave creds mysteriously unset.
func loadDotenv() {
	if _, err := os.Stat(".env"); err != nil {
		return
	}
	if err := godotenv.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "roksctl: warning: parsing .env: %v\n", err)
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagWorkspace, "workspace", "w", "", "workspace name (default: current; first run creates 'default')")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")
	pf.BoolVarP(&flagQuiet, "quiet", "q", false, "suppress all but errors")
	pf.StringVarP(&flagOutput, "output", "o", "text", "output format: text | json")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable colored output")
}

// unimplemented is the placeholder RunE for stubbed commands.
// Returning an error makes CI flag any drift between command surface
// and implementation.
func unimplemented(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("%q not implemented yet — see docs/PRD.md", cmd.CommandPath())
}
