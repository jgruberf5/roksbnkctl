# Disconnected-cluster CLI demo

A **fully disconnected** BNK install onto a ROKS cluster you **already have**, over
a Transit Gateway you **already have** (the Appendix A topology). Everything runs
from the CLI. Two small VSIs in a services VPC do the work:

- **Harbor VSI** — a private registry seeded from F5's artifact registry; also the
  operator host that runs `roksbnkctl` and drives the install over the TGW.
- **Standalone FLP VSI** — the F5 License Proxy (floating IP for licensing + status).

The cluster pulls every chart/image from Harbor by its **private IP**; `bnk up`
installs Harbor's CA on every node automatically and licenses through the FLP — one
pass. This is the interactive, reproducible version of the recorded demo.

## Prerequisites

**You provide** (see `.env.example`):
- an **existing, no-BNK ROKS cluster** (`EXISTING_CLUSTER`), private (no worker egress),
- an **existing Transit Gateway** (`TGW_NAME`) the cluster VPC is attached to,
- an IBM Cloud **API key** and an IBM Cloud VPC **SSH key**,
- the F5 **FAR pull credential** + **subscription JWT** (from F5).

**Tools on this (Ubuntu) host:**

```bash
sudo apt-get update
sudo apt-get install -y jq gettext-base openssh-client curl   # gettext-base = envsubst
# IBM Cloud CLI + VPC plugin
curl -fsSL https://clis.cloud.ibm.com/install/linux | sh
ibmcloud plugin install vpc-infrastructure -f
# roksbnkctl (or point ROKSBNKCTL_BIN at a build)
#   see book Chapter 4 — Installation
```

The **Harbor and FLP VSIs** install their own tooling via cloud-init
(`harbor-cloud-init.yaml.tmpl`) and `roksbnkctl flp up`; nothing to install there.

## Run it (interactive)

```bash
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./disconnected-cluster-cli-demo.sh        # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./disconnected-cluster-cli-demo.sh` prints every command without running
it — a safe first pass to read the flow.

## Files

| File | What |
|---|---|
| `disconnected-cluster-cli-demo.sh` | the demo (five phases: services VPC + Harbor → mirror → FLP → adopt cluster → `bnk up`) |
| `harbor-cloud-init.yaml.tmpl` | Harbor VSI cloud-init (rendered with `envsubst`) |
| `flp-status-build/` | Dockerfile + static `flp-status` binary the demo builds + pushes to Harbor |
| `record.sh`, `post_10x.py` | the recording pipeline (xvfb + ffmpeg, 10×-speeds the long phases) — for cutting the video, not needed to run the demo |

## Teardown

```bash
roksbnkctl -w bnk bnk down --auto          # remove BNK from the adopted cluster
roksbnkctl -w flp down --auto              # the standalone FLP VSI
# then delete the services VPC + Harbor VSI (see the book, Appendix A — Teardown)
```
