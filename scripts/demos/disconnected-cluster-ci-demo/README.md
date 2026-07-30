# Disconnected-cluster CI demo (Argo Workflows / GitOps)

The **same air-gapped BNK install** as the
[disconnected-cluster CLI demo](../disconnected-cluster-cli-demo/README.md), told the way it
ships in a pipeline. GitOps here means a **pipeline**, not sync-from-git: **Argo Workflows**
runs the `roksbnkctl-tools-runner` container through two Workflows — **no git repo, no ArgoCD
`Application`**, just `argo submit` the Workflow YAMLs directly.

- **Workflow 1 — mirror:** `init` → `registry replicate` → `registry verify` (FAR → Harbor)
- **Workflow 2 — install:** `cluster register` → `bnk up` → `bnk status` (adopt the existing
  air-gapped ROKS cluster over the TGW, images from Harbor, license via the FLP)

Each roksbnkctl step is its own pod with its own status/logs/retries. The two Workflows share a
**persistent PVC**, so the workspace (and the mirror CA it records) carries across — and a later
`bnk down` teardown is clean, unlike an ephemeral `emptyDir` whose tfstate dies with the pod.

**Infrastructure setup — plain `ibmcloud` + cloud-init + `roksbnkctl flp up`, exactly as the CLI
demo, NOT Argo Workflows:** the services VPC + Harbor VSI, the standalone FLP VSI, and the new
Argo Workflows VSI. Only the **BNK deployment** — the runner's roksbnkctl pipeline — is driven by
Argo Workflows.

## Topology

```
Services VPC (the only egress) — on the existing Transit Gateway
  ├─ Harbor VSI   private mirror                (built here, as in the CLI demo)
  ├─ FLP VSI      standalone F5 License Proxy   (built here, via `roksbnkctl flp up`)
  └─ Argo VSI     k3s + Argo Workflows  (NEW)   ← the pipeline controller
Existing ROKS cluster  disco-demo (private, no worker egress) — reached over the TGW
```

The Argo VSI has egress, so k3s / Argo Workflows / the runner image pull normally. Only the ROKS
**cluster** is air-gapped; the runner pods (in k3s on the VSI) reach Harbor's private IP and the
ROKS master over the TGW, and IBM Cloud APIs via the VSI's egress.

## Why `>= v1.33.0`

The runner image **must be ≥ v1.33.0** — the first tag with roksbnkctl's **native operator +
node CA trust**. A container operator has no OS trust for Harbor's self-signed CA, so on older
runners the mirror push (`registry replicate`), `registry verify`, and `bnk up`'s terraform-helm
chart pulls all fail `x509: unknown authority`. v1.33.0 resolves the captured/recorded mirror CA
and trusts it for the operator's crane/helm/terraform paths, and installs it on the nodes.

## The Argo Workflows UI

Like Harbor and the FLP, the controller has a reachable web UI — cloud-init runs argo-server with
`--auth-mode=server` (no login) on NodePort **30746**, so the demo can open
`https://<argo-vsi-fip>:30746/workflows/bnk-ci` and watch the step DAG go green (the VSI security
group must allow `30746`).

## Prerequisites

**An existing air-gapped ROKS cluster** on the Transit Gateway with **BNK not installed** (the
pipeline adopts it and installs from clean). Everything else — the services VPC, Harbor, the FLP,
and the Argo VSI — this demo builds. **You provide** (see `.env.example`): an IBM Cloud API key,
`REGION`/`CLUSTER_NAME`, the reused Harbor/FLP coordinates + `HARBOR_ADMIN_PASSWORD`, the
`FAR_COS_BUCKET` (the orchestration COS bucket holding `f5-far-auth-key.tgz` + `subscription.jwt`),
and an `SSH_KEY_FILE` / `SSH_KEY_NAME`.

Tools on this (Ubuntu) control host — only ssh + a couple of CLIs (`openssh-client`, `jq`,
`gettext-base`, the `ibmcloud` CLI with `vpc-infrastructure`). Everything else — k3s, Argo
Workflows, the argo CLI — is installed **on the Argo VSI** by its cloud-init; the entire BNK
toolchain (terraform, helm, kubectl, roksbnkctl) is inside the **runner image**.

## Run it (interactive)

```bash
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./disconnected-cluster-ci-demo.sh         # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./disconnected-cluster-ci-demo.sh` prints every command without running it.

## Record it

```bash
./record.sh                               # → demo-video/disconnected-cluster-ci-demo.mp4
```

`record.sh` runs the demo hands-off in a headless X terminal, captures it with ffmpeg, then
10×-speeds the long deployment windows and **holds each roksbnkctl / argo command frame for 5
seconds**. Needs `Xvfb`, `xterm`, `ffmpeg`, `python3`.

## Files

| File | What |
|---|---|
| `disconnected-cluster-ci-demo.sh` | the demo (six phases; Argo Workflows drives the runner) |
| `workflows/00-prereqs.yaml` | bnk-ci namespace, the persistent `bnk-work` PVC, `bnk-runner` SA + RBAC |
| `workflows/wf-mirror.yaml` | Workflow 1 — `init` → `registry replicate` → `registry verify` |
| `workflows/wf-install.yaml` | Workflow 2 — `cluster register` → `bnk up` (with a cwc-guard sidecar) → `bnk status` |
| `gitops/disconnected-bnk/00-configmap.yaml` | `bnk.yaml` for the runner (no secrets) |
| `argo-vsi-cloud-init.yaml.tmpl` | cloud-init for the Argo VSI (k3s + Argo Workflows + argo CLI + UI) |
| `.env.example` | every input, with defaults |
| `record.sh` | one-line wrapper around the shared recorder (`../lib/record-demo.sh`) |

## Teardown

The persistent PVC makes teardown clean (it still holds the terraform state):

```bash
# from a runner pod / Workflow against the bnk-work PVC:
roksbnkctl -w bnk bnk down --auto          # destroys BNK incl. the IAM trusted profile + secrets
# then delete the Argo VSI + its floating IP
ibmcloud is instance-delete "$ARGO_VSI_NAME" --force
ibmcloud is floating-ip-release "${ARGO_VSI_NAME}-fip" --force
```

The reused Harbor / FLP / cluster belong to the CLI demo.
