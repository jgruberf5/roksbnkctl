// Package exec defines the Backend interface and per-backend
// implementations roksbnkctl uses to run external tools (ibmcloud, kubectl,
// terraform, iperf3, etc.). Backends differ along network locality
// (where the tool runs) and toolchain freshness (host install vs.
// pinned image); see PRD 03 (docs/prd/03-EXECUTION-BACKENDS.md) for the
// full rationale.
//
// Sprint 3 ships `local` and `docker` backends. Sprint 4 adds `k8s` and
// `ssh`. Every backend translates the shared `Credentials` struct into
// its native shape (env vars for local, --env+--mount for docker, etc.)
// per the rules in PRD 04 (docs/prd/04-CREDENTIALS.md).
package exec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Credentials is the single shape every backend consumes. Per-backend
// serialisers (EnvVars, DockerArgs, …) translate it into env vars,
// bind-mounts, Secret references, or wrapper scripts — whatever the
// concrete backend needs.
//
// PRD 04 reserves slots in this struct for AWS / GCP credentials in
// future. For Sprint 3 only IBMCloudAPIKey + KubeconfigBytes are wired.
type Credentials struct {
	// KubeconfigBytes is the raw YAML content of the kubeconfig that
	// should be available to the wrapped tool. nil means "no kubeconfig"
	// — backends that bind-mount or materialise it skip the step.
	//
	// Why bytes (not a path)? Because the docker / k8s / ssh backends
	// need to materialise it inside the container / pod / remote host.
	// Even the local backend takes bytes for symmetry; if a path is
	// already on disk, callers read it once and pass the bytes through.
	KubeconfigBytes []byte

	// IBMCloudAPIKey is the resolved IBM Cloud API key. Empty means "no
	// key" — backends skip the env-var / mount work.
	//
	// PRD 04 §"Cross-backend principles" #1: this value MUST NEVER end
	// up in argv, log lines, or `docker inspect` output. Per-backend
	// serialisers below ensure that; redact.go's NewRedactor wraps
	// stdout/stderr as a defense-in-depth backstop.
	IBMCloudAPIKey string
}

// EnvVars returns KEY=VALUE strings the local backend should append to
// the child process's environment. Empty fields produce no entries —
// callers can append() unconditionally.
//
// Local backend semantics (PRD 04 §Local exec): cred values flow
// through env vars on a single-user assumption. KUBECONFIG points at a
// file path the caller has already written; we don't materialise it
// here because the local backend executes in the same FS namespace as
// roksbnkctl itself.
//
// IBMCLOUD_API_KEY is also exported as IC_API_KEY because the IBM CLI
// older versions accept both names — matches the historical workspaceEnv
// behaviour in cli/cluster.go pre-Sprint-3.
func (c *Credentials) EnvVars() []string {
	if c == nil {
		return nil
	}
	var out []string
	if c.IBMCloudAPIKey != "" {
		out = append(out,
			"IBMCLOUD_API_KEY="+c.IBMCloudAPIKey,
			"IC_API_KEY="+c.IBMCloudAPIKey,
		)
	}
	return out
}

// DockerArgs returns the docker-run argv fragments needed to propagate
// these credentials into a container, plus a cleanup function that
// removes any tempfiles materialised in tempDir.
//
// The split into envArgs + mountArgs lets the caller assemble them in
// the order their docker-run shape requires (envs and mounts are
// independent; the convention is mounts first, then envs, then image,
// then command — but the assembly is the caller's job).
//
// PRD 04 §Docker container constraints, encoded as code:
//
//   - envArgs uses the bare-name `--env IBMCLOUD_API_KEY` form (no
//     `=value`) so the value inherits from the caller env at runtime
//     and never lands in `docker inspect`. Callers MUST set the env
//     in the docker client's environment (or the daemon's, for
//     remote daemons) before invoking docker.
//
//   - mountArgs binds the SINGLE kubeconfig file (not its parent dir)
//     read-only at /root/.kube/config. We materialise it under
//     tempDir as kubeconfig with mode 0600.
//
// cleanup unlinks any materialised tempfiles. Safe to call multiple
// times; nil means "nothing to clean" (no kubeconfig was materialised).
func (c *Credentials) DockerArgs(tempDir string) (envArgs, mountArgs []string, cleanup func(), err error) {
	if c == nil {
		return nil, nil, nil, nil
	}

	if c.IBMCloudAPIKey != "" {
		// Bare-name form. PRD 04 §Docker §"Anti-patterns to avoid".
		envArgs = append(envArgs, "-e", "IBMCLOUD_API_KEY")
		envArgs = append(envArgs, "-e", "IC_API_KEY")
	}

	var tempFiles []string
	cleanup = func() {
		for _, p := range tempFiles {
			_ = os.Remove(p)
		}
		tempFiles = nil
	}

	if len(c.KubeconfigBytes) > 0 {
		if tempDir == "" {
			cleanup()
			return nil, nil, nil, errors.New("DockerArgs: tempDir required when KubeconfigBytes is set")
		}
		// Materialise the kubeconfig in tempDir/kubeconfig (mode 0600 so
		// only the running user can read it on the host before docker
		// bind-mounts it into the container).
		path := filepath.Join(tempDir, "kubeconfig")
		if werr := os.WriteFile(path, c.KubeconfigBytes, 0o600); werr != nil {
			cleanup()
			return nil, nil, nil, fmt.Errorf("materialising kubeconfig: %w", werr)
		}
		tempFiles = append(tempFiles, path)
		// SINGLE FILE, read-only. PRD 04 §Docker §"Anti-patterns".
		mountArgs = append(mountArgs, "-v", path+":/root/.kube/config:ro")
	}

	return envArgs, mountArgs, cleanup, nil
}
