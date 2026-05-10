# Execution backends: local, docker, k8s, ssh

`roksbnkctl` runs a handful of external tools as part of its job — `ibmcloud`, `terraform`, `iperf3`, eventually `dig`-equivalents and others. By default each tool runs as a child process on your laptop. That's fine for some tools and wrong for others: `iperf3` from your laptop measures your laptop's internet uplink, not the cluster's bandwidth.

The execution-backend system lets you pick **where** each tool runs without changing the surface command. The same `roksbnkctl ibmcloud ks cluster ls` invocation can run as a local process, inside a vendored container, inside the cluster, or on a remote SSH host — selected by a flag or a per-tool default in your workspace config.

This chapter is the introduction. Sprint 3 ships the system foundations and the `local` + `docker` backends; Sprint 4 expands the chapter with deep-dive sections on the `k8s` and `ssh` backends. Look for the `*Coming in Sprint 4.*` markers below.

## The four backends at a glance

| Backend | What it does | Ships in |
|---|---|---|
| **local** | `os/exec` — spawns the tool as a child process, inheriting your env and PATH | Sprint 3 (today; today's default for everything) |
| **docker** | `docker run` against a vendored image (`ghcr.io/jgruberf5/roksbnkctl-tools-<tool>:<v>`); frozen toolchain version | Sprint 3 (today) |
| **k8s** | Runs inside the cluster, either in a long-lived ops pod or as a one-shot Job; auth via SA token | Sprint 4 |
| **ssh** | Runs on a registered SSH target via the SSH client; auto-installs missing tools on Ubuntu | Sprint 4 |

Each backend solves a different problem:

- **local**: fastest startup, simplest mental model, requires the host tool to exist on `PATH`.
- **docker**: reproducible across dev machines, no host install needed, frozen at a known-good tool version.
- **k8s**: network-correct (private IPs reachable, cluster-internal services accessible), zero host install, in-cluster identity via ServiceAccount.
- **ssh**: pre-cluster ops from a known-IP bastion, customer-firewall workflows, air-gapped environments where the laptop can't reach IBM Cloud APIs but the jumphost can.

All four implementations conform to the same Go interface (`internal/exec.Backend`) so callers don't branch on backend type — they just call `backend.Run(ctx, argv, opts)` and let the implementation handle the mechanics. That uniformity is what lets the same `roksbnkctl ibmcloud ks cluster ls` work across all four with no surface-level change.

## The `--backend` CLI flag

Override the per-tool default for a single invocation:

```bash
# Today's default (local) is implicit
roksbnkctl ibmcloud ks cluster ls

# Same command, in a vendored docker image (today)
roksbnkctl ibmcloud --backend docker ks cluster ls

# Same command, in the cluster (Sprint 4)
roksbnkctl ibmcloud --backend k8s ks cluster ls

# Same command, on a remote SSH host (Sprint 4)
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

- **Backend-side failure of any kind** (Docker daemon down, image pull failed, container create/start error, binary not on PATH for `local`) ⇒ exit code `127`, with a message naming the cause. **No silent fallback to `local`.** Silent fallback hides intent and produces confusing test results.
- **Tool exit code** (the actual `ibmcloud` / `terraform` / `iperf3` exit code) ⇒ propagated 1:1, including non-zero codes.
- **Context cancellation / timeout** ⇒ exit code `137` (the conventional SIGKILL-on-signal code).

PRD 03 reserves both `126` and `127` for backend-specific failures with a finer-grained split (`126` for "backend started but mid-run failure", `127` for "backend startup failure"). Sprint 3's `local` + `docker` implementations collapse to `127` for both cases; the split lands in Sprint 4 if the use cases that motivated PRD 03's distinction surface in practice.

This way, your CI script can tell "the tool said X failed" (typical exit codes) from "the backend itself broke" (`127`) from "we ran out of time" (`137`).

## Per-tool defaults from `exec:`

Workspace config carries the per-tool default backend in the `exec:` block:

```yaml
# ~/.roksbnkctl/<workspace>/config.yaml
exec:
  ibmcloud:  { backend: local }
  iperf3:    { backend: k8s }
  terraform: { backend: local }
```

The defaults shipped today (Sprint 3):

| Tool | Default backend | Why |
|---|---|---|
| `terraform` | `local` | The terraform-exec local path is the established workflow. State handling is simplest here. |
| `ibmcloud` | `local` | Most users have it on PATH or are happy installing it. Compliance/firewall scenarios opt in via `--backend ssh:jumphost` or `docker`. |
| `iperf3` | `k8s` (Sprint 4) / `local` (today) | Throughput from a laptop's uplink isn't the cluster's bandwidth. The k8s default lands when Sprint 4 wires the backend; today's default falls back to `local`. |

[Chapter 12 — Workspace config](./12-workspace-config.md) covers the `exec:` block schema in detail; this chapter just notes its place in the backend system.

[Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md) (Sprint 4) is the decision tree for "which backend should I pick for this tool in this scenario".

## What's available today (Sprint 3)

### `local`

The default. `os/exec.CommandContext(ctx, argv[0], argv[1:]...)`, inheriting the parent process's environment, PATH, working directory. Identical to today's behaviour from previous sprints — the `local` backend is mostly a refactor of existing call sites through the new `Backend` interface so everything is uniform.

Scenarios where `local` is the right call:

- You have the tool installed and on PATH already.
- You want fastest startup (no container, no SSH handshake, no cluster API call).
- You're running `terraform` against the workspace's local state (the established workflow).
- You're debugging and want the simplest mental model for "where did that output come from".

### `docker`

Runs the tool inside a vendored container image:

```bash
roksbnkctl ibmcloud --backend docker ks cluster ls
```

Mechanically (Sprint 3's `ibmcloud` passthrough shape):

```
docker run --rm \
  -v <kubeconfig-path>:/root/.kube/config:ro \ # if the tool needs a kubeconfig
  -e IBMCLOUD_API_KEY \                        # env var name only; value inherits
  ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v> \
  ks cluster ls
```

There's no workspace-wide bind-mount: each invocation's mount set comes from `RunOpts.Files` (per-file `/work/<basename>` mounts), the kubeconfig (single-file at `/root/.kube/config`), and any explicit working dir from `RunOpts.WorkDir`. Sprint 3's `ibmcloud` passthrough sets none of those except the kubeconfig and `IBMCLOUD_API_KEY`.

Three things to call out:

1. **Frozen toolchain version.** The image tag pins the tool's version. Updates happen on `roksbnkctl` release, not on the host's package manager schedule.
2. **No host install.** You don't need `ibmcloud` on PATH. `docker` is the only prerequisite.
3. **Credential propagation by reference.** The `--env IBMCLOUD_API_KEY` form (no `=value`) inherits the value from the caller's env. The literal string never appears in `docker inspect` or `ps` listings — that's a [PRD 04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md) acceptance criterion. See [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) for the full propagation story.

The vendored images live at:

| Tool | Image |
|---|---|
| `ibmcloud` | `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v>` (vendored from `icr.io/ibm-cloud/ibmcloud-cli` upstream) |
| `iperf3` | `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>` (Alpine + iperf3) |
| `terraform` | `hashicorp/terraform:<v>` (official upstream) |

Image versions are tagged in lock-step with `roksbnkctl` releases; the GitHub Actions workflow that builds + pushes them runs on every release tag. See [Chapter 31 — Building from source](./31-building-from-source.md) (Sprint 6) for the build pipeline details.

When `docker` is the right call:

- You're on a clean dev machine without `ibmcloud` installed and don't want to install it.
- You need a frozen tool version for CI reproducibility.
- You're debugging a "works on my machine" issue and want to factor out the host install variable.

When `docker` is the **wrong** call:

- The tool needs network access that your laptop has but the container doesn't (rare; default bridge networking usually preserves laptop's egress).
- You're running `iperf3` and want a network-locality benefit — `docker` doesn't give you that vs `local`. Use `k8s` (Sprint 4) instead.
- You're on Windows. Linux/macOS docker daemons are in scope; Windows Docker Desktop coverage is deferred to a future round.

## Coming in Sprint 4

### `k8s` backend deep-dive

*Coming in Sprint 4.*

The k8s backend will cover:

- The two execution patterns (long-lived ops pod vs one-shot Job).
- `roksbnkctl ops install/show/uninstall` — managing the cluster-side ops pod, namespace, ServiceAccount, ClusterRole.
- Credential propagation via Kubernetes Secret (or, preferred, IAM trusted profile linking the ops pod's SA to a per-cluster IAM identity).
- iperf3-specific orchestration: server-side Deployment + LoadBalancer Service, client-side Job, log-collection workflow.

For now: the long-lived ops pod is the recommended path for `ibmcloud`-style ad-hoc commands; one-shot Jobs are the path for `iperf3` and (eventually) `terraform`. [Chapter 19 — The in-cluster ops pod](./19-in-cluster-ops-pod.md) (Sprint 4) covers the deployment-time mechanics.

### `ssh` backend deep-dive

*Coming in Sprint 4.*

The ssh backend will cover:

- How it builds on Sprint 1's `internal/remote.Client` (the same SSH client backing `--on`).
- File materialisation: `RunOpts.Files` written to `/tmp/roksbnkctl.<rand>/` on the remote, cleaned up via `trap` on session exit.
- Env propagation via `ssh -o SetEnv=KEY=VALUE` (preferred), falling back to a 0700 wrapper script when the remote sshd doesn't allow `AcceptEnv`.
- Apt-bootstrap: `command -v <tool>` first, `sudo apt-get install -y <pkg>` on missing (Ubuntu only this round).
- The SCP-and-cleanup pattern for shipping a kubeconfig to the remote without a persistent on-disk file.

For now: the `--on jumphost ...` path covered in [Chapter 16](./16-on-flag-ssh-jumphosts.md) is the lightweight predecessor. The same `targets:` config block drives both; Sprint 4's backend just gives the system more it can do with each target.

### Per-backend "when to use it" table

*Coming in Sprint 4.*

The full decision matrix lands in [Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md), with concrete scenarios per (tool, backend) pair: GSLB DNS testing wants `local`+`k8s`; iperf3 throughput wants `k8s`; ibmcloud from a customer-firewalled office wants `ssh`; frozen toolchain in CI wants `docker`.

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

All four implementations (today's `local` + `docker`, Sprint 4's `k8s` + `ssh`) satisfy this interface. Call sites in `cli/cluster.go`, `cli/test.go`, etc., get a `Backend` from the registry and call `Run(...)` — no branching on backend type. The uniformity is what makes the system extensible without rewriting callers each time a backend lands.

The `Credentials` struct is the bridge between the resolver chain (env → keychain → config-b64 → prompt) covered in [Chapter 14](./14-credentials-resolver.md) and the per-backend propagation rules in [PRD 04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md). Each backend translates the struct into the mechanism appropriate to where it runs: env vars for `local`, `--env KEY` (no `=value`) for `docker`, secretKeyRef for `k8s`, `SetEnv` or wrapper script for `ssh`.

## Cross-references

- [PRD 03 — pluggable execution backends](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md) — the design rationale and full per-backend spec.
- [PRD 04 — credential propagation](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md) — the cred-passing rules every backend implements.
- [Chapter 12 — Workspace config](./12-workspace-config.md) — the `exec:` block schema.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) — how creds reach the backend in the first place.
- [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md) — the lightweight remote-exec predecessor to the SSH backend.
- [Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md) — the decision tree (Sprint 4).
- [Chapter 19 — The in-cluster ops pod](./19-in-cluster-ops-pod.md) — deploy-time mechanics for the `k8s` backend (Sprint 4).
