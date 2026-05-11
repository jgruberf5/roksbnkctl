# Choosing a backend per tool

[Chapter 17](./17-execution-backends.md) covered the **mechanics** of each backend. This chapter is the **decision tree**: given a tool and a scenario, which of `local` / `docker` / `k8s` / `ssh` is the right call.

If you're searching for "which backend should I use", you've landed on the right page.

## The four backends in one line each

| Backend | One-line summary | Deep dive |
|---|---|---|
| `local` | `os/exec` on your laptop; fastest, requires the tool on PATH | [§ Local backend](./17-execution-backends.md#local-backend) |
| `docker` | `docker run` against a vendored image; frozen tool version, no host install | [§ Docker backend](./17-execution-backends.md#docker-backend) |
| `k8s` | inside the cluster (long-lived ops pod or one-shot Job); cluster-correct network identity | [§ K8s backend](./17-execution-backends.md#k8s-backend) |
| `ssh:<target>` | on a registered SSH target; opt-in apt-bootstrap on Ubuntu | [§ SSH backend](./17-execution-backends.md#ssh-backend) |

If you're skimming, the cheat-sheet is:

- **`local`** when you have the tool installed and the host's network identity is correct for the call.
- **`docker`** when you don't have the tool and don't want to install it, or you need a frozen version for CI.
- **`k8s`** when the call's *network position* matters and the cluster is the right vantage point.
- **`ssh:<target>`** when the call needs to originate from a specific external host (a customer bastion, an air-gapped bridge).

The rest of this chapter is the longer version.

## Per-tool default backends

Every tool has a default backend baked into `roksbnkctl`. Workspace config (`exec:` block) can override the default per workspace; `--backend` overrides for a single invocation.

| Tool | Default | Resolved by |
|---|---|---|
| `ibmcloud` | `local` | `internal/cli/cluster.go::resolveBackendSpecWith("ibmcloud", flagOverride)` |
| `iperf3` | `k8s` | `internal/cli/test.go::resolveBackendSpecWith("iperf3", flagOverride)` |
| `terraform` | `local` | `internal/cli/lifecycle.go::resolveBackendSpecWith("terraform", flagOverride)` |

The defaults reflect "what's the right answer for the most common scenario":

- **`ibmcloud` defaults to `local`** because most users have it on PATH or are happy installing it. The compliance + firewall scenarios where `ssh` or `docker` are better are the minority of calls.
- **`iperf3` defaults to `k8s`** because throughput from a laptop's uplink isn't the cluster's bandwidth. The k8s backend places the iperf3 client in (or adjacent to) the cluster so the number reflects fabric, not Wi-Fi. Laptop-uplink-to-cluster is a real measurement too, but it's the special case — opt in via `--backend local`.
- **`terraform` defaults to `local`** because the terraform-exec local path is the established workflow. State handling is simplest there. Frozen-version CI runs use `--backend docker`; non-local network-locality use cases (cluster-side, SSH-bastion-side) are deferred to a future release pending a state-handling design — see [PRD 03 §"State concerns"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#terraform).

To change a default per workspace, edit `~/.roksbnkctl/<workspace>/config.yaml`:

```yaml
exec:
  iperf3:    { backend: k8s }      # already the default; shown for clarity
  ibmcloud:  { backend: ssh:bastion }
  terraform: { backend: docker }
```

[Chapter 12 §"`exec:`"](./12-workspace-config.md) covers the schema. The `--backend` CLI flag overrides whatever is in `exec:` for a single invocation.

## Per-tool supported-backend matrix

Not every tool supports every backend. The authoritative matrix at v1.0:

| Tool | `local` | `docker` | `k8s` | `ssh:<target>` |
|---|---|---|---|---|
| `ibmcloud` | yes (default) | yes (frozen image) | yes (long-lived ops pod) | yes |
| `iperf3` | yes (opt-in: laptop vantage) | not supported (same network identity as `local`) | yes (default) | yes |
| `terraform` | yes (default) | yes (frozen image) | deferred to v1.x (state-file handling) | deferred to v1.x (state-file handling) |
| DNS probe | yes (default for laptop vantage) | not supported (same network identity as `local`) | yes (cluster vantage) | yes (remote vantage) |
| `kubectl` / `oc` | internalised — runs via the Go client, not via a host binary | n/a | n/a | n/a |
| `dig` | internalised — DNS probe replaces `dig` for in-tree work | n/a | n/a | n/a |

Legend:

- **yes** — supported; same surface command works on this backend.
- **yes (default)** — this backend is the per-tool default; pass `--backend other` to override.
- **not supported** — rejected at CLI parse time with a clear error pointing at the right alternative.
- **deferred to v1.x** — a real design constraint, not a gap; see the cell text and [PRD 03 §"State concerns"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#terraform).
- **internalised** — `roksbnkctl` performs the operation via its own embedded library, not by shelling out; no backend selection applies.

The "no" entries are intentional design decisions, not gaps:

- **`iperf3` over `docker` is rejected** because a Docker container running locally has the same network identity as the host — same NAT egress, same uplink, same observed bandwidth as `--backend local`. The user's mental model would be "I picked docker, so the iperf3 must be hermetic now" but the throughput number wouldn't actually differ. Better to refuse and force the user to pick `local` (deliberate laptop measurement) or `k8s` (cluster measurement).
- **DNS probe over `docker` is rejected** for the same reason. DNS resolution from a Docker container with default bridge networking goes through the same resolver as the host. There's no GSLB-relevant network-locality difference. The probe subcommand errors with "DNS probe doesn't benefit from docker; use local instead" when `--backend docker` is passed.
- **`terraform` over `k8s` and `ssh` is deferred to v1.x**. The state file is sensitive (admin tokens, generated TLS keys, license bundles); moving it into a Kubernetes Secret or scp'ing it pre/post-run requires a state-handling design that hasn't shipped yet. [PRD 03 §"State concerns"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#terraform) lays out the considerations; the roadmap entry lives in [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) §"What's deliberately deferred to post-v1.0".

Passing an unsupported `(tool, backend)` pair errors at the CLI layer before the backend is invoked:

```
$ roksbnkctl test throughput --backend docker
error: iperf3 doesn't support backend `docker` (same network identity as `local`,
       no value-add); supported: local, k8s, ssh:<target>
```

## Decision tree

Pick the question that matches your scenario.

### "I want to measure cluster bandwidth"

Use `--backend k8s`. The default for `iperf3` is already `k8s` — the explicit flag is redundant unless you've overridden the default in workspace config:

```bash
roksbnkctl test throughput
# equivalent to:
roksbnkctl test throughput --backend k8s
```

The k8s backend deploys a server-side Deployment + LoadBalancer Service in `roksbnkctl-test`, runs the iperf3 client as a one-shot Job in the same namespace, collects the JSON output from the client pod's logs, and tears down both. The bandwidth number reflects the cluster fabric.

If you instead want to measure your laptop's uplink to the cluster:

```bash
roksbnkctl test throughput --backend local --endpoint <cluster-LB-ip>:5201
```

That's a deliberately different measurement — useful when you suspect office Wi-Fi, not cluster fabric, is the bottleneck.

### "I'm doing GSLB DNS validation"

Use **both** `local` and `k8s`. F5 BIG-IP Next's GSLB returns different answers depending on the requesting resolver's IP — geographic affinity, datacenter routing, health-check state. To validate that the GSLB is actually doing this, query from multiple network vantage points and compare.

The multi-vantage probe ships at v1.0 via `roksbnkctl test dns --gslb-compare`:

```bash
roksbnkctl test dns \
  --target www.example.com \
  --type A \
  --server gslb-vip.f5.example.com \
  --gslb-compare
```

`--gslb-compare` fans out to every configured vantage (`local` for your office IP, `k8s` for the cluster's egress IP, `ssh:<region-bastion>` for a bastion in another region) in parallel and emits a single comparison JSON with a `gslb_divergence` boolean. Different answers across vantages are **expected** in a healthy GSLB; identical answers might mean the GSLB rules aren't taking effect for the resolver positions you queried from.

[Chapter 21 — DNS testing for GSLB](./21-dns-testing-gslb.md) is the full reference.

### "I need to run `ibmcloud` from a customer-firewalled office"

Use `--backend ssh:<bastion>`. Your customer's network policy lets the corporate jumphost reach `*.cloud.ibm.com` but blocks your laptop. The SSH backend ships your kubeconfig to the bastion (single file, mode `0600`, removed via `trap` on session exit), runs `ibmcloud` there, streams the output back:

```bash
roksbnkctl ibmcloud --backend ssh:bastion ks cluster ls
```

If `ibmcloud` isn't installed on the bastion, you'll get a clear error:

```
error: tool `ibmcloud` not found on ssh target bastion; re-run with --bootstrap to install
       via apt-get, or pre-install on the target manually
```

Re-run with `--bootstrap` if you want `roksbnkctl` to `sudo apt-get install -y ibmcloud-cli` on the bastion. The opt-in default reflects "we don't surprise users with `sudo apt-get` on a remote they didn't expect mutation on" — see [Chapter 17 §"SSH backend"](./17-execution-backends.md#ssh-backend) for the bootstrap mechanics.

### "I'm in CI and want a frozen toolchain version"

Use `--backend docker`. The vendored images are tagged in lock-step with `roksbnkctl` releases — `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v1.0.0` is the exact same `ibmcloud` binary every CI run sees, regardless of when the runner image was built or what `apt-get` happens to ship that day:

```bash
roksbnkctl ibmcloud --backend docker iam oauth-tokens
roksbnkctl up --backend docker     # terraform inside hashicorp/terraform:<v>
```

For CI specifically, also pin `ibmcloud.api_key_source: env` in workspace config so the API key resolution is unambiguous (no keychain fallback to confuse a non-interactive runner) — see [Chapter 14 §"Pinning a single source"](./14-credentials-resolver.md#pinning-a-single-source).

### "I'm on a clean dev machine without `ibmcloud` installed"

Use `--backend docker`. No `apt-get install ibmcloud-cli`, no IBM repo + GPG key dance, no upstream-package-version mismatch — `docker pull ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev` is the only setup, and `roksbnkctl` does that for you on first invocation.

Alternatively, if your laptop is the dev machine and you'll run `ibmcloud` more than once, just install it. The `local` backend has lower per-invocation startup latency than `docker` (no container create/start/log-attach), so once you've paid the install cost the local path is faster for the rest of the session.

### "I want a cluster-side ad-hoc shell"

Use `--backend k8s` with the long-lived ops pod. Once `roksbnkctl ops install` has run, `--backend k8s` for `ibmcloud` (or any future tool) routes through `kubectl exec -n roksbnkctl-ops ops -- <argv>`. The pod stays alive between invocations, so the second and subsequent commands skip pod-startup latency.

```bash
roksbnkctl ops install
roksbnkctl ibmcloud --backend k8s iam oauth-tokens
roksbnkctl ibmcloud --backend k8s ks cluster ls
roksbnkctl ibmcloud --backend k8s account list
```

[Chapter 19](./19-in-cluster-ops-pod.md) is the full reference for the ops pod lifecycle.

### "I'm pre-cluster — there's no cluster yet"

Use `local` or `ssh:<target>`. The `k8s` backend prereq is a working kubeconfig pointing at a running cluster; before `roksbnkctl up` has succeeded, that doesn't exist. For pre-cluster ibmcloud + terraform calls (account inspection, IAM tinkering, the cluster-create itself), `local` and `ssh:bastion` are the only two paths.

## When *not* to use a backend

Common foot-guns, in rough order of how often they come up:

### `--backend k8s` without `roksbnkctl ops install`

The ops pod must exist before the k8s backend can route `ibmcloud` calls through it. First-time use:

```bash
roksbnkctl ops install         # one-time setup per cluster
roksbnkctl ibmcloud --backend k8s ks cluster ls
```

If you skip the install, the backend errors with a clear remediation:

```
error: ops pod not found in roksbnkctl-ops namespace; run `roksbnkctl ops install` first
```

[Chapter 19](./19-in-cluster-ops-pod.md) covers the install/show/uninstall lifecycle.

### `--backend docker` for a network-locality test

`iperf3` and the DNS probe both reject `--backend docker` because a local Docker container has the same network identity as the host (default bridge networking). The probe wouldn't measure anything different. The CLI errors at parse time:

```
$ roksbnkctl test throughput --backend docker
error: iperf3 doesn't support backend `docker` (same network identity as `local`,
       no value-add); supported: local, k8s, ssh:<target>
```

If you actually want a hermetic-tools throughput test, `--backend k8s` is the right answer.

### `--backend ssh:host` without `--bootstrap` on a fresh target

If `ibmcloud` (or `iperf3`) isn't installed on the target, the SSH backend won't silently `sudo apt-get` for you — `--bootstrap` is opt-in. The first call on a fresh target tells you exactly what's needed:

```
error: tool `ibmcloud` not found on ssh target bastion; re-run with --bootstrap to install
       via apt-get, or pre-install on the target manually
```

Re-run with `--bootstrap` if mutation is OK; otherwise pre-install via your config-management of choice (Ansible, Salt, baked-in-image).

### `--backend ssh:host` to a non-Ubuntu target with `--bootstrap`

The apt-bootstrap recipe is Ubuntu-only this round. RHEL / CentOS / Alpine targets need pre-installation via `yum` / `dnf` / `apk` — `--bootstrap` errors out cleanly:

```
error: auto-install only supports Ubuntu. Pre-install `ibmcloud-cli` on the target
       (RHEL: `yum install ibmcloud-cli`)
```

Once the tool is installed, `--backend ssh:host` works without `--bootstrap`.

### `--backend k8s` for `terraform`

Deferred to v1.x. The `terraform` tool's k8s + ssh backends require a state-handling design that hasn't shipped — moving the state file into a Kubernetes Secret or scp'ing it pre/post-run is fiddly enough to be a feature in its own right ([PRD 03 §"State concerns"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#terraform); roadmap in [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) §"What's deliberately deferred to post-v1.0"). For now, `terraform` supports `local` and `docker` only. If the network-locality use case (running `terraform` from a customer VPC for IP-egress reasons) is blocking, file an issue.

### Mixing `--on` and `--backend ssh:<target>`

`--on <target>` is the [Chapter 16](./16-on-flag-ssh-jumphosts.md) lightweight remote-exec — it runs the *passthrough* shape (`exec`, `shell`, `kubectl`, `oc`, `ibmcloud`) on the target by literally re-running the command via SSH. `--backend ssh:<target>` is the heavier-duty form — it routes through the `Backend` interface, which means file materialisation, env propagation hardening, opt-in apt-bootstrap, and the redactor are all wired in.

You generally want one or the other, not both. The supported precedence is "`--backend ssh:<target>` wins"; passing both flags on the same invocation surfaces a warning. If you're calling `roksbnkctl ibmcloud …`, prefer `--backend ssh:<target>` for the same target — you get the better cred-handling story automatically.

## Workspace config + `--backend` flag interaction

Recap of [Chapter 12 §"`exec:`"](./12-workspace-config.md):

The flag wins. If `~/.roksbnkctl/<ws>/config.yaml` says:

```yaml
exec:
  iperf3: { backend: k8s }
```

…and you run `roksbnkctl test throughput --backend local`, the `local` backend runs. The flag is the **per-invocation override**; the workspace config is the **per-workspace default**.

If neither is set, the per-tool default from the previous section applies (iperf3 → k8s, ibmcloud → local, terraform → local). The resolution order is exact:

1. `--backend` flag
2. `exec.<tool>.backend` in workspace config
3. Per-tool baked-in default

There's no fallback chain inside this resolution — if you pass `--backend k8s` and the cluster is unreachable, the backend errors with "cluster API unreachable" (exit `127`). It does **not** fall through to `local`. Silent fallback hides intent and produces confusing CI results; the failure-mode discipline in [Chapter 17 §"Backend-failure semantics"](./17-execution-backends.md#backend-failure-semantics) applies here too.

## Summary table

The decision-tree contents collapsed into one table:

| If you want to… | Backend | Notes |
|---|---|---|
| Measure cluster bandwidth | `k8s` | iperf3 client + server in cluster (the default) |
| Measure laptop-uplink-to-cluster bandwidth | `local` | deliberate; not the iperf3 default |
| GSLB DNS cross-vantage compare | `local` + `k8s` (`--gslb-compare`) | multiple vantages in parallel |
| `ibmcloud` from a customer-firewalled office | `ssh:bastion` | with `--bootstrap` if first call on fresh Ubuntu |
| Frozen-version CI for any tool | `docker` | image tag matches `roksbnkctl` release |
| Cluster-side ad-hoc `ibmcloud` debugging | `k8s` | requires `roksbnkctl ops install` first |
| Pre-cluster ibmcloud / terraform | `local` or `ssh` | `k8s` requires a working cluster |
| `terraform up` on a clean dev machine | `local` (default) or `docker` | k8s + ssh deferred |
| Air-gapped: laptop can't reach IBM Cloud, bastion can | `ssh:bastion` | with kubeconfig propagation |
| Just learning the tool | `local` | simplest mental model |

## Worked example: bare-metal + jumphost office workflow

End-to-end Part V scenario: you're an F5 SE running a customer POC from a corporate-firewalled office. The laptop can't reach `*.cloud.ibm.com` directly (the office proxy blocks it) but a customer-provisioned Ubuntu jumphost at `10.20.30.40` can. The jumphost was already auto-discovered by an earlier `roksbnkctl up` against this customer's account, so `targets list` shows it. You need to: install the in-cluster ops pod, run `ibmcloud` from the bastion, and run a throughput test from inside the cluster — all without installing tools locally.

```bash
# 1. Verify jumphost is registered + reachable
$ roksbnkctl targets list -w customer
NAME       HOST          KEY_SOURCE         STATUS
jumphost   10.20.30.40   workspace/state    reachable

# 2. Run ibmcloud from the jumphost (Sprint 1 --on flag, lightweight)
$ roksbnkctl ibmcloud --on jumphost ks cluster ls
OK
Name              ID                                     State    Created     ...
customer-cluster  c4abc123def456                         normal   3 days ago  ...

# 3. For the same call routed through the Backend interface (cred-handling
# hardened, redactor wired, opt-in apt-bootstrap available), use --backend
$ roksbnkctl ibmcloud --backend ssh:jumphost ks cluster ls
# Same output; different code path. Prefer --backend ssh:<target> for
# everything except quick interactive shells where --on is faster to type.

# 4. Install the in-cluster ops pod (one-time per cluster)
$ roksbnkctl ops install
✓ Namespace roksbnkctl-ops created
✓ ServiceAccount + Role + RoleBinding applied
✓ Secret roksbnkctl-ibm-creds applied (envFrom secretRef)
✓ Pod roksbnkctl-ops Running (2.3s)

# 5. Same ibmcloud call routed through the ops pod (k8s backend)
$ roksbnkctl ibmcloud --backend k8s iam oauth-tokens
IAM token: Bearer eyJ...
# (The token comes from inside the cluster — different egress IP from the
# jumphost's, useful when IAM policy is IP-conditional.)

# 6. Throughput test using the cluster vantage (default for iperf3)
$ roksbnkctl test throughput
→ Deploying iperf3 server pod into namespace "roksbnkctl-test"
✓ Pod ready (iperf3-server-...)
→ Deploying iperf3 client Job in the same namespace
✓ Client Job complete
✓ throughput: 8.92 Gbits/sec (mean over 10s)
→ Tearing down iperf3 fixture
✓ pod, service, and Job deleted

# 7. Throughput test from the jumphost into the cluster (north-south, real
# customer-network bandwidth — not laptop wifi)
$ roksbnkctl test throughput --backend ssh:jumphost --mode north-south
✓ throughput: 936 Mbits/sec  (jumphost → cluster LB; customer's WAN)

# 8. Persist the per-tool routing in workspace config (one-time)
$ cat >> ~/.roksbnkctl/customer/config.yaml <<'YAML'
exec:
  ibmcloud:  { backend: ssh:jumphost }
  iperf3:    { backend: k8s }
  terraform: { backend: local }
YAML
# Subsequent runs skip the --backend flag — every ibmcloud call routes via
# the jumphost automatically; every iperf3 runs in-cluster.
```

The point of this walkthrough: with no tools installed locally beyond `roksbnkctl` itself, you've reached the IBM Cloud control plane from the customer's bastion (compliance-correct egress), exercised the cluster fabric for throughput, and persisted the routing per workspace. The same laptop with the same `roksbnkctl` binary handles a different customer's POC by pointing at a different workspace; nothing on the laptop is workspace-specific.

[Chapter 19](./19-in-cluster-ops-pod.md) covers the ops-pod lifecycle in detail; [Chapter 22](./22-throughput-testing.md) covers the north-south vs east-west modes.

## Cross-references

- [Chapter 12 — Workspace config](./12-workspace-config.md) — the `exec:` block schema.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) — how creds reach each backend.
- [Chapter 16 — The `--on` flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md) — the lightweight remote-exec predecessor to the SSH backend.
- [Chapter 17 — Execution backends](./17-execution-backends.md) — per-backend mechanics.
- [Chapter 19 — The in-cluster ops pod](./19-in-cluster-ops-pod.md) — the cluster-side prerequisite for `--backend k8s`.
- [Chapter 22 — Throughput testing](./22-throughput-testing.md) — iperf3-specific flags.
- [PRD 03 — pluggable execution backends](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md) — the design spec.
