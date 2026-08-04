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

## Building the services infrastructure (Harbor VSI + FLP VSI)

**`./disconnected-cluster-cli-demo.sh` builds all of this for you** — you do not run these by
hand for the CLI demo. They are documented here as the canonical way to stand up the services
infrastructure, e.g. to **pre-build Harbor + the FLP for the [CI demo](../disconnected-cluster-ci-demo/README.md)**,
which reuses them. Everything after `set -a; source .env; set +a` is cut-and-paste (the same
`.env` the demo uses).

```bash
set -a; source .env; set +a
ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$SVC_REGION" -g "$RESOURCE_GROUP" -q
TGW_ID=$(ibmcloud tg gateways --output json | jq -r --arg n "$TGW_NAME" '.[]|select(.name==$n)|.id')

# 1) Services VPC + subnet + public gateway (the only egress) + open 22/443, attached to the TGW
SVC_VPC_ID=$(ibmcloud is vpc-create "${SVC_PREFIX}-vpc" -g "$RESOURCE_GROUP" --output json | jq -r .id)
SVC_VPC_CRN=$(ibmcloud is vpc "$SVC_VPC_ID" --output json | jq -r .crn)
SUBNET_ID=$(ibmcloud is subnet-create "${SVC_PREFIX}-subnet" "$SVC_VPC_ID" --zone "$SVC_ZONE" \
              --ipv4-address-count 256 -g "$RESOURCE_GROUP" --output json | jq -r .id)
PGW_ID=$(ibmcloud is public-gateway-create "${SVC_PREFIX}-pgw" "$SVC_VPC_ID" "$SVC_ZONE" --output json | jq -r .id)
ibmcloud is subnet-update "$SUBNET_ID" --public-gateway-id "$PGW_ID"
SG=$(ibmcloud is vpc "$SVC_VPC_ID" --output json | jq -r .default_security_group.id)
for p in 22 443; do ibmcloud is security-group-rule-add "$SG" inbound tcp --port-min $p --port-max $p; done
ibmcloud tg connection-create "$TGW_ID" --name "${SVC_PREFIX}-conn" --network-type vpc --network-id "$SVC_VPC_CRN"
# wait until 'attached' before the cluster can reach Harbor over the TGW:
until [ "$(ibmcloud tg connections "$TGW_ID" --output json | jq -r --arg n "${SVC_PREFIX}-conn" '.[]|select(.name==$n)|.status')" = attached ]; do sleep 10; done

# 2) Harbor VSI — render its cloud-init, launch, then read its PRIVATE IP once it binds
export HARBOR_ADMIN_PASSWORD HARBOR_VERSION
envsubst '${HARBOR_ADMIN_PASSWORD} ${HARBOR_VERSION}' < harbor-cloud-init.yaml.tmpl > /tmp/harbor-ci.yaml
HARBOR_VSI_ID=$(ibmcloud is instance-create "${SVC_PREFIX}-harbor" "$SVC_VPC_ID" "$SVC_ZONE" \
    "$HARBOR_VSI_PROFILE" "$SUBNET_ID" --image ibm-ubuntu-22-04-5-minimal-amd64-17 \
    --keys "$SSH_KEY_NAME" -g "$RESOURCE_GROUP" --user-data @/tmp/harbor-ci.yaml --output json | jq -r .id)
# the reserved primary IP is 0.0.0.0 at create time — poll until it binds:
until IP=$(ibmcloud is instance "$HARBOR_VSI_ID" --output json | jq -r '.primary_network_interface.primary_ip.address') \
      && [ -n "$IP" ] && [ "$IP" != 0.0.0.0 ]; do sleep 10; done
echo "Harbor at $IP  (admin / $HARBOR_ADMIN_PASSWORD) — give cloud-init ~5 min to finish install"

# also give the VSI a floating IP for SSH (management only — Harbor's identity stays the private IP)
ibmcloud is floating-ip-reserve "${SVC_PREFIX}-harbor-fip" --zone "$SVC_ZONE"
VNI=$(ibmcloud is instance "$HARBOR_VSI_ID" --output json | jq -r '.primary_network_attachment.virtual_network_interface.id')
ibmcloud is virtual-network-interface-floating-ip-add "$VNI" "${SVC_PREFIX}-harbor-fip"
```

**3) The standalone FLP VSI** is built with `roksbnkctl flp up` (mode `vsi`) — run it **on the
Harbor/operator VSI** (which has the FAR credentials). The minimal `flp.yaml` and the exact
commands are in [Appendix A — Step 3](../../book/src/appendix-a-disconnected-roks-cluster.md#step-3--standalone-flp-licensing-appliance-on-the-vsi):

```bash
roksbnkctl -w flp flp init --config-file flp.yaml   # mode: vsi, floating_ip: true, the FAR auth + JWT
roksbnkctl -w flp flp up --auto                     # builds the FLP VSI + the license-proxy pod
roksbnkctl -w flp flp output                        # → flp_external_endpoint + root CA (paste into bnk.flp.external)
```

## Run it (interactive)

```bash
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./disconnected-cluster-cli-demo.sh        # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./disconnected-cluster-cli-demo.sh` prints every command without running
it — a safe first pass to read the flow.

Before recording, verify no credential can reach the screen:

```bash
../lib/check-masking.sh disconnected-cluster-cli-demo
```

`IBMCLOUD_API_KEY` and `HARBOR_ADMIN_PASSWORD` are registered with `secret` in
preflight, so `say`/`ok`/`show` mask them (and their base64 forms) as
`***REDACTED***` — on top of the explicit `--apikey ***` in the `ibmcloud login`
line. Preflight prints `✓ secret masking active …` on camera to confirm. See
[Secrets on camera](../README.md#secrets-on-camera).

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
