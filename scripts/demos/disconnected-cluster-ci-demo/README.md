# Disconnected-cluster CI demo (ArgoCD / GitOps)

The **same air-gapped BNK install** as the
[disconnected-cluster CLI demo](../disconnected-cluster-cli-demo/README.md), told the
way it ships in a pipeline: **GitOps**. The demo is **self-contained** — it builds its own
services VPC, Harbor mirror, FLP appliance and ArgoCD controller (the SAME way the CLI demo
does), then hands the one part that is genuinely a deployment pipeline — the **roksbnkctl
BNK install** — to **ArgoCD**. ArgoCD syncs a git repo whose one Job runs the
`roksbnkctl-tools-runner` container: mirror FAR → Harbor, adopt the existing air-gapped
ROKS cluster over the TGW, and install BNK — images from Harbor, license via the FLP.

**What runs where — the whole point of this demo:**

- **Infrastructure setup — plain `ibmcloud` + cloud-init + `roksbnkctl flp up`, exactly as
  the CLI demo, NOT ArgoCD:** the services VPC + Harbor VSI, the standalone FLP VSI, and the
  new ArgoCD VSI. This is the standing plumbing; how it is built is not the GitOps story.
- **The BNK deployment — the ONLY part driven by ArgoCD:** the runner Job's roksbnkctl
  pipeline — `init` → `registry replicate` / `verify` (mirror) → `cluster register` (adopt)
  → `bnk up` → `bnk status`.

## Topology

```
Services VPC (the only egress) — on the existing Transit Gateway
  ├─ Harbor VSI      private mirror                (built here, as in the CLI demo)
  ├─ FLP VSI         standalone F5 License Proxy   (built here, via `roksbnkctl flp up`)
  └─ ArgoCD VSI      k3s + ArgoCD  (NEW)           ← the GitOps controller
Existing ROKS cluster  disco-demo (private, no worker egress) — reached over the TGW
```

The ArgoCD VSI has egress, so k3s / ArgoCD / the runner image pull normally. Only the
ROKS **cluster** is air-gapped; the runner Job (in k3s on the VSI) reaches Harbor's
private IP and the ROKS master over the TGW, and IBM Cloud APIs via the VSI's egress.

## What it does

**Setup (exactly as the CLI demo — not ArgoCD):**

| # | Phase | What runs |
|---|---|---|
| 1 | Services VPC + Harbor VSI | `ibmcloud is vpc/subnet/instance-create` + cloud-init (verify TGW attach) |
| 2 | Standalone FLP VSI | `roksbnkctl -w flp flp up` on the operator VSI |
| 3 | ArgoCD controller VSI | one `bx2-4x16`, cloud-init installs k3s + ArgoCD + a git-daemon |

**The GitOps deployment (the ONLY roksbnkctl parts in ArgoCD):**

| # | Phase | What runs |
|---|---|---|
| 4 | The GitOps repo + secret | push the ConfigMap + runner Job to the VSI git-daemon; `kubectl create secret bnk-secrets` (API key, Harbor pw, FLP handoff — never in git) |
| 5 | Register + sync the app | `argocd app create … --core`, then `argocd app sync` fires the Sync-hook Job |
| 6 | The runner drives the install | stream `kubectl logs -f` of the Job: `init` → `registry replicate`/`verify` → `cluster register` → `bnk up` → `bnk status` |
| 7 | Verify | `bnk status` → License Active; the **ArgoCD web UI** (`http://<argocd-vsi-fip>:30080/`, admin / `argocd admin initial-password`) shows the app Synced/Healthy |

Like Harbor and the FLP, the ArgoCD **console has a reachable web UI** — cloud-init serves
it on plain HTTP at NodePort **30080** over the ArgoCD VSI's floating IP, so the demo can
open it and watch the app go Synced/Healthy (the VSI's security group must allow `30080`).

The runner Job's args are the whole disconnected install, and they are what the demo
teaches on screen (held 5s in the recording):

```
roksbnkctl -w bnk init --config-file /config/bnk.yaml --override-from-env
roksbnkctl -w bnk registry replicate --target generic
roksbnkctl -w bnk registry verify
roksbnkctl -w bnk cluster register disco-demo
roksbnkctl -w bnk bnk up --auto
roksbnkctl -w bnk bnk status
```

The runner image **must be ≥ v1.33.0** — that is the first tag with roksbnkctl's
native node-CA-trust, so the air-gapped nodes trust Harbor's self-signed CA with no
manual DaemonSet. It builds a real ArgoCD VSI and installs BNK — expect **~30–45 min**.

## Prerequisites

**An existing air-gapped ROKS cluster** on the Transit Gateway with **BNK not installed**
(the pipeline adopts it and installs from clean). Everything else — the services VPC, the
Harbor mirror, the FLP appliance, and the ArgoCD VSI — this demo **builds itself**, exactly
as the CLI demo does; nothing from a prior CLI-demo run needs to be up.

**You provide** (see `.env.example`): an IBM Cloud **API key**, the target `REGION` /
`TGW_NAME` / `EXISTING_CLUSTER`, the `HARBOR_ADMIN_PASSWORD`, the FAR auth + subscription
JWT (local files, mirrored into Harbor), and an `SSH_KEY_FILE` / `SSH_KEY_NAME` for the VPC
ssh key — the same inputs as the CLI demo, plus the ArgoCD-VSI knobs.

**Tools on this (Ubuntu) control host — only ssh + a couple of CLIs:**

```bash
sudo apt-get update && sudo apt-get install -y openssh-client jq gettext-base
# ibmcloud CLI (provisions the ArgoCD VSI + resolves the NIC/floating IP)
curl -fsSL https://clis.cloud.ibm.com/install/linux | sh
ibmcloud plugin install vpc-infrastructure -f
```

Everything else — k3s, ArgoCD, the argocd CLI, git-daemon — is installed **on the
ArgoCD VSI** by its cloud-init; and the entire BNK toolchain (terraform, helm,
kubectl, roksbnkctl) is inside the **runner image**, never on this host.

## Run it (interactive)

```bash
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./disconnected-cluster-ci-demo.sh         # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./disconnected-cluster-ci-demo.sh` prints every command without running
it — a safe first pass to read the flow.

## Record it

```bash
./record.sh                               # → demo-video/disconnected-cluster-ci-demo.mp4
```

`record.sh` runs the demo hands-off in a headless X terminal, captures it with
ffmpeg, then 10×-speeds the long deployment windows and **holds each `roksbnkctl`
command frame on screen for 5 seconds**. There is no voiceover — the on-screen
context lines carry the narration. Needs `Xvfb`, `xterm`, `ffmpeg`, `python3`.

## Files

| File | What |
|---|---|
| `disconnected-cluster-ci-demo.sh` | the demo (six phases; the GitOps controller drives the runner Job) |
| `gitops/disconnected-bnk/00-configmap.yaml` | `bnk.yaml` for the runner (no secrets; committed to git) |
| `gitops/disconnected-bnk/10-job.yaml` | the runner Job — a ConfigMap-driven, one-shot ArgoCD Sync hook |
| `argocd-vsi-cloud-init.yaml.tmpl` | cloud-init for the ArgoCD VSI (k3s + ArgoCD + git-daemon) |
| `.env.example` | every input, with defaults |
| `record.sh` | one-line wrapper around the shared recorder (`../lib/record-demo.sh`) |

## Teardown

The reused Harbor / FLP / cluster belong to the CLI demo; this demo only adds the
ArgoCD VSI and the ArgoCD app:

```bash
# remove the app + the BNK install, leaving the cluster up for the CLI demo/teardown
ssh -i "$SSH_KEY_FILE" ubuntu@<argocd-fip> 'export KUBECONFIG=/home/ubuntu/.kube/config; argocd app delete bnk-disconnected --core -y'
roksbnkctl -w bnk bnk down --auto
# then delete the ArgoCD VSI + its floating IP
ibmcloud is instance-delete "$ARGOCD_VSI_NAME" --force
ibmcloud is floating-ip-release "${ARGOCD_VSI_NAME}-fip" --force
```
