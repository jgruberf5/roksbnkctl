# demo — full BNK-on-EKS demo deployment

The `demo` topology provisions a complete BNK 2.3 cluster (host-device data
path) plus a multi-ENI jumphost, marked as a **demo** so the curated protocol
use-cases can drive test traffic from inside the BNK external subnet.

It is the `examples/full-cluster` topology with `demo.enabled: true` — equivalent
to running `awsbnkctl up --demo`. Demo mode writes `DEMO_MODE`/`DEMO_EXPIRY` to
state, tags every resource with `awsbnkctl:demo=true`, pre-stages the demo test
clients on the jumphost, and makes `down` clean the demo use-cases first.

## Prerequisites

- An AWS account with credentials configured (SSO or static keys).
- `awsbnkctl` built locally (`go build -o awsbnkctl ./cmd/awsbnkctl`).
- Your F5 supply-chain files (see the `bnk:` block in `cluster.yaml`):
  - FAR pull credentials JSON → referenced as `./cne_pull_64.json`
  - subscription JWT → referenced as `./license.jwt`
  These are gitignored, so they are never committed. Update the two paths in
  `cluster.yaml` if your files live elsewhere.
- Quota for: 1 VPC, 1 IGW, 1 NAT GW, 1 EIP, 6 subnets, an EKS cluster, a 3-node
  `m6i.4xlarge` managed node group, and a `t3.small` jumphost.

## Steps

**1. Authenticate**

```bash
aws sso login --profile <your-profile>
aws sts get-caller-identity     # sanity check
```

**2. Validate the intent (no AWS calls)**

```bash
awsbnkctl validate examples/demo/cluster.yaml
```

**3. Dry-run the plan (requires AWS creds; no mutations)**

```bash
awsbnkctl up --config examples/demo/cluster.yaml --dry-run
```

**4. Provision**

```bash
awsbnkctl up --config examples/demo/cluster.yaml
```

Each phase prints as it runs; state is written to `.awsbnkctl/bnk-demo/state.env`
after every successful phase, so a mid-run failure is safe to resume.

**5. Run the demos**

```bash
awsbnkctl demo list
awsbnkctl demo run http2 --config examples/demo/cluster.yaml
awsbnkctl demo run --all --config examples/demo/cluster.yaml
```

**6. Tear down**

```bash
awsbnkctl down --config examples/demo/cluster.yaml --yes
```

Reverse-order destroy. Demo use-cases are cleaned first, then the infrastructure.
Tolerates resources that are already gone (safe to re-run). If
`.awsbnkctl/bnk-demo/state.env` is missing, `down` falls back to tag-discovery
(`awsbnkctl:cluster=bnk-demo`).

## Migration demo scenarios

Beyond the protocol use-cases (`http2`, `grpc`, `diameter`, …), the demo ships
two scenarios that tell the **"migrate to BNK"** story — useful for a
side-by-side walkthrough and visible in forge. List everything with
`awsbnkctl demo list`.

### `ingress-migration` — old and new ingress, side by side

```bash
awsbnkctl demo run ingress-migration --config examples/demo/cluster.yaml
```

Installs **ingress-nginx**, **HAProxy**, and a **BNK Gateway API** route via
Helm, all fronting one shared `traefik/whoami` backend at the same time, so you
can compare the three traffic paths live before cutting over:

| Front-end | Host header                  | Reached via            |
|-----------|------------------------------|------------------------|
| nginx     | `web.nginx.migration.local`  | in-cluster ClusterIP   |
| HAProxy   | `web.haproxy.migration.local`| in-cluster ClusterIP   |
| BNK       | `web.bnk.migration.local`    | BNK VIP `10.0.10.113`  |

No extra configuration — it runs on the standard demo cluster. `Cleanup`
removes the namespace, so it is safe to re-run.

### `bigip-cis` — the traditional model BNK replaces

```bash
# 1. Enable the BIG-IP VE in cluster.yaml (uncomment the bigipVE: block, enabled: true)
# 2. Supply the BIG-IP admin password out-of-band (never stored in any file):
export AWSBNKCTL_BIGIP_PASSWORD='<choose-a-strong-password>'
# 3. Provision (launches + onboards the appliance) and run the scenario:
awsbnkctl up   --config examples/demo/cluster.yaml
awsbnkctl demo run bigip-cis --config examples/demo/cluster.yaml
```

Stands up an **external F5 BIG-IP VE** appliance fronted by an in-cluster
**CIS** controller (`k8s-bigip-ctlr`) that programs a `VirtualServer` custom
resource — the classic appliance-plus-controller model that BNK's
`cne-controller` collapses into the cluster. Routes on `web.cis.migration.local`
via BIG-IP VIP `10.0.10.120`.

> **Cost:** enabling `bigipVE` provisions a chargeable `c5n.2xlarge` PAYG BIG-IP
> VE (≈15 min extra onboarding, plus ongoing EC2 + licensing). `awsbnkctl down`
> tears it down with the rest of the cluster.

> **Known limitation (data-path routing).** The BIG-IP management subnet
> (`10.0.1.0/24`) overlaps the EKS node/pod subnet, so the `/24` data-plane
> route the onboarder would add is rejected. The proven workaround is a per-pod
> `/32` host route on the BIG-IP; until that route exists on a freshly
> provisioned cluster, the scenario's HTTP-200 data-path assertion can fail even
> though onboarding succeeded. The durable fix (placing the management ENI on a
> non-overlapping subnet) is planned.
