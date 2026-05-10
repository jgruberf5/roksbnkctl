# Execution backends: local, docker, k8s, ssh

`roksbnkctl` runs a handful of external tools as part of its job — `ibmcloud`, `terraform`, `iperf3`, eventually `dig`-equivalents and others. By default each tool runs as a child process on your laptop. That's fine for some tools and wrong for others: `iperf3` from your laptop measures your laptop's internet uplink, not the cluster's bandwidth.

The execution-backend system lets you pick **where** each tool runs without changing the surface command. The same `roksbnkctl ibmcloud ks cluster ls` invocation can run as a local process, inside a vendored container, inside the cluster, or on a remote SSH host — selected by a flag or a per-tool default in your workspace config.

This chapter is the user-facing reference for all four backends. After the introduction, each backend gets its own deep-dive section covering the mechanics, the credential-propagation rules, the failure modes, and a short "when to use it" callout. [Chapter 18](./18-choosing-backend.md) is the decision-tree companion that picks one for a given (tool, scenario) pair.

## The four backends at a glance

| Backend | What it does |
|---|---|
| **local** | `os/exec` — spawns the tool as a child process, inheriting your env and PATH |
| **docker** | `docker run` against a vendored image (`ghcr.io/jgruberf5/roksbnkctl-tools-<tool>:<v>`); frozen toolchain version |
| **k8s** | Runs inside the cluster, either in a long-lived ops pod or as a one-shot Job; auth via the pod's ServiceAccount token |
| **ssh** | Runs on a registered SSH target via the built-in SSH client; opt-in apt-bootstrap of missing tools on Ubuntu |

Each backend solves a different problem:

- **local**: fastest startup, simplest mental model, requires the host tool to exist on `PATH`.
- **docker**: reproducible across dev machines, no host install needed, frozen at a known-good tool version.
- **k8s**: network-correct (private IPs reachable, cluster-internal services accessible), zero host install, in-cluster identity via ServiceAccount.
- **ssh**: pre-cluster ops from a known-IP bastion, customer-firewall workflows, air-gapped environments where the laptop can't reach IBM Cloud APIs but the jumphost can.

All four implementations conform to the same Go interface (`internal/exec.Backend`) so callers don't branch on backend type — they just call `backend.Run(ctx, argv, opts)` and let the implementation handle the mechanics. That uniformity is what lets the same `roksbnkctl ibmcloud ks cluster ls` work across all four with no surface-level change.

## The `--backend` CLI flag

Override the per-tool default for a single invocation:

```bash
# Local (the implicit default for ibmcloud + terraform)
roksbnkctl ibmcloud ks cluster ls

# Same command, in a vendored docker image
roksbnkctl ibmcloud --backend docker ks cluster ls

# Same command, in the cluster (requires `roksbnkctl ops install` first)
roksbnkctl ibmcloud --backend k8s ks cluster ls

# Same command, on a remote SSH host
roksbnkctl ibmcloud --backend ssh:jumphost ks cluster ls
```

Format:

```
--backend <name>            # local | docker | k8s
--backend ssh:<target-name> # SSH backend; target name from `roksbnkctl targets list`
```

The flag is **persistent at the root** — it works for any command that runs an external tool. Commands that don't run external tools (like `roksbnkctl ws list`) ignore it.

The flag wins over the workspace-config default. If `config.yaml` says `iperf3: { backend: k8s }` and you pass `--backend local`, the local backend runs.

### Backend-failure semantics

Each backend has a different failure surface. The convention is:

- **Backend startup failure** (Docker daemon unreachable, k8s API unreachable, SSH connect refused, binary not on PATH for `local`) ⇒ exit code `127`, with a message naming the cause. **No silent fallback to `local`.** Silent fallback hides intent and produces confusing test results.
- **Backend mid-run failure** (the container started but couldn't pull a sub-resource; the pod was OOMKilled before the wrapped tool ran; the SSH session died after `apt-get install` but before the tool exec) ⇒ exit code `126`, distinct from `127` so CI can tell "we never got going" from "we got going then broke".
- **Tool exit code** (the actual `ibmcloud` / `terraform` / `iperf3` exit code, anything in `0-125` or `128-255`) ⇒ propagated 1:1, including non-zero codes.
- **Context cancellation / timeout** ⇒ exit code `137` (the conventional SIGKILL-on-signal code).

This way, your CI script can tell "the tool said X failed" (typical exit codes) from "we never reached the tool" (`127`) from "we reached the tool, then the backend died mid-flight" (`126`) from "we ran out of time" (`137`).

## Per-tool defaults from `exec:`

Workspace config carries the per-tool default backend in the `exec:` block:

```yaml
# ~/.roksbnkctl/<workspace>/config.yaml
exec:
  ibmcloud:  { backend: local }
  iperf3:    { backend: k8s }
  terraform: { backend: local }
```

The defaults shipped today:

| Tool | Default backend | Why |
|---|---|---|
| `terraform` | `local` | The terraform-exec local path is the established workflow. State handling is simplest here. |
| `ibmcloud` | `local` | Most users have it on PATH or are happy installing it. Compliance/firewall scenarios opt in via `--backend ssh:jumphost` or `docker`. |
| `iperf3` | `k8s` | Throughput from a laptop's uplink isn't the cluster's bandwidth. The k8s default runs the iperf3 client adjacent to (or inside) the cluster so the number reflects cluster fabric, not your office Wi-Fi. |

[Chapter 12 — Workspace config](./12-workspace-config.md) covers the `exec:` block schema in detail; this chapter just notes its place in the backend system.

[Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md) is the decision tree for "which backend should I pick for this tool in this scenario".

## Per-backend deep dives

### `local` backend

The default for `ibmcloud` and `terraform`. `os/exec.CommandContext(ctx, argv[0], argv[1:]...)`, inheriting the parent process's environment, PATH, and working directory. Mechanically the simplest of the four — no container, no cluster, no network handshake.

#### `os/exec` shape

`internal/exec/local.go` resolves `argv[0]` via `exec.LookPath`, then builds a `*exec.Cmd`:

```go
bin, err := exec.LookPath(argv[0])
// fall through to argv[0] verbatim if it's an absolute path that LookPath rejects
cmd := exec.CommandContext(ctx, bin, argv[1:]...)
cmd.Env    = effectiveEnv     // os.Environ() + opts.Env + Credentials.EnvVars()
cmd.Dir    = opts.WorkDir     // empty → inherit caller's CWD
cmd.Stdin  = opts.Stdin
cmd.Stdout = redactor(opts.Stdout, creds)
cmd.Stderr = redactor(opts.Stderr, creds)
```

The redactor wrap is defense-in-depth — see [Chapter 14 §"The redactor"](./14-credentials-resolver.md#the-redactor). If a wrapped tool ever prints `IBMCLOUD_API_KEY` value to stdout (a debug trace, an error message), the redactor replaces it with `[REDACTED]` before the bytes leave the binary.

#### Env propagation

Three sources, in order:

1. **The host process's environment** (`os.Environ()`) — your shell's `PATH`, `HOME`, `KUBECONFIG`, etc.
2. **`RunOpts.Env`** — caller-supplied `KEY=VALUE` strings (e.g., `IBMCLOUD_REGION=ca-tor` from the workspace config).
3. **`Credentials.EnvVars()`** — `IBMCLOUD_API_KEY=…` plus the legacy `IC_API_KEY=…` alias older `ibmcloud` versions accept.

`os/exec` documents that for duplicate keys the **last** entry wins. So caller-supplied vars override host env, and credential vars override caller-supplied — meaning a workspace's API key always wins over a stale `IBMCLOUD_API_KEY` in your shell.

The local backend does **not** scrub the host env. If you have an unrelated `AWS_ACCESS_KEY_ID` in your shell, the wrapped tool sees it. That's by design — local is the "trust the user's shell" path; if you want a hermetic env, switch to `docker`.

#### Working directory

`RunOpts.WorkDir` becomes `cmd.Dir`. Empty → inherit the caller's CWD (Cobra's `RootCmd.Run` runs from wherever the user invoked `roksbnkctl`).

When `RunOpts.Files` is non-empty and `WorkDir` is empty, the local backend creates a tempdir under `os.TempDir()`, writes each `Files` entry as a `0600` file inside, and uses the tempdir as `WorkDir`. The tempdir is removed via `defer` after `Run` returns. This is mostly there for symmetry with the docker / k8s / ssh backends; today's `ibmcloud` passthrough never uses it.

#### Signal handling

`exec.CommandContext` wires ctx cancellation to the child: when the ctx ticks past its deadline (or the user hits Ctrl-C and the root cobra command cancels), Go sends `SIGKILL` (the default `Cmd.Cancel`) to the child. The child has no opportunity to clean up; this is intentional — we'd rather kill a stuck `terraform` than wait on an indefinite hang.

The kill is process-only, not process-group. If `terraform` has spawned grandchildren (the IBM provider's helpers, an SSH key generator, etc.) those grandchildren may outlive the ctx-cancel by a few seconds. We haven't seen this matter in practice; if it does, a `pgid` kill is a small follow-up.

#### Exit-code mapping

| Outcome | Exit code | Source |
|---|---|---|
| Child exits 0 | `0` | child |
| Child exits non-zero (e.g., `terraform plan` saw drift) | child's exit code, `1-125` or `128-255` | child |
| `argv[0]` not on PATH and not an absolute path | `127` | local backend (POSIX shell convention) |
| Child binary couldn't be exec'd despite being present (e.g., not executable) | `126` | local backend (mid-run failure: we found the binary but couldn't spawn it) |
| Ctx cancelled mid-run, child SIGKILL'd | `137` | `128 + SIGKILL` |

Note the **126 vs 127 split**: 127 means "we never reached the tool" (binary missing, daemon unreachable, SSH refused); 126 means "we reached the tool but the backend itself broke after that point" (couldn't fork, container created but crashed, pod scheduled but evicted before exec). Sprint 3 collapsed both to 127 in the local + docker implementations; this sprint splits them per [PRD 03 §"Backend interface"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#backend-interface). CI scripts that distinguish "test infra broken" from "real test failure" can now key on the difference.

#### When to use it

- You have the tool installed and on PATH already.
- You want the fastest startup — no container daemon, no SSH handshake, no cluster API call.
- You're running `terraform` against the workspace's local state (the established workflow).
- You're debugging and want the simplest mental model for "where did that output come from".

[Chapter 18 §"Decision tree"](./18-choosing-backend.md#decision-tree) expands these into a per-(tool, scenario) walkthrough.

### `docker` backend

Runs the tool inside a vendored container image, talking to the local docker daemon over its socket. `docker` on PATH is **not** required — `roksbnkctl` uses the official Docker Go SDK (`github.com/moby/moby/client`) and dials the socket directly.

```bash
roksbnkctl ibmcloud --backend docker ks cluster ls
```

#### Container shape

Mechanically (the `ibmcloud` passthrough; iperf3 client is similar with a different image and ports):

```
docker run --rm \
  -v <tempdir>/kubeconfig:/root/.kube/config:ro \  # if Credentials.KubeconfigBytes set
  -e IBMCLOUD_API_KEY \                            # bare name; value inherits
  -e IC_API_KEY \                                  # legacy alias
  ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v> \
  ks cluster ls
```

`internal/exec/docker.go` doesn't shell out to `docker run`; it builds a `container.Config` + `container.HostConfig` and calls `cli.ContainerCreate` → `ContainerStart` → `ContainerLogs(stream=true)`. The bash-style above is the conceptual equivalent.

There's no workspace-wide bind-mount. Per-invocation mounts come from three sources only:

1. **`Credentials.KubeconfigBytes`** — written to `<tempdir>/kubeconfig` (mode `0600`) on the host, bind-mounted **as a single file** at `/root/.kube/config` read-only. Single-file mount per [PRD 04 §"Anti-patterns"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md#docker-container) — bind-mounting `~/.kube/` exposes other clusters' configs.
2. **`RunOpts.Files`** — each name → bytes entry written to `<tempdir>/<basename>` and bind-mounted at `/work/<basename>`. The container's `WorkingDir` is set to `/work` so callers can reference files by relative path. (`ibmcloud` passthrough doesn't use this; it lands when the iperf3 client backend wants to ship `iperf3.json` to the pod, or when a future tool wants a config file.)
3. **`RunOpts.WorkDir`** — overrides `WorkingDir` if explicitly set.

The tempdir is removed via `defer` after `Run` returns, regardless of exit code or panic.

#### Credential propagation specifics

Three things matter, all enforced by `internal/exec/creds.go::Credentials.DockerArgs(...)`:

1. **`--env IBMCLOUD_API_KEY` (bare name, no `=value`).** The docker daemon looks up the value from the *daemon's* environment at container-create time, not from argv. So the literal API key string never appears in `docker inspect`, `docker ps -a --format`, or the daemon's container metadata. [PRD 04 §"Anti-patterns"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md#docker-container) calls out the `--env IBMCLOUD_API_KEY=$KEY` form as a leak vector — we don't use it. See [Chapter 14 — `Credentials.DockerArgs()`](./14-credentials-resolver.md#backend-specific-cred-propagation-forward-look) for the full call shape.
2. **Single-file kubeconfig mount, read-only.** Not the parent dir. The container can read exactly the kubeconfig you handed it — nothing else under `~/.kube/`.
3. **Stdout/stderr through the redactor.** Same defense-in-depth as the local backend: if the wrapped tool prints the API key value (rare but possible), the redactor masks it before the bytes leave `roksbnkctl`'s process.

#### `:dev` tag resolution

The vendored images live at:

| Tool | Image |
|---|---|
| `ibmcloud` | `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>` (vendored from `icr.io/ibm-cloud/ibmcloud-cli` upstream) |
| `iperf3` | `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<tag>` (Alpine + iperf3) |
| `terraform` | `hashicorp/terraform:<v>` (official upstream) |

The `<tag>` in the per-tool images is **`:dev`** during the development cycle and **`:v<roksbnkctl-version>`** on tagged releases. Sprint 3 shipped a hard-coded `:dev` for `ibmcloud` + `iperf3`; that lookup map landed unchanged this sprint because the `:dev` tag is what `tools/docker/Makefile` produces locally and what the `.github/workflows/build-tools-images.yml` workflow publishes on every push to `main`. On a `git tag v1.0.0` release the same workflow re-tags the image as `:v1.0.0` and pushes both. The default in `internal/exec/docker.go::toolImages` flips to the version-tagged form at release-tag time.

If you're cutting a custom tools image and want `roksbnkctl` to pick it up, the simplest path is `docker tag your-image ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev` locally — the docker backend pulls the local-cached version first.

#### Auto-remove and ctx-cancel-kill

Two cleanup mechanisms work together:

- **`AutoRemove: true`** in `HostConfig`. The docker daemon removes the container as soon as it exits, regardless of exit code. No `docker ps -a` clutter, no manual `docker rm` ever required.
- **Ctx-cancel triggers `ContainerKill`.** When `ctx.Done()` fires, the docker backend issues `cli.ContainerKill(ctx, id, "SIGKILL")` and waits a few seconds for the daemon to confirm. The `--rm` then takes care of removal. Net effect: hitting Ctrl-C during a stuck `ibmcloud login` doesn't leave a zombie container behind.

Combined with the daemon's own watchdog on the container, the worst case is a few seconds of "container is dying" between Ctrl-C and the container disappearing. We haven't seen leaked containers in dev or CI.

#### Image build pipeline

Image versions are tagged in lock-step with `roksbnkctl` releases; the GitHub Actions workflow that builds + pushes them runs on every release tag. See [Chapter 31 — Building from source](./31-building-from-source.md) for the build pipeline details.

#### When to use it

- You're on a clean dev machine without `ibmcloud` installed and don't want to install it.
- You need a frozen tool version for CI reproducibility.
- You're debugging a "works on my machine" issue and want to factor out the host install variable.

When `docker` is the **wrong** call:

- The tool needs network access that your laptop has but the container doesn't (rare; default bridge networking usually preserves laptop's egress).
- You're running `iperf3` and want a network-locality benefit — `docker` doesn't give you that vs `local`. Use `k8s` instead.
- You're running a DNS probe and want a different network vantage — same network identity as the host, no value-add. The DNS subcommand rejects `--backend docker` by design.
- You're on Windows. Linux/macOS docker daemons are in scope; Windows Docker Desktop coverage is deferred to a future round.

### `k8s` backend

Runs the wrapped tool inside the cluster. Two distinct execution patterns share the same `Backend.Run` interface:

| Pattern | Used for | Lives in | Lifetime |
|---|---|---|---|
| **Long-lived ops pod** | ad-hoc `ibmcloud` commands, future interactive shells | `roksbnkctl-ops` namespace | manually managed via `roksbnkctl ops install/uninstall` |
| **One-shot Job** | iperf3 client runs, future `terraform` runs, future DNS probes | `roksbnkctl-test` namespace | per-invocation; auto-deleted after `ttlSecondsAfterFinished: 60` |

The split mirrors the two latency budgets. Long-lived pods amortise the pod-startup cost across many invocations — perfect for `ibmcloud iam oauth-tokens` which you might run twenty times in a debugging session. One-shot Jobs are clean (no leftover state, no concurrency questions) — perfect for `iperf3 -c <server>` which runs once, emits its JSON, and exits.

#### Long-lived ops pod pattern

The pod is named `ops` in the `roksbnkctl-ops` namespace. `roksbnkctl ops install` deploys it (see [Chapter 19](./19-in-cluster-ops-pod.md) for the full lifecycle). The image bundles `ibmcloud` CLI plus `kubectl` as backup; future iterations may add `oc`, `terraform`, etc.

`Backend.Run(ctx, argv, opts)` for the ops-pod path is essentially:

```go
exec, _ := remotecommand.NewSPDYExecutor(restConfig, "POST",
    clientset.CoreV1().RESTClient().Post().
        Resource("pods").Namespace("roksbnkctl-ops").Name("ops").
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Container: "ops",
            Command:   argv,
            Stdin:     opts.Stdin != nil,
            Stdout:    true,
            Stderr:    true,
            TTY:       opts.TTY,
        }, scheme.ParameterCodec).URL())
exec.StreamWithContext(ctx, remotecommand.StreamOptions{
    Stdin: opts.Stdin, Stdout: redactor(opts.Stdout, creds), Stderr: redactor(opts.Stderr, creds), Tty: opts.TTY,
})
```

The exit code comes back via the SPDY channel's `metav1.Status` — the executor surfaces it as a `exec.CodeExitError`. We propagate that as the backend's exit code, same as `local` propagates `exec.ExitError.ExitCode()`.

`opts.WorkDir` is **ignored** for the ops pod path. The pod's `WorkingDir` is fixed at container-spec time (`/work`); per-exec working-dir changes would require recreating the pod. Callers that need a specific cwd should `cd <dir> &&` it into argv (the `local` backend's symmetric escape hatch).

#### One-shot Job pattern

For each invocation, the backend builds a `batchv1.Job` spec, applies it, streams logs from the Job's pod, waits for completion, reads the exit code from the pod's container status, and lets `ttlSecondsAfterFinished` clean up.

Skeleton:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  generateName: roksbnkctl-iperf3-client-     # randomized; multiple runs don't collide
  namespace: roksbnkctl-test
spec:
  ttlSecondsAfterFinished: 60                  # auto-delete the Job + its Pod 60s after completion
  backoffLimit: 0                              # no retries; the test reports failure once and stops
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: iperf3-client
        image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>
        command: ["iperf3", "-c", "<server-svc>", "-J"]
        envFrom:
        - secretRef:
            name: roksbnkctl-job-creds-<random>   # projected per invocation
        volumeMounts:
        - name: files
          mountPath: /work
      volumes:
      - name: files
        projected:
          sources:
          - secret:
              name: roksbnkctl-job-files-<random>  # one Secret per invocation, holds RunOpts.Files
```

Three details to call out:

1. **Projected Secret for cred propagation.** `Credentials.IBMCloudAPIKey` (when set) becomes a one-shot Secret, mounted via `envFrom: secretRef`. Per [PRD 04 §"In-cluster pod"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md#in-cluster-pod-k8s-backend) this beats argv (which would show in `kubectl describe pod`) and beats inline `env:` blocks (which surface in `kubectl get pod -o yaml`). The Secret carries the same `ttlSecondsAfterFinished`-equivalent lifecycle: when the Job's `ttlSecondsAfterFinished` deletes the Job, the owning controller's GC sweeps the Secret too via `ownerReferences`.
2. **Log streaming via `client-go`.** Once the Job's pod is in `Running` state, `clientset.CoreV1().Pods(ns).GetLogs(name, &corev1.PodLogOptions{Follow: true}).Stream(ctx)` returns an `io.ReadCloser` that we copy through the redactor into `opts.Stdout`. The stream stays open until the pod terminates or ctx cancels.
3. **Exit-code extraction.** When the pod transitions to `Succeeded` or `Failed`, we read `pod.Status.ContainerStatuses[0].State.Terminated.ExitCode` and return that as the backend's exit code. A `Failed` pod with `ExitCode: 0` (rare; usually OOMKilled or evicted) maps to backend exit code `126` — backend mid-run failure rather than tool failure.

The `roksbnkctl-test` namespace is a fresh namespace dedicated to one-shot test workloads. It's separate from `roksbnkctl-ops` (the long-lived pod's home) so RBAC can be scoped tighter — see [Chapter 19 §"RBAC"](./19-in-cluster-ops-pod.md#rbac-the-clusterrole-rules).

#### iperf3 server side

Worth calling out because it's the asymmetric piece. The `iperf3` test deploys a **server** Deployment + LoadBalancer Service into `roksbnkctl-test`, then runs the **client** as the one-shot Job described above:

| Side | Resource | Lifetime |
|---|---|---|
| Server | `roksbnkctl-iperf3-server` Deployment + LoadBalancer Service | torn down after the client Job completes |
| Client | one-shot Job | `ttlSecondsAfterFinished: 60` |

The client Job's argv is `iperf3 -c <server-cluster-ip-or-lb> -J`. The `-J` JSON flows back via log streaming, parsed in `internal/test/throughput.go`, surfaced as `roksbnkctl test throughput` JSON output. See [Chapter 22 — Throughput testing](./22-throughput-testing.md) for the user-facing flag surface.

The server pod's `securityContext` is set to satisfy OpenShift's `restricted-v2` SCC: `runAsNonRoot: true`, `allowPrivilegeEscalation: false`, `seccompProfile.type: RuntimeDefault`, `capabilities.drop: [ALL]`. iperf3 listens on port 5201 (unprivileged) so no root is needed. The Sprint 3 cluster baseline tripped the SCC by missing one or more of these fields; the manifest the k8s backend emits this sprint sets all four.

#### When to use it

- You're running `iperf3` and want a number that reflects cluster fabric, not your office Wi-Fi.
- You're running `ibmcloud` from a network that can reach the cluster but not `*.cloud.ibm.com` directly. The ops pod has both lines of sight; your laptop has only one.
- You want a cluster-side ad-hoc shell for debugging — `roksbnkctl exec --backend k8s -- bash` (when implemented) drops into the ops pod.

When `k8s` is the **wrong** call:

- The cluster doesn't exist yet (`roksbnkctl ops install` requires a working kubeconfig). Use `local` or `ssh` for pre-cluster ops.
- You haven't run `roksbnkctl ops install`. Run it first; it's a one-time setup per cluster.
- You're running `terraform` — `--backend k8s` for terraform is deferred to a future release pending a state-handling design (see [PRD 03 §"State concerns"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#terraform)).

[Chapter 19](./19-in-cluster-ops-pod.md) is the full reference for the cluster-side mechanics: namespace, ServiceAccount, ClusterRole, Secret, lifecycle.

### `ssh` backend

Runs the wrapped tool on a registered SSH target. Builds on Sprint 1's `internal/remote.Client` (the same SSH client backing the `--on` flag); this section assumes you've read [Chapter 16](./16-on-flag-ssh-jumphosts.md) for the target-config and host-key TOFU framing.

```bash
roksbnkctl ibmcloud --backend ssh:jumphost ks cluster ls
roksbnkctl ibmcloud --backend ssh:bastion --bootstrap iam oauth-tokens
```

#### Per-tool apt-bootstrap and the `--bootstrap` flag

Before exec'ing the wrapped tool, the SSH backend probes whether it's installed:

```
ssh <target> 'command -v <tool>'
```

Exit `0` → tool present, proceed. Non-zero → tool missing. What happens next depends on `--bootstrap`:

- **Without `--bootstrap` (the default).** The backend errors with exit `127` and a clear message:

  ```
  error: tool `iperf3` not found on ssh target jumphost; re-run with --bootstrap to install via apt-get,
         or pre-install on the target manually
  ```

  No `sudo apt-get` ever runs. The backend won't surprise the user with package-manager invocations or sudo password prompts on a remote they didn't expect mutation on.

- **With `--bootstrap`.** The backend runs the per-tool bootstrap recipe. For Ubuntu (the only OS supported this round), the recipe is roughly:

  ```bash
  # ibmcloud needs IBM's apt repo + GPG key first
  curl -fsSL https://download.clis.cloud.ibm.com/Linux/Ubuntu/repo.gpg | sudo apt-key add -
  echo 'deb https://download.clis.cloud.ibm.com/Linux/Ubuntu jammy main' \
    | sudo tee /etc/apt/sources.list.d/ibmcloud.list
  sudo -n apt-get update -y
  sudo -n apt-get install -y ibmcloud-cli
  ```

  `iperf3` is simpler — no repo addition, just `sudo -n apt-get install -y iperf3`.

The opt-in default reflects [PRD 03 open question §"`--bootstrap` opt-in for SSH"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#open-questions): silent `sudo apt-get` on a remote host is the kind of surprise that erodes operator trust, especially when the remote is shared between teams. Make the user say "yes, install for me".

Bootstrap failure modes (all surface as exit `126` — backend mid-run failure — with a remediation message):

| Failure | What you see |
|---|---|
| `sudo` requires a password (NOPASSWD not configured) | `sudo: a password is required` → "the SSH user needs passwordless sudo for `apt-get install`. Configure `<user> ALL=(ALL) NOPASSWD: /usr/bin/apt-get` in /etc/sudoers, or pre-install <pkg> manually." |
| Non-Ubuntu OS (`/etc/os-release` doesn't say `ID=ubuntu`) | "auto-install only supports Ubuntu. Pre-install `<pkg>` on the target (RHEL: `yum install <pkg>`)." |
| Network unreachable from target (apt-get can't reach the repo) | "target can't reach the package repo. Check the target's egress policy or pre-install `<pkg>` manually." Exit `127` (we never got going). |

#### File materialisation

`RunOpts.Files` entries are written to a per-invocation tempdir on the remote. The tempdir is `/tmp/roksbnkctl.<random>/` where `<random>` is a fresh 16-byte hex string per `Run`:

```bash
# pseudo-flow
ssh <target> 'mkdir -m 0700 /tmp/roksbnkctl.<rand>'
scp <local-temp>/<basename> <target>:/tmp/roksbnkctl.<rand>/<basename>
ssh <target> '
  trap "rm -rf /tmp/roksbnkctl.<rand>" EXIT
  cd /tmp/roksbnkctl.<rand>
  <argv...>
'
```

The `trap … EXIT` is shell-builtin; it fires on normal exit, on `set -e` failure, on `SIGINT` (Ctrl-C), on `SIGTERM`. So even if the user kills their `roksbnkctl` invocation mid-run, the remote tempdir is cleaned up by the wrapper script's own trap before the SSH session terminates.

The `0700` mode on the tempdir ensures only the SSH user can read it during the brief on-disk window. On shared bastions (multi-user jumphosts) this matters — and it's why we materialise to `/tmp` (which the user owns) rather than `/var/tmp` or some shared scratch path.

Kubeconfig follows the same pattern: `Credentials.KubeconfigBytes` becomes `<tempdir>/kubeconfig`, the wrapper exports `KUBECONFIG=<tempdir>/kubeconfig`, the trap removes the file on exit. [PRD 04 §"Kubeconfig options for SSH backend"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md#ssh-host-ssh-backend) calls this "Option A" — scp-and-cleanup. We picked it over the in-memory `<()` process-substitution alternative because it's robust across remote shells and sshd configs.

#### Env propagation: `SetEnv` vs wrapper script

OpenSSH supports two ways to pass an env var to a remote command:

1. **`ssh -o SetEnv=KEY=VALUE target …`** — client tells the server "please add this to the env". Works only if the server's `sshd_config` has `AcceptEnv KEY` matching. Most stock sshd configs don't enable `AcceptEnv` for arbitrary keys.
2. **Wrapper script with `export KEY=VALUE`** — the script writes the env var into its own process before `exec "$@"`. Works regardless of sshd config, but the value lives briefly in a 0700 file on the remote.

The SSH backend tries `SetEnv` first. On the first connect to a new target, it sends a sentinel env var (`ROKSBNKCTL_SETENV_TEST=ok`) and runs `echo "$ROKSBNKCTL_SETENV_TEST"`. If the output is `ok`, `SetEnv` works on this target — the result is cached in workspace state, and subsequent runs use `SetEnv` directly.

If the sentinel doesn't surface, sshd silently dropped it (it logs `refused setenv request` on the server side, but clients don't see that). The backend falls back to a wrapper script:

```bash
#!/bin/sh
# /tmp/roksbnkctl.<rand>/wrap.sh, mode 0700, owner-readable only
trap 'rm -f "$0"' EXIT
set +o history
export IBMCLOUD_API_KEY='<value>'
exec "$@"
```

Then: `ssh <target> /tmp/roksbnkctl.<rand>/wrap.sh ibmcloud iam oauth-tokens`.

The wrapper-script path is the [Sprint 1 validator Issue 4 carry-over](https://github.com/jgruberf5/roksbnkctl/blob/main/issues/resolved_sprint1_validator.md) — the same shape `--on` uses for env passing today. Risks (file content includes the secret) are mitigated by:

- Mode `0700` so only the SSH user can read.
- `set +o history` so the value doesn't leak into shell history.
- `trap 'rm -f "$0"' EXIT` deletes the wrapper as soon as it exits — including on Ctrl-C, since the trap covers SIGINT/SIGTERM by virtue of being in the script's main process.
- The key is **never** in argv, so `ps -ef` on the remote doesn't show it.

`roksbnkctl targets show <name>` reports which mechanism the target uses (e.g., `env propagation: SetEnv (AcceptEnv ok)` or `env propagation: wrapper script (sshd refused SetEnv)`) so users can choose to enable `AcceptEnv` server-side if they prefer.

#### Bootstrap failure modes (consolidated)

| Symptom | Cause | Remediation |
|---|---|---|
| `sudo: a password is required` | NOPASSWD sudo not configured | Add `<ssh-user> ALL=(ALL) NOPASSWD: /usr/bin/apt-get` to `/etc/sudoers.d/roksbnkctl` on the target |
| `auto-install only supports Ubuntu` | `/etc/os-release` ID is not `ubuntu` | Pre-install the tool manually; RHEL: `sudo yum install <pkg>`; Alpine: `sudo apk add <pkg>` |
| `target can't reach the package repo` | Target's egress policy blocks `download.clis.cloud.ibm.com` (or upstream Ubuntu mirrors) | Pre-install or open egress; doctor's `--backend ssh:<target>` flags this |
| `tool not found on ssh target …; re-run with --bootstrap` | `--bootstrap` not passed and tool missing | Re-run with `--bootstrap`, or pre-install on the target |

#### When to use it

- You're running `ibmcloud` from a customer-firewalled office where the corporate jumphost can reach IBM Cloud APIs but your laptop can't.
- You're working in an air-gapped environment where `roksbnkctl` runs on your laptop but the IBM Cloud API conversations have to happen from a specific bastion's IP.
- You want a low-overhead remote-exec path that doesn't require a cluster (the `k8s` backend's prereq).

When `ssh` is the **wrong** call:

- The target lacks the tool and you don't want to mutate it. Skip `--bootstrap`; the backend errors clearly without installing anything.
- The target isn't Ubuntu and you don't want to pre-install. Bootstrap won't work; pre-install or use `local`/`docker`/`k8s`.
- You're running `iperf3` to measure cluster bandwidth. SSH puts the client somewhere on the network path *to* the cluster but not necessarily *adjacent to* it — `k8s` is the right answer for that case.

[Chapter 16](./16-on-flag-ssh-jumphosts.md) covers the lighter-weight `--on jumphost` predecessor that uses the same `targets:` config block. The SSH backend is the heavier-duty form: file materialisation, env propagation hardening, opt-in bootstrap. [Chapter 18](./18-choosing-backend.md) is the decision tree.

## The `Backend` interface

For the curious, the Go interface every backend conforms to:

```go
package exec

type Backend interface {
    Run(ctx context.Context, argv []string, opts RunOpts) (int, error)
    Name() string
}

type RunOpts struct {
    Stdin           io.Reader
    Stdout, Stderr  io.Writer
    Env             []string         // KEY=VALUE pairs
    WorkDir         string           // best-effort; some backends ignore (k8s)
    TTY             bool             // request PTY where supported
    Files           map[string][]byte // files materialized at exec time
    Credentials     *Credentials     // routed via PRD 04's per-backend mechanism
}

type Credentials struct {
    KubeconfigBytes []byte
    IBMCloudAPIKey  string
}
```

All four implementations satisfy this interface. Call sites in `cli/cluster.go`, `cli/test.go`, etc., get a `Backend` from the registry and call `Run(...)` — no branching on backend type. The uniformity is what makes the system extensible without rewriting callers each time a backend lands.

The `Credentials` struct is the bridge between the resolver chain (env → keychain → config-b64 → prompt) covered in [Chapter 14](./14-credentials-resolver.md) and the per-backend propagation rules in [PRD 04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md). Each backend translates the struct into the mechanism appropriate to where it runs: env vars for `local`, `--env KEY` (no `=value`) for `docker`, secretKeyRef for `k8s`, `SetEnv` or wrapper script for `ssh`.

## Cross-references

- [PRD 03 — pluggable execution backends](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md) — the design rationale and full per-backend spec.
- [PRD 04 — credential propagation](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md) — the cred-passing rules every backend implements.
- [Chapter 12 — Workspace config](./12-workspace-config.md) — the `exec:` block schema.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) — how creds reach the backend in the first place.
- [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md) — the lightweight remote-exec predecessor to the SSH backend.
- [Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md) — the decision tree.
- [Chapter 19 — The in-cluster ops pod](./19-in-cluster-ops-pod.md) — deploy-time mechanics for the `k8s` backend.
