# Deploying BNK trials on top

`roksbnkctl up` deploys a **BNK trial** — F5's Lifecycle Operator, the CNE Instance, license bundles, and the cluster-side glue that makes them work — onto a ROKS cluster that already exists. "Already exists" means either provisioned by [`cluster up`](./08-cluster-phase.md) or [registered](./09-registering-existing-cluster.md) from a pre-existing cluster.

For workspaces where the cluster and the trial are managed as separate phases (the v1.1.0 default — see [Chapter 8](./08-cluster-phase.md)), the trial layer also gets its own command pair: `roksbnkctl bnk up` / `bnk down`. `bnk down` tears down only the trial; the cluster keeps running, so the next iteration starts in 5-10 minutes instead of an hour. The `bnk` group is documented in [§"The `bnk up` / `bnk down` command group"](#the-bnk-up--bnk-down-command-group) below.

This chapter is the deeper-than-quick-start view of `up`: what each module does, the ~77-resource shape of a clean apply, the token-rotation observation when you re-run `up` against an existing cluster, how to read the Terraform plan output, and how the `bnk` group + the shape-aware composite `up` / `down` fit together.

[Chapter 7 — Quick start](./07-quick-start.md) shows the happy path end-to-end with sample output. This chapter goes deeper.

## What "deploying BNK" means

A BNK trial is a deliberately small set of Kubernetes resources that share state with a cluster-shared cert-manager and a cluster-scoped registry COS. The components that `roksbnkctl up` is responsible for landing:

| Component | What it is | Module in the bundled HCL |
|---|---|---|
| **`flo`** | F5 Lifecycle Operator — the controller that watches CNE Instance CRs and reconciles them into running BIG-IP Next pods | `module.flo` (Helm release) |
| **`cne_instance`** | The CR that declares "I want a BIG-IP Next data plane here" — drives `flo` to provision the TMM pods | `module.cne_instance` (Kubernetes manifest) |
| **`license`** | JWT licenses + activation tokens that gate BNK's runtime — sourced from the registry COS | `module.license` (Helm release + null_resources) |
| **`cluster-side bits`** | ServiceAccounts, RoleBindings, SCC bindings, Secrets that flo / cne_instance / license need at runtime | scattered across the modules above |

`up` does **not** own the cluster, cert-manager, the registry COS, or the jumphost — those are cluster-phase resources. See [Chapter 8](./08-cluster-phase.md) for the split.

## The 77-resource shape

A clean `roksbnkctl up` against a fresh cluster lands roughly **77 resources** when the cluster phase is bundled in (i.e. `cluster up` and `up` were one combined run). Against a pre-existing cluster (`cluster up` then `up`), the trial-only count is smaller — roughly the difference, ~41 resources.

The number isn't load-bearing; it shifts a few resources up or down between upstream HCL releases as the chart adds/removes null_resources and Secrets. Treat "77" as a sanity-check tag, not a contract.

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
  flo Helm release                       ~5
  cne_instance manifest + finalisers     ~4
  license Helm release                  ~10
  Cluster-side SAs / RoleBindings / SCC ~10
  null_resources for token bootstrap    ~12
```

The null_resources at the bottom of the list are interesting — they're the ones that re-run on every apply (more on that below).

## Apply timing

A clean `up` against a fresh cluster takes ~50 minutes:

- ROKS cluster provisioning: 30-40 min (the bulk of the wait)
- cert-manager + flo Helm install: ~5 min
- cne_instance reconcile: 1-2 min
- license bootstrap (token generation + activation): 2-3 min
- Cluster-side bits + finalisers: 2-3 min

Against a pre-existing cluster (already-up'd or registered), the trial-only run is **5-10 minutes**. Most of that is Helm waiting for `flo` to stabilise and the license module's null_resources running.

## The token-rotation observation

If you re-run `roksbnkctl up` against an already-deployed BNK trial, you'll see ~41 resources `re-create` or `update in-place` even though "nothing changed". This is expected.

The `license` module rotates **admin certificate tokens** between runs — the JWT used to authenticate against the BNK control plane is short-lived and re-minted on each apply. A token rotation cascades into ~12 null_resources that exist solely to inject the new token into Helm-managed Secrets:

```
module.license.null_resource.cncf_admin_cert_token: Refreshing state... [id=8746234876]
module.license.null_resource.cncf_admin_cert_token: Destroying... [id=8746234876]
module.license.null_resource.cncf_admin_cert_token: Destruction complete after 0s
module.license.null_resource.cncf_admin_cert_token: Creating...
module.license.null_resource.cncf_admin_cert_token: Creation complete after 12s [id=9183746183]
```

That's why the count of "destroyed + created" can hit ~41 even when no infrastructure-meaningful changes have been made.

The rotation is harmless — running pods aren't restarted, traffic isn't interrupted. The new token replaces the old in the relevant Secret; flo notices and updates its in-memory cache. From the BNK trial's runtime perspective, the second `up` is a no-op.

If you want to skip the rotation cycle and just check "would this plan change anything significant?", use `roksbnkctl plan` rather than `up` — it shows the plan without applying.

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
- **`# destroy`** — an in-progress destroy of an existing resource. Followed by a `+ create` if it's being replaced (the null_resource rotation case).
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
2. `~/.roksbnkctl/<workspace>/terraform.tfvars.user` if present.
3. Each `--var-file` flag, left-to-right.

Later wins on conflict — same as Terraform's own ordering.

## Apply retries on transient errors

ROKS master endpoints take 1-5 minutes to fully propagate after the cluster reaches `Ready`. The `cne_instance`, `license`, and `cert-manager` modules all curl the master directly; on a fresh cluster, they sometimes race propagation and fail with `exit status 7` (curl couldn't connect) or `Connection refused`.

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

Terraform's idempotence means already-created resources are skipped on the retry; only the failed null_resources / data sources re-execute. After 3 attempts, `up` gives up:

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

New in v1.1.0. The `roksbnkctl bnk` group is the trial-only counterpart to `roksbnkctl cluster` — it operates on the trial state under `state/` and leaves the cluster state under `state-cluster/` untouched. The whole point is that **iterating on a BNK trial no longer costs a 30-minute cluster rebuild**: a `bnk down` / `bnk up` round-trip is the 5-10 minute trial-apply window, the cluster keeps running underneath.

### `roksbnkctl bnk up`

Deploys the BNK trial against the workspace's registered cluster.

- If the workspace already has a cluster phase (either from `cluster up` or from `cluster register`), `bnk up` runs the trial apply directly — same plan, same ~41 resources, same 5-10 minute window as the trial half of a full `up`.
- If the workspace is **empty** (no cluster registered yet), `bnk up` offers to **bootstrap the cluster phase first** with a confirmation prompt, then runs the trial apply. This keeps the new user's quick-start path one command, even if they typed `bnk up` instead of `up`.
- On a [legacy single-state](./08-cluster-phase.md#legacy-single-state-workspaces) workspace, `bnk up` **refuses** — there's no way to isolate the trial phase when the trial and cluster share one state file.

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
- On an **empty** or **cluster-only** workspace, `bnk down` refuses: there's no trial to destroy.
- On a **legacy single-state** workspace, `bnk down` refuses: the cluster lives in the trial state so a trial-only destroy isn't possible.

Sample output against a split workspace:

```
$ roksbnkctl bnk down --auto
→ terraform destroy (trial phase)
  module.license.helm_release.license: Destroying...
  module.cne_instance.kubernetes_manifest.cne: Destroying...
  module.flo.helm_release.flo: Destroying...
  ...
  Destroy complete! Resources: 41 destroyed.

✓ Trial phase destroyed. Cluster phase ~/.roksbnkctl/default/state-cluster/ is intact.
  Run `roksbnkctl bnk up` to deploy another trial against the same cluster.
```

### The shape dispatch matrix

The unscoped `roksbnkctl up` / `down` verbs are now **shape-aware composites** — they detect the on-disk shape of the workspace and delegate to the right phase commands underneath. The full picture for all four shapes and all six commands:

| Command | **Empty** (nothing applied) | **ClusterOnly** (`cluster up` ran) | **Split** (cluster + trial both applied) | **LegacySingle** (v1.0.x state) |
|---|---|---|---|---|
| `up` | `cluster up` → trial up | trial up | `cluster up` (refresh) → trial up | monolithic trial up (v1.0.x behaviour) |
| `down` | error: nothing to destroy | `cluster down` | trial down → `cluster down` | monolithic trial down (v1.0.x behaviour) |
| `bnk up` | confirm + `cluster up` → trial up | trial up | trial up | **refuse** |
| `bnk down` | refuse: no trial | refuse: no trial | trial down | **refuse** |
| `cluster up` | `cluster up` | `cluster up` (refresh) | `cluster up` (refresh) | **refuse** |
| `cluster down` | refuse: nothing to destroy | `cluster down` | **refuse**: trial exists | **refuse** |

The user-facing simplification: the unscoped `up` / `down` "just work" against every shape (including v1.0.x legacy state). The phase-scoped commands (`bnk`, `cluster`) only operate when the shape allows isolation and refuse loudly with an actionable message otherwise. Refusals always point at the resolution — see [Chapter 11 §"Refusal messages"](./11-tearing-down.md#refusal-messages-catalogue) for the full catalogue.

The engineering version of this table — with the implementation details, the `ShapeUnknown` edge cases, and the rationale — lives in [PRD 06 §"Dispatch table"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#dispatch-table).

### Worked example — iterating on a BNK trial

The headline workflow the v1.1.0 surface unlocks. You're testing different `cne_instance` parameter combinations against a stable cluster.

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

The win is in step 6: the cluster persists across the `bnk down` / `bnk up` boundary, so the second trial deploy is **~7 minutes** instead of the ~50 minutes a full `down` → `up` cycle would cost in v1.0.x. Across a day of iteration, that's the difference between five trial permutations and one.

When you're done with the whole session:

```bash
# Step 7 — tear down the cluster too
roksbnkctl cluster down --auto
# (or `roksbnkctl down` from any starting state — see the dispatch matrix above)
```

## Cross-references

- [Chapter 7 — Quick start](./07-quick-start.md) — happy-path walkthrough end-to-end.
- [Chapter 8 — The cluster phase](./08-cluster-phase.md) — what `cluster up` provisions, the two state directories, and how to identify a legacy single-state workspace.
- [Chapter 11 — Tearing down](./11-tearing-down.md) — phase-aware decision matrix; full refusal-message catalogue; orphan recovery.
- [Chapter 13 — Terraform variables](./13-terraform-variables.md) — full reference for what you can override via `--var-file`.
- [Chapter 22 — Throughput testing](./22-throughput-testing.md) — once BNK is deployed, validating its data plane.
- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) — long-tail apply failures (SCC violations, propagation lag, kubeconfig 404s) and their fixes.
