# Throughput testing

`roksbnkctl test throughput` measures TCP bandwidth between an iperf3 client and an iperf3 server, with at least one side running adjacent to (or inside) the cluster so the number reflects something useful — cluster fabric, the inbound path through a LoadBalancer (the **iperf3 north-south** mode, default), the outbound path from a jumphost, or pod-to-pod (**east-west**).

The heavy lifting (server pod lifecycle, OpenShift SCC compliance, in-cluster client Job, log streaming) lives in [Chapter 17 §"K8s backend"](./17-execution-backends.md#k8s-backend). This chapter is the user-facing flag surface, the mode selection, and the output-interpretation guide.

## What the suite measures

Plain TCP throughput, plus jitter and retransmits, between two endpoints both running iperf3:

- The **server** runs in the cluster — a single bare Pod plus a Service, deployed in the `roksbnkctl-test` namespace. Service type is `ClusterIP` for east-west, `LoadBalancer` for north-south. See [Chapter 17 §"iperf3 server side"](./17-execution-backends.md#iperf3-server-side) for the manifest details.
- The **client** runs wherever you point the backend — by default in the cluster as a one-shot Job, alternatively on your laptop or on a registered SSH target.
- Output is iperf3's native `-J` JSON, parsed and surfaced as `roksbnkctl test throughput` JSON.

The suite is appropriate for "is the cluster fabric healthy", "is the BNK data path delivering the bandwidth I expect from outside", and "is this jumphost the bottleneck between my office and the cluster". It is not a precision benchmark — TCP throughput is sensitive to MTU, NIC offloads, kernel tunables, and the iperf3 server's own resource limits, none of which the suite tries to control.

## The two modes

Mode is selected by `--mode`. The default is `north-south`.

```bash
roksbnkctl test throughput --mode north-south   # default
roksbnkctl test throughput --mode east-west
```

### `--mode north-south`

Measures the **inbound path** from outside the cluster to a Pod inside it. The server's Service is a `LoadBalancer`, so the cluster provisions an external endpoint (an IBM Cloud LB on ROKS, an external IP / hostname on bare-metal k8s). The client connects to that endpoint.

Use cases:

- "Is the BNK ingress path delivering the bandwidth I expect"
- "Is my office Wi-Fi or my home connection the bottleneck"
- "Is the cluster's egress capacity what the cloud provider promised"

Combine with `--backend local` (run the client on your laptop) when you specifically want to measure the laptop-to-cluster path. Combine with `--backend ssh:<jumphost>` when you want a known-stable measurement vantage from a jumphost in a known IP block — useful when laptop Wi-Fi is suspect.

### `--mode east-west`

Measures the **intra-cluster fabric** — Pod-to-Pod or host-to-Pod. The server's Service is `ClusterIP`, reachable only from inside the cluster. The default `--backend k8s` runs the client adjacent to the server (a one-shot Job in the same namespace), so the number reflects the CNI's pod-to-pod throughput.

Use cases:

- "Is the cluster's network plugin healthy"
- "Are the worker nodes hitting the link rate the underlying fabric promises"
- "Has the BNK CIS deployment regressed cluster-internal throughput"

Today's east-west still allows `--backend local` (the client runs on the host and reaches the ClusterIP via NodePort-equivalent access if the kubeconfig is the same one a `kubectl port-forward` would use), but the number is a host-to-cluster-via-NodePort hybrid in that case rather than a true pod-to-pod measurement. True pod-to-pod east-west — both sides scheduled to specific pods, optionally pinned to different nodes via `--cross-node` — is the v1.x refinement; today the in-cluster Job client gets you most of the way there.

## Per-tool default backend

The default backend for `iperf3` is **`k8s`**. From the per-tool defaults table in [Chapter 18 §"Per-tool default backends"](./18-choosing-backend.md#per-tool-default-backends):

| Tool | Default backend | Why |
|---|---|---|
| `iperf3` | `k8s` | Throughput from a laptop's uplink isn't the cluster's bandwidth. Default to running adjacent to the cluster so the number reflects fabric, not Wi-Fi. |

The default holds whether or not you've set `exec.iperf3.backend` in workspace config. To override per-invocation:

```bash
roksbnkctl test throughput --backend local                  # client on laptop
roksbnkctl test throughput --backend ssh:jumphost           # client on jumphost
roksbnkctl test throughput --backend k8s                    # default; explicit
```

`--backend docker` is **rejected** by the throughput suite. A Docker container running locally has the same network identity as the host (default bridge networking), so the client's view of the network is identical to `--backend local`. The CLI errors at parse time:

```
$ roksbnkctl test throughput --backend docker
error: --backend docker isn't supported for iperf3 — docker shares the host
       network namespace by default and gives no network-locality benefit over
       local. Use --backend local or --backend k8s instead
```

[Chapter 18 §"Throughput testing"](./18-choosing-backend.md#i-want-to-measure-cluster-bandwidth) is the decision-tree row that walks through the (mode, backend) matrix.

When `local` or `ssh:<target>` makes sense:

- **`local`**: you're deliberately measuring laptop-to-cluster bandwidth (north-south from your seat). Typical use is debugging "the dashboard feels slow from my desk" — you want to confirm the office uplink, not the cluster fabric, is the bottleneck.
- **`ssh:<target>`**: you have a registered SSH target with a known IP (often a customer jumphost in a specific datacenter) and want a bandwidth measurement from that vantage. The SSH backend ensures `iperf3` is on the target (auto-installs via `apt` with `--bootstrap` on Ubuntu; see [Chapter 17 §"SSH backend"](./17-execution-backends.md#ssh-backend)).

## The bundled image and the `runAsNonRoot` constraint

The iperf3 server pod's `securityContext` is set to satisfy OpenShift's `restricted-v2` SCC:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  seccompProfile:
    type: RuntimeDefault
containers:
- name: iperf3
  securityContext:
    allowPrivilegeEscalation: false
    runAsNonRoot: true
    capabilities:
      drop: ["ALL"]
```

iperf3 listens on port 5201 (unprivileged) so root isn't needed. The bundled image at `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>` declares `USER 1000` in its Dockerfile, matching the pod's `runAsUser: 1000`.

Two things follow:

1. **Stock images that run as root will fail admission.** The default in workspace config is `networkstatic/iperf3:latest`, which runs as root. On OpenShift / on any cluster with `restricted-v2` PodSecurity admission, that image will fail with `forbidden: violates PodSecurity "restricted:v1.x"`. The fix is to switch to the bundled image:

   ```yaml
   # ~/.roksbnkctl/<workspace>/config.yaml
   test:
     throughput:
       image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:v0.9.0
   ```

   The bundled image is the `--backend k8s` default for the **client** Job regardless; the workspace override only affects the server pod's image. Keep them in sync to avoid version skew during a debug session.

2. **A custom workspace-overridden image must respect `runAsNonRoot`.** If you point `test.throughput.image` at your own iperf3 image, that image must not require root to start. iperf3 itself doesn't need privilege; if your image does, drop the `USER root` line and rebuild.

[Chapter 17 §"iperf3 server side"](./17-execution-backends.md#iperf3-server-side) goes deeper on the SCC story — what the four `securityContext` fields do, why each is required, and how to debug an admission failure.

## OpenShift SCC failure mode

If your throughput pod fails to start with one of:

- `Forbidden: violates PodSecurity "restricted:v1.x"`
- `unable to validate against any security context constraint: ... restricted-v2`
- `runAsNonRoot is required`

…then either the configured image runs as root (use the bundled `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>` image instead — set `test.throughput.image` in workspace config) or the cluster's PodSecurity admission is stricter than `restricted-v2` (the manifest the k8s backend builds satisfies `restricted-v2` but not `privileged`; if your cluster requires `privileged` for the test namespace, that's a cluster policy question outside the suite's control).

[Chapter 17 §"iperf3 server side"](./17-execution-backends.md#iperf3-server-side) is the canonical source for the manifest's `securityContext`. If you're hand-rolling an iperf3 image for the suite, it's the spec to match.

## Reading the output

Default output is human-readable on stderr; `-o json` switches to JSON on stdout.

### Human-readable

```
$ roksbnkctl test throughput
→ Deploying iperf3 fixture
→ Waiting for iperf3 server pod ready
→ Waiting for LoadBalancer endpoint (can take 30–90s on IBM Cloud)
✓ iperf3 endpoint: 169.45.91.10:5201
running throughput ...
  PASS  iperf3 north-south → 169.45.91.10:5201 (k8s)  3.41 Gbps received, 0% retransmits in 30s
throughput PASS (1/1 passed)
✓ iperf3 fixture removed
```

### JSON

iperf3's `-J` output is rich (sender, receiver, per-stream stats, CPU usage). The roksbnkctl wrapper preserves the iperf3 JSON in the probe's `detail` field so all of iperf3's data survives, while the suite-level shell follows the `roksbnkctl.v1` schema:

```bash
roksbnkctl test throughput -o json
```

```json
{
  "schema": "roksbnkctl.v1",
  "command": "test",
  "suite": "throughput",
  "timestamp": "2026-05-10T14:32:01Z",
  "duration_ms": 31420,
  "overall": "pass",
  "results": [
    {
      "suite": "throughput",
      "name": "iperf3 north-south → 169.45.91.10:5201 (k8s)",
      "status": "pass",
      "duration_ms": 30015,
      "detail": "{ ...full iperf3 -J JSON, including sum_received, sum_sent... }"
    }
  ]
}
```

The fields you'll most often want from the embedded iperf3 JSON, in order of usefulness:

| Field | What it tells you |
|---|---|
| `end.sum_received.bits_per_second` | The throughput number you should report. iperf3 measures both sender and receiver and the receiver number is the right one to quote — it accounts for retransmits and any path losses. |
| `end.sum_sent.bits_per_second` | Sender-side throughput. If sent ≫ received, packets were dropped on the path. If sent ≈ received, the path is healthy. |
| `end.sum_sent.retransmits` | TCP retransmits over the run. A handful is normal; double-digit-percent of streams indicates congestion or a bad NIC. |
| `end.streams[].sender.jitter_ms` | Per-stream jitter. Useful for diagnosing variable-latency paths. |
| `end.cpu_utilization_percent.host_total` | Whether the client CPU was the bottleneck. >80% suggests the iperf3 client maxed out CPU before the network did — increase the iperf3 server's stream count (a server-pod knob, not a roksbnkctl flag) to spread load, or run on a beefier client. |

Example interpretation:

```
sum_received: 3.41 Gbps     → headline number
sum_sent:     3.42 Gbps     → very close, healthy path
retransmits:  127           → normal-low
```

vs

```
sum_received: 1.21 Gbps     → headline number
sum_sent:     2.95 Gbps     → ≫ received; >50% of bytes lost or retransmitted
retransmits:  18743         → heavy
```

The second shape is what a saturated link or a flaky NIC looks like. The first is a healthy gigabit-class path.

## Tuning knobs in workspace config

```yaml
# ~/.roksbnkctl/<workspace>/config.yaml
test:
  throughput:
    image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:v0.9.0
    duration: 30        # iperf3 -t flag, seconds
    streams: 8          # iperf3 -P flag, parallel streams
    default_mode: north-south
```

The defaults (30s, 8 streams, north-south) are a reasonable starting point for "is the BNK data path healthy". For deeper diagnosis:

- Bump `duration` to 60-90s if the path is variable and you want a stable average.
- Bump `streams` to 16 or 32 if the path's bandwidth-delay product is high (long-haul links benefit from more parallelism).
- Drop `streams` to 1 if you're specifically testing single-flow throughput (e.g., reproducing a customer's "single-stream upload feels slow" complaint).

[Chapter 12 — Workspace config](./12-workspace-config.md#test) lists the full schema.

## Cleanup and `--keep`

By default the suite tears down the iperf3 server pod and Service after the client run completes. If a test fails and you want to poke at the fixture (kubectl exec into the server, hand-run `iperf3 -c` from a third location, etc.), pass `--keep`:

```bash
roksbnkctl test throughput --keep
# ... fixture stays up; debug to your heart's content ...
kubectl delete -n roksbnkctl-test pod/roksbnkctl-iperf3 svc/roksbnkctl-iperf3
```

The fixture is in the `roksbnkctl-test` namespace (same namespace the k8s backend uses for one-shot Jobs). It's a bare Pod plus a Service; nothing else lingers when you delete the two resources.

## Cross-references

- [Chapter 17 §"K8s backend"](./17-execution-backends.md#k8s-backend) — server-side mechanics (manifest, SCC, log streaming, exit-code extraction).
- [Chapter 17 §"iperf3 server side"](./17-execution-backends.md#iperf3-server-side) — the asymmetric server-pod-plus-client-Job shape.
- [Chapter 18 §"I want to measure cluster bandwidth"](./18-choosing-backend.md#i-want-to-measure-cluster-bandwidth) — the decision-tree entry that picks `(mode, backend)` for your scenario.
- [Chapter 12 §"test:"](./12-workspace-config.md#test) — workspace-config schema for `test.throughput.*`.
- [Chapter 20 — Connectivity testing](./20-connectivity-testing.md) — the simpler "does HTTP work" companion suite.
- [Chapter 21 — DNS testing for GSLB](./21-dns-testing-gslb.md) — the DNS validation companion.
- [PRD 03 §"iperf3"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#iperf3) — the design spec.
