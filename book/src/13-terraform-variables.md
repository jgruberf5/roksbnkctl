# Terraform variables (terraform.tfvars)

> **Variable specs live in [Chapter 29 — Terraform variable reference](./29-terraform-variable-reference.md).**
> That chapter is generated from `terraform/variables.tf` and checked by a test.

`roksbnkctl` is a thin orchestration layer over a Terraform HCL bundle. The HCL has its own variables — well over 60 of them — declared in `terraform/variables.tf`. The workspace's `config.yaml` covers the common knobs; for the rest, you reach into `terraform.tfvars` directly.

This chapter is the surface for that lower layer: where the example file lives, how `roksbnkctl tfvars` bootstraps a starter, what `--var-file` does, the layering rule between `config.yaml`-derived tfvars and your overrides, and the one variable that **never** goes on disk (`ibmcloud_api_key`).

## Where the bundled HCL lives

The Terraform HCL is bundled into the `roksbnkctl` binary via `go:embed`. On first use of a workspace, it gets extracted to:

```
~/.roksbnkctl/<workspace>/state/tf-source/embedded-terraform/
├── main.tf
├── variables.tf
├── outputs.tf
├── providers.tf
├── versions.tf
├── terraform.tfvars.example
└── modules/
```

### The extracted tree is the base plus a per-release overlay

Extraction writes the base tree above, then layers `terraform/lines/<line>/` on
top of it, where `<line>` is the BNK release derived from `bnk.manifest_version`
(`2.3.0-3.2598.3-0.0.170` → `2.3`). Same relative path replaces, new path is
added, nothing is removed — so an overlay can only add to or replace the base,
never quietly delete a resource it declares.

**No supported release needs one today, so `lines/` ships empty and the extracted
tree is byte-identical to the base.** It is documented here anyway, because the
consequence is not obvious and will not announce itself: the extracted tree is no
longer a function of the binary alone, but of the binary *and the workspace's
manifest version*. If you are ever comparing your extracted `main.tf` against the
repo's and they differ, that is the first thing to check.

Why this rather than a branch per BNK release — which was tried and withdrawn —
is in [`terraform/lines/README.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/terraform/lines/README.md).
The short version: a branch forks the whole tool to express a difference that
lives in a handful of `.tf` files.

That `terraform.tfvars.example` file is the canonical reference for what's tunable — every variable with a sensible starter value, grouped by module (ROKS cluster, cert-manager, FLO, CNEInstance, License, testing). `terraform/variables.tf` (linked at the [GitHub canonical URL](https://github.com/jgruberf5/roksbnkctl/blob/main/terraform/variables.tf)) is the formal declaration with types, descriptions, and defaults.

You don't edit the example file in place. Copy or generate from it instead.

## `roksbnkctl tfvars` — bootstrap a starter

The `roksbnkctl tfvars` subcommand prints a starter `terraform.tfvars` to stdout, populated from the **current workspace state**:

```bash
$ roksbnkctl tfvars > ~/.roksbnkctl/dev/terraform.tfvars.user
```

What gets pre-filled:

- Every field from `config.yaml` that maps to a tfvar (cluster name, region, workers, BNK fields, COS fields)
- `cluster_network_mode` — rendered **unconditionally**, unlike the settings around it. Most are omitted when unset so the HCL default stands; this one decides how a cluster is *built*, and a value that means two different things depending on which side you read it from is worth avoiding. Unset renders `"single-nic"`, which is today's behaviour exactly.
- The cluster's identity from `cluster-outputs.json` if `cluster up` has already run
- A commented-out section for the variables you might want to tune next (jumphost profile, GSLB datacenter, license mode)

What's deliberately **excluded**:

- `ibmcloud_api_key` — never on disk (see "The IBMCLOUD_API_KEY exception" below)
- Sensitive outputs (BIG-IP passwords, COS HMAC secrets) — left as upstream defaults

The starter is meant to be copied into `~/.roksbnkctl/<ws>/terraform.tfvars.user` (the workspace-local override file) or into a `--var-file` path you keep alongside the workspace.

## What you typically edit

The variables that matter for day-to-day BNK trial work, ordered by likely-to-touch:

| Variable | Default | What it controls |
|---|---|---|
| `openshift_cluster_name` | `tf-openshift-cluster` | Cluster name. Mirrors `config.yaml`'s `cluster.name`. |
| `roks_workers_per_zone` | `1` | Worker nodes per AZ. `2` ⇒ 6 workers in a 3-AZ MZR region. |
| `create_roks_cluster` | `true` | Set `false` to adopt an existing cluster. Pair with `roks_cluster_id_or_name`. |
| `cluster_public_gateway` | `true` | `false` = private/disconnected cluster (no public gateways, no worker egress). `config.yaml`'s `cluster.public_gateway`. Expert — needs private connectivity you provide. |
| `openshift_cluster_version` | `"4.20"` | OpenShift minor. Quote it — YAML/HCL parses `4.20` as float otherwise. |
| `cneinstance_deployment_size` | `Small` | `Small`/`Medium`/`Large`. CNEInstance sizing. |
| `f5_bigip_k8s_manifest_version` | upstream pin | Pin a specific BNK manifest chart version. |
| `far_repo_url` | `repo.f5.com` | FAR Docker/Helm registry. Override for a non-production FAR (an EA repo) or a staging mirror. Reaches **every** consumer including the standalone FLP VSI, whose chart pulls used to spell the host as a literal and silently ignored this. |
| `flo_namespace` | `f5-bnk` | Where the F5 Lifecycle Operator runs. |
| `testing_create_tgw_jumphost` | `true` | Create the testing jumphost in a client VPC over Transit Gateway. |
| `testing_ssh_key_name` | `""` (must set) | Existing IBM Cloud SSH key name for jumphost provisioning. |
| `flo_trusted_profile_sa_name` / `flo_trusted_profile_roles` | the CNE controller's IBM Cloud identity — modelled as `bnk.trusted_profile.*` |
| `cneinstance_gtm_url` / `_username` / `_password` | the BIG-IP DNS the GSLB datacenter registers with — modelled as `bnk.gtm.*` |
| `cneinstance_vlan_prefixlen_external` / `_internal` | per-VLAN self-IP masks — modelled as `bnk.network.vlan_prefixlen_{external,internal}` |
| `cneinstance_gslb_datacenter_name` | `""` | Set when wiring BNK into an F5 BIG-IP GSLB datacenter. |
| `cneinstance_network_zones` | `[]` (install-guide defaults) | Per-zone VLAN/SNAT/VIP subnets + TMM self-IPs. Mirrors `config.yaml`'s `bnk.network.zones`. Supply all three zones or none. |
| `cneinstance_vlan_prefixlen` | `24` | TMM self-IP prefix length (F5SPKVlan `spec.prefixlen_v4`). `bnk.network.vlan_prefixlen`. Match your VLAN CIDRs. |
| `cneinstance_tmm_k8s_routes` | `172.17.0.0/18` | Pod CIDR TMM routes to (`TMM_K8S_ROUTES`). `bnk.network.tmm_k8s_routes`. Set to your cluster's pod subnet if non-default. |
| `license_mode` | `connected` | `connected` \| `disconnected`. |

For the full list with types and per-field descriptions, see `terraform/variables.tf` directly — link [here](https://github.com/jgruberf5/roksbnkctl/blob/main/terraform/variables.tf) — or the auto-generated [Chapter 29 — Terraform variable reference](./29-terraform-variable-reference.md).

## Resource naming & collision avoidance

Every account-scoped IBM Cloud resource name is derived from a single **workspace prefix**. Without per-workspace naming, two workspaces creating infrastructure in the same IBM Cloud account would collide at the account level — the second `up` would hit `Provided Name … is not unique` / `gateway with the same name already exists`. The prefix avoids this: `roksbnkctl` derives the full name set from it, validates each name against its resource type's length/charset limit at `init` time, and renders a complete `terraform.tfvars` that names every resource explicitly.

### Prefix → name derivation

Give a workspace the prefix `acme-eu`, and `roksbnkctl` generates this name set:

| Resource | tfvars variable | Derived name (`prefix = acme-eu`) | Suffix |
|---|---|---|---|
| ROKS/OpenShift cluster | `openshift_cluster_name` | `acme-eu` | *(none — cluster name **is** the prefix)* |
| Cluster VPC | `roks_cluster_vpc_name` | `acme-eu-cluster-vpc` | `-cluster-vpc` |
| Registry COS instance | `roks_cos_instance_name` | `acme-eu-registry-cos` | `-registry-cos` |
| Transit Gateway | `roks_transit_gateway_name` | `acme-eu-tgw` | `-tgw` |
| Client VPC (TGW jumphost) | `testing_client_vpc_name` | `acme-eu-client-vpc` | `-client-vpc` |
| TGW jumphost | `testing_tgw_jumphost_name` | `acme-eu-jh-tgw` | `-jh-tgw` |
| Per-zone cluster jumphosts | `testing_cluster_jumphost_name_prefix` | `acme-eu-jh` *(module appends `-<zone>`, e.g. `acme-eu-jh-us-south-1`)* | `-jh` |

The names are **deterministic** from the prefix, so `roksbnkctl` re-derives them on every `up` / `plan` / `apply` — regeneration is always safe, and the rendered `terraform.tfvars` is a faithful record of exactly what the tool asked IBM Cloud to create.

### Why the cluster name takes no suffix

The cluster name deliberately **equals the prefix** with no suffix appended. This is load-bearing, not an oversight:

- The ROKS/OpenShift cluster name has the **tightest** limit of any resource here — **35 characters** (see the table below). Every other name is an IS resource capped at **63**.
- By making the cluster name *be* the prefix, the prefix's own length limit becomes that same 35-character cluster limit. A prefix that's short enough to be a valid cluster name is automatically short enough that **every** suffixed name fits inside 63 — the worst case is the zone-appended cluster jumphost (`<prefix>-jh-us-south-1`), which at a 35-char prefix reaches only 49 of its 63-char budget.

> **Do not "tidy" the cluster name into `<prefix>-cluster`.** Adding a suffix to the cluster name would shrink the usable prefix length (the binding 35-char limit would now have to absorb `-cluster` too) without buying anything. The no-suffix cluster name is what lets a single prefix-length check guarantee the whole name set is valid.

### The length / charset limits

`roksbnkctl` validates the prefix — and every name it derives — against these per-resource-type constraints. They are pinned from IBM Cloud's own validators so the tool rejects a bad prefix at `init` time rather than letting IBM Cloud reject it minutes into an `apply`:

| Resource kind | Max length | Charset rule | Source |
|---|---|---|---|
| ROKS / OpenShift cluster name | **35** | Start with a letter; letters, digits, and hyphen `-`; ≤ 35 chars | IBM Cloud provider docs, `ibm_container_cluster` / `ibm_container_vpc_cluster` `name` argument ("must start with a letter, can contain letters, numbers, and hyphen (-), and must be 35 characters or fewer"). |
| IS resource — VPC, VSI, Transit Gateway, SSH key | **63** | Start with a lowercase letter; lowercase letters, digits, and hyphen `-`; must end alphanumeric; ≤ 63 chars (regex `^[a-z][-a-z0-9]*$` + ends `[a-z0-9]`) | terraform-provider-ibm `ValidateISName` (`ibm/validate/validators.go`). |
| COS / Resource Controller instance | **180** | Permissive server-side; `roksbnkctl` reuses the lowercase-alphanumeric IS subset since the name derives from an already-validated lowercase prefix | IBM Cloud Resource Controller create-instance `name` field ("must be 180 characters or less"). |

Because the prefix flows into all three, `roksbnkctl` enforces the **strictest** applicable rule on the prefix label itself (lowercase, start with a letter, `[a-z0-9-]`, no trailing hyphen) and the tightest **length** (35, from the cluster). An over-long prefix is rejected with an actionable message naming the offending resource, its computed length, its limit, and the maximum allowable prefix length — and `init` re-prompts (TTY) or hard-errors (non-TTY). There is **no silent truncation or hashing**.

### Namespaces are NOT prefixed

Kubernetes namespaces — `cert_manager_namespace` (`cert-manager`), `flo_namespace` (`f5-bnk`), `flo_utils_namespace` (`f5-utils`) — default to their conventional values (and are settable via `bnk.flo_namespace` / `bnk.flo_utils_namespace` — including to the **same** value, for one namespace instead of two) and are **never** prefixed. They are cluster-internal: two workspaces only ever collide on namespaces if they target the **same** cluster, in which case a shared namespace is usually what you want (and a prefix would actively break the convention FLO and the BNK charts expect). Only **account-scoped** IBM Cloud infrastructure names — cluster, VPCs, TGW, COS, jumphosts — are prefixed, because those are what collide at the account level.

### Overriding a generated name

Every generated name is just a value in the rendered `terraform.tfvars`, so the normal [layering rule](#the-layering-rule) overrides it. To pin a specific name (e.g. to adopt a VPC that doesn't follow the prefix convention), set the matching variable in `~/.roksbnkctl/<ws>/terraform.tfvars.user` or pass it via `--var-file`:

```bash
# Override just the Transit Gateway name for this workspace, permanently
echo 'roks_transit_gateway_name = "shared-corp-tgw"' \
  >> ~/.roksbnkctl/<ws>/terraform.tfvars.user
```

The `.user` file (or `--var-file`) layers **after** the generated `terraform.tfvars`, so the override wins. This is the same override path documented in [§"The layering rule"](#the-layering-rule) — the prefix machinery doesn't change it. Declining a resource at `init` time and supplying an existing one is the cleaner path for adoption; see [Chapter 12 §"Worked example"](./12-workspace-config.md#worked-example-bootstrap-a-workspace-from-scratch).

## The layering rule

When `roksbnkctl up` (or `plan`/`apply`/`destroy`) invokes Terraform, it composes three layers of tfvars in this order:

```
1. terraform.tfvars              (rendered by roksbnkctl from config.yaml)
2. terraform.tfvars.user         (workspace-local override, optional)
3. --var-file <path> ...         (CLI flag, repeatable, later file wins)
```

Later layers override earlier ones — same rule Terraform itself uses for `-var-file` chaining.

Concretely:

```bash
# config.yaml says cluster.workers_per_zone: 2
# ~/.roksbnkctl/dev/terraform.tfvars.user contains:
#   roks_workers_per_zone = 4
# Run with no flag:
roksbnkctl up
# → terraform sees 4 (.user wins over generated .tfvars)

# Pass a CLI override:
roksbnkctl up --var-file ./perf-test.tfvars
# perf-test.tfvars contains: roks_workers_per_zone = 8
# → terraform sees 8 (.var-file wins over .user)

# Multiple --var-files; later wins:
roksbnkctl up \
  --var-file ./base.tfvars \
  --var-file ./override.tfvars
# → values in override.tfvars win over base.tfvars,
#   which both win over .user, which wins over .tfvars
```

The `--var-file` flag matches Terraform's own `--var-file` exactly — repeatable, paths interpreted relative to the working directory at invocation time.

## The `IBMCLOUD_API_KEY` exception

The upstream HCL declares `ibmcloud_api_key` as a `sensitive` variable. Every other tfvar can land in a file on disk; this one never does.

Instead, the API key flows through the resolver chain (env → keychain → config-b64 → prompt — see [Chapter 14](./14-credentials-resolver.md)), and `roksbnkctl` exports it as `TF_VAR_ibmcloud_api_key` in the environment of the terraform-exec child process. Terraform reads the env var and injects it as if it had been declared in tfvars, but no plaintext key ever touches the filesystem.

If you put `ibmcloud_api_key = "..."` in a hand-edited tfvars and run `terraform` directly (not via `roksbnkctl`), it works — Terraform itself is happy. But this is **not** how `roksbnkctl` runs Terraform, and putting the key in a `.tfvars.user` or `--var-file` is **strongly discouraged**: the file persists on disk, gets backed up, gets committed to git by accident, and gets read by other processes. The env-var path eliminates the on-disk window entirely.

Other secrets in scope:

- `bigip_password` — upstream HCL declares it as a regular string (not `sensitive`). If you set it in tfvars, the value lands on disk. Treat that file like a credential.
- COS HMAC keys — auto-generated by the `roks_cluster` module via the COS service-credentials resource; they live in `terraform.tfstate` (which is itself sensitive — `chmod 0600`, never commit, treat the workspace as a secret store).

## Worked example: bigger cluster for a perf test

Default workspace, default cluster. You want to bump worker count for one perf-test run, then go back.

```bash
# 1. Confirm the current value comes from config.yaml
$ grep workers ~/.roksbnkctl/dev/config.yaml
  workers_per_zone: 2

# 2. Drop a one-off override into a file
$ cat > ~/perf-cluster.tfvars <<'EOF'
roks_workers_per_zone = 6
roks_min_worker_vcpu_count = 32
roks_min_worker_memory_gb = 128
EOF

# 3. Plan against it (note: --var-file passes through to terraform plan)
$ roksbnkctl plan --var-file ~/perf-cluster.tfvars

# 4. Apply
$ roksbnkctl apply --var-file ~/perf-cluster.tfvars

# 5. Run the throughput test
$ roksbnkctl test throughput

# 6. Roll back: re-apply WITHOUT the var-file
$ roksbnkctl apply
# → terraform sees workers_per_zone=2 again from config.yaml-derived tfvars
```

Notice step 6 — dropping the `--var-file` flag is the rollback. Terraform compares its current state to the new desired state (from `config.yaml`) and scales the worker pool back down. No special "undo" command needed.

For a more permanent override (you want this workspace to *always* run with bigger nodes), put the contents of `perf-cluster.tfvars` into `~/.roksbnkctl/dev/terraform.tfvars.user` instead. Then every `roksbnkctl up`/`apply` picks it up automatically without a CLI flag.

## When to edit `config.yaml` vs `.tfvars.user` vs `--var-file`

A rough decision matrix:

| You want to change... | Edit... |
|---|---|
| Cluster identity, region, OpenShift version, worker count | `config.yaml` (via `roksbnkctl init` or by hand) |
| BNK chart version, CNEInstance size, FAR repo | `config.yaml` (the `bnk:` block) |
| A variable not modelled in `config.yaml` | `terraform.tfvars.user` (workspace-local, persistent) |
| You have a complete `./terraform.tfvars` you want this workspace to always use | Copy it to `~/.roksbnkctl/<ws>/terraform.tfvars.user` (sibling to `config.yaml`, mode `0600`) so bare `-w <ws>` commands Just Work for both phases. See [Chapter 6 §"Raw terraform-variable overrides"](./06-workspaces.md#raw-terraform-variable-overrides). |
| A one-off override for a single run (perf test, capacity bump) | `--var-file ./oneoff.tfvars` (CLI) |
| A CI-pipeline variable bundle that's checked into git | `--var-file ./ci-overrides.tfvars` (CLI; the file lives in your CI repo, not the workspace) |

The schema in `config.yaml` covers about a third of the upstream HCL variables — the ones that nearly every workspace needs to set. The other two-thirds (jumphost details, every BNK module's full surface, the testing module's full surface) are reachable through the lower layers.

## Cross-references

- [Chapter 12 — Workspace config](./12-workspace-config.md) — what `config.yaml` covers vs what falls through to tfvars.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) — why `ibmcloud_api_key` doesn't go in tfvars.
- [Chapter 29 — Terraform variable reference](./29-terraform-variable-reference.md) — auto-generated complete reference for `terraform/variables.tf`.
- The upstream `terraform/variables.tf` source: <https://github.com/jgruberf5/roksbnkctl/blob/main/terraform/variables.tf>
- The upstream starter file: <https://github.com/jgruberf5/roksbnkctl/blob/main/terraform/terraform.tfvars.example>
