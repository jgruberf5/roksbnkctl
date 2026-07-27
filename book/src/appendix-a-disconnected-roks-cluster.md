# Appendix A — A disconnected ROKS cluster

This appendix is a manual, end-to-end **runbook** for deploying a genuinely disconnected
BNK cluster — the same flow the `disconnected_deployment_demo.sh` script automates, done
by hand so you can adapt each step. It combines three chapters into one worked topology:

- [Air-gapped install](./10a-air-gapped-install.md) — mirroring BNK into a private registry
- [The F5 License Proxy](./10c-flp-licensing.md) — licensing without cluster egress
- [Sharing a Transit Gateway](./09a-transit-gateway-sharing.md) — cross-region private reach

## The topology

```
  Region A — SERVICES VPC  (has a public gateway  →  the ONLY egress points)
      • Harbor OCI registry            (a VSI; the BNK image/chart mirror)
      • F5 License Proxy               (a standalone VSI; the one path to F5)
                     │
                Transit Gateway  (global, existing)
                     │
  Region B — CLUSTER VPC  (public_gateway: false  →  NO worker Internet egress)
      • private ROKS cluster
      • BNK — installed from Harbor, licensed via the FLP, all over the TGW
```

Everything that must reach the Internet lives in the **services VPC**: Harbor (only during
the one-time mirror) and the FLP (for licensing). The **cluster** never reaches the
Internet — it pulls every image and chart from Harbor and licenses through the FLP, both
privately over the Transit Gateway. That is what "disconnected" means here: no external
pulls, and — with `cluster.public_gateway: false` — no egress path at all.

> **roksbnkctl does not build the services VPC or Harbor.** It provisions no registries and
> no standalone VPCs (VPC creation only happens inside the cluster phase). Steps 1 uses the
> `ibmcloud` CLI for that prerequisite; everything from Step 2 on is roksbnkctl.

## Prerequisites

- An **existing global Transit Gateway** (a *global* gateway spans regions; a local one
  does not). We're assuming you're out of TGW quota and must reuse one — set `TGW_NAME`.
- `roksbnkctl`, the `ibmcloud` CLI with the `vpc-infrastructure` and `tg-cli` plugins, and `jq`.
- An **existing IBM Cloud VPC SSH key** (for the Harbor VSI).
- Your **FAR auth tarball** and **subscription JWT** as local files (no orchestration COS
  needed — see [Local files instead of COS](./25-cos-supply-chain.md)).
- Two regions: `SVC_REGION` (A, services) and `BNK_REGION` (B, cluster).

```bash
export IBMCLOUD_API_KEY=…            # or resolved from a .env / keychain
export TGW_NAME=my-global-tgw
export SVC_REGION=us-south SVC_ZONE=us-south-1
export BNK_REGION=eu-de
ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$SVC_REGION" -g default -q
TGW_ID=$(ibmcloud tg gateways --output json | jq -r --arg n "$TGW_NAME" '.[]|select(.name==$n)|.id')
```

## Step 1 — Services VPC + Harbor, attached to the TGW (`ibmcloud` CLI)

Create the VPC, a subnet with a **public gateway** (Harbor needs egress to install and to
receive the mirror), a security group opening 22 + 443, and attach the VPC to the existing
Transit Gateway:

```bash
SVC_VPC=$(ibmcloud is vpc-create bnk-svc-vpc --output json | jq -r .id)
SVC_CRN=$(ibmcloud is vpc bnk-svc-vpc --output json | jq -r .crn)
SUBNET=$(ibmcloud is subnet-create bnk-svc-subnet "$SVC_VPC" --zone "$SVC_ZONE" \
           --ipv4-address-count 256 --output json | jq -r .id)
PGW=$(ibmcloud is public-gateway-create bnk-svc-pgw "$SVC_VPC" "$SVC_ZONE" --output json | jq -r .id)
ibmcloud is subnet-update "$SUBNET" --public-gateway-id "$PGW"
SG=$(ibmcloud is vpc "$SVC_VPC" --output json | jq -r .default_security_group.id)
for p in 22 443; do ibmcloud is security-group-rule-add "$SG" inbound tcp --port-min $p --port-max $p; done
ibmcloud tg connection-create "$TGW_ID" --name bnk-svc-conn --network-type vpc --network-id "$SVC_CRN"
```

Launch a Harbor VSI. A minimal `cloud-init` installs Docker + the Harbor offline installer
with a self-signed cert on the VSI's IP (adapt the version/creds to your standards):

```bash
cat > harbor-init.yaml <<'CI'
#cloud-config
packages: [docker.io, docker-compose-v2, openssl]
runcmd:
  - [bash, -lc, "systemctl enable --now docker"]
  - [bash, -lc, "IP=$(hostname -I|awk '{print $1}'); mkdir -p /opt/harbor/certs; openssl req -x509 -nodes -days 3650 -newkey rsa:2048 -keyout /opt/harbor/certs/harbor.key -out /opt/harbor/certs/harbor.crt -subj \"/CN=$IP\" -addext \"subjectAltName=IP:$IP\""]
  - [bash, -lc, "cd /opt/harbor && curl -fsSL -o h.tgz https://github.com/goharbor/harbor/releases/download/v2.11.1/harbor-offline-installer-v2.11.1.tgz && tar xzf h.tgz --strip-components=1"]
  - [bash, -lc, "IP=$(hostname -I|awk '{print $1}'); sed -e \"s/^hostname:.*/hostname: $IP/\" -e \"s#certificate:.*#certificate: /opt/harbor/certs/harbor.crt#\" -e \"s#private_key:.*#private_key: /opt/harbor/certs/harbor.key#\" -e \"s/^harbor_admin_password:.*/harbor_admin_password: Harbor12345!/\" /opt/harbor/harbor.yml.tmpl > /opt/harbor/harbor.yml"]
  - [bash, -lc, "cd /opt/harbor && ./install.sh && touch /opt/harbor/READY"]
CI

VSI=$(ibmcloud is instance-create bnk-svc-harbor "$SVC_VPC" "$SVC_ZONE" bx2-4x16 "$SUBNET" \
        --image ibm-ubuntu-22-04-5-minimal-amd64-3 --keys "$SSH_KEY_NAME" \
        --user-data @harbor-init.yaml --output json)
NIC=$(echo "$VSI" | jq -r .primary_network_interface.id)
HARBOR_PRIVATE_IP=$(echo "$VSI" | jq -r .primary_network_interface.primary_ip.address)   # reached over the TGW
HARBOR_FIP=$(ibmcloud is floating-ip-reserve bnk-svc-harbor-fip --nic "$NIC" --output json | jq -r .address)
```

Wait for Harbor (`ssh root@$HARBOR_FIP 'ls /opt/harbor/READY'`), create the `bnk-mirror`
project in its UI (`https://$HARBOR_FIP`, admin / your password), and **trust its cert on
the host** you'll run `registry replicate` from:

```bash
ssh root@$HARBOR_FIP 'cat /opt/harbor/certs/harbor.crt' | sudo tee /usr/local/share/ca-certificates/harbor.crt >/dev/null
sudo update-ca-certificates
```

## Step 2 — Mirror FAR → Harbor (standalone, no cluster)

`registry replicate` is a host-side, registry-to-registry copy — it needs **no cluster**
(see [Air-gapped install](./10a-air-gapped-install.md)). Seed Harbor now with a minimal
registry-only workspace:

```yaml
# mirror.yaml
ibmcloud: { region: us-south, resource_group: default }
prefix: bnk-mirror
tf_source: { type: embedded }
cluster: { create: false, name: none }
bnk:
  manifest_version: 2.3.0-3.2598.3-0.0.170
  far_auth_local_file: /path/to/f5-far-auth-key.tgz
  subscription_jwt_local_file: /path/to/subscription.jwt
registry:
  target: generic
  generic_host: <HARBOR_FIP>        # host reach over the floating IP
  generic_repo_prefix: bnk-mirror
  generic_username: admin
  generic_password_b64: <base64 of the Harbor admin password>
```

```console
$ roksbnkctl -w bnk-mirror init --config-file mirror.yaml
$ roksbnkctl -w bnk-mirror registry bom          # what will be copied
$ roksbnkctl -w bnk-mirror registry replicate    # FAR → Harbor
$ roksbnkctl -w bnk-mirror registry verify       # every artifact present + digest-matched
```

## Step 3 — Standalone FLP licensing appliance in the services VPC

Deploy the F5 License Proxy as a standalone VSI into the **services VPC** — no cluster — with
`bnk.flp.vsi.vpc`. `reach: floating` gives it the one controlled egress path to F5. This is
the licensing appliance the disconnected cluster will use:

```yaml
# flp.yaml
ibmcloud: { region: us-south, resource_group: default }
prefix: bnk-flp
tf_source: { type: embedded }
cluster: { create: false, name: none }
bnk:
  manifest_version: 2.3.0-3.2598.3-0.0.170
  far_auth_local_file: /path/to/f5-far-auth-key.tgz
  subscription_jwt_local_file: /path/to/subscription.jwt
  license_mode: f5licenseproxy
  flp:
    mode: vsi
    vsi:
      vpc: <SVC_VPC>               # standalone FLP into the services VPC (no cluster)
      zone: us-south-1
      reach: floating             # floating IP → controlled egress to F5
      allowed_cidrs: [10.0.0.0/8] # allow the cluster's workers over the TGW (scope to your worker subnets in prod)
```

```console
$ roksbnkctl -w bnk-flp init --config-file flp.yaml
$ roksbnkctl -w bnk-flp flp up
$ roksbnkctl -w bnk-flp flp output flp_external_endpoint   # → the cluster's bnk.flp.external.url
$ roksbnkctl -w bnk-flp flp output flp_root_ca             # → the cluster's bnk.flp.external.root_ca_b64
```

## Step 4 — The private cluster, joined to the TGW

Now the disconnected cluster in region B. `cluster.public_gateway: false` gives it **no
worker egress**; the `registry:` block points at Harbor's **private** IP (reached over the
TGW), and `bnk.flp.external` points at the FLP from Step 3:

```yaml
# cluster.yaml
ibmcloud: { region: eu-de, resource_group: default }
prefix: bnk-dc
tf_source: { type: embedded }
cluster:
  create: true
  name: bnk-dc-roks
  openshift_version: "4.18"
  workers_per_zone: 2
  public_gateway: false           # PRIVATE — no worker Internet egress
resources:
  transit_gateway: { create: false, existing: my-global-tgw }
  registry_cos:    { create: false }
  cert_manager:    { create: true }
  bnk:             { create: true }
  tgw_jumphost:    { create: false }
  cluster_jumphosts: { create: false }
  client_vpc:      { create: false }
registry:
  target: generic
  generic_host: <HARBOR_PRIVATE_IP>   # reached over the TGW
  generic_repo_prefix: bnk-mirror
  generic_username: admin
  generic_password_b64: <base64 of the Harbor admin password>
bnk:
  manifest_version: 2.3.0-3.2598.3-0.0.170
  far_auth_local_file: /path/to/f5-far-auth-key.tgz
  subscription_jwt_local_file: /path/to/subscription.jwt
  license_mode: f5licenseproxy
  flp:
    external:
      url: <flp_external_endpoint from Step 3>
      root_ca_b64: <flp_root_ca from Step 3>
```

```console
$ roksbnkctl -w bnk-dc init --config-file cluster.yaml
$ roksbnkctl -w bnk-dc cluster up            # ~30–50 min (ROKS create)
$ roksbnkctl -w bnk-dc tgw connect my-global-tgw   # cluster VPC → same global TGW
$ roksbnkctl -w bnk-dc tgw status
```

> **`public_gateway: false` is an expert topology.** A no-egress cluster can only reach
> images and IBM Cloud services over **private** paths that roksbnkctl does **not** build.
> Before this works end-to-end you must have provided **VPEs / private service endpoints**
> for the IBM services the cluster uses, in addition to the private mirror + FLP reach over
> the TGW. If you have not built those, use `public_gateway: true` instead — you still get a
> **disconnected install** (all pulls from Harbor, licensing via the FLP), just not a
> no-egress VPC. Note the cluster **master** keeps its public API endpoint regardless; this
> toggle governs worker/subnet egress. See
> [Chapter 10a §"A truly disconnected cluster"](./10a-air-gapped-install.md).

## Step 5 — Install BNK, disconnected

`bnk up` reads the mirror record and `bnk.flp.external`, so every chart and image resolves
from Harbor and licensing goes through the FLP — no `repo.f5.com`, `quay.io`, `docker.io`,
or direct F5 calls:

```console
$ roksbnkctl -w bnk-dc bnk up
$ roksbnkctl -w bnk-dc k get pods -n f5-bnk    # all Running, no external pulls
```

## Teardown

```console
$ roksbnkctl -w bnk-dc  down --auto     # cluster + BNK (+ detaches its TGW connection)
$ roksbnkctl -w bnk-flp down --auto     # the standalone FLP VSI
$ ibmcloud is instance-delete bnk-svc-harbor --force
$ ibmcloud tg connection-delete "$TGW_ID" <services-conn-id>
$ ibmcloud is vpc-delete "$SVC_VPC" --force   # after its subnet / public gateway / floating IP are gone
```

## The automated version

Everything above is scripted, interactively and phase-by-phase, in
`disconnected_deployment_demo.sh` (shipped alongside the project resources). It asks only
for `IBMCLOUD_API_KEY` and an ENTER between phases, and drives the exact commands in this
appendix.
