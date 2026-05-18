# SSH targets

This chapter is the technical reference for the `targets:` system. Its companion is [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md), which is the user-facing prose for "how do I run a command on the jumphost". Chapter 16 introduces targets briefly; this chapter goes deeper into the schema, the key sources, the host-key trust model, the auto-discovery pipeline, and what the `ssh` execution backend layers on top of the lightweight `--on` flag.

If you arrived here from Chapter 16 looking for "where do I learn the full surface", you're in the right place.

## The `targets:` block schema

Targets live under `targets:` in `~/.roksbnkctl/<workspace>/config.yaml`:

```yaml
targets:
  jumphost:
    host: 169.45.91.177
    port: 22
    user: ubuntu
    key_source: tf-output:jumphost_shared_key

  bastion:
    host: ops.example.com
    user: jgruber
    key_path: ~/.ssh/id_ed25519

  prod-jump:
    host: 10.0.0.5
    user: ec2-user
    key_source: agent
```

The Go struct backing it is `internal/config.TargetCfg`:

```go
type TargetCfg struct {
    Host      string `yaml:"host"`
    Port      int    `yaml:"port,omitempty"`        // default 22
    User      string `yaml:"user"`
    KeyPath   string `yaml:"key_path,omitempty"`    // file path
    KeySource string `yaml:"key_source,omitempty"`  // "agent" | "tf-output:<name>"
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | IP or hostname. Resolved via the standard Go resolver chain (no special DNS handling). |
| `port` | int | no | Defaults to `22`. Only set when the remote sshd listens elsewhere. |
| `user` | string | yes | Remote login username. |
| `key_path` | string | one-of | File path to a PEM-encoded private key. Tilde expansion honoured. |
| `key_source` | string | one-of | `agent` or `tf-output:<output-name>`. |

Validation rules at load time:

- Exactly **one** of `key_path` or `key_source` must be set. Setting neither, or both, fails the load with a clear error.
- The target name (the YAML map key) must be non-empty and stable across YAML round-trips — `roksbnkctl targets show <name>` and `roksbnkctl targets remove <name>` look up by this name.

The `TargetCfg` type lives in `internal/config` rather than `internal/remote` to avoid an import cycle: the YAML (de)serialiser needs the wire shape, and the SSH client (`internal/remote`) needs to consume it. Keeping the shape in `config` and the runtime `Target` (parsed key, dialer config, etc.) in `remote` keeps the dependency direction one-way.

## Key sources

The three options for telling `roksbnkctl` how to find the SSH private key for a target.

### `key_path: <file>`

A PEM-encoded private key on disk:

```yaml
bastion:
  host: ops.example.com
  user: jgruber
  key_path: ~/.ssh/id_ed25519
```

Standard OpenSSH formats are accepted: `id_rsa`, `id_ed25519`, `id_ecdsa`, `id_dsa` (deprecated but supported). Tilde expansion uses `os.UserHomeDir()` semantics — `~/` → user home, `~user/` is **not** supported (use an absolute path).

The file is read at SSH-connect time, not at config-load time. A missing or unreadable file fails the connect, not the workspace load. This matters for ergonomics: you can edit a target into config.yaml referencing a key path that doesn't exist yet, then create the key separately, without `roksbnkctl init`/`use` failing in between.

Encrypted keys (passphrase-protected) are not currently supported in the SSH client — the agent path is the recommended workflow for keys that need a passphrase.

### `key_source: agent`

Talks to ssh-agent over `$SSH_AUTH_SOCK`:

```yaml
prod-jump:
  host: 10.0.0.5
  user: ec2-user
  key_source: agent
```

The agent presents whichever keys it currently holds; `roksbnkctl` tries each in turn against the target's `authorized_keys` (via SSH's standard publickey-authentication exchange). The first key the server accepts is the one that gets used.

This is the right setting when:

- Your team manages keys via 1Password / hardware tokens / `gpg-agent` and you don't want a key file on disk.
- You're on a shared workstation where putting the key file in `~/.ssh/` is undesirable.
- You're already using ssh-agent for everything else and want consistent behaviour.

**Platform note**: ssh-agent integration is **Linux/macOS-only**. Windows users should use `key_path` to a file. The restriction is structural to the Go SSH library, which doesn't wrap the Windows ssh-agent named-pipe protocol — see [`golang.org/x/crypto/ssh/agent`](https://pkg.go.dev/golang.org/x/crypto/ssh/agent) and upstream tracking issues for status; full Windows support is a v2 item.

### `key_source: tf-output:<output-name>`

Reads the key from the workspace's terraform state output of that name:

```yaml
jumphost:
  host: 169.45.91.177
  user: ubuntu
  key_source: tf-output:jumphost_shared_key
```

This is the auto-discovered jumphost path. The upstream HCL provisions a `tls_private_key` resource per cluster create, marks it `sensitive`, and surfaces it as a terraform output named `jumphost_shared_key`. `roksbnkctl` reads it via the equivalent of `terraform output -raw <name>` at SSH-connect time.

What this gets you that `key_path` doesn't:

- **No on-disk key file separate from terraform state.** The key only exists in `terraform.tfstate` (which is already a sensitive workspace artefact) and in memory during the SSH handshake.
- **Auto-rotation on cluster re-create.** Destroy and re-create the cluster, terraform generates a new `tls_private_key`, and the next `--on jumphost` invocation picks up the new key without any manual rewriting of the workspace config.
- **Single source of truth.** The key value is in terraform state — the same place every other cluster-generated secret lives.

The terraform output must be a string-typed PEM-encoded private key. `terraform output -raw <name>` returns the value regardless of the `sensitive` flag (the flag just suppresses display; the data is still readable to anyone with state access).

## Host-key TOFU and `~/.roksbnkctl/known_hosts`

`roksbnkctl` keeps its own `known_hosts` file at `~/.roksbnkctl/known_hosts`. **It does not read or write `~/.ssh/known_hosts`.** The two files are independent.

### Why a separate file

Three reasons:

1. **Isolation.** `roksbnkctl`'s SSH client is a different program from `ssh(1)`; mixing host-key state between the two creates surprising behaviour (deleting a key from `~/.ssh/known_hosts` doesn't clear it from `roksbnkctl`'s view, or vice versa).
2. **Audit.** A `roksbnkctl`-managed file lets the tool's behaviour be reasoned about without inspecting the user's broader SSH state.
3. **Cleanup.** `roksbnkctl ws delete <name>` could theoretically scrub host-key entries on workspace destroy; mixing into `~/.ssh/known_hosts` would mean editing a file the tool didn't own.

The format matches OpenSSH's `~/.ssh/known_hosts` exactly (so future cross-pollination is technically possible), but the filenames are deliberately separate.

### TOFU on first connect

The first time you connect to a target, `roksbnkctl` shows the host key fingerprint and asks whether to trust it:

```bash
$ roksbnkctl exec --on jumphost -- whoami
Add 169.45.91.177:22's key (SHA256:abc123def456ghi789jkl0mnopqrstuvwxyz/+=) to ~/.roksbnkctl/known_hosts? [y/N]: y
ubuntu
```

Answer `y` and the key is appended. Subsequent connects to the same `host:port` with the same server key trust silently.

Answer `n` and the connect aborts with exit code 126.

### Mismatch behaviour

If the host key changes — re-provisioned VM, MITM attack, configuration drift — `roksbnkctl` refuses to connect:

```
error: host key mismatch: 169.45.91.177:22 known with SHA256:abc123... but
       server presented SHA256:zyx987...; if the host was rebuilt, edit
       ~/.roksbnkctl/known_hosts
```

Same model OpenSSH uses. The fix is the same: edit (or `ssh-keygen -R`) the file to remove the stale entry, then re-connect to re-trigger the TOFU prompt.

The default `ssh-keygen` binary works against `~/.roksbnkctl/known_hosts` — pass `-f`:

```bash
ssh-keygen -R 169.45.91.177 -f ~/.roksbnkctl/known_hosts
```

## `--insecure-host-key` for CI

Automation contexts can't answer a TOFU prompt. The `--insecure-host-key` flag skips host-key verification entirely:

```bash
roksbnkctl exec --on jumphost --insecure-host-key -- whoami
```

This is **insecure** — anyone in the network path can MITM the connection — and is intended only for short-lived CI runs against ephemeral test infrastructure. Don't use it where session content is sensitive.

The flag is per-invocation, not per-target. There's deliberately no `targets.<name>.insecure_host_key: true` config knob: forcing the choice into the call site keeps the security implications visible at every invocation.

When to use it:

- E2E tests against a freshly-provisioned cluster jumphost where the host key is just-generated and changes per run.
- Pipeline runs against ephemeral test VMs that get torn down within minutes.
- Recovery scenarios where the known-hosts file is corrupt and you need to bootstrap.

When **not** to use it:

- Production jumphosts with stable identity.
- Customer environments where session integrity matters.
- Anything where the SSH session carries secrets you can't afford to leak to a passive attacker.

## `roksbnkctl targets` — full reference

Four subcommands. Chapter 16 introduces them with worked examples; here's the complete flag surface.

### `roksbnkctl targets list`

```
NAME       HOST                USER     KEY
jumphost   169.45.91.177:22    ubuntu   tf-output:jumphost_shared_key
bastion    ops.example.com:22  jgruber  file:~/.ssh/id_ed25519
prod-jump  10.0.0.5:22         ec2-user agent
```

Flags:

- `--verbose` / `-v`: also prints whether the target has a known-hosts entry recorded.
- `-o json`: machine-readable form. Schema: `{"targets": [{"name": ..., "host": ..., "port": ..., "user": ..., "key_source": ...}]}`.

The `KEY` column shows the source descriptor — never the key material. File-backed sources are prefixed `file:` to distinguish them visually from `tf-output:` and `agent`.

### `roksbnkctl targets show <name>`

```
name:        jumphost
host:        169.45.91.177
port:        22
user:        ubuntu
key_source:  tf-output:jumphost_shared_key
```

Same restriction: key material itself is never printed.

`-o json` is supported for scripted callers.

### `roksbnkctl targets add <name> ...`

Required flags: `--host`, `--user`, and exactly one of `--key-path` / `--key-source`.

```bash
# File-backed key
roksbnkctl targets add bastion \
  --host ops.example.com \
  --user jgruber \
  --key-path ~/.ssh/id_ed25519

# ssh-agent
roksbnkctl targets add prod-jump \
  --host 10.0.0.5 \
  --user ec2-user \
  --key-source agent

# Non-default port
roksbnkctl targets add custom \
  --host 10.0.0.5 \
  --user root \
  --key-path ~/.ssh/custom \
  --port 2222

# tf-output (rare; usually auto-populated by `up`)
roksbnkctl targets add backup-jump \
  --host 10.0.0.6 \
  --user ubuntu \
  --key-source tf-output:backup_jumphost_key
```

Refuses to add a target whose name collides with an existing entry — use `targets remove <name>` first, or pick a different name.

### `roksbnkctl targets remove <name>`

```bash
roksbnkctl targets remove bastion
```

Removes the entry from `config.yaml`. **Does not** remove the corresponding host-key line from `~/.roksbnkctl/known_hosts` — re-adding the same target later doesn't re-trigger TOFU. This is deliberate; if you want to wipe the host key too, edit the known-hosts file by hand.

## Auto-discovery from terraform outputs

The single most-used target — `jumphost` — is auto-populated post-`roksbnkctl up`. The flow:

1. `roksbnkctl up` runs `terraform apply` against the workspace's HCL.
2. After successful apply, `roksbnkctl` reads three outputs: `testing_tgw_jumphost_ip`, `testing_tgw_jumphost_user`, `jumphost_shared_key`.
3. If `testing_tgw_jumphost_ip` is non-empty AND not the literal sentinel string `"TGW jumphost not created"` (which the upstream HCL emits when the testing module is disabled), `roksbnkctl` writes a `jumphost` target into `config.yaml`:

   ```yaml
   targets:
     jumphost:
       host: <testing_tgw_jumphost_ip>
       user: <testing_tgw_jumphost_user || "ubuntu">
       key_source: tf-output:jumphost_shared_key
   ```

4. A confirmation line is printed:

   ```
   ✓ Auto-registered target jumphost (169.45.91.177); use `roksbnkctl --on jumphost ...`
   ```

The auto-population is **idempotent** — re-running `up` against an already-jumphost-populated workspace re-writes the same fields. If you've manually customised the entry (changed the user, swapped to `key_path`), the auto-population overwrites your changes. There's no merge logic; the latest `up` wins.

If `testing_create_tgw_jumphost = false` in tfvars, the upstream HCL skips creating the jumphost VM and emits the sentinel output. Auto-population is then a no-op, and you're free to create your own `jumphost` (or differently-named) entry via `targets add`.

### Per-AZ cluster jumphosts (`jumphost-<zone>`)

When `testing_create_cluster_jumphosts = true`, the deploy builds **one cluster jumphost per cluster-VPC availability zone** in addition to the single TGW jumphost — each on its own floating IP, all sharing the *same* key as the TGW jumphost. Since **v1.5.0**, the same post-`up` hook that seeds the singular `jumphost` also auto-registers one target per AZ:

1. After a successful `up`, `roksbnkctl` reads the `testing_cluster_jumphost_ips` terraform output — a map `{ zone => floating-IP }`.
2. For each `zone => fip`, it upserts a target named `jumphost-<zone>`, reusing the same shared key the singular `jumphost` uses:

   ```yaml
   targets:
     jumphost-ca-tor-1:
       host: <ca-tor-1-fip>
       user: ubuntu
       key_source: tf-output:jumphost_shared_key
     # …one per AZ…
   ```

3. A summary line is printed:

   ```
   ✓ Auto-registered 3 per-AZ cluster jumphost targets (jumphost-ca-tor-1, jumphost-ca-tor-2, jumphost-ca-tor-3); use `roksbnkctl --on jumphost-<zone> ...`
   ```

Verify with `roksbnkctl targets list` — you should see `jumphost` plus one `jumphost-<zone>` per AZ. Each is a first-class `--on` target (full `kubectl`/`oc`/`ibmcloud`/`shell` passthrough, no SSH hop): `roksbnkctl --on jumphost-ca-tor-2 kubectl get pods`. Like the singular `jumphost`, registration is **best-effort and idempotent** — a parse/write failure logs a single `warning:` and does not fail `up`, and re-running `up` after a floating-IP rotation refreshes each `jumphost-<zone>` host in place. When `testing_create_cluster_jumphosts = false` (or the output is absent/empty), this is a silent no-op — only `jumphost` is seeded, with no warning noise.

> **Orphaned-target caveat (option (a) upsert-only).** Auto-registration upserts but never prunes. If a later apply removes a zone, or `testing_create_cluster_jumphosts` is flipped to `false`, the now-orphaned `jumphost-<oldzone>` target points at a destroyed host and lingers in your config until you remove it by hand:
>
> ```bash
> roksbnkctl targets remove jumphost-ca-tor-3
> ```
>
> A host re-created on a recycled floating IP will also trip the host-key mismatch refusal — see [§"Host-key TOFU and `~/.roksbnkctl/known_hosts`"](#host-key-tofu-and-roksbnkctlknown_hosts) and clear the stale `known_hosts` line with `ssh-keygen -R <fip> -f ~/.roksbnkctl/known_hosts`. An automatic-prune (reconcile) mode that removes orphans on the next `up` is a deliberate post-v1.5.0 follow-up (it needs unambiguous "this target is auto-managed" ownership semantics so a hand-named `jumphost-mybox` is never deleted). See [PRD 09](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md).

> **Pre-v1.5.0 fallback.** On a release before v1.5.0 the per-AZ jumphosts are not auto-registered — register each by hand. Look up the floating IPs with the read-only `terraform` command (v1.5.0+; [Chapter 16 §"Per-AZ cluster jumphosts"](./16-on-flag-ssh-jumphosts.md#per-az-cluster-jumphosts)):
>
> ```bash
> roksbnkctl terraform output testing_cluster_jumphost_ips
> roksbnkctl terraform output testing_cluster_jumphost_ssh_commands
> ```
>
> …or, on an even older release without `roksbnkctl terraform`, the raw form `cd ~/.roksbnkctl/<ws>/state && TF_DATA_DIR=$PWD/terraform terraform output testing_cluster_jumphost_ips`. Then register one target per AZ — note `--key-source tf-output:jumphost_shared_key` is correct because one shared key covers *all* jumphosts (see [§"`key_source: tf-output:<output-name>`"](#key_source-tf-outputoutput-name)):
>
> ```bash
> roksbnkctl targets add jumphost-ca-tor-1 \
>   --host <ca-tor-1-fip> --user ubuntu \
>   --key-source tf-output:jumphost_shared_key
> # …repeat per zone…
> ```
>
> Each new IP triggers a one-time host-key TOFU prompt on first connect (see [§"Host-key TOFU and `~/.roksbnkctl/known_hosts`"](#host-key-tofu-and-roksbnkctlknown_hosts)). Manually-added targets are not auto-managed: a destroy+recreate rotates the FIPs and you must re-`targets add` (contrast the v1.5.0 auto-registered targets, which `up` refreshes in place).

### What is *not* auto-discovered

The auto-discovery flow registers the TGW `jumphost` (always) and, since v1.5.0, the per-AZ `jumphost-<zone>` targets when `testing_create_cluster_jumphosts = true`. It does **not** register:

- **Per-AZ jumphosts by *private* IP.** There is no top-level `testing_cluster_jumphost_private_ips` terraform output; the private-IP hop pattern in [Chapter 16 §"Per-AZ cluster jumphosts"](./16-on-flag-ssh-jumphosts.md#per-az-cluster-jumphosts) is a documented zero-setup technique, not an auto-registered target.
- **Any non-jumphost host.** Bastions, ops boxes, and the like are always `targets add` by hand.

### Inspecting what the post-`up` flow saw

When the auto-population doesn't happen and you expected it to, check:

```bash
roksbnkctl tf output testing_tgw_jumphost_ip
roksbnkctl tf output testing_tgw_jumphost_user
roksbnkctl tf output -json jumphost_shared_key | head -c 50
```

(The third one returns a JSON-encoded string for a sensitive output; truncate to confirm it's non-empty without dumping the key.)

If all three are populated and the auto-write didn't fire, that's a bug — file an issue with the output values redacted.

## What the SSH execution backend adds on top of `--on`

The `--on <target>` flag is the lightweight remote-exec path — one SSH session, one command, no remote state. The `ssh:<target>` **execution backend** layers more on top, reusing the same `internal/remote.Client` under [`internal/exec/ssh.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/exec/ssh.go):

| Capability | What it gives you |
|---|---|
| **File materialisation** | `RunOpts.Files` map gets written to `/tmp/roksbnkctl.<rand>/<basename>` on the remote, available as the working directory for the command. Cleanup via `trap 'rm -rf' EXIT` in a wrapper. |
| **Env passing with fallback** | First tries `ssh -o SetEnv=KEY=VALUE` (requires remote sshd `AcceptEnv`). On failure, writes a 0700 wrapper script that exports the env and execs the command, with `trap 'rm -f $0' EXIT` to scrub. |
| **Apt bootstrap** | If the remote target doesn't have a tool (`iperf3`, `ibmcloud`) installed, the backend can `sudo apt-get install` it on demand (Ubuntu only at v1.0). |
| **SCP-and-cleanup for kubeconfig** | The backend's recommended path for shipping a kubeconfig to the remote: SCP to a tempdir, run, `trap 'rm -rf' EXIT` to scrub. |
| **Wrapper-script credential propagation** | Detailed in [PRD 04 § SSH](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md). Brief on-disk window with strict cleanup. |

The `targets:` schema and the `roksbnkctl targets` commands are the same surface for both — the backend just uses each target in a heavier-weight way. Anything you set up for `--on` keeps working under `--backend ssh:<target>`.

The split between the lightweight `--on` path and the full `ssh` backend is deliberate: `--on` stays simple — one SSH session, one command, no remote state. The backend handles the heavier lifting (file materialisation, package installation, multi-step orchestration).

## Cross-references

- [Chapter 12 — Workspace config](./12-workspace-config.md) — where `targets:` fits in the overall schema.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) — the SSH-key sources from a credential-discipline perspective.
- [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md) — the user-facing prose for "how do I use this".
- [Chapter 17 — Execution backends](./17-execution-backends.md) — where the SSH backend sits in the broader backend matrix.
- [PRD 01 — SSH client + --on flag](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md) — the design rationale for `targets:` and the SSH client.
- `internal/remote/` package: <https://github.com/jgruberf5/roksbnkctl/tree/main/internal/remote>
- `internal/cli/targets.go`: <https://github.com/jgruberf5/roksbnkctl/blob/main/internal/cli/targets.go>
