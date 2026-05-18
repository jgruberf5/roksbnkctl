package orchestration

// Sprint 16 consolidation phase-1b: the cluster / remote-passthrough
// RunE orchestration (shell / exec / kubeconfig[-download] / the
// kubectl|oc|ibmcloud passthroughs + the backend-dispatch /
// ibmcloud-login / env / tf-output helpers + the DisableFlagParsing
// extract*Flag argv splitters) relocated verbatim out of
// internal/cli/cluster.go into this service layer. internal/cli is now a
// thin cobra adapter: it binds flags + persistent flags, builds a
// ClusterInputs once per command entry, and delegates here.
//
// The cli/cobra-resident collaborators the moved code calls (the SSH
// dispatch, the remote interactive shell, the IBM client opener) are
// injected as function fields on ClusterInputs rather than imported —
// the orchestration → cli boundary stays one-directional (asserted by
// the validator's import audit and the chokepoint guard test).
//
// extractWorkspaceFlag mutates the resolved --workspace; because the
// flag global lives in internal/cli, the cli shim performs that mutation
// against the package global and the moved splitter returns the parsed
// value + cleaned argv (behavior identical to the pre-move in-place
// global write).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// ClusterInputs is the resolved-invocation context the cobra adapter
// hands the cluster / remote-passthrough orchestration, replacing the
// package-level `flag*` globals the code read while it lived in
// internal/cli. The function fields inject the cli-resident
// collaborators (SSH dispatch, remote shell, IBM client opener) so this
// package never imports internal/cli.
type ClusterInputs struct {
	// Workspace is the resolved --workspace value (flagWorkspace). The
	// passthroughs run with DisableFlagParsing, so the cli shim has
	// already applied any extractWorkspaceFlag mutation to the global
	// before populating this.
	Workspace string
	// On is the resolved persistent --on value (flagOn).
	On string
	// Backend is the resolved persistent --backend value (flagBackend).
	Backend string
	// Bootstrap / InsecureHostKey are the persistent --bootstrap /
	// --insecure-host-key values (flagBootstrap / flagInsecureHostKey),
	// used only on the ssh:<target> backend dispatch path.
	Bootstrap       bool
	InsecureHostKey bool
	// ExportKubeconfig / KubeconfigDownload / KubeconfigCluster are the
	// `kubeconfig` subcommand flags (flagExportKubeconfig /
	// flagKubeconfigDownload / flagKubeconfigCluster).
	ExportKubeconfig   bool
	KubeconfigDownload bool
	KubeconfigCluster  string

	// SetWorkspace lets the DisableFlagParsing -w/--workspace extraction
	// mutate the cli-resident flagWorkspace global (it lives in
	// internal/cli) so a passthrough's later WorkspaceEnv() resolves to
	// the right workspace — exactly the pre-move in-place global write.
	SetWorkspace func(string)

	// WorkspaceEnv / WorkspaceEnvCore are the cli-resident env
	// chokepoint wrappers (cli.workspaceEnv / cli.workspaceEnvCore).
	// They stay in cli (frozen, pinned by env_split_test.go); injected
	// here so the moved code composes env exactly as before.
	WorkspaceEnv     func() (*config.Context, []string, error)
	WorkspaceEnvCore func() (*config.Context, []string, error)
	// DispatchRemote / DispatchRemoteShell are cli.dispatchRemote /
	// cli.dispatchRemoteShell (remote.go stays in cli per the scope —
	// it holds the single Sprint 15 SSH-boundary assertion). On success
	// dispatchRemote does not return (os.Exit with the remote rc).
	DispatchRemote      func(ctx context.Context, target string, argv []string, envExtra []string, tty bool) error
	DispatchRemoteShell func(ctx context.Context, target string) error
	// OpenIBMClient is cli.openIBMClient (cos.go stays in cli per the
	// scope) — used by the kubeconfig --download path.
	OpenIBMClient func() (*config.Context, *ibm.Client, error)
}

// ── runE implementations ────────────────────────────────────────────

func RunShell(ctx context.Context, in *ClusterInputs) error {
	_, env, err := in.WorkspaceEnv()
	if err != nil {
		return err
	}
	if in.On != "" {
		// Remote shell. Always TTY — that's the point of `shell`.
		return in.DispatchRemoteShell(ctx, in.On)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return runWithEnv(shell, nil, env)
}

func RunExec(ctx context.Context, in *ClusterInputs, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("exec requires a command to run")
	}
	// `roksbnkctl exec` uses DisableFlagParsing so cobra doesn't grab
	// flags meant for the wrapped binary. That means --on may show up
	// in args; pull it out manually before dispatch. Also splits at
	// the canonical `--` separator if the user adds one for clarity.
	on, argv := extractOnFlag(args)
	if on == "" {
		on = in.On
	}
	if on != "" {
		// Remote: machine-portable core only — never forward the local
		// KUBECONFIG path across the SSH boundary (Sprint 13 Issue 1).
		_, core, cerr := in.WorkspaceEnvCore()
		if cerr != nil {
			return cerr
		}
		return in.DispatchRemote(ctx, on, argv, core, false)
	}
	_, env, err := in.WorkspaceEnv()
	if err != nil {
		return err
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("%s not found on PATH", argv[0])
	}
	return runWithEnv(bin, argv[1:], env)
}

// ExtractWorkspaceFlag is the persistent-flag counterpart of
// extractOnFlag. With DisableFlagParsing=true on the passthrough
// subcommands, cobra doesn't consume the root's persistent flags
// either — `-w foo passthrough args` leaks `-w foo` through to the
// wrapped binary. This pulls `-w`/`--workspace` (and their `=VAL`
// shapes) out of argv. It returns the parsed workspace value (or "" if
// none) and the cleaned argv; the cli shim applies the value to the
// flagWorkspace global so workspaceEnv() resolves to the right
// workspace — behavior identical to the pre-move in-place global write.
func ExtractWorkspaceFlag(args []string) (string, []string) {
	out := make([]string, 0, len(args))
	ws := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-w" || a == "--workspace":
			if i+1 < len(args) {
				ws = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "-w="):
			ws = strings.TrimPrefix(a, "-w=")
		case strings.HasPrefix(a, "--workspace="):
			ws = strings.TrimPrefix(a, "--workspace=")
		default:
			out = append(out, a)
		}
	}
	if len(out) > 0 && out[0] == "--" {
		out = out[1:]
	}
	return ws, out
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

// ExtractOnFlag is the exported seam over extractOnFlag for the cli
// adapter (terraform.go stays in cli per the phase-1b scope and calls
// it for its DisableFlagParsing --on extraction). Behavior identical to
// the pre-move package-private helper.
func ExtractOnFlag(args []string) (string, []string) {
	return extractOnFlag(args)
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

func RunKubeconfig(ctx context.Context, in *ClusterInputs) error {
	if in.KubeconfigDownload {
		return runKubeconfigDownload(ctx, in)
	}

	path := k8s.DefaultKubeconfigPath()
	if path == "" {
		return fmt.Errorf("no kubeconfig found; run `roksbnkctl kubeconfig --download` or `ibmcloud ks cluster config --admin -c <cluster>`")
	}
	if in.ExportKubeconfig {
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
func runKubeconfigDownload(ctx context.Context, in *ClusterInputs) error {
	cctx, ic, err := in.OpenIBMClient()
	if err != nil {
		return err
	}

	cluster := in.KubeconfigCluster
	if cluster == "" {
		// Try terraform output first (catches --var-file-overridden names).
		cluster = ClusterFromTFOutput(ctx, cctx)
	}
	if cluster == "" && cctx.Workspace != nil {
		cluster = cctx.Workspace.Cluster.Name
	}
	if cluster == "" {
		return fmt.Errorf("--cluster required (or set cluster.name in the workspace config, or run after `roksbnkctl up`)")
	}

	fmt.Fprintf(os.Stderr, "→ Fetching admin kubeconfig for %q\n", cluster)
	body, err := ic.FetchClusterConfig(ctx, cluster)
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

func RunKubectlPassthrough(ctx context.Context, in *ClusterInputs, args []string) error {
	args = applyWorkspaceFlag(in, args)
	return runPassthrough(ctx, in, "kubectl", args)
}

func RunOCPassthrough(ctx context.Context, in *ClusterInputs, args []string) error {
	args = applyWorkspaceFlag(in, args)
	return runPassthrough(ctx, in, "oc", args)
}

func RunIBMCloudPassthrough(ctx context.Context, in *ClusterInputs, args []string) error {
	args = applyWorkspaceFlag(in, args)
	on, argv := extractOnFlag(args)
	if on == "" {
		on = in.On
	}
	// Pull --backend out of argv too; same DisableFlagParsing rationale
	// as extractOnFlag. flagBackend (the persistent flag value cobra
	// set when --backend appeared BEFORE the subcommand) wins over the
	// extracted form for backwards-compat with the cobra path.
	be, argv := extractBackendFlag(argv)
	if be == "" {
		be = in.Backend
	}
	if on != "" {
		// Remote ibmcloud — skip the local-session ensureLoggedIn
		// dance; the remote sshd / target manages its own state. Pass
		// IBMCLOUD_API_KEY via env so the remote `ibmcloud` CLI does
		// non-interactive apikey login on first call. Machine-portable
		// core only — never forward the local KUBECONFIG path across
		// the SSH boundary (Sprint 13 Issue 1).
		//
		// PRD 03 + PLAN.md note: --on (Sprint 1's SSH dispatch) and
		// --backend (Sprint 3's backend selector) are independent
		// flags in v0.8; --on takes the legacy SSH path here. Sprint
		// 4 folds the two together under a real `ssh` backend.
		_, core, cerr := in.WorkspaceEnvCore()
		if cerr != nil {
			return cerr
		}
		return in.DispatchRemote(ctx, on, append([]string{"ibmcloud"}, argv...), core, false)
	}
	cctx, env, err := in.WorkspaceEnv()
	if err != nil {
		return err
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
	return dispatchBackend(ctx, in, backendSpec, "ibmcloud", argv, cctx, env, longLived)
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

// ResolveBackendSpecWith is the exported seam over resolveBackendSpecWith
// for the cli adapter (test.go stays in cli per the phase-1b scope and
// calls it for the iperf3 backend resolution). Behavior identical to the
// pre-move package-private helper.
func ResolveBackendSpecWith(cctx *config.Context, tool, flagOverride string) string {
	return resolveBackendSpecWith(cctx, tool, flagOverride)
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
func dispatchBackend(ctx context.Context, in *ClusterInputs, spec, tool string, argv []string, cctx *config.Context, env []string, longLived bool) error {
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
			Bootstrap:       in.Bootstrap,
			InsecureHostKey: in.InsecureHostKey,
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

func runPassthrough(ctx context.Context, in *ClusterInputs, tool string, args []string) error {
	on, argv := extractOnFlag(args)
	if on == "" {
		on = in.On
	}
	if on != "" {
		// Remote: machine-portable core only — never forward the local
		// KUBECONFIG path across the SSH boundary (Sprint 13 Issue 1).
		_, core, cerr := in.WorkspaceEnvCore()
		if cerr != nil {
			return cerr
		}
		return in.DispatchRemote(ctx, on, append([]string{tool}, argv...), core, false)
	}
	_, env, err := in.WorkspaceEnv()
	if err != nil {
		return err
	}
	bin, err := exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%s not found on PATH (install it to use `roksbnkctl %s`)", tool, tool)
	}
	return runWithEnv(bin, argv, env)
}

// ── helpers ─────────────────────────────────────────────────────────

// applyWorkspaceFlag runs the DisableFlagParsing -w/--workspace
// extraction and, when a value was present, mutates the resolved
// --workspace via the injected setter so the subsequent WorkspaceEnv()
// resolves to the right workspace — the exact behavior of the pre-move
// in-place flagWorkspace global write, just routed through the cli
// adapter (which owns the flag global).
func applyWorkspaceFlag(in *ClusterInputs, args []string) []string {
	ws, out := ExtractWorkspaceFlag(args)
	if ws != "" {
		in.Workspace = ws
		if in.SetWorkspace != nil {
			in.SetWorkspace(ws)
		}
	}
	return out
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

// ClusterFromTFOutput is the exported seam over clusterFromTFOutput for
// the cli adapter (remote.go stays in cli per the phase-1b scope and
// calls it for the --on cluster-identity resolution). Behavior identical
// to the pre-move package-private helper.
func ClusterFromTFOutput(ctx context.Context, cctx *config.Context) string {
	return clusterFromTFOutput(ctx, cctx)
}
