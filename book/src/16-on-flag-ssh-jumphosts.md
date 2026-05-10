# The --on flag and SSH jumphosts

The `--on <target>` flag re-runs a `roksbnkctl` passthrough command (`exec`, `shell`, `kubectl`, `oc`, `ibmcloud`) on a remote SSH host instead of locally. After a successful `roksbnkctl up`, a `jumphost` target is auto-populated from the upstream HCL's terraform outputs, so the flag works with no manual configuration in the common case.

This chapter covers when to reach for `--on`, the `targets:` workspace config block, the auto-population behaviour, the `roksbnkctl targets` command tree for managing your own targets, and how host-key trust is established.

The full design rationale for this feature lives in [PRD 01](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md). This chapter is the user-facing distillation.

## Why this exists

There are a handful of scenarios where running a command from your laptop is the wrong answer:

- **Customer-firewall scenarios.** Your customer's network policy lets the corporate jumphost reach `*.cloud.ibm.com` but blocks your laptop's egress to anything except web traffic. `ibmcloud iam oauth-tokens` works from the jumphost; from your laptop it times out.
- **Air-gapped environments.** The cluster lives in a VPC with no public ingress, accessible only via a bastion VM. The cluster API server isn't reachable from your laptop at all; you need to be inside the network to talk to it.
- **Pre-cluster operations.** You want to run `ibmcloud` commands against the IBM Cloud API but your workstation doesn't have `ibmcloud` installed and you'd rather not install it. The jumphost has it; route through there.

`--on` makes those scenarios one flag rather than "ssh to the jumphost, install your tools there, copy your kubeconfig over manually". The SSH client is built into `roksbnkctl` (using `golang.org/x/crypto/ssh`); no host `ssh` binary is required.

## The `targets:` workspace config block

Targets are stored in your workspace config at `~/.roksbnkctl/<workspace>/config.yaml` under a `targets:` key:

```yaml
targets:
  jumphost:                                # auto-populated after `roksbnkctl up`
    host: 169.45.91.177
    user: ubuntu
    key_source: tf-output:jumphost_shared_key
    port: 22                               # default; can be omitted

  bastion:                                 # user-defined
    host: ops.example.com
    user: jgruber
    key_path: ~/.ssh/id_ed25519

  prod-jump:
    host: 10.0.0.5
    user: ec2-user
    key_source: agent                      # use ssh-agent
```

Each entry has at minimum `host` and `user`. Port defaults to `22`. Key resolution is determined by exactly one of `key_path` or `key_source` — see "Key sources" below.

You don't typically edit this file by hand. The auto-discovery flow populates `jumphost` for you, and `roksbnkctl targets add ...` populates other entries.

## Auto-discovery from `roksbnkctl up`

The upstream HCL provisions a small testing jumphost as part of every cluster apply. Two terraform outputs surface it:

- `testing_tgw_jumphost_ip` — the public IP of the jumphost VM.
- `jumphost_shared_key` — the private key (PEM) the jumphost was provisioned with, marked `sensitive` in the HCL.

After a successful `roksbnkctl up`, `roksbnkctl` reads both outputs and writes a `jumphost` target into your workspace config:

```
✓ Auto-registered target jumphost (169.45.91.177); use `roksbnkctl --on jumphost ...`
```

The auto-registered target uses `user: ubuntu` (the upstream HCL provisions an Ubuntu cloud image whose default user is `ubuntu`).

The `key_source: tf-output:jumphost_shared_key` form means the private key is **read from terraform state at SSH-connect time** rather than being copied into the workspace config or written to disk separately. The key only ever exists in terraform state and in memory during a connect; destroying and re-creating the cluster generates a new key, and `roksbnkctl` picks up the new one without any manual intervention.

If your cluster apply produced a `testing_tgw_jumphost_ip` output of `"TGW jumphost not created"` (the upstream HCL emits this string when the testing module is disabled) the auto-population is skipped. You can still add a `jumphost` target manually with `roksbnkctl targets add` if you have a different bastion in mind.

## Key sources

Three ways to tell `roksbnkctl` how to find the SSH private key:

1. **`key_path: <path>`** — a file on disk. Standard OpenSSH key formats are accepted (`~/.ssh/id_ed25519`, `~/.ssh/id_rsa`, etc.). Tilde expansion is honoured.

2. **`key_source: agent`** — talk to the user's `ssh-agent` over the socket pointed at by `$SSH_AUTH_SOCK`. The agent presents whichever keys it currently holds; `roksbnkctl` tries each in turn against the target's `authorized_keys`. This is the right setting if your team already manages keys via 1Password / hardware tokens / `gpg-agent` and you don't want a key file on disk. **Note**: ssh-agent integration is Linux/macOS-only in v0.7; Windows users should use `key_path` instead.

3. **`key_source: tf-output:<output-name>`** — read the key from the workspace's terraform state output of that name. Used by the auto-discovered `jumphost` target. The terraform output must be a string-typed PEM-encoded private key; sensitive outputs work fine because `terraform output -raw <name>` returns the value regardless of the sensitive flag.

Exactly one of `key_path` or `key_source` must be set per target. `roksbnkctl targets show <name>` will tell you which is in use without printing the key material.

## Host-key TOFU on first connect

The first time you connect to a target, `roksbnkctl` shows the host key fingerprint and asks whether to trust it. The prompt is a single line:

```bash
$ roksbnkctl exec --on jumphost -- whoami
Add 169.45.91.177:22's key (SHA256:abc123def456ghi789jkl0mnopqrstuvwxyz/+=) to ~/.roksbnkctl/known_hosts? [y/N]: y
ubuntu
```

Answer `y` and the key is appended to `~/.roksbnkctl/known_hosts` (the same format as OpenSSH's `~/.ssh/known_hosts`). Subsequent connects trust silently.

Answer `n` and the connect fails with a clear "host key not trusted" error.

If the host key changes between runs — which would happen on a re-provisioned VM, or could happen as a man-in-the-middle attack — `roksbnkctl` refuses to connect:

```
error: host key mismatch: 169.45.91.177:22 known with SHA256:abc123... but server presented SHA256:zyx987...; if the host was rebuilt, edit ~/.roksbnkctl/known_hosts
```

This is "trust on first use" (TOFU) — the same model OpenSSH uses for new hosts. Exit code is 126 on host-key rejections.

### `--insecure-host-key` for CI

In automation contexts where a TOFU prompt would block forever, pass `--insecure-host-key` to skip host-key verification entirely:

```bash
roksbnkctl exec --on jumphost --insecure-host-key -- whoami
```

This is **insecure** — anyone in the network path can MITM the connection — and is intended only for short-lived CI runs against ephemeral test infrastructure. Don't use it in any context where the SSH session matters for security.

## The `roksbnkctl targets` command tree

Four subcommands for managing target entries:

```bash
roksbnkctl targets list
roksbnkctl targets show <name>
roksbnkctl targets add <name> --host ... --user ... --key-path ...
roksbnkctl targets remove <name>
```

### `targets list`

```
roksbnkctl targets list
NAME       HOST                USER     KEY
jumphost   169.45.91.177:22    ubuntu   tf-output:jumphost_shared_key
bastion    ops.example.com:22  jgruber  file:~/.ssh/id_ed25519
```

Prints every target in the current workspace's config. The `KEY` column shows the key source — never the key material itself. File-backed keys are prefixed with `file:` so they're visually distinct from `tf-output:` and `agent` sources.

### `targets show <name>`

```
roksbnkctl targets show jumphost
name:        jumphost
host:        169.45.91.177
port:        22
user:        ubuntu
key_source:  tf-output:jumphost_shared_key
```

Prints the full record. Note that the key material itself is never printed — only the source descriptor (file path, ssh-agent, or terraform-output name).

### `targets add <name> ...`

```bash
roksbnkctl targets add bastion \
  --host ops.example.com \
  --user jgruber \
  --key-path ~/.ssh/id_ed25519

# or with ssh-agent:
roksbnkctl targets add prod-jump \
  --host 10.0.0.5 \
  --user ec2-user \
  --key-source agent

# or with a non-default port:
roksbnkctl targets add custom \
  --host 10.0.0.5 \
  --user root \
  --key-path ~/.ssh/custom \
  --port 2222
```

Writes the new target into `~/.roksbnkctl/<workspace>/config.yaml`. Refuses if a target of that name already exists (use `targets remove` first).

### `targets remove <name>`

```bash
roksbnkctl targets remove bastion
```

Removes the entry from `config.yaml`. Does not remove the corresponding line from `~/.roksbnkctl/known_hosts` — the host key stays recorded so re-adding the same target later doesn't re-trigger a TOFU prompt.

## Working examples

The everyday verbs:

```bash
# Run an arbitrary command on the jumphost
roksbnkctl exec --on jumphost -- whoami
# → ubuntu

roksbnkctl exec --on jumphost -- uname -a
# → Linux jumphost-vm 5.15.0-... #... SMP ... x86_64 GNU/Linux

# Interactive PTY shell
roksbnkctl shell --on jumphost
# → drops you into the jumphost's default shell as the configured user
# → exit returns you to your local prompt

# ibmcloud passthrough — runs `ibmcloud ks cluster ls` on the jumphost
# (handy when your laptop's network can't reach IBM Cloud APIs)
roksbnkctl ibmcloud --on jumphost ks cluster ls

# kubectl passthrough — same pattern
roksbnkctl kubectl --on jumphost get pods -A

# oc passthrough
roksbnkctl oc --on jumphost projects
```

Behaviour details worth knowing:

- **Streaming I/O.** stdout, stderr, stdin all stream in real time — the same as running the command locally. Long-running commands (`oc adm top nodes`, `ibmcloud ks cluster get` on a slow API call) work normally.
- **Exit code propagation.** The remote command's exit code is the local exit code. A failing remote command produces a non-zero `roksbnkctl` exit; a succeeding remote command produces `0`. CI scripts can rely on this.
- **TTY auto-detection.** `roksbnkctl shell --on` auto-allocates a PTY. Other verbs (`exec`, `kubectl`, `oc`, `ibmcloud`) run without a PTY in v0.7; if you need a PTY for `top` or another `isatty()`-sensitive command, fall back to `roksbnkctl shell --on jumphost` and run the command from the interactive shell.
- **Environment passthrough.** `IBMCLOUD_API_KEY`, `IBMCLOUD_REGION`, and `KUBECONFIG` are propagated to the remote session via SSH `SetEnv`, so `ibmcloud iam oauth-tokens` on the jumphost authenticates with the same key your local workspace uses. The remote sshd must be configured to accept `AcceptEnv IBMCLOUD_*` etc. for this to work; the upstream HCL's jumphost is already configured for it.

## What `--on` doesn't do (yet)

A few things deliberately deferred to later phases:

- **Lifecycle commands** (`up`, `down`, `plan`, `apply`) reject `--on` with a clear error in v0.7. Running terraform on a remote host has different state-handling considerations and is the job of v0.9's SSH execution backend ([Chapter 17](./17-execution-backends.md)).
- **ProxyJump / multi-hop SSH.** If your jumphost itself is reached through another bastion, that's not directly supported in v0.7. The upstream HCL's jumphost design lets the TGW jumphost reach cluster-internal VMs natively, so you usually don't need multi-hop in practice.
- **`~/.ssh/config` parsing.** Targets must be defined explicitly in workspace config; `roksbnkctl` does not read your existing `~/.ssh/config`.
- **Password auth.** Keys + agent only. Passwords are not supported and won't be.
- **SCP / SFTP.** File transfer is the SSH execution backend's job in v0.9. v0.7's `--on` does one-shot remote exec only.
- **Windows ssh-agent.** The `key_source: agent` path is Linux/macOS only in v0.7; Windows users must use `key_path` to a file. Already noted in [Key sources](#key-sources) above; called out here so a Windows reader who skipped to this section doesn't miss it.

## Cross-reference

[Chapter 17 — Execution backends](./17-execution-backends.md) (lands in v0.9) extends the SSH client used here into a full execution backend with file materialisation, env-file fallback for sshd configurations that can't `AcceptEnv`, and apt-bootstrap of missing tools on Ubuntu jumphosts. The `--on` flag stays as the lightweight one-shot path; `--backend ssh` is the deeper integration. The two are designed to share the same `internal/remote.Client` so what you learn here translates directly.

For the design rationale, edge cases, and open questions, read [PRD 01](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md) — this chapter is the user-facing surface; PRD 01 is the developer-facing surface.
