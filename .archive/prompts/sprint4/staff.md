You are the staff engineer agent for Sprint 4 of the roksbnkctl project. Your scope is **PRD 03 second half** — k8s + SSH backends, the in-cluster ops pod, iperf3 SCC fix, iperf3 + ibmcloud backend-selection wiring, doctor extensions — plus four polish carry-overs from Sprint 3.

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25.

## Read first

- `docs/prd/03-EXECUTION-BACKENDS.md` §"K8s", §"SSH", §"Tool migration plan" (iperf3 + ibmcloud) — your authoritative design spec.
- `docs/prd/04-CREDENTIALS.md` §"K8s" + §"SSH" — cred-propagation contracts every backend must honor (Secret projection for k8s; SetEnv-then-wrapper fallback for ssh).
- `docs/PLAN.md` Sprint 4 section — confirms the two-week structure (week 1: k8s; week 2: ssh + tool migration + doctor) and the gate criteria.
- Existing files:
  - `internal/exec/backend.go` — the Backend interface + RunOpts + ResolveBackend registry. Sprint 3 landed this; your k8s + ssh implementations register here.
  - `internal/exec/local.go` + `docker.go` — reference implementations to mirror for k8s + ssh shape.
  - `internal/exec/creds.go` — `Credentials` struct + `EnvVars()` + `DockerArgs()`. You'll add k8s + ssh serializers here (or in adjacent files).
  - `internal/exec/redact.go` — wrap stdout/stderr in your new backends too.
  - `internal/cred/resolver.go` — Sprint 3's cred resolver. The legacy `internal/config.ResolveAPIKey` shim is one of your polish carry-overs.
  - `internal/remote/ssh.go` — Sprint 1's SSH client. Your `internal/exec/ssh.go` wraps this.
  - `internal/test/throughput.go` — has the iperf3 pod manifest your SCC fix targets. Read its current `securityContext` block first.
  - `internal/cli/cluster.go` + `internal/cli/test.go` — passthrough callsites. You wire backend selection here.
  - `internal/cli/doctor.go` — doctor base; you extend it with `--backend k8s/ssh` checks.
  - `internal/k8s/client.go` — Sprint 2's `client-go` wrapper. Reuse for k8s backend.
- `prompts/sprint3/staff.md` for prompt-structure reference.
- `issues/resolved_sprint3_staff.md` + `resolved_sprint3_tech-writer.md` for the explicit Sprint 4 carry-overs (4 polish items called out below).

## Coordinate with parallel agents

An architect agent is replacing/extending 3 book chapters under `book/src/` (17 full, 18 new, 19 new). A validator agent is adding argv-builder unit tests for k8s + ssh, kind-based integration tests in CI, a new `scripts/e2e-test-backends.sh` covering PRD 05 Phases K + L, k8s + ssh cred-leak audit tests, the cspell `SSC→SCC` fix, and CONTRIBUTING.md updates. **Do not touch their files.** You own production code only.

## Tasks (priority order — finish from the top down)

If you run out of token budget, stop at a priority boundary and file an issue describing what's deferred.

### Priority 1 — K8s backend, embedded RBAC, ops command

#### 1a. `internal/exec/k8s_install.yaml` (new)

Embedded YAML manifests for the in-cluster ops fixtures. Use Go's `embed` directive in a sibling `.go` file to ship the YAML as part of the binary.

Manifests to include:
- `Namespace: roksbnkctl-ops`
- `Namespace: roksbnkctl-test` (created by Job-mode invocations; included here so `ops install` provisions both upfront)
- `ServiceAccount: roksbnkctl-ops` in `roksbnkctl-ops`
- `Secret: roksbnkctl-ibm-creds` in `roksbnkctl-ops` — populated at apply-time with `IBMCLOUD_API_KEY` data (the embedded YAML uses a `${IBMCLOUD_API_KEY}` placeholder; Go-side templating substitutes it from the workspace's resolved cred)
- `ClusterRole: roksbnkctl-ops` — least-privilege: `jobs/get,list,create,delete` in `roksbnkctl-test`; `pods/get,list,exec,log` in `roksbnkctl-ops` and `roksbnkctl-test`; `secrets/get` in `roksbnkctl-ops`. **No** cluster-admin, **no** wildcard verbs, **no** unrelated namespace access.
- `ClusterRoleBinding: roksbnkctl-ops` — binds the ClusterRole to the ServiceAccount
- `Pod: roksbnkctl-ops` in `roksbnkctl-ops` — long-lived; image is `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<version>` (or pin via the polish carry-over below); env-from-secretRef `roksbnkctl-ibm-creds`; `restartPolicy: Always`; `securityContext` correct for `restricted-v2` SCC (matches the iperf3 fix below — `runAsNonRoot: true`, `allowPrivilegeEscalation: false`, `seccompProfile.type: RuntimeDefault`, `capabilities.drop: ["ALL"]`)

#### 1b. `internal/exec/k8s.go` (new)

Two execution patterns:

```go
type K8sBackend struct {
    client   kubernetes.Interface  // from internal/k8s
    config   *rest.Config          // for SPDY exec
}

func (b *K8sBackend) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
    if opts.LongLivedExec {
        return b.runOnOpsPod(ctx, argv, opts)  // kubectl exec via SPDY
    }
    return b.runAsJob(ctx, argv, opts)  // ephemeral Job
}
```

Long-lived ops-pod path (`runOnOpsPod`):
- Resolve `roksbnkctl-ops/roksbnkctl-ops` pod, status=Ready
- `client-go`'s `remotecommand.NewSPDYExecutor` → exec into the pod with stdin/stdout/stderr wired through `RunOpts` (wrap stdout/stderr with the redactor)
- Exit code from the exec stream's terminated reason

One-shot Job path (`runAsJob`):
- Build a `Job` spec in `roksbnkctl-test` namespace with the wrapped image (configurable per tool — the iperf3 image for the iperf3 client; the ibmcloud image for ibmcloud; etc.)
- `RunOpts.Files` materialised via projected `Secret` (one Secret per Job; auto-deleted with the Job via owner ref)
- `RunOpts.Credentials` materialised via env-from-secretRef on the existing `roksbnkctl-ibm-creds` Secret (don't re-project the cred per Job — read once at install time)
- `kubectl logs -f` equivalent via `Pods().GetLogs(...).Stream()` after the Job's pod is Running
- Wait on Job completion; surface exit code from container status `terminated.exitCode`
- `ttlSecondsAfterFinished: 60` for auto-cleanup; ctx cancellation triggers explicit Job + pod delete

`Backend.Name() == "k8s"`. Register in `backend.go`'s registry.

#### 1c. `internal/cli/ops.go` (new)

Cobra subcommands `roksbnkctl ops install/show/uninstall`:

- `install` — applies `k8s_install.yaml` (substituting the API key into the Secret), then waits for the Pod to be Ready (60s timeout, clear error on failure). Idempotent — re-applying with a new API key updates the Secret + rolls the Pod.
- `show` — prints pod status, image version (parse from container[0].image), Secret rotation timestamp (annotation we set on Secret apply), RBAC subject (`system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops`).
- `uninstall` — deletes the Pod, Secret, ServiceAccount, ClusterRole, ClusterRoleBinding, then both namespaces. Confirm + abort if the user passes `--confirm` not set.

Wire into the root command alongside `cluster`, `targets`, `up`, etc.

#### 1d. iperf3 SCC fix in `internal/test/throughput.go`

The existing iperf3 server pod manifest fails `restricted-v2` SCC on OpenShift. Fix the `securityContext`:

```go
securityContext: &corev1.PodSecurityContext{
    RunAsNonRoot: ptr.To(true),
    SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
}
```

Container-level:

```go
SecurityContext: &corev1.SecurityContext{
    AllowPrivilegeEscalation: ptr.To(false),
    Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
    RunAsNonRoot: ptr.To(true),
}
```

Image must respect non-root — the Sprint 0 iperf3 Dockerfile may need a `USER 1000` (verify; if missing, the staff agent doesn't own `tools/docker/iperf3/Dockerfile` here — file an issue for the integrator if the Dockerfile change is needed).

### Priority 2 — SSH backend (`internal/exec/ssh.go`)

Wraps `internal/remote/ssh.Client`. Adds the per-PRD-03 surface:

- **Pre-flight tool check + bootstrap** with `--bootstrap` opt-in flag (PRD 03 open-question recommendation: opt-in by default). The `--bootstrap` flag is plumbed in `internal/cli/root.go` as a persistent flag alongside `--backend`. Without `--bootstrap`, a missing tool returns a clear "missing tool, run with --bootstrap to install" error (exit 127).
- **Apt bootstrap (Ubuntu only)** — `apt-get update` + `apt-get install -y <pkg>` over the SSH session. The `ibmcloud-cli` case adds IBM's apt repo first (gpg key + sources.list.d entry). Failure modes:
  - `sudo` requires password → exit 126, message: "the SSH user needs passwordless sudo for `apt-get install <pkg>`. Configure `<user> ALL=(ALL) NOPASSWD: /usr/bin/apt-get` in /etc/sudoers, or pre-install <pkg> manually."
  - Non-Ubuntu OS (detect via `lsb_release -is`) → exit 126, message: "auto-install only supports Ubuntu. Pre-install <pkg> on the target."
  - Network unreachable from target → exit 127, message: "target can't reach the package repo."
  - Per PRD 03 §"Backend startup failures" recommendation: hard-error, no fallback to local.
- **File materialization** — write each `RunOpts.Files` entry to `/tmp/roksbnkctl.<random>/<basename>` on the remote via the SSH session, then exec with `WorkDir=/tmp/roksbnkctl.<random>`. Wrapper-script sets `trap 'rm -rf /tmp/roksbnkctl.<random>' EXIT`.
- **Env propagation** (PRD 04 §"SSH" — both paths required this sprint per Sprint 1 validator Issue 4 carry-over):
  1. **Preferred**: `SetEnv` directives over the SSH connection (requires sshd `AcceptEnv` configuration on the remote)
  2. **Fallback**: detect SetEnv silent-drop (compare expected env on remote via `env | grep`) and fall back to wrapper-script-with-trap (writes `KEY=VALUE` to `/tmp/roksbnkctl.<random>/.env`, sources it pre-exec, traps cleanup on script exit)

  The fallback path **must** redact the API key value from the remote `set -x` trace if any; keep the wrapper script `set +x` and source the env file silently.
- **TTY** — `--tty` flag on the SSH session; pass through `RunOpts.TTY`.
- **Cleanup** — `defer` removes the materialised tempdir even on ctx cancel; trap-on-EXIT wrapper handles abnormal termination on the remote.

`Backend.Name() == "ssh"`. The registry's `ResolveBackend("ssh:<target>")` parser already exists from Sprint 3; verify it returns an SSHBackend pinned to the named target.

### Priority 3 — Tool backend selection wiring

#### 3a. iperf3 (`internal/cli/test.go test throughput`)

Per-tool default backend map. Sprint 3 landed `resolveBackendSpecWith` defaulting all tools to `local`; this sprint adds the per-tool defaults table (Sprint 3 tech-writer Issue 2 carry-over):

```go
var perToolDefaults = map[string]string{
    "iperf3":    "k8s",
    "ibmcloud":  "local",
    "terraform": "local",
}
```

Wire `iperf3 → k8s` as the resolved default for `roksbnkctl test throughput`. Supported backends: `k8s`, `local`, `ssh`. `--backend docker` errors clearly ("iperf3 doesn't benefit from docker; use local instead").

#### 3b. ibmcloud (`internal/cli/cluster.go ibmcloud passthrough`)

`ibmcloud` supports all four backends (local, docker, k8s, ssh). Default `local`. Wire all four through the existing `Backend.Run` dispatch. The k8s path uses the long-lived ops pod (`opts.LongLivedExec = true`).

### Priority 4 — Doctor: per-backend availability checks

Extend `internal/cli/doctor.go` with `--backend k8s/ssh` checks. Each backend's check:

- `k8s` — verifies the cluster is reachable, the ops pod exists + is Ready, the ServiceAccount + ClusterRole + Secret are present, the `roksbnkctl-ibm-creds` Secret has a populated `IBMCLOUD_API_KEY` key. RBAC negative check: `kubectl auth can-i delete pods --as=...:roksbnkctl-ops -n default` returns `no` (least-privilege confirmed).
- `ssh:<target>` — verifies the named target resolves, the SSH connection establishes, the user has `sudo -n true` or the bootstrap step is feasible, and (if the tool name is known) the tool is on PATH or the bootstrap can install it.

The default `roksbnkctl doctor` (no `--backend`) keeps Sprint 0+ behaviour unchanged.

### Priority 5 — Sprint 3 polish carry-overs

#### 5a. Legacy `internal/config.ResolveAPIKey` migration

Mechanical refactor: replace remaining callers of `internal/config.ResolveAPIKey` with `cred.Resolver.IBMCloudAPIKey(ctx)`. Per `resolved_sprint3_staff.md`, the shim was retained to keep Sprint 3's diff tractable. Now that the new resolver is settled, finish the migration. Delete the shim once the last caller is converted.

#### 5b. `:dev` tag publish fix in `internal/exec/docker.go::toolImages`

Per `resolved_sprint3_tech-writer.md` Issue 8: a clean `go install ./cmd/roksbnkctl` on a fresh host fails to pull because `toolImages` hard-codes `:dev` but CI doesn't publish `:dev`. Pick one:

- (preferred) Pin `toolImages` to the binary's `internal/version.Version` value (e.g. `:v0.9.0` for a `v0.9.0` build), so a tag-released binary pulls the matching tag-released image
- (fallback) File an issue for the validator to also publish `:dev` on `main` pushes in `tools-images.yml`; pin `toolImages` to `:dev` only on dev builds (detect via `version.Version == "dev"`)

Implement option 1 (binary-version-pinning) by default; if there's a reason it doesn't work, document why and fall back to option 2 with a paired validator issue.

#### 5c. 126/127 backend-failure semantics split

Per `resolved_sprint3_tech-writer.md` Issue 6: split the current "127 for any backend-side failure" into the PRD-03-spec'd two-code split:

- `127` — backend failed to start (Docker daemon down, image pull failed, ssh target unreachable, k8s API unreachable, etc.)
- `126` — backend started but the wrapped invocation failed for backend-specific reasons (container created but exec inside failed, ssh session established but the wrapped command's process couldn't spawn, k8s pod started but the exec stream errored, etc.)

Apply the split to `local`, `docker` (Sprint 3 implementations) and the new `k8s`, `ssh` (this sprint). Document the mapping in code comments referencing PRD 03.

#### 5d. Per-tool default backend map

Already covered in Priority 3a — the per-tool defaults table (`iperf3 → k8s`) is the Sprint 3 tech-writer Issue 2 polish carry-over.

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean (validator's tests pass; your code shouldn't break them)
- `go vet ./...` clean
- `gofmt -d -l .` clean
- `roksbnkctl ops install` against a kind cluster works (or, if no cluster locally, document in issue file what the integrator should validate)
- `roksbnkctl ibmcloud --backend k8s iam oauth-tokens` works (against a kind cluster with ops installed)
- `roksbnkctl test throughput --backend k8s` works (iperf3 server + client both in cluster, JSON output)
- `roksbnkctl ibmcloud --backend ssh:jumphost ks cluster ls` works on a fresh Ubuntu jumphost with `--bootstrap` (auto-installs ibmcloud-cli)
- `--backend bogus` produces a clear error (regression check)
- Sprint 3's `--backend docker` regression check passes (refactors didn't break docker path)
- Sprint 1's `--on jumphost` still works (regression check; SSH backend and `--on` are independent code paths until the consolidation in PLAN.md's Sprint 5+)
- `roksbnkctl doctor --backend k8s` and `--backend ssh:<target>` produce accurate output

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint4_staff.md`. Same format as Sprint 3.

## Final report (under 200 words)

- Files created
- Files edited
- Build / test / vet / gofmt status
- Which priority items completed; which deferred
- Issues filed
- Anything the integrator should know (especially regarding the SSH wrapper-script env fallback's edge cases, the `:dev` → version-pinned tag migration, and any iperf3 Dockerfile changes you needed but couldn't make)

Do NOT commit. The integrator commits the aggregated work.
