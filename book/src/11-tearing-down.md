# Tearing down

> **Three-phase note.** With the Cluster / BNK / Testing split there is now a
> fourth destroy verb, `roksbnkctl testing down` (destroys the jumphost rig,
> leaving the cluster and BNK intact), and the bare `roksbnkctl down` tears
> down **BNK || Testing in parallel, then the cluster**. `cluster down` refuses
> while **either** BNK or Testing state exists. The decision tree below is
> written for the earlier two-phase shape; see
> [Chapter 8a §"Teardown ordering and the `cluster down` guard"](./08a-three-phase-lifecycle.md#teardown-ordering-and-the-cluster-down-guard)
> for the three-phase ordering and guard.

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
```

The big rule, stated up front: **destroy in reverse of create**. Trial first (`bnk down`), cluster second (`cluster down`). The unscoped `roksbnkctl down` does this ordering for you — it runs the trial destroy first and then the cluster destroy. You don't have to think about ordering; `down` is the safe default.

The phase-scoped commands (`bnk down`, `cluster down`) are the precision tools — they let you keep one phase across many cycles of the other. They also **refuse loudly** if you ask them to do something that would orphan resources or that the shape doesn't allow. The full refusal catalogue is in [§"Refusal messages catalogue"](#refusal-messages-catalogue) below; the rule of thumb is that the error message always names the verb that would actually work.

## The three destroys

There are three teardown verbs matching the three slices of state:

### `roksbnkctl down` — shape-aware composite

The unscoped `down` is a **shape-aware composite**: it detects the on-disk shape of the workspace and dispatches to the right phase destroys in the right order.

```bash
roksbnkctl down
```

| Workspace shape | `down` does |
|---|---|
| Split (cluster + trial) | trial destroy → cluster destroy |
| ClusterOnly (only cluster applied) | cluster destroy |
| Empty | no-op success: `✓ Nothing to destroy in this workspace — no phase has any state.` |

This is the safe default — `down` always does the right thing regardless of shape.

### `roksbnkctl bnk down` — destroy the BNK trial only

Tears down everything the trial phase created — the `flo` / `cis` Helm releases, the `cne_instance` and `license` custom resources, and the cluster-side namespaces / Secrets / ServiceAccounts / RoleBindings / SCC bindings — and leaves the cluster running. These are all real terraform resources (`helm_release`, `kubernetes_*`, and `alekc/kubectl` `kubectl_manifest`), so `bnk down` is an ordinary `terraform destroy` that deletes the CRs finalizer-aware.

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

`bnk down` is a **no-op success** (exits 0, "nothing to do") on Empty and ClusterOnly workspaces — there's no trial state to destroy, so it leaves the cluster alone and returns cleanly. This lets a tool like bnk-forge call `bnk down` unconditionally in a reverse-order teardown without a missing-trial error blocking the cluster destroy. See [§"Refusal messages catalogue"](#refusal-messages-catalogue) for the exact text.

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

`roksbnkctl cluster down` enforces this ordering with a **hard refusal**: if the trial state has any resources in it, `cluster down` errors out and points you at `bnk down` (or `down`) instead. Even `--auto` won't bypass the guard, because correctness, not confirmation, is the issue. The full refusal text:

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

> **SSH key cleanup.** If `init` generated a testing SSH key and you accepted the
> prompt to copy it into `~/.ssh/`, `ws delete` removes those copied files too,
> so a generated key doesn't outlive its workspace. It deletes **only** the files
> `init` actually created (recorded in `resources.copied_ssh_key_files`) — a
> pre-existing `~/.ssh` key with the same name is never touched. The confirmation
> prompt names the files it will remove.

## `roksbnkctl cleanup` — recovering from a failed `down`

`terraform destroy` is not always clean. A transient IBM Cloud API error, a resource stuck behind a finaliser, or a `down` you `Ctrl-C`'d partway can leave **orphaned cloud resources** that Terraform no longer tracks — and a re-run of `down` may not finish the job. The classic symptom is a follow-up `up` that fails with `… name is not unique` because a half-deleted security group or VPC is still there.

`roksbnkctl cleanup` is the recovery tool. It sweeps your IBM Cloud account for every resource named after the workspace prefix (`<prefix>-*`) and deletes them in dependency order — regardless of what Terraform state says.

```bash
roksbnkctl cleanup            # scan, list, confirm, delete
roksbnkctl cleanup --dry-run  # scan + list only, delete nothing
roksbnkctl cleanup --auto     # skip the confirmation prompt
```

### What it deletes

Everything whose name is the workspace prefix or a `<prefix>-` child, across:

- **VPC (per region):** virtual server instances (jumphosts), floating IPs, public gateways, subnets, security groups, SSH keys, and VPCs.
- **Transit Gateway** (and its connections).
- The **registry COS** instance.
- The **ROKS cluster** itself.
- The **BNK trusted profile** (named after the cluster ID — found via `cluster-outputs.json`, so it's still identifiable after the cluster is gone).

Deletion runs in reverse-dependency order (instances → floating IPs → public gateways → subnets → security groups → VPCs → Transit Gateway → registry COS → cluster → trusted profile) so children go before their parents. It is **best-effort**: a failure on one resource is reported and the sweep continues. Re-run `cleanup` for anything blocked on an async delete (e.g. a VPC that can't go until its cluster finishes terminating).

### How the Transit Gateway is handled

A gateway cannot be deleted while anything is attached to it, so `cleanup` detaches its connections first, **waits for them to actually clear**, and only then deletes the gateway. The detach is asynchronous — IBM leaves the connection in `deleting` for a few seconds — and deleting the gateway during that window fails with:

```
412 Precondition Failed: Before you can delete this gateway,
you must delete all attached connections.
```

Which connections it may detach is a deliberate limit, not an oversight:

- A connection to a **VPC this same sweep is deleting** is yours; `cleanup` removes it.
- **Anything else** — a VPC under a different prefix, another account's network, a Direct Link or GRE attachment — belongs to someone else. `cleanup` **refuses the gateway** and names what is attached, rather than quietly disconnecting a shared gateway's other tenants.

Connections **mid-transition** are a third case, and the two directions are not the same:

| status | meaning | what `cleanup` does |
|---|---|---|
| `deleting` / `detaching` | **departing** — going away | waited for, whoever started it. It will be gone either way, so it is never a reason to refuse the gateway |
| `pending` | **arriving** — still attaching | ownership decides: **yours** is waited for; **someone else's** is refused straight away |

IBM accepts a connection `DELETE` only from a settled state, so acting on either would fail with `409 invalid_state`. The `pending` case matters more than it sounds: `cleanup` is most often reached for right after an interrupted `up`, which is exactly when a connection is still attaching. Sweeping then used to fail and advise a re-run that failed identically until IBM finished attaching — a wait the sweep now does itself.

Ownership is checked **before** that wait, because it cannot change as a connection settles. A gateway attached to a network outside the sweep is refused on the first listing rather than after the timeout.

If a connection never finishes attaching, the sweep says so rather than timing out twice on something it never had a chance to delete:

```
transit gateway f5orph-tgw still has a connection attaching after 5m0s:
conn-1 [vpc crn:…, pending] — IBM refuses a DELETE from `pending`, so this
has to finish before the gateway can go. Re-run once it reports `attached`
```

States that are *settled* — including `failed` and `suspended` — are detached normally. Waiting for `attached` specifically would mean a broken connection was never cleaned up, which is the opposite of what a sweep is for.

A refusal is not a transient, and an identical re-run will not clear it. **Check the region first**: `cleanup` scans only the workspace's cluster and client regions by default, so the commonest reason a VPC looks foreign is that it is yours and simply was not scanned. Re-run with `--all-regions` (or `--region <name>`) and it comes into scope. If the network really is someone else's, detach it yourself (`ibmcloud tg connection-delete <gateway-id> <connection-id>`) or delete it, then sweep again.

This matters most for the shared-gateway topology in [Sharing a Transit Gateway](./09a-transit-gateway-sharing.md), where one gateway deliberately outlives the clusters attached to it.

### Which regions it scans

By default `cleanup` scans the workspace's cluster region plus the testing-client region (from `config.yaml`'s [`resources.client_region`](./28-configuration-reference.md#resources-block) and `cluster-outputs.json`). If resources landed in a region not recorded in config, add it explicitly or sweep everything:

```bash
roksbnkctl cleanup --region ca-tor   # add a specific region to the scan
roksbnkctl cleanup --all-regions     # sweep every IBM Cloud region (slower)
```

### Caveats

- **It matches purely on the `<prefix>-` name convention.** If unrelated resources happen to share the workspace prefix, they match too — always review the `--dry-run` (or the pre-delete confirmation) list first.
- **It needs a prefix.** A workspace with no configured prefix has nothing to match; `cleanup` reports there's nothing to sweep.
- It does **not** touch Terraform state or `config.yaml`. After a successful sweep the state files still reference the now-gone resources; run `roksbnkctl ws delete <name> --force` to drop the workspace, or re-`up` to rebuild.

A typical recovery — a `down` errored, and `up` now refuses with "not unique":

```bash
roksbnkctl cleanup --dry-run   # confirm what's stranded
roksbnkctl cleanup --auto      # sweep it
roksbnkctl up --auto           # rebuild clean
```

## Refusal messages catalogue

The phase-scoped destroy verbs refuse loudly when the shape doesn't allow what you've asked for. Every refusal names the verb that would actually work. If you hit one in the wild, grep your terminal output for the message text and you should land here:

| Command + shape | Refusal text | Resolution |
|---|---|---|
| `bnk down` on **Empty** or **ClusterOnly** | *(not a refusal — exits 0)* `✓ No BNK trial state to destroy in this workspace — nothing to do.` | No-op success — no trial is deployed, so the command returns cleanly and leaves the cluster alone. If you want to destroy the cluster, use `roksbnkctl cluster down`. |
| `testing down` with **no jumphosts** | *(not a refusal — exits 0)* `✓ No testing jumphost state to destroy in this workspace — nothing to do.` | No-op success — the testing phase is optional, so `down` returns cleanly when nothing was provisioned. |
| `gateway down` with **no gateway state** | *(not a refusal — exits 0)* `✓ No gateway phase state to destroy in this workspace — nothing to do.` | No-op success — the gateway phase is opt-in, so `down` returns cleanly when it was never applied. |
| `cluster down` on **Split** | ``BNK trial state exists in this workspace; run `roksbnkctl bnk down` first (or `roksbnkctl down` to tear down both phases)`` | Run `bnk down` first to remove the trial, then `cluster down` for the cluster — or `roksbnkctl down` to do both in one shot. |
| `cluster down` on **Empty** | *(not a refusal — exits 0)* `✓ No cluster state to destroy in this workspace — nothing to do.` | No-op success — the cluster hasn't been provisioned. |
| `down` on **Empty** | *(not a refusal — exits 0)* `✓ Nothing to destroy in this workspace — no phase has any state.` | No-op success — no phase has any state. |
| any `down` on an **uninitialised workspace** | ``workspace "<name>" is not initialised; run `roksbnkctl init` first`` | A **real error**, unlike the empty case above. The two are indistinguishable to the state check but mean opposite things: a mistyped `-w` reporting a successful teardown is how someone concludes a cluster came down while it is still running. |

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

All three destroy commands prompt for confirmation by default. On a Split workspace the composite `down` takes a single up-front confirmation that names both phases, then runs trial → cluster without re-prompting — so you can't accidentally answer "yes" to the trial and miss the cluster gate:

```
$ roksbnkctl down
This will destroy BOTH the BNK trial AND the cluster phase for workspace "default" (ROKS + transit gateway + registry COS + cert-manager + jumphost).
Continue? [y/N]: 
```

On LegacySingle the monolithic prompt is unchanged (one state, one destroy):

```
$ roksbnkctl down                # LegacySingle shape
This will destroy workspace "default"'s resources.
Continue? [y/N]: 
```

```
$ roksbnkctl bnk down
This will destroy workspace "default"'s resources.
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

`--auto` does **not** override the shape-based refusals (see [§"Refusal messages catalogue"](#refusal-messages-catalogue) above) — those are correctness guards, not confirmation prompts. If trial state is present, `cluster down --auto` still refuses.

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

One safety rail on `ws delete`: it **refuses if Terraform state still lists resources** (unless `--force`) — which catches the case where you forgot to run `down` first. You **can** delete the current workspace directly; the pointer moves to another existing workspace (or clears when none remain), so you don't need to switch away first (see [Chapter 6](./06-workspaces.md#deleting-the-workspace-youre-in)).

The `--force` flag overrides the state check — but if you `ws delete --force` a workspace that still has provisioned cloud resources, you'll have stranded them. Recover with [`roksbnkctl cleanup`](#roksbnkctl-cleanup--recovering-from-a-failed-down): run it **before** deleting the workspace (it reads the prefix from `config.yaml`), or recreate the workspace's `config.yaml` with the same prefix and run it after.

The full clean-as-you-go teardown (`scripts/e2e-test.sh` Phase D destroys; Phase H deletes):

```bash
# 1. Destroy the trial
roksbnkctl down --auto

# 2. Destroy the cluster phase
roksbnkctl cluster down --auto

# 3. Delete the workspace — even if it's the current one (the pointer auto-switches)
roksbnkctl ws delete default --force
```

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

- [Chapter 6 — Workspaces](./06-workspaces.md) — `ws delete` mechanics and how the current-workspace pointer auto-switches on delete.
- [Chapter 8 — The cluster phase](./08-cluster-phase.md) — what `cluster up` provisions and `cluster down` removes.
- [Chapter 9 — Registering an existing cluster](./09-registering-existing-cluster.md) — the `cluster register` mechanics the walkthrough builds on.
- [Chapter 10 — Deploying BNK trials](./10-deploying-bnk-trials.md) — what `up` provisions and `down` removes.
- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) — recovery from partial-destroy and orphan-state scenarios.
