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
ibmcloud:        # required
cluster:         # required
bnk:             # optional; populates upstream HCL bnk variables
test:            # optional; populates test.* settings
tf_source:       # required (defaults to embedded if omitted)
cos:             # optional; supply-chain auto-upload
targets:         # optional; populated automatically by up's post-apply hook
exec:            # optional; per-tool default-backend map
```

The order of the top-level keys in the file doesn't matter; YAML is a mapping. The order shown above is the canonical render order produced by `roksbnkctl init`.

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

All three fields are optional; omitting renders the HCL's own defaults. See [Chapter 13 — Terraform variables](./13-terraform-variables.md) for the upstream defaults.

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
| `ibmcloud.region` | string | (prompted) | IBM Cloud region (`ca-tor`, `us-south`, …). |
| `ibmcloud.resource_group` | string | `default` | Resource group name. |
| `ibmcloud.api_key_source` | string | (chain) | `env` \| `keychain` \| `config` \| `prompt`. |
| `ibmcloud.api_key_b64` | string | (empty) | Base64-encoded API key. Obfuscation only. |
| `cluster.create` | bool | `true` | Provision new vs attach existing. |
| `cluster.name` | string | (prompted) | Cluster name. |
| `cluster.openshift_version` | string | `4.18` | OpenShift minor version. |
| `cluster.workers_per_zone` | integer | `1` | Workers per AZ. |
| `bnk.cneinstance_size` | string | `Small` | `Small` \| `Medium` \| `Large`. |
| `bnk.far_repo_url` | string | `repo.f5.com` | FAR image registry URL. |
| `bnk.manifest_version` | string | `2.3.0-3.2598.3-0.0.170` | f5-bigip-k8s-manifest chart version. |
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

## How `--var-file` interacts with `config.yaml`

`roksbnkctl up --var-file <file>` layers user-supplied tfvars **after** the auto-rendered tfvars derived from `config.yaml`. Later wins, terraform-style. Multiple `--var-file` flags are accepted and stack in command-line order.

The auto-render path: `config.yaml` → typed `Workspace` struct → key/value tfvars → `~/.roksbnkctl/<ws>/state/terraform.tfvars`. The user's `--var-file` is appended to the terraform invocation as an additional `-var-file=<path>` argument. See [Chapter 13 — Terraform variables](./13-terraform-variables.md) for the layering rules.

A workspace-persistent override file is `~/.roksbnkctl/<ws>/terraform.tfvars.user` — when present, it's auto-layered after the rendered tfvars and before any explicit `--var-file`. Useful for "always pass this `bigip_password` value when applying this workspace" without putting it in `config.yaml` (where the plaintext-secret rejection would reject it).

## Cross-references

- [Chapter 12 — Workspace config](./12-workspace-config.md) — the teaching counterpart to this lookup.
- [Chapter 13 — Terraform variables](./13-terraform-variables.md) — how `config.yaml` fields render into tfvars.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md) — the `ibmcloud.api_key_*` semantics.
- [Chapter 29 — Terraform variable reference](./29-terraform-variable-reference.md) — the upstream HCL variable surface that `bnk.*` and `cluster.*` populate.
