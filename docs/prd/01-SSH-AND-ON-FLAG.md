# PRD 01 — Phase 1: embedded SSH client + `--on` target flag

> Prerequisites: none. This is the foundational phase.
>
> Estimated effort: small (~600 LOC); 1 week of focused work.

## Goal

Add a generic `--on <target>` flag to `roksbnkctl` commands that runs the underlying operation on a remote host via SSH instead of locally. Targets are named entries in workspace config or auto-discovered from terraform output.

## Why this first

- Works **before the cluster exists** — only mechanism available pre-`up` (in-cluster pod exec needs a cluster).
- Customer-firewall scenarios where `ibmcloud iam` and `ks cluster get` succeed only from an approved bastion IP.
- Air-gapped environments where the user's laptop can't reach IBM Cloud directly.
- The upstream HCL already provisions a jumphost (`testing_tgw_jumphost_*` outputs); we just need to use it.
- The SSH backend in Phase 3 reuses this client + target abstraction, so it's foundational.

## Scope

### In scope

- `--on <target>` persistent flag on the root command, applied to: `exec`, `kubectl`, `oc`, `ibmcloud`, `shell`. (Lifecycle commands `up`/`down`/etc. error clearly when given `--on` — they're handled in Phase 3 via the SSH backend, not via top-level dispatch.)
- `targets:` block in workspace config:
  ```yaml
  targets:
    jumphost:                                # auto-populated after `roksbnkctl up`
      host: 169.45.91.177
      user: root
      key_source: tf-output:jumphost_shared_key
      port: 22                               # default
    bastion:                                 # user-defined
      host: ops.example.com
      user: jgruber
      key_path: ~/.ssh/id_ed25519
  ```
- `roksbnkctl targets` command group: `list`, `show <name>`, `add <name> --host ... --user ... --key-path ...`, `remove <name>`
- Auto-population: after a successful `roksbnkctl up`, write `targets.jumphost` from `tls_private_key.jumphost_shared_key` + `testing_tgw_jumphost_ip` (TF outputs)
- Key sources:
  - File path: `key_path: ~/.ssh/id_ed25519`
  - SSH agent: `key_source: agent`
  - TF state output: `key_source: tf-output:<output-name>` (reads from workspace's TF state)
- Host key handling: `~/.roksbnkctl/known_hosts`, TOFU prompt on first connect, `--insecure-host-key` flag for CI / first-time-no-prompt scenarios
- Streaming I/O (stdout/stderr) and exit code propagation
- Optional TTY (`--tty` flag, auto-on for `roksbnkctl shell --on`)

### Out of scope

- ProxyJump / multi-hop SSH (deferred — note the TG jumphost can already proxy to cluster-internal VMs via the upstream HCL's jumphost network design, but routing roksbnkctl through it is Phase 1.x)
- SSH config file (`~/.ssh/config`) parsing — explicit config in workspace YAML only
- Password auth — keys + agent only
- Windows ssh-agent named-pipe protocol — file keys initially; agent on macOS/Linux only
- SCP / SFTP file transfer (Phase 3's SSH backend handles file materialization)

## Design

### Library

`golang.org/x/crypto/ssh` — Go-native SSH client. No host `ssh` binary required. Standard library-adjacent quality.

Supplementary:
- `golang.org/x/crypto/ssh/agent` — agent socket protocol
- `golang.org/x/crypto/ssh/knownhosts` — known_hosts file format

### Code organization

```
internal/remote/
  ssh.go        # Client wrapper: connect, run, shell, close
  targets.go    # Target struct, config integration, auto-discovery
  hostkeys.go   # known_hosts read/write, TOFU prompt
  agent.go      # ssh-agent socket discovery (linux/darwin only)
  keys.go       # key loading (file, agent, tf-output)
internal/config/
  workspace.go  # add Targets map[string]TargetCfg field
internal/cli/
  targets.go    # roksbnkctl targets list/show/add/remove
  root.go       # --on persistent flag binding
```

### Target struct

```go
package remote

type Target struct {
    Name      string `yaml:"-"`           // map key
    Host      string `yaml:"host"`
    Port      int    `yaml:"port,omitempty"`         // default 22
    User      string `yaml:"user"`
    KeyPath   string `yaml:"key_path,omitempty"`     // file path
    KeySource string `yaml:"key_source,omitempty"`   // "agent" | "tf-output:<name>"
}
```

### Connection flow

1. Resolve `--on <name>` against `cctx.Workspace.Targets[name]`
2. Load key per `KeySource` / `KeyPath` rules
3. Connect with `knownhosts.New(...)` host-key callback
4. On unknown host: prompt user (`Add 169.45.91.177's key (ED25519:abc...) to ~/.roksbnkctl/known_hosts? [y/N]`); error if `--no-input` and unknown
5. Open session, set env, run command, stream I/O, return exit code

### Auto-discovery from TF output

After successful `up`:

```go
// Pseudocode in cli/lifecycle.go runUp() post-apply
outputs, _ := tfws.Output(ctx)
ip := stringOutput(outputs, "testing_tgw_jumphost_ip")
keyPEM := stringOutput(outputs, "jumphost_shared_key")  // sensitive output
if ip != "" && ip != "TGW jumphost not created" && keyPEM != "" {
    target := remote.Target{
        Host: ip, User: "root",
        KeySource: "tf-output:jumphost_shared_key",
    }
    config.SetTarget(workspace, "jumphost", target)
}
```

The `tf-output:` source means roksbnkctl reads the key from terraform state at SSH-connect time — not stored elsewhere.

### Command shape

```bash
# Run a command on the jumphost
roksbnkctl exec --on jumphost -- ls /etc

# Interactive shell
roksbnkctl shell --on jumphost

# Passthroughs over SSH
roksbnkctl ibmcloud --on jumphost ks cluster ls
roksbnkctl kubectl --on jumphost get pods    # Phase 1 routes via SSH; Phase 2 short-circuits to internal client-go
roksbnkctl oc --on jumphost projects

# Target management
roksbnkctl targets list
roksbnkctl targets show jumphost
roksbnkctl targets add bastion --host ops.example.com --user jgruber --key-path ~/.ssh/id_ed25519
roksbnkctl targets remove bastion
```

### Error semantics

- Unreachable target / connect failure: exit code **127** ("command not found" analog)
- Auth failure: exit code **126** ("permission denied" analog)
- Host key mismatch (man-in-the-middle protection): exit code **126**, plus pointer to `roksbnkctl targets show` to inspect the stored key
- Remote command failed: pass through the remote process's exit code unchanged

## Implementation tasks

1. **`internal/remote/ssh.go`** — `Client` struct wrapping `*ssh.Client`; methods `Run(ctx, argv []string, opts RunOpts) (int, error)` and `Shell(ctx, opts ShellOpts) error`. Streams stdin/stdout/stderr; respects ctx cancellation by closing the session.
2. **`internal/remote/targets.go`** — `LoadTarget(workspace, name) (*Target, error)`, `SetTarget(...)`, `ListTargets()`. Reads from `~/.roksbnkctl/<ws>/config.yaml`'s `targets:` block.
3. **`internal/remote/keys.go`** — `LoadSigner(target, tfState) (ssh.Signer, error)` dispatching on `KeyPath` / `KeySource`. The `tf-output:<name>` source pulls from a passed-in TF outputs map (caller's job to refresh).
4. **`internal/remote/hostkeys.go`** — wraps `knownhosts.New(...)` with TOFU prompt logic.
5. **`internal/remote/agent.go`** — `Agent() (agent.Agent, error)` — reads `SSH_AUTH_SOCK`.
6. **`internal/config/workspace.go`** — add `Targets map[string]remote.TargetCfg` to `Workspace`. Integrate in `LoadWorkspace` / `SaveWorkspace`.
7. **`internal/cli/root.go`** — add `flagOn string` persistent flag. In `Execute()`, after parsing, resolve target if set; pass through cobra's `cmd.Context()` so per-command runners can pick it up.
8. **Refactor `cli/cluster.go` passthrough commands** (`kubectl`, `oc`, `ibmcloud`, `shell`, `exec`) — when `flagOn != ""`, dispatch via `remote.Client.Run` instead of `os/exec`.
9. **`cli/targets.go`** — new `roksbnkctl targets` command tree (list/show/add/remove).
10. **`cli/lifecycle.go runUp()`** — post-apply jumphost auto-population.
11. **Doctor**: optional check `roksbnkctl doctor --target jumphost` runs a no-op `whoami` against the target.

## Acceptance criteria

- `roksbnkctl exec --on jumphost -- whoami` prints `root` (the jumphost default user) and exits 0
- `roksbnkctl shell --on jumphost` enters an interactive PTY remote shell
- `roksbnkctl ibmcloud --on jumphost iam oauth-tokens` propagates `IBMCLOUD_API_KEY` and returns a token (validates env handling)
- After `roksbnkctl up`, `roksbnkctl targets list` shows `jumphost` without manual intervention
- Lifecycle commands error out clearly when given `--on` ("--on not supported on `up` in Phase 1; use --backend ssh in Phase 3")
- First-connect TOFU prompt works; rejecting it errors cleanly; accepting persists to `~/.roksbnkctl/known_hosts`
- Host key change between runs is detected and rejected with a man-in-the-middle warning

## Open questions

- Should `--on jumphost` become the **default** for `ibmcloud` passthrough once a jumphost exists in the workspace? Probably **yes** for compliance scenarios; let users opt-out with `--on local`.
- How to surface SSH connect failures vs. remote command failures distinctly in exit codes? The 126/127/passthrough split above is one option; alternatives welcome.
- Should `jumphost` be a special-cased name, or just one of many possible target names with no privilege? **Just a name**, with auto-discovery only writing to that key. Users can add others freely.
- Key persistence: when reading a key via `tf-output:<name>` and that TF state changes (e.g., cluster destroyed and recreated), the key changes. Should the host key in `known_hosts` reset accordingly? Probably yes — match host key rotation to TF apply detection.

## Related work

The SSH backend in [PRD 03](./03-EXECUTION-BACKENDS.md) reuses `internal/remote/ssh.go`'s `Client` directly. Phase 1's `--on` flag dispatches one-shot remote exec; Phase 3's SSH backend extends this with file materialization (kubeconfig, env-file fallback) and Ubuntu apt-bootstrap for missing tools.
