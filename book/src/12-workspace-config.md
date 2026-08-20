# Workspace config (config.yaml)

This chapter is the field-by-field reference for the per-workspace `config.yaml`. If you've read [Chapter 6 — Workspaces](./06-workspaces.md) you've seen the on-disk layout; this chapter zooms in on the YAML file that drives everything else (`init`, `up`, `down`, `cluster up`, the test suite, the SSH targets, the new execution backends).

You don't usually edit this file by hand. `roksbnkctl init` generates it interactively; later runs read it. But because every other knob in the tool reads from here, it's worth knowing what every field means and what defaults apply when you leave one out.

## File location

Each workspace's config lives at:

```
~/.roksbnkctl/<workspace>/config.yaml
```

Override the base directory with the `ROKSBNKCTL_HOME` env var (test fixtures use this; everyday users shouldn't need it). The file is created mode `0600`, inside a workspace directory created `0700` — owner-only, because this file can hold `ibmcloud.api_key_b64` and `registry.generic_password_b64`, and base64 is obfuscation rather than encryption.

Workspaces created by a release before v1.50.0 are on disk at `0644`/`0755`. They are tightened in place the first time roksbnkctl reads them, and it says so once:

```
⚠ workspace "prod" was readable by other users on this host; tightened 3 path(s) to owner-only.
  config.yaml can hold your IBM Cloud API key and registry password. If this host is shared, rotate them.
```

The repair fixes the mode from that point on; it does not undo the exposure that already happened. On a shared host, rotate the API key. `roksbnkctl doctor` reports the current state as the `workspace permissions` check, and is the only thing that reports a tree that could **not** be tightened (a read-only mount, or a filesystem with no POSIX modes). On Windows the check is skipped: Go's `Chmod` there only toggles the read-only bit, so there is no owner-only mode to assert.

There's also a *global* `~/.roksbnkctl/config.yaml` at the top level — it holds the `current_workspace` pointer and other user-wide preferences. That's a different file with a different schema; this chapter is about the per-workspace one.

## When it gets written

| Action | Effect on `config.yaml` |
|---|---|
| `roksbnkctl init` | Creates the file from interactive prompts. Existing file? Asks before overwriting. |
| `roksbnkctl init --upgrade-tf` | Updates `tf_source:` only; leaves every other field alone. |
| `roksbnkctl targets add <name> ...` | Adds an entry under `targets:`. |
| `roksbnkctl targets remove <name>` | Removes the entry. |
| `roksbnkctl up` (post-apply) | Auto-populates `targets.jumphost` if the upstream HCL emitted a TGW jumphost output. |
| Anything else | Reads the file. Doesn't write back. |

Direct hand-editing is supported (the file is plain YAML) but discouraged for fields that have dedicated commands — adding an SSH target via `roksbnkctl targets add` keeps the schema validation in one place.

## Top-level structure

```yaml
prefix: acme-eu  # base for every account-scoped resource name

ibmcloud:        # IBM Cloud account + auth
  region: ca-tor
  resource_group: default
  api_key_source: keychain
  # api_key_b64: <base64-of-api-key>   # OPTIONAL fallback when keychain unavailable

cluster:         # ROKS cluster identity
  create: true
  name: acme-eu   # equals prefix when init derived the name
  openshift_version: "4.20"
  workers_per_zone: 2

resources:       # per-resource create/adopt toggles
  transit_gateway:   { create: true }
  registry_cos:      { create: true }
  cert_manager:      { create: true }
  bnk:               { create: true }
  tgw_jumphost:      { create: true }
  cluster_jumphosts: { create: false }
  client_vpc:        { create: false }

bnk:             # BNK trial knobs (optional; falls through to upstream HCL defaults)
  cneinstance_size: Small
  far_repo_url: repo.f5.com
  manifest_version: 2.3.0-3.2598.3-0.0.170

test:            # test-suite tuning (optional)
  throughput:
    duration: 30
    streams: 8
  connectivity:
    extra_hosts:
      - https://my.gslb.example.com

tf_source:       # where the Terraform HCL comes from
  type: embedded         # embedded | github | local

targets:         # SSH targets (see Chapter 15)
  jumphost:
    host: 169.45.91.177
    user: ubuntu
    key_source: tf-output:jumphost_shared_key

exec:            # per-tool execution backend defaults (see Chapter 17)
  ibmcloud:  { backend: local }
  iperf3:    { backend: k8s }
  terraform: { backend: local }

cos:             # optional COS supply-chain config
  instance: bnk-supply-chain
  bucket: bnk-artifacts
```

Every block except `ibmcloud:`, `cluster:`, and `tf_source:` is optional. Omit a block and the tool falls through to either a documented default (covered below) or the upstream HCL's own default for terraform variables. The `prefix:` field and `resources:` block are also optional — omit them and the workspace renders a sparse `terraform.tfvars` that falls through to the upstream module default names.

## `prefix:`

```yaml
prefix: acme-eu
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `prefix` | string | empty (legacy sparse render) | The base for every account-scoped IBM Cloud resource name (cluster, VPCs, Transit Gateway, COS, jumphosts). Must be a lowercase label: start with a letter, `[a-z0-9-]`, no trailing hyphen, **≤ 35 chars**. |

The `init` interview asks for a workspace **prefix** and derives every account-scoped resource name from it — `acme-eu` becomes cluster `acme-eu`, VPC `acme-eu-cluster-vpc`, TGW `acme-eu-tgw`, COS `acme-eu-registry-cos`, jumphosts `acme-eu-jh-tgw` / `acme-eu-jh-<zone>`. This stops two workspaces in the same IBM Cloud account from colliding on the old shared `tf-*` default names.

The 35-char cap is the ROKS cluster-name limit (the tightest of all the resource types). `roksbnkctl` validates the prefix — and every name it derives — at `init` time and re-prompts on overflow; there is no silent truncation. An **empty** `prefix` keeps the legacy sparse render (no names emitted), so old configs are unaffected. The full derivation table, the per-resource length/charset limits, the source citations, and the override path live in [Chapter 13 §"Resource naming & collision avoidance"](./13-terraform-variables.md#resource-naming--collision-avoidance).

## `ibmcloud:`

```yaml
ibmcloud:
  region: ca-tor
  resource_group: default
  api_key_source: keychain
  api_key_b64: ""
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `region` | string | none — required | IBM Cloud region for cluster, VPC, COS. Examples: `ca-tor`, `us-south`, `eu-de`. |
| `resource_group` | string | `default` | Account-level resource group all created resources land in. |
| `api_key_source` | enum | empty (auto-resolve chain) | `env` \| `keychain` \| `config` \| `prompt`. Pin the resolver to one source; leave empty to walk the full chain. See [Chapter 14](./14-credentials-resolver.md). |
| `api_key_b64` | string | empty | Base64-encoded API key, **obfuscation only — not encryption**. The fallback when no OS keychain is available (e.g. WSL2 without libsecret). Treat the file as plaintext-credential-equivalent. |

The plaintext field name `api_key:` is **rejected** at load time — `roksbnkctl` refuses to read a workspace config that contains it. The encoded `api_key_b64:` form is the only inline path. Full discussion in [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md).

## `cluster:`

```yaml
cluster:
  create: true
  name: tf-openshift-cluster
  openshift_version: "4.20"
  workers_per_zone: 2
  public_gateway: true          # optional; false = private/disconnected cluster (no egress)
  min_worker_vcpu_count: 16     # optional; worker-flavor auto-select floor
  min_worker_memory_gb: 64      # optional; worker-flavor auto-select floor
  vpc_cidr: 10.242.0.0/16       # optional; address block for a NEW cluster VPC
  network_mode: single-nic      # optional; single-nic (default) | multi-nic
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `create` | bool | `true` | When `true`, `roksbnkctl cluster up` provisions a new ROKS cluster. When `false`, `cluster register <name>` adopts an existing one. |
| `name` | string | none — required | OpenShift cluster name when `create=true`; cluster ID-or-name to adopt when `create=false`. |
| `openshift_version` | string | empty (latest) | E.g. `"4.20"`. Empty lets IBM Cloud pick the current default. Quote it — YAML otherwise parses `4.20` as a float. |
| `workers_per_zone` | int | `1` | Worker nodes per AZ; cluster runs across 3 AZs by default in MZR regions, so `2` ⇒ 6 workers total. |
| `public_gateway` | bool | `true` | Attach a public gateway to each cluster subnet for worker Internet egress. `false` builds a **private/disconnected** cluster with no egress (no `ibm_is_public_gateway`, no subnet attachment). Sets `cluster_public_gateway`. **Expert:** a `false` cluster needs private connectivity you provide — a reachable mirror registry, VPEs / private service endpoints for IBM Cloud services, and FLP/`disconnected` licensing — see [Chapter 10a §"A truly disconnected cluster"](./10a-air-gapped-install.md). Governs worker egress only; the master keeps its public API endpoint. |
| `min_worker_vcpu_count` | int | `16` | Minimum vCPUs when the cluster module auto-selects the `bx2` worker flavor (smallest profile meeting both minimums). `0`/omitted ⇒ HCL default. Sets `roks_min_worker_vcpu_count`. |
| `min_worker_memory_gb` | int | `64` | Minimum memory (GB) for the same auto-select. `0`/omitted ⇒ HCL default. Sets `roks_min_worker_memory_gb`. |
| `vpc_cidr` | string | empty (IBM `auto`) | Address block a **new** cluster VPC's three per-zone prefixes are carved from, so `/18` is the smallest usable value. Empty leaves IBM's `auto`, which gives every VPC in a region the same prefixes — two such VPCs cannot share a Transit Gateway. Sets `cluster_vpc_cidr`. **CREATE-time only**; changing it later warns. |
| `existing_subnet_ids` | list | (empty) | Place the cluster in subnets that **already exist**, one per zone, **in zone order** — each subnet's zone is read from the subnet itself, so a reordered list places the cluster differently rather than failing. Requires `resources.cluster_vpc: {create: false, existing: <vpc-id>}`. Renders `use_existing_cluster_subnets` + `existing_cluster_subnet_ids`. Env: `ROKSBNKCTL_EXISTING_SUBNET_IDS` (comma-separated). |
| `network_mode` | string | `single-nic` | How the worker nodes are attached. Omitted means `single-nic`, which is what every cluster built with this tool is — the field exists to *name* that, not to change it. `multi-nic` requires BNK 2.4+. Sets `cluster_network_mode`. **CREATE-time only and enforced**: an explicit value contradicting the built cluster is refused, because there is no in-place conversion. See [Chapter 28](./28-configuration-reference.md#bnk-release-and-network-mode). |

The `cluster:` block translates to terraform variables `create_roks_cluster`, `openshift_cluster_name`, `roks_cluster_id_or_name`, `openshift_cluster_version`, `roks_workers_per_zone`, `cluster_public_gateway`, `cluster_vpc_cidr`, `cluster_network_mode` — see [Chapter 13](./13-terraform-variables.md) and [Chapter 29](./29-terraform-variable-reference.md) for the full mapping.

When a `prefix` is set, `init` fills `cluster.name` with the prefix itself (the cluster name carries no suffix — see [Chapter 13](./13-terraform-variables.md#why-the-cluster-name-takes-no-suffix)). Setting `cluster.create: false` adopts an existing cluster by name/ID via `cluster.name`, exactly as before — the cluster is **not** part of the `resources:` block below.

## `resources:`

```yaml
resources:
  transit_gateway:   { create: true }
  registry_cos:      { create: true }
  cert_manager:      { create: true }
  bnk:               { create: true }
  tgw_jumphost:      { create: true }
  cluster_jumphosts: { create: false }
  client_vpc:        { create: false, existing: shared-client-vpc }
  client_region:     ca-tor               # testing-client region (plain string)
```

The `init` interview's create/adopt answers, one `{create, existing}` pair per resource. `create: true` provisions a new, prefix-named resource; `create: false` declines it, and when a still-enabled resource depends on the declined one, `existing:` names the pre-existing resource to consume instead.

| Sub-block | `create` default | Renders into | Asks for `existing` when… |
|---|---|---|---|
| `transit_gateway` | `true` | `create_roks_transit_gateway` (+ `roks_transit_gateway_name`) | declined **and** the TGW jumphost is enabled (it needs a TGW) |
| `registry_cos` | `true` | `create_roks_registry_cos_instance` (+ `roks_cos_instance_name`) | declined |
| `cert_manager` | `true` | `install_cert_manager` | — |
| `bnk` | `true` | `deploy_bnk` | — |
| `tgw_jumphost` | `true` | `testing_create_tgw_jumphost` (+ `testing_tgw_jumphost_name`) | — |
| `cluster_jumphosts` | `false` | `testing_create_cluster_jumphosts` (+ `testing_cluster_jumphost_name_prefix`) | — |
| `client_vpc` | `false` | `testing_create_client_vpc` (+ `testing_client_vpc_name`) | TGW jumphost enabled but you decline creating a new client VPC for it |

One extra key, `client_region`, is a plain **string** rather than a toggle: the region the testing client (TGW jumphost + client VPC) is installed in, set when `init` asks where to install the test client. It renders into `testing_client_vpc_region` (omitted → the terraform default applies) and seeds the regions `roksbnkctl cleanup` scans.

The block is **optional** — omit it and the sparse render (upstream module default names) applies; a fresh `init` writes it in full. The deep reference, including which terraform `create_*`/`*_name` variables each toggle renders, is [Chapter 28 §"`resources:` block"](./28-configuration-reference.md#resources-block).

## `bnk:`

```yaml
bnk:
  cneinstance_size: Small
  far_repo_url: repo.f5.com
  manifest_version: 2.3.0-3.2598.3-0.0.170
  flo_namespace: f5-bnk               # optional; FLO namespace
  flo_utils_namespace: f5-utils       # optional; = flo_namespace ⇒ ONE namespace
  gslb_datacenter_name: ""            # optional; CNEInstance GSLB datacenter
  cert_manager:                       # optional; cert-manager coordinates
    namespace: cert-manager
    version: v1.17.3
  network:                            # optional; data-plane subnets + TMM self-IPs
    vlan_prefixlen: 24                #   self-IP prefix length (F5SPKVlan)
    vlan_prefixlen_external: 23       #   optional; overrides the shared value
    vlan_prefixlen_internal: 26       #   optional; the two VLANs need not match
    tmm_k8s_routes: 172.17.0.0/18     #   pod CIDR TMM routes to
    zones:                            #   one entry per AZ (3 total)
      - ext_vlan_cidr: 10.155.15.0/24
        int_vlan_cidr: 10.254.99.0/24
        int_snat_cidr: 10.10.11.0/24
        int_vip_cidr: 10.135.15.0/24
        external_selfip: 10.155.15.101
        internal_selfip: 10.254.99.101
      # …zones 2 and 3
  flp:
    storage_class: ""                 # optional; FLP PVC StorageClass
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `cneinstance_size` | enum | upstream HCL default (`Small`) | `Tiny` \| `Small` \| `Medium` \| `Large`. Sets `cneinstance_deployment_size`. `Tiny` is what the BNK 2.4 install guide uses. Passed through unvalidated — a size a given manifest does not define is rejected by the operator, not by this tool. |
| `far_repo_url` | string | upstream HCL default (`repo.f5.com`) | The FAR Docker/Helm repo. Override only for staging/internal repos. |
| `manifest_version` | string | upstream HCL default | Pin a specific BNK manifest chart version. Leave empty to track the upstream HCL's pin. |
| `flo_namespace` | string | `f5-bnk` | F5 Lifecycle Operator namespace. Sets `flo_namespace`. |
| `flo_utils_namespace` | string | `f5-utils` | F5 utility-components namespace. Sets `flo_utils_namespace`. |
| `gslb_datacenter_name` | string | empty | CNEInstance GSLB datacenter **name**. Sets `cneinstance_gslb_datacenter_name`. Names the datacenter; `gtm.*` is what it registers **with**. |
| `gtm.url` | string | empty | BIG-IP DNS / GTM management URL. Empty disables GTM. Without it the datacenter name is a label pointing at nothing. Sets `cneinstance_gtm_url`. Env: `ROKSBNKCTL_GTM_URL`. |
| `gtm.username` | string | empty | GTM user. Env: `ROKSBNKCTL_GTM_USERNAME`. |
| `gtm.password_b64` | string | empty | GTM password, base64 (obfuscation, **not** encryption — like `ibmcloud.api_key_b64`). `ROKSBNKCTL_GTM_PASSWORD` takes the raw value. |
| `trusted_profile.service_account` | string | (derived) | Account allowed to assume the CNE controller's IBM Cloud Trusted Profile. Empty derives FLO's own name, `f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount`. The IAM trust rule is a **matcher**, not a pointer: IBM compares a pod's service-account token against `crn`/`namespace`/`name` with `EQUALS`, so a name that does not match the account the CNE controller actually runs as means **nothing can assume the profile, and nothing reports an error**. Set this only if you can also make FLO name the account differently — roksbnkctl cannot, since FLO creates it when it reconciles the CNEInstance and that spec has no service-account field. Env: `ROKSBNKCTL_TRUSTED_PROFILE_SA`. |
| `trusted_profile.roles` | list | `[Viewer, Editor]` | IAM roles for that profile, scoped to the cluster's own VPC. Env: `ROKSBNKCTL_TRUSTED_PROFILE_ROLES` (comma-separated). |
| `cert_manager.namespace` | string | `cert-manager` | cert-manager namespace. Sets `cert_manager_namespace`. Install/skip stays on `resources.cert_manager.create`. |
| `cert_manager.version` | string | HCL default | Pin the cert-manager chart version. Sets `cert_manager_version`. |
| `license_mode` | enum | `connected` | `connected` \| `disconnected` \| `f5licenseproxy`. Sets `license_mode`. |
| `flp.storage_class` | string | HCL default | Dynamic StorageClass for the FLP's PVCs. Sets `flp_storage_class`. Other `flp.*` fields: see [Chapter 28](./28-configuration-reference.md). |

Every field here is optional — leave the block out entirely and you get the upstream HCL's defaults.

### `bnk.network:` — data-plane subnets + TMM self-IPs

BNK's data plane (TMM) needs per-availability-zone VLAN subnets, SNAT/VIP ranges, and self-IPs. Leave `bnk.network` out entirely and the BNK install-guide defaults apply. Set it to match your cluster's fabric — `roksbnkctl init` prompts for every field (opt in at *"Customize BNK networking?"*, seeded with these defaults), or hand-write the block.

| Field | Default (AZ1) | Notes |
|---|---|---|
| `zones[].ext_vlan_cidr` | `10.155.15.0/24` | External VLAN subnet CIDR. |
| `zones[].int_vlan_cidr` | `10.254.99.0/24` | Internal VLAN subnet CIDR. |
| `zones[].int_snat_cidr` | `10.10.11.0/24` | Internal SNAT-pool CIDR. |
| `zones[].int_vip_cidr` | `10.135.15.0/24` | Internal VIP CIDR. |
| `zones[].external_selfip` | `10.155.15.101` | External TMM self-IP. |
| `zones[].internal_selfip` | `10.254.99.101` | Internal TMM self-IP. |
| `vlan_prefixlen` | `24` | Self-IP prefix length (`spec.prefixlen_v4` on the F5SPKVlan CRs) — the size of the L2 subnet TMM treats as directly connected. Usually you want it to match your VLAN CIDRs — but it is **independent of them and never derived from them**, and nothing validates the two against each other. A deliberate disagreement, paired with static routes, is how a specific traffic pattern is forced. Sets `cneinstance_vlan_prefixlen`. |
| `vlan_prefixlen_external` | (inherit) | Overrides `vlan_prefixlen` for the **external** VLAN only. Unset inherits. Sets `cneinstance_vlan_prefixlen_external`. Env: `ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL`. |
| `vlan_prefixlen_internal` | (inherit) | Same for the **internal** VLAN. Sets `cneinstance_vlan_prefixlen_internal`. Env: `ROKSBNKCTL_VLAN_PREFIXLEN_INTERNAL`. |
| `tmm_k8s_routes` | `172.17.0.0/18` | Pod CIDR TMM installs a route toward (`TMM_K8S_ROUTES`) so it can reach backend pods. Set to your cluster's pod subnet if it isn't the ROKS default. Sets `cneinstance_tmm_k8s_routes`. |

Provide **all three zones** when you set `zones` — supplying zones replaces the defaults entirely (they render `cneinstance_network_zones`, driving the cloud-network-mapping ConfigMap and the external/internal F5SPKVlan CRs). Zone *names* are derived from the region and aren't configurable. `vlan_prefixlen` and `tmm_k8s_routes` are network-wide (shared across all zones), though the mask can differ **between the two VLANs** via `vlan_prefixlen_external` / `vlan_prefixlen_internal` — TMM can front a `/23` externally while the internal side is a `/26`, which one shared scalar could not express. Unset either scalar to keep the terraform default.

## `test:`

```yaml
test:
  throughput:
    image: networkstatic/iperf3:latest
    duration: 30
    streams: 8
    default_mode: north-south
  connectivity:
    extra_hosts:
      - https://my.gslb.example.com
      - https://internal.example.test
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `throughput.image` | string | `networkstatic/iperf3:latest` | iperf3 image used by the throughput test (when running with the `local` or `ssh` backends). The `k8s` backend uses the GHCR image (`ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<version>`) instead. |
| `throughput.duration` | int seconds | `30` | iperf3 `-t` flag. |
| `throughput.streams` | int | `8` | iperf3 `-P` flag. |
| `throughput.default_mode` | enum | `north-south` | `north-south` \| `east-west`. The connectivity vector to test by default. |
| `connectivity.extra_hosts` | []string | empty | Extra URLs the connectivity test probes alongside the canonical IBM/F5 endpoints. |

## `tf_source:`

```yaml
tf_source:
  type: embedded
```

| `type` | Other fields | Use case |
|---|---|---|
| `embedded` (default) | none | Use the HCL bundled into the `roksbnkctl` binary via `go:embed`. The recommended path for users — install one binary, get matched CLI + Terraform together. |
| `github` | `repo: "owner/name"`, `ref: "v0.6.1"` | Pull a tarball from a GitHub release. Useful for testing forks or pinning to a specific upstream tag. |
| `local` | `path: "/abs/path/to/tf-source"` | Point Terraform at an on-disk directory. For active development on the HCL itself. |

An empty `type` is treated as `embedded` (legacy / forgot-to-set).

`roksbnkctl init --upgrade-tf` is the helper for bumping the source between versions without retyping the rest of the config — see "Editing by hand vs helpers" below.

## `targets:` — SSH targets

```yaml
targets:
  jumphost:
    host: 169.45.91.177
    user: ubuntu
    key_source: tf-output:jumphost_shared_key
  bastion:
    host: ops.example.com
    user: jgruber
    key_path: ~/.ssh/id_ed25519
```

Each entry has `host`, `user`, optional `port` (default `22`), and exactly one of `key_path` or `key_source`. The `key_source` enum supports `agent` and `tf-output:<name>`.

The deep reference is [Chapter 15 — SSH targets](./15-ssh-targets.md), and the user-facing prose is [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md). This chapter just notes the schema's place in the overall config.

You don't typically edit this block by hand. `roksbnkctl up` auto-populates `jumphost` post-apply, and `roksbnkctl targets add ...` populates the rest.

## `exec:` — execution-backend defaults

```yaml
exec:
  ibmcloud:  { backend: local }
  iperf3:    { backend: k8s }
  terraform: { backend: local }
```

Per-tool defaults for the `--backend` system. Each entry is keyed by the tool name (`ibmcloud`, `iperf3`, `terraform`, and others as the matrix grows) and selects which execution backend that tool uses by default. Allowed backend values:

| Backend | Notes |
|---|---|
| `local` | `os/exec` against the host binary. The default for `terraform` and `ibmcloud`. |
| `docker` | Runs inside a vendored container image. Frozen toolchain version, no host install. |
| `k8s` | Runs inside the cluster (long-lived ops pod or one-shot Job). Default for `iperf3`. |
| `ssh` | Runs on a registered SSH target. Format: `ssh:<target-name>`. |

A `--backend <value>` flag on the command line overrides the workspace config for that single invocation. The flag wins; the config sets the default.

The `iperf3` default is `k8s` because measuring throughput from a laptop's internet uplink isn't useful — you want the test to run from a network location adjacent to or inside the cluster. The `local` default is wrong for that tool, so the workspace config flips it.

[Chapter 17 — Execution backends](./17-execution-backends.md) covers the full backend matrix; [Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md) is the decision tree.

## `cos:` — COS supply-chain (optional)

```yaml
cos:
  instance: bnk-supply-chain
  bucket: bnk-artifacts
  region: us-south
  upload:
    - source: ./local/f5-far-auth-key.tgz
      key: f5-far-auth-key.tgz
    - source: ./local/subscription.jwt
      key: subscription.jwt
```

| Field | Type | Notes |
|---|---|---|
| `instance` | string | COS instance name holding the FAR auth key + JWT. Empty ⇒ `bnk-supply-chain`. Sets `ibmcloud_cos_instance_name`. |
| `bucket` | string | COS bucket name within that instance. Empty ⇒ an **account-scoped** `bnk-artifacts-<first-12-of-account-id>` (COS bucket names are globally unique, so the account suffix keeps it collision-free; `init` provisions it, and a second workspace from the same account discovers and reuses it). Sets `ibmcloud_resources_cos_bucket`. |
| `region` | string | Region the bucket lives in. Empty ⇒ `us-south`. Sets `ibmcloud_cos_bucket_region`. |
| `upload` | []{source, key} | Optional pre-flight uploads from local files into the bucket. Useful for CI scenarios where the supply-chain artefacts are produced by the pipeline. |

`instance` / `bucket` / `region` are honoured by **both** the terraform render and the `registry` FAR-file resolver, so a customer-owned COS bucket is used consistently across both.

The block is optional — if you've already populated COS by hand or via the upstream HCL's `roks_cos_instance_name` variable, you don't need it. [Chapter 25 — COS supply chain management](./25-cos-supply-chain.md) covers the full workflow.

## Behaviour when fields are missing

`roksbnkctl` falls through three layers in order: **workspace config → upstream HCL default → fail**.

| Missing field | Behaviour |
|---|---|
| `prefix` | Empty ⇒ legacy sparse `terraform.tfvars` (no name variables; upstream `tf-*` module defaults). `init` prompts for one on a fresh interactive run. |
| `resources` | Empty ⇒ no per-resource toggles rendered; goes with the empty-`prefix` legacy path. A fresh prefix-driven `init` writes the full block. |
| `ibmcloud.region` | `roksbnkctl init` prompts; programmatic loads error with "region is empty". |
| `ibmcloud.api_key_source` | Resolver walks the full chain (env → keychain → config → prompt). |
| `ibmcloud.api_key_b64` | Skipped in the resolver chain. |
| `cluster.name` | `init` prompts; programmatic loads error. |
| `cluster.openshift_version` | Empty string passed to upstream HCL; the module picks the current default. |
| `cluster.workers_per_zone` | Falls through to `1` (upstream default). |
| `bnk.*` | Field is omitted from the generated `terraform.tfvars` and the upstream HCL default applies. |
| `tf_source` | Treated as `type: embedded` (legacy default). |
| `targets.*` | Block absent ⇒ `roksbnkctl --on jumphost` errors with "no target named jumphost"; auto-populated by `up`. |
| `exec.*` | Per-tool defaults: `ibmcloud`→`local`, `terraform`→`local`, `iperf3`→`k8s`, DNS probe→`local`. Override per-tool via this block, or per-invocation via `--backend`. |
| `cos.*` | No pre-flight uploads; the COS instance/bucket are read from the upstream HCL's tfvars instead. |

The general rule: **if you don't write it in `config.yaml`, `roksbnkctl` doesn't write it into `terraform.tfvars`**, and the upstream HCL's `default = ...` clause takes over. The full upstream defaults are listed in [Chapter 29](./29-terraform-variable-reference.md).

## How `--var-file` interacts with `config.yaml`

Both `roksbnkctl up` and `roksbnkctl plan/apply/destroy` accept the same `--var-file` flag terraform itself accepts (repeatable, later files win). The layering rule is:

```
1. config.yaml-derived terraform.tfvars        (written first by roksbnkctl)
2. ~/.roksbnkctl/<ws>/terraform.tfvars.user  (optional manual override)
3. --var-file <path>                           (CLI; repeatable)
```

Later layers override earlier. Concretely: `config.yaml`'s `cluster.workers_per_zone: 2` writes `roks_workers_per_zone = 2` into the generated tfvars. If you then pass `--var-file ./bigger.tfvars` containing `roks_workers_per_zone = 5`, terraform sees `5`. The `config.yaml` value didn't get re-applied; `--var-file` wins.

The `terraform.tfvars.user` middle layer is for when you want a workspace-local override that survives across runs without modifying `config.yaml` — it's typically used for fields the YAML schema doesn't model (rare; the schema covers the common knobs). [Chapter 13](./13-terraform-variables.md) goes deep on this.

If you want a workspace to always apply a raw `terraform.tfvars` without re-passing `--var-file` on every phase command, drop it verbatim at `~/.roksbnkctl/<ws>/terraform.tfvars.user` (mode `0600`, sibling to `config.yaml`); subsequent `up` / `plan` / `apply` / `down` against bare `-w <ws>` pick it up automatically for both phases. To seed the workspace's `config.yaml` itself from a file, use `roksbnkctl init -w <ws> --config-file ./config.yaml` — see [Chapter 6 §"Skip the interview: `init --config-file`"](./06-workspaces.md#skip-the-interview-init---config-file).

The `IBMCLOUD_API_KEY` is the one exception that **never** goes through tfvars on disk. It's passed as a `TF_VAR_ibmcloud_api_key` env var on the terraform invocation. `--var-file` cannot supply the API key — the resolver chain in [Chapter 14](./14-credentials-resolver.md) is the only path.

## Editing by hand vs helpers

Several commands manage subsets of `config.yaml` so you don't have to:

| Subset | Helper |
|---|---|
| Whole file (interactive) | `roksbnkctl init` |
| `tf_source:` only | `roksbnkctl init --upgrade-tf` |
| `targets:` block | `roksbnkctl targets add/remove` |
| `registry:` block | `roksbnkctl registry target …` |
| `test.connectivity.extra_hosts` | `roksbnkctl test hosts add/remove/clear` |
| `state:` block (local ↔ COS/S3) | `roksbnkctl state local` / `state s3 …` / `state show` |
| `exec:` block (per-tool backend) | `roksbnkctl backend set/unset/show` |
| `bnkforge:` block | `roksbnkctl bnkforge enable/disable/status` |
| `ibmcloud.api_key_b64` | `roksbnkctl init` (after entering the key, it offers to save) |

When you do edit by hand, the load-time validators run on next `roksbnkctl` invocation:

- The plaintext-secret heuristic rejects an `api_key:` field (it must be `api_key_b64:` to be tolerated).
- Workspace name validation runs on directory access (workspace names must match `[A-Za-z0-9][A-Za-z0-9_.-]{0,63}`).
- YAML parse errors surface a line number.

If a hand edit breaks the file, every command that reads the workspace fails fast with the parse error path, so you'll know within one invocation.

## Worked example: bootstrap a workspace from scratch

End-to-end Part IV scenario: brand-new laptop, no `roksbnkctl` workspaces yet, an IBM Cloud API key in your password manager. Goal: a usable workspace with the key in the OS keychain, the right region + resource group resolved, and `terraform.tfvars` ready to drive the HCL.

The transcript below is captured against the shipped binary. Prompts are written to **stderr**, indented two spaces, with the label left-padded; the `[default]` (or `[Y/n]` / `[y/N]`) in brackets is what you get on a bare Enter. Here the operator types `acme-eu` for the prefix, keeps the cluster + COS + cert-manager + BNK + TGW-jumphost defaults, declines the Transit Gateway (adopting `shared-corp-tgw`), declines a new client VPC for the jumphost (adopting `shared-client-vpc`), and declines per-zone cluster jumphosts.

```text
$ roksbnkctl init -w dev
Setting up workspace "dev"

  Region                         [ca-tor]:

→ Verifying IBM Cloud credentials...
✓ IBM Cloud user you@example.com (account 1a2b3c…)

  Resource group                 [default]:
✓ Resource group "default" (id 0d1e2f…)

  Workspace prefix (≤ 35 chars)  [dev]: acme-eu
  Create new ROKS cluster?       [Y/n]: y
  OpenShift version              [4.20]:
  Workers per zone               [1]: 2
  Create registry COS instance?  [Y/n]: y
  Create Transit Gateway?        [Y/n]: n
  Install cert-manager?          [Y/n]: y
  Deploy BIG-IP Next (BNK)?      [Y/n]: y
  Create TGW test jumphost?      [Y/n]: y
  Create a new client VPC for it? [y/N]: n
  Existing client VPC name       : shared-client-vpc
  Existing Transit Gateway name  : shared-corp-tgw
  Create per-zone cluster jumphosts? [y/N]: n

Resolved resource names for prefix "acme-eu":
  cluster                acme-eu
  cluster VPC            acme-eu-cluster-vpc
  registry COS instance  acme-eu-registry-cos
  transit gateway        shared-corp-tgw  (existing)
  TGW jumphost           acme-eu-jh-tgw
  client VPC             shared-client-vpc  (existing)

✓ Wrote /home/you/.roksbnkctl/dev/config.yaml
```

(The IBM Cloud API key is resolved before the prompts above via the [credentials resolver chain](./14-credentials-resolver.md) — env, then keychain, then the workspace `api_key_b64`, then a hidden TTY prompt that offers to save the key. When the key is already in your environment or keychain, no key prompt appears, which is the common re-`init` case shown here.)

Two ordering details worth noting against the transcript: the **OpenShift version** and **Workers per zone** prompts appear only when you answer *yes* to "Create new ROKS cluster?" (a declined cluster prompts instead for an existing cluster name/ID), and the **Existing Transit Gateway name** prompt fires *after* the client-VPC questions — it is only asked when the TGW was declined **and** the TGW jumphost is enabled (the jumphost rides the gateway, so it needs the existing one's name).

Three things to note in that flow:

- **The prefix prompt validates and re-prompts.** Enter a prefix that's too long, starts with a digit, or has a trailing hyphen, and `init` rejects it with the offending resource, its computed length, its limit, and the maximum allowable prefix length — then asks again. In a non-TTY context (CI), an invalid default is a hard error rather than a silent truncation. The cap is 35 characters (the ROKS cluster-name limit). See [Chapter 13 §"The length / charset limits"](./13-terraform-variables.md#the-length--charset-limits).
- **Declining a create toggle triggers existing-resource discovery — but only when something still needs it.** Here the Transit Gateway was declined, and because the TGW jumphost is enabled (it rides the TGW), `init` asks for an existing TGW name (the `Existing Transit Gateway name` prompt). The same pattern drives the `Existing client VPC name` follow-up. Declining a resource that nothing else depends on (e.g. cluster jumphosts) just turns it off with no follow-up.
- **The resolved name plan is printed before the workspace is saved**, so you see exactly what `roksbnkctl` will ask IBM Cloud to create (and which names it will *adopt* rather than create) before committing.

The resulting `~/.roksbnkctl/dev/config.yaml`:

```yaml
prefix: acme-eu
ibmcloud:
  region: ca-tor
  resource_group: default
  api_key_source: keychain
cluster:
  create: true
  name: acme-eu
  workers_per_zone: 2
resources:
  transit_gateway:   { create: false, existing: shared-corp-tgw }
  registry_cos:      { create: true }
  cert_manager:      { create: true }
  bnk:               { create: true }
  tgw_jumphost:      { create: true }
  cluster_jumphosts: { create: false }
  client_vpc:        { create: false, existing: shared-client-vpc }
tf_source:
  type: embedded
```

That's the minimum a prefix-driven workspace writes. Everything else (`bnk:`, `test:`, `targets:`, `exec:`, `cos:`) is empty and falls through to defaults. A workspace created **without** a prefix (e.g. an old config) omits both `prefix:` and `resources:` and renders the legacy sparse `terraform.tfvars`. The API key can also be supplied non-interactively from your password manager's CLI by setting `IBMCLOUD_API_KEY` in the environment of the `init` invocation:

`op` here is the [1Password CLI](https://developer.1password.com/docs/cli/); the `op://...` URI is its secret-reference scheme. Any password-manager CLI that prints a secret to stdout works the same way — Bitwarden (`bw`), gopass, `aws secretsmanager get-secret-value`, Doppler, etc. — the only thing roksbnkctl cares about is that `IBMCLOUD_API_KEY` is set in the environment when `init` runs.

```bash
# Alternative: pre-set IBMCLOUD_API_KEY so init resolves it from env rather than prompting
IBMCLOUD_API_KEY=$(op read 'op://Private/IBM Cloud/api-key') roksbnkctl init -w dev
```

[Chapter 14 §"The `IBMCLOUD_API_KEY` resolver chain"](./14-credentials-resolver.md#the-ibmcloud_api_key-resolver-chain) covers the full env → keychain → workspace `api_key_b64` → TTY-prompt order; this env-var path is the first link in that chain, so anything `init` resolves at bootstrap time follows the same precedence later invocations use. Once `init` has saved the key to the OS keychain (the default sink), no further prompting is needed. `init` still prompts interactively for the remaining workspace metadata (region, resource group, workspace prefix, and the per-resource create toggles — the cluster name is *derived* from the prefix when you create a cluster, not prompted) — a fully non-interactive bootstrap is on the v1.x roadmap.

You don't render `terraform.tfvars` by hand. `roksbnkctl` derives it from `config.yaml` automatically on every `up` / `plan` / `apply`, writing it into the phase's state directory (`~/.roksbnkctl/dev/state/terraform.tfvars`). With the `prefix: acme-eu` set above, that generated file names every account-scoped resource from the prefix:

```hcl
# ~/.roksbnkctl/dev/state/terraform.tfvars (generated; do not hand-edit)
openshift_cluster_name    = "acme-eu"
roks_cluster_vpc_name     = "acme-eu-cluster-vpc"
roks_cos_instance_name    = "acme-eu-registry-cos"
create_roks_transit_gateway = false
roks_transit_gateway_name = "shared-corp-tgw"
testing_tgw_jumphost_name = "acme-eu-jh-tgw"
testing_client_vpc_name   = "shared-client-vpc"
# ...
```

(The separate `roksbnkctl tfvars` *command* is unrelated — it writes a copy of the upstream `terraform.tfvars.example` starter template to edit by hand, not this config-derived render. See [Chapter 13 §"`roksbnkctl tfvars` — bootstrap a starter"](./13-terraform-variables.md#roksbnkctl-tfvars--bootstrap-a-starter).)

[Chapter 13](./13-terraform-variables.md) covers the precedence rules between the generated `terraform.tfvars`, `terraform.tfvars.user` (the hand-edit overlay), and `--var-file`.

Finally, verify the workspace is healthy before the first real `up`:

```bash
# 3. Sanity-check
$ roksbnkctl doctor -w dev
✓ terraform     1.6.2  on PATH
✓ IBMCLOUD_API_KEY resolves via keychain
✓ region "ca-tor" accepts the key (IAM round-trip OK)
✓ resource group "default" exists (id: ...)
✓ workspace dev healthy
```

From here, `roksbnkctl up --auto -w dev` is the next step (see [Chapter 7 — Quick start](./07-quick-start.md)). You can layer on `bnk:`, `test:`, `targets:`, `exec:`, `cos:` blocks by hand-editing `config.yaml` whenever you need them — `init` only writes the minimum to keep first-run friction low.

## Cross-references

- [Chapter 13 — Terraform variables](./13-terraform-variables.md) — the layering between `config.yaml` and `terraform.tfvars`.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) — the `api_key_*` fields and how they're resolved.
- [Chapter 15 — SSH targets](./15-ssh-targets.md) — the `targets:` block.
- [Chapter 17 — Execution backends](./17-execution-backends.md) — the `exec:` block.
- [Chapter 28 — Configuration reference](./28-configuration-reference.md) — auto-generated complete field list.
- [Chapter 29 — Terraform variable reference](./29-terraform-variable-reference.md) — the upstream HCL variables `config.yaml` translates to.
