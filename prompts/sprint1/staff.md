You are the staff engineer agent for Sprint 1 of the roksbnkctl project. Your scope is **PRD 01** — the embedded SSH client + `--on <target>` flag + `targets:` workspace config + jumphost auto-discovery.

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`.

## Read first

- `docs/prd/01-SSH-AND-ON-FLAG.md` — your authoritative spec. The "Implementation tasks" section lists what you're building.
- `docs/PLAN.md` Sprint 1 section — confirms ordering and verification requirements.
- `internal/cli/root.go`, `internal/cli/cluster.go` (the existing passthroughs: `kubectl`, `oc`, `ibmcloud`, `exec`, `shell`), `internal/cli/lifecycle.go` (the existing `runUp`), `internal/config/workspace.go` (where you'll add `Targets`).
- `prompts/sprint0/staff.md` for prompt-structure reference (coordination notes, verification format).

## Coordinate with parallel agents

An architect agent is replacing 6 chapter stubs with real prose under `book/src/`. A validator agent is adding integration tests in `internal/remote/integration_test.go`, extending `scripts/e2e-test.sh` Phase B with `--on` steps, and possibly editing `.github/workflows/ci.yml`. **Do not touch their files.** You own everything else.

## Tasks (in priority order — finish from the top down)

### 1. SSH client core — `internal/remote/ssh.go`

```go
package remote

type Client struct { /* wraps *ssh.Client */ }
type RunOpts struct {
    Stdin           io.Reader
    Stdout, Stderr  io.Writer
    Env             []string
    TTY             bool
}
type ShellOpts struct { Stdin io.Reader; Stdout, Stderr io.Writer }

func Connect(ctx context.Context, target *Target) (*Client, error)
func (c *Client) Run(ctx context.Context, argv []string, opts RunOpts) (exitCode int, err error)
func (c *Client) Shell(ctx context.Context, opts ShellOpts) error
func (c *Client) Close() error
```

Use `golang.org/x/crypto/ssh`. Stream stdout/stderr live to caller's writers (don't buffer the whole output). Exit code from the remote process flows through unchanged. Context cancellation closes the session within a few seconds.

### 2. Target struct + workspace config — `internal/remote/targets.go` + edit `internal/config/workspace.go`

Add to `internal/config/workspace.go`:

```go
type Workspace struct {
    // ... existing fields ...
    Targets map[string]TargetCfg `yaml:"targets,omitempty"`
}

type TargetCfg struct {
    Host      string `yaml:"host"`
    Port      int    `yaml:"port,omitempty"`         // default 22
    User      string `yaml:"user"`
    KeyPath   string `yaml:"key_path,omitempty"`
    KeySource string `yaml:"key_source,omitempty"`   // "agent" | "tf-output:<name>"
}
```

In `internal/remote/targets.go`:

```go
type Target struct {
    Name string
    Host string
    Port int
    User string
    Signer ssh.Signer  // resolved at Connect time
}

func LoadTarget(workspace string, name string) (*Target, error)  // reads ~/.roksbnkctl/<ws>/config.yaml
func ListTargets(workspace string) ([]*Target, error)
func SetTarget(workspace string, name string, cfg TargetCfg) error
```

### 3. Key sources — `internal/remote/keys.go`

`ResolveSigner(target *TargetCfg, tfOutputs map[string]string) (ssh.Signer, error)` dispatching on:
- `KeyPath != ""`: read PEM file, parse via `ssh.ParsePrivateKey`
- `KeySource == "agent"`: connect to `$SSH_AUTH_SOCK`, return first available signer
- `KeySource == "tf-output:<name>"`: read `tfOutputs[name]` as PEM bytes, parse

Pass-in TF outputs map rather than reading TF state from inside this package — keeps the dep direction clean (`tf` package gives `remote` package the outputs it needs).

### 4. Host-key TOFU — `internal/remote/hostkeys.go`

`HostKeyCallback(...)` that:
- Reads `~/.roksbnkctl/known_hosts` (per-tool, not the user's ~/.ssh/known_hosts)
- On match: accept
- On mismatch: error with man-in-the-middle warning, exit code 126
- On unknown host:
  - If TTY: prompt the user `Add <ip>'s key (ED25519:...) to known_hosts? [y/N]`
  - If not TTY: error unless `--insecure-host-key` flag was passed
  - On accept: append to `~/.roksbnkctl/known_hosts`

### 5. `--on` flag — edit `internal/cli/root.go` + dispatch in `internal/cli/cluster.go`

In `root.go`: add persistent flag `flagOn string` to rootCmd.

In `cluster.go` (the file with `kubectl`/`oc`/`ibmcloud`/`exec`/`shell` passthrough commands): each runner that currently does `os/exec` should check `if flagOn != ""` and dispatch via `remote.Connect(target).Run(...)` instead. Local exec stays the default when `--on` is unset.

Lifecycle commands (`up`, `down`, `plan`, `apply`, `init`) should error fast if given `--on`:

```
Error: --on not supported on `roksbnkctl up` in v0.7. Use --backend ssh in a future release once Phase 3 lands (see docs/prd/03-EXECUTION-BACKENDS.md).
```

### 6. `targets` command tree — `internal/cli/targets.go`

```
roksbnkctl targets list                   # table: name, host, user, key source
roksbnkctl targets show <name>            # detail view
roksbnkctl targets add <name> --host H --user U [--port P] [--key-path P | --key-source S]
roksbnkctl targets remove <name>
```

### 7. Auto-populate jumphost — edit `internal/cli/lifecycle.go runUp`

After successful apply, read terraform outputs:

```go
outputs, _ := tfws.Output(ctx)
ip := stringOutput(outputs, "testing_tgw_jumphost_ip")
keyPEM := stringOutput(outputs, "jumphost_shared_key")  // sensitive
if ip != "" && ip != "TGW jumphost not created" && keyPEM != "" {
    cfg := TargetCfg{ Host: ip, User: "root", KeySource: "tf-output:jumphost_shared_key" }
    config.SetTarget(cctx.WorkspaceName, "jumphost", cfg)
}
```

### 8. Doctor target check (extends Sprint 0's Check struct)

In `internal/doctor/`, add an optional `--target <name>` flag to `roksbnkctl doctor` that runs a no-op `whoami` against the target and reports as a `Check`. The Check uses `BackendName: ""` for now; this will become `BackendName: "ssh"` in Phase 3 — note it in a comment.

### 9. Unit tests

Add `internal/remote/ssh_test.go`, `keys_test.go`, `hostkeys_test.go`, `targets_test.go`. Use `github.com/gliderlabs/ssh` (already a transitive dep via the SSH library; if not, add it) for mocked SSH server testing. Cover:
- Connect → Run → exit code propagation
- Stdin/stdout/stderr streaming
- Host-key TOFU acceptance + mismatch rejection
- Key resolution: file path, tf-output, agent (skip if `SSH_AUTH_SOCK` not set in test env)
- Target config load/save round-trip via tempdir workspace

Aim for ~80%+ coverage on `internal/remote/`.

### 10. `--insecure-host-key` flag

Persistent flag at root that, when set, skips the TOFU check (just-add-the-key behavior). For CI use.

## Verification before reporting done

- `go build ./...` succeeds
- `go test ./...` succeeds (the unit tests you added are green)
- `go vet ./...` succeeds
- `gofmt -d -l .` produces no diff for files you edited
- `roksbnkctl exec --help` shows the `--on` flag
- `roksbnkctl targets --help` shows the four sub-commands
- `roksbnkctl up --on jumphost --auto` errors fast with the "not supported" message (don't actually run an apply)
- Existing `roksbnkctl doctor` output unchanged when `--target` is not passed (Sprint 0 byte-equivalence preserved)

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint1_staff.md`:

```markdown
# Sprint 1 — staff engineer issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: ...
**Files affected**: ...
**Proposed fix**: ...
```

If you can't complete a task in priority order (run out of time / blocked), file an issue describing what's missing and why; don't half-finish.

## Final report (under 200 words)

- Files created (counts + key paths)
- Files edited
- Build / test / vet / gofmt status
- Which priority items completed; which (if any) deferred to Sprint 1.5 / Sprint 2
- Issues filed
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
