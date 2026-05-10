# Migrating to roksbnkctl

This guide covers moving an existing F5 BIG-IP Next for Kubernetes (BNK) on IBM Cloud ROKS deployment workflow over to `roksbnkctl`. It also covers in-tree upgrades between roksbnkctl versions.

The book at [`book/src/`](./book/src/) is the canonical learning path for new users; this file is a focused reference for users who already have an environment, an automation pipeline, or a workspace from a previous version.

Cross-references:

- [Chapter 6 — Workspaces](./book/src/06-workspaces.md)
- [Chapter 12 — Workspace config](./book/src/12-workspace-config.md)
- [Chapter 13 — Terraform variables](./book/src/13-terraform-variables.md)
- [Chapter 14 — Credentials resolver](./book/src/14-credentials-resolver.md)
- [Chapter 17 — Execution backends](./book/src/17-execution-backends.md)

## From `bnk` (the pre-roksbnkctl bash-and-docker workflow)

The pre-roksbnkctl tooling was a bash-script driver plus a `bnk`-runner docker image — see PRD §"Background" for the rationale. roksbnkctl replaces every part of that workflow except the Terraform itself, which stays unchanged in its own repo and is consumed via a pinned source URL.

| Old (`bnk` bash workflow) | New (`roksbnkctl`) |
|---|---|
| `bnk init` / hand-edited `terraform.tfvars` | `roksbnkctl init` (interactive wizard writes `~/.roksbnkctl/<workspace>/config.yaml` and a derived `terraform.tfvars`) |
| `ibmcloud login --apikey @key.json` | API key resolved by the cred chain (env → OS keychain → workspace config → prompt); see Chapter 14 |
| `cd terraform && terraform init && terraform plan && terraform apply` | `roksbnkctl up` (runs init + plan + apply; idempotent and resumable) |
| `./scripts/fetch-kubeconfig.sh && export KUBECONFIG=…` | Auto-fetched post-apply; landed at `~/.roksbnkctl/<workspace>/state/kubeconfig` and pointed to via `KUBECONFIG` in `roksbnkctl shell` |
| `docker run bnk-runner …` for `ibmcloud` / `kubectl` / `oc` calls | Built-in passthrough verbs (`roksbnkctl ibmcloud …`, `roksbnkctl k …`) with `--backend local|docker|k8s|ssh:<target>` execution selection |
| Manual `iperf3` install on jumphost + manual port-forward | `roksbnkctl test throughput` (bundled image, k8s Job by default; no host install required) |
| Manual `dig` + comparing answers across vantages | `roksbnkctl test dns --gslb-compare` (multi-vantage probe; emits `gslb_divergence` boolean) |
| `terraform destroy` | `roksbnkctl down` |

The Terraform repo (`ibmcloud_terraform_bigip_next_for_kubernetes_2_3`) continues to be developed and versioned independently. roksbnkctl consumes it via the `tf_source` field in `config.yaml`; bump the pin with `roksbnkctl init --upgrade-tf`.

### Migrating your `terraform.tfvars` by hand

If you have an existing `terraform.tfvars` file you want to keep, copy it to `~/.roksbnkctl/<workspace>/terraform.tfvars.user` after running `roksbnkctl init`. roksbnkctl layers files in this order (later wins):

1. The auto-generated `terraform.tfvars` derived from `config.yaml`
2. `terraform.tfvars.user` (your hand-edited override)
3. Anything passed via `--var-file <path>` on the command line (repeatable)

See [Chapter 13](./book/src/13-terraform-variables.md) for the full layering rules.

## From "BNK deployment by hand" (no helper tool at all)

If you have been deploying BNK to ROKS manually — `ibmcloud login`, `terraform init/apply`, manual kubeconfig fetch, manual `iperf3` install on a jumphost — roksbnkctl collapses that workflow into the 3-command happy path:

```bash
roksbnkctl init     # interactive setup; writes workspace config
roksbnkctl up       # provision + deploy BNK
roksbnkctl test     # connectivity, DNS, throughput
```

Concretely, `roksbnkctl up` replaces:

- `ibmcloud login --apikey …` + `ibmcloud target -r <region> -g <rg>`
- `terraform init && terraform plan && terraform apply` against the BNK Terraform tree
- The post-apply kubeconfig fetch (`ibmcloud ks cluster config --cluster <id>`)
- The "now wait for FLO to come up" loop most users wire by hand
- The first-time `iperf3` install on the jumphost (now run as a one-shot Job in the cluster)

What you keep doing yourself:

- Choosing the IBM Cloud region, resource group, and OpenShift version (asked once by `roksbnkctl init`)
- Setting `bigip_username` / `bigip_password` / `bigip_url` if you're connecting BNK to an existing BIG-IP for CIS
- Bringing your own COS bucket if you don't want roksbnkctl to provision one for the OpenShift registry

### When to skip `roksbnkctl up` and apply terraform directly

For most users the answer is: never. If you genuinely want to drive terraform yourself but still benefit from roksbnkctl's tfvars rendering, run:

```bash
roksbnkctl tfvars render -w <ws>      # writes ~/.roksbnkctl/<ws>/state/terraform.tfvars
cd terraform-tree-of-your-choice
terraform apply -var-file=~/.roksbnkctl/<ws>/state/terraform.tfvars
```

The state file lives wherever terraform's local backend lands it. roksbnkctl's auto-discovery (`roksbnkctl up` re-using state from a prior `terraform apply`) only works when state lives at `~/.roksbnkctl/<ws>/state/terraform.tfstate` — adjust the working directory or copy the state file if you want to interoperate.

## From roksbnkctl v0.7 / v0.8 → v0.9 → v1.0

Per-version migration notes from `CHANGELOG.md`. roksbnkctl follows [semantic versioning](https://semver.org/) starting at `v0.9.0`; pre-v1.0 minor bumps may introduce breaking changes (always documented here and in `CHANGELOG.md`).

### Sprint 6 (v1.0 prep — pre-tag)

No breaking changes. New surface landing for the v1.0 cut:

- **Auto-generated reference chapters**: chapter 27 (command reference) and chapter 29 (terraform variable reference) are now generated from source via `go run ./tools/refgen/cobra-md` and `go run ./tools/refgen/tfvars-md`. Run these after any CLI or `variables.tf` change.
- **Doctor green-by-default refresh**: `roksbnkctl doctor` on a stock dev box with only `terraform` installed now exits 0 with zero warnings. `kubectl`, `oc`, `ibmcloud`, `iperf3`, and `dig` are rendered as informational rows naming the internalised path (`--backend docker` / `--backend k8s` / miekg-dns / client-go). A missing kubeconfig pre-`up` is informational rather than a warning. See [Chapter 5](./book/src/05-doctor.md).
- **Top-level `MIGRATING.md`** (this file): canonical migration reference.

If you have automation that grepped doctor output for the literal string `not on PATH` to detect missing optional tools, update it — the new informational rows render the path-or-not differently. The exit code semantic (0 on green / 1 on red) is unchanged.

### v0.9 (the four-backend release)

The cumulative v0.9 surface is documented in `CHANGELOG.md` §"v0.9.0". Migration highlights for users upgrading from a pre-v0.9 development build:

- **New `--backend` flag**: every command accepts `--backend local | docker | k8s | ssh:<target>` to pick the execution path. The workspace config's `exec:` block sets per-tool defaults; the `--backend` flag overrides for a single invocation.
- **`roksbnkctl ops install`**: provisions the in-cluster ops pod that `--backend k8s` needs for ad-hoc `ibmcloud` calls. Run once per cluster after `roksbnkctl up`.
- **DNS probe schemas namespaced**: JSON consumers that read `roksbnkctl test dns -o json` should switch from the old umbrella schema to either `roksbnkctl.dns.v1.vantage` (single-vantage) or `roksbnkctl.dns.v1` (multi-vantage `--gslb-compare`). See [Chapter 21](./book/src/21-dns-testing-gslb.md) §"JSON output schema".
- **`--backend docker` for terraform**: `roksbnkctl up/plan/apply/destroy --backend docker` runs terraform inside `hashicorp/terraform:1.5.7` against the workspace's state directory bind-mounted at `/state`. State files are written with the host user's UID/GID. `--backend k8s` and `--backend ssh:<target>` for terraform are deferred to v1.x (clear error at dispatch).
- **iperf3 SCC fix**: `roksbnkctl test throughput --backend k8s` now satisfies OpenShift's `restricted-v2` SCC out of the box. No action needed unless you were overriding the iperf3 image; if so, ensure your override sets `runAsNonRoot: true`, `runAsUser: 1000+`, `seccompProfile: RuntimeDefault`, and `capabilities.drop: [ALL]`.

### v0.8 and earlier

Pre-v0.9 builds were internal-only milestones (M0–M2). If you're sitting on one of those binaries, the cleanest path forward is `rm -rf ~/.roksbnkctl/` followed by a fresh `roksbnkctl init`. The pre-v0.9 state layout is not forward-compatible with v0.9+.

## Workspace migration

Every roksbnkctl invocation runs against exactly one workspace, identified by `--workspace <name>` (or `-w <name>`, or the current default). Workspaces live under `~/.roksbnkctl/<name>/` with this layout:

```
~/.roksbnkctl/
└── <workspace-name>/
    ├── config.yaml                 # workspace config (Chapter 12)
    ├── terraform.tfvars.user       # optional user override (Chapter 13) — workspace root, not state/
    └── state/
        ├── terraform.tfstate       # terraform local-backend state
        ├── terraform.tfstate.backup
        ├── terraform.tfvars        # auto-generated from config.yaml
        ├── kubeconfig              # admin kubeconfig fetched post-apply
        ├── cluster-outputs.json    # ROKS cluster identity (Chapter 9)
        └── tf-source/              # materialised terraform module tree
```

### What's preserved across roksbnkctl upgrades

- `config.yaml` — schema is forward-compatible within a major version. Pre-v0.9 → v0.9+ is a re-init.
- `state/terraform.tfstate` — terraform's own state, untouched by roksbnkctl upgrades.
- `state/kubeconfig` — yours; roksbnkctl writes it once on first cluster apply and never rewrites unless `--no-kubeconfig` is unset on a subsequent `up`.
- OS keychain entries (`service="roksbnkctl"`, `user="<workspace>/ibmcloud_api_key"`) — persistent across binary upgrades.

### What to back up before a major upgrade (pre-v1.0)

- `~/.roksbnkctl/<workspace>/state/terraform.tfstate` — the only file with operational consequences if lost. `cp ~/.roksbnkctl/<ws>/state/terraform.tfstate{,.pre-upgrade}` is enough.
- `~/.roksbnkctl/<workspace>/config.yaml` — easy to recreate via `roksbnkctl init`, but a backup avoids re-answering the wizard prompts.

The OS keychain entry survives binary upgrades; you don't need to export it.

### Cross-host workspace transfer

Workspaces are portable across hosts of the same OS family (Linux ↔ Linux, macOS ↔ macOS). To move a workspace:

```bash
# On the source host:
tar -czf my-ws.tgz -C ~/.roksbnkctl <workspace-name>

# On the destination host:
mkdir -p ~/.roksbnkctl
tar -xzf my-ws.tgz -C ~/.roksbnkctl
roksbnkctl ws use <workspace-name>

# Re-add the API key to the destination's keychain:
roksbnkctl init -w <workspace-name>   # picks up existing config; only prompts for the missing key
```

Cross-OS-family transfer (Linux → macOS) works for `config.yaml` + `state/terraform.tfstate` but the OS keychain entry doesn't migrate; set `IBMCLOUD_API_KEY` in env or re-prompt via `roksbnkctl init`.

## Getting help

- The book at [`book/src/`](./book/src/) covers every surface in depth.
- `roksbnkctl doctor` is the first stop when something doesn't work as expected.
- File issues at <https://github.com/jgruberf5/roksbnkctl/issues>; include `roksbnkctl version` output.
