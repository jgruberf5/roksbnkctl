package exec

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Backend is the common interface every external-tool runner implements.
// Implementations differ along (a) where the tool actually runs (host
// process vs docker container vs in-cluster pod vs SSH host) and (b)
// how credentials and files are propagated (env vs bind-mount vs
// projected Secret vs SSH wrapper).
//
// PRD 03 §"Backend interface" is the authoritative spec. Implementations
// in this package: local.go, docker.go (Sprint 3); k8s.go, ssh.go are
// Sprint 4.
type Backend interface {
	// Run executes argv with stdin/stdout/stderr wired to the streams
	// in opts. Returns the wrapped process's exit code.
	//
	// 126 + 127 are reserved per PRD 03 §"Backend interface" for
	// backend-side failures (e.g., docker daemon unreachable, SSH
	// connect refused). Codes 0-125 + 128-255 mirror the wrapped
	// process exit code unchanged.
	//
	// ctx cancellation must terminate the wrapped process within a
	// few seconds — backends signal/kill the container/process and
	// return ctx.Err() (or the equivalent killed-by-signal exit
	// code).
	Run(ctx context.Context, argv []string, opts RunOpts) (exitCode int, err error)

	// Name returns the registry key used to resolve this backend
	// ("local" | "docker" | "k8s" | "ssh"). Used by logging + doctor.
	Name() string
}

// RunOpts is the options bundle for one Backend.Run invocation.
//
// PRD 03 §"Backend interface" defines the shape; this package adds two
// implementation conveniences:
//
//   - Stdin/Stdout/Stderr default to nil-ignored / nil-discarded
//     (backends use io.Discard as a fallback, so callers don't have to).
//   - Files materialise at exec time — the local backend ignores them
//     (FS is shared); the docker backend bind-mounts a tempdir; ssh /
//     k8s backends scp / kubectl-cp them in.
type RunOpts struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Env is a list of "KEY=VALUE" strings to append to the wrapped
	// process's environment. Backends merge with their own defaults
	// (e.g., the local backend appends to os.Environ()).
	Env []string

	// WorkDir is the wrapped process's working directory. Best-effort
	// — some backends ignore (k8s pod's WorkingDir is set on the
	// container spec; can't change per-exec).
	WorkDir string

	// TTY requests a PTY where the backend supports it (local, ssh).
	// Backends that don't support TTY (docker without -t flag plumbed,
	// k8s without remotecommand TTY) silently degrade to non-TTY.
	TTY bool

	// Files is a name → content map that materialises at exec time.
	// Local backend uses these as files in WorkDir; docker bind-mounts;
	// k8s / ssh stage to a tempdir on the remote.
	Files map[string][]byte

	// Credentials carries cred values for the wrapped tool. Backends
	// translate via the per-backend serialisers in creds.go (EnvVars,
	// DockerArgs, etc.) and wrap stdout/stderr through NewRedactor as
	// defense-in-depth.
	Credentials *Credentials

	// HostMounts lists additional bind-mounts the docker backend
	// projects from the host filesystem into the container. Sprint 5
	// terraform integration uses this to mount the workspace state
	// directory at `/state` so the in-container `terraform init/plan/
	// apply/destroy` operates on the same .tfstate file the local
	// backend writes. Other backends ignore HostMounts (the local
	// backend has no need; ssh / k8s would need scp / projected-volume
	// shapes that aren't worth the v1 complexity).
	//
	// PRD 03 §"terraform" + chapter 17 §"terraform docker subsection".
	HostMounts []HostMount

	// RunAsUser pins the container's UID:GID. Set for the terraform
	// docker path so the state file is written with the host user's
	// ownership (otherwise terraform-in-container runs as root and
	// produces root-owned state files the host user can't edit).
	//
	// Format: "uid:gid" or just "uid". Empty defers to the image's
	// default user. Backends that don't honor a runtime UID (k8s,
	// ssh, local) ignore the field.
	RunAsUser string
}

// HostMount is one host → container bind-mount. Used by the docker
// backend's terraform path; future backends may grow analogous
// shapes (k8s projected-volume, ssh staged-file).
type HostMount struct {
	HostPath      string // absolute path on the host
	ContainerPath string // absolute path inside the container
	ReadOnly      bool   // true → read-only mount
}

// ResolveBackend looks up a backend by spec. Spec forms:
//
//	"local"            — local execution
//	"docker"           — docker daemon on caller host
//	"ssh:<target>"     — SSH backend with named target (Sprint 4)
//	"k8s"              — in-cluster pod (Sprint 4)
//
// Sprint 3 only registers "local" + "docker". The "ssh:<target>" spec
// parser is in place so the integrator's CLI flag parsing has a stable
// hook; resolving an unregistered spec (k8s, ssh:foo) returns a clear
// "backend not implemented yet" error pointing at PRD 03.
//
// Empty spec defaults to "local" — matches the per-tool default for
// ibmcloud and terraform per PLAN.md Sprint 3.
func ResolveBackend(spec string) (Backend, error) {
	if spec == "" {
		spec = "local"
	}

	// Strip the "<name>:<target>" form for ssh / k8s. Sprint 3 doesn't
	// dispatch on the target (no SSH backend yet) but we accept the
	// form so callers don't get "unknown backend" errors on a
	// well-formed ssh:<target> spec.
	name := spec
	if idx := strings.IndexByte(spec, ':'); idx > 0 {
		name = spec[:idx]
	}

	registryMu.RLock()
	b, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		// Spec parsed cleanly but the backend isn't registered. The
		// most likely cause for k8s / ssh in Sprint 3 is "not
		// implemented yet"; emit a clearer error than "unknown" to
		// save users a docs round-trip.
		switch name {
		case "k8s", "ssh":
			return nil, fmt.Errorf("backend %q not implemented in this build (Sprint 4); see docs/prd/03-EXECUTION-BACKENDS.md", name)
		default:
			return nil, fmt.Errorf("unknown backend %q (want local|docker|k8s|ssh[:<target>])", spec)
		}
	}
	return b, nil
}

// Register installs a backend under name. Called from each backend's
// init() so concrete implementations register themselves; the
// registration order is irrelevant.
//
// Re-registering the same name overwrites silently — useful for tests
// that swap in stubs. If you want fail-on-collision behaviour, wrap
// this with a check at the call site.
func Register(name string, b Backend) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if registry == nil {
		registry = make(map[string]Backend)
	}
	registry[name] = b
}

// SpecTarget extracts the "<target>" component from a spec like
// "ssh:<target>". Returns "" for specs without a colon. Sprint 4 SSH
// backend uses this to look up the target by name.
func SpecTarget(spec string) string {
	if idx := strings.IndexByte(spec, ':'); idx > 0 {
		return spec[idx+1:]
	}
	return ""
}

var (
	registry   map[string]Backend
	registryMu sync.RWMutex
)
