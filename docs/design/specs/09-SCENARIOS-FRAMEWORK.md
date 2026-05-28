# PRD 09 — Scenarios framework + `awsbnkctl test traffic`

> **Status:** stable. Operator-facing surface for end-to-end BNK validation. Specifies the scenarios framework alongside the `awsbnkctl bnk resync` primitive (slice-11).

## Why this PRD exists

awsbnkctl lacks a built-in assertion that "BNK is actually serving traffic". The lifecycle commands (`up`/`down`) check that pods are Ready, the License is Active, and the Gateway is Programmed=True — but they don't curl the VIP. Live testing surfaced the gap: every Phase 25 check passed and yet the VIP returned HTTP 500 because of a stale TMM pool member ([upstream issue draft](../upstream-issues/cne-controller-endpointslice-not-watched.md)).

We need an explicit, repeatable, reportable check: deploy a known-good workload, drive traffic through the data plane from an AWS-side vantage that exercises the real VIP (not a kube-proxy shortcut), assert the response, and emit a report that includes an ASCII rendering of the environment exercised.

## Prior art

The design adopts the shape of an established scenarios framework so operators see consistent vocabulary:

- One Go package per scenario under `internal/scenarios/<name>/`
- A scenario implements a `Scenario` interface — `Manifests() / Apply() / Verify() / Cleanup()`
- Each scenario carries a README, a `manifests/` directory, and a stable `Rating` (Green/Amber/Red) of what's testable in this target's shape
- Scenarios self-register in `init()` so the CLI auto-discovers them
- Reports written to `<workspace>/artifacts/scenarios/<name>/` (per-run manifests + logs) and aggregated at `<workspace>/reports/<stamp>/`
- Each scenario maps to one F5 how-to at `clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/`

Reference catalogue of scenarios (existing implementations available in prior tooling):

| Scenario | F5 how-to | Reference rating |
|---|---|---|
| `bgp-peer-frr` | BGP peering with FRR | Green |
| `http-routing-e2e` | HTTP traffic steering with Gateway API HTTPRoute | Green |
| `grpc-route` | gRPC routing | Green |
| `tcp-l4-lb` | TCP L4 load balancing | Green |
| `udp-l4-lb` | UDP L4 load balancing | Green |
| `proxy-protocol` | Proxy Protocol v2 | Green |
| `cluster-wide-watch` | Cluster-wide watch namespaces | Green |
| `ext-res-pool` | External resource pool | Green |
| `fic-dynamic-ip` | FIC dynamic IP | Amber |
| `ai-token-count` | AI token counter | Amber |
| `ai-sem-cache` | AI semantic cache | Amber |
| `core-files` | Core file collection (induced panic) | Red on kind |
| `cwc-admin-access` | CWC admin access | Amber |

Our AWS shape can reach most of these as **Green** (real EKS, real SR-IOV) while a few — anything that needs real DPUs or BGP peers we don't have — will be Amber or Red.

## What slice-11 ships (this branch)

A small foundation that the scenarios framework will build on:

- **`pkg/bnk.ResyncHTTPRoutes`** — Go-callable resync that scenarios will invoke before their HTTPRoute-dependent assertions. Workaround for the EndpointSlice-not-watched bug; will become a no-op once the upstream is fixed.
- **`awsbnkctl bnk resync`** — same primitive exposed as a CLI verb for ad-hoc operator use.
- This PRD + the [upstream issue draft](../upstream-issues/cne-controller-endpointslice-not-watched.md).

## What follow-up slices ship

### Slice-12: Phase N (jumphost-with-multi-ENI)

The scenarios that exercise the data plane need a vantage point that is **outside** the kube network namespace (so we exercise the real VIP, not a Service ClusterIP) but **inside** the BNK_EXT subnet (so the source IP matches what TMM SNAT expects).

Add a new lifecycle phase (after Phase 17 — secondary ENIs) that provisions:

- A `t3.small` EC2 with two ENIs:
  - Primary in the MGMT subnet (for AWS Systems Manager + EICE access)
  - Secondary in the BNK_EXT subnet (for traffic that lands on the data path VIP)
- An EC2 Instance Connect Endpoint in the MGMT subnet (so SSH works even from corporate networks that drop AWS 3.x.x.x traffic — validated in testing)
- A security group that allows: SSH from EICE only (no public ingress) + outbound to anywhere in the VPC
- IAM instance profile with `ec2:DescribeInstances` for the jumphost's own metadata

State is written to `state.env` under the `JUMPHOST_*` prefix so `down` can find and destroy it.

This is intentionally a phase, not part of `test traffic`, so the EC2 cost amortises across many scenario runs (the jumphost lives for the cluster's lifetime, not per-test).

Dependencies: requires Phase 17's ENI/SG plumbing.

### Slice-13: Scenarios framework + `awsbnkctl scenarios` + `http-routing-e2e`

The user-facing entry point.

```
awsbnkctl scenarios list                  # show registered scenarios + Rating
awsbnkctl scenarios run <name>            # run one
awsbnkctl scenarios run --all             # run every Green scenario topo-sorted
awsbnkctl scenarios clean <name>          # cleanup
awsbnkctl test traffic                    # alias for `scenarios run http-routing-e2e`
```

Internal layout:

```
internal/scenarios/
  scenario.go                    # Scenario interface + Registry
  runner.go                      # cobra subcommand wiring
  reporter.go                    # JSON + text + ASCII-env rendering
  envdiagram.go                  # ASCII env diagram of cluster + jumphost
  httproutee2e/
    README.md
    scenario.go                  # implements the http-routing-e2e scenario
    manifests/
      01-namespace.yaml
      02-gateway.yaml
      03-httproute.yaml
      04-nginx.yaml
```

Each scenario writes `Result` JSON + assertion list per the framework shape; the framework prepends an ASCII env diagram to each report.

#### `http-routing-e2e` shape (ported, AWS-adapted)

1. `Manifests`: render namespace, Gateway (`gatewayClassName: <cluster>-gatewayclass`, `spec.addresses=[VIP]` from cluster.yaml), HTTPRoute → nginx Service, nginx Deployment.
2. `Apply`: apply manifests; wait for Gateway `Programmed=True`, HTTPRoute `Accepted=True` + `ResolvedRefs=True`, nginx Deployment Available.
3. `Verify`:
   - Call `pkg/bnk.ResyncHTTPRoutes` against the namespace (gentle, idempotent)
   - SSH to the jumphost via EICE
   - 5x `curl --interface <BNK_EXT-secondary-IP> http://<VIP>/`
   - Assertions: 5/5 HTTP 200, p95 latency under threshold, response body matches nginx marker
4. `Cleanup`: delete the namespace (cascades to HTTPRoute/Gateway/Deployment/Service).

#### ASCII env diagram (output in every report)

Every report includes a deterministic ASCII rendering of what was exercised. Example for `http-routing-e2e`:

```
my-cluster  (eks 1.30, ap-southeast-2)
└── node ip-10-0-1-177 (BNK eligible, SR-IOV)
    ├── f5-tmm pod (eth0=10.0.1.177)
    │   ├── ext-vlan  → ENI 10.0.10.240/24 → BNK_EXT subnet
    │   └── int-vlan  → ENI 10.0.20.240/24 → BNK_INT subnet
    └── nginx-tgm28 pod (10.0.1.76:80)  ← HTTPRoute backend

Gateway nginx-gw → VIP 10.0.10.100 (BNK_EXT)
  └── HTTPRoute nginx-route
        └── Service nginx → pod 10.0.1.76:80

jumphost  i-0387e9d852da361e7  (t3.small)
  ├── primary ENI 10.0.1.128 (MGMT) ← EICE ingress
  └── secondary ENI 10.0.10.202 (BNK_EXT) ← test traffic source

Path exercised:
  jumphost.10.0.10.202  →  TMM listener 10.0.10.100:80  →  TMM SNAT 10.0.10.240
                       →  pod 10.0.1.76:80              →  reverse

Scenario: http-routing-e2e   Rating: 🟢   Result: ✓ 5/5 HTTP 200 (p95=9ms)
```

The renderer reads:
- `cluster.yaml` (cluster name, region, VIP, subnets)
- `state.env` (jumphost instance ID + ENI IPs + EICE endpoint)
- live cluster state (TMM pod + node + Gateway + HTTPRoute backend Service Endpoints)

ASCII output goes to stderr by default + included verbatim in `reports/<stamp>/<name>/env-diagram.txt`. JSON output (`-o json`) embeds it as a field.

## Acceptance criteria (slice-11 only)

- [x] `docs/upstream-issues/cne-controller-endpointslice-not-watched.md` exists with reproduction + diagnostic + suggested fix
- [x] This PRD exists, names slice-12 + slice-13, documents the reference scenario catalogue to implement, and specifies the ASCII env diagram contract
- [ ] `pkg/bnk.ResyncHTTPRoutes` + `awsbnkctl bnk resync` ship per `.agent/tasks/active/slice-11b-bnk-resync/TASK.md`
- [ ] Live verification: `awsbnkctl bnk resync nginx-route -n f5-cne-system` flips a stale pool back to current within 5s

## Acceptance criteria (follow-up slices)

### Slice-12

- New phase in `up`/`down` provisions/destroys a t3.small jumphost with primary MGMT ENI + secondary BNK_EXT ENI + EICE endpoint
- `state.env` carries `JUMPHOST_INSTANCE_ID`, `JUMPHOST_MGMT_ENI_IP`, `JUMPHOST_BNK_EXT_ENI_IP`, `JUMPHOST_EICE_ID`, `JUMPHOST_SG_ID`
- Idempotent across re-runs; `down` cleanly removes the jumphost
- Skippable via `cluster.yaml` `testing.jumphost.enabled: false`

### Slice-13

- `awsbnkctl scenarios list` lists Green/Amber/Red catalogue
- `awsbnkctl scenarios run http-routing-e2e` returns 0 against a freshly-built cluster with assertions all passing
- `awsbnkctl test traffic` is an alias that maps to `scenarios run http-routing-e2e`
- Report includes ASCII env diagram per the contract above
- JSON output schema: `awsbnkctl.scenario.v1`

## Out of scope

- BGP scenarios (`bgp-peer-frr`) — needs an FRR sibling pod or external peer; defer to a later slice once we decide which side hosts the BGP control plane
- Multi-region / GSLB scenarios
- Modifying the F5 cne-controller binary
- Replacing the host-device pattern with a sidecar pattern
- Performance benchmarking (handled by `awsbnkctl test throughput`, not the scenarios runner)
