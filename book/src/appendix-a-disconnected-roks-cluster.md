# Appendix A — A disconnected ROKS cluster

This appendix is a manual, end-to-end **runbook** for a genuinely air-gapped BNK cluster — the
cluster has **no Internet egress at all**; only the two service VSIs (Harbor and the FLP) ever
touch the Internet, and only for the two things that intrinsically require F5: pulling the FAR
artifacts and sending F5 **TEEM** telemetry. Everything else is private, over a Transit
Gateway. It combines three chapters into one worked topology:

- [Air-gapped install](./10a-air-gapped-install.md) — mirroring BNK into a private registry
- [The F5 License Proxy](./10c-flp-licensing.md) — licensing without cluster egress
- [Sharing a Transit Gateway](./09a-transit-gateway-sharing.md) — cross-region private reach

## The topology — what reaches the Internet, and what doesn't

The key move: **run `roksbnkctl` on the Harbor VSI itself.** That host is the only one that
needs *both* Internet egress (to pull FAR) *and* private reach to Harbor — and it has both, so
Harbor is addressed by its **private IP everywhere**, with no split-horizon DNS and no separate
jumphost. The cluster is fully private.

```
                          ╔══════════════════ INTERNET ══════════════════╗
                          ║   repo.f5.com (FAR pull)     F5 TEEM (telemetry) ║
                          ╚════════▲═══════════════════════════▲════════════╝
                                   │  ONLY these two VSIs       │
        ┌──────────────────────────┼────────────────────────────┼───────────────────┐
        │ SERVICES VPC  (region A)  │   public gateway = egress   │                   │
        │                          │                             │                   │
        │   ┌───────────────────┐  │        ┌─────────────────┐  │                   │
        │   │  Harbor VSI       │──┘        │  FLP VSI         │──┘                   │
        │   │  • OCI mirror     │           │  • license proxy │  → TEEM to F5        │
        │   │  • hostname =     │           │  • pulls its own │                      │
        │   │    PRIVATE IP     │           │    image (FAR)   │                      │
        │   │  ◀ roksbnkctl RUNS│           └────────▲─────────┘                      │
        │   │    HERE (operator)│                    │  private IP                    │
        │   └─────────▲─────────┘                    │                                │
        └─────────────┼──────────────────────────────┼────────────────────────────────┘
                      │            Transit Gateway (global) — private RFC1918 only
        ┌─────────────┼──────────────────────────────┼────────────────────────────────┐
        │ CLUSTER VPC (region B)   public_gateway: FALSE  — NO worker Internet egress   │
        │             │                              │                                 │
        │   ROKS workers                                                               │
        │     • images + charts ── over TGW ─────▶ Harbor  (private IP)                 │
        │     • licensing ─────── over TGW ─────▶ FLP     (private IP)                  │
        │     • ICR / IAM / COS / master ──▶ 161.26.0.0/16 + 166.8.0.0/14              │
        │                                    (IBM Cloud private service endpoints —     │
        │                                     routable with NO public gateway)          │
        └──────────────────────────────────────────────────────────────────────────────┘
```

**Reachability summary**

| Component | Internet? | How it reaches what it needs |
|---|---|---|
| **Harbor VSI** | **Yes** — FAR pull only | public gateway (services VPC) |
| **FLP VSI** | **Yes** — F5 licensing + TEEM only | public gateway (services VPC) |
| Cluster workers → Harbor / FLP | **No** | private IPs over the Transit Gateway |
| Cluster workers → ICR / IAM / COS / master | **No** | IBM Cloud private service-endpoint CIDRs (`161.26.0.0/16`, `166.8.0.0/14`) — routable without a public gateway |
| Operator (`roksbnkctl`) | runs **on the Harbor VSI** | reaches Harbor locally + FAR/IBM API via the VSI's egress |

> **Why no operator-built VPEs are needed for the platform.** A `public_gateway: false` ROKS
> VPC cluster still reaches its IBM Cloud dependencies privately: system-image pulls use
> **private ICR** (`private.icr.io`), IAM/COS use their **private endpoints**, and the
> **master** is reached over the **private service endpoint ROKS provisions itself**. All of
> those resolve into IBM Cloud's service-endpoint ranges (`161.26.0.0/16`, `166.8.0.0/14`),
> which the VPC routes **without a public gateway**. Calico SNATs pod egress to the node, and
> the node uses those implicit routes. So the platform works private-only out of the box; you
> only add explicit **VPEs** for a *specific* service instance you want on a dedicated private
> endpoint (e.g. a particular COS bucket), not for the core ICR/IAM/COS/master path.

> **roksbnkctl does not build the services VPC, Harbor, or the FLP host image.** It provisions
> no registries and no standalone VPCs (VPC creation only happens inside the cluster phase).
> Step 1 uses the `ibmcloud` CLI for those prerequisites; everything from Step 2 on is
> `roksbnkctl`, **run on the Harbor VSI**.

## Prerequisites

- An **existing global Transit Gateway** (a *global* gateway spans regions; a local one does
  not). Set `TGW_NAME`.
- The `ibmcloud` CLI (with `vpc-infrastructure` + `tg-cli` plugins), `jq`, and an SSH client on
  your workstation. **`roksbnkctl`, `kubectl`, the FAR tarball, and the JWT go on the Harbor
  VSI**, not your workstation.
- An **RSA** VPC SSH key (IBM Cloud VPC rejects ed25519).
- Two regions: `SVC_REGION` (A, services) and `BNK_REGION` (B, cluster).

```bash
export IBMCLOUD_API_KEY=…
export TGW_NAME=my-global-tgw
export SVC_REGION=us-east SVC_ZONE=us-east-1
export BNK_REGION=us-south
ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$SVC_REGION" -g default -q
TGW_ID=$(ibmcloud tg gateways --output json | jq -r --arg n "$TGW_NAME" '.[]|select(.name==$n)|.id')
```

## Step 1 — Services VPC + Harbor (the operator host), attached to the TGW

Create the VPC, a subnet with a **public gateway**, a security group opening 22 + 443, and
attach the VPC to the TGW:

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

Launch the Harbor VSI. **Because `roksbnkctl` will run *on this VSI*, Harbor's `hostname` is
simply its own private IP** (`hostname -I`) — the naive choice, which is correct here: the
operator reaches Harbor locally, and the cluster reaches the *same* private IP over the TGW. No
floating IP appears in Harbor's identity; the floating IP is only for your SSH + the VSI's FAR
egress.

```bash
cat > harbor-init.yaml <<'CI'
#cloud-config
packages: [docker.io, docker-compose-v2, openssl]
runcmd:
  - [bash, -lc, "systemctl enable --now docker"]
  - [bash, -lc, "PRIV=$(hostname -I|awk '{print $1}'); mkdir -p /opt/harbor/certs; openssl req -x509 -nodes -days 3650 -newkey rsa:2048 -keyout /opt/harbor/certs/harbor.key -out /opt/harbor/certs/harbor.crt -subj \"/CN=$PRIV\" -addext \"subjectAltName=IP:$PRIV\""]
  - [bash, -lc, "cd /opt/harbor && curl -fsSL -o h.tgz https://github.com/goharbor/harbor/releases/download/v2.11.1/harbor-offline-installer-v2.11.1.tgz && tar xzf h.tgz --strip-components=1"]
  - [bash, -lc, "PRIV=$(hostname -I|awk '{print $1}'); sed -e \"s/^hostname:.*/hostname: $PRIV/\" -e 's#certificate:.*#certificate: /opt/harbor/certs/harbor.crt#' -e 's#private_key:.*#private_key: /opt/harbor/certs/harbor.key#' -e 's/^harbor_admin_password:.*/harbor_admin_password: Harbor12345!/' /opt/harbor/harbor.yml.tmpl > /opt/harbor/harbor.yml"]
  - [bash, -lc, "cd /opt/harbor && ./install.sh && touch /opt/harbor/READY"]
CI

VSI=$(ibmcloud is instance-create bnk-svc-harbor "$SVC_VPC" "$SVC_ZONE" bx2-4x16 "$SUBNET" \
        --image ibm-ubuntu-22-04-5-minimal-amd64-17 --keys "$RSA_KEY_NAME" \
        --user-data @harbor-init.yaml --output json)
HARBOR_PRIVATE_IP=$(echo "$VSI" | jq -r .primary_network_interface.primary_ip.address)
VNI=$(echo "$VSI" | jq -r .primary_network_attachment.virtual_network_interface.id)
FIPID=$(ibmcloud is floating-ip-reserve bnk-svc-harbor-fip --zone "$SVC_ZONE" --output json | jq -r .id)
ibmcloud is virtual-network-interface-floating-ip-add "$VNI" "$FIPID"   # for SSH + FAR egress
HARBOR_FIP=$(ibmcloud is floating-ip "$FIPID" --output json | jq -r .address)
```

### Put the operator on the VSI

The IBM Cloud stock Ubuntu image logs in as **`ubuntu`** (with `sudo`), not `root`. Once
`ssh ubuntu@$HARBOR_FIP 'ls /opt/harbor/READY'` succeeds, copy the operator toolchain onto the
VSI and install roksbnkctl's runtime dependencies — it shells out to **`terraform`**, **`helm`**
(OCI chart pulls), and **`kubectl`**, all of which the VSI pulls over its own egress:

```bash
scp roksbnkctl f5-far-auth-key.tgz subscription.jwt ubuntu@"$HARBOR_FIP":/home/ubuntu/
ssh ubuntu@"$HARBOR_FIP" '
  sudo install -m755 roksbnkctl /usr/local/bin/roksbnkctl
  # terraform (>=1.5), helm (>=3), kubectl — from the VSI'\''s egress
  curl -fsSL https://releases.hashicorp.com/terraform/1.9.8/terraform_1.9.8_linux_amd64.zip -o /tmp/tf.zip
  sudo apt-get install -y unzip && (cd /tmp && unzip -o tf.zip && sudo install -m755 terraform /usr/local/bin/)
  curl -fsSL https://get.helm.sh/helm-v3.16.3-linux-amd64.tar.gz | tar xz -C /tmp && sudo install -m755 /tmp/linux-amd64/helm /usr/local/bin/
  curl -fsSL -o /tmp/kubectl "https://dl.k8s.io/release/$(curl -sL https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && sudo install -m755 /tmp/kubectl /usr/local/bin/
  # trust Harbor'\''s own cert for host-side pulls (no DNS — it is the local private IP)
  sudo cp /opt/harbor/certs/harbor.crt ~/harbor.crt && sudo chown ubuntu ~/harbor.crt
  cat /etc/ssl/certs/ca-certificates.crt ~/harbor.crt > ~/ca-bundle.crt
'
ssh ubuntu@"$HARBOR_FIP"     # ← run Steps 2–5 from this shell, with:
#   export SSL_CERT_FILE=~/ca-bundle.crt
#   export IBMCLOUD_API_KEY=…   (the VSI needs it for cluster config + tfx COS-less flows)
```

Create the `bnk-mirror` project in Harbor (`curl -sk -u admin:… -X POST
https://$HARBOR_PRIVATE_IP/api/v2.0/projects -d '{"project_name":"bnk-mirror"}'`).

## Step 2 — Mirror FAR → Harbor (on the VSI, over the private IP)

`registry replicate` pulls from `repo.f5.com` (the VSI's egress) and pushes to Harbor at its
**private IP** (local). Give the workspace the FAR service account directly — replicate
resolves its FAR *source* credential from COS or `registry.source_service_account_b64`, not
from `bnk.far_auth_local_file`:

```bash
roksbnkctl tfx far-extract --tarball /root/f5-far-auth-key.tgz --out /root/far-sa.json
FAR_SA_B64=$(cat /root/far-sa.json)
```

```yaml
# mirror.yaml  (on the VSI)
ibmcloud: { region: us-east, resource_group: default }
prefix: bnk-mirror
tf_source: { type: embedded }
cluster: { create: false, name: none }
bnk:
  manifest_version: 2.3.0-3.2598.3-0.0.170
  far_auth_local_file: /root/f5-far-auth-key.tgz
  subscription_jwt_local_file: /root/subscription.jwt
registry:
  target: generic
  generic_host: <HARBOR_PRIVATE_IP>          # local to the VSI, private to the cluster
  generic_repo_prefix: bnk-mirror
  generic_username: admin
  generic_password_b64: <base64 of the Harbor admin password>
  source_service_account_b64: <FAR_SA_B64>
```

```console
# on the VSI:
$ roksbnkctl -w bnk-mirror init --config-file mirror.yaml
$ roksbnkctl -w bnk-mirror registry replicate    # FAR → Harbor (89 artifacts)
$ roksbnkctl -w bnk-mirror registry verify
```

## Step 3 — Standalone FLP licensing appliance (on the VSI)

Deploy the FLP as a standalone VSI into the services VPC — no cluster. It pulls its own image
from `repo.f5.com` through the services-VPC public gateway (local FAR) and sends F5 **TEEM**
telemetry; its endpoint is a **private** IP the cluster reaches over the TGW:

```yaml
# flp.yaml  (on the VSI)
ibmcloud: { region: us-east, resource_group: default }
prefix: bnk-flp
tf_source: { type: embedded }
cluster: { create: false, name: none }
bnk:
  manifest_version: 2.3.0-3.2598.3-0.0.170
  far_auth_local_file: /root/f5-far-auth-key.tgz
  subscription_jwt_local_file: /root/subscription.jwt
  license_mode: f5licenseproxy
  flp:
    mode: vsi
    vsi:
      vpc: <SVC_VPC>
      zone: us-east-1
      reach: floating
      allowed_cidrs: [10.0.0.0/8]   # the cluster's workers over the TGW (scope in prod)
```

```console
$ roksbnkctl -w bnk-flp init --config-file flp.yaml
$ roksbnkctl -w bnk-flp flp up
$ roksbnkctl -w bnk-flp flp output    # flp_external_endpoint (https://<private-ip>:8443) + flp_root_ca
```

## Step 4 — The air-gapped cluster, joined to the TGW (on the VSI)

`public_gateway: false` — **no worker egress**. The `registry:` block points at Harbor's
private IP (over the TGW), and `bnk.flp.external` at the FLP from Step 3:

```yaml
# cluster.yaml  (on the VSI)
ibmcloud: { region: us-south, resource_group: default }
prefix: bnk-dc
tf_source: { type: embedded }
cluster:
  create: true
  name: bnk-dc-roks
  openshift_version: "4.18"
  workers_per_zone: 2
  public_gateway: false           # air-gapped — no worker Internet egress
resources:
  transit_gateway: { create: false, existing: my-global-tgw }
  registry_cos:    { create: true }   # REQUIRED — ROKS-on-VPC internal registry (see below)
  cert_manager:    { create: true }
  bnk:             { create: true }
  tgw_jumphost:    { create: false }
  cluster_jumphosts: { create: false }
  client_vpc:      { create: false }
registry:
  target: generic
  generic_host: <HARBOR_PRIVATE_IP>   # over the TGW
  generic_repo_prefix: bnk-mirror
  generic_username: admin
  generic_password_b64: <base64 of the Harbor admin password>
bnk:
  manifest_version: 2.3.0-3.2598.3-0.0.170
  far_auth_local_file: /root/f5-far-auth-key.tgz
  subscription_jwt_local_file: /root/subscription.jwt
  license_mode: f5licenseproxy
  flp:
    external:
      url: <flp_external_endpoint from Step 3>
      root_ca_b64: <flp_root_ca from Step 3>
```

```console
$ roksbnkctl -w bnk-dc init --config-file cluster.yaml
$ roksbnkctl -w bnk-dc cluster up            # ~45–55 min; TGW connect runs at the end
$ roksbnkctl -w bnk-dc tgw status
```

> **⚠ Confirmed — `registry_cos: { create: true }` is mandatory.** ROKS-on-VPC refuses to
> provision without a COS instance backing its **internal** image registry (`E7278`). This is
> IBM Cloud COS reached over the private service-endpoint range — needed even air-gapped.
> `create: false` fails the cluster create outright.

### Trust Harbor's cert on the cluster nodes (ROKS-specific)

Before `bnk up`, CRI-O on each node must trust Harbor's self-signed cert or every pull fails
`x509`. **The usual OpenShift mechanism does not work on ROKS** —
`image.config.openshift.io/cluster` is HostedCluster-managed and a ValidatingAdmissionPolicy
denies edits. Instead a privileged DaemonSet drops the CA into each node's
`/etc/containers/certs.d/<HARBOR_PRIVATE_IP>/ca.crt` (CRI-O reads it per-registry; no
MachineConfig, no reboot). Because Harbor is addressed by an **IP**, the `certs.d` key is that
IP and no node `/etc/hosts` entry is needed. Apply it from the VSI:

```console
$ roksbnkctl -w bnk-dc k apply -f harbor-ca-daemonset.yaml   # full manifest in the demo script
$ kubectl get pods -n harbor-ca-trust                        # one Running per node
```

> **⚠ Confirmed — the installer's own image must be node-resident.** A no-egress cluster can't
> pull `ubi-minimal` (or any public image) for the installer pod, and it can't pull from Harbor
> yet (that's the very trust you're bootstrapping) — a chicken-and-egg. Point the DaemonSet at
> an image **already cached on every node** with `imagePullPolicy: IfNotPresent` — e.g. the
> `openshift-dns/node-resolver` image (`kubectl get ds -n openshift-dns node-resolver -o
> jsonpath='{.spec.template.spec.containers[0].image}'`), which is the OCP tools image (has
> `sh`/`cp`) and runs on every node. It then runs from cache with no pull. (With
> `public_gateway: true` you can just use a public image; this only bites the true air-gap.)

Verify a pull from Harbor's private IP over the TGW actually works before `bnk up`:

```console
$ kubectl -n harbor-ca-trust run t --image=<HARBOR_PRIVATE_IP>/bnk-mirror/images/vault-init:1.29.0-0.10.28 \
    --restart=Never --command -- sh -c 'echo ok'   # Succeeded = CA trust + TGW reach + auth all OK
```

## Step 5 — Install BNK, air-gapped (on the VSI)

`bnk up` requires a populated `registry-mirror.json` in **this** (cluster) workspace. You
replicated in the `bnk-mirror` workspace (Step 2); since both live under the same
`~/.roksbnkctl` on the VSI, that's a one-line local copy — no cross-host transfer (or replicate
directly in the `bnk-dc` workspace to skip it):

```bash
cp ~/.roksbnkctl/bnk-mirror/registry-mirror.json ~/.roksbnkctl/bnk-dc/registry-mirror.json
```

Keep `SSL_CERT_FILE` pointed at Harbor's cert (the chart pulls run host-side, i.e. on the VSI):

```console
$ export SSL_CERT_FILE=/opt/harbor/certs/harbor.crt
$ roksbnkctl -w bnk-dc bnk up
$ roksbnkctl -w bnk-dc bnk up            # run twice — converge the VLAN CRs (webhook race)
```

> **⚠ Confirmed — run `bnk up` twice.** The F5 validation webhook (`f5-validation-svc`) comes
> up *with* the stack, so the first apply can lose a race and the `external-vlan`/`internal-vlan`
> CRs fail (`http: server gave HTTP response to HTTPS client`); the License CR can report a
> transient quota error. A second pass converges both.

Verify — every pull private, nothing off the cluster to the Internet:

```console
$ kubectl get pods -n f5-bnk                     # all Running — images all from <HARBOR_PRIVATE_IP>
$ kubectl get license -n f5-utils                # STATE: Active   MODE: f5licenseproxy
$ kubectl get cneinstance -n f5-bnk -o \
    jsonpath='{.items[0].status.conditions[?(@.type=="Available")].status}'   # True
```

## Teardown

```console
$ roksbnkctl -w bnk-dc  down --auto     # cluster + BNK (+ detaches its TGW connection)
$ roksbnkctl -w bnk-flp down --auto     # the standalone FLP VSI
$ ibmcloud is instance-delete bnk-svc-harbor --force
$ ibmcloud is floating-ip-release "$FIPID" --force
$ ibmcloud tg connection-delete "$TGW_ID" <services-conn-id>
$ ibmcloud is vpc-delete "$SVC_VPC" --force   # after its subnet / public gateway / floating IP are gone
```

## The automated version

Everything above is scripted, interactively and phase-by-phase, in
`disconnected_deployment_demo.sh` (shipped alongside the project resources). It asks only for
`IBMCLOUD_API_KEY` and an ENTER between phases, and drives the exact commands in this appendix —
including the full Harbor-CA DaemonSet manifest and the convergence re-run.
