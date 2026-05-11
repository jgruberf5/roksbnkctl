# Deploying BNK trials on top

`roksbnkctl up` is the verb that deploys a **BNK trial** — F5's Lifecycle Operator, the CNE Instance, license bundles, and the cluster-side glue that makes them work — onto a ROKS cluster that already exists. "Already exists" means either provisioned by [`cluster up`](./08-cluster-phase.md) or [registered](./09-registering-existing-cluster.md) from a pre-existing cluster.

This chapter is the deeper-than-quick-start view of `up`: what each module does, the ~77-resource shape of a clean apply, the token-rotation observation when you re-run `up` against an existing cluster, and how to read the Terraform plan output that scrolls past on every run.

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

## Cross-references

- [Chapter 7 — Quick start](./07-quick-start.md) — happy-path walkthrough end-to-end.
- [Chapter 11 — Tearing down](./11-tearing-down.md) — `roksbnkctl down` to undo a trial.
- [Chapter 13 — Terraform variables](./13-terraform-variables.md) — full reference for what you can override via `--var-file`.
- [Chapter 22 — Throughput testing](./22-throughput-testing.md) — once BNK is deployed, validating its data plane.
- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) — long-tail apply failures (SCC violations, propagation lag, kubeconfig 404s) and their fixes.
