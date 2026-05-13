# PRD 04 — credential propagation across execution backends (cross-cutting)

> Cross-cutting concern for [PRD 03 (execution backends)](./03-EXECUTION-BACKENDS.md). Read this before designing or reviewing the backend interfaces.
>
> Estimated effort: small in code (~300 LOC), medium in design care.

## Resolved in Sprint 9

Sprint 9 (`v1.2.0`) closed two §"Open questions" items that had been open since the v0.9 cycle and that surfaced as `t.Skip`-marked integration tests on commit `776fe56`. Landing notes preserved for future readers; the user-facing shape lives in [Chapter 14 §"What changed in v1.2"](https://github.com/jgruberf5/roksbnkctl/blob/main/book/src/14-credentials-resolver.md#whats-new-in-v12-the-cred-tmpfile-and-trusted-profile-paths) and [Chapter 19 §"Trusted-profile flow"](https://github.com/jgruberf5/roksbnkctl/blob/main/book/src/19-in-cluster-ops-pod.md#trusted-profile-flow-v12).

### Cred tmpfile-bind-mount pattern (docker backend)

**The gap.** The v1.0.x docker path materialised the IBM Cloud API key as `--env IBMCLOUD_API_KEY` (bare-name, value inherits from caller env) — secure on `docker inspect` but silently broken on the docker SDK path because the SDK requires `KEY=VALUE` form. The v1.0.x → v1.1.0 fix switched to the explicit `KEY=VALUE` form so the SDK path worked, but the value then appeared verbatim in `docker inspect` output, regressing PRD 04 §"Anti-patterns to avoid" item 1. The integration test `TestIntegration_DockerBackend_NoLeakInInspect` was `t.Skip`'d on `776fe56` against this exact gap.

**The closure.** The docker backend now writes `IBMCLOUD_API_KEY` to a per-run `0600` tempfile under `$TMPDIR/roksbnkctl-creds-<rand>/api-key`, bind-mounts the file (not its parent directory) read-only at `/run/secrets/ibmcloud_api_key` in the container, and sets a single env var that points at the bind-mounted path:

```
IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key
```

The container's command is wrapped in a small `sh -c` shim — `export IBMCLOUD_API_KEY="$(cat "$IBMCLOUD_API_KEY_FILE")" && exec …` — so the existing `dockerImageBinary["ibmcloud"]` login wrap (and any tool that reads `IBMCLOUD_API_KEY` from env) sees the value at process-spawn time without it ever appearing in the container's stored env-var metadata. `docker inspect` shows only `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key` plus the bind-mount entry pointing at the tempfile path; the key value is in the bind-mounted file (mode `0600`, owned by the calling user, on host filesystem only — not in image layers, not in container metadata, not in `docker logs`).

**Tempfile lifecycle.** The tempfile is created at backend `Run` invocation time (one per container execution; multiple concurrent docker runs each get their own random dir). Cleanup runs via `defer` on the backend's `Run` method tail, plus a backstop on `context.Context` cancellation (`AfterFunc` registered against `RunOpts.Context`) so an interrupted run still scrubs the file. The longer-running `roksbnkctl up --backend docker` (terraform-via-docker; up to ~30 minute apply window) creates the tempfile once per terraform invocation; the file outlives the container by definition since the container holds the bind mount open for the duration of the run.

**Replaces:** the v1.0.x bare-name `Env: ["IBMCLOUD_API_KEY"]` form (silently broken on docker SDK path) and the v1.1.0/v1.1.x `IBMCLOUD_API_KEY=<value>` form (works but leaks to `docker inspect`).

**Fallback.** None needed — the pattern works on any docker daemon (Linux native, Docker Desktop for macOS, Docker Desktop for Windows / WSL2) since bind mounts of single files are a baseline docker primitive. The only environments where it doesn't apply are non-docker docker-compatible runtimes that don't support file bind-mounts; those would already be excluded from the docker backend by the daemon-availability check.

### Trusted-profile auto-provisioning (k8s backend)

**The gap.** The v1.0.x k8s backend put the IBM Cloud API key in a Kubernetes Secret (`roksbnkctl-ibm-creds`) and mounted it into the ops pod via `envFrom: secretRef`. The static-key approach worked but had two well-known weaknesses: the key is at rest in etcd (cluster admins can read it), and rotating the key requires a full `roksbnkctl ops install` re-run + pod recreation. PRD 04 §"Recommended path: IAM trusted profile" called for the trusted-profile path but it was §"Open questions" first item — deferred to a later release.

**The closure.** `roksbnkctl ops install --trusted-profile=auto` (the new default) provisions an IBM Cloud IAM trusted profile named `roksbnkctl-ops-<workspace>` linked to the ops pod's ServiceAccount via its projected SA token. The ops pod assumes the trusted profile at runtime using the projected token as the OIDC-style proof; the IBM IAM endpoint issues short-lived IAM tokens against the profile's policies. The static API key never lands in any Secret — the workspace's resolved key is used only at `ops install` time to perform the one-shot IAM API calls that create the profile and bind the cluster's OIDC issuer as a trusted compute resource.

**The `--trusted-profile` flag.** New flag on `roksbnkctl ops install`. Three values, validated at flag-parse time:

| Value | Behaviour | When to use |
|---|---|---|
| `auto` (default) | Try to provision; on IAM `iam-identity` perm-missing, fall back to the v1.0.x static-key Secret with a stderr warning naming the missing perm and how to opt out (`--trusted-profile=off`). | Default for new installs. Production users get the secure path automatically; restricted-IAM users still complete `ops install` successfully. |
| `on` | Try to provision; fail loudly with a non-zero exit if perms don't allow. No fallback. | CI / hardened environments where the static-key path is unacceptable and the perm-missing case should block, not warn. |
| `off` | Skip the trusted-profile path entirely; provision the v1.0.x static-key Secret. | Compatibility / debugging — and the documented path for clusters whose IAM admin doesn't grant `iam-identity` perms and isn't expected to. |

**Workspace namespacing.** The profile name `roksbnkctl-ops-<workspace>` lets multiple workspaces against the same IBM Cloud account each provision their own profile without racing for a single shared name. A single user with `dev`, `staging`, `prod` workspaces ends up with three distinct trusted profiles, each scoped to its own cluster's ops pod SA. The cleanup path (`roksbnkctl ops uninstall`) deletes the profile if `ops install` provisioned it (the `roksbnkctl.io/trusted-profile-managed: "true"` annotation on the SA records this).

**Replaces:** the v1.0.x static-key Secret as the default; the static-key Secret remains as the explicit `--trusted-profile=off` fallback (and as the auto-fallback when IAM perms don't allow).

**Fallback.** Built into the `auto` semantics — if the resolved API key doesn't have IAM `iam-identity` perms (i.e., can't create trusted profiles), `auto` automatically degrades to the static-key path with a warning. The warning is one stderr line, doesn't fail the command, and tells the user how to silence it (`--trusted-profile=off`) or how to upgrade the key's perms.

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
- ~~**Trusted profile auto-provisioning**: should `roksbnkctl ops install` provision the trusted profile, or expect the user to configure it? **Recommendation: auto-provision with `--trusted-profile=auto` default**, fall back to static key if IAM permissions don't allow.~~ **Resolved in Sprint 9** — see [§"Resolved in Sprint 9" → "Trusted-profile auto-provisioning (k8s backend)"](#trusted-profile-auto-provisioning-k8s-backend).
- **kubeconfig refresh during long-running pods**: the long-lived ops pod has its SA token rotated periodically (k8s v1.21+ projected token rotation); does the ibmcloud CLI inside the pod handle this? Test in Phase 5.
- ~~**Cred TTL alignment**: `roksbnkctl up` triggers a token rotation in the upstream HCL; should `roksbnkctl ops install`'s Secret rotate too? Or rely on trusted-profile model where this is moot?~~ **Resolved in Sprint 9** — moot under the trusted-profile path; the ops pod's IAM tokens are short-lived (5-min default) and rotated transparently by IBM IAM as the projected SA token rotates. See [§"Resolved in Sprint 9" → "Trusted-profile auto-provisioning (k8s backend)"](#trusted-profile-auto-provisioning-k8s-backend). The `--trusted-profile=off` static-key fallback retains the v1.0.x behaviour (re-run `ops install` to rotate the Secret).

## Related work

- [PRD 03 (execution backends)](./03-EXECUTION-BACKENDS.md) implements the backend interfaces this PRD constrains
- [PRD 05 (E2E)](./05-E2E-TEST-PLAN.md), Phase M, audits credential leak vectors across all backends
