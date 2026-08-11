package tf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/hashicorp/terraform-exec/tfexec"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// ErrNoState is returned by OpenReadOnly when the workspace phase has no
// terraform.tfstate — i.e. the phase was never applied. The read-only
// `roksbnkctl terraform` command surfaces this as a clean
// "run `roksbnkctl up` first" message and exits non-zero WITHOUT a
// source fetch or `terraform init` side effect (Sprint 13 Issue 2,
// hard requirement 4).
var ErrNoState = errors.New("workspace has no terraform state for this phase")

// Workspace ties together the terraform working directory (resolved TF
// source), the per-roksbnkctl-workspace state directory, and a configured
// terraform-exec handle that drives plan/apply/destroy.
//
// One Workspace per command invocation. Not safe for concurrent reuse.
type Workspace struct {
	name      string
	sourceDir string
	stateDir  string
	tf        *tfexec.Terraform
	// stderrCap tees terraform's stderr for post-failure diagnostic summarization
	// (nil when Open was given no stderr writer).
	stderrCap *diagCapture
}

// Open prepares a Workspace for terraform operations:
//
//   - Locates `terraform` on PATH; clear error if missing.
//   - Resolves the TF source via FetchSource (downloads if needed).
//   - Constructs a terraform-exec handle whose working dir is the resolved
//     (per-phase) source dir; terraform's .terraform/ data dir defaults
//     there. We do NOT set a process-global TF_DATA_DIR — that would race
//     between concurrent phases (see the note at the env block below).
//   - Exports apiKey as TF_VAR_ibmcloud_api_key in the env terraform sees.
//     The key is never written to disk by roksbnkctl.
//
// stdout/stderr (if non-nil) get terraform's streamed output. Pass
// os.Stdout / os.Stderr from CLI commands.
func Open(
	ctx context.Context,
	name string,
	wsCfg *config.Workspace,
	stateDir string,
	apiKey string,
	stdout, stderr io.Writer,
) (*Workspace, error) {
	if wsCfg == nil {
		return nil, fmt.Errorf("workspace config is nil (run `roksbnkctl init`)")
	}

	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		return nil, fmt.Errorf("terraform not found on PATH — install terraform >= 1.5 (https://developer.hashicorp.com/terraform/install)")
	}

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating state dir %s: %w", stateDir, err)
	}
	srcRoot := filepath.Join(stateDir, "tf-source")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		return nil, fmt.Errorf("creating tf-source dir %s: %w", srcRoot, err)
	}
	// Pre-create the per-module kubeconfig subdirs that
	// ibm_container_cluster_config writes into. The IBM provider does
	// NOT MkdirAll, so a missing leaf surfaces at plan time as
	// "Path: ..., to download the config doesn't exist". Doing this
	// here keeps it idempotent across plan/apply/destroy.
	kcDir := filepath.Join(stateDir, "kubeconfig")
	for _, sub := range []string{"cluster", "cert_manager", "cne_instance", "flo", "license", "gateway", "flp"} {
		if err := os.MkdirAll(filepath.Join(kcDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", filepath.Join(kcDir, sub), err)
		}
	}
	// FLO scratch dir + the f5-manifest subdir its local-exec provisioners
	// expect. Same MkdirAll-self defense — the upstream module's curl
	// and tar commands assume the parent exists.
	scratchDir := filepath.Join(stateDir, "scratch")
	for _, sub := range []string{"", "f5-manifest"} {
		p := filepath.Join(scratchDir, sub)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", p, err)
		}
	}
	// Point Helm and the admin-kubeconfig writer at writable, workspace-
	// relative paths under ROKSBNKCTL_HOME so neither depends on the
	// container's $HOME. In a fresh roksbnkctl runner $HOME often resolves
	// to an empty / non-writable path, which makes the helm provider's
	// repo-index download (e.g. cert-manager off charts.jetstack.io) fail
	// with "open <HOME>/.cache/helm/repository/<hash>-index.yaml: no such
	// file or directory", and the post-apply kubeconfig fetch warn with
	// "mkdir <HOME>: permission denied". Setting these on the process env
	// (the same mechanism as TF_PLUGIN_CACHE_DIR / TF_VAR_* below) routes
	// the helm cache + kubeconfig to the persisted workspace tree, which is
	// writable and survives the phased invocations. Best-effort: a failure
	// to create the dirs degrades to the $HOME-derived defaults rather than
	// failing the whole open.
	if err := prepareToolEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not prepare helm/kubeconfig env: %v\n", err)
	}

	// Fetch the source FOR this BNK line, so a release that needs different HCL
	// gets it without every other release having to move.
	sourceDir, err := FetchSourceForLine(ctx, wsCfg.TFSource, srcRoot, wsCfg.BNKLineOrEmpty())
	if err != nil {
		return nil, err
	}
	// Heal any non-executable provider binaries left in this source dir's
	// .terraform/providers by an earlier roksbnkctl build that extracted
	// the embedded provider cache 0644 — otherwise terraform "reuses" them
	// and the plan dies with "fork/exec ...: permission denied". No-op for
	// terraform's own 0755 installs.
	EnsureProvidersExecutable(sourceDir)

	// Write a backend override pointing terraform's local backend at our
	// workspace state file. Replaces the deprecated `-state=<path>` flag
	// (terraform-exec's tfexec.State()) which prints:
	//   Warning: Deprecated flag: -state
	// on every apply/plan/destroy.
	//
	// "*_override.tf" filename triggers terraform's override-file merge
	// semantics, so this works whether the upstream config has a backend
	// block already or not. Each workspace has its own resolved
	// sourceDir under <stateDir>/tf-source so the override is per-workspace.
	backendOverride := filepath.Join(sourceDir, "roksbnkctl_backend_override.tf")
	backendBody, useS3, err := backendOverrideHCL(wsCfg.State, name, stateDir)
	if err != nil {
		return nil, err
	}
	if useS3 {
		// The s3 backend's native lockfile (the only COS-compatible lock —
		// COS has no DynamoDB) needs terraform >= 1.10. Fail fast here, not
		// mid-apply (PRD 16).
		if err := requireTerraformVersion(ctx, tfBin, 1, 10); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(backendOverride, []byte(backendBody), 0o644); err != nil {
		return nil, fmt.Errorf("writing backend override: %w", err)
	}

	tf, err := tfexec.NewTerraform(sourceDir, tfBin)
	if err != nil {
		return nil, fmt.Errorf("initialising terraform-exec: %w", err)
	}

	if stdout != nil {
		tf.SetStdout(stdout)
	}
	// Tee terraform's stderr through a bounded capture so a failed apply/destroy
	// can print a deduplicated diagnostic summary (live streaming is unchanged).
	var stderrCap *diagCapture
	if stderr != nil {
		stderrCap = newDiagCapture(stderr, 128*1024)
		tf.SetStderr(stderrCap)
	}

	// terraform-exec inherits the roksbnkctl process env when SetEnv is
	// NOT called. We deliberately don't call SetEnv: it explicitly
	// rejects TF_VAR_* keys ("manual setting of env var TF_VAR_X
	// detected") AND replaces (rather than merges) the inherited env, so
	// routing apiKey through it is impossible without exposing the key in
	// argv via a `tfexec.Var()` option (visible in `ps`). We therefore
	// keep apiKey on the process env as TF_VAR_ibmcloud_api_key — a
	// process-global set, but every phase of a given workspace uses the
	// SAME key, so the concurrent BNK∥Testing applies race only to an
	// identical value (benign).
	//
	// We deliberately do NOT set a process-global TF_DATA_DIR. It used to
	// point .terraform/ at <stateDir>/terraform, but a process-global is
	// unsafe once phases apply concurrently (Sprint 28): the BNK and
	// Testing goroutines would clobber each other's TF_DATA_DIR between
	// os.Setenv and the terraform child spawn, so one phase's apply would
	// init against the other phase's backend — surfacing as
	// "Backend configuration block has changed". Instead we let terraform
	// default .terraform/ into the working dir (sourceDir), which is
	// per-phase (<stateDir>/tf-source/...) and therefore already isolated.
	// The backend override pins state to an absolute
	// <stateDir>/terraform.tfstate, so the data-dir location doesn't move
	// the state file, and FetchSource rewrites source files over the top
	// (no RemoveAll), so .terraform/ persists across `up`s.
	// Shared provider plugin cache (TF_PLUGIN_CACHE_DIR): each provider
	// downloads ONCE into ~/.roksbnkctl/plugin-cache and every phase /
	// workspace links it from there, instead of re-fetching ~440 MB of
	// providers per phase. Process-global like the api key below, but the
	// same value for every phase; the parallel `up` inits each phase
	// sequentially (before the concurrent apply), so there's no concurrent
	// cache write. terraform ignores TF_PLUGIN_CACHE_DIR unless the dir
	// already exists, so MkdirAll it; degrade silently to per-phase
	// downloads if we can't create it.
	if cacheDir, cerr := config.PluginCacheDir(); cerr == nil {
		if os.MkdirAll(cacheDir, 0o755) == nil {
			_ = os.Setenv("TF_PLUGIN_CACHE_DIR", cacheDir)
			// We ship no committed lock file, so let the cache populate the
			// per-workspace lock with single-platform (h1:) hashes instead
			// of erroring on the absent zip (zh:) checksums.
			_ = os.Setenv("TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE", "1")
		}
	}
	if apiKey != "" {
		if err := os.Setenv("TF_VAR_ibmcloud_api_key", apiKey); err != nil {
			return nil, fmt.Errorf("setting TF_VAR_ibmcloud_api_key: %w", err)
		}
	}
	// Hand terraform this binary's own path. helm invokes it as the
	// f5-license-proxy chart's post-renderer (`roksbnkctl flp postrender`), so the
	// FLP install needs no interpreter on the host — it used to generate a python
	// script, which is why `flp up` died inside the tools-runner container, where
	// there is no python at all. Passing the resolved path (not the bare name)
	// guarantees helm post-renders with the exact build driving the apply.
	if exe, err := os.Executable(); err == nil && exe != "" {
		if err := os.Setenv("TF_VAR_roksbnkctl_binary", exe); err != nil {
			return nil, fmt.Errorf("setting TF_VAR_roksbnkctl_binary: %w", err)
		}
	}
	if useS3 {
		// COS HMAC keys for the s3 backend, injected as AWS_* env (the
		// backend reads them) — never into the rendered HCL or state.
		// Process-global like the api key above; the same COS creds serve
		// every phase, so concurrent applies race only on identical values.
		access, secret, herr := resolveCOSHMAC(wsCfg.State.S3)
		if herr != nil {
			return nil, herr
		}
		_ = os.Setenv("AWS_ACCESS_KEY_ID", access)
		_ = os.Setenv("AWS_SECRET_ACCESS_KEY", secret)
	}

	return &Workspace{
		name:      name,
		sourceDir: sourceDir,
		stateDir:  stateDir,
		tf:        tf,
		stderrCap: stderrCap,
	}, nil
}

// prepareToolEnv exports writable, ROKSBNKCTL_HOME-relative paths for
// Helm's cache/config/data homes and the admin kubeconfig, and pre-creates
// the dirs they need. The terraform helm provider runs as a plugin
// subprocess that inherits this env (terraform-exec passes os.Environ()
// through), so setting these here reaches the chart download. The leaf-dir
// pre-creation mirrors the kubeconfig/scratch pre-creation in Open.
//
// Every var is set "if empty" so an operator (or bnk-forge) can still
// override any of them — e.g. pointing the helm cache at an air-gap
// mirror — and we won't clobber that choice. The base is config.BaseDir
// (ROKSBNKCTL_HOME, /work/.roksbnkctl under bnk-forge), the persisted
// workspace tree, so the cache survives across the phased invocations.
func prepareToolEnv() error {
	base, err := config.BaseDir()
	if err != nil {
		return err
	}

	// Helm cache/config/data, all under <base>/.helm so the terraform helm
	// provider (a plugin subprocess that inherits this env) never touches
	// $HOME.
	//
	// The var that actually matters is HELM_CACHE_HOME: a `helm_release`
	// whose `repository` is a bare chart-repo URL (e.g. cert-manager off
	// charts.jetstack.io) resolves through helm's anonymous-repo path
	// (repo.FindChartInRepoURL), which downloads the repo index into
	// helmpath.CachePath("repository") == $HELM_CACHE_HOME/repository —
	// it does NOT consult HELM_REPOSITORY_CACHE. In a fresh runner whose
	// $HOME isn't writable, that download can't create
	// $HOME/.cache/helm/repository and fails with
	//   could not download chart: ... open
	//   <HOME>/.cache/helm/repository/<hash>-index.yaml: no such file
	// even though the network is fine (the index downloads on the fly once
	// the dir is writable — no pre-`helm repo add` is needed). Redirecting
	// HELM_CACHE_HOME (+ HELM_CONFIG_HOME for repositories.yaml / OCI
	// registry auth, + HELM_DATA_HOME) to the writable workspace tree is
	// the fix; verified end-to-end against the hashicorp/helm provider.
	//
	// HELM_REPOSITORY_CACHE / HELM_REPOSITORY_CONFIG are still set (they
	// govern the named-repo path and `helm repo` CLI) for completeness and
	// to keep the whole helm footprint inside <base>/.helm.
	helmBase := filepath.Join(base, ".helm")
	helmCacheHome := filepath.Join(helmBase, "cache")
	helmConfigHome := filepath.Join(helmBase, "config")
	helmDataHome := filepath.Join(helmBase, "data")
	// Pre-create the cache/repository leaf too — helm MkdirAll's it itself,
	// but pre-creating keeps it consistent with the other roksbnkctl dirs.
	for _, d := range []string{filepath.Join(helmCacheHome, "repository"), helmConfigHome, helmDataHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}
	setEnvIfEmpty("HELM_CACHE_HOME", helmCacheHome)
	setEnvIfEmpty("HELM_CONFIG_HOME", helmConfigHome)
	setEnvIfEmpty("HELM_DATA_HOME", helmDataHome)
	setEnvIfEmpty("HELM_REPOSITORY_CACHE", filepath.Join(helmCacheHome, "repository"))
	setEnvIfEmpty("HELM_REPOSITORY_CONFIG", filepath.Join(helmConfigHome, "repositories.yaml"))
	// OCI credential storage must be INLINE, never via a native credential helper.
	// On Windows a `credsStore` in the docker/helm registry config (Docker Desktop
	// sets "desktop" in ~/.docker/config.json) makes the terraform helm provider's
	// OCI `Login` store the credential through that helper — which fails on the
	// multi-KB FAR `_json_key_base64` password with "error storing credentials … The
	// stub received bad data" (the Windows Credential Manager blob cap), breaking the
	// FLO/FLP `helm_release` pulls. Point the helm registry config AND DOCKER_CONFIG
	// at fresh files with an empty `auths` and NO credsStore, so the provider's login
	// stores the auth as inline base64 in the file (no helper). Overwritten each run
	// (the login re-populates it), and harmless on Linux where the store was already
	// inline. tfx helm-value is unaffected — it passes its own --registry-config.
	regConfig := filepath.Join(helmConfigHome, "registry", "config.json")
	if err := writeCleanRegistryConfig(regConfig); err != nil {
		return err
	}
	setEnvIfEmpty("HELM_REGISTRY_CONFIG", regConfig)
	dockerConfigDir := filepath.Join(helmBase, "docker")
	if err := writeCleanRegistryConfig(filepath.Join(dockerConfigDir, "config.json")); err != nil {
		return err
	}
	setEnvIfEmpty("DOCKER_CONFIG", dockerConfigDir)
	// Expose the helm registry-config path to the modules ON WINDOWS ONLY. There the
	// FLO/FLP helm_release resources write the OCI pull credential INLINE here
	// (local_file) and drop repository_username/password, so the helm provider READS
	// the auth for its pull instead of doing a login-and-STORE — which on Windows
	// shells out to a docker credential helper that fails on the multi-KB FAR
	// password ("The stub received bad data"). The provider reads this same file via
	// HELM_REGISTRY_CONFIG. Left empty elsewhere so Linux/macOS keep the proven
	// repository_username/password login path unchanged.
	if runtime.GOOS == "windows" {
		_ = os.Setenv("TF_VAR_helm_registry_config", regConfig)
	}

	// Kubeconfig: only redirect $KUBECONFIG to the workspace tree when the
	// standard $HOME/.kube location ISN'T writable (the runner case). On a
	// normal host $HOME/.kube is writable, so we leave $KUBECONFIG unset and
	// the post-apply fetch keeps landing at the conventional ~/.kube/config
	// (where the user's own kubectl reads it) — no behavior change there.
	// When $HOME/.kube can't be created (empty / non-writable $HOME in a
	// fresh runner), point $KUBECONFIG at the writable <base>/.kube/config;
	// k8s.DefaultKubeconfigPath falls back to that same path so later,
	// separate `roksbnkctl k …` invocations (which don't run through
	// tf.Open) still find it. An operator-set $KUBECONFIG always wins.
	if os.Getenv("KUBECONFIG") == "" && !homeKubeDirWritable() {
		kubeDir := filepath.Join(base, ".kube")
		if err := os.MkdirAll(kubeDir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", kubeDir, err)
		}
		_ = os.Setenv("KUBECONFIG", filepath.Join(kubeDir, "config"))
	}

	// Opt-in debug: confirm the resolved helm/kubeconfig env right before a
	// phase's terraform exec (the helm chart download is the one that broke
	// in fresh runners). Off unless ROKSBNKCTL_DEBUG is set, so normal runs
	// stay quiet.
	if os.Getenv("ROKSBNKCTL_DEBUG") != "" {
		fmt.Fprintf(os.Stderr,
			"debug: HELM_CACHE_HOME=%s HELM_REPOSITORY_CACHE=%s KUBECONFIG=%s\n",
			os.Getenv("HELM_CACHE_HOME"), os.Getenv("HELM_REPOSITORY_CACHE"), os.Getenv("KUBECONFIG"))
	}
	return nil
}

// homeKubeDirWritable reports whether $HOME/.kube exists or can be created
// — the test that decides whether the conventional kubeconfig location is
// usable. Returns false when $HOME is unset/empty or the MkdirAll is
// denied (the fresh-runner case the workspace-relative fallback targets).
func homeKubeDirWritable() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	return os.MkdirAll(filepath.Join(home, ".kube"), 0o755) == nil
}

// setEnvIfEmpty sets key=val only when key is not already present (or is
// empty) in the process env, so an operator override always wins.
func setEnvIfEmpty(key, val string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, val)
	}
}

// writeCleanRegistryConfig writes `{"auths":{}}` to path (creating parents). No
// credsStore/credHelpers, so OCI credential storage (the helm provider's login) is
// inline base64 rather than via a native credential helper — the Windows fix.
func writeCleanRegistryConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(`{"auths":{}}`), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// SourceDir is the path containing the resolved .tf files.
func (w *Workspace) SourceDir() string { return w.sourceDir }

// StateDir is the roksbnkctl per-workspace state root.
func (w *Workspace) StateDir() string { return w.stateDir }

// TFVarsPath: <stateDir>/terraform.tfvars  (auto-rendered from config.yaml; do not hand-edit)
func (w *Workspace) TFVarsPath() string {
	return filepath.Join(w.stateDir, "terraform.tfvars")
}

// UserTFVarsPath: <workspace-dir>/terraform.tfvars.user (sibling to
// config.yaml). Optional — if present, roksbnkctl passes it to terraform
// as a second -var-file after the auto-rendered one, so values in the
// user file override values from config.yaml. Useful for variables
// roksbnkctl's RenderTFVars doesn't expose (testing_*, roks_min_worker_*,
// cert_manager_namespace, etc.) or for one-off overrides.
func (w *Workspace) UserTFVarsPath() string {
	return filepath.Join(filepath.Dir(w.stateDir), "terraform.tfvars.user")
}

// HasUserTFVars reports whether the optional override file exists.
func (w *Workspace) HasUserTFVars() bool {
	_, err := os.Stat(w.UserTFVarsPath())
	return err == nil
}

// varFiles returns the list of -var-file paths to pass terraform.
// Order matters: later files override earlier (terraform's spec).
//
//  1. auto-rendered terraform.tfvars (from config.yaml)
//  2. terraform.tfvars.user (workspace-persistent override, if present)
//  3. extra (--var-file flags from the CLI, in the order given)
//
// Later layers win — a --var-file value beats both the workspace
// override and the generated tfvars.
func (w *Workspace) varFiles(extra ...string) []string {
	paths := []string{w.TFVarsPath()}
	if w.HasUserTFVars() {
		paths = append(paths, w.UserTFVarsPath())
	}
	paths = append(paths, extra...)
	return paths
}

// StatePath: <stateDir>/terraform.tfstate
func (w *Workspace) StatePath() string {
	return filepath.Join(w.stateDir, "terraform.tfstate")
}

// WriteTFVars renders wsCfg into the workspace's terraform.tfvars file
// (excluding api_key — see WriteTFVars in vars.go). roksbnkctl-managed
// paths (kubeconfig_dir, scratch_dir) are pre-created in Open() so the
// IBM provider's data sources and FLO's local-exec provisioners find
// them.
func (w *Workspace) WriteTFVars(wsCfg *config.Workspace) error {
	// Pass the workspace name so the render can resolve the Sprint-29 air-gap
	// registry-mirror record and redirect the BNK install off far_repo_url.
	return WriteTFVarsForWorkspace(w.TFVarsPath(), w.name, wsCfg, w.KubeconfigDir(), w.ScratchDir())
}

// KubeconfigDir is the path threaded through to the root TF's
// kubeconfig_dir variable (v0.6.8+). Each submodule appends its own
// name as a subdir; roksbnkctl pre-creates them in Open.
func (w *Workspace) KubeconfigDir() string {
	return filepath.Join(w.stateDir, "kubeconfig")
}

// ScratchDir is the path threaded to the root TF's scratch_dir variable
// (v0.6.9+) for FLO's FAR auth tarball + f5-manifest extraction. The
// upstream module derives manifest_download_dir as ${scratch_dir}/f5-manifest.
func (w *Workspace) ScratchDir() string {
	return filepath.Join(w.stateDir, "scratch")
}

// Init runs `terraform init`. Reconfigure(true) lets the backend
// override land cleanly even if a previous roksbnkctl run initialised
// against a different (deprecated -state=) layout.
func (w *Workspace) Init(ctx context.Context) error {
	return w.tf.Init(ctx, tfexec.Upgrade(false), tfexec.Reconfigure(true))
}

// Plan runs `terraform plan`. Returns true if changes are pending.
// extraVarFiles are appended to the var-file chain — see varFiles for
// the precedence order. State path is configured via the backend
// override written in Open(), not the deprecated -state flag.
func (w *Workspace) Plan(ctx context.Context, extraVarFiles ...string) (bool, error) {
	var opts []tfexec.PlanOption
	for _, p := range w.varFiles(extraVarFiles...) {
		opts = append(opts, tfexec.VarFile(p))
	}
	w.resetDiag()
	changes, err := w.tf.Plan(ctx, opts...)
	return changes, w.wrapDiag(err)
}

// PlanTo is Plan with `-out=<planPath>`: it saves a binary plan file that ApplyPlan
// can later consume verbatim, so the plan a user reviews is exactly the plan that
// applies — no re-plan, no drift between review and apply. Same var-file precedence
// as Plan. Returns whether the plan has changes.
func (w *Workspace) PlanTo(ctx context.Context, planPath string, extraVarFiles ...string) (bool, error) {
	opts := []tfexec.PlanOption{tfexec.Out(planPath)}
	for _, p := range w.varFiles(extraVarFiles...) {
		opts = append(opts, tfexec.VarFile(p))
	}
	w.resetDiag()
	changes, err := w.tf.Plan(ctx, opts...)
	return changes, w.wrapDiag(err)
}

// ShowPlan returns the human-readable rendering of a saved plan file (the text
// `terraform show <planfile>` prints), for writing a reviewable copy to disk.
func (w *Workspace) ShowPlan(ctx context.Context, planPath string) (string, error) {
	return w.tf.ShowPlanFileRaw(ctx, planPath)
}

// resetDiag clears the captured-stderr tail so a subsequent wrapDiag summarizes
// only THIS operation's output (each retry attempt starts clean).
func (w *Workspace) resetDiag() {
	if w.stderrCap != nil {
		w.stderrCap.Reset()
	}
}

// wrapDiag appends a deduplicated diagnostic summary (parsed from the captured
// terraform stderr) to a non-nil error, so the user sees a short "N distinct
// errors" digest instead of the raw walls of repeated provider blocks. A no-op
// when there's no error, no capture, or nothing parseable.
func (w *Workspace) wrapDiag(err error) error {
	if err == nil || w.stderrCap == nil {
		return err
	}
	if summary := summarizeTerraformDiagnostics(w.stderrCap.String()); summary != "" {
		return fmt.Errorf("%w\n\n%s", err, summary)
	}
	return err
}

// Apply runs `terraform apply`. tfexec auto-passes -auto-approve since
// terraform-exec doesn't allow interactive prompts; roksbnkctl's own
// confirmation gate runs at the CLI layer instead.
//
// After a successful apply this also writes terraform.applied.tfvars
// (PRD 07): a snapshot of the var-file chain terraform consumed, landed
// under the per-phase state dir at mode 0600. The snapshot write is
// best-effort — a failure logs a warning to stderr and does NOT fail
// the apply (the apply already succeeded and the snapshot is a
// nice-to-have output).
func (w *Workspace) Apply(ctx context.Context, extraVarFiles ...string) error {
	sources := w.varFiles(extraVarFiles...)
	var opts []tfexec.ApplyOption
	for _, p := range sources {
		opts = append(opts, tfexec.VarFile(p))
	}
	w.resetDiag()
	if err := w.tf.Apply(ctx, opts...); err != nil {
		return w.wrapDiag(err)
	}
	phase := w.phaseLabel(sources)
	if err := config.WriteAppliedTFVars(w.name, phase, sources); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write terraform.applied.tfvars: %v\n", err)
	}
	return nil
}

// ApplyPlan runs `terraform apply <planPath>`, applying a plan file saved by PlanTo
// EXACTLY as reviewed. terraform rejects -var-file when applying a saved plan (the
// plan already captured every variable), so none are passed — recordVarFiles is used
// only to write the applied-tfvars snapshot. If state or config drifted since the
// plan was saved, terraform refuses with a stale-plan error rather than applying
// something the operator didn't review — which is the whole point of the flow.
func (w *Workspace) ApplyPlan(ctx context.Context, planPath string, recordVarFiles ...string) error {
	w.resetDiag()
	if err := w.tf.Apply(ctx, tfexec.DirOrPlan(planPath)); err != nil {
		return w.wrapDiag(err)
	}
	sources := w.varFiles(recordVarFiles...)
	phase := w.phaseLabel(sources)
	if err := config.WriteAppliedTFVars(w.name, phase, sources); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write terraform.applied.tfvars: %v\n", err)
	}
	return nil
}

// phaseLabel classifies which workspace phase this Apply represents,
// for the PRD 07 snapshot header + state-dir routing. The label decides
// which per-phase state dir terraform.applied.tfvars is written to
// (appliedTFVarsPath), so it MUST match the stateDir the apply ran
// against — otherwise one phase's snapshot clobbers another's:
//
//   - "cluster"       — stateDir is state-cluster/ (`cluster up` flow).
//   - "testing"       — stateDir is state-testing/ (`testing up` flow).
//   - "gateway"       — stateDir is state-gateway/ (`gateway up` flow).
//   - "legacy-single" — stateDir is state/ AND DetectShape reports the
//     workspace is a v1.0.x single-state shape. Recorded so the reader
//     doesn't mistake a legacy snapshot for trial-only.
//   - "trial"         — anything else (the modern `bnk up` flow and the
//     defensive fallback for unclassifiable shapes).
//
// DetectShape is a cheap pure-filesystem call; it's safe to make this on
// every Apply. Errors from DetectShape fall through to "trial" — same
// fallback PRD 07 §"Anti-patterns to avoid" #4 mandates for the snapshot
// write itself.
func (w *Workspace) phaseLabel(_ []string) string {
	if filepath.Base(w.stateDir) == "state-cluster" {
		return "cluster"
	}
	if filepath.Base(w.stateDir) == "state-testing" {
		return "testing"
	}
	if filepath.Base(w.stateDir) == "state-gateway" {
		return "gateway"
	}
	if filepath.Base(w.stateDir) == "state-flp" {
		return "flp"
	}
	if filepath.Base(w.stateDir) == "state-tgw" {
		return "tgw"
	}
	return "trial"
}

// Destroy runs `terraform destroy`.
//
// Per PRD 07 §"Resolved design decisions" #2, Destroy intentionally does
// NOT touch the prior apply's terraform.applied.tfvars snapshot. The
// file represents "what was last deployed"; leaving it in place after a
// destroy is a deliberate user-facing signal that the workspace was
// applied at some point and the inputs that produced it are still on
// record. The snapshot/state divergence is itself useful (the file's
// mtime tells you when the last apply was; the absence of state tells
// you it's since been torn down).
func (w *Workspace) Destroy(ctx context.Context, extraVarFiles ...string) error {
	var opts []tfexec.DestroyOption
	for _, p := range w.varFiles(extraVarFiles...) {
		opts = append(opts, tfexec.VarFile(p))
	}
	w.resetDiag()
	return w.wrapDiag(w.tf.Destroy(ctx, opts...))
}

// StateMvTo moves resource address `src` out of this workspace's state
// file and into the local state file at `destStatePath` (Sprint 28
// jumphost migration: evict module.testing.* from state/ into
// state-testing/ with no cloud churn). Uses terraform's `state mv`
// -state/-state-out CLI flags so both source and destination are operated
// on as local files directly — the destination need not be an initialized
// backend. The source address and destination address are identical (a
// pure cross-state-file move, not a rename).
//
// Shells the terraform binary directly (mirroring RunReadOnly's exec
// pattern) rather than terraform-exec's typed StateMv: the -state/-state-out
// options in terraform-exec are deprecated for the everyday local-backend
// case, but the cross-state-file move is exactly the legacy use the CLI
// flags still serve, and shelling keeps the typed-API deprecation out of
// the build.
func (w *Workspace) StateMvTo(ctx context.Context, src, destStatePath string) error {
	if w == nil {
		return errors.New("terraform workspace not opened")
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		return fmt.Errorf("terraform not found on PATH — install terraform >= 1.5 (https://developer.hashicorp.com/terraform/install)")
	}
	argv := []string{
		"state", "mv",
		"-state=" + w.StatePath(),
		"-state-out=" + destStatePath,
		src, src,
	}
	cmd := exec.CommandContext(ctx, tfBin, argv...)
	cmd.Dir = w.sourceDir
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("terraform state mv %s: %w", src, err)
	}
	return nil
}

// Output reads terraform outputs (raw values + sensitivity flags).
func (w *Workspace) Output(ctx context.Context) (map[string]tfexec.OutputMeta, error) {
	return w.tf.Output(ctx)
}

// OpenReadOnly prepares a Workspace for a READ-ONLY terraform invocation
// (the `roksbnkctl terraform` escape hatch, Sprint 13 Issue 2 / PRD 08).
//
// Side-effect contract for a NEVER-APPLIED workspace phase: if
// <stateDir>/terraform.tfstate is absent, this returns ErrNoState
// BEFORE any source fetch or `terraform init` — the read-only command
// must not silently fetch source or init a phase the user never applied.
//
// When state IS present the phase was necessarily applied at least once,
// so the TF source was already fetched into <stateDir>/tf-source by that
// `up`/`apply`. We then delegate to Open, which is side-effect-safe for
// an already-applied workspace: embedded source re-extract is a cheap
// idempotent file write (no init, no network), `local` source is only
// re-validated, `github` source cache-hits the already-downloaded tree
// (fetch.go: "Already present? Reuse"). Open never runs `terraform init`
// itself (that is the separate Init() method, which OpenReadOnly /
// RunReadOnly deliberately never call). apiKey is empty — read-only
// operations don't need credentials and we must not trigger a prompt.
func OpenReadOnly(
	ctx context.Context,
	name string,
	wsCfg *config.Workspace,
	stateDir string,
) (*Workspace, error) {
	if wsCfg == nil {
		return nil, fmt.Errorf("workspace config is nil (run `roksbnkctl init`)")
	}
	statePath := filepath.Join(stateDir, "terraform.tfstate")
	if _, err := os.Stat(statePath); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoState
		}
		return nil, fmt.Errorf("checking terraform state %s: %w", statePath, err)
	}
	// State exists ⇒ source was already fetched by the prior apply ⇒
	// Open is side-effect-safe (no init, no network). Reuse it so the
	// read-only run inherits the exact same sourceDir cwd, where
	// terraform's .terraform/ data dir lives (Sprint 13: the CLI layer
	// must NOT re-derive these).
	return Open(ctx, name, wsCfg, stateDir, "", nil, nil)
}

// RunReadOnly shells the prepared terraform binary with argv, running in
// the resolved source dir (w.sourceDir), where terraform finds its
// .terraform/ data dir (providers/modules/backend config). The
// argv allowlist / mutation-flag policy is enforced by the CLI layer
// (internal/cli/terraform.go) — the tf package owns only the safe exec.
//
// Shelling the binary (rather than tfexec's typed methods) lets the CLI
// allowlist cover heterogeneous read-only verbs — output / show /
// state list / providers / graph / validate / fmt -check — uniformly.
//
// Returns terraform's combined stdout. stderr is streamed straight to
// the process stderr so progress/errors stay visible.
func (w *Workspace) RunReadOnly(ctx context.Context, argv []string) (string, error) {
	if w == nil || w.tf == nil {
		return "", errors.New("terraform workspace not opened")
	}
	if len(argv) == 0 {
		return "", errors.New("no terraform subcommand given")
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		return "", fmt.Errorf("terraform not found on PATH — install terraform >= 1.5 (https://developer.hashicorp.com/terraform/install)")
	}
	cmd := exec.CommandContext(ctx, tfBin, argv...)
	cmd.Dir = w.sourceDir
	// cwd=w.sourceDir is where terraform's .terraform/ data dir lives, so
	// the read-only verb finds providers/modules without any TF_DATA_DIR.
	// Inherit the process env (api key etc.); do NOT re-derive cwd at the
	// CLI layer — that re-derivation is exactly the Sprint 13 bug class.
	cmd.Env = os.Environ()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	return stdout.String(), runErr
}
