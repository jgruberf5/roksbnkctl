# Configuration reference

Field-by-field schema reference for the workspace `config.yaml`. Source of truth is the [`Workspace` struct](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/config/workspace.go) in `internal/config/workspace.go`; this chapter is the human-readable rendering of those tags.

[Chapter 12 — Workspace config](./12-workspace-config.md) is the *teaching* chapter; this one is the *lookup* chapter. Use chapter 12 to learn the shape, use this one to look up the type of a specific field.

## File location and lifecycle

| Property | Value |
|---|---|
| Path | `~/.roksbnkctl/<workspace>/config.yaml` |
| Default workspace | `default` (auto-created on first run) |
| Overridable home | `ROKSBNKCTL_HOME` env var (defaults to `~/.roksbnkctl/`) |
| Mode | `0644` |
| Created by | `roksbnkctl init` |
| Updated by | `roksbnkctl init --upgrade-tf`, `roksbnkctl kubeconfig --download`, hand-editing |

The file is hand-editable; YAML is parsed with [`gopkg.in/yaml.v3`](https://pkg.go.dev/gopkg.in/yaml.v3) so anchors and aliases work but are not idiomatic for this file. Plaintext credentials in any of the regex-matched secret fields (`api_key`, `apikey`, `password`, `token`, `secret_access_key`, `hmac_secret`) are rejected at load time — the file fails to parse with a clear error. Base64-encoded credentials in `ibmcloud.api_key_b64` are allowed (the field name doesn't match the rejection regex). See [Chapter 14](./14-credentials-resolver.md).

## Top-level structure

```yaml
prefix:          # optional; workspace name-prefix base
ibmcloud:        # required
cluster:         # required
resources:       # optional; per-resource create/existing toggles
bnk:             # optional; populates upstream HCL bnk variables
registry:        # optional; the mirror `registry replicate` pushes to, and installs pull from
test:            # optional; populates test.* settings
tf_source:       # required (defaults to embedded if omitted)
cos:             # optional; supply-chain auto-upload
targets:         # optional; populated automatically by up's post-apply hook
exec:            # optional; per-tool default-backend map
bnkforge:        # optional; opt-in BNK Forge cluster registration
```

The order of the top-level keys in the file doesn't matter; YAML is a mapping. The order shown above is the canonical render order produced by `roksbnkctl init`.

## `prefix:` field

```yaml
prefix: acme-eu
```

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `prefix` | string | (empty) | lowercase RFC-1123-style label: starts with a letter, `[a-z0-9-]`, no trailing hyphen, **≤ 35 chars** | *(since `v1.8.0`)* The base for every account-scoped IBM Cloud resource name. `roksbnkctl` derives the cluster, VPCs, Transit Gateway, COS, and jumphost names from it and renders them into `terraform.tfvars`. The 35-char cap is the ROKS cluster-name limit (the tightest); a valid prefix guarantees every derived name fits its own limit. **Empty / omitted ⇒ legacy behaviour**: the sparse, name-less render that falls through to upstream module defaults (backward-compatible). See [Chapter 13 §"Resource naming & collision avoidance"](./13-terraform-variables.md#resource-naming--collision-avoidance) for the full derivation table and limits. |

The derived names are deterministic, so they are re-rendered on every `up` / `plan` / `apply`; the `prefix` is the single source of truth. To pin one generated name to something off-convention, override the matching variable via `terraform.tfvars.user` or `--var-file` — the override layers last and wins.

## `ibmcloud:` block

```yaml
ibmcloud:
  region: ca-tor
  resource_group: default
  api_key_source: keychain
  api_key_b64: <base64>
```

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `region` | string | — (prompted by `init`) | any IBM Cloud region: `us-south`, `us-east`, `ca-tor`, `eu-de`, `eu-gb`, `jp-tok`, `au-syd`, etc. | The IBM Cloud region for all cluster + COS resources. Crosses module boundaries — must match the upstream HCL's `ibmcloud_cluster_region`. |
| `resource_group` | string | `default` | any RG name in the account | The resource group cluster + COS resources are provisioned into. |
| `api_key_source` | string | (resolver chain runs) | `env` \| `keychain` \| `config` \| `prompt` | Pins the resolver to a single source rather than walking the chain. Set explicitly when you want predictable behaviour in CI. See [Chapter 14 §"Pinning a single source"](./14-credentials-resolver.md#pinning-a-single-source). |
| `api_key_b64` | string | — | base64-encoded API key | **Obfuscation, not encryption** — anyone with file-read access decodes instantly. For single-user dev only; never commit. The field name deliberately doesn't match the plaintext-secret rejection regex. |

## `cluster:` block

```yaml
cluster:
  create: true
  name: tf-openshift-cluster
  openshift_version: "4.20"
  workers_per_zone: 1
  min_worker_vcpu_count: 16     # optional; worker-flavor auto-select floor
  min_worker_memory_gb: 64      # optional; worker-flavor auto-select floor
```

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `create` | bool | `true` | `true` \| `false` | `true` provisions a new ROKS cluster; `false` attaches to an existing one (set `name` to the existing cluster's name or ID). |
| `name` | string | — (prompted by `init`) | RFC 1123 DNS label | The cluster name. Used as the OpenShift cluster identity and as the resource group disambiguator. |
| `openshift_version` | string | `4.20` | any version IBM Cloud's catalog accepts | Pinned to a minor (`4.20`) rather than patch — IBM ships continuous patch updates within a minor. Leave empty for "latest". |
| `workers_per_zone` | integer | `1` | 1+ | Worker nodes provisioned per availability zone. Multiply by the zone count (typically 3) for the total cluster size. BNK needs ≥1 worker; production deployments use 2-3 per zone. |
| `min_worker_vcpu_count` | integer | `16` | 2+ | Minimum vCPUs when the cluster module auto-selects the worker flavor (it picks the smallest `bx2` profile meeting **both** minimums). Renders into `roks_min_worker_vcpu_count`. `0`/omitted keeps the HCL default. Only meaningful when `create: true`. |
| `min_worker_memory_gb` | integer | `64` | 8+ | Minimum memory (GB) for the same worker-flavor auto-select. Renders into `roks_min_worker_memory_gb`. `0`/omitted keeps the HCL default. |
| `existing_subnet_ids` | list | (empty) | three subnet ids, zone order | Place the cluster in subnets that **already exist** instead of creating them. Renders `use_existing_cluster_subnets` + `existing_cluster_subnet_ids`. Requires `resources.cluster_vpc: { create: false, existing: <vpc-id> }` — a subnet cannot be adopted independently of its VPC, and terraform fails at plan with that message if you try. Each subnet's **zone is read from the subnet**, not from the region default, so they may be in any zone order the VPC owner chose. For estates where address space is allocated centrally: adopting the VPC alone still creates three subnets inside it, leaving the cluster outside whatever ACLs and routing made that network acceptable. |
| `vpc_cidr` | string | (empty) | a `/18` or larger IPv4 CIDR | Address block a **new** cluster VPC's three per-zone prefixes are carved from (`10.242.0.0/16` → `10.242.0.0/18`, `.64.0/18`, `.128.0/18`) → `cluster_vpc_cidr`. Empty leaves IBM's `auto` management, which gives **every** VPC in a region the same prefixes — two such clusters cannot share a Transit Gateway, which blackholes traffic for one of them silently. Set a distinct block per cluster when they share a gateway; see [Sharing a Transit Gateway](./09a-transit-gateway-sharing.md#first-give-each-cluster-vpc-its-own-address-block). Split three ways, so `/18` is the smallest usable block. CREATE-time only — ignored when adopting an existing VPC. The block a cluster was actually created from is recorded alongside it, and changing this afterwards **warns**: the new value is ignored, because an existing VPC's prefixes cannot be re-addressed without replacing its subnets. A future release will refuse rather than ignore it. Setting it and leaving it alone — the normal case — says nothing. Env: `ROKSBNKCTL_CLUSTER_VPC_CIDR`. |
| `network_mode` | string | `single-nic` | `single-nic`, `multi-nic` | How the worker nodes are attached → `cluster_network_mode`. Omitted means `single-nic`, which is what every cluster built with this tool so far is; the setting exists to *name* that, not to change it. `multi-nic` needs a BNK release that expresses it — the pairing is checked against the [support matrix](#bnk-release-and-network-mode) at plan time, not discovered against a live cluster. **CREATE-time only and enforced:** a cluster is never converted between modes in place, so changing this on a workspace that already has a cluster is refused rather than planned (what terraform would otherwise plan is a *replacement* of the running cluster that reads like an update). Env: `ROKSBNKCTL_CLUSTER_NETWORK_MODE`. |

## `resources:` block

```yaml
resources:
  transit_gateway:   { create: true }
  registry_cos:      { create: true }
  cert_manager:      { create: true }
  bnk:               { create: true }
  tgw_jumphost:      { create: false }   # the optional testing client — OFF by default
  cluster_jumphosts: { create: false }
  client_vpc:        { create: false, existing: my-shared-client-vpc }
  client_region:     ca-tor               # testing-client region
  testing_jumphost_profile: ""            # optional; pin a profile for ALL jumphosts
  testing_min_vcpu_count:   4             # optional; jumphost auto-select floor
  testing_min_memory_gb:    8             # optional; jumphost auto-select floor
```

*(since `v1.8.0`)* Per-resource create/adopt toggles, written by the `init` interview. Each key is a `{create, existing}` pair: `create: true` provisions a new prefix-named resource; `create: false` declines it and (when a still-enabled resource depends on it) `existing:` names the pre-existing resource to consume instead.

| Sub-block | `create` default | `existing` consumed by | Renders into |
|---|---|---|---|
| `transit_gateway` | `true` | `create_roks_transit_gateway`; `existing` (name or id) attaches the cluster VPC to that gateway via the `tgw` phase | `create_roks_transit_gateway`, `roks_transit_gateway_name`, `tgw_connection_target` |
| `registry_cos` | `true` | `create_roks_registry_cos_instance`; `existing` → `roks_cos_instance_name` | `create_roks_registry_cos_instance`, `roks_cos_instance_name` |
| `cert_manager` | `true` | — | `install_cert_manager` |
| `bnk` | `true` | — | `deploy_bnk` |
| `tgw_jumphost` | `false` | — | `testing_create_tgw_jumphost`, `testing_tgw_jumphost_name` |
| `cluster_jumphosts` | `false` | — | `testing_create_cluster_jumphosts`, `testing_cluster_jumphost_name_prefix` |
| `client_vpc` | `false` (created on demand for the TGW jumphost) | `existing` → `testing_client_vpc_name` when not creating one | `testing_create_client_vpc`, `testing_client_vpc_name` |

`client_region` *(since `v1.9.0`)* is the odd one out — a plain **string**, not a `{create, existing}` toggle. It's the IBM Cloud region the testing client (TGW jumphost + client VPC) is installed in, letting the test client live in a **different region from the cluster**. The `init` interview sets it when you answer the *"Add a testing client?"* / region prompt; omitted, it renders nothing and the terraform default (`testing_client_vpc_region`) applies. It also seeds the regions [`roksbnkctl cleanup`](./11-tearing-down.md#which-regions-it-scans) scans. Renders into `testing_client_vpc_region`.

`testing_jumphost_profile` / `testing_min_vcpu_count` / `testing_min_memory_gb` are plain fields (like `client_region`) that size **all** the testing jumphosts (both the TGW jumphost and the per-zone cluster jumphosts). `testing_jumphost_profile` pins an explicit IBM Cloud instance profile; leave it empty to auto-select the smallest profile meeting the two minimums. They render into `testing_jumphost_profile` / `testing_min_vcpu_count` / `testing_min_memory_gb`; omitted, the terraform defaults (`4` vCPU / `8` GB, auto-select) apply.

Each `{create, existing}` entry:

| Field | Type | Default | Notes |
|---|---|---|---|
| `create` | bool | per the table above | `true` provisions a new, prefix-named resource. `false` declines it. |
| `existing` | string | (empty) | The name or ID of a pre-existing resource to adopt — only consumed when `create: false` **and** a still-enabled resource depends on this one (e.g. the TGW jumphost needs a TGW). Renders into the resource's `*_name` variable with the matching `create_* = false` toggle. |

The block is **additive and optional**: an omitted `resources:` block (any pre-`v1.8.0` `config.yaml`) loads unchanged, and a fresh `init` writes the full block with sensible defaults. The cluster itself is **not** in this block — its create/adopt toggle is the existing `cluster.create` + `cluster.name` pair (Name doubles as the existing cluster id/name when `create: false`). See [Chapter 13 §"Resource naming & collision avoidance"](./13-terraform-variables.md#resource-naming--collision-avoidance).

## `bnk:` block

```yaml
bnk:
  cneinstance_size: Small
  far_repo_url: repo.f5.com
  manifest_version: 2.3.0-3.2598.3-0.0.170
  flo_namespace: f5-bnk               # optional; FLO namespace
  flo_utils_namespace: f5-utils       # optional; utilities namespace
  gslb_datacenter_name: ""            # optional; CNEInstance GSLB datacenter
  cert_manager:                       # optional; cert-manager coordinates
    namespace: cert-manager
    version: v1.17.3
```

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `cneinstance_size` | string | `Small` | `Small` \| `Medium` \| `Large` | Sizing for the deployed CNE Instance. Renders into the upstream HCL `cneinstance_deployment_size` variable. |
| `far_repo_url` | string | `repo.f5.com` | URL of a Docker-compatible image registry | The image registry FLO pulls FAR container images from. Override for air-gapped installs pointing at a local mirror. |
| `manifest_version` | string | `2.3.0-3.2598.3-0.0.170` | a published `f5-bigip-k8s-manifest` chart version | Pins the FLO + CIS versions transitively (both are extracted from the manifest chart). |
| `flo_namespace` | string | `f5-bnk` | RFC 1123 namespace label | Namespace the F5 Lifecycle Operator installs into (`flo_namespace`). Set for multi-tenant clusters or to avoid a namespace collision. |
| `flo_utils_namespace` | string | `f5-utils` | RFC 1123 namespace label | Namespace for the F5 utility components (`flo_utils_namespace`). |
| `trusted_profile.service_account` | string | (derived) | a Kubernetes service account name | The account allowed to **assume** the CNE controller's IBM Cloud Trusted Profile → `flo_trusted_profile_sa_name`. Empty derives what FLO creates: `f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount`. The IAM trust rule is a **matcher**, not a pointer: IBM compares a pod's service-account token against `crn`/`namespace`/`name` with `EQUALS`, so a name that does not match the account the CNE controller actually runs as means **nothing can assume the profile, and nothing reports an error**. Set this only if you can also make FLO name the account differently — roksbnkctl cannot, since FLO creates it when it reconciles the CNEInstance and that spec has no service-account field. **Safe to share across clusters:** uniqueness comes from the profile name, which carries the cluster name, and from the link's cluster CRN. The same value also drives the privileged-SCC binding for that account, so the two cannot drift. Env: `ROKSBNKCTL_TRUSTED_PROFILE_SA`. |
| `trusted_profile.roles` | list | `[Viewer, Editor]` | IAM role names | Roles granted to that profile, scoped to the cluster's **own** VPC (`serviceName=is`, `vpcId=<cluster vpc>`) → `flo_trusted_profile_roles`. `Editor` is what lets the controller manage TMM's network attachments; narrowing it fails at attachment time on a running cluster rather than at apply, so change it only against a policy you have tested. Env: `ROKSBNKCTL_TRUSTED_PROFILE_ROLES` (comma-separated). |
| `gtm.url` / `gtm.username` / `gtm.password_b64` | string | (empty) | URL, user, base64 password | The BIG-IP DNS / GTM the GSLB datacenter registers **with** (`cneinstance_gtm_*`). `gslb_datacenter_name` supplies the datacenter's *name*; this supplies the thing it registers with. Empty disables GTM entirely. The password is base64 for obfuscation, not encryption — `ROKSBNKCTL_GTM_PASSWORD` takes the raw value. |
| `gslb_datacenter_name` | string | (empty) | any string | Optional CNEInstance GSLB datacenter name (`cneinstance_gslb_datacenter_name`). |
| `cert_manager.namespace` | string | `cert-manager` | RFC 1123 namespace label | Namespace cert-manager installs into (`cert_manager_namespace`). The install/skip toggle stays on `resources.cert_manager.create`. |
| `cert_manager.version` | string | (HCL default) | a published cert-manager chart version | Pins the cert-manager Helm chart (`cert_manager_version`). Useful for air-gap / compliance version pinning. |

All fields are optional; omitting renders the HCL's own defaults. See [Chapter 13 — Terraform variables](./13-terraform-variables.md) for the upstream defaults. Data-plane networking (per-AZ subnets, TMM self-IPs, VLAN prefix length, and the pod-route CIDR) lives under `bnk.network` — `roksbnkctl init` prompts for it (opt in at *"Customize BNK networking?"*), or see [Chapter 12 §`bnk.network`](./12-workspace-config.md). FLP settings live under `bnk.flp` (see the master table below); `bnk.flp.storage_class` sets the FLP's PVC StorageClass.

### BNK release and network mode

Two versions vary here, independently, and they interact.

**The BNK release** decides the terraform and the F5 CRDs. It is not a separate
setting — it is *derived* from `bnk.manifest_version`, which already pins the
release: `2.3.0-3.2598.3-0.0.170` is the `2.3` line. Deriving it is deliberate.
A second field naming the line could disagree with the manifest actually being
installed, and the failure from that disagreement would arrive as a CRD mismatch
against a live cluster, hours later.

**The cluster's network mode** decides what BNK has to attach to. It comes from
`cluster.network_mode`, is fixed when the cluster is built, and is written into
the cluster's own record so that later runs read what the cluster *is* rather
than what the config currently asks for.

Not every pairing exists:

| BNK line | `single-nic` | `multi-nic` |
|---|---|---|
| 2.3 | **yes** | **no** — 2.3 does not express the multi-NIC network attachments or CNEInstance options |
| 2.4 | **yes** | **yes** — provisional; see the note below |

A 2.4 install still drives a single-NIC cluster, including one created before
multi-NIC existed. That is what lets an existing deployment move to 2.4 without
touching its cluster. What does not exist is a conversion: a cluster is built in
one mode and stays in it.

The 2.4 row is **provisional**: it was written before 2.4 shipped and has never
been run against it. Every value in it is an expectation, not an observation.

`roksbnkctl` checks the pairing before it plans and refuses an unsupported one
there, rather than letting it fail against real infrastructure. The matrix is
data (`internal/config/support_matrix.yaml`), not a rule buried in code, so
adding a release is an edit to a table.

A release the matrix has never heard of **warns and proceeds**. The matrix ships
inside the binary, so an unknown line usually means the binary predates the
release — that is missing information, not a known incompatibility, and refusing
on it would make every build refuse every BNK release that shipped after it.
This matters most where the binary is pinned: the [BNK Forge
modules](./24a-bnk-forge-registration.md) pin the runner image by digest, so
"use a newer build" is not something a user choosing a BNK release can do.

Leaving `network_mode` unset is likewise not a claim. Only an **explicit** value
that contradicts the cluster's record is refused; silence defers to the record.
Configs are not always hand-written and durable — the Forge modules regenerate
`config.yaml` per step from a curated environment — so absence has to mean "no
opinion", not "single-nic, and I insist".

### `bnk.preflight:` — the reachability gate's timers

Before `bnk up` plans anything, it probes the mirror (and the F5 License Proxy, when one is
configured) from **every node, in every AZ**. These two fields tune that gate. Neither is a
terraform variable — they govern the check itself, not what is installed.

```yaml
bnk:
  preflight:
    reachability_retry_seconds: 180     # per target, before the verdict is believed
    reachability_timeout_seconds: 480   # for every node to report
```

| Field | Type | Default | Env var | Notes |
|---|---|---|---|---|
| `reachability_retry_seconds` | int | `180` | `ROKSBNKCTL_REACHABILITY_RETRY_SECONDS` | How long **one target** may keep failing before its verdict is believed. `0` means one shot. |
| `reachability_timeout_seconds` | int | `480` | `ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS` | How long to wait for **every node** to report. Clamped to stay above the retry budget. |

They are exposed because the right values are a property of the environment, not of `roksbnkctl`.
A Transit Gateway attachment is **asynchronous** — IBM programs the routes some time after the
connection reports `attached` — so a workspace that creates the VPC and joins the gateway in the
same run as the install can probe a path that is correct but not yet live. A probe 73 seconds
after attach has failed on a path that was healthy minutes later.

- **Raise the retry budget** when the gateway is attached in the same run, or the fabric is known
  to program routes slowly.
- **Set it to `0`** for a static, long-established gateway where a failure is never a race — one
  shot, fail immediately.

A success ends the retry immediately, so a healthy environment never pays the budget; only a
failing target does. `0` is distinguished from "unset", so setting it explicitly is honoured
rather than falling back to the default.

The timeout is **clamped** to stay above the retry budget. Raising only the retry would otherwise
produce a wait that gives up while the probe is still legitimately retrying — a timeout that
reads as a network problem but is really this file. The clamp is a floor, not a policy: a value
comfortably above the budget is used exactly as written.

The env vars matter for the CI path in particular, where the workspace is built by
`init --non-interactive --override-from-env` and there is no `config.yaml` to edit
([Chapter 7a](./07a-unattended-setup.md)). See
[Appendix A](./appendix-a-disconnected-roks-cluster.md) for the gate's full output and rationale.

## `test:` block

```yaml
test:
  throughput:
    image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:v0.9.0
    duration: 30
    streams: 8
    default_mode: north-south
  connectivity:
    extra_hosts:
      - https://www.example.com/healthz
      - https://internal.bnk.local/status
  dns:
    resolvers:
      google: "8.8.8.8:53"
      cloudflare: "1.1.1.1:53"
      gslb-vip: "169.45.91.5:53"
    default_target: www.example.com
```

### `test.throughput`

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `image` | string | `networkstatic/iperf3:latest` | any iperf3 Docker image | The image used for both server pod and client Job. The default runs as root and fails on OpenShift's `restricted-v2`; use the bundled image `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>` for SCC-clean installs. See [Chapter 22](./22-throughput-testing.md#the-bundled-image-and-the-runasnonroot-constraint). |
| `duration` | integer | `30` | 1-300 (seconds) | The iperf3 `-t` flag — test duration in seconds. |
| `streams` | integer | `8` | 1-128 | The iperf3 `-P` flag — parallel TCP streams. |
| `default_mode` | string | `north-south` | `north-south` \| `east-west` | Default `--mode` when not passed on the command line. |

### `test.connectivity`

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `extra_hosts` | list of string | (empty) | URLs | Each URL is probed via HTTP GET; pass criterion is a 2xx response. The v1.0 shape is a bare list — no per-host method, expected-status, or TLS-trust override. Use `--insecure` (session-wide) for self-signed certs. See [Chapter 20 §"Configuring extra_hosts"](./20-connectivity-testing.md). |

### `test.dns`

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `resolvers` | map[string]string | (empty) | name → `<ip>[:<port>]` | Friendly-name aliases for `--server <name>`. Lets workspace config push GSLB VIP addresses out of the command line. |
| `default_target` | string | (empty) | DNS name | Default `--target` when not passed on the command line. Useful for "always probe this name". |

## `matrix.yaml` — the performance grid

> **This is its own file, not part of `config.yaml`.** The [performance matrix](./22a-performance-matrix.md) grid lives in a **workspace-sibling** `matrix.yaml` — under `~/.roksbnkctl/<workspace>/`, *next to* `config.yaml`, never inside it. The grid is large, churns independently of deploy config, and is the kind of thing you diff in git per-campaign, so it stays out of the deploy-shaped `config.yaml`. `roksbnkctl test matrix` resolves it via `--file`, then `<workspace>/matrix.yaml`, then `./matrix.yaml`.

Four top-level keys. The example grid is `internal/test/testdata/matrix.example.yaml`; [Chapter 22a §"The matrix.yaml schema"](./22a-performance-matrix.md#the-matrixyaml-schema) walks it with worked cells.

```yaml
gateway:
  app_namespace: bnk-apps
  name: bnk-gateway
  http_section: http
  https_section: https
  tcp_section: tcp
fixtures:
  iperf3_server: true
  http_backend: true
  routes: true
endpoints:
  vsi-diff-vpc: { kind: vsi, target: jumphost }
  tmm-tcp:      { kind: address, host: 10.240.0.10, port: 5201 }
  tmm-http-128: { kind: url, url: "http://10.240.0.10/128" }
cells:
  - { name: "L4 512K diff-VPC", family: iperf3, client: vsi-diff-vpc, server: tmm-tcp, length: "512K", duration: 30, streams: 8 }
  - { name: "L7 http CPS 128B", family: l7, client: vsi-diff-vpc, server: tmm-http-128, l7: { mode: cps } }
```

### `gateway:` — existing-stack identity

Names the already-deployed BNK gateway so route fixtures can attach to it. Used **only** when `fixtures.routes` is true; the runner adds Routes, never listeners.

| Field | Type | Required | Notes |
|---|---|---|---|
| `app_namespace` | string | with `routes` | Namespace the fixtures + routes are created in. |
| `name` | string | with `routes` | The existing `Gateway` object the routes' `parentRefs.name` point at. |
| `http_section` / `https_section` / `tcp_section` | string | one+, with `routes` | Listener `sectionName`s on that Gateway the http / https / tcp routes bind to. **Must already exist.** Empty → that route isn't rendered. |
| `class_name` / `controller_name` / `bnkgateway_name` / `flo_namespace` | string | no | Descriptive identity; not needed to attach routes. |

### `fixtures:` — ephemeral runner-owned objects

The only cluster writes the matrix performs; all torn down after the run unless `--keep`, all skipped under `--dry-run`.

| Field | Type | Default | Notes |
|---|---|---|---|
| `iperf3_server` | bool | `false` | Deploy the L4 iperf3 server (the throughput fixture); the TCPRoute's backend. |
| `http_backend` | bool | `false` | Deploy an nginx backend serving `/128`, `/5k`, `/512k`; the L7 backend. |
| `routes` | bool | `false` | Apply TCPRoute + HTTPRoute (+ TLS HTTPRoute & self-signed Secret when `https_section` is set), attaching to `gateway.name`. |

### `endpoints:` — named `(placement, role)` anchors

A map of `name → { kind, … }`. The locality axis is implicit in which `vsi` endpoint a cell names as its client — there is no locality enum.

| `kind` | Fields | Resolves to |
|---|---|---|
| `vsi` | `target` (SSH target name) | an `ssh:<target>` jumphost — a traffic-source client |
| `address` | `host`, `port` (default `5201`) | an iperf3 TCP server (e.g. a TCPRoute VIP); an `iperf3` cell's `server` |
| `url` | `url` (full `http(s)://`) | an HTTPRoute target; an `l7` cell's `server`. The scheme selects cleartext vs TLS-terminate-at-TMM. |

### `cells:` — the grid (one row per report cell)

| Field | Applies to | Required | Notes |
|---|---|---|---|
| `name` | both | yes | Report label + `--only` glob target. |
| `family` | both | yes | `iperf3` \| `l7`. |
| `client` | both | no | Endpoint key of kind `vsi`; empty → run locally. |
| `server` | both | yes | Endpoint key: `address` for `iperf3`, `url` for `l7`. |
| `length` / `bytes` / `duration` / `streams` | `iperf3` | no | iperf3 `-l` / `-n` / `-t` / `-P`. |
| `l7.mode` | `l7` | yes (`l7`) | `cps` \| `tps` \| `throughput` — h2load flag preset. |
| `l7.clients` / `streams` / `threads` / `requests` / `duration` / `http1` | `l7` | no | h2load `-c` / `-m` / `-t` / `-n` / `-D` / `--h1`; override the mode preset. |

## `registry:` block

Selects the OCI registry `registry replicate` mirrors FAR into — and, once a mirror
record exists, the registry the install pulls **out of**. See
[Air-gapped install](./10a-air-gapped-install.md) and
[Registry targets](./10b-registry-targets.md).

```yaml
registry:
  target: generic                    # icr (default) | generic
  # target: generic — any OCI registry (Harbor, Artifactory, Quay, registry:2)
  generic_host: harbor.example.com
  generic_repo_prefix: bnk-mirror
  generic_username: admin
  generic_password_b64: <base64>     # set via `registry target generic_password --password-stdin`
  generic_ca_b64: <base64>           # set via `registry target generic_ca <harbor.crt>`
  generic_ca_sha256: sha256:ab12...  # its fingerprint; also pins a captured CA
  # target: icr — IBM Container Registry
  # icr_host: uk.icr.io              # empty → derived from ibmcloud.region
  # icr_namespace: my-namespace      # empty → the workspace prefix
```

| Field | Type | Default | Meaning |
|---|---|---|---|
| `registry.target` | string | `icr` | `icr` (IBM Container Registry) or `generic` (any OCI-compliant registry — Harbor, Artifactory, Quay, `registry:2`). |
| `registry.icr_host` | string | (from region) | ICR host for `target: icr`, e.g. `de.icr.io`. |
| `registry.icr_namespace` | string | (the prefix) | ICR namespace artifacts nest under. |
| `registry.generic_host` | string | (empty) | Registry host for `target: generic`, e.g. `harbor.example.com`. |
| `registry.generic_repo_prefix` | string | (empty) | Repository path artifacts nest under — a Harbor **project**, or an Artifactory repo key. |
| `registry.generic_username` | string | (empty) | Basic-auth user. Both credential fields empty ⇒ anonymous push/pull. |
| `registry.generic_password_b64` | string | (empty) | Basic-auth password/token, base64. Like every `_b64` field this is **obfuscation, not encryption** — `chmod 600`, never commit. Set it with `registry target generic_password --password-stdin` so it never reaches your shell history; templatable from the environment via `init --override-from-env` (`ROKSBNKCTL_GENERIC_PASSWORD`). |
| `registry.generic_ca_b64` | string | (empty) | The mirror's CA chain (PEM, base64) — the **authoritative** copy, taken from the file that generated it. When set, `registry replicate` never dials the mirror to discover trust. A certificate is public data, so unlike the other `_b64` fields this is encoded only for single-line YAML safety. Set it with `registry target generic_ca <file>`; templatable via `ROKSBNKCTL_GENERIC_CA_B64`. |
| `registry.generic_ca_sha256` | string | (empty) | SHA-256 pin for the mirror CA (hex; a `sha256:` prefix and colons are accepted). Authenticates a CA **captured** from the host when `generic_ca_b64` is unset. Without either, `replicate` **refuses** to adopt a self-signed CA it discovered over the wire — it would become cluster-wide node trust. Templatable via `ROKSBNKCTL_GENERIC_CA_SHA256`. |

**The same credential installs, not just replicates.** For an external private registry
these fields authenticate *both* halves of the install: chart pulls log in with them, and
pods get a `mirror-secret` dockerconfig built from them — created for you in every
namespace that pulls (cert-manager, the FLO/BNK namespaces, and `kube-system` for the
node-labeler) and referenced from the CNEInstance. **A private registry needs no
anonymous or public project.** An in-cluster/ICR mirror authorizes by RBAC instead and
needs no pull secret at all.

## `tf_source:` block

```yaml
tf_source:
  type: embedded         # or: github | local
  repo: jgruberf5/roksbnkctl-tf
  ref: v1.0.0
  path: /path/to/checkout
```

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `type` | string | `embedded` | `embedded` \| `github` \| `local` | Where the Terraform source comes from. `embedded` uses the HCL bundled into the binary at compile time via `//go:embed`. `github` downloads a tarball from a GitHub release. `local` points at a directory on disk. |
| `repo` | string | — | `owner/name` form | Required for `type: github`. The GitHub repo holding the HCL. |
| `ref` | string | — | a tag, branch, or SHA | Required for `type: github`. The release tag or git ref to fetch. |
| `path` | string | — | absolute or relative directory | Required for `type: local`. The on-disk directory containing `main.tf`. |

Most users want `embedded` (the default). The `github` mode is for testing forks or pinning to an upstream tag that's newer than the bundled one. The `local` mode is for active development on the HCL itself.

## `cos:` block

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

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `instance` | string | (empty ⇒ `bnk-supply-chain`) | COS instance name or CRN | The instance the supply-chain bucket lives on. Names are resolved via Resource Controller at runtime. |
| `bucket` | string | (empty ⇒ `bnk-artifacts`) | S3 bucket name | The bucket within the instance that holds the FAR auth key + subscription JWT. |
| `region` | string | (empty ⇒ `us-south`) | any IBM Cloud region | The region the bucket lives in. |
| `upload` | list of `{source, key}` | (empty) | host path → bucket key | Pre-flight uploads run before `roksbnkctl up`. Idempotent — re-running overwrites the bucket objects. |

`instance` / `bucket` / `region` point roksbnkctl at the orchestration COS holding the FAR auth key + subscription JWT. They are honoured **both** by the terraform render (`ibmcloud_cos_instance_name` / `ibmcloud_resources_cos_bucket` / `ibmcloud_cos_bucket_region`) **and** by the `registry` FAR-file resolver, so a customer-owned bucket is used consistently across both. Empty fields fall back to the built-in defaults. See [Chapter 25 — COS supply chain management](./25-cos-supply-chain.md) for the full surface.

## `state:` block

Selects where terraform state lives. Absent (or `backend: local`) keeps per-phase local `terraform.tfstate` — byte-identical to before. `backend: s3` stores each phase's state in an S3-compatible bucket (IBM COS). See [Chapter 12a — Remote state](./12a-remote-state.md) for the full walkthrough.

```yaml
state:
  backend: s3          # "" | local (default) | s3
  s3:
    endpoint: "https://s3.us-south.cloud-object-storage.appdomain.cloud"
    bucket:   "acme-bnk-tfstate"
    region:   "us-south"
    key_prefix: "roksbnkctl"
    access_key_source: ""    # env var name; default ROKSBNKCTL_COS_HMAC_ACCESS_KEY → AWS_ACCESS_KEY_ID
    secret_key_source: ""    # env var name; default ROKSBNKCTL_COS_HMAC_SECRET_KEY → AWS_SECRET_ACCESS_KEY
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `backend` | string | `local` | `local` \| `s3`. Empty = local. |
| `s3.endpoint` | string | — | COS S3 endpoint URL (required for `s3`). |
| `s3.bucket` | string | — | Pre-provisioned bucket (required). roksbnkctl never creates/deletes it. |
| `s3.region` | string | — | COS location / region (required). |
| `s3.key_prefix` | string | the workspace name | First segment of the per-phase key `<prefix>/<workspace>/<phase>/terraform.tfstate`. |
| `s3.access_key_source` | string | `ROKSBNKCTL_COS_HMAC_ACCESS_KEY` → `AWS_ACCESS_KEY_ID` | Env var the **HMAC** access key is read from. Never stored in config / HCL / state. |
| `s3.secret_key_source` | string | `ROKSBNKCTL_COS_HMAC_SECRET_KEY` → `AWS_SECRET_ACCESS_KEY` | Env var for the HMAC secret key. |

`s3` requires **terraform ≥ 1.10** (the native lockfile) — a preflight error fires otherwise. Convert an existing local-state workspace with `roksbnkctl state migrate`.

## `bnkforge:` block

Opt-in integration with a co-located [BNK Forge](./24a-bnk-forge-registration.md)
install. When `register: true`, a post-apply hook on `cluster up` registers the
just-provisioned ROKS cluster with BNK Forge over its v3 REST API —
**credential-backed**, so Forge re-derives the kubeconfig on demand from an IBM
Cloud credential template rather than storing a perishable one. Best-effort: it
never blocks or fails the deploy. Absent (or `register: false`) ⇒ no-op. The hook
**never** passes `--force`, so it cannot take a cluster held by another Forge
project. See
[Chapter 24a — Registering the cluster with BNK Forge](./24a-bnk-forge-registration.md).

**Set this block with the CLI — don't hand-edit it:** `roksbnkctl bnkforge
enable [--url U] [--project P]` writes it for you, `roksbnkctl bnkforge disable`
flips `register` off, and `roksbnkctl bnkforge status` shows the effective
values. The resulting block looks like:

```yaml
bnkforge:
  register: true
  url: https://forge.example.com   # only if you passed --url
  project: "42"                    # only if you passed --project
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `register` | bool | `false` | Opt the workspace in. `false`/omitted ⇒ no registration. |
| `url` | string | (`BNK_FORGE_URL`) | The BNK Forge server URL. Empty ⇒ the `BNK_FORGE_URL` env var; with neither, registration errors. |
| `project` | string | (the workspace name) | BNK Forge project to register the cluster under. Empty ⇒ a project named after the workspace, created if it does not exist. |
| `username` | string | (`BNK_FORGE_USER`) | Forge login. The **password** is never stored in `config.yaml` — it comes from `BNK_FORGE_PASSWORD` or an interactive prompt, and only the resulting session token is cached (OS keychain). |
| `insecure` | bool | `false` | Skip TLS verification. Forge installs commonly carry a self-signed certificate. |

## `targets:` block

```yaml
targets:
  jumphost:
    host: 169.45.91.10
    port: 22
    user: ubuntu
    key_path: /path/to/private/key.pem      # one of key_path
    key_source: tf-output:jumphost_shared_key  # ...or key_source
```

The top-level value is a map; the key is the target name (`jumphost`, `eu-bastion`, etc.). Each entry:

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `host` | string | — | hostname or IP | The SSH endpoint. IPv6 literals must be unbracketed (the SSH client brackets internally). |
| `port` | integer | `22` | 1-65535 | SSH port. |
| `user` | string | — | a username on the target | Typically `ubuntu` for HCL-provisioned jumphosts (cloud-init writes the user); `root` for direct-IBM-Cloud Linux VSIs. |
| `key_path` | string | — | a path to a PEM file | One of `key_path` or `key_source` is required. Path to the PEM-encoded private key. |
| `key_source` | string | — | `agent` \| `tf-output:<output-name>` | The other "key source" form. `agent` uses ssh-agent; `tf-output:<name>` reads the named terraform output as the PEM. |

Auto-populated by `roksbnkctl up` post-apply for the upstream HCL's TGW jumphost when `testing_create_tgw_jumphost = true`. See [Chapter 15 — SSH targets](./15-ssh-targets.md) and [Chapter 16 — The `--on` flag](./16-on-flag-ssh-jumphosts.md).

## `exec:` block

```yaml
exec:
  ibmcloud:  { backend: local }
  iperf3:    { backend: k8s }
  terraform: { backend: local }
```

Top-level value is a map keyed by tool name. Each entry has one field:

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `backend` | string | `local` | `local` \| `docker` \| `k8s` \| `ssh:<target>` | The default execution backend for this tool. A `--backend <value>` flag on the command line overrides the workspace config for that single invocation. |

The per-tool defaults at v1.0:

| Tool | Default backend | Supported backends |
|---|---|---|
| `terraform` | `local` | `local`, `docker` (k8s and ssh deferred to v1.x) |
| `ibmcloud` | `local` | `local`, `docker`, `k8s`, `ssh:<target>` |
| `iperf3` | `k8s` | `local`, `k8s`, `ssh:<target>` (docker rejected) |
| `dns` | `local` | `local`, `k8s`, `ssh:<target>` (docker rejected) |

See [Chapter 17 — Execution backends](./17-execution-backends.md) and [Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md).

## Field-by-field reference table

Sorted by top-level block. Lookup-friendly. Every field that appears in [`internal/config/workspace.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/config/workspace.go).

| Path | Type | Default | Notes |
|---|---|---|---|
| `prefix` | string | (empty ⇒ legacy sparse render) | Workspace name-prefix base; ≤ 35 chars, lowercase label. Since `v1.8.0`. |
| `ibmcloud.region` | string | (prompted) | IBM Cloud region (`ca-tor`, `us-south`, …). |
| `ibmcloud.resource_group` | string | `default` | Resource group name. |
| `ibmcloud.api_key_source` | string | (chain) | `env` \| `keychain` \| `config` \| `prompt`. |
| `ibmcloud.api_key_b64` | string | (empty) | Base64-encoded API key. Obfuscation only. |
| `cluster.create` | bool | `true` | Provision new vs attach existing. |
| `cluster.name` | string | (prompted) | Cluster name. |
| `cluster.openshift_version` | string | `4.20` | OpenShift minor version. |
| `cluster.workers_per_zone` | integer | `1` | Workers per AZ. |
| `cluster.public_gateway` | bool | `true` | Worker Internet egress via a per-subnet public gateway → `cluster_public_gateway`. `false` = private/disconnected cluster (no egress; expert — see [Chapter 10a](./10a-air-gapped-install.md)). Settable from the environment with `ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY`; it is a tri-state, so *unset* inherits the terraform default rather than rendering `true`. |
| `cluster.min_worker_vcpu_count` | integer | `16` | Worker-flavor auto-select floor (vCPUs) → `roks_min_worker_vcpu_count`. `0`/omitted keeps the HCL default. |
| `cluster.min_worker_memory_gb` | integer | `64` | Worker-flavor auto-select floor (GB) → `roks_min_worker_memory_gb`. `0`/omitted keeps the HCL default. |
| `cluster.network_mode` | string | `single-nic` | Worker attachment mode → `cluster_network_mode`. Create-time only and enforced; checked against the BNK line derived from `bnk.manifest_version`. |
| `cluster.vpc_cidr` | string | (empty) | Address block for a new cluster VPC → `cluster_vpc_cidr`. Empty = IBM `auto`, which is identical for every VPC in the region; set a distinct block per cluster sharing a Transit Gateway. `/18` minimum. |
| `resources.transit_gateway.create` | bool | `true` | Create a prefix-named TGW vs adopt an existing one. Since `v1.8.0`. |
| `resources.transit_gateway.existing` | string | (empty) | Existing Transit Gateway to attach the cluster VPC to, by **name or id**, when `create: false`. `cluster up`/`register` connects it; `tgw connect` does it after the fact. See [Sharing a Transit Gateway](./09a-transit-gateway-sharing.md). |
| `resources.cluster_vpc.create` | bool | `true` | Create a prefix-named cluster VPC vs adopt an existing one. Selectable in the `init` interview since `v1.26.0`. |
| `resources.cluster_vpc.existing` | string | (empty) | Existing cluster VPC to build the new cluster into, by **id** (not name), when `create: false` — renders `use_existing_cluster_vpc` + `existing_cluster_vpc_id`. Lets multiple clusters share one VPC. See [Reusing an existing VPC](./08-cluster-phase.md#reusing-an-existing-vpc-multiple-clusters-in-one-vpc). |
| `resources.registry_cos.create` | bool | `true` | Create the registry COS instance vs adopt an existing one. |
| `resources.registry_cos.existing` | string | (empty) | Existing COS instance name when `create: false`. |
| `resources.cert_manager.create` | bool | `true` | Install cert-manager (`install_cert_manager`). |
| `resources.bnk.create` | bool | `true` | Deploy BIG-IP Next for Kubernetes (`deploy_bnk`). |
| `resources.tgw_jumphost.create` | bool | `false` | Create the TGW test jumphost — the optional testing client, off by default (the `init` interview's *"Add a testing client?"* also defaults to no). **It lives in a client VPC**, so enabling it requires either `client_vpc.create: true` or `client_vpc.existing`; `init` rejects the combination with neither. |
| `resources.cluster_jumphosts.create` | bool | `false` | Create per-zone cluster jumphosts. |
| `resources.client_vpc.create` | bool | `false` | Create a new client VPC for the TGW jumphost. Consumes a **Transit Gateway connection**, which is a quota'd resource. |
| `resources.client_vpc.existing` | string | (empty) | Existing client VPC name when `create: false`. |
| `resources.client_region` | string | (empty) | Region the testing client (TGW jumphost + client VPC) lives in → `testing_client_vpc_region`. Since `v1.9.0`. |
| `resources.testing_client_vpc_name` | string | (empty) | Name for the testing client VPC when `client_vpc.create: true` → `testing_client_vpc_name`. |
| `resources.testing_ssh_key_name` | string | (empty) | Existing IBM Cloud VPC SSH key attached to the jumphosts → `testing_ssh_key_name`. Resolved by `init`. |
| `resources.testing_jumphost_profile` | string | (empty ⇒ auto-select) | Pin an instance profile for **all** testing jumphosts → `testing_jumphost_profile`. |
| `resources.testing_min_vcpu_count` | integer | `4` | Jumphost-profile auto-select floor (vCPUs) → `testing_min_vcpu_count`. |
| `resources.testing_min_memory_gb` | integer | `8` | Jumphost-profile auto-select floor (GB) → `testing_min_memory_gb`. |
| `bnk.cneinstance_size` | string | `Small` | `Small` \| `Medium` \| `Large`. |
| `bnk.far_repo_url` | string | `repo.f5.com` | FAR image registry URL. |
| `bnk.manifest_version` | string | `2.3.0-3.2598.3-0.0.170` | f5-bigip-k8s-manifest chart version. |
| `bnk.far_auth_file` | string | `f5-far-auth-key.tgz` | Object KEY the FAR auth tarball is read from in the COS bucket → `far_auth_file`. |
| `bnk.subscription_jwt_file` | string | `subscription.jwt` | Object KEY the subscription JWT is read from in the COS bucket → `subscription_jwt_file`. |
| `bnk.far_auth_local_file` | string | (empty) | Read the FAR auth tarball from this **local path** instead of COS. When set together with `subscription_jwt_local_file`, renders `use_cos_bucket = false` + injects the content directly (no bucket). Mutually required with the JWT local file. See [Local files instead of COS](./25-cos-supply-chain.md#local-files-instead-of-cos-no-bucket-needed). |
| `bnk.subscription_jwt_local_file` | string | (empty) | Read the subscription JWT from this **local path** instead of COS (pairs with `far_auth_local_file`; both or neither). |
| `bnk.flo_namespace` | string | `f5-bnk` | F5 Lifecycle Operator namespace → `flo_namespace`. |
| `bnk.flo_utils_namespace` | string | `f5-utils` | F5 utility-components namespace → `flo_utils_namespace`. |
| `bnk.gslb_datacenter_name` | string | (empty) | Optional CNEInstance GSLB datacenter → `cneinstance_gslb_datacenter_name`. |
| `bnk.network.zones[]` | list | (install-guide defaults) | Per-AZ data-plane subnets (`ext_vlan_cidr`, `int_vlan_cidr`, `int_snat_cidr`, `int_vip_cidr`) + TMM self-IPs (`external_selfip`, `internal_selfip`) → `cneinstance_network_zones`. Supply all 3 zones or none. See [Chapter 12 §`bnk.network`](./12-workspace-config.md). |
| `bnk.gtm.url` | string | (empty) | BIG-IP DNS / GTM the GSLB datacenter registers **with** → `cneinstance_gtm_url`. Empty disables GTM. `gslb_datacenter_name` names the datacenter; without this it is a label pointing at nothing. Env: `ROKSBNKCTL_GTM_URL`. |
| `bnk.gtm.username` | string | (empty) | GTM user → `cneinstance_gtm_username`. Env: `ROKSBNKCTL_GTM_USERNAME`. |
| `bnk.gtm.password_b64` | string | (empty) | GTM password, base64 (obfuscation, not encryption) → rendered raw as `cneinstance_gtm_password`. `ROKSBNKCTL_GTM_PASSWORD` takes the raw value. |
| `bnk.trusted_profile.service_account` | string | (derived) | Account allowed to assume the CNE controller's Trusted Profile → `flo_trusted_profile_sa_name`. Empty derives what FLO actually creates: `f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount`. The IAM trust rule is a **matcher**, not a pointer: IBM compares a pod's service-account token against `crn`/`namespace`/`name` with `EQUALS`, so a name that does not match the account the CNE controller actually runs as means **nothing can assume the profile, and nothing reports an error**. Set this only if you can also make FLO name the account differently — roksbnkctl cannot, since FLO creates it when it reconciles the CNEInstance and that spec has no service-account field. Env: `ROKSBNKCTL_TRUSTED_PROFILE_SA`. |
| `bnk.trusted_profile.roles` | list | `[Viewer, Editor]` | Roles for that profile, scoped to the cluster VPC → `flo_trusted_profile_roles`. Env: `ROKSBNKCTL_TRUSTED_PROFILE_ROLES`. |
| `bnk.network.vlan_prefixlen_external` | int | (inherit) | Per-VLAN override of the mask → `cneinstance_vlan_prefixlen_external`. Unset inherits `vlan_prefixlen`. Env: `ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL`. |
| `bnk.network.vlan_prefixlen_internal` | int | (inherit) | Same, internal VLAN → `cneinstance_vlan_prefixlen_internal`. The two need not match. Env: `ROKSBNKCTL_VLAN_PREFIXLEN_INTERNAL`. |
| `bnk.network.vlan_prefixlen` | int | `24` | TMM self-IP prefix length (F5SPKVlan `spec.prefixlen_v4`) → `cneinstance_vlan_prefixlen`. Normally match your VLAN CIDRs — but it is **independent of them and never derived from them**, because a mask that deliberately disagrees with its subnet is a tool: it changes what TMM treats as directly connected, and static routes then steer the remainder to force a specific traffic pattern. Nothing validates the two against each other, so a `/23` subnet with this left at `24` silently yields `/24` self-IPs. `roksbnkctl init` asks for it **after** the zone CIDRs and suggests the mask they share when all of them agree, but that is a prompt default only. Env: `ROKSBNKCTL_VLAN_PREFIXLEN`. |
| `bnk.network.tmm_k8s_routes` | string | `172.17.0.0/18` | Pod CIDR TMM routes to (`TMM_K8S_ROUTES`) → `cneinstance_tmm_k8s_routes`. Your cluster's pod subnet if non-default. |
| `bnk.preflight.reachability_retry_seconds` | int | `180` | How long one target may keep failing in the pre-install reachability gate before the verdict is believed. `0` = one shot. Not a tfvar. Env: `ROKSBNKCTL_REACHABILITY_RETRY_SECONDS`. |
| `bnk.preflight.reachability_timeout_seconds` | int | `480` | How long to wait for every node to report. Clamped above the retry budget. Not a tfvar. Env: `ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS`. |
| `bnk.cert_manager.namespace` | string | `cert-manager` | cert-manager namespace → `cert_manager_namespace`. Install/skip stays on `resources.cert_manager.create`. |
| `bnk.cert_manager.version` | string | (HCL default) | cert-manager chart version → `cert_manager_version`. |
| `bnk.license_mode` | string | (empty ⇒ `connected`) | `connected` \| `disconnected` \| `f5licenseproxy`. Rendered as the `license_mode` tfvar. `f5licenseproxy` licenses BNK via the [F5 License Proxy](./10c-flp-licensing.md) — either one this workspace deployed (`roksbnkctl flp up`) or one in **another** cluster (`bnk.flp.external`). Empty/omitted keeps the JWT/connected default. |
| `bnk.flp.namespace` | string | `f5-license-proxy` | Namespace the FLP phase installs into (FLP mode only). |
| `bnk.flp.vsi.vpc` | string | (empty) | **Adopt** an existing VPC for the standalone proxy VSI. Mutually exclusive with `create_vpc`. |
| `bnk.flp.vsi.create_vpc` | bool | `false` | Build the proxy its **own** VPC, address prefix and public gateway. The proxy is the one component needing egress to F5, which makes it a natural *first* deployment in an air-gapped estate — without this something else has to have created a VPC, in practice coupling licensing to a registry it is otherwise unrelated to. |
| `bnk.flp.vsi.vpc_name` | string | `flp-vsi-vpc` | Name for the VPC created by `create_vpc`. |
| `bnk.flp.vsi.subnet_cidr` | string | `10.250.0.0/24` | Address prefix for that VPC. **Must not overlap** anything the consuming clusters can already route to — a transit gateway cannot route to two VPCs with overlapping prefixes and silently blackholes one. |
| `bnk.flp.chart_version` | string | (empty ⇒ from the BNK manifest) | Pin the `f5-license-proxy` chart version. Normally unset — the version is read from the BNK manifest, like the FLO and CIS charts. |
| `bnk.flp.storage_class` | string | (empty ⇒ HCL default) | Dynamic StorageClass for the FLP's PVCs → `flp_storage_class`. Set it when the cluster/region exposes a different block-storage class. (helm mode) |
| `bnk.flp.mode` | string | (empty ⇒ `helm`) | `helm` \| `vsi`. `vsi` deploys the proxy on a standalone VSI (podman pod, no k8s) instead of the helm chart; both terminate in the same endpoint + root CA handoff. |
| `bnk.flp.vsi.vpc` | string | (empty ⇒ the cluster VPC) | Existing VPC id to deploy the standalone FLP VSI into **without any cluster** — a licensing appliance in a services VPC. Empty ⇒ joins the workspace's cluster VPC (requires a cluster) — unless `create_vpc` is set, which builds the proxy its own and needs no cluster. See [Chapter 10c §"Standalone"](./10c-flp-licensing.md). |
| `bnk.flp.vsi.profile` | string | `bx2-4x16` | VSI instance profile (≥ 4 vCPU / 8 GB) → `flp_vsi_profile`. |
| `bnk.flp.vsi.zone` | string | (empty ⇒ `<region>-1`) | Zone for the FLP VSI → `flp_vsi_zone`. |
| `bnk.flp.vsi.boot_size_gb` | integer | `100` | Boot volume size (≥ 80) → `flp_vsi_boot_size_gb`. |
| `bnk.flp.vsi.reach` | string | `private` | How the CWC reaches the VSI: `private` (VPC / transit gateway) or `floating` → `flp_vsi_reach`. |
| `bnk.flp.vsi.allowed_cidrs` | list of string | (empty ⇒ VPC space) | Source CIDRs allowed to reach the VSI's 8443 → `flp_vsi_allowed_cidrs`. |
| `bnk.flp.vsi.forward_proxy.{host,port,protocol}` | — | (none) | Optional egress forward proxy for the VSI's calls to F5 licensing → `flp_forward_proxy_*`. |
| `bnk.flp.node_port_access` | bool | `false` | Expose the proxy OUTSIDE its cluster so a BNK install in a different cluster can license through it ([Flow C](./10c-flp-licensing.md#flow-c--a-shared-licensing-cluster)). Set by `flp up --add-node-port-access` and persisted, so a later `flp up` does not tear the exposure down. |
| `bnk.flp.node_port_source_cidrs` | list of string | (empty) | With `node_port_access`: open the proxy's NodePort to these CIDRs on the worker security group. A **list**, and it must name **every zone** — a multi-zone VPC carries one address prefix per zone, and a consuming pod scheduled in a zone you left out is dropped at the security group (the proxy answers some pods and silently times out for others). Set by `flp up --node-port-source-cidr` and persisted. |
| `bnk.flp.external.url` | string | (empty) | License against a proxy in **another** cluster — its `external_endpoint` from `roksbnkctl -w <owner> flp output`. This workspace then needs no `flp up` of its own. |
| `bnk.flp.external.root_ca_b64` | string | (empty) | That proxy's `root_ca_b64`, delivered to the CWC so it can verify the proxy's certificate. Required with `external.url`. |
| `test.throughput.image` | string | `networkstatic/iperf3:latest` | iperf3 image. |
| `test.throughput.duration` | integer | `30` | iperf3 `-t` (seconds). |
| `test.throughput.streams` | integer | `8` | iperf3 `-P` (parallel streams). |
| `test.throughput.default_mode` | string | `north-south` | Default mode. |
| `test.connectivity.extra_hosts` | []string | (empty) | URLs to probe. |
| `test.dns.resolvers` | map[string]string | (empty) | Name → `<ip>[:<port>]`. |
| `test.dns.default_target` | string | (empty) | Default `--target` value. |
| `tf_source.type` | string | `embedded` | `embedded` \| `github` \| `local`. |
| `tf_source.repo` | string | (empty) | GitHub `owner/name`; required for `github`. |
| `tf_source.ref` | string | (empty) | Git ref; required for `github`. |
| `tf_source.path` | string | (empty) | Local directory; required for `local`. |
| `cos.instance` | string | (empty ⇒ `bnk-supply-chain`) | Orchestration COS instance name or CRN → `ibmcloud_cos_instance_name` + the `registry` FAR resolver. |
| `cos.bucket` | string | (empty ⇒ `bnk-artifacts`) | Bucket holding the FAR auth key + JWT → `ibmcloud_resources_cos_bucket` + the `registry` FAR resolver. |
| `cos.region` | string | (empty ⇒ `us-south`) | Region the orchestration COS bucket lives in → `ibmcloud_cos_bucket_region` + the `registry` FAR resolver. |
| `cos.upload[].source` | string | — | Local file path. |
| `cos.upload[].key` | string | — | Bucket key. |
| `targets.<name>.host` | string | — | SSH host. |
| `targets.<name>.port` | integer | `22` | SSH port. |
| `targets.<name>.user` | string | — | SSH user. |
| `targets.<name>.key_path` | string | (empty) | PEM file path. |
| `targets.<name>.key_source` | string | (empty) | `agent` \| `tf-output:<name>`. |
| `exec.<tool>.backend` | string | `local` (varies by tool) | `local` \| `docker` \| `k8s` \| `ssh:<target>`. |
| `bnkforge.register` | bool | `false` | Opt into BNK Forge cluster registration on `cluster up`. |
| `bnkforge.url` | string | (CLI's stored-session URL) | Override the BNK Forge server URL. |
| `bnkforge.project` | string | (CLI auto-selects) | Target BNK Forge project id. |

## Behaviour when fields are missing

`roksbnkctl` falls through three layers: **workspace config → upstream HCL default → fail**.

| Missing field | Behaviour |
|---|---|
| `ibmcloud.region` | `roksbnkctl init` prompts; programmatic loads error with "region is empty". |
| `ibmcloud.resource_group` | Defaults to `default`. |
| `ibmcloud.api_key_source` | Resolver walks the full chain (env → keychain → config → prompt). |
| `ibmcloud.api_key_b64` | Skipped in the resolver chain. |
| `cluster.create` | Defaults to `true`. |
| `cluster.name` | `init` prompts; programmatic loads error. |
| `cluster.openshift_version` | Empty string passed to upstream HCL; the module picks the current default. |
| `cluster.workers_per_zone` | Falls through to `1` (upstream HCL default). |
| `cluster.network_mode` | The cluster is built and recorded as `single-nic` — identical to every cluster built before the setting existed. |
| `cluster.vpc_cidr` | `cluster_vpc_cidr` is omitted from the generated tfvars; the VPC keeps IBM's `auto` address prefixes (`10.241.0.0/18`, `10.241.64.0/18`, `10.241.128.0/18`). |
| `bnk.*` | Each field is omitted from the generated `terraform.tfvars` and the upstream HCL default applies. |
| `test.throughput.*` | Coded defaults (30s, 8 streams, `networkstatic/iperf3:latest`) apply. |
| `test.connectivity.extra_hosts` | Connectivity probe runs with built-in URLs only. |
| `test.dns.resolvers` | `--server` requires a literal IP or `host:port`. |
| `test.dns.default_target` | `--target` becomes required on the command line. |
| `tf_source` | Treated as `type: embedded` (legacy default). |
| `cos` | Block omitted ⇒ no pre-flight uploads; FLO reads whatever's already in the configured bucket. |
| `targets.*` | Block absent ⇒ `roksbnkctl --on jumphost` errors with "no target named jumphost"; auto-populated by `up` when terraform provisions a jumphost. |
| `exec.*` | Each tool falls back to its built-in default (typically `local`; `iperf3` is `k8s`). |
| `bnkforge` | Block absent (or `register: false`) ⇒ no BNK Forge registration; `cluster up` behaves exactly as before. |

## How `--var-file` interacts with `config.yaml`

`roksbnkctl up --var-file <file>` layers user-supplied tfvars **after** the auto-rendered tfvars derived from `config.yaml`. Later wins, terraform-style. Multiple `--var-file` flags are accepted and stack in command-line order.

The auto-render path: `config.yaml` → typed `Workspace` struct → key/value tfvars → `~/.roksbnkctl/<ws>/state/terraform.tfvars`. The user's `--var-file` is appended to the terraform invocation as an additional `-var-file=<path>` argument. See [Chapter 13 — Terraform variables](./13-terraform-variables.md) for the layering rules.

A workspace-persistent override file is `~/.roksbnkctl/<ws>/terraform.tfvars.user` — when present, it's auto-layered after the rendered tfvars and before any explicit `--var-file`. Useful for "always pass this `bigip_password` value when applying this workspace" without putting it in `config.yaml` (where the plaintext-secret rejection would reject it).

## Cross-references

- [Chapter 12 — Workspace config](./12-workspace-config.md) — the teaching counterpart to this lookup.
- [Chapter 13 — Terraform variables](./13-terraform-variables.md) — how `config.yaml` fields render into tfvars.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) — the `ibmcloud.api_key_*` semantics.
- [Chapter 29 — Terraform variable reference](./29-terraform-variable-reference.md) — the upstream HCL variable surface that `bnk.*` and `cluster.*` populate.
