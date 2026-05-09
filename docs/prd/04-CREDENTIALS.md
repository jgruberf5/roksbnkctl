# PRD 04 — credential propagation across execution backends (cross-cutting)

> Cross-cutting concern for [PRD 03 (execution backends)](./03-EXECUTION-BACKENDS.md). Read this before designing or reviewing the backend interfaces.
>
> Estimated effort: small in code (~300 LOC), medium in design care.

## Goal

Single source of truth for how `roksbnkctl` propagates secrets — kubeconfig contents, IBM Cloud API keys, SSH keys, terraform state — across every execution backend (local, docker, k8s, ssh). Every external-tool invocation that needs creds gets them via the same documented mechanism per backend, with security tradeoffs explicit.

## The credentials in scope

| Credential | Used by | Today's source |
|---|---|---|
| **kubeconfig** | kubectl, oc, terraform's k8s/helm providers, in-cluster probes | `~/.roksbnkctl/<ws>/state/kubeconfig` (workspace path), or `~/.kube/config`, `KUBECONFIG` env |
| **IBMCLOUD_API_KEY** | ibmcloud CLI, terraform IBM provider, IBM SDK calls | env var, OS keychain, base64 in workspace config (`api_key_b64`) |
| **SSH private key** | SSH backend itself | file path, ssh-agent, TF state output (jumphost shared key) |
| **TF state** | terraform | local file `~/.roksbnkctl/<ws>/state/terraform.tfstate` (contains sensitive values like admin tokens, certs) |

## Backend × credential matrix

The recommendations below are the **default** path. Each backend documents fallbacks for environments where the recommended path doesn't apply.

### Local exec

| Cred | Mechanism | Risk |
|---|---|---|
| kubeconfig | `KUBECONFIG` env var pointing at workspace file path | env vars visible via `/proc/<pid>/environ` to user-owned processes — single-user assumption |
| IBMCLOUD_API_KEY | env var (also `TF_VAR_ibmcloud_api_key` for terraform) | same |
| SSH key | n/a | |
| TF state | file path — terraform-exec reads/writes directly | filesystem ACL only; backup the workspace dir, treat as plaintext credential |

**Today's behavior; no change.**

### Docker container

| Cred | Mechanism | Risk | Mitigation |
|---|---|---|---|
| kubeconfig | Bind-mount the kubeconfig **file** (not its parent dir) read-only at `/root/.kube/config` | container can read other host files if mount is wider than the single file | Mount the SINGLE FILE; `chmod 0600` the source before mounting |
| IBMCLOUD_API_KEY | `--env IBMCLOUD_API_KEY` (no `=value`) — value inherits from caller's env | `docker inspect` shows env var **name** but not value (unless caller used `=value` form) | Always use `--env IBMCLOUD_API_KEY` form, never `--env IBMCLOUD_API_KEY=...` |
| SSH key | n/a | | |
| TF state | bind-mount `~/.roksbnkctl/<ws>/state/` read-write | container could leak via image layers if it modifies | Write atomically to a tempfile, rename; never write into a path inside the bind that's also in the image layer |

**Anti-patterns to avoid**:

- ❌ `--env IBMCLOUD_API_KEY=$KEY` — value visible in `docker inspect`
- ❌ `--env-file <(echo IBMCLOUD_API_KEY=$KEY)` — process substitution path visible in `ps`
- ❌ Bind-mounting `~/.kube/` (parent dir) — exposes other clusters' configs
- ❌ Bind-mounting `~/.ssh/` — exposes user's private keys to the container

**Recommended docker run shape**:

```bash
docker run --rm \
  -v "$WORKSPACE_DIR/state/kubeconfig:/root/.kube/config:ro" \
  -e IBMCLOUD_API_KEY \
  ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v> ks cluster ls
```

### In-cluster pod (k8s backend)

| Cred | Mechanism | Risk | Mitigation |
|---|---|---|---|
| kubeconfig | The pod's auto-mounted serviceaccount token at `/var/run/secrets/kubernetes.io/serviceaccount/`; **NOT** the user's local kubeconfig | RBAC must grant the SA the right permissions; less than admin (good!) | Bind a `roksbnkctl-ops` ClusterRole with the minimum verbs |
| IBMCLOUD_API_KEY | Kubernetes Secret `roksbnkctl-ibm-creds`, mounted as env var via `valueFrom: secretKeyRef` | cluster admins can read all Secrets; SA tokens grant access | Encrypt-at-rest in etcd (cluster's `--encryption-provider-config` setting); short-lived tokens via IBM IAM trusted profiles where possible |
| SSH key | not propagated into the cluster — SSH backend doesn't run from inside the cluster | | |
| TF state | If running terraform from a Job: stash state in a Secret (versioned by gen-ID), mount, lock via leader election in case of concurrent runs | state file size cap: ~1 MiB per Secret; very large states need a different store | Recommend external backend (S3, COS) for terraform state when using k8s backend |

**Recommended path: IAM trusted profile** instead of static API key.

The upstream `flo` module already provisions `ibm_iam_trusted_profile.cne_controller` linked to the cluster's serviceaccounts. The K8s backend can do the same — provision a `roksbnkctl-ops` trusted profile linked to the ops pod's SA. The pod assumes the trusted profile via the projected SA token, gets short-lived IAM tokens, and the static API key never transits.

If trusted profile isn't available: static key in a Secret, with these guards:

1. Secret has `metadata.annotations: helm.sh/resource-policy: keep` so accidental destroys don't clear it
2. Cluster's etcd has encryption-at-rest enabled (verify in `roksbnkctl ops install`)
3. Secret is only readable by the ops pod's SA — RoleBinding scoped, not ClusterRoleBinding
4. Doctor includes `roksbnkctl doctor --backend k8s` that confirms the Secret exists and is access-checked

**`roksbnkctl ops install`** (new command, Phase 3): creates the namespace, ServiceAccount, ClusterRole, RoleBinding, Secret, and Deployment for the long-lived ops pod. Idempotent. Document the privileges granted in the command's `--help` output.

### SSH host (ssh backend)

| Cred | Mechanism | Risk | Mitigation |
|---|---|---|---|
| kubeconfig | Two options — see below | | |
| IBMCLOUD_API_KEY | `ssh -o SetEnv=IBMCLOUD_API_KEY=...` (preferred) — falls back to wrapper script | wrapper-script content includes the secret | Wrapper file mode 0700, `set +o history`, `trap 'rm -f $0' EXIT` |
| SSH key | the **backend's** key (separate from creds-for-tools) | standard SSH key handling | rely on ssh-agent for the user's interactive key; the jumphost-shared TF-output key has its own threat model (it's a tls_private_key generated per cluster create) |
| TF state | scp pre-run, scp back post-run; treat as a state move | brief plaintext on remote | `umask 077` before write; explicit `rm -f` in a trap |

**Kubeconfig options for SSH backend**:

**Option A (recommended): scp-and-cleanup**

```bash
# pseudo-flow
scp $WORKSPACE/state/kubeconfig $TARGET:/tmp/roksbnkctl.$RAND/kubeconfig
ssh $TARGET "
  trap 'rm -rf /tmp/roksbnkctl.$RAND' EXIT
  KUBECONFIG=/tmp/roksbnkctl.$RAND/kubeconfig $TOOL $ARGS
"
```

Pros: simple, works with any sshd config. Cons: brief on-disk window on the remote.

**Option B: ssh local port-forward + synthesized in-memory kubeconfig**

```bash
ssh -L 6443:<master-url>:443 $TARGET "
  KUBECONFIG=<(echo '<inline-kubeconfig pointing at localhost:6443>') $TOOL $ARGS
"
```

Pros: no kubeconfig file written to remote. Cons: `<()` process substitution doesn't work over SSH unless the remote shell supports it; complex to get right; master URL might require SNI/TLS-SNI matching.

**Recommendation: Option A** for simplicity. Set strict umask before scp, explicit rm in trap.

**Env var propagation over SSH (the IBMCLOUD_API_KEY case)**:

OpenSSH has two relevant directives in remote `sshd_config`:

- `AcceptEnv KEY1 KEY2 ...` — accepts client-provided env vars matching these names
- `SetEnv KEY1=value ...` — server-side hardcoded env (not what we want)

Client side: `ssh -o SetEnv=KEY=value target ...` sends the env var if the server's `AcceptEnv` permits it.

**Default**: try `SetEnv`. If it fails (server logs `refused setenv request`), fall back to a wrapper script:

```bash
# wrapper script written to /tmp/roksbnkctl.$RAND/wrap.sh on remote, chmod 0700
#!/bin/sh
trap 'rm -f "$0"' EXIT
set +o history
export IBMCLOUD_API_KEY='<value>'
exec "$@"
```

Then `ssh target /tmp/roksbnkctl.$RAND/wrap.sh ibmcloud iam oauth-tokens`.

**Risk**: wrapper script content includes the secret. Mitigations: 0700 perms (only the SSH user can read), `trap 'rm' EXIT` removes it as soon as the wrapper exits, the key isn't in argv (so `ps` doesn't show it). Brief on-disk window.

**Doctor + `roksbnkctl targets show <name>` reports** which mechanism the target uses (SetEnv vs wrapper) so users can configure their sshd to allow SetEnv if they prefer.

## Cross-backend principles

1. **Never log credentials.** Every backend wraps its own logging to redact `IBMCLOUD_API_KEY=...` patterns from stdout/stderr that pass through `RunOpts.Stdout/Stderr`. Implementation: a wrapping `io.Writer` with regex-based redaction.

2. **Never put credentials in argv.** They go via env, file, or stdin. `argv` shows up in `ps`, `kubectl get pods -o yaml`, `docker inspect`. The Backend interface's `argv []string` parameter must NEVER include a secret string.

3. **Minimum lifetime.** Tempfiles deleted via `defer` / `trap`; env vars unset after the command finishes when the process is reused (long-lived ops pod scenario).

4. **Least privilege per backend.**
   - K8s SA bound to a ClusterRole with only the verbs `roksbnkctl ops install` declares
   - SSH key bound to the jumphost user account (not a shared root)
   - Docker bind-mounts limited to specific files (not directories)

5. **Documented escape hatches.** If a user can't satisfy the recommendation (e.g., can't modify sshd_config to AcceptEnv), the fallback is documented and warned. Doctor surfaces these warnings.

6. **Cred lifecycle ties to workspace lifecycle.**
   - `roksbnkctl ws delete <ws>`: removes keychain entry, scrubs `api_key_b64` from config, deletes `~/.roksbnkctl/<ws>/state/kubeconfig`
   - `roksbnkctl down`: state file emptied (terraform's normal behavior); kubeconfig kept (cluster-shared services may have lingered)
   - `roksbnkctl ops uninstall`: deletes the cluster-side Secret + SA (cluster-side credential lifecycle)

## Implementation tasks

1. **`internal/exec/creds.go`** — defines `Credentials` struct (kubeconfig bytes, IBM API key, etc.) and per-backend serializers (`ToDockerArgs`, `ToK8sEnvVars`, `ToSSHWrapper`)
2. **Each backend's `Run()`** accepts `RunOpts.Credentials`, wires them via the documented mechanism, cleans up on exit (defer-based unlink for files, kill ENV vars from process state)
3. **Logging redactor**: `internal/exec/redact.go` — middleware on `RunOpts.Stdout/Stderr` that masks the IBM API key value if it ever appears in output (defense-in-depth — backends shouldn't print it, but if a tool does, we redact)
4. **K8s backend RBAC**: `internal/exec/k8s_install.yaml` — namespace, SA, ClusterRole (minimum verbs), RoleBinding (NOT ClusterRoleBinding), Secret stub. Applied by `roksbnkctl ops install`.
5. **SSH SetEnv detection**: probe whether the remote sshd accepts `SetEnv`. Implementation: send a sentinel env var (`ROKSBNKCTL_SETENV_TEST=ok`), run `echo $ROKSBNKCTL_SETENV_TEST`, check output. Cache the result per-target in workspace state.
6. **Doctor extensions**:
   - `roksbnkctl doctor --backend docker` confirms daemon, can pull `tools-ibmcloud` image, can run a no-op
   - `roksbnkctl doctor --backend k8s` confirms ops pod deployed, Secret readable by SA, kubectl exec works
   - `roksbnkctl doctor --backend ssh:jumphost` confirms target reachable, key auth works, SetEnv works, sudo NOPASSWD configured for apt-get if bootstrap needed
7. **`roksbnkctl ops install/show/uninstall`** — manages the cluster-side ops pod + Secret + RBAC; idempotent
8. **Trusted-profile path** for k8s backend (Phase 3.1): when the user's IBM Cloud account has trusted-profile capability and roksbnkctl can provision one, use it instead of static API key in Secret

## Acceptance criteria

- No credential strings appear in `docker inspect`, `ps -ef`, `kubectl get pods/events -o yaml`, `kubectl describe pod`, or any log file readable by another local user
- A user with no `IBMCLOUD_API_KEY` env set but a configured workspace API key in the OS keychain can still run docker / k8s / ssh backends successfully (roksbnkctl resolves the key and propagates per-backend)
- IAM trusted profile path works end-to-end on k8s backend: pod assumes profile → ibmcloud calls succeed without static key in any Secret
- Wrapper-script SSH fallback removes the temp file even on SIGINT during command execution (the `trap rm EXIT` covers SIGINT, SIGTERM via shell signal handling)
- A regression test runs each backend with a known API key and asserts the key string never appears in any of: docker inspect output, kubectl get all -o yaml, ssh's process listing, the wrapper script after exit
- Doctor surfaces all per-backend cred-related issues clearly

## Open questions

- **Centralized cred resolver**: should there be a single `internal/cred/Resolver` that backends call, or each backend resolves independently? **Recommendation: single resolver** so the keychain → env → config-b64 → prompt chain is implemented once.
- **Trusted profile auto-provisioning**: should `roksbnkctl ops install` provision the trusted profile, or expect the user to configure it? **Recommendation: auto-provision with `--trusted-profile=auto` default**, fall back to static key if IAM permissions don't allow.
- **kubeconfig refresh during long-running pods**: the long-lived ops pod has its SA token rotated periodically (k8s v1.21+ projected token rotation); does the ibmcloud CLI inside the pod handle this? Test in Phase 5.
- **Cred TTL alignment**: `roksbnkctl up` triggers a token rotation in the upstream HCL; should `roksbnkctl ops install`'s Secret rotate too? Or rely on trusted-profile model where this is moot?

## Related work

- [PRD 03 (execution backends)](./03-EXECUTION-BACKENDS.md) implements the backend interfaces this PRD constrains
- [PRD 05 (E2E)](./05-E2E-TEST-PLAN.md), Phase M, audits credential leak vectors across all backends
