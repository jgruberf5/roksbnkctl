# PRD 03 — Phase 3: pluggable execution backends

> Prerequisites: Phase 1 (SSH client) for the SSH backend; Phase 2 (kubectl-internal) for the K8s backend's pod orchestration; [PRD 04 (credentials)](./04-CREDENTIALS.md) read first to inform the `RunOpts` shape.
>
> Estimated effort: large (~2500 LOC across four backends + tool migration); 3-4 weeks.

## Goal

Define a common Go interface every external-tool runner uses, with backend-specific implementations that handle process spawn, I/O streaming, exit codes, working directories, and credential propagation. Apply to the tools that aren't Go-internalized: **iperf3**, **ibmcloud**, and (optionally) **terraform**.

## Why

Each backend solves a different problem:

| Backend | Solves |
|---|---|
| **Local exec** | Today's behavior; fastest startup; the default for terraform |
| **Local docker** | Frozen toolchain version; no host install of the tool; reproducible across dev machines |
| **In-cluster pod** | Network-correct (private IPs reachable); zero host install; pod auth via SA token |
| **SSH host** | Pre-cluster ops; customer firewalls; air-gapped; tools auto-installed on Ubuntu jumphost |

Different tools want different defaults:

- **iperf3**: must run from a network location adjacent to or inside the cluster; `local` measures laptop's internet uplink, not cluster bandwidth → **default `k8s`**
- **ibmcloud**: most users don't need a backend, but compliance/firewall scenarios benefit from `ssh` to a known-IP bastion → **default `local`**, opt-in to others
- **terraform**: terraform-exec on local host is the established pattern → **default `local`**, with `docker` for frozen-version CI and `k8s`/`ssh` for advanced network-locality use cases

## Scope

### In scope

- `internal/exec/` package with a `Backend` interface
- Four concrete implementations: `local`, `docker`, `k8s`, `ssh`
- Per-tool default backend in workspace config:
  ```yaml
  exec:
    iperf3:    { backend: k8s }
    ibmcloud:  { backend: local }
    terraform: { backend: local }
  ```
- `--backend <local|docker|k8s|ssh[:<target>]>` CLI flag override per invocation
- For the **SSH backend** specifically: simple "is the tool on PATH?" check, and if not, `apt-get install -y <package>` bootstrap (Ubuntu only — Phase 3.x extends to RHEL/Alpine)
- Tools migrated in Phase 3:
  - **iperf3** → primary backend `k8s`
  - **ibmcloud** → backend selectable; default `local`
  - **terraform** → backend selectable; default `local` (terraform-exec stays the local impl)

### Out of scope

- Backend pooling / warm containers — every call spawns fresh
- Multi-tenant container registries / image signing
- RHEL/CentOS/Alpine SSH bootstrap — Ubuntu only this round
- Automatic backend selection based on heuristics — explicit defaults + explicit `--backend` only
- Windows Docker Desktop coverage — Linux/macOS Docker daemons in scope; Windows users use internalized Go paths

## Design

### Backend interface

```go
package exec

type Backend interface {
    // Run executes argv with stdin/stdout/stderr wired to the given
    // streams. Returns the process exit code (mirrors the remote
    // process's exit code; 126/127 reserved for backend-specific
    // failures). ctx cancellation must terminate the remote process
    // within a few seconds.
    Run(ctx context.Context, argv []string, opts RunOpts) (int, error)

    // Name returns "local" | "docker" | "k8s" | "ssh" — used by
    // logging and doctor.
    Name() string
}

type RunOpts struct {
    Stdin           io.Reader
    Stdout, Stderr  io.Writer
    Env             []string         // KEY=VALUE pairs; merged with backend's defaults
    WorkDir         string           // best-effort; some backends ignore (k8s)
    TTY             bool             // request PTY where supported
    Files           map[string][]byte // files to materialize at exec time (e.g., kubeconfig)

    // Credentials passed via the documented per-backend mechanism
    // (PRD 04). Backends translate this struct into env vars,
    // bind-mounts, Secret references, or wrapper scripts.
    Credentials *Credentials
}

type Credentials struct {
    KubeconfigBytes []byte           // raw YAML content; nil = no kubeconfig
    IBMCloudAPIKey  string           // empty = no key
    // Future: AWSCredentials, GCPServiceAccount, etc.
}
```

### Backend lookup

```go
// internal/exec/registry.go
var backends = map[string]Backend{
    "local":  &LocalBackend{},
    "docker": &DockerBackend{},
    "k8s":    &K8sBackend{},
    "ssh":    &SSHBackend{},
}

// Resolve "ssh:jumphost" → SSH backend with target=jumphost
func ResolveBackend(spec string) (Backend, error) { ... }
```

### Per-backend implementation

#### Local (`internal/exec/local.go`)

Wraps `os/exec`. The default. Identical behavior to today. ~50 LOC. Mostly exists already in lifecycle commands; this just refactors them to share.

#### Docker (`internal/exec/docker.go`)

Builds a `docker run` invocation:

```bash
docker run --rm \
  --workdir /work \
  -v <tempdir>:/work \                       # for Files
  -v <kubeconfig-path>:/root/.kube/config:ro \  # if Credentials.KubeconfigBytes set
  -e IBMCLOUD_API_KEY \                      # if Credentials.IBMCloudAPIKey set; takes from caller env
  -e <user-Env>... \
  [-it]                                      # if TTY
  <tool-image>:<version> \
  <argv...>
```

Image per tool:

| Tool | Image |
|---|---|
| `ibmcloud` | `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v>` (vendored from icr.io/ibm-cloud/ibmcloud-cli upstream) |
| `iperf3` | `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>` (Alpine + iperf3 package) |
| `terraform` | `hashicorp/terraform:<v>` (official) |

Image build in `tools/docker/<tool>/Dockerfile` — small and reproducible; published via goreleaser on release tags.

Cleanup: `--rm` plus a `defer cli.ContainerKill` if ctx cancels mid-run.

Detail: see [PRD 04 § Docker container](./04-CREDENTIALS.md) for credential propagation specifics — never bake creds into the image, never put `--env IBMCLOUD_API_KEY=<value>` (the value would show in `docker inspect`); use `--env IBMCLOUD_API_KEY` (no value, inherits from caller env).

#### K8s (`internal/exec/k8s.go`)

Two patterns:

**Long-lived ops pod** (recommended for ibmcloud and ad-hoc shell):

- Deployed once via `roksbnkctl ops install` (new command in Phase 3) or auto-deployed post `roksbnkctl up`
- Lives in `roksbnkctl-ops` namespace with a dedicated SA + minimum-privilege ClusterRole
- Image bundles tools (ibmcloud CLI, kubectl as backup, etc.)
- `Run()` does `kubectl exec -n roksbnkctl-ops ops-pod -- <argv...>` via `client-go`'s SPDY executor

**One-shot Job** (for iperf3 client, terraform):

- Build a Job spec per invocation
- Mount `Files` via projected Secret
- Mount `Credentials` via Secret (separate from Files so it lives at a documented path)
- `kubectl logs -f` equivalent via `client-go` `Pods().GetLogs(...).Stream()` after the Job pod becomes ready
- Wait for pod completion; surface its exit code (read from container status `terminated.exitCode`)
- Auto-delete on completion (`ttlSecondsAfterFinished: 60`)

For iperf3 specifically: the **server** side runs as a long-lived Pod + LoadBalancer Service deployed at test start; the **client** side runs as a Job. Both tear down at test end. Outputs (iperf3's `-J` JSON) collected from the client Pod's logs.

#### SSH (`internal/exec/ssh.go`)

Builds on Phase 1's `internal/remote/ssh.go`. Adds:

**Pre-flight tool check + bootstrap** (Ubuntu only):

```go
func (b *SSHBackend) ensureToolInstalled(ctx context.Context, target *Target, tool string) error {
    // 1. Check: ssh target "command -v <tool>"
    rc, _ := b.client.Run(ctx, []string{"command", "-v", tool}, ...)
    if rc == 0 { return nil }

    // 2. Bootstrap (Ubuntu): apt-get update + install
    pkg := toolPackages[tool]  // e.g., iperf3 → "iperf3", ibmcloud → "ibmcloud-cli"
    if pkg.IBMRepo {
        // ibmcloud needs IBM's apt repo + key first
        b.client.Run(ctx, []string{"sh", "-c",
          "curl -fsSL https://download.clis.cloud.ibm.com/Linux/Ubuntu/repo.gpg | sudo apt-key add - && " +
          "echo 'deb https://download.clis.cloud.ibm.com/Linux/Ubuntu jammy main' | sudo tee /etc/apt/sources.list.d/ibmcloud.list",
        }, ...)
    }
    rc, err := b.client.Run(ctx, []string{"sudo", "-n", "apt-get", "update", "-y"}, ...)
    if err != nil || rc != 0 { return fmt.Errorf("apt-get update failed (sudo password required?): rc=%d", rc) }
    rc, err = b.client.Run(ctx, []string{"sudo", "-n", "apt-get", "install", "-y", pkg.Name}, ...)
    if err != nil || rc != 0 { return fmt.Errorf("installing %s failed: rc=%d", pkg.Name, rc) }
    return nil
}
```

**File materialization**: write each `RunOpts.Files` entry to `/tmp/roksbnkctl.<random>/<basename>` on the remote via the SSH session, then exec with `-w /tmp/roksbnkctl.<random>` as the working directory. Cleanup via `trap 'rm -rf /tmp/roksbnkctl.<random>' EXIT` in a wrapper script.

**Env propagation**: see [PRD 04 § SSH](./04-CREDENTIALS.md). Two paths: `SetEnv` (preferred, requires sshd `AcceptEnv` directive) and wrapper-script-with-trap (fallback).

**TTY**: `--tty` flag on the SSH session.

**Bootstrap failure modes**:
- `sudo` requires password → exit 126, message: "the SSH user needs passwordless sudo for `apt-get install <pkg>`. Configure `<user> ALL=(ALL) NOPASSWD: /usr/bin/apt-get` in /etc/sudoers, or pre-install <pkg> manually."
- Non-Ubuntu OS → exit 126, message: "auto-install only supports Ubuntu. Pre-install <pkg> on the target (RHEL: `yum install <pkg>`)."
- Network unreachable from target → exit 127, message: "target can't reach the package repo."

### Tool migration plan

For each tool: default backend, supported backends, image/package details.

#### iperf3

| Field | Value |
|---|---|
| **Default backend** | `k8s` |
| **Supported** | `k8s`, `local`, `ssh` |
| **K8s shape** | `roksbnkctl-iperf3-server` Deployment + LoadBalancer Service in `roksbnkctl-test` namespace; `roksbnkctl-iperf3-client` Job runs `iperf3 -c <server-svc> -J` and emits JSON to stdout; collect log + tear down everything |
| **SSH shape** | ensure `iperf3` installed on target (auto-install via apt), spawn `iperf3 -c <cluster-LB-endpoint> -J`, capture JSON |
| **Local shape** | requires host iperf3 install; same as today; doctor flags it as "needed for backend=local" only |
| **Image (k8s server)** | `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>` (Alpine + iperf3) |
| **OpenShift SCC** | the iperf3 pod needs privileged ports (none, actually — 5201 is unprivileged) and host-network=false; the existing `restricted-v2` SCC works once pod's securityContext is set correctly |

The `restricted-v2` SCC failure we hit in E2E was because the pod manifest didn't set `runAsNonRoot/allowPrivilegeEscalation/seccompProfile/capabilities.drop` correctly. Phase 3 fixes this in the manifest the k8s backend builds.

#### ibmcloud

| Field | Value |
|---|---|
| **Default backend** | `local` |
| **Supported** | `local`, `docker`, `k8s`, `ssh` |
| **Docker image** | `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v>` (vendored ibmcloud-cli + ks plugin) |
| **K8s pod** | runs in the long-lived ops pod with the same image; auth via injected IBMCLOUD_API_KEY env from a Secret |
| **SSH apt package** | `ibmcloud-cli` from IBM's apt repo (auto-add the repo on bootstrap) |
| **Auto-login** | every backend needs `IBMCLOUD_API_KEY`; backends propagate it per [PRD 04](./04-CREDENTIALS.md). The `ibmcloud login --apikey @/dev/stdin` invocation is the same regardless of where it runs |

#### terraform

| Field | Value |
|---|---|
| **Default backend** | `local` (terraform-exec, today's path) |
| **Supported** | `local`, `docker`, `k8s`, `ssh` |
| **Use case for non-local** | run TF inside customer VPC for IP-egress reasons; CI runners with frozen TF version; air-gapped via SSH bastion |
| **Image** | `hashicorp/terraform:<v>` (official) |
| **State concerns** | each backend needs the workspace's TF state (sensitive!). Local: trivial. Docker: bind-mount `~/.roksbnkctl/<ws>/state/`. K8s: stash state in a versioned ConfigMap/Secret pair (state file size cap is 1 MiB by default, may need raising). SSH: scp pre/post; treat as an atomic state move |

State handling for non-local backends is the trickiest part of terraform support. Phase 3.0 ships **local + docker** for terraform; **k8s + ssh** deferred to 3.x once state-handling is solid.

#### DNS probe (GSLB-aware)

| Field | Value |
|---|---|
| **Default backends** | `local` **and** `k8s` (run both, surface both answers — see GSLB note) |
| **Supported** | `local`, `k8s`, `ssh` (docker available but rarely useful — no network-locality benefit over local) |
| **Library** | [`github.com/miekg/dns`](https://github.com/miekg/dns) — the reference Go DNS implementation (used by CoreDNS), MIT licensed. Replaces `dig` as a host prerequisite. |
| **Why miekg/dns over std lib `net`** | std `net.Resolver` only exposes a fixed record-type set (A/AAAA/CNAME/MX/NS/SRV/TXT) and can't easily target a specific server per query; miekg/dns gives full protocol surface, per-query custom server, and exposes RTT directly for latency measurement |
| **K8s shape** | one-shot Job in `roksbnkctl-test` namespace; runs the embedded probe (no separate image needed — the `roksbnkctl` binary itself runs in `dns-probe` mode); emits JSON output to its log |
| **SSH shape** | execute the `roksbnkctl` binary on the SSH target (or scp it first if missing); same JSON output |
| **Local shape** | runs the probe in-process; no external dependencies |

**GSLB use case** — the primary motivator. F5 BIG-IP Next's GSLB returns different answers depending on the requesting resolver's IP (geographic affinity, datacenter routing, health-check state). To validate GSLB is working, you have to query the same name from multiple network vantage points and compare:

- From **local** (your laptop): see the answer your office IP gets
- From **k8s** (in-cluster): see the answer the cluster's worker nodes get when they egress
- From **ssh** (a customer bastion in another region): see the answer that bastion's IP gets

The DNS probe runs all three (or any subset) in parallel and emits a comparison report. Different answers across vantage points are **expected** in a healthy GSLB; identical answers might indicate the GSLB rules aren't taking effect.

**CLI surface**:

```bash
# Single-vantage probe with full control
roksbnkctl test dns \
  --target www.example.com \
  --type A \
  --server 8.8.8.8 \
  --backend local

# GSLB comparison probe (default — runs across all configured vantage points)
roksbnkctl test dns \
  --target www.example.com \
  --type A \
  --server gslb-vip.f5.example.com \
  --gslb-compare      # implied when both `local` and `k8s` are configured backends

# Latency measurement (built in to every probe)
roksbnkctl test dns \
  --target www.example.com \
  --type A \
  --server 8.8.8.8 \
  --iterations 10 \
  -o json
# JSON includes: rtt_ms (per query), rtt_p50/p95/p99 across iterations,
# answer_set (all RRs returned), authoritative flag, truncated flag.
```

**Record types supported**: A, AAAA, CNAME, MX, NS, TXT, SRV, SOA, PTR, CAA, DS, DNSKEY, ANY. Anything `miekg/dns` exposes via its `dns.Type` enum — basically all of them.

**Server resolution**:
- `--server <ip>` or `--server <hostname>:<port>` — explicit IP/hostname
- `--server system` — use the host's `/etc/resolv.conf` resolvers (today's behavior)
- `--server cluster` — use the cluster's CoreDNS (k8s backend only — resolves via the pod's `/etc/resolv.conf` which CoreDNS owns)
- `--server <name-from-config>` — named resolver in workspace config:
  ```yaml
  test:
    dns:
      resolvers:
        google:    "8.8.8.8:53"
        cloudflare:"1.1.1.1:53"
        gslb-vip:  "169.45.91.5:53"
      default_target: "www.example.com"
  ```

**JSON output schema** (`-o json`, schema `roksbnkctl.dns.v1`):

```json
{
  "schema": "roksbnkctl.dns.v1",
  "target": "www.example.com",
  "type": "A",
  "vantages": [
    {
      "backend": "local",
      "server": "8.8.8.8:53",
      "iterations": 10,
      "rtt_ms": { "p50": 12.4, "p95": 18.1, "p99": 22.7 },
      "answers": [
        { "name": "www.example.com.", "type": "A", "ttl": 60, "rdata": "169.45.91.10" }
      ],
      "rcode": "NOERROR",
      "authoritative": false,
      "truncated": false,
      "edns_client_subnet": null
    },
    {
      "backend": "k8s",
      "server": "8.8.8.8:53",
      "rtt_ms": { "p50": 8.2, "p95": 11.0, "p99": 14.3 },
      "answers": [
        { "name": "www.example.com.", "type": "A", "ttl": 60, "rdata": "10.20.30.40" }
      ],
      "rcode": "NOERROR"
    }
  ],
  "gslb_divergence": true,
  "gslb_divergence_summary": "answers differ between local (169.45.91.10) and k8s (10.20.30.40) — GSLB returning location-specific records as expected"
}
```

The `gslb_divergence` bool is true when the answer sets differ across vantages — useful for CI assertions (`exit 0` if divergence is expected; flip to `--require-divergence` to fail when GSLB silently returns identical answers everywhere).

**Latency measurement**:
- Per-query RTT extracted from `miekg/dns`'s response handler (its `Exchange()` returns `time.Duration`)
- `--iterations N` runs N queries against the same server, reports p50/p95/p99
- Useful for detecting GSLB health-check flapping or anycast routing changes
- For k8s/ssh backends: measured **inside** the remote vantage point, so it reflects the actual resolver-to-resolver path, not the laptop-to-cluster transit

**Why no `docker` backend**: a Docker container running locally has the same network identity as the local host (default bridge networking) — no GSLB-relevant network-locality difference. Skipping by design; `--backend docker` errors with "DNS probe doesn't benefit from docker; use local instead."



1. **`internal/exec/Backend` interface** + `RunOpts` + `Credentials` structs in a new package
2. **`internal/exec/local.go`** — refactor existing `os/exec` callsites in `cli/cluster.go` (passthroughs) to dispatch through this
3. **Tool image build infra**:
   - `tools/docker/ibmcloud/Dockerfile`
   - `tools/docker/iperf3/Dockerfile`
   - GitHub Actions workflow to build + push to `ghcr.io/jgruberf5/...` on tag releases
4. **`internal/exec/docker.go`** — build via `github.com/docker/docker/client`; `--backend docker` end-to-end with ibmcloud
5. **`internal/exec/k8s.go`** — Pod + Job templates; iperf3 happy path; long-lived ops pod for ibmcloud
6. **`internal/cli/ops.go`** — new `roksbnkctl ops install/show/uninstall` command for the long-lived ops pod
7. **`internal/exec/ssh.go`** — depends on Phase 1; ibmcloud + iperf3 over SSH; apt-bootstrap; file materialization; cleanup-on-exit
8. **Workspace config `exec:` block** → backend lookup wired in `cli/test.go test throughput`, `cli/cluster.go ibmcloud passthrough`, etc.
9. **`--backend` CLI flag** parsed at root, scoped per subcommand
10. **Doctor extensions**: `roksbnkctl doctor --backend k8s` checks SA + Secret + image pull; `--backend docker` checks daemon reachable; `--backend ssh` checks target connectivity
11. **iperf3 SCC fix**: rebuild the pod manifest `internal/test/throughput.go` with proper `securityContext` so `restricted-v2` SCC accepts it
12. **DNS probe internalization**:
    - Add `github.com/miekg/dns` dep
    - `internal/test/dns.go` — replace today's `net.Resolver` impl with a miekg-based `Probe` struct supporting `--server`, `--type`, `--iterations`, RTT capture
    - `cli/test.go` — extend `dns` subcommand with the new flags; add `--gslb-compare` multi-vantage mode that fans out to all configured backends and emits the comparison JSON
    - `internal/exec/k8s.go` — add a `dns-probe` Job mode that execs `roksbnkctl` itself in-cluster (single binary, no separate image) with the probe args
    - Workspace config schema: add `test.dns.resolvers` map and `test.dns.default_target`
13. **Logging**: every backend prefixes its stderr lines with `[<backend>] ` so users can tell where output is coming from in mixed-mode runs

## Acceptance criteria

- `roksbnkctl test throughput --backend k8s` runs iperf3 entirely in cluster, no local iperf3 install needed; JSON output matches local-backend's
- `roksbnkctl ibmcloud --backend docker ks cluster ls` produces output identical to local-backend (modulo CLI version line)
- `roksbnkctl ibmcloud --backend ssh:jumphost ks cluster ls` works on a fresh Ubuntu jumphost (auto-installs ibmcloud CLI on first call; subsequent calls skip the install check via a marker file)
- `roksbnkctl up --backend docker` runs terraform inside `hashicorp/terraform:<v>` against the bundled TF source; state file persisted to `~/.roksbnkctl/<ws>/state/` via bind mount
- Backend selection from workspace config + flag override are both honored; flag wins
- iperf3 throughput test no longer hits SCC/PodSecurity warnings on OpenShift
- `roksbnkctl test dns --target gslb.example.com --type A --server 8.8.8.8` runs without `dig` installed and reports per-query RTT plus p50/p95/p99 across `--iterations`
- `roksbnkctl test dns --gslb-compare ...` runs the same probe from `local` + `k8s` (and `ssh:<target>` if configured), emits a single comparison JSON; `gslb_divergence: true` when answers differ across vantages
- Doctor reports per-backend availability accurately

## Open questions

- **Long-lived ops pod vs per-call Job for k8s backend**: defaulting to long-lived (faster, ad-hoc shell capable, slightly more management surface). Job-only mode for ephemeral CI runs?
- **Image versioning**: tie tool image versions to `roksbnkctl` release version, or independent? Tying simplifies reproducibility; independent allows tool patches without a roksbnkctl release.
- **`--bootstrap` opt-in for SSH**: should auto-install of missing tools require `--bootstrap` to opt in, to avoid surprise `sudo apt-get` invocations? **Recommendation: yes, opt-in by default**, with a clear "missing tool, run with --bootstrap to install" error.
- **Backend startup failures**: Docker daemon down, cluster unreachable, ssh target unreachable — fall back to `local` (with warning) or hard error? **Recommendation: hard error**, since silent fallback hides intent.
- **`--backend ssh` without `:target`**: assume `jumphost` if it's the only target? Or require explicit? **Require explicit.**

## Related work

- [PRD 01 (SSH/--on)](./01-SSH-AND-ON-FLAG.md) — SSH client this backend builds on
- [PRD 02 (kubectl-internal)](./02-KUBECTL-INTERNAL.md) — k8s client used for the K8s backend's pod orchestration
- [PRD 04 (credentials)](./04-CREDENTIALS.md) — credential propagation rules every backend implements
- [PRD 05 (E2E)](./05-E2E-TEST-PLAN.md) — Phases K (docker), L (k8s), N (mixed-mode) test this PRD's deliverables
