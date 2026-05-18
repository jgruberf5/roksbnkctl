package cli

// Sprint 16 consolidation phase-1b: the cluster / remote-passthrough
// RunE orchestration (shell / exec / kubeconfig[-download] / the
// kubectl|oc|ibmcloud passthroughs + the backend-dispatch /
// ibmcloud-login / env / tf-output / extract*Flag helpers) moved
// verbatim into internal/orchestration. What remains here is the thin
// cobra adapter: the command definitions, flag binding, the
// workspaceEnv/workspaceEnvCore/remoteSafeEnv chokepoint wrappers
// (frozen — pinned by env_split_test.go / lifecycle_e2e_test.go), and
// RunE shims that build an orchestration.ClusterInputs (flag globals +
// the cli-resident collaborators injected as function fields — no
// orchestration → cli import) and delegate. Behavior is byte-for-byte
// preserved.
//
// extractWorkspaceFlag / extractOnFlag / resolveBackendSpecWith /
// clusterFromTFOutput keep their original cli-package names + signatures
// because the frozen (out-of-phase-1b-scope) terraform.go / test.go /
// remote.go call them; they delegate to the moved orchestration
// implementations. extractWorkspaceFlag preserves the in-place
// flagWorkspace global mutation it always did.

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
)

var (
	flagExportKubeconfig   bool
	flagKubeconfigDownload bool
	flagKubeconfigCluster  string
)

var shellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Interactive bash with KUBECONFIG, IBMCLOUD_API_KEY, and region pre-loaded",
	Long: `roksbnkctl shell drops into a $SHELL subshell with the workspace's
KUBECONFIG, IBMCLOUD_API_KEY, IC_API_KEY, and IBMCLOUD_REGION exported so
locally-installed kubectl / oc / ibmcloud commands work without further
setup. Exits when the subshell does.`,
	RunE: runShell,
}

var execCmd = &cobra.Command{
	Use:                "exec [command...]",
	Short:              "Run a single command with cluster context loaded",
	Args:               cobra.MinimumNArgs(1),
	DisableFlagParsing: true,
	RunE:               runExec,
}

var kubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig",
	Short: "Print the kubeconfig path (or contents with --export)",
	RunE:  runKubeconfig,
}

// Passthrough commands — DisableFlagParsing so cobra doesn't grab flags
// intended for the wrapped tool (e.g. `roksbnkctl kubectl get pods --all-namespaces`).
var kubectlCmd = &cobra.Command{
	Use:                "kubectl [args...]",
	Short:              "Passthrough to local kubectl with workspace KUBECONFIG loaded",
	DisableFlagParsing: true,
	RunE:               runKubectlPassthrough,
}

var ocCmd = &cobra.Command{
	Use:                "oc [args...]",
	Short:              "Passthrough to local oc with workspace KUBECONFIG loaded",
	DisableFlagParsing: true,
	RunE:               runOCPassthrough,
}

var ibmcloudCmd = &cobra.Command{
	Use:                "ibmcloud [args...]",
	Short:              "Passthrough to local ibmcloud with workspace API key + region loaded",
	DisableFlagParsing: true,
	RunE:               runIBMCloudPassthrough,
}

func init() {
	kubeconfigCmd.Flags().BoolVar(&flagExportKubeconfig, "export", false, "print kubeconfig contents instead of path")
	kubeconfigCmd.Flags().BoolVar(&flagKubeconfigDownload, "download", false, "fetch admin kubeconfig from IBM Cloud and save to ~/.kube/config")
	kubeconfigCmd.Flags().StringVar(&flagKubeconfigCluster, "cluster", "", "cluster name or ID for --download (default: workspace cluster.name)")
	rootCmd.AddCommand(shellCmd, execCmd, kubeconfigCmd, kubectlCmd, ocCmd, ibmcloudCmd, terraformCmd)
}

// ── env chokepoint wrappers (frozen — pinned by the parity harness) ──
//
// workspaceEnv / workspaceEnvCore / remoteSafeEnv are the cli-layer
// thin wrappers over the single env-classification chokepoint in
// internal/orchestration. They exist so the cobra adapter (and the
// in-package tests that pin the core-vs-local-only split) call a stable
// local symbol while the canonical composition + the one
// LocalOnlyEnvKeys classification live in orchestration. They read the
// resolved persistent --workspace flag and forward it; they add NO
// logic of their own (the Sprint 13 Issue 1 boundary correctness is the
// orchestration package's single ScrubLocalOnly classification).

// workspaceEnv composes the LOCAL-exec env (machine-portable core +
// the local-only KUBECONFIG addendum). KUBECONFIG is a host filesystem
// path that is meaningless on an SSH target — callers crossing the --on
// boundary MUST use workspaceEnvCore().
func workspaceEnv() (*config.Context, []string, error) {
	return orchestration.WorkspaceEnv(flagWorkspace)
}

// workspaceEnvCore composes the machine-portable subset that is the
// ONLY workspace-derived env safe to cross the --on SSH boundary; it
// deliberately omits KUBECONFIG (a local path), including any inherited
// from the shell (Sprint 13 Issue 1).
func workspaceEnvCore() (*config.Context, []string, error) {
	return orchestration.WorkspaceEnvCore(flagWorkspace)
}

// remoteSafeEnv strips every local-path-valued var (the single
// orchestration.LocalOnlyEnvKeys classification) from env. It is the
// one boundary assertion applied at the SSH wire in dispatchRemote.
func remoteSafeEnv(env []string) []string {
	return orchestration.ScrubLocalOnly(env)
}

// ── cli-side wrappers for the (frozen) cross-file consumers ──────────
//
// terraform.go / test.go / remote.go are out of phase-1b scope and must
// stay byte-unchanged; they still call these by their original
// cli-package names + signatures. They delegate to the moved
// orchestration implementations; behavior is identical.

// extractWorkspaceFlag preserves its original single-return shape AND
// the in-place flagWorkspace global mutation it always performed: it
// pulls -w/--workspace out of the DisableFlagParsing argv and, when
// present, writes flagWorkspace so the subsequent workspaceEnv()
// resolves to the right workspace.
func extractWorkspaceFlag(args []string) []string {
	ws, out := orchestration.ExtractWorkspaceFlag(args)
	if ws != "" {
		flagWorkspace = ws
	}
	return out
}

// extractOnFlag retains its original cli-package name + signature for
// terraform.go (frozen). Delegates to the moved splitter.
func extractOnFlag(args []string) (string, []string) {
	return orchestration.ExtractOnFlag(args)
}

// resolveBackendSpecWith retains its original cli-package name +
// signature for test.go's iperf3 backend resolution (frozen).
func resolveBackendSpecWith(cctx *config.Context, tool, flagOverride string) string {
	return orchestration.ResolveBackendSpecWith(cctx, tool, flagOverride)
}

// clusterFromTFOutput retains its original cli-package name + signature
// for remote.go's --on cluster-identity resolution (frozen).
func clusterFromTFOutput(ctx context.Context, cctx *config.Context) string {
	return orchestration.ClusterFromTFOutput(ctx, cctx)
}

// ── ClusterInputs assembly ──────────────────────────────────────────

// clusterInputs builds the resolved-invocation context the moved
// cluster / remote-passthrough orchestration consumes, replacing the
// package-global `flag*` reads the code did while it lived here. The
// cli-resident collaborators (the env chokepoint wrappers, the SSH
// dispatch, the remote shell, the IBM client opener) are injected as
// function fields so internal/orchestration never imports internal/cli.
// SetWorkspace lets the DisableFlagParsing -w extraction mutate the
// flagWorkspace global exactly as before the move.
func clusterInputs() *orchestration.ClusterInputs {
	return &orchestration.ClusterInputs{
		Workspace:          flagWorkspace,
		On:                 flagOn,
		Backend:            flagBackend,
		Bootstrap:          flagBootstrap,
		InsecureHostKey:    flagInsecureHostKey,
		ExportKubeconfig:   flagExportKubeconfig,
		KubeconfigDownload: flagKubeconfigDownload,
		KubeconfigCluster:  flagKubeconfigCluster,

		SetWorkspace:        func(ws string) { flagWorkspace = ws },
		WorkspaceEnv:        workspaceEnv,
		WorkspaceEnvCore:    workspaceEnvCore,
		DispatchRemote:      dispatchRemote,
		DispatchRemoteShell: dispatchRemoteShell,
		OpenIBMClient:       openIBMClient,
	}
}

// ── thin cobra RunE shims ───────────────────────────────────────────

func runShell(cmd *cobra.Command, _ []string) error {
	return orchestration.RunShell(cmd.Context(), clusterInputs())
}

func runExec(cmd *cobra.Command, args []string) error {
	return orchestration.RunExec(cmd.Context(), clusterInputs(), args)
}

func runKubeconfig(cmd *cobra.Command, _ []string) error {
	return orchestration.RunKubeconfig(cmd.Context(), clusterInputs())
}

func runKubectlPassthrough(cmd *cobra.Command, args []string) error {
	return orchestration.RunKubectlPassthrough(cmd.Context(), clusterInputs(), args)
}

func runOCPassthrough(cmd *cobra.Command, args []string) error {
	return orchestration.RunOCPassthrough(cmd.Context(), clusterInputs(), args)
}

func runIBMCloudPassthrough(cmd *cobra.Command, args []string) error {
	return orchestration.RunIBMCloudPassthrough(cmd.Context(), clusterInputs(), args)
}
