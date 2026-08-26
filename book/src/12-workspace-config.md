# Workspace config (config.yaml)

> **This is the teaching chapter. The field specs live in [Chapter 28 — Configuration reference](./28-configuration-reference.md).**
> Chapter 28 is generated from the `Workspace` struct and checked by a test, so it cannot drift:
> go there for a field's name, type, default, and which BNK line it applies to. This chapter
> explains what each block is *for*, which combinations make sense, and the judgement calls a
> schema table cannot express.

This chapter is the guided tour of the per-workspace `config.yaml`. If you've read [Chapter 6 — Workspaces](./06-workspaces.md) you've seen the on-disk layout; this chapter zooms in on the YAML file that drives everything else (`init`, `up`, `down`, `cluster up`, the test suite, the SSH targets, the execution backends).

You don't usually edit this file by hand. `roksbnkctl init` generates it interactively; later runs read it. But because every other knob in the tool reads from here, it's worth knowing what each block means and what happens when you leave one out. Every section below links to the part of Chapter 28 that lists its fields; what you get here is the reasoning around them.

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

Every block except `ibmcloud:`, `cluster:`, and `tf_source:` is optional. Omit a block and the tool falls through to either a documented default or the upstream HCL's own default for terraform variables. The `prefix:` field and `resources:` block are also optional — omit them and the workspace renders a sparse `terraform.tfvars` that falls through to the upstream module default names.

That sketch is not the whole schema. A workspace can also carry `gateway:` (the Gateway API resources installed after BNK), `registry:` (an air-gap mirror), `state:` (remote Terraform state — [Chapter 12a](./12a-remote-state.md)), `bnkforge:` and `agent:`. Each has its own chapter, and every field of every block is listed in [Chapter 28](./28-configuration-reference.md). The sections below walk the blocks you meet first.

## `prefix:`

```yaml
prefix: acme-eu
```

The field spec is [Chapter 28 §`Workspace`](./28-configuration-reference.md#workspace).

The `init` interview asks for a workspace **prefix** and derives every account-scoped resource name from it — `acme-eu` becomes cluster `acme-eu`, VPC `acme-eu-cluster-vpc`, TGW `acme-eu-tgw`, COS `acme-eu-registry-cos`, jumphosts `acme-eu-jh-tgw` / `acme-eu-jh-<zone>`. This stops two workspaces in the same IBM Cloud account from colliding on the old shared `tf-*` default names.

A prefix must be a lowercase DNS-style label — start with a letter, then `[a-z0-9-]`, no trailing hyphen — and **35 characters or fewer**. The 35-char cap is the ROKS cluster-name limit, the tightest of all the resource types. `roksbnkctl` validates the prefix — and every name it derives — at `init` time and re-prompts on overflow; there is no silent truncation. An **empty** `prefix` keeps the legacy sparse render (no name variables emitted at all), so old configs are unaffected. The full derivation table, the per-resource length/charset limits, the source citations, and the override path live in [Chapter 13 §"Resource naming & collision avoidance"](./13-terraform-variables.md#resource-naming--collision-avoidance).

## `ibmcloud:`

```yaml
ibmcloud:
  region: ca-tor
  resource_group: default
  api_key_source: keychain
  api_key_b64: ""
```

The field spec is [Chapter 28 §`IBMCloudCfg`](./28-configuration-reference.md#ibmcloudcfg).

This block is the account context every phase runs against. `region` is the one field with no default worth having — it decides where the cluster, the VPCs and the COS instance are created, and there is no sensible guess, so `init` prompts for it and a programmatic load of a config without it fails rather than picking one.

`api_key_source` pins the credential resolver to a single source. Leaving it empty is the normal case: the resolver then walks its whole chain (env → keychain → the workspace's own `api_key_b64` → a hidden TTY prompt). Pin it when you want a failure rather than a fallback — a CI runner that must use the environment variable and must not silently prompt, say.

`api_key_b64` is the last-resort inline copy of the key, for hosts with no usable OS keychain (WSL2 without libsecret is the common one). Base64 is **obfuscation, not encryption**: anyone holding the file can decode it instantly, so treat `config.yaml` as a plaintext credential — mode `0600`, never committed, rotated if the host is shared. The plaintext field name `api_key:` is **rejected** at load time; `roksbnkctl` refuses to read a workspace config that contains it, and the encoded form is the only inline path. Full discussion in [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md).

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

The field spec is [Chapter 28 §`ClusterCfg`](./28-configuration-reference.md#clustercfg). The rest of this section is the reasoning behind the fields that have some.

**Create or adopt.** `create: true` builds a ROKS cluster; `create: false` adopts one that already exists, looked up by `name` (a cluster name or ID) in `ibmcloud.resource_group`. Adopting is the common case: most estates build clusters to their own standards and ask `roksbnkctl` only for BNK. The cluster is deliberately **not** part of the `resources:` block below; it kept its own pair of fields because it predates that block and because a cluster is the one thing a workspace cannot do without. When a `prefix` is set, `init` fills `cluster.name` with the prefix itself — the cluster name carries no suffix, for the reason given in [Chapter 13](./13-terraform-variables.md#why-the-cluster-name-takes-no-suffix).

**Quote the OpenShift version.** `openshift_version: 4.20` is a YAML float, and `4.20` as a float is `4.2`. Write `openshift_version: "4.20"`. Leaving it empty is legitimate and takes IBM's current default, which moves over time — pin it when a run has to be reproducible.

**`workers_per_zone` is per zone.** ROKS spans three availability zones in an MZR region, so `2` is a six-node cluster.

**`public_gateway: false` builds a disconnected cluster, and that is a bigger decision than one boolean.** It removes the public gateway from every cluster subnet, so the workers have no Internet egress at all. Everything they used to pull now has to be reachable privately: a mirror registry they can route to, VPEs or private service endpoints for the IBM Cloud services the install touches, and `disconnected` (or F5 License Proxy) licensing instead of the connected JWT path. Get one of those wrong and the failure shows up much later, as an image pull that hangs. [Chapter 10a §"A truly disconnected cluster"](./10a-air-gapped-install.md) is the checklist. Note the scope: this governs **worker** egress only — the cluster master keeps its public API endpoint, so `kubectl` still works from your desk.

**Worker sizing.** Leave `worker_flavor` unset and the cluster module auto-selects the smallest profile meeting `min_worker_vcpu_count` and `min_worker_memory_gb`. The auto-select only considers the `bx2` family, so any other profile is unreachable without naming it in `worker_flavor` — F5's approved reference cluster runs `cx3d.8x20`, which no combination of minimums can produce.

**`vpc_cidr` is what lets two clusters share a Transit Gateway.** Left empty, the VPC uses IBM's `auto` address-prefix management, which hands *every* VPC in a region the same three prefixes. Two `roksbnkctl`-built clusters in one region then overlap, and a Transit Gateway joining them cannot route to both — it blackholes one, and what you see is intermittent image-pull timeouts rather than a routing error (issue #46). Give each cluster its own block whenever they must share a gateway, which is the norm for disconnected installs, since the cluster reaches its private mirror across that gateway. The block is split into three per-zone prefixes of `/n+2`, and each cluster subnet is the first `/n+8` of its zone — so a `/16` gives 256-address subnets (today's size), a `/17` gives 128, and a `/18` gives 64. `/18` is therefore the smallest value Terraform accepts, and `/16` is the one you want unless address space is tight. It is meaningful only for a **created** VPC; an adopted VPC keeps its own prefixes, and changing the value after the fact warns rather than silently re-planning.

**`existing_subnet_ids` is the other half of "bring your own network".** Adopting the VPC alone is not enough: in an estate that allocates address space centrally, the *subnets* carry the ACLs and routing that make them acceptable, and a cluster dropped into freshly-created subnets sits outside all of it. Supply one subnet ID per zone, **in zone order**. Each subnet's zone is read from the subnet itself, so a reordered list places the cluster differently rather than failing — check the order. It requires `resources.cluster_vpc: {create: false, existing: <vpc-id>}`, because a subnet cannot be adopted independently of the VPC containing it. `ROKSBNKCTL_EXISTING_SUBNET_IDS` sets it from the environment, comma-separated.

**`network_mode` is create-time only, and enforced.** Omitted means `single-nic`, which is what every cluster this tool builds is; the field exists to *name* that, not to change it. `multi-nic` requires BNK 2.4+. There is no in-place conversion between the two — Terraform would plan a replacement of a running cluster — so a workspace whose `network_mode` contradicts the cluster recorded in `cluster-outputs.json` is **refused** rather than planned. See [Chapter 28 §`ClusterCfg`](./28-configuration-reference.md#clustercfg) for which line each value belongs to.

The `cluster:` block translates to the terraform variables `create_roks_cluster`, `openshift_cluster_name`, `roks_cluster_id_or_name`, `openshift_cluster_version`, `roks_workers_per_zone`, `cluster_public_gateway`, `cluster_vpc_cidr` and `cluster_network_mode` — see [Chapter 13](./13-terraform-variables.md) and [Chapter 29](./29-terraform-variable-reference.md) for the full mapping.

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

The field spec — every toggle, plus the jumphost sizing and security-group keys that also live in this block — is [Chapter 28 §`ResourcesCfg`](./28-configuration-reference.md#resourcescfg) and [§`ResourceToggle`](./28-configuration-reference.md#resourcetoggle). What the table below adds is the *mapping*: which terraform variables each toggle drives, and when declining one makes `init` ask for an existing resource to adopt instead.

| Sub-block | Renders into | Asks for `existing` when… |
|---|---|---|
| `transit_gateway` | `create_roks_transit_gateway` (+ `roks_transit_gateway_name`) | declined **and** the TGW jumphost is enabled (it needs a TGW) |
| `registry_cos` | `create_roks_registry_cos_instance` (+ `roks_cos_instance_name`) | declined |
| `cert_manager` | `install_cert_manager` | — |
| `bnk` | `deploy_bnk` | — |
| `tgw_jumphost` | `testing_create_tgw_jumphost` (+ `testing_tgw_jumphost_name`) | — |
| `cluster_jumphosts` | `testing_create_cluster_jumphosts` (+ `testing_cluster_jumphost_name_prefix`) | — |
| `client_vpc` | `testing_create_client_vpc` (+ `testing_client_vpc_name`) | TGW jumphost enabled but you decline creating a new client VPC for it |
| `cluster_vpc` | `use_existing_cluster_vpc` + `existing_cluster_vpc_id`, emitted only when adopting | declining it *is* the adoption — `existing:` takes the VPC **ID**, and it is the prerequisite for `cluster.existing_subnet_ids` |

One extra key, `client_region`, is a plain **string** rather than a toggle: the region the testing client (TGW jumphost + client VPC) is installed in, set when `init` asks where to install the test client. It renders into `testing_client_vpc_region` (omitted → the terraform default applies) and seeds the regions `roksbnkctl cleanup` scans.

The block is **optional** — omit it and the sparse render (upstream module default names) applies; a fresh `init` writes it in full. For what each of those terraform variables defaults to when a toggle is absent, see [Chapter 29](./29-terraform-variable-reference.md).

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

Every field here is optional — leave the block out entirely and you get the upstream HCL's defaults. The field spec is [Chapter 28 §`BNKCfg`](./28-configuration-reference.md#bnkcfg), with the sub-blocks broken out as [§`BNKTrustedProfileCfg`](./28-configuration-reference.md#bnktrustedprofilecfg), [§`BNKCertManagerCfg`](./28-configuration-reference.md#bnkcertmanagercfg), [§`BNKFLPCfg`](./28-configuration-reference.md#bnkflpcfg) and [§`BNKPreflightCfg`](./28-configuration-reference.md#bnkpreflightcfg). Chapter 28's `line` column matters here more than anywhere else in the config: a good part of `bnk:` is 2.4-only, and `manifest_version` is the only thing that selects the line.

**`cneinstance_size` is passed through unvalidated, on purpose.** The legal set of sizes is a property of the BNK manifest, not of this tool. Hardcoding a list would go stale the first time F5 adds one and would then refuse a size the product supports, so a size a given manifest does not define is rejected by the operator on the cluster, not here. `Tiny` is what the BNK 2.4 install guide uses, and on ROKS it is the only size that runs: everything above it requests hugepages (`Small` 4 GiB, `Medium` 8 GiB) that the platform has no supported way to allocate. Do not reach for `bnk.hugepages` — it is a no-op on ROKS and issue #203 explains why. Capacity comes from `bnk.tmm_replicas` and node size instead; see Appendix C.

**`gslb_datacenter_name` is the whole feature.** It is what the CNEInstance calls itself in GSLB, and it is emitted whenever set. There was also a `gtm:` block carrying the BIG-IP DNS url, username and password; it was **removed in #227** because nothing read it — the f5ingress controller binary contains no occurrence of the environment variables it produced, on either BNK line. A `config.yaml` still carrying `bnk.gtm` needs no edit: an unrecognised key is ignored.

**`trusted_profile.service_account` is a matcher, not a pointer — and getting it wrong fails silently.** The IBM Cloud Trusted Profile is the identity the CNE controller assumes to manage the VPC network attachments it creates for TMM, so it works without a stored API key. IBM's trust relationship compares a pod's service-account token against `crn` / `namespace` / `name` with `EQUALS`. Left empty, `roksbnkctl` derives the name FLO actually creates — `f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount` — and the match holds. Write a different name and nothing can assume the profile, **and nothing reports an error**: the pod simply loses its IBM Cloud permissions, and it surfaces much later as an authorization failure at VPC-attachment time that names neither this setting nor the profile. The profile looks correct in the IBM Cloud console the whole time.

So set it only if you can *also* make FLO name the account differently — and `roksbnkctl` cannot, because FLO creates that account when it reconciles the CNEInstance and the CNEInstance spec has no service-account field. In practice this field exists for estates driving FLO by some other means. The derived name is safe to share across clusters: uniqueness comes from the profile name (which carries the cluster name) and from the link's cluster CRN, not from the account name.

**`cert_manager:` overrides coordinates, not the decision.** `bnk.cert_manager.namespace` / `.version` say *where* and *which chart*; whether cert-manager is installed at all stays on `resources.cert_manager.create`. The two are easy to confuse because they share a name.

`license_mode: f5licenseproxy` additionally requires the FLP phase to be up (`roksbnkctl flp up`) before `bnk up` runs, so the install has a proxy to point at. The proxy's endpoint and root CA are **not** config — they are produced by `flp up` and read back from `flp-outputs.json`.

### `bnk.network:` — data-plane subnets + TMM self-IPs

BNK's data plane (TMM) needs per-availability-zone VLAN subnets, SNAT/VIP ranges, and self-IPs. Leave `bnk.network` out entirely and the BNK install-guide defaults apply. Set it to match your cluster's fabric — `roksbnkctl init` prompts for every field (opt in at *"Customize BNK networking?"*, seeded with the values shown below), or hand-write the block.

The field spec is [Chapter 28 §`BNKNetworkCfg`](./28-configuration-reference.md#bnknetworkcfg) and, for one zone's six CIDRs, [§`BNKZoneCfg`](./28-configuration-reference.md#bnkzonecfg).

Provide **all three zones** when you set `zones` — supplying zones replaces the install-guide defaults entirely (they render `cneinstance_network_zones`, driving the cloud-network-mapping ConfigMap and the external/internal F5SPKVlan CRs). The YAML above shows zone 1's defaults; zones 2 and 3 follow the same shape one octet up (`10.156.16.0/24` and `10.157.17.0/24` externally), and the full default set is the `cneinstance_network_zones` default in `terraform/modules/cne_instance/modules/cneinstance/variables.tf`. Zone *names* are derived from the region and aren't configurable.

`vlan_prefixlen` and `tmm_k8s_routes` are network-wide, shared across all zones. **`vlan_prefixlen` is independent of your VLAN CIDRs and is never derived from them** — nothing validates the two against each other. Usually you want them to agree; a deliberate disagreement, paired with static routes, is how a smaller or larger directly-connected block is forced and the remainder steered. The mask can also differ **between the two VLANs** via `vlan_prefixlen_external` / `vlan_prefixlen_internal`: TMM can front a `/23` externally while the internal side is a `/26`, which one shared scalar could not express. Unset either override to inherit the shared value; `ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL` and `ROKSBNKCTL_VLAN_PREFIXLEN_INTERNAL` set them from the environment.

`tmm_k8s_routes` is the pod CIDR TMM installs a route toward (`TMM_K8S_ROUTES`) so it can reach the backend pods on the internal data path. The default is the ROKS default pod subnet — set it if your cluster's isn't.

**What each zone CIDR becomes on 2.4.** The same values drive a different set of
objects on each line, which is why they are marked "2.3 + 2.4" rather than being
split: on 2.3 they build the `cloud-network-mapping` ConfigMap and the
`F5SPKVlan` pair, and on 2.4 they become the `Infra` CR's IPAM pools —
`ext_vlan_cidr` is the `external-vlan-ipam` pool and the next hop of every
generated route, `int_snat_cidr` is `egress-snat-ipam`, and `int_vip_cidr` is
`vip-listener-ipam`. The exceptions are the ones chapter 28 marks **2.3**:
`int_vlan_cidr` (2.4 has no internal VLAN), both self-IPs (2.4 allocates TMM's
VLAN addresses from `external-vlan-ipam`), and the three `vlan_prefixlen*`
fields (a 2.4 IPAM pool is a CIDR, and a CIDR states its own mask).

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

The field spec is [Chapter 28 §`TestCfg`](./28-configuration-reference.md#testcfg), broken out as [§`ThroughputCfg`](./28-configuration-reference.md#throughputcfg), [§`ConnectivityCfg`](./28-configuration-reference.md#connectivitycfg) and [§`DNSCfg`](./28-configuration-reference.md#dnscfg). `duration` and `streams` are iperf3's `-t` and `-P`.

**`throughput.image` is the in-cluster iperf3 *server* fixture**, not the client. `roksbnkctl test throughput` deploys that image as a pod in the cluster and then runs an iperf3 *client* against it — and the client's image is a separate thing, chosen by the execution backend (the `docker` and `k8s` backends run F5's bundled `ghcr.io/jgruberf5/roksbnkctl-tools-*` tool images; see [Chapter 17](./17-execution-backends.md)). Changing `throughput.image` moves the server end only.

The default server image, `networkstatic/iperf3:latest`, **runs as root**. OpenShift's SCC admission overrides the pod's user with a project-allocated UID, so it works on ROKS; plain Kubernetes with the `restricted` Pod Security standard does not, and the pod is refused with a `RunAsNonRoot` admission error. On a non-OpenShift cluster, point `throughput.image` at the bundled image instead — `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<version>`, which is built to run as UID 1000.

`connectivity.extra_hosts` adds targets to the reachability probe alongside the canonical IBM/F5 endpoints. It exists mainly for disconnected estates: a probe against a public host there reports a failure that says nothing useful, so give it something the cluster can actually reach.

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

An empty `type` is treated as `embedded` (legacy / forgot-to-set). The field spec for `repo` / `ref` / `path` is [Chapter 28 §`TFSourceCfg`](./28-configuration-reference.md#tfsourcecfg); the table above is about which `type` to pick, not what the fields are.

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

Each entry has `host`, `user`, an optional `port`, and exactly one of `key_path` (a PEM file) or `key_source` (`agent`, or `tf-output:<name>` to pull the key from a Terraform output). The field spec is [Chapter 28 §`TargetCfg`](./28-configuration-reference.md#targetcfg).

The deep reference is [Chapter 15 — SSH targets](./15-ssh-targets.md), and the user-facing prose is [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md). This chapter just notes the block's place in the overall config.

You don't typically edit this block by hand. `roksbnkctl up` auto-populates `jumphost` post-apply, and `roksbnkctl targets add ...` populates the rest.

## `exec:` — execution-backend defaults

```yaml
exec:
  ibmcloud:  { backend: local }
  iperf3:    { backend: k8s }
  terraform: { backend: local }
```

Per-tool defaults for the `--backend` system. Each entry is keyed by the tool name (`ibmcloud`, `iperf3`, `terraform`, and others as the matrix grows) and its one field, `backend`, is specified in [Chapter 28 §`ExecToolCfg`](./28-configuration-reference.md#exectoolcfg). The table below is the enum of values that field accepts:

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

The field spec is [Chapter 28 §`COSCfg`](./28-configuration-reference.md#coscfg) and [§`COSUpload`](./28-configuration-reference.md#cosupload).

This is the *orchestration* COS — the bucket holding the FAR auth tarball and the subscription JWT, which is a different thing from the registry COS instance the `resources:` block can create. `instance` / `bucket` / `region` are honoured by **both** the terraform render (`ibmcloud_cos_instance_name` / `ibmcloud_resources_cos_bucket` / `ibmcloud_cos_bucket_region`) and the `registry` FAR-file resolver, so a customer-owned bucket is used consistently across both.

**The bucket `init` provisions is not the bare default.** COS bucket names share one global namespace, like S3, so a generic base such as `bnk-artifacts` frequently collides with a bucket some other account already owns. When `init` provisions the supply chain it therefore appends a short token derived from your **account ID** — `bnk-artifacts-<first 12 of the account id>` — and writes that name into `cos.bucket`. Keying the suffix off the account (rather than the COS instance) makes the name both globally unique and *stable and discoverable*: a second workspace created from the same account's API key derives the same name and reuses the bucket the first one provisioned, instead of making a second copy of the same artefacts.

`upload` performs pre-flight uploads from local files into that bucket before the phases that read them run — useful in CI, where the supply-chain artefacts are produced by the pipeline rather than staged by hand.

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
| `cos.*` | No pre-flight uploads, and both the terraform render and the `registry` FAR resolver fall back to the built-in orchestration-COS defaults (`bnk-supply-chain` / `bnk-artifacts` / `us-south`) — which are also the upstream HCL's. |

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
