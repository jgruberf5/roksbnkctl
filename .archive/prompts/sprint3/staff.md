You are the staff engineer agent for Sprint 3 of the roksbnkctl project. Your scope is **PRD 04** (cred abstraction) and **PRD 03 first half** (`Backend` interface + `local` + `docker` backends, applied to ibmcloud as the first migrated tool).

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25 (per Sprint 1).

## Read first

- `docs/prd/04-CREDENTIALS.md` — your authoritative spec for the cred abstraction. The "Implementation tasks" section is your work breakdown for that half.
- `docs/prd/03-EXECUTION-BACKENDS.md` — the backend interface design. Sprint 3 ships local + docker only; k8s + ssh are Sprint 4.
- `docs/PLAN.md` Sprint 3 section — confirms ordering and verification gates. Two-week structure: cred abstraction first (informs the Backend interface), then local + docker.
- Existing files: `internal/config/secrets.go` (the existing API-key resolver — this Sprint extracts + refactors it into `internal/cred/resolver.go`), `internal/cli/cluster.go` (the `os/exec` callsites for passthroughs — these get refactored through `internal/exec/local.go`).
- `prompts/sprint2/staff.md` for prompt-structure reference.
- `issues/resolved_sprint2_staff.md` — Sprint 2's deferred items (none directly impact Sprint 3 scope).

## Coordinate with parallel agents

An architect agent is replacing 5 chapter stubs with real prose under `book/src/` (chapters 12, 13, 14, 15, 17 intro). A validator agent is adding unit tests under `internal/cred/` + `internal/exec/`, integration tests for the docker backend, a cred-audit unit test, the new GitHub Actions workflow that builds + pushes the tools images on tag, and e2e Phase K-prelim. **Do not touch their files.** You own production code only.

## Tasks (priority order — finish from the top down)

If you run out of token budget, stop at a priority boundary and file an issue describing what's deferred.

### Priority 1 — Cred resolver (`internal/cred/resolver.go`)

Single source of truth for "give me the IBM API key". Extract the chain from the existing scattered logic in `internal/config/secrets.go` + `internal/cli/cluster.go` + lifecycle commands.

```go
package cred

// Resolver implements the chain: env → keychain → config-b64 → prompt.
type Resolver struct {
    Workspace string  // for keychain key + config lookup
    NonInteractive bool  // skip the prompt step
}

func (r *Resolver) IBMCloudAPIKey(ctx context.Context) (string, error)
```

Existing callers in `cluster.go`/`lifecycle.go` get refactored to use this. Behaviour must be byte-identical to today (verify by running `roksbnkctl ibmcloud iam oauth-tokens` before/after). Don't break Sprint 1's `--on jumphost` env propagation — the resolver feeds into the same env var.

### Priority 2 — Credentials struct + per-backend serialisers (`internal/exec/creds.go`)

```go
package exec

type Credentials struct {
    KubeconfigBytes []byte  // raw YAML; nil = no kubeconfig
    IBMCloudAPIKey  string  // empty = no key
    // future: AWS/GCP slots reserved per PRD 04
}

// Per-backend serialisers materialise this struct into the
// backend-specific shape (env vars, bind-mounts, etc.). The Local
// backend gets EnvVars(); the Docker backend gets DockerArgs(); the
// k8s + ssh serialisers come in Sprint 4.

func (c *Credentials) EnvVars() []string
func (c *Credentials) DockerArgs(tempDir string) (envArgs, mountArgs []string, cleanup func(), err error)
```

The Docker serialiser must follow PRD 04's "anti-patterns to avoid" rules:
- `--env IBMCLOUD_API_KEY` (no `=value`) — value inherits from caller env, never appears in `docker inspect`
- Bind-mount the **single** kubeconfig file read-only at `/root/.kube/config` — NEVER the parent dir
- The `tempDir` argument is for files materialised on disk (only kubeconfig content); cleanup func unlinks them on backend exit

### Priority 3 — Output stream redactor (`internal/exec/redact.go`)

Wraps `io.Writer` to mask the IBM API key value if it ever appears in stream content. Defense-in-depth — backends shouldn't print it, but if a wrapped tool does, we redact:

```go
package exec

// NewRedactor wraps w; any byte sequence matching one of the secrets
// is replaced with "[REDACTED]" before reaching the underlying writer.
// Buffers across writes so split-across-chunks secrets are caught.
func NewRedactor(w io.Writer, secrets []string) io.Writer
```

Used by the Local + Docker backends to wrap stdout/stderr before passing to the caller. The `secrets` arg is `[]string{credentials.IBMCloudAPIKey}` populated from the cred resolver.

### Priority 4 — `Backend` interface + registry (`internal/exec/backend.go`)

```go
package exec

type Backend interface {
    Run(ctx context.Context, argv []string, opts RunOpts) (exitCode int, err error)
    Name() string
}

type RunOpts struct {
    Stdin           io.Reader
    Stdout, Stderr  io.Writer
    Env             []string
    WorkDir         string
    TTY             bool
    Files           map[string][]byte  // files materialised at exec time
    Credentials     *Credentials
}

// Registry: backends register themselves at init().
func ResolveBackend(spec string) (Backend, error)  // "local" | "docker" | "ssh:<target>" | "k8s"
```

The `spec` parser handles `ssh:<target-name>` form for the SSH backend in Sprint 4. Sprint 3 only registers `local` and `docker`; the resolver errors clearly on unknown names.

### Priority 5 — Local backend (`internal/exec/local.go`)

Wraps `os/exec`. Refactor the existing passthrough callsites in `internal/cli/cluster.go` (`runKubectlPassthrough`, `runOcPassthrough`, `runIBMCloudPassthrough`, etc.) to dispatch through `Backend.Run(ctx, argv, opts)`. The `local` backend behaviour matches today's `os/exec` behaviour exactly — this is a refactor for symmetry with the future docker/k8s/ssh backends.

Make sure `--on <target>` from Sprint 1 still works — when set, dispatch goes through `internal/remote.Client.Run` (the existing path), not through the new `Backend` interface. The two flags (`--on` for SSH target, `--backend` for execution mode) are independent in v0.8; PLAN.md notes they consolidate later.

### Priority 6 — Docker backend (`internal/exec/docker.go`)

Build via `github.com/docker/docker/client`. Per-tool image lookup:

```go
var toolImages = map[string]string{
    "ibmcloud": "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev",
    "iperf3":   "ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:dev",
}
```

The `dev` tag is what the validator's GH Actions workflow will publish on `tag: v*` events. For local development, users can build via `tools/docker/Makefile` (already exists from Sprint 0).

`Run` shape:
- Translate `RunOpts` to `container.Config` + `container.HostConfig`
- Use `Credentials.DockerArgs()` to get env + mount args
- Stream stdout/stderr through the redactor
- Auto-remove on exit (`AutoRemove: true`)
- Context cancellation triggers `cli.ContainerKill`

If the Docker daemon isn't reachable, return a clear error message ("Docker daemon unreachable; is dockerd running?") with exit code 127.

### Priority 7 — Tool image Dockerfiles (`tools/docker/{ibmcloud,iperf3}/Dockerfile`)

Replace the Sprint 0 placeholders with buildable images.

`tools/docker/ibmcloud/Dockerfile`:
- Base: `ubuntu:22.04`
- Install ibmcloud-cli from IBM's apt repo (gpg key import + repo add + apt-get install -y ibmcloud-cli)
- Install ks plugin (`ibmcloud plugin install container-service`)
- Default ENTRYPOINT `["ibmcloud"]`

`tools/docker/iperf3/Dockerfile`:
- Already minimally functional from Sprint 0 (alpine + iperf3); leave as-is or refine

Verify both build locally: `cd tools/docker && make build-all`.

### Priority 8 — Workspace config `exec:` block + `--backend` CLI flag

Add to `internal/config/workspace.go`:

```go
type Workspace struct {
    // ... existing ...
    Exec map[string]ExecToolCfg `yaml:"exec,omitempty"`
}

type ExecToolCfg struct {
    Backend string `yaml:"backend"` // "local" | "docker" | "ssh:<target>" | "k8s"
}
```

Add to `internal/cli/root.go`: `flagBackend string` persistent flag. The flag value overrides workspace config per-invocation.

In each passthrough command (cluster.go), determine the backend:
1. `--backend` flag (if set)
2. Workspace config `exec:<tool>:backend` (if set)
3. Default `local`

### Priority 9 — Refactor existing callsites + plumbing

Once Backend interface lands, refactor:
- `internal/cli/cluster.go` passthrough commands to dispatch via `exec.Backend`
- The `--on jumphost` SSH path stays via `internal/remote/Client` (Sprint 4 will fold it into a proper `ssh` backend)
- `runIBMCloudPassthrough` resolves cred via `cred.Resolver`, builds `Credentials{IBMCloudAPIKey: ...}`, passes to `Backend.Run`

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean (validator's tests pass; your code shouldn't break them)
- `go vet ./...` clean
- `gofmt -d -l .` clean
- `roksbnkctl ibmcloud --backend local iam oauth-tokens` works (output identical to pre-refactor)
- `roksbnkctl ibmcloud --backend docker iam oauth-tokens` works against a local Docker daemon (if available; skip + note in issue file otherwise — the integrator will validate against a real env)
- `--backend bogus` produces a clear error
- Sprint 1's `--on jumphost` still works (regression check)

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint3_staff.md`. Same format as Sprint 2.

## Final report (under 200 words)

- Files created
- Files edited
- Build / test / vet / gofmt status
- Which priority items completed; which deferred
- Issues filed
- Anything the integrator should know (especially regarding go.mod additions for `github.com/docker/docker/client` and the resolver-chain refactor)

Do NOT commit. The integrator commits the aggregated work.
