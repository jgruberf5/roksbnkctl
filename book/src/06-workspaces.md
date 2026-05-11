# Workspaces

A **workspace** is a per-environment bundle of config + state. The shape is modelled on `kubectl` contexts: you can have many of them, exactly one is "current" at a time, and a `-w` flag lets you address a specific one for a single command without flipping the pointer.

This chapter covers the on-disk layout, the everyday `init` / `use` / `list` flow, the full `roksbnkctl workspaces` command tree, the `-w` / `--workspace` override, and the "parking-lot" pattern the end-to-end test uses to delete the workspace it's currently inside.

## The on-disk layout

Every workspace lives under `~/.roksbnkctl/<name>/`:

```
~/.roksbnkctl/
  config.yaml                          # global; current_workspace pointer
  known_hosts                          # SSH host keys (shared across workspaces)
  default/                             # workspace "default"
    config.yaml                        # this workspace's inputs
    cluster-outputs.json               # post-apply cluster identity (when present)
    state/                             # BNK trial state
      terraform.tfstate
      terraform.tfvars
      kubeconfig                       # admin kubeconfig (mode 0600)
      tf-source/                       # bundled HCL extracted to disk
      scratch/                         # docker bind-mounts, helm caches
    state-cluster/                     # cluster-phase state (separate tree)
      terraform.tfstate
      cluster-phase-override.tfvars
  prod/                                # workspace "prod"
    config.yaml
    state/
    ...
```

Three things are worth calling out:

- **`~/.roksbnkctl/config.yaml`** is *global* — non-secret user-wide preferences plus the `current_workspace` pointer. It is **not** a workspace config; the per-workspace files live one level deeper.
- **`state/` and `state-cluster/`** are intentionally separate so [`roksbnkctl cluster up`](./08-cluster-phase.md) and `roksbnkctl up` don't tangle their Terraform state. Most users won't touch either directly.
- **`cluster-outputs.json`** is the persisted identity of the workspace's ROKS cluster — written by `cluster up` or [`cluster register`](./09-registering-existing-cluster.md), read by `roksbnkctl up` so BNK trials don't have to re-state cluster identity in every tfvars.

Override the base directory with the `ROKSBNKCTL_HOME` env var. Test fixtures use this; everyday users shouldn't need it.

## The everyday workspace routine

The minimum daily routine:

```bash
# Initialise (creates ~/.roksbnkctl/<name>/config.yaml; defaults to "default")
roksbnkctl init

# Switch which workspace is "current"
roksbnkctl ws use prod

# See all workspaces and which one is current
roksbnkctl ws list
```

`roksbnkctl init -w <name>` is the one-shot path that creates the directory **and** populates `config.yaml` interactively. Everything else (`ws new`, `ws use`, `ws delete`) is the deconstructed form for users who want finer-grained control.

## The full command tree

```bash
roksbnkctl workspaces ...     # canonical name
roksbnkctl ws ...              # alias
```

### `ws new <name>` — empty skeleton

Creates `~/.roksbnkctl/<name>/` with no `config.yaml`. Useful when you want the directory to exist (so `ws use` works) before you run `init`.

```bash
roksbnkctl ws new staging
# ✓ Created workspace "staging" (run `roksbnkctl init -w staging` to configure)
```

Most users skip this and use `roksbnkctl init -w staging` directly, which does both steps in one go.

### `ws use <name>` — switch current

Sets the `current_workspace` pointer in `~/.roksbnkctl/config.yaml`:

```bash
roksbnkctl ws use prod
# ✓ Current workspace: prod

roksbnkctl ws current
# prod
```

Refuses to point at a non-existent workspace. The pointer is the only thing that changes — workspace state stays put.

### `ws current` — print the pointer

```bash
roksbnkctl ws current
# default
```

Prints the current workspace name on stdout. If no pointer is set, prints `(no current workspace; run `roksbnkctl ws use <name>` or `roksbnkctl init`)` to **stderr** and exits 0 with empty stdout — so `WS=$(roksbnkctl ws current)` produces an empty string in scripts rather than spurious output.

### `ws list` — table view

```bash
roksbnkctl ws list
NAME      CURRENT  REGION    CLUSTER          TF SOURCE
default   *        us-south  bnk-quickstart   embedded@v1.0.0
prod               eu-de     bnk-prod         embedded@v1.0.0
staging            us-south  bnk-staging      local:./terraform
```

The `*` marker on `CURRENT` highlights the active workspace. Other columns reflect each workspace's `config.yaml`. Rows where `config.yaml` is missing or unparseable still show the name, with the other columns blank — the list never errors out because of one corrupt workspace.

### `ws delete <name> [--force]`

Removes the workspace directory and the OS-keychain entry for its API key. Two safety rails:

1. **Refuses to delete the current workspace.** You'd be left with a dangling `current_workspace` pointer, so `delete` errors out with: `cannot delete current workspace "foo"; switch first: roksbnkctl ws use <other>`.
2. **Refuses if Terraform state lists provisioned resources** (unless `--force`). Catches the foot-gun where you forget to run `roksbnkctl down` first.

```bash
roksbnkctl ws delete staging
# Delete workspace "staging"? [y/N]: y
# ✓ Deleted workspace "staging"

# Refused — state still has resources
roksbnkctl ws delete prod
# Error: terraform state lists 77 resources; run `roksbnkctl down` first or pass --force

# I really mean it
roksbnkctl ws delete prod --force
# ✓ Deleted workspace "prod"
```

`--force` skips both the prompt and the state-non-empty check. Use it sparingly — there's no "undo" for `rm -rf ~/.roksbnkctl/<name>/`.

## The current-workspace pointer

The pointer lives at `~/.roksbnkctl/config.yaml`:

```yaml
current_workspace: prod
```

Every command that doesn't pass `-w` reads this pointer. `roksbnkctl init` writes it on first run (so the very first `init` makes `default` current automatically). `ws use` rewrites it. Nothing else touches it.

If the pointer references a workspace that doesn't exist (e.g. someone `rm -rf`'d the directory by hand), `roksbnkctl` errors out with a clear message: `workspace "prod" referenced by current_workspace does not exist; run roksbnkctl ws use <other>`.

## `-w` / `--workspace` for one-off overrides

Every command accepts `-w <name>` to override the current pointer for a single invocation:

```bash
# Doctor against "prod" without flipping the global pointer
roksbnkctl -w prod doctor

# Run init for a new workspace called "staging"
roksbnkctl init -w staging

# Get pods from the "default" cluster while currently on "prod"
roksbnkctl -w default k get pods -A
```

Use this when:

- You're scripting against multiple workspaces in a single run (CI runner that exercises `default` + `e2e-cleanup` back-to-back).
- You want to run a one-off command against a different environment without losing your current context.
- You're testing a fresh workspace before promoting it to current.

The flag only affects the running command — the pointer in `~/.roksbnkctl/config.yaml` is unchanged. After the command exits, the next bare `roksbnkctl` reads the original pointer.

## The parking-lot pattern

A subtle gotcha: `ws delete` refuses to remove the current workspace, but the end-to-end test suite needs to clean itself up after running against the `default` workspace.

The fix is the **parking-lot pattern**: have a throwaway workspace that exists only to be the "current" pointer while you delete other workspaces.

```bash
# End-to-end test cleanup (e2e-test.sh: Phase D destroys; Phase H runs the parking-lot dance below)

# Run the destroy against "default" (still current at this point)
roksbnkctl down --auto

# Park the pointer somewhere harmless
roksbnkctl ws new e2e-cleanup
roksbnkctl ws use e2e-cleanup

# Now we can drop the original workspace — it's no longer current
roksbnkctl ws delete default --force

# Optional: remove the parking lot too, by parking somewhere else first
roksbnkctl ws new tmp-park
roksbnkctl ws use tmp-park
roksbnkctl ws delete e2e-cleanup --force
roksbnkctl ws delete tmp-park --force   # leaves no current pointer
```

The pattern works because `current_workspace` only matters for commands that read workspace config. Once the pointer points elsewhere, the original workspace is just a directory and `delete` is happy to remove it.

If you want to delete *every* workspace including the parking lot, the last `delete` will leave you with an empty `current_workspace`. The next `roksbnkctl init` will populate it again with `default`.

## Using a workspace's environment in your shell

`roksbnkctl shell` drops you into a subshell with `KUBECONFIG`, `IBMCLOUD_API_KEY`, `IC_API_KEY`, and `IBMCLOUD_REGION` pre-loaded from the current workspace:

```bash
roksbnkctl shell
# (now in a subshell)
echo $KUBECONFIG
# /home/you/.roksbnkctl/default/state/kubeconfig
exit
# (back to the parent shell)
```

Same for `-w`:

```bash
roksbnkctl -w prod shell
```

Useful when you want to run host `kubectl` / host `oc` / arbitrary tools with the workspace context loaded. The Sprint 2 internalised verbs (`roksbnkctl k get`, etc.) read the same context automatically — you don't need to be in a subshell to use them.

## Common workspace patterns

A handful of patterns that come up in practice:

| Use case | Pattern |
|---|---|
| Different IBM Cloud accounts | `default` for personal, `acct-foo` for an account-specific key |
| Different regions | `us-south`, `eu-de` workspaces with distinct `cluster.name` values |
| Throwaway short-lived clusters | `bnk-trial-N` workspaces; delete with `--force` after `down` |
| CI vs local dev | `dev` and `ci` workspaces; `ci` uses `IBMCLOUD_API_KEY` from env, `dev` uses keychain |
| Parking-lot cleanup | `e2e-cleanup` workspace per "the parking-lot pattern" above |

Workspaces are cheap. If a flow benefits from isolation, make a new one rather than fighting with `--var-file` overrides on the existing one.

## Forward-link to Chapter 12

This chapter covers the *workspace-as-a-unit*: how to create, switch, list, delete. The schema of the per-workspace `config.yaml` itself — every field, default, valid range — is [Chapter 12 — Workspace config](./12-workspace-config.md).
