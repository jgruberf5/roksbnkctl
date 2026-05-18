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

	"github.com/jgruberf5/roksbnkctl/internal/config"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
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

The 4-command lifecycle:
  roksbnkctl init    Interactive setup; writes the workspace config
  roksbnkctl up      Provision (or attach) and deploy BNK
  roksbnkctl test    Run connectivity, DNS, and throughput tests
  roksbnkctl down    Tear down BNK (and the cluster if cluster up provisioned it)

See https://jgruberf5.github.io/roksbnkctl/book/ for the canonical user guide.`,
	SilenceUsage:      true,
	PersistentPreRunE: rootPersistentPreRunE,
}

// resolvedFlags is the single resolved-invocation context for this
// process, computed exactly once by resolveInvocationContext from the
// root PersistentPreRunE before any RunE runs. Downstream code reads
// the chokepoint-normalized globals (flagVarFiles / flagTFSource) it
// produced; nothing re-derives a path. nil until the PersistentPreRunE
// has run (e.g. in unit tests that call helpers directly — those pin
// the wrapper symbols, which delegate to orchestration regardless).
var resolvedFlags *orchestration.ResolvedFlags

// rootPersistentPreRunE is the single chokepoint entry point. Cobra
// runs exactly one PersistentPreRunE (the most-specific in the chain,
// here always the root's — no subcommand overrides it) before every
// command's RunE, including DisableFlagParsing passthrough commands.
// This is the smallest correct surface for "normalize every path-valued
// flag exactly once": one function, one call, every command.
//
// It (1) keeps the legacy-state nudge, then (2) builds the single
// ResolvedFlags — normalizing --var-file and the local --tf-source
// against the invocation CWD exactly once (orchestration.Resolve) — and
// writes the resolved values back into the flag globals so every
// downstream RunE / dispatch consumes already-absolute paths without
// re-deriving them (Sprint 12 Issues 1/2 + Sprint 13 Issue 1, retired
// as a class, not patched as instances).
//
// Resolution is over the raw flag globals cobra has already populated
// by PersistentPreRunE time. Passthrough commands (DisableFlagParsing)
// don't register --var-file/--tf-source, so for them this is a no-op on
// empty inputs. Note: an invalid --var-file now surfaces here (before
// RunE) rather than at the RunE top — same error text, one step
// earlier; the lifecycle `--on` reject is unaffected for valid inputs.
func rootPersistentPreRunE(cmd *cobra.Command, args []string) error {
	if err := warnLegacyState(cmd, args); err != nil {
		return err
	}
	rf, err := orchestration.Resolve(flagVarFiles, flagTFSource)
	if err != nil {
		return err
	}
	resolvedFlags = rf
	// Write the chokepoint-normalized values back into the flag globals
	// so every downstream consumer reads absolute paths. This is the
	// single mutation site — replacing the 8+ per-RunE `flagVarFiles =
	// resolved` fan-out and the 2 per-init-site --tf-source
	// normalizations that previously each re-derived.
	flagVarFiles = rf.VarFiles
	flagTFSource = rf.TFSource
	return nil
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
	// Wire cobra's auto-generated `--version` flag at Execute() time
	// rather than init() so the value reflects the build-time Version
	// even when callers (tests, refgen) import the package and mutate
	// the Version variable. The custom VersionTemplate produces the
	// same two-line shape as `roksbnkctl version`:
	//   roksbnkctl <version> (commit <c>, built <d>)
	//   Docs: <url>
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate(fmt.Sprintf(
		"roksbnkctl {{.Version}} (commit %s, built %s)\nDocs: %s\n",
		Commit, BuildDate, DocsURL))
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

	// Sprint 11 / PRD 07: wire the build-time Version into the
	// terraform.applied.tfvars header so the snapshot records which
	// roksbnkctl produced it. Same import-cycle-dodging seam pattern as
	// SetToolImageTag above.
	config.SetAppliedTFVarsVersion(func() string { return Version })
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
