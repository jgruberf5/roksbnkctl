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
