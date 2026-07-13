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
prefix:          # optional (since v1.8.0); workspace name-prefix base
ibmcloud:        # required
cluster:         # required
resources:       # optional (since v1.8.0); per-resource create/existing toggles
bnk:             # optional; populates upstream HCL bnk variables
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
  openshift_version: "4.18"
  workers_per_zone: 1
```

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `create` | bool | `true` | `true` \| `false` | `true` provisions a new ROKS cluster; `false` attaches to an existing one (set `name` to the existing cluster's name or ID). |
| `name` | string | — (prompted by `init`) | RFC 1123 DNS label | The cluster name. Used as the OpenShift cluster identity and as the resource group disambiguator. |
| `openshift_version` | string | `4.18` | any version IBM Cloud's catalog accepts | Pinned to a minor (`4.18`) rather than patch — IBM ships continuous patch updates within a minor. Leave empty for "latest". |
| `workers_per_zone` | integer | `1` | 1+ | Worker nodes provisioned per availability zone. Multiply by the zone count (typically 3) for the total cluster size. BNK needs ≥1 worker; production deployments use 2-3 per zone. |

## `resources:` block

```yaml
resources:
  transit_gateway:   { create: true }
  registry_cos:      { create: true }
  cert_manager:      { create: true }
  bnk:               { create: true }
  tgw_jumphost:      { create: true }
  cluster_jumphosts: { create: false }
  client_vpc:        { create: false, existing: my-shared-client-vpc }
  client_region:     ca-tor               # since v1.9.0; testing-client region
```

*(since `v1.8.0`)* Per-resource create/adopt toggles, written by the `init` interview. Each key is a `{create, existing}` pair: `create: true` provisions a new prefix-named resource; `create: false` declines it and (when a still-enabled resource depends on it) `existing:` names the pre-existing resource to consume instead.

| Sub-block | `create` default | `existing` consumed by | Renders into |
|---|---|---|---|
| `transit_gateway` | `true` | `create_roks_transit_gateway`; `existing` → `roks_transit_gateway_name` when the TGW jumphost needs an existing TGW | `create_roks_transit_gateway`, `roks_transit_gateway_name` |
| `registry_cos` | `true` | `create_roks_registry_cos_instance`; `existing` → `roks_cos_instance_name` | `create_roks_registry_cos_instance`, `roks_cos_instance_name` |
| `cert_manager` | `true` | — | `install_cert_manager` |
| `bnk` | `true` | — | `deploy_bnk` |
| `tgw_jumphost` | `true` | — | `testing_create_tgw_jumphost`, `testing_tgw_jumphost_name` |
| `cluster_jumphosts` | `false` | — | `testing_create_cluster_jumphosts`, `testing_cluster_jumphost_name_prefix` |
| `client_vpc` | `false` (created on demand for the TGW jumphost) | `existing` → `testing_client_vpc_name` when not creating one | `testing_create_client_vpc`, `testing_client_vpc_name` |

`client_region` *(since `v1.9.0`)* is the odd one out — a plain **string**, not a `{create, existing}` toggle. It's the IBM Cloud region the testing client (TGW jumphost + client VPC) is installed in, letting the test client live in a **different region from the cluster**. The `init` interview sets it when you answer the *"Add a testing client?"* / region prompt; omitted, it renders nothing and the terraform default (`testing_client_vpc_region`) applies. It also seeds the regions [`roksbnkctl cleanup`](./11-tearing-down.md#which-regions-it-scans) scans. Renders into `testing_client_vpc_region`.

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
```

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `cneinstance_size` | string | `Small` | `Small` \| `Medium` \| `Large` | Sizing for the deployed CNE Instance. Renders into the upstream HCL `cneinstance_deployment_size` variable. |
| `far_repo_url` | string | `repo.f5.com` | URL of a Docker-compatible image registry | The image registry FLO pulls FAR container images from. Override for air-gapped installs pointing at a local mirror. |
| `manifest_version` | string | `2.3.0-3.2598.3-0.0.170` | a published `f5-bigip-k8s-manifest` chart version | Pins the FLO + CIS versions transitively (both are extracted from the manifest chart). |
| `cr_mode` | string | (empty ⇒ `kubectl`) | `kubectl` \| `legacy_curl` | *(since Sprint 27)* Selects the BNK custom-resource install mechanism, rendered as the `bnk_cr_mode` tfvar. Empty/omitted or `kubectl` ⇒ the terraform-native path (`helm_release` + `kubernetes_*` + `alekc/kubectl` `kubectl_manifest` + `wait_for`); `legacy_curl` ⇒ the `null_resource`/`curl`/`time_sleep` baseline. The `--legacy-bnk` flag on `bnk up`/`bnk down` overrides this to `legacy_curl` for a single run. See [Chapter 10 §"The install-mode flag"](./10-deploying-bnk-trials.md#the-install-mode-flag-bnk_cr_mode). |

All four fields are optional; omitting renders the HCL's own defaults. See [Chapter 13 — Terraform variables](./13-terraform-variables.md) for the upstream defaults.

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
  instance: bnk-orchestration
  bucket: bnk-schematics-resources
  upload:
    - source: ./local/f5-far-auth-key.tgz
      key: f5-far-auth-key.tgz
    - source: ./local/trial.jwt
      key: trial.jwt
```

| Field | Type | Default | Allowed | Notes |
|---|---|---|---|---|
| `instance` | string | — | COS instance name or CRN | The instance the supply-chain bucket lives on. Names are resolved via Resource Controller at runtime. |
| `bucket` | string | — | S3 bucket name | The bucket within the instance. |
| `upload` | list of `{source, key}` | (empty) | host path → bucket key | Pre-flight uploads run before `roksbnkctl up`. Idempotent — re-running overwrites the bucket objects. |

See [Chapter 25 — COS supply chain management](./25-cos-supply-chain.md) for the full surface.

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
install. When `register: true` and the `bnk-forge` CLI is on `PATH`, a post-apply
hook on `cluster up` registers the just-provisioned ROKS cluster with BNK Forge —
**credential-backed**, so Forge re-derives the kubeconfig on demand from an IBM
Cloud credential template rather than storing a perishable one. Best-effort: it
never blocks or fails the deploy. Absent (or `register: false`) ⇒ no-op. See
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
| `url` | string | (CLI's stored-session URL) | Overrides the BNK Forge server URL the `bnk-forge` CLI would read from its stored session (`~/.bnk-forge/config.json`). Empty ⇒ use the stored session's URL. |
| `project` | string | (CLI auto-selects) | Target BNK Forge project id to register the cluster under. Empty ⇒ the CLI picks the active/sole project, or prompts. |

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
| `cluster.openshift_version` | string | `4.18` | OpenShift minor version. |
| `cluster.workers_per_zone` | integer | `1` | Workers per AZ. |
| `resources.transit_gateway.create` | bool | `true` | Create a prefix-named TGW vs adopt an existing one. Since `v1.8.0`. |
| `resources.transit_gateway.existing` | string | (empty) | Existing TGW name/ID when `create: false` and the TGW jumphost needs it. |
| `resources.registry_cos.create` | bool | `true` | Create the registry COS instance vs adopt an existing one. |
| `resources.registry_cos.existing` | string | (empty) | Existing COS instance name when `create: false`. |
| `resources.cert_manager.create` | bool | `true` | Install cert-manager (`install_cert_manager`). |
| `resources.bnk.create` | bool | `true` | Deploy BIG-IP Next for Kubernetes (`deploy_bnk`). |
| `resources.tgw_jumphost.create` | bool | `true` | Create the TGW test jumphost. |
| `resources.cluster_jumphosts.create` | bool | `false` | Create per-zone cluster jumphosts. |
| `resources.client_vpc.create` | bool | `false` | Create a new client VPC for the TGW jumphost. |
| `resources.client_vpc.existing` | string | (empty) | Existing client VPC name when `create: false`. |
| `bnk.cneinstance_size` | string | `Small` | `Small` \| `Medium` \| `Large`. |
| `bnk.far_repo_url` | string | `repo.f5.com` | FAR image registry URL. |
| `bnk.manifest_version` | string | `2.3.0-3.2598.3-0.0.170` | f5-bigip-k8s-manifest chart version. |
| `bnk.license_mode` | string | (empty ⇒ `connected`) | `connected` \| `disconnected` \| `f5licenseproxy`. Rendered as the `license_mode` tfvar. `f5licenseproxy` licenses BNK via the [F5 License Proxy](./10c-flp-licensing.md) — either one this workspace deployed (`roksbnkctl flp up`) or one in **another** cluster (`bnk.flp.external`). Empty/omitted keeps the JWT/connected default. |
| `bnk.flp.namespace` | string | `f5-license-proxy` | Namespace the FLP phase installs into (FLP mode only). |
| `bnk.flp.chart_version` | string | (empty ⇒ from the BNK manifest) | Pin the `f5-license-proxy` chart version. Normally unset — the version is read from the BNK manifest, like the FLO and CIS charts. |
| `bnk.flp.node_port_access` | bool | `false` | Expose the proxy OUTSIDE its cluster so a BNK install in a different cluster can license through it ([Flow C](./10c-flp-licensing.md#flow-c--a-shared-licensing-cluster)). Set by `flp up --add-node-port-access` and persisted, so a later `flp up` does not tear the exposure down. |
| `bnk.flp.node_port_source_cidr` | string | (empty) | With `node_port_access`: open the proxy's NodePort to this CIDR (the consuming cluster's subnet) on the worker security group. |
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
| `cos.instance` | string | (empty) | COS instance name or CRN. |
| `cos.bucket` | string | (empty) | Bucket name. |
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
