# An end to end demo using Argo Workflows and roksbnkctl for deployment use cases

This guide walks through installing **BIG-IP Next for Kubernetes (BNK)** onto IBM
Cloud ROKS clusters using **Argo Workflows** and **roksbnkctl**, covering four
deployment situations you are likely to meet in the field.

It is the pipeline-driven counterpart to the BNK Forge guide. Where Forge gives you a
form, Argo gives you a YAML file you submit — the same six blueprints, step for step.
Everything a step needs arrives as an **environment variable**: there is no
`config.yaml` anywhere in this demo, because a CI runner has no shell, no prompts and
nowhere to stage a file.

**Before you start you need**

| | |
|---|---|
| A cluster running Argo Workflows | **v4.0.8** or later, and the `argo` CLI |
| An IBM Cloud API key | able to create VPCs, clusters and Transit Gateways |
| IBM Cloud Object Storage | holding your F5 FAR auth key and subscription JWT |
| A Transit Gateway | existing, for the disconnected use cases |
| The runner image | `ghcr.io/jgruberf5/roksbnkctl-tools-runner:v1.42.0` or later |

Everything else — the services VPC, the private registry, the licence proxy, even the
Argo controller itself — is built for you by `bootstrap`.

---

## The four use cases

| # | Use case | Workflow | What it creates | Where images come from |
|---|---|---|---|---|
| **1** | **New cluster, connected** | `wf-new-cluster.yaml` | VPC, ROKS cluster, Transit Gateway | F5's registry, direct |
| **2** | **New cluster, disconnected** | `wf-new-cluster-disconnected.yaml` | VPC, ROKS cluster | your private mirror |
| **3** | **Existing cluster, connected** | `wf-existing-cluster.yaml` | nothing — adopts your cluster | F5's registry, direct |
| **4** | **Existing cluster, disconnected** | `wf-existing-disconnected.yaml` | nothing — adopts your cluster | your private mirror |

"Connected" means the cluster's worker nodes can reach the Internet, so BNK pulls
straight from F5 and licenses directly. "Disconnected" means they cannot: images come
from a private registry you have mirrored, and licensing goes through an F5 License
Proxy running inside your network.

Use cases 2 and 4 need two supporting pieces built first — a private registry and a
License Proxy. Workflows are provided for both, and **Step 3** walks through them.
Build them once and several disconnected clusters can share them.

---

## Step 1 — Build the substrate

One command builds everything except the Transit Gateway:

```bash
export IBMCLOUD_API_KEY=… TGW_NAME=bnkci-testing
./blueprint-workflows-ci-demo.sh bootstrap
```

It creates an SSH key, the services VPC and its gateway attachment, a Harbor
registry, and a VSI running k3s + Argo Workflows. It is **idempotent** — re-running
after a partial failure continues rather than duplicating.

> **Why the gateway is the exception.** It is the one genuinely shared, long-lived
> resource: the mirror, the licence proxy and every disconnected cluster attach to it.
> Attaching is cheap and reversible; owning it is not. Create it once with
> `ibmcloud tg gateway-create --name bnkci-testing --location us-south --routing global`.

Fold the generated values into `.env`, then open the tunnel it prints. The k3s API is
deliberately **not** published — only 22 and 443 are open on the services VPC.

```bash
ssh -i <key> -N -L 6443:127.0.0.1:6443 -L 2746:127.0.0.1:30746 ubuntu@<argo-fip> &
```

The second forward is the Argo web UI, at `https://localhost:2746`.

### Give the cluster VPC its own address block

`ROKSBNKCTL_CLUSTER_VPC_CIDR` must not overlap anything already on the gateway.

A transit gateway cannot route to two VPCs with overlapping prefixes — it silently
blackholes one. It does **not** surface as a routing error. It surfaces as
*intermittent image-pull timeouts from the mirror*, with every security group and ACL
in the path allowing the traffic, which sends you looking at firewalls.

The demo now refuses to submit when it detects this, naming the VPC it collides with:

```
Refusing: ROKSBNKCTL_CLUSTER_VPC_CIDR (10.242.0.0/16) overlaps a VPC already on bnkci-testing.
    • forge-app-eu-gb-1 (eu-gb): 10.242.64.0/18 10.242.128.0/18 10.242.0.0/18
```

Attached VPCs may live in **any** region — the gateway is global-routing, which is the
whole point of it.

---

## Step 2 — Submit a workflow

Everything is submitted the same way:

```bash
argo submit -n bnk-ci workflows/wf-new-cluster.yaml
```

or through the UI's **+ SUBMIT NEW WORKFLOW** button. The workflow list is the home
screen:

![The workflow list](screenshots/20-workflow-list.png)

Each row is one run: its phase, how long it took, and how many steps have finished.

> **`argo submit --wait` is not a completion signal.** Its watch is a long-lived
> connection to the API server, and this demo reaches that server through an SSH
> tunnel. When the tunnel blips, argo logs `Failed to re-establish workflow watch` and
> **returns** — so a script that trusts it reports success while the workflow is still
> Running. The demo now polls the workflow's `status.phase`, which is the only thing
> that actually answers "is it done".

---

## Step 3 — Build the registry and the licence proxy (use cases 2 and 4 only)

### 3a — Mirror F5's registry into Harbor

```bash
./blueprint-workflows-ci-demo.sh far-mirror
```

Five steps: `init` → `show-target` → `registry-bom` → `registry-replicate` →
`registry-verify`.

![The mirror workflow](screenshots/wf-bnk-far-mirror-lzdx9.png)

`registry-verify` is the one that matters — a mirror that *looks* full but is missing a
layer fails much later, as an image pull on a cluster with no route to anywhere else.
On the run captured here it mirrored **89 repositories**.

### 3b — The F5 License Proxy

```bash
./blueprint-workflows-ci-demo.sh flp-vsi
```

Three steps: `init` → `flp-up` → `handoff`. Its last step writes the `flp-handoff` Secret, holding the proxy's endpoint and CA.
The disconnected workflows `envFrom` that Secret, so **those two values never pass
through a human** — leave `ROKSBNKCTL_FLP_EXTERNAL_URL` and
`ROKSBNKCTL_FLP_ROOT_CA_B64` empty in `.env` unless the proxy was built elsewhere.

---

## Step 4 — Use case 1: a new connected cluster

```bash
./blueprint-workflows-ci-demo.sh new-cluster
```

Five steps: `init` → `cluster-up` → `bnkforge-register` → `bnk-up` → `bnk-status`.

![Use case 1](screenshots/wf-bnk-new-cluster-hbhj8.png)

This variant makes its **own** Transit Gateway and returns it on teardown, so it does
not touch the shared one. It also has no registry at all — "connected" means rendering
against `far_repo_url` directly.

> **The connected variants must blank `ROKSBNKCTL_REGISTRY_TARGET`, not just the
> `GENERIC_*` values.** `bnk-env` is shared with the mirror and disconnected
> workflows, so the registry settings reach every workflow. Blanking the six
> `GENERIC_*` values is not enough: `registryCfg()` creates the registry block the
> moment `target` is set, so `target: generic` alone materialises a mirror
> configuration with no host. `bnk up` then looks for the mirror record this
> workspace never wrote and fails with *"a registry mirror is configured for this
> workspace but this workspace has no record of it"* — **37 minutes in, after the
> cluster is already built.**

---

## Step 5 — Use case 3: adopt a connected cluster

`wf-existing-cluster.yaml` provisions nothing. It records the cluster's identity and
installs BNK onto it.

![Use case 3](screenshots/wf-bnk-existing-cluster-hjnp5.png)

### Remove BNK first

You cannot run use case 3 against a cluster that use case 1 just installed BNK onto.
The adopting workspace has no terraform state for that install, so `bnk up` refuses
immediately:

```
cluster "bnk-ci" already has BNK installed (the F5 Lifecycle Operator is running in
namespace "f5-bnk"), but workspace "bnkadopt" has no terraform state for it.
```

That refusal is correct and it is fast — before it existed, the same situation planned
a full re-install over a working one and spent about thirteen minutes failing without
naming the cause. The fix is one command:

```bash
./blueprint-workflows-ci-demo.sh bnk-down bnkconn
```

which removes BNK and **leaves the cluster, its VPC and its gateway up**, ready to be
adopted. Do not use `teardown bnkconn` for this — that also runs `cluster down` and
destroys the very cluster you are about to adopt.

---

## Step 6 — Use case 2: a new disconnected cluster

```bash
./blueprint-workflows-ci-demo.sh new-cluster-disconnected
```

![Use case 2](screenshots/wf-bnk-new-cluster-disco-9pchd.png)

This is the variant that differs from use case 1 in essentially one line —
`public_gateway: false`. The consequences are everything else: no worker egress, so
every image comes from the mirror, and licensing goes through the proxy.

It also **joins the shared Transit Gateway** rather than making its own, which is what
gives it a route to Harbor and the licence proxy in the services VPC.

### The reachability gate

Before `bnk up` plans anything it probes the mirror and the licence proxy **from every
node, in every zone**:

```
→ installing registry CA trust on all nodes (10.241.0.4) and checking reachability
  each target is retried for up to 180s before it is called unreachable (bnk.preflight.reachability_retry_seconds)
  F5-License-Proxy: 3/3 nodes reachable
    ✓ kube-…-000001f8 -> F5-License-Proxy (10.241.1.4:8443): dns=skipped-ip tcp=ok
  registry: 3/3 nodes reachable
    ✓ kube-…-000001f8 -> registry (10.241.0.4:443): dns=skipped-ip tcp=ok
✓ registry CA installed on all nodes; 10.241.0.4 is trusted and reachable
```

The retry matters here specifically. This workflow attaches its VPC to the gateway and
installs in the **same run**, and a Transit Gateway attachment is asynchronous — IBM
programs the routes some time after the connection reports `attached`. A single-shot
probe can beat route programming and fail a path that is perfectly correct. Raise
`ROKSBNKCTL_REACHABILITY_RETRY_SECONDS` if your fabric is slower than the 180s default.

---

## Step 7 — Use case 4: adopt a disconnected cluster

```bash
./blueprint-workflows-ci-demo.sh bnk-down bnkdisco      # BNK off, cluster stays
./blueprint-workflows-ci-demo.sh existing-disconnected
```

![Use case 4](screenshots/wf-bnk-existing-disco-kdl84.png)

Six steps — this variant carries the **cwc guard** as a sidecar of `bnk up`, because a
Multi-Attach deadlock on the CWC volume is what makes `bnk up` fail on a re-used
cluster.

Any step's logs are one click away, which is where you look when something stalls:

![Step logs](screenshots/wf-bnk-existing-disco-kdl84-logs.png)

---

## Confirming BNK is running

The workflow's last step runs `bnk status`, but that reports terraform's view. To see
the cluster itself:

```bash
ibmcloud ks cluster config --cluster <id> --admin
kubectl get pods -n f5-bnk
```

On the runs captured here that was **14 pods Running** — TMM, the CNE controller, DSSM
(three replicas plus three sentinels), CIS, AFM and the downloader.

For a disconnected cluster, confirm the images actually came from your mirror:

```console
$ kubectl get pods -n f5-bnk -o jsonpath='{range .items[*]}{.spec.containers[0].image}{"\n"}{end}' | sort -u
10.241.0.4/bnk-mirror/images/f5-bnk-cis:v3.0.6-0.0.5
10.241.0.4/bnk-mirror/images/f5-downloader:v0.32.11-0.0.5
10.241.0.4/bnk-mirror/images/f5-dssm-store:v5.1.49-0.0.3
```

If those still say `repo.f5.com`, the workspace never picked up the mirror — check
`ROKSBNKCTL_REGISTRY_TARGET` and that `registry adopt` ran.

---

## Removing a demo

### Order matters

```bash
./blueprint-workflows-ci-demo.sh teardown            # everything
./blueprint-workflows-ci-demo.sh teardown bnkdisco   # one workspace
```

Teardown walks `bnk down` → `tgw disconnect` → `cluster down` per workspace, and that
order is load-bearing: `cluster down` refuses while a gateway connection exists,
because the connection pins the VPC's CRN and the VPC delete would fail.
`tgw disconnect` removes **only this cluster's** connection — the shared gateway and
everyone else's attachments stay.

**Adopted clusters are never destroyed.** The `existing-*` workflows registered them;
they did not create them.

### Removing BNK but keeping the cluster

```bash
./blueprint-workflows-ci-demo.sh bnk-down <workspace>
```

This is what you want between a build variant and its reuse variant. Do **not** use
`teardown` for it — that also runs `cluster down`.

---

## If something goes wrong

**`a registry mirror is configured for this workspace but this workspace has no record of it`**
A connected workflow inherited registry settings. `bnk-env` is shared by every
workflow, so the connected variants must blank `ROKSBNKCTL_REGISTRY_TARGET` **as well
as** the six `GENERIC_*` values — `target` alone materialises a registry block.

**`cluster "…" already has BNK installed … but workspace "…" has no terraform state for it`**
You are adopting a cluster that still has BNK on it from another workspace. Run
`bnk-down <the workspace that installed it>` first. The refusal is immediate; before
that guard existed this planned a full re-install and spent ~13 minutes failing.

**`Refusing: ROKSBNKCTL_CLUSTER_VPC_CIDR (…) overlaps a VPC already on …`**
Pick a block nothing else on the gateway uses. The message names the VPC and its
prefixes. Note this only applies to runs that **create** a cluster VPC — adopting a
cluster that is already attached is not a collision.

**`Error acquiring the state lock`**
An interrupted run left a lock on the PVC. `./blueprint-workflows-ci-demo.sh unlock <workspace>`.

**`Permission denied (publickey)` from the bootstrap**
If the repo is on a Windows drive under WSL, DrvFs cannot hold mode `0600`, so ssh
refuses the key. The bootstrap now stages a copy on a Linux filesystem automatically;
if you are driving ssh yourself, `install -m600 <key> /tmp/key` first.

**A workflow "finished" but nothing happened**
Do not trust `argo submit --wait`. Its watch is a long-lived connection, and through an
SSH tunnel a blip makes it return early. Check `kubectl get wf -n bnk-ci` for the real
phase.

---

## All four, verified end to end

Every use case below was run on **roksbnkctl v1.42.0** against IBM Cloud ROKS
**4.20.32**, and confirmed by checking BNK pods on the cluster — not just by the
workflow's exit status.

![All four](screenshots/50-all-four-succeeded.png)

| # | Use case | Workflow run | Result |
|---|---|---|---|
| 1 | New cluster, connected | `bnk-new-cluster-hbhj8` | Succeeded — 11 pods Running |
| 2 | New cluster, disconnected | `bnk-new-cluster-disco-9pchd` | Succeeded — 14 pods, all images from the mirror |
| 3 | Existing cluster, connected | `bnk-existing-cluster-hjnp5` | Succeeded — 14 pods Running |
| 4 | Existing cluster, disconnected | `bnk-existing-disco-kdl84` | Succeeded — 14 pods, all images from the mirror |
| — | FAR mirror | `bnk-far-mirror-lzdx9` | Succeeded — 89 repositories |
| — | F5 License Proxy | `bnk-flp-vsi-z6qx2` | Succeeded — `flp-handoff` published |

The two `Failed` rows in that screenshot are real and deliberately left in place: they
are the runs that exposed the registry-target and adopt-ordering bugs described above.
Both are fixed in the workflows shipped here.

## Refreshing the screenshots

`capture.js` produces every image in `screenshots/`:

```bash
NODE_PATH=$(npm root -g) SHOT_SECRETS="$IBMCLOUD_API_KEY" \
  node capture.js https://localhost:2746 screenshots bnk-ci list:20-workflow-list wf:<name>
```

It dismisses Argo's first-run survey modal and the transient event-stream toast, and
scrubs registered secrets — plus anything matching a credential pattern — out of the
DOM before each shutter. These runs carry a live API key; do not capture with a tool
that does not do this.

