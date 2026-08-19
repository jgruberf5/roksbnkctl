# Deploying BNK trials on top

`roksbnkctl up` deploys a **BNK trial** — F5's Lifecycle Operator, the CNE Instance, license bundles, and the cluster-side glue that makes them work — onto a ROKS cluster that already exists. "Already exists" means either provisioned by [`cluster up`](./08-cluster-phase.md) or [registered](./09-registering-existing-cluster.md) from a pre-existing cluster.

For workspaces where the cluster and the trial are managed as separate phases (the default — see [Chapter 8](./08-cluster-phase.md)), the trial layer also gets its own command pair: `roksbnkctl bnk up` / `bnk down`. `bnk down` tears down only the trial; the cluster keeps running, so the next iteration starts in 5-10 minutes instead of an hour. The `bnk` group is documented in [§"The `bnk up` / `bnk down` command group"](#the-bnk-up--bnk-down-command-group) below.

This chapter is the deeper-than-quick-start view of `up`: what each module does, the ~77-resource shape of a clean apply, the token-rotation observation when you re-run `up` against an existing cluster, how to read the Terraform plan output, and how the `bnk` group + the shape-aware composite `up` / `down` fit together.

[Chapter 7 — Quick start](./07-quick-start.md) shows the happy path end-to-end with sample output. This chapter goes deeper.

## What "deploying BNK" means

A BNK trial is a deliberately small set of Kubernetes resources that share state with a cluster-shared cert-manager and a cluster-scoped registry COS. The components that `roksbnkctl up` is responsible for landing:

| Component | What it is | Module in the bundled HCL |
|---|---|---|
| **`flo`** | F5 Lifecycle Operator — the controller that watches CNE Instance CRs and reconciles them into running BIG-IP Next pods | `module.flo` (`helm_release`) |
| **`cne_instance`** | The CR that declares "I want a BIG-IP Next data plane here" — drives `flo` to provision the TMM pods | `module.cne_instance` (`kubectl_manifest` + `wait_for`) |
| **`license`** | The License CR (JWT + operation mode) that gates BNK's runtime — sourced from the registry COS | `module.license` (`kubectl_manifest` + `wait_for`) |
| **`cluster-side bits`** | Namespaces, Secrets, ServiceAccounts, RoleBindings, SCC bindings, NADs, the cert-manager issuer chain | `kubernetes_namespace`/`kubernetes_secret` + `kubectl_manifest`, across the modules above |

`up` does **not** own the cluster, cert-manager's *chart*, the registry COS, or the jumphost — those are cluster-phase resources. See [Chapter 8](./08-cluster-phase.md) for the split.

> **The BNK phase is terraform-native.** Chart installs are `helm_release` (`wait = true`); the custom resources are `kubectl_manifest` (from the `alekc/kubectl` provider) with `wait_for` blocks that watch the resource's real `.status`; namespaces and Secrets are the `kubernetes` provider. The CRs are **real Terraform state** — `plan` diffs them, `destroy` deletes them (finalizer-aware), and drift is detected. See [§"The terraform-native deployment model"](#the-terraform-native-deployment-model) below.

## The 77-resource shape

A clean `roksbnkctl up` against a fresh cluster lands roughly **77 resources** when the cluster phase is bundled in (i.e. `cluster up` and `up` were one combined run). Against a pre-existing cluster (`cluster up` then `up`), the trial-only count is smaller — roughly the difference, ~41 resources.

The number isn't load-bearing; it shifts a few resources up or down between upstream chart releases as the charts add/remove Secrets and `kubectl_manifest` CRs. Treat "77" as a sanity-check tag, not a contract.

A representative breakdown:

```
Cluster phase (~36 resources, owned by `cluster up`)
  ROKS cluster + worker pools          ~5
  VPC + subnets + security groups       ~6
  Transit gateway + connections          ~4
  Registry COS instance + bucket          ~3
  cert-manager Helm release               ~2
  TGW jumphost VSI + cloud-init         ~16

Trial phase (~41 resources, owned by `roksbnkctl up`)
  flo + cis helm_release                 ~4
  cne_instance kubectl_manifest          ~1
  license kubectl_manifest               ~1
  namespaces + secrets (kubernetes)      ~5
  cert issuer chain (kubectl_manifest)   ~3
  NADs (kubectl_manifest)                ~2
  SCC / node-labeler kubectl_manifest   ~20
  IBM IAM trusted profile + COS reads   ~5
```

The `kubectl_manifest` resources are ordinary Terraform state: a re-run `plan` shows them as no-ops unless their spec changed — see [§"Re-running `up` is a no-op"](#re-running-up-is-a-no-op).

## Apply timing

A clean `up` against a fresh cluster takes ~50 minutes:

- ROKS cluster provisioning: 30-40 min (the bulk of the wait)
- cert-manager + flo `helm_release` (`wait = true`): ~5 min
- cne_instance reconcile: 1-2 min (gated on the CNEInstance's real `Available` condition, not a sleep)
- license verification: 1-2 min (gated on the License CR's real `status.state`, not a sleep)
- Cluster-side bits: applied concurrently with the above

Against a pre-existing cluster (already-up'd or registered), the trial-only run is **5-10 minutes**. Most of that is `helm_release` waiting for `flo` to stabilise and the two CR `wait_for` blocks resolving.

> **Readiness is gated on real signals, not fixed sleeps.** The terraform-native path uses `helm_release wait = true` and `kubectl_manifest` `wait_for`, which return the *instant* readiness is observed and no sooner — the CNEInstance's `Available` condition and the License CR's `status.state` are the actual signals it watches. On a warm cluster this real-readiness gating is the single largest contributor to the fast re-deploy loop.

## Re-running `up` is a no-op

If you re-run `roksbnkctl up` against an already-deployed BNK trial with no config change, the plan reports **`0 to add, 0 to change, 0 to destroy`** and `up` skips apply.

Each cluster object is a real Terraform resource:

- A `helm_release` knows the installed chart version, so an unchanged version is a no-op.
- A `kubectl_manifest` carries the rendered manifest as state, so an unchanged spec is a no-op (and a *changed* spec shows a precise `~ update in-place` diff of exactly the changed fields).
- A `kubernetes_secret` is keyed by its data, so an unchanged Secret is a no-op.

So the second `up` against an unchanged trial genuinely changes nothing. To bump a BNK version or tweak a CNEInstance parameter, edit `config.yaml` (or a `--var-file`) and re-run: `terraform plan` diffs **only** the changed CNEInstance spec and helm versions — Terraform is the delta engine. Use `roksbnkctl plan` to preview the diff without applying.

## Reading the Terraform plan output

`roksbnkctl up` runs `terraform plan` first and prints its output. The plan summary at the end is the most useful part:

```
Plan: 77 to add, 0 to change, 0 to destroy.
```

Or, post-rotation:

```
Plan: 12 to add, 0 to change, 12 to destroy.
```

The body of the plan shows individual resource changes with one of three markers:

- **`+ create`** — a new resource. Lines are green in a TTY.
- **`<= read`** — a data source the plan read but did not change. Common for `data "ibm_resource_group"` and similar lookups; effectively informational.
- **`# destroy`** — an in-progress destroy of an existing resource. Followed by a `+ create` if it's being replaced (e.g. a `kubectl_manifest` whose spec changed in a way that forces replacement).
- **`~ update in-place`** — a resource whose attributes are being mutated without re-creation.

The `<=` data sources are the ones that look like:

```hcl
data "ibm_resource_group" "default" {
  name = "Default"
  id   = "abc123..." (will be read)
}
```

These are read-only — Terraform is just resolving the resource group's ID at plan time so downstream modules can reference it. They show up in every plan, including no-op plans.

`# destroy` lines without a corresponding `+ create` — i.e. resources actually leaving — should make you stop and read carefully. On a re-run of `up`, this generally means an upstream HCL change removed a resource. It's rare but not zero.

## When `up` doesn't apply (no-op runs)

If the plan reports zero changes, `up` skips apply and prints:

```
✓ no changes
```

But it still does two best-effort post-actions:

1. **Fetch the kubeconfig** (unless `--no-kubeconfig`). Useful when the cluster exists but you've never grabbed the admin kubeconfig on this workstation.
2. **Auto-register the `jumphost` target.** Reads `testing_tgw_jumphost_ip` and `jumphost_shared_key` from Terraform outputs and writes a `targets:jumphost` entry in workspace config. Re-runs are idempotent.

So `roksbnkctl up` against an unchanged cluster is a useful "re-establish my workstation's view of this workspace" verb — it can't hurt anything (no apply runs), and it freshens local artefacts.

## The `--auto`, `--no-kubeconfig`, `--var-file` flags

```bash
roksbnkctl up [--auto] [--no-kubeconfig] [--var-file <path>]...
```

| Flag | Effect |
|---|---|
| `--auto` | Skip the "Apply this plan? [y/N]" prompt. Required for non-interactive runs (CI, scripted pipelines). |
| `--no-kubeconfig` | Skip the post-apply kubeconfig fetch. Useful when you've already got a kubeconfig and don't want it overwritten. |
| `--var-file <path>` | Layer extra Terraform var-files onto the chain (repeatable; later wins). Lets you parameterise without editing config.yaml. |
| `--tf-source <ref>` | Override the pinned TF source for this run only. Skip the embedded HCL and use a path or URL instead. Mostly for dev. |

`--var-file` is the canonical way to stage a non-default deploy. For example, deploying a BNK trial with a non-default `cne_instance.replicas`:

```bash
echo 'cne_replicas = 3' > ./more-replicas.tfvars
roksbnkctl up --auto --var-file ./more-replicas.tfvars
```

The var-file chain is, in order:

1. The auto-generated `terraform.tfvars` (rendered from `config.yaml`).
2. `~/.roksbnkctl/<workspace>/terraform.tfvars.user` if present — sibling to `config.yaml`, serving both the trial and cluster phases (a manual raw-tfvars override you place at the workspace root — see [Chapter 6 §"Raw terraform-variable overrides"](./06-workspaces.md#raw-terraform-variable-overrides)).
3. Each `--var-file` flag, left-to-right.

Later wins on conflict — same as Terraform's own ordering. If you find yourself passing the same `--var-file` on every `up` / `plan` / `apply` / `down`, drop it once at `~/.roksbnkctl/<ws>/terraform.tfvars.user` (sibling to `config.yaml`) — the lifecycle auto-layers that file on every subsequent call, so you can drop the flag.

## Reviewing a plan before applying (`plan --out` / `apply --plan`)

`plan` and `apply` are separate commands, so you can review a change in full before it touches the cluster — useful for change-control sign-off, or simply when the diff is larger than your terminal scrollback. Save the plan to a file, review it, then apply **exactly that plan**:

```bash
# 1. Save the plan: a binary plan file + a human-readable .txt copy
roksbnkctl plan -w dev --out ./bnk.plan
#   ✓ Saved plan: /…/bnk.plan (reviewable copy: /…/bnk.plan.txt)
#     Review it, then apply EXACTLY this plan:
#       roksbnkctl apply -w dev --plan /…/bnk.plan

# 2. Review bnk.plan.txt (the complete terraform diff — no scrollback limit)

# 3. Apply exactly the reviewed plan — no re-plan
roksbnkctl apply -w dev --plan ./bnk.plan
```

`apply --plan` applies the saved plan **verbatim**: it does not re-plan, and it passes no var-files (the plan already captured every variable). If state or config has drifted since the plan was saved, Terraform **refuses** the stale plan rather than applying something you didn't review — that is the change-control guarantee.

Without `--plan`, a bare `roksbnkctl apply` re-plans and applies fresh. That is fine for iteration, but it does **not** guarantee the applied change equals the one you reviewed with `plan`. Use `--out` / `--plan` whenever "what I reviewed is exactly what applied" matters.

> The saved-plan flow requires the **local** execution backend; on a `docker` or remote backend, `--out` / `--plan` error clearly rather than being silently ignored.

## Apply retries on transient errors

ROKS master endpoints take 1-5 minutes to fully propagate after the cluster reaches `Ready`. The `helm`, `kubernetes`, and `alekc/kubectl` providers all talk to the master directly; on a fresh cluster, they sometimes race propagation and fail with a connection error (`Connection refused`, `i/o timeout`, `TLS handshake timeout`).

`roksbnkctl up` has built-in retry: up to 3 apply attempts, with a 60-second sleep between attempts, on any of these heuristic patterns:

- `exit status 7` (curl couldn't connect)
- `Connection refused` / `connection refused`
- `i/o timeout`
- `no route to host`
- `network is unreachable`
- `no such host`
- `TLS handshake timeout`
- `failed to dial`
- `to download the config doesn't exist`

If your apply hits one of these, you'll see:

```
→ apply attempt 1 hit a transient-looking failure; waiting 60s and retrying...
```

Terraform's idempotence means already-created resources are skipped on the retry; only the failed resources / data sources re-execute. After 3 attempts, `up` gives up:

```
✗ apply still failing after 3 attempts — giving up
```

At that point, fix the underlying cause (usually wait longer or re-run manually) and try again. The retry is for transient races, not persistent failures.

## What happens on success

A successful `up` does five things in order:

1. **Apply complete.** `Apply complete! Resources: 77 added, 0 changed, 0 destroyed.`
2. **Fetch the admin kubeconfig** from IBM Cloud's container service API. Written to `$KUBECONFIG` (or `~/.kube/config`) at mode 0600.
3. **Auto-register the `jumphost` target** in workspace config (so `--on jumphost` works without manual config — see [Chapter 16](./16-on-flag-ssh-jumphosts.md)).
4. **Stamp `terraform.tfstate`'s mtime.** `roksbnkctl status` reads this as "last apply" timestamp.
5. **Exit 0.**

The kubeconfig fetch and jumphost registration are best-effort: they log warnings on failure but don't fail the parent command. `up` succeeded if Terraform succeeded; the post-apply niceties are conveniences.

## The `bnk up` / `bnk down` command group

The `roksbnkctl bnk` group is the trial-only counterpart to `roksbnkctl cluster` — it operates on the trial state under `state/` and leaves the cluster state under `state-cluster/` untouched. Because it iterates on the trial without rebuilding the cluster, a `bnk down` / `bnk up` round-trip is the 5-10 minute trial-apply window, with the cluster running underneath.

### `roksbnkctl bnk up`

Deploys the BNK trial against the workspace's registered cluster.

- If the workspace already has a cluster phase (either from `cluster up` or from `cluster register`), `bnk up` runs the trial apply directly — same plan, same ~41 resources, same 5-10 minute window as the trial half of a full `up`.
- If the workspace is **empty** (no cluster registered yet), `bnk up` offers to **bootstrap the cluster phase first** with a confirmation prompt, then runs the trial apply. This keeps the new user's quick-start path one command, even if they typed `bnk up` instead of `up`.

Sample output of the bootstrap-prompt path:

```
$ roksbnkctl bnk up
No cluster registered for this workspace.
→ Provisioning the cluster phase first (ROKS cluster + transit gateway +
  registry COS + cert-manager + jumphost; ~30 min) before the BNK trial.
Continue? [y/N]: y
→ terraform plan (cluster phase: deploy_bnk=false forced)
...
✓ Wrote ~/.roksbnkctl/default/cluster-outputs.json
→ terraform plan (trial phase)
...
Apply complete! Resources: 41 added, 0 changed, 0 destroyed.
```

Three prompts fire in the empty-workspace case — one for "do you want to bootstrap the cluster phase," one for "apply this terraform plan" inside the nested `cluster up`, and a third when the trial-phase apply prompts. (On a non-empty workspace where `bnk up` skips the cluster bootstrap, only the latter two fire — and a `ShapeClusterOnly`/`ShapeSplit` `bnk up` is the common iteration case.) For a 30-minute operation we kept the prompts explicit rather than collapsing them. `--auto` skips all three:

```
$ roksbnkctl bnk up --auto
```

### `roksbnkctl bnk down`

Destroys the trial only. The cluster phase keeps running.

- On a **split** workspace (cluster + trial both present), `bnk down` runs `terraform destroy` against the trial state — ~41 resources, the same as the trial half of a full `down`.
- On an **empty** or **cluster-only** workspace, `bnk down` is a **no-op success** (exit 0): there's no trial to destroy, and an orchestrated teardown runs every phase unconditionally, so failing for having nothing to do would fail a teardown that worked.

Sample output against a split workspace:

```
$ roksbnkctl bnk down --auto
→ terraform destroy (trial phase)
  module.license.kubectl_manifest.license: Destroying...
  module.cne_instance.kubectl_manifest.cneinstance: Destroying...
  module.flo.helm_release.flo: Destroying...
  ...
  Destroy complete! Resources: 41 destroyed.

✓ Trial phase destroyed. Cluster phase ~/.roksbnkctl/default/state-cluster/ is intact.
  Run `roksbnkctl bnk up` to deploy another trial against the same cluster.
```

### The shape dispatch matrix

The unscoped `roksbnkctl up` / `down` verbs are now **shape-aware composites** — they detect the on-disk shape of the workspace and delegate to the right phase commands underneath. The full picture for all four shapes and all six commands:

| Command | **Empty** (nothing applied) | **ClusterOnly** (`cluster up` ran) | **Split** (cluster + trial both applied) |
|---|---|---|---|
| `up` | `cluster up` → trial up | trial up | `cluster up` (refresh) → trial up |
| `down` | no-op (exit 0): nothing to destroy | `cluster down` | trial down → `cluster down` |
| `bnk up` | confirm + `cluster up` → trial up | trial up | trial up |
| `bnk down` | no-op (exit 0): no trial | no-op (exit 0): no trial | trial down |
| `cluster up` | `cluster up` | `cluster up` (refresh) | `cluster up` (refresh) |
| `cluster down` | no-op (exit 0): nothing to destroy | `cluster down` | **refuse**: trial exists |

The user-facing simplification: the unscoped `up` / `down` "just work" against every shape. The phase-scoped commands (`bnk`, `cluster`) only operate when the shape allows isolation and refuse loudly with an actionable message otherwise. Refusals always point at the resolution — see [Chapter 11 §"Refusal messages"](./11-tearing-down.md#refusal-messages-catalogue) for the full catalogue.

The engineering version of this table — with the implementation details, the `ShapeUnknown` edge cases, and the rationale — lives in [PRD 06 §"Dispatch table"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#dispatch-table).

### Worked example — iterating on a BNK trial

The headline workflow the phase split unlocks. You're testing different `cne_instance` parameter combinations against a stable cluster.

```bash
# Step 1 — one-time cluster provision (~38 minutes)
roksbnkctl cluster up --auto
# → terraform apply (cluster phase: deploy_bnk=false forced)
#   ...
#   Apply complete! Resources: 36 added, 0 changed, 0 destroyed.
# ✓ Wrote ~/.roksbnkctl/default/cluster-outputs.json

# Step 2 — first BNK trial (~7 minutes — trial only, cluster is reused)
roksbnkctl bnk up --auto
# → terraform plan (trial phase)
#   Plan: 41 to add, 0 to change, 0 to destroy.
#   ...
#   Apply complete! Resources: 41 added, 0 changed, 0 destroyed.

# Step 3 — poke at the trial, find something to tune
roksbnkctl k get pods -n f5-bnk
roksbnkctl test connectivity

# Step 4 — destroy just the trial (~3 minutes — cluster persists)
roksbnkctl bnk down --auto
# → terraform destroy (trial phase)
#   Destroy complete! Resources: 41 destroyed.
# ✓ Trial phase destroyed. Cluster phase ~/.roksbnkctl/default/state-cluster/ is intact.

# Step 5 — edit config.yaml (or a --var-file) to change cne_instance settings
$EDITOR ~/.roksbnkctl/default/config.yaml

# Step 6 — second BNK trial against the same cluster (~7 minutes; the 30-minute
#          cluster provision from step 1 does NOT repeat)
roksbnkctl bnk up --auto
# → terraform plan (trial phase)
#   ...
#   Apply complete! Resources: 41 added, 0 changed, 0 destroyed.
```

The win is in step 6: the cluster persists across the `bnk down` / `bnk up` boundary, so the second trial deploy is **~7 minutes** instead of the ~50 minutes a full `down` → `up` cycle would cost. Across a day of iteration, that's the difference between five trial permutations and one.

When you're done with the whole session:

```bash
# Step 7 — tear down the cluster too
roksbnkctl cluster down --auto
# (or `roksbnkctl down` from any starting state — see the dispatch matrix above)
```

## The terraform-native deployment model

The BNK phase has three layers, each owned by a purpose-built Terraform provider. Nothing shells out to `curl`, `kubectl`, or `helm`; nothing sleeps a fixed interval.

### 1. The install layer — `helm_release`

cert-manager, FLO, and CIS install as `helm_release` resources with `wait = true`. `helm_release` runs Helm in-process (via the `hashicorp/helm` provider), tracks the installed chart/version in Terraform state, and — with `wait = true` — blocks until the release's workloads report ready. FAR version discovery stays Terraform-side: the manifest chart is still pulled to read the FLO and CIS chart versions, which feed the `helm_release.version` argument.

The chart installs have **prerequisites** that must exist first, or `helm_release wait = true` would hang on `ImagePullBackOff`:

- **Namespaces** (`f5-bnk`, `f5-utils`) — `kubernetes_namespace`. **One namespace if you collapse them**: setting `bnk.flo_namespace` and `bnk.flo_utils_namespace` to the same value is supported, and the utils-side namespace (and its duplicate secrets) are then not created at all.
- **Secrets** — the FAR image-pull secret `far-secret` (in both namespaces — or once, when they are collapsed to one) and the CIS `f5-bigip-ctlr-login` login secret — `kubernetes_secret`. These carry the registry credentials the charts pull with, so they are ordered *before* the charts via `depends_on`.

### 2. The CR layer — `kubectl_manifest` + `wait_for`

Every custom resource — the cert-manager issuer chain (`ClusterIssuer`, the CA `Certificate`, the CA `ClusterIssuer`), the two `NetworkAttachmentDefinition`s, the OpenShift SCC `ClusterRoleBinding`s, the node-labeler `Job`, the **CNEInstance**, and the **License** — is a `kubectl_manifest` resource from the `alekc/kubectl` provider. Each manifest body is the CR spec rendered by `yamlencode`.

Two CRs gate on **real readiness** via a `wait_for` block that watches the live object's `.status`:

```hcl
# CNEInstance — wait for FLO to report the instance up
resource "kubectl_manifest" "cneinstance" {
  yaml_body         = yamlencode(local.cneinstance_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  wait_for {
    condition {
      type   = "Available"
      status = "True"
    }
  }
  depends_on = [helm_release.flo]   # the FLO chart installs the CRD
}

# License — wait for the CPCL state machine to reach the licensed state
resource "kubectl_manifest" "license" {
  yaml_body         = yamlencode(local.license_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  wait_for {
    field {
      key   = "status.state"
      value = "Verification Complete"
    }
  }
  depends_on = [helm_release.flo, kubectl_manifest.cneinstance]
}
```

`wait_for` polls the live object and returns the instant the matched condition/field becomes true. The CNEInstance signal is its `Available` condition (`status.conditions[type=Available].status == True`); the License signal is its `status.state` reaching the licensed value. (The `"Verification Complete"` literal comes from the architect spike's CWC-REST evidence and the FAR-shipped License CRD; it is **confirmed against a live licensed cluster by the integrator's `e2e-bnk-native.sh` run** before merge — if the live value differs, staff pins the real one or switches to the `status.conditions[]` fallback matcher, which is equally expressible.) Resources with no meaningful readiness status — NADs, SCC bindings, issuers — carry no `wait_for` and simply apply.

### 3. Namespaces + Secrets — the `kubernetes` provider

Covered above as the chart prerequisites. They use the `hashicorp/kubernetes` provider rather than `kubectl_manifest` because they have typed, first-class resource schemas (`kubernetes_namespace`, `kubernetes_secret`) and no plan-time-CRD concern.

### Provider wiring and ordering

All three providers are fed from the **same** `data "ibm_container_cluster_config"` the modules already use — `host`, `token`, and the decoded `cluster_ca_certificate`. The `create_roks_cluster ? 0 : 1` guard that keeps the provider config valid when the cluster doesn't exist yet at plan time is unchanged.

The only **genuinely serial** spine is `cert-manager helm_release → the CA issuer chain → flo helm_release → CNEInstance → License`. Everything else — the NADs, the three Secrets, the node-labeler subtree, and the CNEInstance SCC bindings (19 — 9 FLO-side, 10 utils-side; 18 when the namespaces are collapsed and the duplicate `default` binding dedupes) — carries no `depends_on` edge back to the spine, so Terraform's default parallelism applies them concurrently with the install spine. That parallelism, plus real-readiness gating instead of fixed sleeps, is where the speed comes from.

## Why `alekc/kubectl`

A concept note on the deployment model, for readers who want the *why* rather than the *how*.

**The plan-time-CRD problem.** Applying a CR whose CRD is installed *in the same apply* (the FLO chart installs the CNEInstance and License CRDs, then we apply those CRs) is the classic Terraform trap. HashiCorp's own `kubernetes_manifest` does a typed-schema lookup of the kind at **plan** time and fails with `cannot select exact GVK ... no matches for kind` because the CRD doesn't exist yet. The `alekc/kubectl` provider's `kubectl_manifest` carries the manifest as an opaque YAML string and resolves the GVK at **apply** time — so it applies a CR whose CRD is created in the same run, no plan-time schema lookup. It gives us real state + drift + finalizer-aware delete + `wait_for`, *without* the plan-time-CRD failure and *without* building a custom provider or a Go reconciler. Ordering (CRD before CR) is expressed with `depends_on` on the chart's `helm_release` — `wait_for` removes the sleep, not the ordering.

**What stays IBM-Cloud-native.** The IBM IAM trusted-profile resources (for the CNE controller service account) and the COS reads (the FAR auth archive and the license JWT, fetched via the IBM IAM token + the COS S3 REST API) are IBM-Cloud-native Terraform resources/data sources, not Kubernetes mutations.

**The air-gap caveat for `alekc/kubectl`.** `alekc/kubectl` is a third-party provider on the public Terraform registry (`registry.terraform.io/providers/alekc/kubectl`). On connected runs it downloads at `terraform init` like any provider. For **air-gapped / offline** ROKS installs it must be pre-staged: build a `terraform providers mirror` bundle on a connected machine and point the offline runner at it via a `provider_installation { filesystem_mirror { ... } }` block in the Terraform CLI config (or an internal `network_mirror`). The provider is pinned (`>= 2.4.0`) and recorded in `.terraform.lock.hcl`, so mirrored installs are reproducible.

> **Note — transcripts, resource counts, and timings are illustrative.** The sample `terraform plan` / `apply` / `bnk up` / `bnk down` output, the `~41`/`~77`-resource breakdowns, and the timing figures in this chapter are *representative* of the terraform-native path, not captured from a live run. The command **surface** (the resource *shapes* — `helm_release` / `kubernetes_*_v1` / `kubectl_manifest` + `wait_for`) is reconciled against the built binary and the landed terraform and is accurate; treat the specific numbers and console lines as orientation, not contract.

## Cross-references

- [Chapter 7 — Quick start](./07-quick-start.md) — happy-path walkthrough end-to-end.
- [Chapter 8 — The cluster phase](./08-cluster-phase.md) — what `cluster up` provisions and the two state directories.
- [Chapter 11 — Tearing down](./11-tearing-down.md) — phase-aware decision matrix; full refusal-message catalogue; orphan recovery.
- [Chapter 13 — Terraform variables](./13-terraform-variables.md) — full reference for what you can override via `--var-file`.
- [Chapter 22 — Throughput testing](./22-throughput-testing.md) — once BNK is deployed, validating its data plane.
- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) — long-tail apply failures (SCC violations, propagation lag, kubeconfig 404s) and their fixes.
