// Package cli wires the cobra command tree for roksbnkctl.
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

	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
)

// Build metadata, populated via -ldflags at link time.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Persistent flag values, bound on the root command.
var (
	flagWorkspace       string
	flagVerbose         bool
	flagQuiet           bool
	flagOutput          string
	flagNoColor         bool
	flagOn              string // --on <target>: dispatch a passthrough over SSH instead of locally
	flagInsecureHostKey bool   // --insecure-host-key: skip TOFU prompt; just record the key (CI use)
	flagBackend         string // --backend <local|docker|k8s|ssh:<target>>: per-invocation execution backend override (PRD 03)
	flagBootstrap       bool   // --bootstrap: opt-in to apt-get auto-install of missing tools on the SSH backend (PRD 03 §"open questions")
)

var rootCmd = &cobra.Command{
	Use:   "roksbnkctl",
	Short: "Deploy and validate F5 BIG-IP Next for Kubernetes (BNK) on IBM Cloud ROKS",
	Long: `roksbnkctl deploys F5 BIG-IP Next for Kubernetes (BNK) onto IBM Cloud ROKS,
manages the COS supply chain BNK depends on, and runs built-in connectivity,
DNS, and throughput tests against the deployed environment.

The 3-command happy path:
  roksbnkctl init    Interactive setup; writes the workspace config
  roksbnkctl up      Provision (or attach) and deploy BNK
  roksbnkctl test    Run connectivity, DNS, and throughput tests

See docs/PRD.md or https://github.com/jgruberf5/roksbnkctl for the full surface.`,
	SilenceUsage:      true,
	PersistentPreRunE: warnLegacyState,
}

// warnLegacyState nudges users with leftover ~/.bnkctl/ state from
// the previous binary name. Single-line, idempotent — printed every
// invocation until the user moves the directory. No auto-migration:
// state moves are user decisions (multiple workspaces, kubeconfig
// linkage, etc.) so we just point at the path and let them act.
func warnLegacyState(_ *cobra.Command, _ []string) error {
	if os.Getenv("ROKSBNKCTL_HOME") != "" {
		// Custom home — legacy detection isn't meaningful.
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	legacy := filepath.Join(home, ".bnkctl")
	current := filepath.Join(home, ".roksbnkctl")
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
		fmt.Fprintf(os.Stderr, "roksbnkctl: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "roksbnkctl: warning: parsing .env: %v\n", err)
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagWorkspace, "workspace", "w", "", "workspace name (default: current; first run creates 'default')")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")
	pf.BoolVarP(&flagQuiet, "quiet", "q", false, "suppress all but errors")
	pf.StringVarP(&flagOutput, "output", "o", "text", "output format: text | json")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable colored output")
	pf.StringVar(&flagOn, "on", "", "run on the named SSH target instead of locally (`roksbnkctl targets list` to see options)")
	pf.BoolVar(&flagInsecureHostKey, "insecure-host-key", false, "skip the host-key TOFU prompt; record on first contact (CI use)")
	pf.StringVar(&flagBackend, "backend", "", "execution backend: local | docker | k8s | ssh:<target> (default: per-tool from workspace exec: block, else local)")
	pf.BoolVar(&flagBootstrap, "bootstrap", false, "for --backend ssh:<target>: auto-install missing tools on Ubuntu via apt-get (requires passwordless sudo on the target)")

	// Wire the docker / k8s backends' image-tag resolver to the
	// binary's build-time Version. Sprint 4 polish carry-over 5b: a
	// tag-released binary pulls matching tag-released tool images
	// instead of the :dev tag CI doesn't publish.
	execbackend.SetToolImageTag(func() string { return Version })
}

// RootCommand returns the wired-up root cobra command for tooling that
// needs to walk the command tree (e.g. the cobra-to-markdown reference
// generator under tools/refgen/cobra-md). Subcommands are registered
// via package-level init() funcs, so the tree is fully assembled before
// any caller imports this package.
//
// Callers MUST NOT mutate the returned command — it's the same instance
// Execute() runs.
func RootCommand() *cobra.Command {
	return rootCmd
}
