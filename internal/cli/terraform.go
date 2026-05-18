package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// terraformCmd is the read-only `roksbnkctl terraform` escape hatch
// (Sprint 13 Issue 2 / PRD 08). roksbnkctl drives terraform; it does not
// wrap it. The lifecycle verbs (up / plan / apply / down, plus
// phase-scoped cluster/bnk up/down) are the ONLY mutation path. This
// subcommand provides a *gated, read-only* window onto the workspace's
// managed state so users don't have to `cd ~/.roksbnkctl/<ws>/state &&
// TF_DATA_DIR=... terraform output` by hand (the layout leak + foot-gun
// this removes).
//
// DisableFlagParsing so terraform's own flags (e.g. `-json`, `-no-color`)
// reach terraform untouched; --phase / --on are pulled out by manual
// parse, mirroring the kubectl/oc/ibmcloud passthrough pattern.
var terraformCmd = &cobra.Command{
	Use:     "terraform [subcommand] [args...]",
	Aliases: []string{"tf"},
	Short:   "Read-only passthrough to terraform against the workspace's managed state",
	Long: `roksbnkctl terraform runs a READ-ONLY terraform subcommand against the
workspace's managed state, with terraform's working directory and
TF_DATA_DIR resolved for you (no need to cd into ~/.roksbnkctl/... or
export TF_DATA_DIR).

This command is read-only by allowlist. Permitted subcommands:
  output, show, state list, state show, state pull, providers,
  version, graph, validate, fmt -check

Everything else — including apply, destroy, init, plan, import, taint,
untaint, state rm/mv/replace-provider, and any -auto-approve — is
rejected before terraform runs. Mutations go exclusively through
roksbnkctl up / plan / apply / down (or cluster/bnk up/down); running
terraform apply/destroy outside the orchestration skips the rendered
tfvars, the apply-retry wrapper, the post-apply kubeconfig fetch, the
applied-tfvars snapshot, and the auto-jumphost seeding, and desyncs the
managed state.

Use --phase cluster to target the cluster-phase state
(~/.roksbnkctl/<ws>/state-cluster/); the default is the trial/single
state (~/.roksbnkctl/<ws>/state/). --on is not supported: the managed
terraform state lives on this workstation, not on a jumphost.

Examples:
  roksbnkctl terraform output testing_cluster_jumphost_ssh_commands
  roksbnkctl terraform state list
  roksbnkctl --phase cluster terraform show`,
	DisableFlagParsing: true,
	RunE:               runTerraformPassthrough,
}

// terraformCmd is registered in cluster.go's init() alongside the other
// passthrough commands (kubectlCmd / ocCmd / ibmcloudCmd).

// terraformReadOnlyTop is the allowlist of permitted top-level terraform
// subcommands (Sprint 13 Issue 2, hard requirement 1 — allowlist, not
// denylist). Anything not present is rejected before terraform runs.
var terraformReadOnlyTop = map[string]struct{}{
	"output":    {},
	"show":      {},
	"providers": {},
	"version":   {},
	"graph":     {},
	"validate":  {},
	"state":     {}, // gated further by terraformReadOnlyStateSub
	"fmt":       {}, // gated to require -check (terraformFmtIsReadOnly)
}

// terraformReadOnlyStateSub is the sub-verb allowlist under the
// permitted top-level `state` (hard requirement 2 — `state rm`/`mv`/
// `replace-provider` must NOT slip through a permitted `state`).
var terraformReadOnlyStateSub = map[string]struct{}{
	"list": {},
	"show": {},
	"pull": {},
}

// terraformMutatingFlags are flags that imply (or enable) a mutation;
// rejected on any allowlisted subcommand as a second guard
// (hard requirement 2).
var terraformMutatingFlags = []string{
	"-auto-approve",
	"-destroy",
	"-replace=",
	"-replace ",
	"-target=",
	"-target ",
}

const terraformReadOnlyHelp = "`roksbnkctl terraform` is read-only. Mutations go through `roksbnkctl up`/`plan`/`apply`/`down` (or `cluster`/`bnk` up/down)."

// validateTerraformReadOnly enforces the allowlist + sub-verb guard +
// mutation-flag scrub. Returns a user-facing error (pointing at the
// lifecycle verbs) for anything that could mutate state; nil if the
// argv is a permitted read-only invocation. argv is the terraform argv
// with roksbnkctl flags (--phase/--on/-w) already stripped.
func validateTerraformReadOnly(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no terraform subcommand given. %s", terraformReadOnlyHelp)
	}
	sub := argv[0]
	if _, ok := terraformReadOnlyTop[sub]; !ok {
		return fmt.Errorf("`terraform %s` can mutate state or is not a supported read-only subcommand. %s", sub, terraformReadOnlyHelp)
	}

	// Sub-verb guard for `state`: only list|show|pull permitted.
	if sub == "state" {
		if len(argv) < 2 {
			return fmt.Errorf("`terraform state` needs a read-only sub-verb (list|show|pull). %s", terraformReadOnlyHelp)
		}
		stateSub := argv[1]
		if _, ok := terraformReadOnlyStateSub[stateSub]; !ok {
			return fmt.Errorf("`terraform state %s` can mutate state. Only `state list`, `state show`, `state pull` are permitted. %s", stateSub, terraformReadOnlyHelp)
		}
	}

	// `fmt` is read-only only with -check (otherwise it rewrites files).
	if sub == "fmt" && !terraformFmtIsReadOnly(argv[1:]) {
		return fmt.Errorf("`terraform fmt` rewrites files; only `terraform fmt -check` is permitted. %s", terraformReadOnlyHelp)
	}

	// Mutation-flag scrub on any allowlisted subcommand.
	for _, a := range argv[1:] {
		for _, bad := range terraformMutatingFlags {
			b := strings.TrimSpace(bad)
			if a == b || strings.HasPrefix(a, b) {
				return fmt.Errorf("flag %q implies a state mutation and is not permitted here. %s", a, terraformReadOnlyHelp)
			}
		}
	}
	return nil
}

// terraformFmtIsReadOnly reports whether a `terraform fmt` arg list
// includes -check (the only non-rewriting form).
func terraformFmtIsReadOnly(args []string) bool {
	for _, a := range args {
		if a == "-check" || a == "--check" || strings.HasPrefix(a, "-check=") {
			return true
		}
	}
	return false
}

// extractPhaseFlag pulls `--phase <p>` / `--phase=<p>` out of an
// otherwise-untouched argv (DisableFlagParsing means cobra won't claim
// it). Mirrors extractOnFlag. Returns ("", argv) when no --phase.
func extractPhaseFlag(args []string) (string, []string) {
	out := make([]string, 0, len(args))
	phase := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--phase":
			if i+1 < len(args) {
				phase = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--phase="):
			phase = strings.TrimPrefix(a, "--phase=")
		default:
			out = append(out, a)
		}
	}
	if len(out) > 0 && out[0] == "--" {
		out = out[1:]
	}
	return phase, out
}

func runTerraformPassthrough(cmd *cobra.Command, args []string) error {
	args = extractWorkspaceFlag(args)
	phase, args := extractPhaseFlag(args)
	on, argv := extractOnFlag(args)
	if on == "" {
		on = flagOn
	}

	// --on is explicitly rejected (not deferred): the managed terraform
	// state lives on this workstation, not on the jumphost. Running
	// terraform "over there" would operate on the wrong (or no) state.
	if on != "" {
		return fmt.Errorf("--on is not supported for `roksbnkctl terraform`: the managed terraform state is workstation-local, not on the jumphost. Run it locally; use `roksbnkctl --on <target> kubectl`/`oc`/`ibmcloud` for in-cluster-network passthroughs")
	}

	// Allowlist + sub-verb guard + mutation-flag scrub BEFORE terraform
	// is invoked / the workspace is even opened.
	if err := validateTerraformReadOnly(argv); err != nil {
		return err
	}

	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}

	stateDir, phaseLabel, err := terraformReadOnlyStateDir(cctx.WorkspaceName, phase)
	if err != nil {
		return err
	}

	tfws, err := tf.OpenReadOnly(cmd.Context(), cctx.WorkspaceName, cctx.Workspace, stateDir)
	if err != nil {
		if errors.Is(err, tf.ErrNoState) {
			return fmt.Errorf("workspace has no terraform state for phase %s; run `roksbnkctl up` first", phaseLabel)
		}
		return err
	}

	out, runErr := tfws.RunReadOnly(cmd.Context(), argv)
	if out != "" {
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
	}
	if runErr != nil {
		// terraform already streamed its diagnostics to stderr; surface
		// a non-zero exit without double-printing.
		if ee, ok := runErr.(interface{ ExitCode() int }); ok {
			os.Exit(ee.ExitCode())
		}
		return runErr
	}
	return nil
}

// terraformReadOnlyStateDir resolves the state dir for the requested
// phase, reusing the same config helpers the lifecycle uses
// (config.WorkspaceStateDir / WorkspaceClusterStateDir) — the CLI layer
// must NOT re-derive terraform's cwd/TF_DATA_DIR. Returns the dir + a
// human label for error messages.
func terraformReadOnlyStateDir(workspace, phase string) (string, string, error) {
	switch phase {
	case "", "trial", "bnk", "single", "default":
		dir, err := config.WorkspaceStateDir(workspace)
		return dir, "default", err
	case "cluster":
		dir, err := config.WorkspaceClusterStateDir(workspace)
		return dir, "cluster", err
	default:
		return "", "", fmt.Errorf("unknown --phase %q (want `cluster` or omit for the default trial/single state)", phase)
	}
}
