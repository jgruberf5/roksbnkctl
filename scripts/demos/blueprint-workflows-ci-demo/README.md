# Blueprint workflows CI demo

The six **BNK Forge blueprints**, expressed as **Argo Workflows** and driven entirely
by **environment variables**. There is no `config.yaml` anywhere in this demo — every
setting arrives as an env var, because that is the only shape available to a BNK Forge
container module or an argv-only CI runner: no shell, no prompts, nowhere to stage a
file.

Each workflow's step sequence mirrors the corresponding module in
[`roksbnkctl-bnk-forge`](https://github.com/jgruberf5/roksbnkctl-bnk-forge)
(`roksbnkctl/*/bnkforge.artifact.json`), so what runs here is what runs in a Forge
deployment.

## The six workflows

| # | Workflow | Blueprint | What it does |
|---|---|---|---|
| 1 | `wf-far-mirror.yaml` | `far-mirror`, `harbor-registry` | Replicate F5's artifact registry into a private OCI registry, and verify it. **No cluster.** |
| 2 | `wf-flp-vsi.yaml` | `flp-vsi` | Build a standalone F5 License Proxy on a VSI. **No cluster.** Publishes its endpoint + CA as the `flp-handoff` Secret. |
| 3 | `wf-new-cluster.yaml` | `roks-new-cluster` | New VPC + **connected** ROKS cluster + transit gateway, then install BNK. No mirror, no proxy. |
| 4 | `wf-new-cluster-disconnected.yaml` | `roks-new-cluster-disconnected` | The same, **disconnected**: no worker egress, artifacts from the mirror, licensing through the proxy. |
| 5 | `wf-existing-cluster.yaml` | `roks-existing-cluster` | **Adopt** a running connected cluster and install BNK. Provisions nothing. Carries the cwc sidecar. |
| 6 | `wf-existing-disconnected.yaml` | `roks-disconnected` | **Adopt** a running disconnected cluster and install BNK from the mirror, licensed by the proxy. Carries the cwc sidecar. |

Workflows 1 and 2 are prerequisites of 4 and 6. Nothing else depends on anything.

## Everything is an environment variable

The demo splits your `.env` in two and attaches both to every step with `envFrom`:

| Carrier | Holds | Why |
|---|---|---|
| `bnk-env` **ConfigMap** | every non-secret `ROKSBNKCTL_*` / `BNKFORGE_*` / `CWC_*` setting | So the whole workspace shape is readable in the Argo UI and via `kubectl get cm bnk-env -o yaml`. That visibility is the demonstration. |
| `bnk-secrets` **Secret** | `IBMCLOUD_API_KEY`, `ROKSBNKCTL_GENERIC_PASSWORD`, `ROKSBNKCTL_BIGIP_PASSWORD`, `BNK_FORGE_PASSWORD` | Never rendered to a file that persists, never logged, never printed by a step. |

`init --non-interactive --override-from-env` then builds the workspace from that
environment. Settings that *define* a workflow — `ROKSBNKCTL_CLUSTER_CREATE`,
`ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY`, `ROKSBNKCTL_LICENSE_MODE` — are pinned in the
workflow YAML rather than the ConfigMap, so a reader sees them next to the steps they
govern. `wf-new-cluster.yaml` and `wf-new-cluster-disconnected.yaml` differ in
essentially one line.

## Prerequisites

### From a clean slate — one command

The only thing this demo will **not** create is the global **Transit Gateway**. Everything
else — an RSA VPC SSH key, the services VPC, its gateway attachment, Harbor, and the
k3s + Argo Workflows controller — is built for you:

```bash
export IBMCLOUD_API_KEY=… TGW_NAME=bnkci-testing
./blueprint-workflows-ci-demo.sh bootstrap
```

It is **idempotent**, so re-running after a partial failure continues rather than
duplicating, and it writes what it made to `../.bootstrap-state/services.env` and
`argo.env` (both gitignored — they hold a generated private key and Harbor's password).
Fold those values into your `.env`, open the SSH tunnel it prints, and carry on.

If the gateway itself does not exist:

```bash
ibmcloud tg gateway-create --name bnkci-testing --location us-east --routing global
```

> **Why the gateway is the exception.** It is the one resource that is genuinely shared
> and long-lived — the mirror, the licence proxy and every disconnected cluster attach to
> it, and deleting it would break things this demo never created. Attaching to a gateway
> is cheap and reversible; owning one is not.

### Bringing your own

An operator who already has Harbor and an Argo controller sets the values in `.env` and
never runs `bootstrap`. You then need:

**A cluster running Argo Workflows**, reachable via `KUBECONFIG`, plus the `argo` CLI.

**The runner image** — `RUNNER_TAG` must be **≥ v1.36.0** (for `registry adopt`, which
workflows 4 and 6 depend on) and **≥ v1.37.0** if you want `bnkforge unregister` on
teardown. The `.env.example` default is v1.41.0.

**Per workflow:**

| Workflow | Also needs |
|---|---|
| 1 `far-mirror` | A reachable OCI registry with push credentials, and its **CA** (`ROKSBNKCTL_GENERIC_CA_B64`) taken from the file that generated it — roksbnkctl refuses to adopt a self-signed CA it merely discovered over the wire. |
| 2 `flp-vsi` | A services VPC + zone + an IBM Cloud VPC SSH key. |
| 3, 4 | The FAR supply chain in COS, and the per-zone dataplane addressing. |
| 4 | `ROKSBNKCTL_TRANSIT_GATEWAY_NAME` — the **existing** gateway the mirror and FLP sit on. A disconnected cluster on a brand-new gateway is isolated from the only registry it can install from. The connected variant blanks this and creates its own. |
| 4 | `ROKSBNKCTL_CLUSTER_VPC_CIDR` — this workflow **creates** a VPC *and* joins a **shared** gateway, the one combination that can overlap. IBM's default `auto` prefixes are identical for every VPC in a region, so a second run collides with the first; the gateway does not report that, it silently blackholes one VPC and you see intermittent image-pull timeouts from the mirror. Give each cluster its own `/16`. Blank is fine for a single cluster. (`cluster up` refuses a detectable overlap up front since `v1.39.0`.) |
| 5, 6 | A **running** cluster already attached to a transit gateway. |
| 4, 6 | Workflows 1 and 2 to have run. |
| **all** | `ROKSBNKCTL_COS_BUCKET` — **required, no default.** The bucket is account-suffixed (`bnk-artifacts-<account>`), so roksbnkctl cannot guess it, and `bnk up` cannot reach the entitlement without it. The driver refuses to submit anything until it is set: a blank here failed a blueprint run *forty minutes in*, with nothing surfacing the omission. |

### The Harbor VSI is not built here

Use case 1 in the blueprints pairs a Harbor **VSI** (a terraform module) with the
mirror. This demo covers the **mirror half only** — building a registry and filling one
are different jobs with different lifetimes, and the registry usually long outlives any
single pipeline. Stand Harbor up first with the cut-and-paste in the
[disconnected-cluster CLI demo README](../disconnected-cluster-cli-demo/README.md#building-the-services-infrastructure-harbor-vsi--flp-vsi),
then point `ROKSBNKCTL_GENERIC_HOST` at it.

## Run it

```bash
cp .env.example .env && $EDITOR .env
set -a; source .env; set +a

./blueprint-workflows-ci-demo.sh setup        # substrate + env carriers, submit nothing
./blueprint-workflows-ci-demo.sh              # setup + far-mirror (the default)
./blueprint-workflows-ci-demo.sh far-mirror flp-vsi
./blueprint-workflows-ci-demo.sh all          # every workflow, in dependency order
```

Nothing runs unless you name it — the cluster workflows cost real quota and 45–90
minutes each. `DRY_RUN=1` prints every command without running any of it.

Submitting by hand works too, once `setup` has run:

```bash
argo submit -n bnk-ci --wait --log workflows/wf-far-mirror.yaml
argo submit -n bnk-ci --wait workflows/wf-existing-cluster.yaml -p bnkforge=false -p cwc-guard=false
```

> The workflow YAML carries `PLACEHOLDER_RUNNER_IMAGE`; the demo renders it from
> `RUNNER_IMAGE` before submitting. Substitute it yourself if you submit by hand.

### Workflow parameters the driver sets for you

Both of these exist because an **optional** component that was never configured
should not fail a run whose real output is fine. The driver decides from your
`.env`; pass them yourself when submitting by hand.

| Parameter | Default | The driver sets it from |
|---|---|---|
| `status-check` (`flp-vsi`) | `false` | `ROKSBNKCTL_FLP_VSI_STATUS_IMAGE`. `flp-status` is a separate image you build and push; the licence proxy works without it. Left ungated, an unbuilt add-on failed the run **and took `publish-handoff` with it** — and that Secret is the entire output workflows 4 and 6 consume. |
| `bnkforge` (cluster workflows) | `true` | `BNK_FORGE_URL`. The blueprints always register, so the YAML default matches them; a bare CI run usually has no Forge, and registering against an empty URL fails **after** the cluster build — an hour in, for a bookkeeping step. |

## Why the PVC matters

`bnk-work` is a real PersistentVolumeClaim, not an `emptyDir`. roksbnkctl keeps its
config, terraform state and the recorded mirror record under `/work/.roksbnkctl`, and
**every teardown reads that state** — `cluster down` destroys from terraform state, and
`bnk down` needs the mirror record. An `emptyDir` orphans the IAM trusted profile and
leaves terraform with nothing to destroy from.

It is also how the workflows hand off: `wf-far-mirror` writes the mirror record that
`wf-*-disconnected` later adopts.

### One workspace per cluster, and the mirror is not shared

The PVC is shared; the **roksbnkctl workspace inside it is not**. Each workflow names
its own, and the split is load-bearing rather than cosmetic:

| Workspace | Used by | Why it is separate |
|---|---|---|
| `bnk` | `wf-far-mirror` | owns `registry-mirror.json`, the record of what was replicated |
| `bnkconn` | the two **connected** workflows | must have **no** registry at all |
| `bnkdisco` | the two **disconnected** workflows | `registry adopt` writes its own record here |
| `flp` | `wf-flp-vsi` | separate lifecycle from any cluster |

Two independent reasons, both learned the hard way:

**Terraform state is per workspace, and a workspace can only describe one cluster.**
Pointing the disconnected pair at the same workspace as the connected pair would have
terraform plan the *existing* cluster out of existence when the prefix changed.

**A mirror record is inherited by anything sharing the workspace.** `tf/vars.go` reads
`registry-mirror.json` for the current workspace and rewrites every image reference to
the mirror. When the connected workflows shared `bnk` with `wf-far-mirror`, `bnk up`
rendered cert-manager as `10.241.0.4/bnk-mirror/jetstack/...` — a private address on a
gateway that cluster is not attached to. Nothing said "wrong registry"; the pods sat in
`ImagePullBackOff` until `bnk up` hit its 600-second deadline and reported
`context deadline exceeded`.

The connected workflows also **blank `ROKSBNKCTL_GENERIC_*` in their own `env:`**, because
`bnk-env` is shared and carries the mirror host for everyone. `--override-from-env` skips
empty values, so on a fresh workspace blank means "never set a registry" — which is what
connected means.

### …which is why you run one workflow at a time

That shared PVC holds **one terraform state**, and `bnk-env` is **one ConfigMap**. Two
workflows running at once fight over both:

- **Terraform state.** Both hold the same state file. The loser dies on the state lock —
  observed here as `bnk-up` failing 11 seconds in while the other run proceeded normally.
- **`bnk-env`.** The driver re-renders it on every invocation. A workflow that has already
  started but has steps still pending will have those steps pick up the *new* values,
  because `envFrom` is resolved per pod at creation. Change `ROKSBNKCTL_PREFIX` or
  `ROKSBNKCTL_CLUSTER_NAME` for a second run and the first run's remaining steps quietly
  target the second run's cluster.

So a connected pair and a disconnected pair cannot be run in parallel to save wall-clock:
finish one, then switch the environment and start the next. If you genuinely need
concurrency, give each stream its own namespace, PVC and ConfigMap — the sharing is what
makes the handoff work, and it is also what makes concurrency unsafe.

## Only one cluster VPC per Transit Gateway

**roksbnkctl gives every cluster VPC it creates the same address prefixes** —
`10.241.0.0/18`, `10.241.64.0/18`, `10.241.128.0/18`. Two of them attached to one
Transit Gateway overlap, and the gateway cannot route to both: traffic is ambiguous
and silently blackholed.

It does **not** present as a routing error. It presents as intermittent image pulls:

```
Failed to pull image "10.243.0.4/bnk-mirror/…":
  dial tcp 10.243.0.4:443: connect: connection timed out
```

Some pulls succeed, some time out, and every security group and network ACL in the
path allows the traffic — which sends you looking at firewalls. It cost an hour on the
blueprint side before anyone checked CIDRs.

**Rule: only one roksbnkctl-created cluster VPC may be attached to a shared gateway at
a time.** Detaching is enough; the cluster need not be destroyed:

```bash
roksbnkctl -w bnk tgw disconnect --auto
```

Which workflows are affected:

| Workflow | Gateway | Risk |
|---|---|---|
| `wf-new-cluster` | creates its **own** | none — it never shares |
| `wf-new-cluster-disconnected` | **adopts** the shared one | creates a cluster VPC on it |
| `wf-existing-*` | adopts whatever the cluster is already on | only if that is a *second* cluster |

The driver refuses the deterministic case — building a disconnected cluster **and**
adopting a different one on the same gateway in a single invocation — and prints the
gateway's current VPC attachments when the `ibmcloud tg` plugin is available locally.
The runner image has no `tg-cli` plugin, so that check runs from your host, not in the
cluster.

**Sequencing `all`.** The two disconnected variants must share the gateway (that is how
they reach the mirror), so they can never be attached at once. Run the reuse variants
against the cluster the build variants just made — set `ROKSBNKCTL_CLUSTER_NAME` to
`ROKSBNKCTL_PREFIX` — and remove BNK between them with `roksbnkctl bnk down`, **not** by
destroying the module, which cascades into the cluster and costs a 40-minute rebuild.

## The cwc guard is a sidecar, and only on the reuse workflows

`wf-existing-cluster` and `wf-existing-disconnected` run `bnk up` with a **sidecar**
that watches `f5-spk-cwc`. It is a sidecar rather than a following step for a reason
worth stating: **the deadlock is what makes `bnk up` fail**. The F5 Lifecycle Operator
rolls the cwc part-way through the install; the Deployment is single-replica, mounts a
ReadWriteOnce PVC and ships `strategy: RollingUpdate`, so `maxSurge` rounds up to 1 and
the replacement pod is scheduled before the incumbent is terminated. Landing on another
node it cannot attach the volume, the rollout wedges, the License CR never reaches
Active, and `bnk up`'s licence gate times out after 15 minutes. A remedy placed *after*
`bnk up` would never be reached.

The sidecar forces `strategy: Recreate` and clears an existing deadlock by cycling
replicas — patching the strategy alone does not release a volume that is already
attached. It exits `0` unconditionally so it can never fail the step, and Argo kills it
when `bnk up` returns.

**Only the reuse workflows carry it.** A freshly built cluster has no prior volume
attachments for the replacement pod to collide with, so `wf-new-cluster*.yaml`
deliberately do not. Skip it with `-p cwc-guard=false` once F5 ships the Deployment with
`strategy: Recreate` — the fix belongs in the chart, not here.

## Two clusters are in play

Easy to conflate, so worth stating plainly:

- **`roksbnkctl kubectl …`** targets the cluster the *workspace* manages — the ROKS
  cluster being built or adopted. This is what the cwc guard uses.
- **plain `kubectl …`** targets the cluster *Argo itself runs on*, via the pod's
  ServiceAccount. This is what the FLP handoff step uses to write `flp-handoff`.

## Record it

```bash
./record.sh          # → demo-video/blueprint-workflows-ci-demo.mp4
```

Runs hands-off in a headless X terminal; `../lib/post_10x.py` then builds the cut —
cleared-screen phase banners, each `roksbnkctl` command held 5s, its settled output
held 5s, and the long windows at 10×. No voiceover. Before recording:

```bash
../lib/check-masking.sh blueprint-workflows-ci-demo
```

## Files

| File | What |
|---|---|
| `blueprint-workflows-ci-demo.sh` | the driver — renders the env carriers, submits the workflows you name |
| `.env.example` | every setting, grouped by which workflows read it |
| `workflows/00-prereqs.yaml` | namespace, workspace PVC, ServiceAccount + executor RBAC |
| `workflows/01-flp-handoff-rbac.yaml` | the one extra grant `wf-flp-vsi` needs, pinned to the `flp-handoff` Secret |
| `workflows/wf-*.yaml` | the six workflows |
| `record.sh` | wrapper around the shared recorder |

## Teardown

```bash
./blueprint-workflows-ci-demo.sh teardown
```

Runs `bnk down`, `cluster down` and `flp down` from the workspace on the PVC. A cluster
that was **adopted** (workflows 5 and 6) is left running — those registered it, so
roksbnkctl does not own it. The namespace and PVC survive so a re-run resumes; remove
them with `kubectl delete ns bnk-ci`.
