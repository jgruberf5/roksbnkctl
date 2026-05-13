# Tearing down

`roksbnkctl down`, `roksbnkctl bnk down`, and `roksbnkctl cluster down` are the three destroy verbs — the inverses of [`up`](./10-deploying-bnk-trials.md), [`bnk up`](./10-deploying-bnk-trials.md#the-bnk-up--bnk-down-command-group), and [`cluster up`](./08-cluster-phase.md) respectively. This chapter covers what each one removes, the ordering constraint between them, the refusal messages you'll hit if you ask for the wrong one, what survives a destroy, the `--auto` flag for non-interactive runs, and the workspace-cleanup story.

## The phase-aware decision tree

Which verb do you want? The shape of your workspace and your intent both matter. Start here:

```
I want to keep the cluster and just tear down the BNK trial:
    → roksbnkctl bnk down

I want to tear down everything (cluster + trial):
    → roksbnkctl down

I want to tear down only the cluster (no trial currently deployed):
    → roksbnkctl cluster down

I'm on a v1.0.x workspace (cluster + trial in one state):
    → roksbnkctl down       (tears down everything in one shot)
    → see Chapter 8 §"Legacy single-state workspaces" to confirm your shape
```

Quick shape check: `ls ~/.roksbnkctl/<workspace>/` — if you see `state-cluster/`, you're on the v1.1.0 split shape; if you see only `state/`, you're on legacy single-state.

The big rule, stated up front: **destroy in reverse of create**. Trial first (`bnk down`), cluster second (`cluster down`). The unscoped `roksbnkctl down` does this ordering for you — on a split workspace it runs the trial destroy first and then the cluster destroy. On a legacy single-state workspace it runs a monolithic destroy (the v1.0.x behaviour, byte-for-byte). Either way you don't have to think about ordering; `down` is the safe default.

The phase-scoped commands (`bnk down`, `cluster down`) are the precision tools — they let you keep one phase across many cycles of the other. They also **refuse loudly** if you ask them to do something that would orphan resources or that the shape doesn't allow. The full refusal catalogue is in [§"Refusal messages catalogue"](#refusal-messages-catalogue) below; the rule of thumb is that the error message always names the verb that would actually work.

## The three destroys

There are three teardown verbs matching the three slices of state:

### `roksbnkctl down` — shape-aware composite

The unscoped `down` is a **shape-aware composite** in v1.1.0: it detects the on-disk shape of the workspace and dispatches to the right phase destroys in the right order.

```bash
roksbnkctl down
```

| Workspace shape | `down` does |
|---|---|
| Split (cluster + trial) | trial destroy → cluster destroy |
| ClusterOnly (only cluster applied) | cluster destroy |
| LegacySingle (v1.0.x — both in one state) | monolithic destroy (v1.0.x behaviour, byte-for-byte) |
| Empty | error: `nothing to destroy in this workspace` |

This is the safe default — `down` always does the right thing regardless of shape, and it's the only verb you can run on a legacy single-state workspace.

### `roksbnkctl bnk down` — destroy the BNK trial only

New in v1.1.0. Tears down everything the trial phase created — the `flo` Helm release, `cne_instance`, the license module, cluster-side ServiceAccounts / RoleBindings / SCC bindings, and the null_resources that bootstrap admin tokens — and leaves the cluster running.

```bash
roksbnkctl bnk down
```

What survives:

- The ROKS cluster itself
- cert-manager
- The registry COS instance and its bucket contents (FAR images, license artefacts)
- The TGW jumphost
- All cluster-phase Terraform state under `state-cluster/`
- `cluster-outputs.json` (the cluster is still registered)
- The workspace's `config.yaml`

Roughly **41 resources destroyed** on a clean trial-only `bnk down`. Time is dominated by Helm's pre-delete hooks and the cne_instance finaliser unwind — usually 2-5 minutes total.

`bnk down` **refuses** on Empty, ClusterOnly, and LegacySingle workspaces — there's nothing to destroy on the first two, and the trial-only isolation isn't possible on the third. See [§"Refusal messages catalogue"](#refusal-messages-catalogue) for the exact text.

### `roksbnkctl cluster down` — destroy the cluster phase

Tears down the cluster + cluster-shared services: the ROKS cluster, transit gateway, registry COS instance, cert-manager Helm release, and the TGW jumphost.

```bash
roksbnkctl cluster down
```

What survives:

- The workspace's `config.yaml`
- `~/.roksbnkctl/<workspace>/state/` (now empty of resources but the directory persists)
- `~/.roksbnkctl/<workspace>/state-cluster/` Terraform state files (the cluster-side state itself is empty; the directory and `terraform.tfstate` persist)

Roughly **36 resources destroyed**. The ROKS cluster destroy alone is 5-10 minutes; everything else is fast.

The post-destroy cleanup deletes `cluster-outputs.json` automatically — the workspace no longer has a registered cluster.

## Order matters: trial first, then cluster

The upstream HCL's resource graph requires this ordering. The trial-phase resources have implicit dependencies on cluster-phase resources (they live *in* the cluster, after all), and Terraform's destroy graph traverses dependencies in reverse. If the cluster phase tries to destroy first, the trial phase's resources are still there — finalisers block the destroy of the cluster's namespaces, the cluster-side SCC bindings reference SCCs that are in the way, and so on.

In v1.1.0 `roksbnkctl cluster down` enforces this ordering with a **hard refusal**: if the trial state has any resources in it, `cluster down` errors out and points you at `bnk down` (or `down`) instead. The v1.0.x "warning-but-prompt" behaviour is gone — even `--auto` won't bypass the guard, because correctness, not confirmation, is the issue. The full refusal text:

```
$ roksbnkctl cluster down
BNK trial state exists in this workspace; run `roksbnkctl bnk down` first
(or `roksbnkctl down` to tear down both phases)
```

So in practice, **always destroy the trial before the cluster**. The unscoped `down` does this ordering for you on a split workspace; the phase-scoped pair is `bnk down` then `cluster down`.

The clean teardown sequence — split workspace, explicit phase commands:

```bash
# 1. Destroy the BNK trial
roksbnkctl bnk down --auto

# 2. Now safe to destroy the cluster phase
roksbnkctl cluster down --auto

# 3. (Optional) Delete the workspace itself
roksbnkctl ws delete <name> --force
```

Or the one-shot equivalent:

```bash
# 1. Tear down both phases in order
roksbnkctl down --auto

# 2. (Optional) Delete the workspace itself
roksbnkctl ws delete <name> --force
```

If you `roksbnkctl up` against a registered cluster (one you didn't `cluster up` yourself), step 2 doesn't apply — the cluster wasn't yours to destroy. Just `bnk down` the trial and stop there, then optionally unregister by deleting `cluster-outputs.json`.

## Refusal messages catalogue

The phase-scoped destroy verbs refuse loudly when the shape doesn't allow what you've asked for. Every refusal names the verb that would actually work. If you hit one in the wild, grep your terminal output for the message text and you should land here:

| Command + shape | Refusal text | Resolution |
|---|---|---|
| `bnk down` on **LegacySingle** | `this workspace is legacy single-state; `bnk down` can't isolate the trial phase. Use `roksbnkctl down` to tear down both, or migrate the state first` | Use `roksbnkctl down`; the legacy state has the trial and cluster in one file, so a trial-only destroy isn't possible. See [Chapter 8 §"Legacy single-state workspaces"](./08-cluster-phase.md#legacy-single-state-workspaces). |
| `bnk down` on **Empty** or **ClusterOnly** | `no BNK trial state to destroy in this workspace` | Nothing to do — no trial is deployed. If you want to destroy the cluster, use `roksbnkctl cluster down`. |
| `cluster down` on **LegacySingle** | `this workspace is legacy single-state; cluster and BNK trial share one state. Use `roksbnkctl down` to tear down both, or migrate the state first` | Use `roksbnkctl down`. |
| `cluster down` on **Split** | ``BNK trial state exists in this workspace; run `roksbnkctl bnk down` first (or `roksbnkctl down` to tear down both phases)`` | Run `bnk down` first to remove the trial, then `cluster down` for the cluster — or `roksbnkctl down` to do both in one shot. |
| `cluster down` on **Empty** | `nothing to destroy in this workspace` | Nothing to do — the cluster hasn't been provisioned. |
| `down` on **Empty** | `nothing to destroy in this workspace` | Nothing to do — the workspace has no state. |
| `cluster up` on **LegacySingle** | ``this workspace was provisioned with v1.0.x single-state — its cluster lives in the trial state file. Use `roksbnkctl up` to operate on it, or migrate the state to two-phase shape first`` | Use `roksbnkctl up`. The cluster already exists in the trial state; applying the cluster phase separately would create a second one. |
| `bnk up` on **LegacySingle** | ``this workspace is legacy single-state; `bnk up` can't isolate the trial phase. Use `roksbnkctl up` for in-place behavior, or migrate the state first`` | Use `roksbnkctl up`. |

The "migrate the state first" references in two of the messages describe a future `roksbnkctl migrate` command that does not exist in v1.1.0. The refusals point at it so the wording stays valid once migrate ships; until then, the unscoped `up` / `down` is the working alternative for legacy workspaces.

## What survives a destroy

The contract: **`roksbnkctl` never destroys local state without explicit consent**, and never destroys cloud resources outside its Terraform state.

After a successful `down`:

| Survives | Where |
|---|---|
| Workspace config | `~/.roksbnkctl/<name>/config.yaml` |
| Workspace directory + state files | `~/.roksbnkctl/<name>/` (empty `state/`; `state-cluster/` untouched if `cluster down` not run) |
| OS keychain entry for the API key | per-workspace, named `roksbnkctl/<name>/ibmcloud_api_key` |
| `~/.kube/config` | left in place |
| The cluster (if only trial was destroyed) | runs and bills as before |
| The registry COS bucket's contents | FAR images, JWT licenses, schematic state — survive cluster destroy too if the bucket was created outside the bundled HCL |
| `~/.roksbnkctl/known_hosts` | SSH host keys persist; deleting a workspace does not clear them |

Re-running `up` against a `down`'d workspace re-creates everything from scratch. The workspace's `config.yaml` is preserved precisely so this re-create can use the same inputs without re-prompting.

The COS bucket point is worth highlighting: the bundled HCL provisions the COS instance but generally does not provision the buckets inside it (those are written by post-apply provisioners or by the BNK runtime itself). When `cluster down` destroys the COS instance, the bucket goes with it — but if the COS instance was created out-of-band (e.g. by a registered cluster's owner) and `roksbnkctl` is just attaching, then `cluster down` doesn't apply and the COS survives.

## `--auto` for non-interactive runs

All three destroy commands prompt for confirmation by default:

```
$ roksbnkctl down
This will destroy workspace "default"'s resources.
Continue? [y/N]: 
```

```
$ roksbnkctl bnk down
This will destroy the BNK trial for workspace "default". The cluster phase
will remain in place — run `roksbnkctl cluster down` to remove it too.
Continue? [y/N]: 
```

```
$ roksbnkctl cluster down
This will destroy the cluster phase for workspace "default" (ROKS + transit gateway + registry COS + cert-manager + jumphost).
Continue? [y/N]: 
```

`--auto` skips the prompt — required for CI / scripted pipelines:

```bash
roksbnkctl down --auto
roksbnkctl bnk down --auto
roksbnkctl cluster down --auto
```

`--auto` does **not** override the shape-based refusals (see [§"Refusal messages catalogue"](#refusal-messages-catalogue) above) — those are correctness guards, not confirmation prompts. If trial state is present, `cluster down --auto` still refuses; on a legacy single-state workspace, `bnk down --auto` and `cluster down --auto` still refuse.

## Like `up`, transient errors retry

`down` doesn't share `up`'s explicit retry-on-transient-error logic, but Terraform's destroy is naturally idempotent: re-running `down` after a partial destroy picks up where the previous run left off. If you see a transient network error during destroy, just re-run:

```bash
roksbnkctl down --auto
# (some resources destroyed, then transient error)

roksbnkctl down --auto
# (picks up where it left off, completes)
```

The same applies to `cluster down`. ROKS cluster destroy specifically can take longer than expected when the master is propagating its delete state — wait a few minutes and re-try if you see master-not-found errors.

## Cleaning up workspaces

A successful `down` leaves the workspace directory in place. You usually want to clean that up too:

```bash
roksbnkctl ws delete <name> --force
```

Two safety rails on `ws delete`:

- **Refuses to delete the current workspace.** Use the [parking-lot pattern](./06-workspaces.md#the-parking-lot-pattern) if you need to drop your current workspace.
- **Refuses if Terraform state still lists resources** (unless `--force`). Catches the case where you forgot to run `down` first.

The `--force` flag overrides both checks — but if you `ws delete --force` a workspace that still has provisioned cloud resources, you'll have leaked them. There's no auto-recovery; you'd need to find them via the IBM Cloud console and delete them by hand.

The full clean-as-you-go pattern from `scripts/e2e-test.sh` (Phase D destroys; Phase H parks and deletes):

```bash
# 1. Destroy the trial
roksbnkctl down --auto

# 2. Destroy the cluster phase
roksbnkctl cluster down --auto

# 3. Park the current-workspace pointer somewhere harmless
roksbnkctl ws new e2e-cleanup
roksbnkctl ws use e2e-cleanup

# 4. Now the original workspace is no longer current — safe to delete
roksbnkctl ws delete default --force

# 5. (Optional) clean up the parking lot too
roksbnkctl ws delete e2e-cleanup --force
```

Step 3-5 is the parking-lot pattern from [Chapter 6](./06-workspaces.md). It's specifically necessary when the workspace you want to delete is currently the active one — `ws delete` refuses to remove the current workspace because that would leave a dangling `current_workspace` pointer.

## Cost note: an undestroyed cluster keeps billing

ROKS clusters bill at roughly **$0.30/hour** per cluster + worker pool — call it $7/day for a 2-worker cluster, plus a few cents/day for the VPC / load balancers / COS / jumphost. A forgotten cluster can rack up real cost over a weekend.

To verify what's still running in your account:

1. **IBM Cloud console → Kubernetes → Clusters** — every cluster, billing or not.
2. **IBM Cloud console → VPC Infrastructure → VPCs** — networks left over after a partial destroy.
3. **IBM Cloud console → Resource list** — exhaustive view of everything in the account, filterable by RG.

If you find a leaked cluster from a past `roksbnkctl` run, the right move is to re-attach to it via `roksbnkctl cluster register <name>` and then `cluster down --auto` — `roksbnkctl` cleans up cleanly when it has the cluster in its state. Manually deleting via the console works too but leaves dangling VPCs and security groups that the bundled HCL would have cleaned up.

`roksbnkctl status` and `roksbnkctl cluster show` both report the cluster identity recorded in `cluster-outputs.json`, but they don't probe for "are there other clusters in this account?" — that's deliberately not their job. The IBM Cloud console is the canonical source of truth for what's billing.

## Workspace deletion ≠ destroy

A subtle but important distinction. `roksbnkctl ws delete` removes the **local** workspace directory and the OS-keychain API key entry. It does **not** destroy any cloud resources. If you `ws delete --force` without first running `down` / `cluster down`, the cloud resources keep running and you've lost the local Terraform state that `roksbnkctl` would use to destroy them.

In that scenario, recovery is:

1. Find the leaked cluster in the IBM Cloud console.
2. Recreate the workspace: `roksbnkctl init -w recovery`.
3. Register the existing cluster: `roksbnkctl cluster register <leaked-cluster-name>`.
4. Then run `roksbnkctl cluster down --auto` to destroy it cleanly.

The Terraform state is regenerated implicitly during register + plan; the resources `roksbnkctl` would otherwise have tracked get re-discovered through the IBM SDK lookups. It's not seamless, but it's recoverable.

The `ws delete` `--force` flag's "still has resources" check exists exactly to prevent this scenario — don't bypass it without thinking about the consequences.

## Worked example: register an existing cluster, deploy BNK, tear down

End-to-end Part III scenario: somebody on your team already provisioned a ROKS cluster manually via the IBM Cloud console (or via a different terraform tree); you need to deploy BNK on top of it using `roksbnkctl`, validate, and tear the whole thing down cleanly. The flow exercises [Chapter 9](./09-registering-existing-cluster.md), [Chapter 10](./10-deploying-bnk-trials.md), and this chapter end-to-end.

```bash
# 1. Workspace bootstrap — same as a fresh deploy
roksbnkctl init -w preexisting
# (answer prompts for region + resource group; pick the values matching
#  the existing cluster's location)

# 2. Register the already-running cluster into the workspace
roksbnkctl cluster register existing-bnk-cluster -w preexisting
# Expected:
#   → Discovering cluster "existing-bnk-cluster" via IBM Cloud API ...
#   ✓ Cluster ID: <crn>
#   ✓ Wrote ~/.roksbnkctl/preexisting/cluster-outputs.json
#   ✓ Fetched admin kubeconfig to ~/.kube/config (chmod 0600)

# 3. Verify roksbnkctl sees the cluster
roksbnkctl status -w preexisting
# Expected: cluster Ready, workers count, no BNK pods yet

# 4. Deploy BNK on top — `up` is idempotent over the existing cluster
roksbnkctl up --auto -w preexisting
# Expected: terraform applies the cert-manager + flo + cne_instance +
# license modules only; the roks_cluster module sees the cluster already
# exists and skips. ~10-15 min vs ~50 min for a from-scratch up.

# 5. Validate
roksbnkctl test -w preexisting
# Expected: green across connectivity + dns

# 6. Tear down — destroys the BNK overlay; the registered cluster survives
roksbnkctl down --auto -w preexisting
# Expected:
#   → terraform destroy (auto-approved)
#   Destroy complete! Resources: N destroyed.
#   ✓ Workspace "preexisting" state retained at ~/.roksbnkctl/preexisting/
```

The destroy count `N` is the BNK overlay + jumphost only — typically 30-40 resources, **not** the from-scratch ~77 count. `cluster register` is a discovery-only path: terraform state holds the overlay modules (`cert_manager`, `flo`, `cne_instance`, `license`) and the `testing` jumphost, but **not** the `roks_cluster` module, because the cluster pre-existed roksbnkctl. `down` destroys only what terraform knows about, so the registered cluster survives untouched.

If you also want to release the underlying cluster, you have to tear it down through whatever provisioned it originally (the IBM Cloud console, or the separate terraform tree your teammate used). `roksbnkctl cluster down` only works against clusters `roksbnkctl cluster up` created in the first place — see [Chapter 8](./08-cluster-phase.md) for the cluster-phase boundary.

The full register → up → test → down loop above is what Phase E + Phase H of the e2e plan exercise; see [Chapter 23](./23-e2e-test-plan.md) for the CI version.

## Cross-references

- [Chapter 6 — Workspaces](./06-workspaces.md) — `ws delete` mechanics and the parking-lot pattern.
- [Chapter 8 — The cluster phase](./08-cluster-phase.md) — what `cluster up` provisions and `cluster down` removes.
- [Chapter 9 — Registering an existing cluster](./09-registering-existing-cluster.md) — the `cluster register` mechanics the walkthrough builds on.
- [Chapter 10 — Deploying BNK trials](./10-deploying-bnk-trials.md) — what `up` provisions and `down` removes.
- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) — recovery from partial-destroy and orphan-state scenarios.
