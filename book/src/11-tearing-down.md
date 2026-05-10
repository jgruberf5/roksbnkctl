# Tearing down

`roksbnkctl down` and `roksbnkctl cluster down` are the destroy verbs — the inverse of [`up`](./10-deploying-bnk-trials.md) and [`cluster up`](./08-cluster-phase.md) respectively. This chapter covers what each one removes, the ordering constraint between them, what survives a destroy, the `--auto` flag for non-interactive runs, and the workspace-cleanup story.

The big rule, stated up front: **destroy in reverse of create**. Trial first (`down`), cluster second (`cluster down`). Destroying out of order leaves orphans the upstream HCL's resource graph can't unwind cleanly.

## The two destroys

There are two distinct teardowns matching the two phases:

### `roksbnkctl down` — destroy the BNK trial

Tears down everything `roksbnkctl up` created: the `flo` Helm release, `cne_instance`, the license module, cluster-side ServiceAccounts / RoleBindings / SCC bindings, and the null_resources that bootstrap admin tokens.

```bash
roksbnkctl down
```

What survives:

- The ROKS cluster itself
- cert-manager
- The registry COS instance and its bucket contents (FAR images, license artefacts)
- The TGW jumphost
- All cluster-phase Terraform state under `state-cluster/`
- The workspace's `config.yaml`

Roughly **41 resources destroyed** on a clean trial-only `down`. Time is dominated by Helm's pre-delete hooks and the cne_instance finaliser unwind — usually 2-5 minutes total.

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

`roksbnkctl cluster down` warns about this when run interactively — without `--auto` it prints a stderr line ("Any BNK trial state on top of this cluster will be orphaned — run `roksbnkctl down` first if needed.") and prompts `Continue? [y/N]`. With `--auto` the warning and prompt are skipped and the destroy proceeds; correctness becomes the user's responsibility. v0.8 does not yet inspect `state/terraform.tfstate` to refuse on a non-empty trial — that hard guard is tracked as a future improvement.

So in practice, **always run `down` before `cluster down`**, and do not skip the warning when running interactively.

The clean teardown sequence:

```bash
# 1. Destroy the BNK trial
roksbnkctl down --auto

# 2. Now safe to destroy the cluster phase
roksbnkctl cluster down --auto

# 3. (Optional) Delete the workspace itself
roksbnkctl ws delete <name> --force
```

If you `roksbnkctl up` against a registered cluster (one you didn't `cluster up` yourself), you skip step 2 — the cluster wasn't yours to destroy. Just `down` the trial and stop there, then optionally unregister by deleting `cluster-outputs.json`.

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

Both destroy commands prompt for confirmation by default:

```
$ roksbnkctl down
This will destroy workspace "default"'s resources.
Continue? [y/N]: 
```

```
$ roksbnkctl cluster down
This will destroy the cluster phase for workspace "default" (ROKS + transit gateway + registry COS + cert-manager + jumphost).
Any BNK trial state on top of this cluster will be orphaned — run `roksbnkctl down` first if needed.
Continue? [y/N]: 
```

`--auto` skips the prompt — required for CI / scripted pipelines:

```bash
roksbnkctl down --auto
roksbnkctl cluster down --auto
```

`--auto` does **not** override the trial-then-cluster ordering check; that's a correctness guard, not a confirmation prompt. If trial state is present, `cluster down --auto` still refuses.

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

## Cross-references

- [Chapter 6 — Workspaces](./06-workspaces.md) — `ws delete` mechanics and the parking-lot pattern.
- [Chapter 8 — The cluster phase](./08-cluster-phase.md) — what `cluster up` provisions and `cluster down` removes.
- [Chapter 10 — Deploying BNK trials](./10-deploying-bnk-trials.md) — what `up` provisions and `down` removes.
- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) (lands in Sprint 6) — recovery from partial-destroy and orphan-state scenarios.
