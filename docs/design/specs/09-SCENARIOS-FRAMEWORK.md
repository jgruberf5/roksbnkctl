# Scenarios framework + `awsbnkctl test traffic`

The scenarios framework is the operator-facing surface for end-to-end BNK
validation. It deploys a known-good workload, drives real traffic through the
data plane, asserts the response, and emits a report with an ASCII rendering of
the environment it exercised.

## Overview

The lifecycle commands (`up` / `down`) check that pods are Ready, the License is
Active, and the Gateway is `Programmed=True` — but they don't curl the VIP. That
leaves a gap: those control-plane checks can all pass while the VIP still returns
HTTP 500 because of a stale TMM pool member
([upstream issue](../../upstream-issues/cne-controller-endpointslice-not-watched.md)).

A scenario closes that gap with an explicit, repeatable, reportable check: it
deploys a known-good workload, drives traffic from an AWS-side vantage point that
exercises the real VIP (not a kube-proxy shortcut), asserts the response, and
writes a report. Each scenario maps to one F5 how-to at
`clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/`, so operators see
consistent vocabulary.

## Using it

```
awsbnkctl scenarios list                       # show registered scenarios + Rating
awsbnkctl scenarios run <name> -f cluster.yaml # run one scenario
awsbnkctl scenarios run --all  -f cluster.yaml # run every scenario, topo-sorted
awsbnkctl scenarios clean <name> -f cluster.yaml
awsbnkctl test traffic                         # alias for the http-routing-e2e scenario
```

Flags on `scenarios run`:

- `-f, --config` — path to `cluster.yaml` (required).
- `--vip` — Gateway VIP to use (default: derived from `cluster.yaml`).
- `--dry-run` — render manifests only; do not apply or verify.
- `--all` — run every registered scenario in topo-sorted dependency order.

`scenarios run --all` stops at the first failing scenario and returns a non-zero
exit code; otherwise it reports all scenarios green.

## How it works

Each scenario is a Go package under `internal/scenarios/<name>/` that implements
the `Scenario` interface — `Manifests()` / `Apply()` / `Verify()` / `Cleanup()` —
and self-registers in `init()` so the CLI auto-discovers it. A scenario carries a
README, a `manifests/` directory, and a stable `Rating` (Green / Amber / Red)
describing what's testable in this deployment's shape.

`scenarios run` drives one scenario through a fixed pipeline:

1. **Render** — render the scenario's manifests into
   `<workspace>/artifacts/scenarios/<name>/`.
2. **Apply** — apply the manifests via server-side apply with a live RESTMapper,
   then wait for the control-plane conditions the scenario needs (for example
   Gateway `Programmed=True`, HTTPRoute `Accepted=True` + `ResolvedRefs=True`,
   backend Deployment Available).
3. **Verify** — call `pkg/bnk.ResyncHTTPRoutes` against the scenario's namespace
   (gentle, idempotent), then SSH to the jumphost over EICE and drive traffic at
   the real VIP, asserting the response.
4. **Report** — emit a JSON report plus an ASCII environment diagram.
5. **Clean** — `scenarios clean` invokes the scenario's `Cleanup` hook, which
   deletes the scenario namespace (cascading to its HTTPRoute / Gateway /
   Deployment / Service).

Reports are written under `<workspace>/artifacts/scenarios/<name>/` (per-run
manifests + logs) and aggregated at `<workspace>/reports/<stamp>/`.

### The `http-routing-e2e` scenario

The canonical scenario, and the target of `awsbnkctl test traffic`:

1. `Manifests`: render a namespace, a Gateway
   (`gatewayClassName: <cluster>-gatewayclass`, `spec.addresses=[VIP]` from
   `cluster.yaml`), an HTTPRoute pointing at an nginx Service, and the nginx
   Deployment.
2. `Apply`: apply the manifests; wait for Gateway `Programmed=True`, HTTPRoute
   `Accepted=True` + `ResolvedRefs=True`, and the nginx Deployment Available.
3. `Verify`: resync the HTTPRoute, SSH to the jumphost via EICE, and run
   `5x curl --interface <BNK_EXT-secondary-IP> http://<VIP>/`. Assertions: 5/5
   HTTP 200, p95 latency under threshold, and a response body that matches the
   nginx marker.
4. `Cleanup`: delete the namespace.

### ASCII environment diagram

Every report includes a deterministic ASCII rendering of what was exercised.
Example for `http-routing-e2e`:

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

- `cluster.yaml` (cluster name, region, VIP, subnets),
- `state.env` (jumphost instance ID + ENI IPs + EICE endpoint),
- live cluster state (TMM pod + node + Gateway + HTTPRoute backend Service
  Endpoints).

The diagram is printed to stderr and included verbatim in the report.

## Scenario catalogue

The registered scenarios and their ratings:

| Scenario | What it exercises | Rating |
|---|---|---|
| `http-routing-e2e` | HTTP traffic steering with Gateway API HTTPRoute | Green |
| `http-traffic-split` | Weighted HTTP traffic split across backends | Green |
| `external-resource-pool` | External resource pool | Green |
| `proxy-protocol-l4` | Proxy Protocol v2 over L4 | Green |
| `multi-vip` | Multiple VIPs on one cluster | Green |
| `ai-token-counting` | AI token counter | Amber |
| `ai-semantic-cache` | AI semantic cache | Amber |
| `egress-snat` | Egress SNAT | Amber |

The Green scenarios run cleanly against the real EKS / SR-IOV deployment. The
Amber scenarios depend on capabilities the data plane doesn't fully program in
this shape, so they're carried as known-partial.

## Operational notes

- **Jumphost requirement.** The data-plane verification needs a vantage point
  that is *outside* the kube network namespace (so it exercises the real VIP, not
  a Service ClusterIP) but *inside* the BNK_EXT subnet (so the source IP matches
  what TMM SNAT expects). `up` provisions this jumphost when
  `testing.jumphost.enabled` — a small EC2 with a primary ENI in the MGMT subnet
  (for AWS Systems Manager + EICE access) and a secondary ENI in the BNK_EXT
  subnet (for traffic that lands on the data-path VIP), an EC2 Instance Connect
  Endpoint so SSH works even from networks that drop AWS `3.x.x.x` traffic, and a
  security group that allows SSH from EICE only with no public ingress. The
  jumphost lives for the cluster's lifetime, so its cost amortizes across many
  scenario runs. State is recorded under the `JUMPHOST_*` keys in `state.env` so
  `down` can find and destroy it.

- **Pool-member staleness → auto-resync.** The cne-controller resolves HTTPRoute
  pool members only at HTTPRoute reconcile, not on EndpointSlice change, so a
  rescheduled backend pod can leave a stale pool member that returns HTTP 500.
  Each scenario calls `pkg/bnk.ResyncHTTPRoutes` before its HTTPRoute-dependent
  assertions to flip a stale pool back to current. The same primitive is exposed
  for ad-hoc use as `awsbnkctl bnk resync`. This becomes a no-op once the upstream
  is fixed — see the
  [upstream issue](../../upstream-issues/cne-controller-endpointslice-not-watched.md).

- **Server-side apply only.** Scenarios apply manifests via SSA with a live
  RESTMapper, never via raw YAML apply, so re-runs converge instead of
  conflicting.

## Related

- [Demo experience](10-DEMO-EXPERIENCE.md) — `up --demo`, the launch renderer,
  and `demo run` build on this framework to *present* BNK to an audience.
