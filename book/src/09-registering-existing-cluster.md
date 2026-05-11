# Registering an existing cluster

`roksbnkctl cluster register <name>` wires `roksbnkctl` up to a ROKS cluster that already exists in your IBM Cloud account — one you didn't provision via [`cluster up`](./08-cluster-phase.md). After a successful register, the workspace behaves exactly as if you'd done `cluster up`: `roksbnkctl up` deploys BNK trials onto the registered cluster, `roksbnkctl down` tears those trials down, `roksbnkctl status` reports the cluster's identity, and so on.

This chapter covers when registration is the right answer, what input is required vs auto-discovered, the COS naming convention, the `cluster-outputs.json` write, and a worked example.

## When to use this

`cluster register` is the answer when **all** of these are true:

- A ROKS cluster already exists in the IBM Cloud account.
- You have IAM access to the cluster's VPC + container service.
- You want `roksbnkctl` to deploy BNK trials onto that cluster.
- You don't want `roksbnkctl` to own the cluster's lifecycle (it shouldn't be `terraform destroy`-able from your workstation).

Common scenarios:

1. **Your team operates the ROKS cluster centrally.** A platform team provisioned the cluster via their own Terraform / Pulumi / IBM Cloud Schematics; you just want to deploy BNK trials onto it. Register it; deploy trials; tear them back down. The cluster itself stays under the platform team's ownership.

2. **You're attaching to an existing demo cluster.** A workshop hosts a shared cluster that participants attach to. Each participant registers it in their own workspace and deploys their own trial — trials are isolated by namespace under the same cluster.

3. **You provisioned the cluster manually for testing.** You created a one-off cluster via `ibmcloud ks cluster create vpc-gen2 ...` and want to move forward with `roksbnkctl` rather than re-creating it.

If none of those apply — i.e. you want `roksbnkctl` to own cluster lifecycle end-to-end — use `cluster up` instead. Register and `cluster up` are mutually exclusive per workspace; the second one wins.

## Required input vs auto-discovery

`cluster register` takes one positional argument (the cluster name or ID) and one optional flag (`--registry-cos-name`).

```bash
roksbnkctl cluster register <cluster-name-or-id> [--registry-cos-name <cos-instance-name>]
```

Everything else is **auto-discovered** via the IBM SDK:

| Field | Source |
|---|---|
| `cluster_id` | `ibmcloud ks cluster get <name>` (resolved by name → ID) |
| `region` | from the cluster lookup |
| `resource_group_id` | from the cluster lookup |
| `vpc_id` | from the cluster's `provider.vpcs[0].id` |
| `master_url` | from the cluster lookup |
| `openshift_version` | from the cluster's `masterKubeVersion` |
| `registry_cos_crn` | discovered via the registry COS instance lookup (see below) |

The cluster lookup goes through the same container-service endpoint `ibmcloud ks cluster get` uses — no host `ibmcloud` install required. If the named cluster doesn't exist in the account, the call returns a clear `no cluster named <foo>` error rather than a 404 stack trace.

A **vpc-gen2** cluster is required. Classic infrastructure clusters return successfully but their `vpc_id` is empty, and `cluster register` refuses to write a record without one:

```
Error: cluster "old-classic" has no VPC — roksbnkctl only supports vpc-gen2 clusters
```

## The COS naming convention

`roksbnkctl up` needs a Cloud Object Storage instance to act as the registry for FAR images, JWT licenses, and schematic state. `cluster register` verifies that this COS instance exists at registration time so a later `up` doesn't fail mid-apply with a missing-instance error.

### Default convention

The bundled HCL falls back to **`<cluster-name>-cos`** if the user's tfvars don't override `roks_cos_instance_name`. So `cluster register` defaults to looking up `<cluster-name>-cos`:

```bash
# Cluster name: "canada-roks" → expects COS instance "canada-roks-cos"
roksbnkctl cluster register canada-roks
```

### Override with `--registry-cos-name`

If your team set `roks_cos_instance_name` to something else in their tfvars (or named the COS instance via the IBM Cloud console with a different convention), pass `--registry-cos-name <name>`:

```bash
roksbnkctl cluster register canada-roks \
  --registry-cos-name canada-roks-bnk-registry
```

The instance name is **case-sensitive** and must match exactly — `Canada-ROKS-COS` and `canada-roks-cos` are different instances.

### What if the COS doesn't exist yet?

`cluster register` errors out:

```
Error: registry COS instance "canada-roks-cos" not found in account: ...
  Either run `roksbnkctl cluster up` to create it, or pass --registry-cos-name <name>
  if your tfvars uses a different roks_cos_instance_name
```

You have two options:

1. **Create the COS instance** in the IBM Cloud console with the conventional name (`<cluster>-cos`), then re-run register. The instance can be empty — `roksbnkctl up` will populate it with the bucket structure it needs on its first apply.

2. **Use a different name** that already exists in the account, via `--registry-cos-name <name>`.

Either way, `cluster register` won't write `cluster-outputs.json` until both the cluster and its registry COS instance exist.

## The `cluster-outputs.json` write

On success, `cluster register` writes `~/.roksbnkctl/<workspace>/cluster-outputs.json` — the same file `cluster up` writes. The contents look identical except for one field:

```json
{
  "cluster_name": "canada-roks",
  "cluster_id": "cre6h4l20jjsg4kvt3a0",
  "region": "ca-tor",
  "resource_group_id": "abc123...",
  "vpc_id": "r038-...",
  "registry_cos_crn": "crn:v1:bluemix:public:cloud-object-storage:global:a/...",
  "registry_cos_name": "canada-roks-cos",
  "master_url": "https://c106.ca-tor.containers.cloud.ibm.com:31415",
  "openshift_version": "4.14_openshift",
  "source": "cluster-register",
  "recorded_at": "2026-05-08T14:22:08Z"
}
```

The `source` field is `cluster-register` (vs `cluster-up` for self-provisioned clusters). Downstream commands that care about provenance — for example, a future `roksbnkctl cluster down` would refuse to destroy a `cluster-register`-sourced cluster — read this field. Subnet IDs (`subnet_ids`) and transit gateway ID (`transit_gateway_id`) are left blank for registered clusters; the bundled HCL doesn't need them when `roksbnkctl up` runs against a pre-existing cluster.

## Worked example: register canada-roks

The full flow for attaching to a hypothetical `canada-roks` cluster.

### Step 1 — create or pick a workspace

```bash
roksbnkctl ws new canada
roksbnkctl init -w canada
# (interactive — fill in region as ca-tor; cluster.name = canada-roks)
```

You can also run `cluster register` against the current workspace; the `-w` is just for clarity.

### Step 2 — `cluster register`

```bash
roksbnkctl -w canada cluster register canada-roks
```

Sample output:

```
→ Looking up cluster "canada-roks"
✓ Cluster canada-roks (cre6h4l20jjsg4kvt3a0) — state: normal, masters: 4.14_openshift
✓ VPC r038-... (resource group prod-rg)
→ Verifying registry COS instance "canada-roks-cos"
✓ COS instance canada-roks-cos (abc-123-def-...)
✓ Wrote ~/.roksbnkctl/canada/cluster-outputs.json
```

If the COS naming was non-conventional:

```bash
roksbnkctl -w canada cluster register canada-roks \
  --registry-cos-name canada-bnk-registry
```

### Step 3 — verify with `cluster show`

```bash
roksbnkctl -w canada cluster show
workspace:        canada
source:           cluster-register
recorded_at:      2026-05-08T14:22:08Z

cluster_name:     canada-roks
cluster_id:       cre6h4l20jjsg4kvt3a0
region:           ca-tor
resource_group:   abc123...
openshift:        4.14_openshift
master_url:       https://c106.ca-tor.containers.cloud.ibm.com:31415

vpc_id:           r038-...
registry_cos:     canada-roks-cos
registry_cos_crn: crn:v1:bluemix:public:cloud-object-storage:global:a/...
```

### Step 4 — fetch the kubeconfig

`cluster register` does **not** automatically download the kubeconfig — it's a metadata-only operation. Grab it explicitly:

```bash
roksbnkctl -w canada kubeconfig --download
# → Fetching admin kubeconfig for "canada-roks"
# ✓ Wrote /home/you/.kube/config (12345 bytes)
```

### Step 5 — use the cluster as if you'd done `cluster up`

From here, the workflow is identical to a self-provisioned cluster:

```bash
# Verify reachability
roksbnkctl -w canada k get nodes

# Deploy a BNK trial onto it
roksbnkctl -w canada up --auto

# Tear the trial back down (cluster survives)
roksbnkctl -w canada down --auto
```

`roksbnkctl up` reads `cluster-outputs.json` and uses the cluster identity directly — no need to re-state cluster name/region/RG in the trial's tfvars.

## When register isn't enough

Some scenarios where `cluster register` won't get you over the line:

- **The cluster is in a different IBM Cloud account.** API keys are account-scoped; you'd need a key for the cluster's account. `cluster register` doesn't cross account boundaries.
- **The cluster is private (no public master endpoint).** `roksbnkctl up` needs to apply Helm charts and Kubernetes manifests against the master. If the master is only reachable from inside a VPN, route the apply through `--on jumphost` (Sprint 1) or wait for the SSH execution backend in Sprint 4.
- **The cluster is a classic-infrastructure ROKS** (not vpc-gen2). Registration refuses; classic clusters aren't supported.
- **The cluster's worker pool is too small.** BNK trials need at least 2 workers with adequate CPU/memory. The upstream HCL provisions appropriately-sized workers; an existing cluster might not.

For the first three, the cluster simply isn't a candidate. For the last one, the apply may run but `flo` / `cne_instance` will fail to schedule — scale the worker pool first.

## Re-registering and unregistering

To **re-register** with new data (e.g. you renamed the COS instance, or the master URL changed), just run `cluster register` again — it overwrites `cluster-outputs.json` in place.

To **unregister** without destroying anything, delete the file directly:

```bash
rm ~/.roksbnkctl/canada/cluster-outputs.json
```

The workspace's `config.yaml` and `state/` survive; only the cluster identity record is removed. The next `roksbnkctl up` will fail with `workspace has no cluster-outputs.json` until you either re-register or run `cluster up`.

There's deliberately **no** `roksbnkctl cluster unregister` command. Deleting the JSON is a single-file operation that doesn't deserve its own subcommand, and the absence of one nudges users toward "destroy the trial first, then deal with the cluster identity" rather than "unregister without thinking about the consequences".

## Cross-references

- [Chapter 8 — The cluster phase](./08-cluster-phase.md) — the alternative when you want `roksbnkctl` to provision the cluster.
- [Chapter 10 — Deploying BNK trials](./10-deploying-bnk-trials.md) — what `roksbnkctl up` does on top of a registered (or `cluster up`'d) cluster.
- [Chapter 25 — COS supply chain management](./25-cos-supply-chain.md) — the COS instance and bucket layout that `--registry-cos-name` points at.
