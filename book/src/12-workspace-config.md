# Workspace config (config.yaml)

This chapter is the field-by-field reference for the per-workspace `config.yaml`. If you've read [Chapter 6 — Workspaces](./06-workspaces.md) you've seen the on-disk layout; this chapter zooms in on the YAML file that drives everything else (`init`, `up`, `down`, `cluster up`, the test suite, the SSH targets, the new execution backends).

You don't usually edit this file by hand. `roksbnkctl init` generates it interactively; later runs read it. But because every other knob in the tool reads from here, it's worth knowing what every field means and what defaults apply when you leave one out.

## File location

Each workspace's config lives at:

```
~/.roksbnkctl/<workspace>/config.yaml
```

Override the base directory with the `ROKSBNKCTL_HOME` env var (test fixtures use this; everyday users shouldn't need it). The file is created mode `0644` — readable by your user, the same trust posture as the surrounding workspace directory.

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
ibmcloud:        # IBM Cloud account + auth
  region: ca-tor
  resource_group: default
  api_key_source: keychain
  # api_key_b64: <base64-of-api-key>   # OPTIONAL fallback when keychain unavailable

cluster:         # ROKS cluster identity
  create: true
  name: tf-openshift-cluster
  openshift_version: "4.18"
  workers_per_zone: 2

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
  instance: bnk-orchestration
  bucket: bnk-schematics-resources
```

Every block except `ibmcloud:`, `cluster:`, and `tf_source:` is optional. Omit a block and the tool falls through to either a documented default (covered below) or the upstream HCL's own default for terraform variables.

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
  openshift_version: "4.18"
  workers_per_zone: 2
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `create` | bool | `true` | When `true`, `roksbnkctl cluster up` provisions a new ROKS cluster. When `false`, `cluster register <name>` adopts an existing one. |
| `name` | string | none — required | OpenShift cluster name when `create=true`; cluster ID-or-name to adopt when `create=false`. |
| `openshift_version` | string | empty (latest) | E.g. `"4.18"`. Empty lets IBM Cloud pick the current default. Quote it — YAML otherwise parses `4.18` as a float. |
| `workers_per_zone` | int | `1` | Worker nodes per AZ; cluster runs across 3 AZs by default in MZR regions, so `2` ⇒ 6 workers total. |

The `cluster:` block translates to terraform variables `create_roks_cluster`, `openshift_cluster_name`, `roks_cluster_id_or_name`, `openshift_cluster_version`, `roks_workers_per_zone` — see [Chapter 13](./13-terraform-variables.md) and [Chapter 29](./29-terraform-variable-reference.md) for the full mapping.

## `bnk:`

```yaml
bnk:
  cneinstance_size: Small
  far_repo_url: repo.f5.com
  manifest_version: 2.3.0-3.2598.3-0.0.170
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `cneinstance_size` | enum | upstream HCL default (`Small`) | `Small` \| `Medium` \| `Large`. Sets `cneinstance_deployment_size`. |
| `far_repo_url` | string | upstream HCL default (`repo.f5.com`) | The FAR Docker/Helm repo. Override only for staging/internal repos. |
| `manifest_version` | string | upstream HCL default | Pin a specific BNK manifest chart version. Leave empty to track the upstream HCL's pin. |

Every field here is optional — leave the block out entirely and you get the upstream HCL's defaults for all three.

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
  instance: bnk-orchestration
  bucket: bnk-schematics-resources
  upload:
    - source: ./local/f5-far-auth-key.tgz
      key: f5-far-auth-key.tgz
    - source: ./local/trial.jwt
      key: trial.jwt
```

| Field | Type | Notes |
|---|---|---|
| `instance` | string | COS instance name holding the FAR auth key + JWT. |
| `bucket` | string | COS bucket name within that instance. |
| `upload` | []{source, key} | Optional pre-flight uploads from local files into the bucket. Useful for CI scenarios where the supply-chain artefacts are produced by the pipeline. |

The block is optional — if you've already populated COS by hand or via the upstream HCL's `roks_cos_instance_name` variable, you don't need it. [Chapter 25 — COS supply chain management](./25-cos-supply-chain.md) covers the full workflow.

## Behaviour when fields are missing

`roksbnkctl` falls through three layers in order: **workspace config → upstream HCL default → fail**.

| Missing field | Behaviour |
|---|---|
| `ibmcloud.region` | `roksbnkctl init` prompts; programmatic loads error with "region is empty". |
| `ibmcloud.api_key_source` | Resolver walks the full chain (env → keychain → config → prompt). |
| `ibmcloud.api_key_b64` | Skipped in the resolver chain. |
| `cluster.name` | `init` prompts; programmatic loads error. |
| `cluster.openshift_version` | Empty string passed to upstream HCL; the module picks the current default. |
| `cluster.workers_per_zone` | Falls through to `1` (upstream default). |
| `bnk.*` | Field is omitted from the generated `terraform.tfvars` and the upstream HCL default applies. |
| `tf_source` | Treated as `type: embedded` (legacy default). |
| `targets.*` | Block absent ⇒ `roksbnkctl --on jumphost` errors with "no target named jumphost"; auto-populated by `up`. |
| `exec.*` | Per-tool defaults at v1.0: `ibmcloud`→`local`, `terraform`→`local`, `iperf3`→`k8s`, DNS probe→`local`. Override per-tool via this block, or per-invocation via `--backend`. |
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

The `IBMCLOUD_API_KEY` is the one exception that **never** goes through tfvars on disk. It's passed as a `TF_VAR_ibmcloud_api_key` env var on the terraform invocation. `--var-file` cannot supply the API key — the resolver chain in [Chapter 14](./14-credentials-resolver.md) is the only path.

## Editing by hand vs helpers

Several commands manage subsets of `config.yaml` so you don't have to:

| Subset | Helper |
|---|---|
| Whole file (interactive) | `roksbnkctl init` |
| `tf_source:` only | `roksbnkctl init --upgrade-tf` |
| `targets:` block | `roksbnkctl targets add/remove` |
| `ibmcloud.api_key_b64` | `roksbnkctl init` (after entering the key, it offers to save) |

When you do edit by hand, the load-time validators run on next `roksbnkctl` invocation:

- The plaintext-secret heuristic rejects an `api_key:` field (it must be `api_key_b64:` to be tolerated).
- Workspace name validation runs on directory access (workspace names must match `[A-Za-z0-9][A-Za-z0-9_.-]{0,63}`).
- YAML parse errors surface a line number.

If a hand edit breaks the file, every command that reads the workspace fails fast with the parse error path, so you'll know within one invocation.

## Worked example: bootstrap a workspace from scratch

End-to-end Part IV scenario: brand-new laptop, no `roksbnkctl` workspaces yet, an IBM Cloud API key in your password manager. Goal: a usable workspace with the key in the OS keychain, the right region + resource group resolved, and `terraform.tfvars` ready to drive the HCL.

```bash
# 1. roksbnkctl init — interactive bootstrap
$ roksbnkctl init
Workspace name [default]: dev
IBM Cloud region [ca-tor]:
IBM Cloud resource group [default]:
Enter IBM Cloud API key (input hidden):
Save the key for future runs? [Y/n]: y
  ✓ saved to OS keychain (service: roksbnkctl, account: dev/ibmcloud_api_key)
Cluster name [tf-openshift-cluster]: dev-cluster
Workers per zone [1]: 2
✓ Created workspace "dev"
```

The resulting `~/.roksbnkctl/dev/config.yaml`:

```yaml
ibmcloud:
  region: ca-tor
  resource_group: default
  api_key_source: keychain
cluster:
  create: true
  name: dev-cluster
  workers_per_zone: 2
tf_source:
  type: embedded
```

That's the minimum. Everything else (`bnk:`, `test:`, `targets:`, `exec:`, `cos:`) is empty and falls through to defaults. The API key can also be supplied non-interactively from your password manager's CLI by setting `IBMCLOUD_API_KEY` in the environment of the `init` invocation:

`op` here is the [1Password CLI](https://developer.1password.com/docs/cli/); the `op://...` URI is its secret-reference scheme. Any password-manager CLI that prints a secret to stdout works the same way — Bitwarden (`bw`), gopass, `aws secretsmanager get-secret-value`, Doppler, etc. — the only thing roksbnkctl cares about is that `IBMCLOUD_API_KEY` is set in the environment when `init` runs.

```bash
# Alternative: pre-set IBMCLOUD_API_KEY so init resolves it from env rather than prompting
IBMCLOUD_API_KEY=$(op read 'op://Private/IBM Cloud/api-key') roksbnkctl init -w dev
```

[Chapter 14 §"The `IBMCLOUD_API_KEY` resolver chain"](./14-credentials-resolver.md#the-ibmcloud_api_key-resolver-chain) covers the full env → keychain → workspace `api_key_b64` → TTY-prompt order; this env-var path is the first link in that chain, so anything `init` resolves at bootstrap time follows the same precedence later invocations use. Once `init` has saved the key to the OS keychain (the default sink), no further prompting is needed. `init` still prompts interactively for the remaining workspace metadata (region, resource group, cluster name) — a fully non-interactive bootstrap is on the v1.x roadmap.

Now render `terraform.tfvars` so subsequent `up` runs have explicit HCL inputs to point `--var-file` at:

```bash
# 2. Render terraform.tfvars from config.yaml
$ roksbnkctl tfvars -w dev > ~/.roksbnkctl/dev/terraform.tfvars
$ head ~/.roksbnkctl/dev/terraform.tfvars
ibmcloud_region        = "ca-tor"
ibmcloud_resource_group = "default"
cluster_name           = "dev-cluster"
workers_per_zone       = 2
# ...
```

[Chapter 13](./13-terraform-variables.md) covers the precedence rules between `config.yaml`, `terraform.tfvars`, and `terraform.tfvars.user` (the hand-edit overlay).

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
