package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
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
	rootCmd.AddCommand(shellCmd, execCmd, kubeconfigCmd, kubectlCmd, ocCmd, ibmcloudCmd)
}

// ── runE implementations ────────────────────────────────────────────

func runShell(cmd *cobra.Command, _ []string) error {
	_, env, err := workspaceEnv()
	if err != nil {
		return err
	}
	if flagOn != "" {
		// Remote shell. Always TTY — that's the point of `shell`.
		return dispatchRemoteShell(cmd.Context(), flagOn)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return runWithEnv(shell, nil, env)
}

func runExec(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("exec requires a command to run")
	}
	// `roksbnkctl exec` uses DisableFlagParsing so cobra doesn't grab
	// flags meant for the wrapped binary. That means --on may show up
	// in args; pull it out manually before dispatch. Also splits at
	// the canonical `--` separator if the user adds one for clarity.
	on, argv := extractOnFlag(args)
	if on == "" {
		on = flagOn
	}
	_, env, err := workspaceEnv()
	if err != nil {
		return err
	}
	if on != "" {
		return dispatchRemote(cmd.Context(), on, argv, env, false)
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("%s not found on PATH", argv[0])
	}
	return runWithEnv(bin, argv[1:], env)
}

// extractOnFlag pulls `--on <name>` (or `--on=<name>`) out of an
// otherwise-untouched argv. Necessary because exec runs with
// DisableFlagParsing so cobra doesn't claim flags meant for the
// wrapped command. Returns ("", argv) if no --on appears.
//
// Also strips a leading `--` separator if present after the on flag
// is removed — users who follow the canonical `roksbnkctl exec --on x -- ls`
// form expect the `--` to disappear.
func extractOnFlag(args []string) (string, []string) {
	out := make([]string, 0, len(args))
	on := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--on":
			if i+1 < len(args) {
				on = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--on="):
			on = strings.TrimPrefix(a, "--on=")
		default:
			out = append(out, a)
		}
	}
	if len(out) > 0 && out[0] == "--" {
		out = out[1:]
	}
	return on, out
}

// extractBackendFlag is the Sprint-3 sibling of extractOnFlag. Same
// rationale: the passthrough commands disable cobra's flag parsing so
// downstream tool flags (e.g. `roksbnkctl ibmcloud --debug ks cluster
// ls`) reach the wrapped binary verbatim. Side-effect: the persistent
// `--backend` flag is also swallowed into args when placed AFTER the
// subcommand name, so we extract it manually.
//
// `--backend` placed BEFORE the subcommand name (e.g. `roksbnkctl
// --backend docker ibmcloud ks cluster ls`) hits cobra's persistent
// flag path normally — extractBackendFlag returns "" in that case
// and the runtime falls through to the flagBackend value cobra
// already set.
func extractBackendFlag(args []string) (string, []string) {
	out := make([]string, 0, len(args))
	be := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--backend":
			if i+1 < len(args) {
				be = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--backend="):
			be = strings.TrimPrefix(a, "--backend=")
		default:
			out = append(out, a)
		}
	}
	if len(out) > 0 && out[0] == "--" {
		out = out[1:]
	}
	return be, out
}

func runKubeconfig(cmd *cobra.Command, _ []string) error {
	if flagKubeconfigDownload {
		return runKubeconfigDownload(cmd)
	}

	path := k8s.DefaultKubeconfigPath()
	if path == "" {
		return fmt.Errorf("no kubeconfig found; run `roksbnkctl kubeconfig --download` or `ibmcloud ks cluster config --admin -c <cluster>`")
	}
	if flagExportKubeconfig {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	}
	fmt.Println(path)
	return nil
}

// runKubeconfigDownload fetches the admin kubeconfig directly from
// IBM's container service and saves it to $KUBECONFIG (or ~/.kube/config).
// Picks the target cluster from (in order):
//
//  1. --cluster flag (explicit user override)
//  2. terraform output `roks_cluster_id` from the workspace state
//     (post-apply truth, beats config.yaml when --var-file overrode
//     the cluster name)
//  3. terraform output `roks_cluster_name`
//  4. workspace config.yaml's cluster.name (pre-apply fallback)
func runKubeconfigDownload(cmd *cobra.Command) error {
	cctx, ic, err := openIBMClient()
	if err != nil {
		return err
	}

	cluster := flagKubeconfigCluster
	if cluster == "" {
		// Try terraform output first (catches --var-file-overridden names).
		cluster = clusterFromTFOutput(cmd.Context(), cctx)
	}
	if cluster == "" && cctx.Workspace != nil {
		cluster = cctx.Workspace.Cluster.Name
	}
	if cluster == "" {
		return fmt.Errorf("--cluster required (or set cluster.name in the workspace config, or run after `roksbnkctl up`)")
	}

	fmt.Fprintf(os.Stderr, "→ Fetching admin kubeconfig for %q\n", cluster)
	body, err := ic.FetchClusterConfig(cmd.Context(), cluster)
	if err != nil {
		return err
	}

	target := k8s.DefaultKubeconfigPath()
	if target == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return fmt.Errorf("resolving home directory: %w", herr)
		}
		target = filepath.Join(home, ".kube", "config")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	// 0o600 — kubeconfig has cluster admin certs.
	if err := os.WriteFile(target, body, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	fmt.Fprintf(os.Stderr, "✓ Wrote %s (%d bytes)\n", target, len(body))
	return nil
}

func runKubectlPassthrough(cmd *cobra.Command, args []string) error {
	return runPassthrough(cmd, "kubectl", args)
}

func runOCPassthrough(cmd *cobra.Command, args []string) error {
	return runPassthrough(cmd, "oc", args)
}

func runIBMCloudPassthrough(cmd *cobra.Command, args []string) error {
	on, argv := extractOnFlag(args)
	if on == "" {
		on = flagOn
	}
	// Pull --backend out of argv too; same DisableFlagParsing rationale
	// as extractOnFlag. flagBackend (the persistent flag value cobra
	// set when --backend appeared BEFORE the subcommand) wins over the
	// extracted form for backwards-compat with the cobra path.
	be, argv := extractBackendFlag(argv)
	if be == "" {
		be = flagBackend
	}
	cctx, env, err := workspaceEnv()
	if err != nil {
		return err
	}
	if on != "" {
		// Remote ibmcloud — skip the local-session ensureLoggedIn
		// dance; the remote sshd / target manages its own state. Pass
		// IBMCLOUD_API_KEY via env so the remote `ibmcloud` CLI does
		// non-interactive apikey login on first call.
		//
		// PRD 03 + PLAN.md note: --on (Sprint 1's SSH dispatch) and
		// --backend (Sprint 3's backend selector) are independent
		// flags in v0.8; --on takes the legacy SSH path here. Sprint
		// 4 folds the two together under a real `ssh` backend.
		return dispatchRemote(cmd.Context(), on, append([]string{"ibmcloud"}, argv...), env, false)
	}

	// Resolve the execution backend for ibmcloud. Per PLAN.md Sprint 3
	// §"Workspace config exec: block + --backend CLI flag":
	//
	//   1. --backend flag (if set) wins.
	//   2. Workspace exec.<tool>.backend (if set).
	//   3. Per-tool default (Sprint 4: ibmcloud=local).
	//   4. Default "local".
	backendSpec := resolveBackendSpecWith(cctx, "ibmcloud", be)
	if backendSpec == "local" || backendSpec == "" {
		// Fast path — byte-identical to pre-Sprint-3 behaviour.
		bin, err := exec.LookPath("ibmcloud")
		if err != nil {
			return fmt.Errorf("ibmcloud not found on PATH (install it to use `roksbnkctl ibmcloud`)")
		}
		if err := ensureIBMCloudLoggedIn(bin, env); err != nil {
			return err
		}
		return runWithEnv(bin, argv, env)
	}

	// Non-local backend dispatch. ibmcloud over k8s uses the long-lived
	// ops pod path (PRD 03 §"K8s"); other backends are stateless one-shot.
	longLived := backendSpec == "k8s"
	return dispatchBackend(cmd.Context(), backendSpec, "ibmcloud", argv, cctx, env, longLived)
}

// perToolDefaultBackend is the per-tool default backend table (Sprint 4
// §"Tool migration plan" in PRD 03). Tools not present default to
// "local". The map is consulted only when neither --backend flag nor
// workspace exec.<tool>.backend is set.
//
// PRD 03 §"iperf3" puts the default at k8s — the reproducible-toolchain
// + in-cluster-network-locality wins. ibmcloud and terraform stay local
// for v1; users opt in to docker / k8s / ssh per invocation.
var perToolDefaultBackend = map[string]string{
	"iperf3":    "k8s",
	"ibmcloud":  "local",
	"terraform": "local",
}

// resolveBackendSpecWith picks the execution backend for tool. Order:
//
//  1. flagOverride (the explicit per-invocation flag — caller passes
//     either flagBackend or the extractBackendFlag result, whichever
//     is non-empty)
//  2. workspace's exec.<tool>.backend
//  3. perToolDefaultBackend[tool] (Sprint 4)
//  4. "local" default
//
// Returns the spec string ("local", "docker", "k8s", "ssh:<target>")
// — the caller passes it into exec.ResolveBackend.
func resolveBackendSpecWith(cctx *config.Context, tool, flagOverride string) string {
	if flagOverride != "" {
		return flagOverride
	}
	if cctx != nil && cctx.Workspace != nil {
		if entry, ok := cctx.Workspace.Exec[tool]; ok && entry.Backend != "" {
			return entry.Backend
		}
	}
	if def, ok := perToolDefaultBackend[tool]; ok {
		return def
	}
	return "local"
}

// dispatchBackend resolves the named backend spec, builds Credentials
// for the wrapped tool, and runs argv through exec.Backend.Run. Used
// for the non-local execution paths in Sprint 3 (`--backend docker`)
// and Sprint 4's k8s + ssh.
//
// argv[0] is the tool name (e.g. "ibmcloud") which the docker backend
// uses to look up the per-tool image; for the local backend (which
// reaches here only on explicit --backend=local) argv[0] is the
// binary on PATH.
//
// Sprint 4 additions:
//
//   - longLived: set to true for the k8s ops-pod path (ibmcloud,
//     ad-hoc shells). False for one-shot Job invocations like the
//     iperf3 client. Communicated to the k8s backend via a sentinel
//     env entry (see exec.k8sLongLivedKey).
//   - ssh:<target>: the spec-target form pushes the target name into
//     the env via ROKSBNKCTL_SSH_TARGET so the SSH backend can resolve
//     without reading the spec string itself.
func dispatchBackend(ctx context.Context, spec, tool string, argv []string, cctx *config.Context, env []string, longLived bool) error {
	backend, err := execbackend.ResolveBackend(spec)
	if err != nil {
		return err
	}

	// Build Credentials. Sprint 3 only wires IBM Cloud API key; the
	// kubeconfig wiring is reserved for the k8s/ssh backends in
	// Sprint 4 (docker rarely needs cluster-side kubeconfig in the
	// ibmcloud-passthrough use case — `ibmcloud ks cluster ls` etc.
	// don't read kubeconfig).
	creds := &execbackend.Credentials{}
	if cctx != nil && cctx.Workspace != nil {
		resolver := &cred.Resolver{
			Workspace:      cctx.WorkspaceName,
			NonInteractive: true, // backends shouldn't prompt mid-run
			Source:         cctx.Workspace.IBMCloud.APIKeySource,
		}
		key, kerr := resolver.IBMCloudAPIKey(ctx)
		if kerr != nil {
			return fmt.Errorf("resolving IBM Cloud API key: %w", kerr)
		}
		creds.IBMCloudAPIKey = key
	}

	// Filter env: don't double-feed IBMCLOUD_API_KEY — the
	// Credentials path owns that value. Other workspace env entries
	// (KUBECONFIG, IBMCLOUD_REGION, etc.) flow through.
	var filteredEnv []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "IBMCLOUD_API_KEY=") || strings.HasPrefix(kv, "IC_API_KEY=") {
			continue
		}
		filteredEnv = append(filteredEnv, kv)
	}

	// Sprint 4: pass the k8s long-lived sentinel + ssh target sentinel
	// through env. The respective backends strip them before exec.
	if longLived {
		filteredEnv = append(filteredEnv, "ROKSBNKCTL_K8S_LONG_LIVED=1")
	}
	if target := execbackend.SpecTarget(spec); target != "" && strings.HasPrefix(spec, "ssh:") {
		filteredEnv = append(filteredEnv, "ROKSBNKCTL_SSH_TARGET="+target)
		// Ensure the SSH backend has the workspace + bootstrap flag wired.
		execbackend.SetSSHOpts(execbackend.SSHBackendOpts{
			Workspace:       cctx.WorkspaceName,
			Bootstrap:       flagBootstrap,
			InsecureHostKey: flagInsecureHostKey,
		})
	}

	rc, err := backend.Run(ctx, append([]string{tool}, argv...), execbackend.RunOpts{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Env:         filteredEnv,
		Credentials: creds,
	})
	if err != nil && rc == 0 {
		return err
	}
	if rc != 0 {
		os.Exit(rc)
	}
	return nil
}

// ensureIBMCloudLoggedIn establishes a valid ibmcloud session before
// passthrough commands run. Stateful CLI commands (ks, target, etc.)
// fail with "Log in to the IBM Cloud CLI" if no session is cached
// in ~/.bluemix/, even with IBMCLOUD_API_KEY set in env.
//
// Strategy:
//  1. Probe with `ibmcloud account show` (fast, no side effects).
//  2. If that succeeds, session is good — skip login.
//  3. Otherwise run `ibmcloud login -r <region>` with IBMCLOUD_API_KEY
//     in env. The CLI does non-interactive apikey login when the env
//     var is set.
//
// Login output is streamed to stderr so users see what's happening
// when roksbnkctl is taking the extra second.
func ensureIBMCloudLoggedIn(bin string, env []string) error {
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := exec.CommandContext(probeCtx, bin, "account", "show")
	probe.Env = env
	if err := probe.Run(); err == nil {
		return nil
	}
	fmt.Fprintln(os.Stderr, "→ ibmcloud login")
	loginArgs := []string{"login"}
	if region := envValue(env, "IBMCLOUD_REGION"); region != "" {
		loginArgs = append(loginArgs, "-r", region)
	}
	login := exec.Command(bin, loginArgs...)
	login.Env = env
	login.Stdout = os.Stderr
	login.Stderr = os.Stderr
	if err := login.Run(); err != nil {
		return fmt.Errorf("ibmcloud login failed: %w", err)
	}
	return nil
}

// envValue is a small helper for reading a key out of an env slice
// (KEY=VALUE strings). Returns "" if not found.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

func runPassthrough(cmd *cobra.Command, tool string, args []string) error {
	on, argv := extractOnFlag(args)
	if on == "" {
		on = flagOn
	}
	_, env, err := workspaceEnv()
	if err != nil {
		return err
	}
	if on != "" {
		return dispatchRemote(cmd.Context(), on, append([]string{tool}, argv...), env, false)
	}
	bin, err := exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%s not found on PATH (install it to use `roksbnkctl %s`)", tool, tool)
	}
	return runWithEnv(bin, argv, env)
}

// ── helpers ─────────────────────────────────────────────────────────

// workspaceEnv composes the env a child process should inherit:
// host env + workspace's IBMCLOUD_API_KEY / IC_API_KEY / IBMCLOUD_REGION
// + KUBECONFIG (from the host's lookup chain — v1 doesn't auto-fetch).
//
// Returns the resolved Context too in case the caller wants to log
// "loaded workspace foo" before exec'ing.
func workspaceEnv() (*config.Context, []string, error) {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}

	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("resolving API key: %w", err)
	}

	env := os.Environ()
	env = append(env, "IBMCLOUD_API_KEY="+apiKey)
	env = append(env, "IC_API_KEY="+apiKey)
	if r := cctx.Workspace.IBMCloud.Region; r != "" {
		env = append(env, "IBMCLOUD_REGION="+r)
	}
	if path := k8s.DefaultKubeconfigPath(); path != "" {
		env = append(env, "KUBECONFIG="+path)
	}
	// Silence the "New plug-in version available" / "TIP: --check-version"
	// banner the ibmcloud CLI prints on every invocation. It's noise the
	// user can't act on inside the roksbnkctl flow.
	env = append(env, "IBMCLOUD_VERSION_CHECK=false")
	return cctx, env, nil
}

// runWithEnv runs bin with args + env, wired to the host's stdin/out/err,
// and propagates the child's exit code. Cross-platform — uses os/exec
// rather than syscall.Exec so it works on Windows.
func runWithEnv(bin string, args, env []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

// clusterFromTFOutput attempts to read the post-apply cluster identity
// from terraform's outputs (roks_cluster_id then roks_cluster_name).
// Returns "" silently on any failure — caller falls back to config.yaml.
//
// Opens a minimal tf.Workspace (no source fetch beyond what's already
// resolved on disk; no API key needed) just to call Output.
func clusterFromTFOutput(ctx context.Context, cctx *config.Context) string {
	if cctx == nil || cctx.Workspace == nil {
		return ""
	}
	stateDir, err := config.WorkspaceStateDir(cctx.WorkspaceName)
	if err != nil {
		return ""
	}
	// Open without an API key — terraform output doesn't need creds and
	// we don't want to trigger a prompt here. Use io.Discard equivalents
	// for stdout/stderr — these aren't user-facing surfaces.
	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, "", nil, nil)
	if err != nil {
		return ""
	}
	outputs, err := tfws.Output(ctx)
	if err != nil {
		return ""
	}
	for _, key := range []string{"roks_cluster_id", "roks_cluster_name"} {
		if om, ok := outputs[key]; ok && len(om.Value) > 0 {
			var s string
			if json.Unmarshal(om.Value, &s) == nil && s != "" {
				return s
			}
		}
	}
	return ""
}
