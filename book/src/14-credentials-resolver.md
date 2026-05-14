# Credentials and the resolver chain

`roksbnkctl` handles four kinds of secrets: an IBM Cloud API key, a kubeconfig, an SSH private key, and the Terraform state file. Each has a different threat model, a different lookup chain, and a different rule for "what's safe to commit to a workspace".

This chapter is the user-facing distillation of [PRD 04 — credential propagation](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md). PRD 04 is the design surface for developers extending the credential system; this chapter is the operational surface for users who need to know "where does my key live, and how does the tool find it".

## The four secrets in scope

| Credential | Used by | Resolved from |
|---|---|---|
| **IBMCLOUD_API_KEY** | `ibmcloud` CLI, terraform's IBM provider, IBM SDK calls | Env → OS keychain → workspace `api_key_b64` → prompt |
| **kubeconfig** | `kubectl`/`oc` passthroughs, `roksbnkctl k get/apply/...`, terraform's k8s + helm providers | `KUBECONFIG` env → `~/.kube/config` (kubectl-style) |
| **SSH private key** | The SSH client backing `--on` and the `ssh:<target>` execution backend | Per-target: file path, ssh-agent, or `tf-output:<name>` |
| **Terraform state** | The `terraform-exec` calls inside `roksbnkctl up`/`apply`/`destroy` | Workspace `state/terraform.tfstate` (filesystem only) |

Each has its own discovery rules. Walk them in turn.

## The IBMCLOUD_API_KEY resolver chain

The single most-used credential. Resolved by `internal/cred/resolver.go` (extracted this sprint from the formerly scattered logic in `internal/config/secrets.go`). The resolver walks four sources in order:

```
1. Environment variables (process-scoped, never persisted)
2. OS keychain (per-user, system-managed)
3. Workspace config api_key_b64 (per-workspace, base64 obfuscation)
4. Interactive prompt (TTY-only)
```

The first source that yields a non-empty value wins. The chain stops there — the key isn't re-fetched from a "more authoritative" source on subsequent calls.

### Source 1 — Environment

The resolver checks these env vars in order, returning the first non-empty value:

```
IBMCLOUD_API_KEY            # canonical
IC_API_KEY                  # short alias
TF_VAR_ibmcloud_api_key     # terraform passthrough form
TF_VAR_IBMCLOUD_API_KEY     # uppercase variant some pipelines use
TF_VAR_IC_API_KEY           # uppercase variant of the short alias
```

Env vars are first because they're the **most explicit** path — if you've gone to the trouble of setting one, you've made a deliberate choice. Pre-existing CI pipelines, automation scripts, and `direnv` setups all live here. The resolver respects that ordering even when a keychain entry also exists.

### Source 2 — OS keychain

`roksbnkctl` stores per-workspace API keys in the OS-native keychain via [`github.com/zalando/go-keyring`](https://github.com/zalando/go-keyring):

| OS | Backend |
|---|---|
| macOS | Keychain (`security` framework) |
| Linux (with libsecret) | GNOME Keyring / KWallet via Secret Service API |
| Windows | Credential Manager |
| Linux (no libsecret) | Falls back to source 3 (config base64) |

Entries are namespaced under service `roksbnkctl`, with user `<workspace>/ibmcloud_api_key`:

```bash
# What `roksbnkctl init` writes (no-op shown for clarity)
$ keyring set roksbnkctl dev/ibmcloud_api_key
```

This is the **recommended** secure default. The OS handles process isolation; `roksbnkctl` only sees the value during the brief window between fetch and use.

### Source 3 — Workspace `api_key_b64`

A base64-encoded blob in `~/.roksbnkctl/<workspace>/config.yaml`:

```yaml
ibmcloud:
  api_key_b64: ZW5jb2RlZC1hcGkta2V5LXZhbHVl
```

**Important framing**: base64 is **obfuscation**, **not encryption**. Anyone with read access to the file can decode it instantly:

```bash
echo -n "ZW5jb2RlZC1hcGkta2V5LXZhbHVl" | base64 -d
# → encoded-api-key-value
```

The encoding exists for two reasons:

1. **Visual.** A glancing `cat config.yaml` doesn't surface the literal API key.
2. **Format.** API keys can contain `=` and other YAML-special characters that complicate inline storage. Base64 normalises them.

`api_key_b64` is the fallback when the OS keychain isn't available — most commonly **WSL2 without libsecret**, **headless Linux servers**, and **CI runners** where bringing up a keychain daemon is more friction than it's worth. Treat the file like a plaintext credential: `chmod 0600`, never commit, never share.

> **File-mode note**: `roksbnkctl init` writes `config.yaml` mode `0644` by default (chapter 12 §"File permissions"). When you populate `api_key_b64`, `chmod 0600` the file yourself — and re-chmod after any subsequent `roksbnkctl init` that re-writes the file, since `init` doesn't preserve the tightened mode. The keychain and env-var paths sidestep this entirely: nothing sensitive lands in `config.yaml`, so the default `0644` is fine.

The plaintext field name `api_key:` is **rejected** at workspace-load time:

```
$ roksbnkctl up
error: ~/.roksbnkctl/dev/config.yaml: plaintext secret detected (offset 47):
       workspace config.yaml must not contain credentials — use IBMCLOUD_API_KEY
       env var or the OS keychain (see `roksbnkctl init`)
```

The regex catches `api_key`, `apikey`, `ibmcloud_api_key`, `password`, `token`, `secret_access_key`, `hmac_secret`. The `_b64` suffix is the documented escape — it's the only inline form the loader tolerates.

### Source 4 — Interactive prompt

When sources 1-3 all come up empty AND stdin is a TTY, the resolver prompts:

```
Enter IBM Cloud API key for workspace "dev": ********
Save the key for future runs? [Y/n]: y
  ✓ saved to OS keychain
```

The key is read with echo disabled (via `golang.org/x/term`). The prompt offers to persist — by default it tries the OS keychain first, falls back to `api_key_b64` in `config.yaml` if the keychain is unavailable.

If stdin **isn't** a TTY (CI runner, piped input, daemon process), the resolver errors instead of hanging:

```
error: no IBM Cloud API key available and stdin is not a TTY (cannot prompt;
       set IBMCLOUD_API_KEY or run `roksbnkctl init`)
```

### Pinning a single source

The chain is the default. To force one specific source, set `ibmcloud.api_key_source` in `config.yaml`:

```yaml
ibmcloud:
  api_key_source: keychain    # env | keychain | config | prompt
```

This is useful in two scenarios:

- **CI**: `api_key_source: env` makes a missing env var a hard error rather than falling through to a (locked / non-existent) keychain.
- **Auditable single-source-of-truth**: pinning to `keychain` documents that this workspace's key lives in the OS keychain and nowhere else; reading the key from a different source becomes an error rather than a silent fallback.

## kubeconfig discovery

Different chain, different rules. `roksbnkctl` discovers the kubeconfig the same way `kubectl` does — two sources, in this order:

```
1. KUBECONFIG environment variable (first existing path in a colon-separated list)
2. ~/.kube/config
```

This is the kubectl-standard discovery chain, implemented in `internal/k8s/client.go::DefaultKubeconfigPath()`. Whatever you've already taught `kubectl` to read, `roksbnkctl` reads too.

`cluster up`'s post-apply step writes the admin kubeconfig to `~/.kube/config` (mode `0600`) by default — so the second source in the chain is also the destination of the tool's own output, and the same `KUBECONFIG`-overrides-everything rule applies. If `KUBECONFIG` is set when `cluster up` runs, the download lands at that path instead.

> **Note**: there is also a `~/.roksbnkctl/<workspace>/state/kubeconfig/` **directory** under the workspace state dir. It's a Terraform input (`kubeconfig_dir` tfvar) that the upstream HCL writes per-component sub-files into (`cert_manager`, `cne_instance`, `flo`, `license`); it is not a kubeconfig file the resolver reads. Don't confuse the two.

### When the file is missing

If neither source yields a kubeconfig, commands that need one error with:

```
error: no kubeconfig: KUBECONFIG env not set, ~/.kube/config not present.
       Run `roksbnkctl cluster up`, `roksbnkctl cluster register <name>`,
       or set KUBECONFIG.
```

The remediation message tells you which path to take. `cluster register <name>` is the path for an existing cluster you want to adopt without re-creating it (see [Chapter 9](./09-registering-existing-cluster.md)).

### File permissions

`cluster up` writes `~/.kube/config` `chmod 0600` (owner read/write only). It contains the cluster admin token; treat it like a credential. Don't commit it, don't email it, don't `cat` it in screen-shared sessions.

## SSH private keys

Per-target, not per-workspace. Each entry under `targets:` in `config.yaml` declares exactly one of:

| Source | Form | Notes |
|---|---|---|
| **File** | `key_path: ~/.ssh/id_ed25519` | Standard OpenSSH key formats. Tilde expansion honoured. |
| **Agent** | `key_source: agent` | Talks to ssh-agent over `$SSH_AUTH_SOCK`. Linux/macOS only at v1.0; Windows ssh-agent named-pipe support is on the v1.x roadmap. |
| **TF output** | `key_source: tf-output:jumphost_shared_key` | Reads from terraform state at connect time; never written to disk separately. |

The `tf-output:` form is the auto-discovered jumphost path — the upstream HCL provisions a `tls_private_key` resource per cluster create, marks it `sensitive`, and surfaces it as a terraform output. `roksbnkctl` reads the output via `terraform output -raw` at SSH-connect time, never persists it, and the key only exists in TF state plus in memory during a connect.

[Chapter 15 — SSH targets](./15-ssh-targets.md) is the deep reference for the `targets:` block; this chapter just notes the credential-side framing.

## Terraform state

`~/.roksbnkctl/<workspace>/state/terraform.tfstate` is the workspace's terraform state file. It contains:

- IBM Cloud admin tokens (cluster admin, COS HMAC credentials)
- Generated TLS private keys (the jumphost shared key referenced above)
- Sensitive outputs (FAR auth bundles, license JWTs)
- Every resource attribute terraform tracks

It is **plaintext-credential-equivalent**. The file mode is `0600`; the parent directory is `0700`. Backup the workspace dir intact, never commit it to git, treat compromise of the state file as compromise of every secret it contains.

There is no separate "TF state credential" — the file's filesystem ACL is the only access control. PRD 04 covers the cross-backend story for moving state into a Docker bind-mount, a Kubernetes Secret, or an SCP'd remote temp directory; at v1.0 the local file is the only path (`terraform --backend k8s` / `ssh` are deferred to v1.x; see [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) §"What's deliberately deferred to post-v1.0").

## What's safe to commit vs not

A short rule:

```
SAFE TO COMMIT:    nothing in ~/.roksbnkctl/<workspace>/
NOT SAFE:          everything in ~/.roksbnkctl/<workspace>/
```

The longer version, by file:

| Path | Commit? | Why |
|---|---|---|
| `~/.roksbnkctl/<ws>/config.yaml` | **No** | Even without `api_key_b64`, this file documents your cluster identity, region, COS bucket — useful inventory for an attacker. |
| `~/.roksbnkctl/<ws>/config.yaml` (with `api_key_b64`) | **Hard no** | The base64 value is plaintext-equivalent. Committing it = leaking the key. |
| `~/.roksbnkctl/<ws>/state/kubeconfig` | **No** | Cluster admin token. |
| `~/.roksbnkctl/<ws>/state/terraform.tfstate` | **No** | Every secret terraform manages, in plaintext. |
| `~/.roksbnkctl/<ws>/state/terraform.tfvars` | **No** | Generated; references no secrets directly but documents resource layout. |
| `~/.roksbnkctl/<ws>/terraform.tfvars.user` | **Maybe** | If you've kept secrets out (no `bigip_password`, no `ibmcloud_api_key`), it's just config. Audit before committing. |
| `~/.roksbnkctl/<ws>/cluster-outputs.json` | **No** | Cluster identity + COS instance name. Not directly a secret but tied to the workspace. |
| `~/.roksbnkctl/known_hosts` | **Yes (if you want)** | Host-key fingerprints; not a secret. Same threat model as OpenSSH's `~/.ssh/known_hosts`. |

The simplest policy: a `.gitignore` that excludes the entire `~/.roksbnkctl/` tree. If you really want to share a workspace skeleton with a colleague, send the `config.yaml` minus `api_key_b64` and let them re-run `roksbnkctl init` against their own account.

## How `roksbnkctl init` writes the API key

Walk through the writeable side of the resolver:

```bash
$ roksbnkctl init
Workspace name [default]: dev
IBM Cloud region [ca-tor]:
Enter IBM Cloud API key (input hidden): ********
Save the key for future runs? [Y/n]: y
  ✓ saved to OS keychain
```

What just happened:

1. `init` prompted for the key. Input echo was off; the key never appeared on screen.
2. The user said "save".
3. `SaveAPIKeyForWorkspace` tried `SaveAPIKeyToKeychain` first.
4. The OS keychain accepted the entry (Linux + libsecret in this case). The success path returned `"OS keychain"` and `init` printed the confirmation.
5. The key was **not** written to `terraform.tfvars` (that's the resolver's job at terraform-invoke time, via the `TF_VAR_ibmcloud_api_key` env var).

If step 4 had failed (no keychain, WSL2 without libsecret), `SaveAPIKeyForWorkspace` would have fallen through to `saveAPIKeyToConfig` — base64-encoded the key, written it into `config.yaml`'s `api_key_b64` field, returned `"config.yaml (base64)"`. `init` would have printed:

```
  ✓ saved to config.yaml (base64)
  warning: base64 is obfuscation, not encryption — chmod 0600 the file
```

Both destinations work. The keychain path is the recommended default; the config-b64 path is the documented fallback.

## What's new in v1.2: the cred-tmpfile and trusted-profile paths

`v1.2.0` closes the two longest-deferred items from [PRD 04 §"Open questions"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md#open-questions): `roksbnkctl --backend docker` no longer leaks `IBMCLOUD_API_KEY` in `docker inspect`, and `roksbnkctl --backend k8s ops install` auto-provisions an IBM Cloud trusted profile so the ops pod never sees a static API key. Both have fallbacks for environments where the new path doesn't apply; v1.0.x / v1.1.x workspaces continue to work without change.

### The tmpfile-bind-mount pattern (docker backend)

The docker backend writes the resolved `IBMCLOUD_API_KEY` to a `0600` tempfile on the host, bind-mounts that single file read-only at `/run/secrets/ibmcloud_api_key` inside the container, and points the container at the file via `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key`. The value never appears in the container's stored env metadata — `docker inspect <id>` shows the path, not the key. The tempfile is owned by the calling user and is removed on backend exit (and on context cancellation, so an interrupted run still cleans up).

You don't have to do anything to opt in — the pattern is the default for `--backend docker` on v1.2 and up. The engineering shape (lifecycle, the inline `sh -c` shim that re-exports the value into the legacy `IBMCLOUD_API_KEY` env name for tools that read from env, the why-not-just-use-`--secret` discussion) lives in [PRD 04 §"Resolved in Sprint 9" → "Cred tmpfile-bind-mount pattern (docker backend)"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md#cred-tmpfile-bind-mount-pattern-docker-backend). For most users the takeaway is one line: in `v1.2`, `--backend docker` is `docker inspect`-clean.

### The `--trusted-profile` flag (k8s backend)

New flag on `roksbnkctl ops install` that controls how the ops pod gets its IBM Cloud credential. Three values:

| Value | What it does | When to use |
|---|---|---|
| `auto` (default) | Try to provision an IBM Cloud trusted profile (`roksbnkctl-ops-<workspace>`) linked to the ops pod's ServiceAccount. The pod assumes the profile via its projected SA token and the static API key never lands in any Secret. If your workspace API key doesn't have IAM `iam-identity` permissions, automatically fall back to the v1.0.x static-key Secret with a stderr warning that names the missing perm and how to silence it (`--trusted-profile=off`). | Default for new installs. Production users get the secure path automatically; restricted-IAM users still complete `ops install` successfully. |
| `on` | Try to provision; fail loudly with a non-zero exit if perms don't allow. No fallback. | CI / hardened environments where the static-key path is unacceptable and a perm-missing case should block, not warn. |
| `off` | Skip the trusted-profile path entirely; provision the v1.0.x static-key Secret (matches v1.0.x / v1.1.x behaviour). | Compatibility / debugging — and the documented path for clusters whose IAM admin doesn't grant `iam-identity` perms and isn't expected to. |

[Chapter 19 — The in-cluster ops pod](./19-in-cluster-ops-pod.md#trusted-profile-flow-v12) walks through the `--trusted-profile=auto` install flow, the verification commands (`oc get serviceaccount roksbnkctl-ops -o yaml` showing the trusted-profile annotation), the fallback warning shape, and how `ops uninstall` cleans up a provisioned profile.

### Compatibility note

v1.0.x and v1.1.x workspaces continue to work without migration. The docker tmpfile pattern is a transparent replacement — the resolver chain is unchanged, the workspace config is unchanged, and no flag is required to opt in. The k8s `--trusted-profile=auto` default with auto-fallback means existing workspaces against an IAM-restricted key keep getting the static-key Secret as before, with one extra stderr warning line on `ops install` naming the fallback and how to silence it. Setting `--trusted-profile=off` reproduces the v1.0.x behaviour byte-for-byte (no warning, static-key Secret straight away).

## Backend-specific cred propagation

The credential-propagation rules differ per backend. All four backends ship at v1.0:

| Backend | Where creds live | Mechanism |
|---|---|---|
| `local` | The user's environment | `os/exec` inherits parent env |
| `docker` | Caller's env, propagated by reference | `docker run --env IBMCLOUD_API_KEY` (no `=value`) — value inherits, never appears in `docker inspect` |
| `k8s` | Kubernetes Secret in the `roksbnkctl-ops` namespace | Mounted into the ops pod via `envFrom: secretRef`; or IAM trusted profile (preferred) |
| `ssh` | Remote env or wrapper script | `ssh -o SetEnv=IBMCLOUD_API_KEY=...` first; falls back to a 0700 wrapper script with `trap rm EXIT` |

Each backend's "where creds live" surface is summarised in [Chapter 17 — Execution backends](./17-execution-backends.md); the design rationale is in [PRD 04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md).

The user-facing invariant across all four: you put the key into one of the resolver chain's sources, and `roksbnkctl` figures out the rest. You don't have to learn four different credential APIs to use four different backends.

## The redactor

`roksbnkctl` writes a fair amount to its own logs (stdout, stderr) — terraform plan output, ibmcloud CLI output, error traces. Anywhere we can plausibly print the IBM API key (because a downstream tool printed it, because an error message included it, because a debug trace dumped a struct), the redactor masks it before the bytes leave the binary.

What gets redacted:

- The IBM Cloud API key value, anywhere it appears in `Stdout` or `Stderr` of an exec backend's `RunOpts`. Replaced with `[REDACTED]`.
- The same value in `roksbnkctl`'s own log output (the lifecycle commands that wrap terraform-exec).

What does **not** get redacted:

- Output captured by callers via `-o yaml`/`-o json` for resources that legitimately contain the key (e.g., a `Secret` returned from `roksbnkctl k get`). The redactor doesn't know about Kubernetes resource semantics; if you `k get secret -o yaml`, you'll see the key. (The same is true of `kubectl`.)
- Output from a tool you ran outside `roksbnkctl` (e.g., piping to `tee` after invoking `terraform` directly). The redactor only sees bytes that pass through the exec backend's `Stdout`/`Stderr` writers.
- The terraform state file. State is on-disk; the redactor is an in-memory stream filter.

The implementation is `internal/exec/redact.go` — a wrapping `io.Writer` with byte-comparison redaction and cross-write prefix buffering (so a secret split across two `Write` calls still gets masked). The matcher uses the resolved API key value verbatim (a known string at run-time) rather than a generic "looks like an IBM API key" pattern, to avoid false positives on legitimate output.

PRD 04's acceptance criteria require that the API key never appears in `docker inspect`, `ps -ef`, `kubectl get pods/events -o yaml`, or `kubectl describe pod`. The redactor is the defence-in-depth layer; the per-backend cred-propagation rules are the primary control.

## Cross-references

- [PRD 04 — credential propagation across execution backends](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md) — the full design.
- [Chapter 12 — Workspace config](./12-workspace-config.md) — the `ibmcloud:` block schema.
- [Chapter 13 — Terraform variables](./13-terraform-variables.md) — why `ibmcloud_api_key` doesn't go in tfvars.
- [Chapter 15 — SSH targets](./15-ssh-targets.md) — the SSH key sources.
- [Chapter 17 — Execution backends](./17-execution-backends.md) — backend-specific cred mechanics.
- `internal/cred/resolver.go` — the implementation extracted this sprint: <https://github.com/jgruberf5/roksbnkctl/blob/main/internal/cred/resolver.go>
- `internal/config/secrets.go` — the keychain + config-b64 helpers: <https://github.com/jgruberf5/roksbnkctl/blob/main/internal/config/secrets.go>
