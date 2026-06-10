// Package exec defines the Backend interface and per-backend
// implementations awsbnkctl uses to run external tools (kubectl,
// iperf3, etc.). Backends differ along network locality (where the
// tool runs) and toolchain freshness (host install vs. pinned image).
//
// AWS credentials resolve via the SDK chain in `internal/aws` (env /
// shared config / profile / instance role / SSO / web-identity) and
// are passed to external tools via the standard AWS provider env vars
// (AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_REGION /
// AWS_PROFILE). The `Credentials` struct below carries only the
// kubeconfig bytes; per-backend serialisers translate it into
// bind-mounts, Secret references, or KUBECONFIG paths as needed.
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
}

// EnvVars returns KEY=VALUE strings the local backend should append to
// the child process's environment. Empty fields produce no entries —
// callers can append() unconditionally.
//
// AWS credentials are NOT threaded through this struct. The AWS SDK
// chain resolves them at the caller (`internal/aws.NewClients`) and
// external tools consume them via the standard AWS provider env vars
// inherited from the awsbnkctl process environment.
func (c *Credentials) EnvVars() []string {
	if c == nil {
		return nil
	}
	return nil
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
		// SINGLE FILE, read-only — never mount the parent .kube dir.
		mountArgs = append(mountArgs, "-v", path+":/root/.kube/config:ro")
	}

	return envArgs, mountArgs, cleanup, nil
}
