# GitHub Actions CI as an example

roksbnkctl is built to be driven from a pipeline: **config is the input, the
environment supplies the values, and one image carries every tool.** This chapter
is a worked GitHub Actions example — a single cluster, then a fleet — using the
[unattended / `--non-interactive`](./07a-unattended-setup.md) flow and the
[all-in-one runner image](./04-installation.md#path-c--run-from-the-all-in-one-container-image-no-install).

The whole model in one breath: **GitHub secrets → `ROKSBNKCTL_*` env →
`init --non-interactive` builds `config.yaml` → `up` reconciles it.** No API key
in the repo, no hand-edited config, no interactive prompts.

## The runner image as a job container

Run the job *in* `roksbnkctl-tools-runner` — it has `roksbnkctl` plus terraform,
helm, kubectl, oc, and the IBM Cloud CLI on `PATH`, so the workflow needs nothing
installed:

```yaml
jobs:
  deploy:
    runs-on: ubuntu-latest
    container:
      image: ghcr.io/jgruberf5/roksbnkctl-tools-runner:latest
    steps:
      - run: roksbnkctl version
```

## Ephemeral runners need remote state

A GitHub-hosted runner is thrown away after the job, so a later **teardown** run
can't see the terraform state from the **deploy** run. Put the state in COS so it
survives across runs (and across runners) — see [Remote state](./12a-remote-state.md):

```yaml
state: { backend: s3 }   # in config.yaml; HMAC keys come from env (below)
```

Supply the bucket via config and the HMAC keys via env
(`ROKSBNKCTL_COS_HMAC_ACCESS_KEY` / `_SECRET_KEY`).

## A single-cluster workflow

```yaml
name: Deploy BNK
on:
  workflow_dispatch:
    inputs:
      region:  { description: IBM Cloud region, default: eu-de }
      prefix:  { description: Resource prefix,  default: ci }

jobs:
  deploy:
    runs-on: ubuntu-latest
    container: { image: ghcr.io/jgruberf5/roksbnkctl-tools-runner:latest }
    env:
      # secrets → the credential + cluster identity
      IBMCLOUD_API_KEY:               ${{ secrets.IBMCLOUD_API_KEY }}
      ROKSBNKCTL_REGION:              ${{ inputs.region }}
      ROKSBNKCTL_RESOURCE_GROUP:      default
      ROKSBNKCTL_PREFIX:              ${{ inputs.prefix }}
      ROKSBNKCTL_CLUSTER_NAME:        ${{ inputs.prefix }}-roks
      ROKSBNKCTL_CLUSTER_CREATE:      "true"
      # adopt shared infrastructure (reuse is config — no extra commands)
      ROKSBNKCTL_TRANSIT_GATEWAY_NAME: shared-corp-tgw
      # remote state so teardown works from a fresh runner
      ROKSBNKCTL_COS_HMAC_ACCESS_KEY: ${{ secrets.COS_HMAC_ACCESS_KEY }}
      ROKSBNKCTL_COS_HMAC_SECRET_KEY: ${{ secrets.COS_HMAC_SECRET_KEY }}
    steps:
      - name: Seed config.yaml from the environment
        run: roksbnkctl -w ci init --non-interactive

      - name: Deploy (cluster + BNK + testing, per config)
        run: roksbnkctl -w ci up --auto

      - name: Publish cluster outputs
        run: |
          roksbnkctl -w ci cluster output --json | tee -a "$GITHUB_STEP_SUMMARY"
          echo "roks_cluster_id=$(roksbnkctl -w ci cluster output roks_cluster_id)" >> "$GITHUB_OUTPUT"
```

`init --non-interactive` logs which fields it pulled from the environment (never
the values), then `up` brings up the cluster (create *or* attach, per
`cluster.create`) and **BNK ∥ Testing** — exactly the phases the config enables.
Add `roksbnkctl -w ci gateway up --auto` if the deployment includes the gateway
phase.

## A fleet: deploy many clusters with a matrix

The thing that makes this a *CI* tool is that the same env-driven flow scales to a
fleet — one workspace per cluster, varied by the matrix, all sharing one TGW:

```yaml
name: Deploy fleet
on: { workflow_dispatch: {} }

jobs:
  deploy:
    runs-on: ubuntu-latest
    container: { image: ghcr.io/jgruberf5/roksbnkctl-tools-runner:latest }
    strategy:
      fail-fast: false
      matrix:
        cluster:
          - { ws: eu,  region: eu-de,   prefix: acme-eu }
          - { ws: us,  region: us-south, prefix: acme-us }
          - { ws: jp,  region: jp-tok,   prefix: acme-jp }
    env:
      IBMCLOUD_API_KEY:                ${{ secrets.IBMCLOUD_API_KEY }}
      ROKSBNKCTL_RESOURCE_GROUP:       default
      ROKSBNKCTL_CLUSTER_CREATE:       "true"
      ROKSBNKCTL_TRANSIT_GATEWAY_NAME: shared-corp-tgw
      ROKSBNKCTL_COS_HMAC_ACCESS_KEY:  ${{ secrets.COS_HMAC_ACCESS_KEY }}
      ROKSBNKCTL_COS_HMAC_SECRET_KEY:  ${{ secrets.COS_HMAC_SECRET_KEY }}
    steps:
      - env:
          ROKSBNKCTL_REGION:       ${{ matrix.cluster.region }}
          ROKSBNKCTL_PREFIX:       ${{ matrix.cluster.prefix }}
          ROKSBNKCTL_CLUSTER_NAME: ${{ matrix.cluster.prefix }}-roks
        run: |
          roksbnkctl -w ${{ matrix.cluster.ws }} init --non-interactive
          roksbnkctl -w ${{ matrix.cluster.ws }} up --auto
```

Each matrix leg is one cluster; reuse-by-name (`ROKSBNKCTL_TRANSIT_GATEWAY_NAME`)
lets them share one network fabric. To dictate per-AZ addressing or attach a BIG-IP
for CIS, add the matching env vars (`ROKSBNKCTL_ZONE<n>_*`, `ROKSBNKCTL_BIGIP_*`) —
the full set is in [Unattended setup](./07a-unattended-setup.md#-override-from-env).

## Teardown

A separate manually-triggered (or PR-close) workflow tears a workspace down. Because
the state lives in COS, it runs cleanly on a fresh runner:

```yaml
name: Teardown
on: { workflow_dispatch: { inputs: { ws: { required: true } } } }
jobs:
  destroy:
    runs-on: ubuntu-latest
    container: { image: ghcr.io/jgruberf5/roksbnkctl-tools-runner:latest }
    env:
      IBMCLOUD_API_KEY:               ${{ secrets.IBMCLOUD_API_KEY }}
      ROKSBNKCTL_COS_HMAC_ACCESS_KEY: ${{ secrets.COS_HMAC_ACCESS_KEY }}
      ROKSBNKCTL_COS_HMAC_SECRET_KEY: ${{ secrets.COS_HMAC_SECRET_KEY }}
    steps:
      - run: roksbnkctl -w ${{ inputs.ws }} down --auto
```

(An *attached* existing cluster — `ROKSBNKCTL_CLUSTER_CREATE=false` — is not
destroyed; `down` tears down only what roksbnkctl created.)

## Notes

- **Pin the image by digest** in production CI (`...-runner@sha256:…`), not
  `:latest`, so a run can't silently change toolchains.
- **Secrets never reach the repo or the logs** — they enter as job env from GitHub
  Secrets, and `init` redacts which fields it applied.
- **This is the same contract bnk-forge uses.** The bnk-forge container step *is* a
  GitHub-Action-style invocation of this image with these env vars; this chapter is
  the do-it-yourself version of [Registering with BNK Forge](./24a-bnk-forge-registration.md).
- **Every phase has `status` + `output`.** `roksbnkctl <phase> status [--json]` and `<phase> output [name]` (scoped to that phase’s own attributes) are CI stage gates; `roksbnkctl output` merges them all — e.g. `roksbnkctl testing output testing_tgw_jumphost_ip`.
- **Where this is heading.** `up` already reconciles cluster + BNK + Testing from
  config; folding `gateway` in (so a single `up` is the whole deployment) plus
  `output -o json` / `validate` are the next step — see
  [PRD 17 — Declarative CLI](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/17-DECLARATIVE-CLI.md).
