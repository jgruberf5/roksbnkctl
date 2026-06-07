# Workspaces

A **workspace** is a per-environment bundle of config + state. The shape is modelled on `kubectl` contexts: you can have many of them, exactly one is "current" at a time, and a `-w` flag lets you address a specific one for a single command without flipping the pointer.

This chapter covers the on-disk layout, the everyday `init` / `use` / `list` flow, the full `roksbnkctl workspaces` command tree, the `-w` / `--workspace` override, and how creating or deleting a workspace moves the "current" pointer for you (so the old parking-lot dance is no longer needed).

> **Since `v1.9.0`, workspace selection follows you automatically.** Creating a workspace (`init` or `ws new`) makes it current; deleting the current workspace moves the pointer to another existing one, or clears it when none remain. There is no longer a phantom `default` fallback — with nothing selected, commands say so instead of guessing.

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

## `terraform.applied.tfvars` — what's deployed right now

`v1.4.0` adds a per-phase snapshot of the effective Terraform var-file inputs that produced the workspace's current state. After every successful `terraform apply` — `roksbnkctl cluster up`, `roksbnkctl bnk up`, or the legacy single-shape `roksbnkctl up` — `roksbnkctl` writes a canonical-HCL summary of "what var-files said" to the phase's state directory. Re-create / audit / handoff workflows that previously needed `config.yaml` (or memory) now have a file-on-disk record of the inputs.

### Where it lives

| Workspace shape | Phase | Path |
|---|---|---|
| `ShapeSplit` / `ShapeClusterOnly` | Cluster phase | `~/.roksbnkctl/<workspace>/state-cluster/terraform.applied.tfvars` |
| `ShapeSplit` | Trial phase | `~/.roksbnkctl/<workspace>/state/terraform.applied.tfvars` |
| `ShapeLegacySingle` | both phases (collapsed) | `~/.roksbnkctl/<workspace>/state/terraform.applied.tfvars` |

On `ShapeLegacySingle`, the file is a union of all sources (since the legacy shape doesn't separate cluster and trial state) and the header comment records `phase=legacy-single` so the reader doesn't mistake it for either a cluster-only or trial-only snapshot. See [PRD 07 §"Design"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/07-DEPLOYED-TFVARS.md#design) for the format spec.

### What it captures

A canonical HCL var-file: one assignment per line, variables sorted alphabetically within each source section. Each section is preceded by a comment line documenting which source contributed the values:

- `# === from config.yaml ===` — vars derived from the workspace's `config.yaml` (written to `terraform.tfvars` on disk).
- `# === from terraform.tfvars.user ===` — the workspace-local user override file. If the file doesn't exist, the section header is `# === from terraform.tfvars.user (missing) ===` and the body is empty.
- `# === from cluster-phase override ===` — `state-cluster/cluster-phase-override.tfvars` (cluster-phase snapshots only).

Source-attribution comments matter because the same variable can appear in multiple sources; the "winner" — the value Terraform actually used — is the last section to mention it. The comments let the reader trace why a particular value ended up live.

### Lifecycle

- **Written** after every successful `terraform apply`. Plan flows don't write the snapshot — the name `terraform.applied.tfvars` would mislead if a plan-time write existed.
- **Overwritten** each apply. If you want history, copy the file aside before re-running `up` or wire `restic` / a git commit hook against `~/.roksbnkctl/<workspace>/`.
- **Untouched by destroy.** `cluster down` / `bnk down` leave the prior `up`'s snapshot in place; that's what was last deployed. The file's mtime + the absence of Terraform state is the "torn down on `<date>`" signal.
- **Never read by `roksbnkctl` itself.** The snapshot is an output for the user — never an input the tool depends on. Making it an input would create a feedback loop where redacted values get written back as the literal string `<redacted>`.

### Redaction

Exactly one variable is redacted: `ibmcloud_api_key`. It's the only var whose value comes from the [cred resolver](./14-credentials-resolver.md) rather than being authored by the user in `config.yaml` or a tfvars file — so it's the only value the snapshot would expose that the user didn't put there themselves. See [PRD 04 §"Cred tmpfile-bind-mount pattern"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md#cred-tmpfile-bind-mount-pattern-docker-backend) for why the API key isn't in tfvars in the first place. The redacted line carries an inline comment:

```hcl
ibmcloud_api_key = "<redacted>"  # source: cred resolver, not persisted
```

For team-handoff scenarios (a teammate receives this file out-of-band and wants to re-create the workspace): replace the `<redacted>` value with the teammate's own API key, or simply remove the `ibmcloud_api_key` line so the [cred resolver](./14-credentials-resolver.md) supplies it from the teammate's own environment (keychain, shell env, `~/.bluemix/api_key`, etc.) at apply time. Every other line round-trips verbatim.

The file mode is `0600` regardless. The non-redacted contents (workspace identifiers, region, resource group, cluster name, tunable values) aren't credential-grade secrets, but aren't world-readable-grade either. Tight permissions are the cheap default.

### What it's **not**

- **Not an input** to subsequent applies. The `-var-file` chain on the next apply is unchanged: `config.yaml`-derived → `terraform.tfvars.user` → phase overrides.
- **Not a record of Terraform defaults.** If `variable "foo" { default = "bar" }` and the user never set `foo`, the snapshot omits `foo` entirely. Capturing defaults would require running `terraform output` against the variables block — separate concern.
- **Not a state-derived value capture.** Computed expressions, resource references, locals, and data-source values aren't var-file inputs and don't appear. `terraform console` against the live state dir is the right tool for those.
- **Not a `TF_VAR_*` env capture.** `roksbnkctl` doesn't set `TF_VAR_*` today — everything goes via `-var-file` — so the snapshot covers the complete input surface. A future cycle that starts using `TF_VAR_*` will need to extend this file.

### Safe-to-commit guidance

The file is suitable for git commit alongside `config.yaml` **after** the user verifies the redaction matches their threat model. The standard reminder applies: the workspace dir may contain other semi-sensitive material — `cluster-outputs.json` records the cluster's `crn` and admin identity hints; the `state/` and `state-cluster/` trees include `terraform.tfstate` (which contains resource IDs, IAM bindings, and any value Terraform's provider exposed); the `kubeconfig` files are mode `0600` for a reason. Review the whole workspace dir with the same lens before committing.

`roksbnkctl` does not touch `.gitignore`. If you commit the workspace, you commit the workspace; if you don't, you don't. The tool stays out of that decision.

### Worked example

For a `ShapeSplit` cluster phase apply, `~/.roksbnkctl/canada-roks/state-cluster/terraform.applied.tfvars` looks like:

```hcl
# Generated by roksbnkctl v1.4.0 at 2026-05-14T10:23:17Z after terraform apply on phase=cluster.
# Re-generated each apply. Do not edit by hand — your changes will be overwritten.

# === from config.yaml ===
cluster_name = "canada-roks"
ibmcloud_api_key = "<redacted>"  # source: cred resolver, not persisted
region = "ca-tor"
resource_group_name = "default"

# === from terraform.tfvars.user ===
worker_count = 4

# === from cluster-phase override ===
deploy_bnk = false
```

Re-applying from this snapshot alone reconstructs the inputs the user wrote; embedded Terraform module defaults are **not** captured (see [§"What it's **not**"](#what-its-not) above for the full list of what's out of scope).

The header records the binary version and apply timestamp so the reader can correlate the snapshot to a specific `roksbnkctl` invocation. Alphabetic ordering within each section means re-running `apply` with identical inputs produces a byte-identical file (idempotency — handy for diffing snapshots across applies).

## The everyday workspace routine

The minimum daily routine:

```bash
# Initialise (creates ~/.roksbnkctl/<name>/config.yaml and makes it current).
# With no -w and no current workspace, init asks for a name (defaults to "default").
roksbnkctl init

# Switch which workspace is "current"
roksbnkctl ws use prod

# See all workspaces and which one is current
roksbnkctl ws list
```

`roksbnkctl init -w <name>` is the one-shot path that creates the directory **and** populates `config.yaml` interactively — and selects it. Everything else (`ws new`, `ws use`, `ws delete`) is the deconstructed form for users who want finer-grained control. The `init` interview itself (which now lists your account's regions and existing clusters) is covered in [Chapter 7 — Quick start](./07-quick-start.md#step-2--roksbnkctl-init).

## Skip the interview: `init --var-file`

If you already have a `terraform.tfvars` in hand — from a prior run, a colleague's hand-off, or `cp terraform.tfvars.example terraform.tfvars && $EDITOR terraform.tfvars` — you don't need to retype its values into the interactive prompts. Pass the file to `init` and the interview is short-circuited for every field the file already answers:

```bash
roksbnkctl init -w myws --var-file ./terraform.tfvars
```

What that one command persists:

- **`~/.roksbnkctl/myws/config.yaml`** — seeded from the tfvars' values for the fields the interview asks about (`ibmcloud_cluster_region`, `openshift_cluster_name`, `openshift_cluster_version`, worker pool sizing, resource group, and the BNK-block knobs). Fields the file doesn't carry still prompt (or fall back to defaults), so a partial `terraform.tfvars` is fine. Since `v1.8.0`, `init --var-file` also sets a sanitized `prefix` (derived from the workspace name or the file's `openshift_cluster_name`) and a default all-create `resources:` block non-interactively, so the generated base is collision-safe — and your var-file still overrides any generated name via the layering below. The prefix-driven naming concept is [Chapter 13 §"Resource naming & collision avoidance"](./13-terraform-variables.md#resource-naming--collision-avoidance).
- **`~/.roksbnkctl/myws/terraform.tfvars.user`** — a verbatim copy of your var-file at the workspace root (sibling to `config.yaml`), mode `0600`. This is the workspace-persistent override the lifecycle auto-layers on every `up` / `plan` / `apply` / `down` for **both** the trial and the cluster phases. One file serves both — internally, `tf.Workspace.UserTFVarsPath()` resolves to `<workspace-dir>/terraform.tfvars.user` regardless of which phase opened the workspace.

### Why it matters: subsequent commands Just Work on bare `-w <ws>`

Once `init --var-file` has dropped the workspace-root `terraform.tfvars.user`, you can run the rest of the lifecycle without re-passing the var-file:

```bash
roksbnkctl up    -w myws --auto    # var-file already on disk; no flag needed
roksbnkctl plan  -w myws
roksbnkctl apply -w myws --auto
roksbnkctl down  -w myws --auto    # bare -w; no "No value for required variable" error
```

This closes the UX gap where a fresh workspace's first `up --var-file <path>` was followed later by a bare `down -w <ws>` that errored out because the var-file was nowhere on disk for the destroy. With `init --var-file`, the file is on disk from the moment the workspace exists.

Passing `--var-file <path>` on a later command still wins — the precedence chain is unchanged: rendered `terraform.tfvars` → `terraform.tfvars.user` (this section's deliverable) → caller's `--var-file` flags → phase overrides. To swap credentials or knobs for a single invocation, pass `--var-file ./alt.tfvars` on that one call; the persisted `terraform.tfvars.user` is untouched.

### Secrets on disk

The operator's `ibmcloud_api_key` from `./terraform.tfvars` lands at `~/.roksbnkctl/<ws>/terraform.tfvars.user`, mode `0600`. This is the same security posture as the repo-root `./terraform.tfvars` you copied from — just relocated under the workspace dir. The standard tfvars guidance applies (see [Chapter 13 §"The `IBMCLOUD_API_KEY` exception"](./13-terraform-variables.md#the-ibmcloud_api_key-exception)): if you'd prefer not to have the key on disk at all, omit `ibmcloud_api_key` from `./terraform.tfvars` before running `init --var-file`, and let the [cred resolver](./14-credentials-resolver.md) supply it from env / keychain / `~/.bluemix/api_key` at apply time. The persisted `terraform.tfvars.user` then carries every other knob but no credential.

### Diagnostics

If `roksbnkctl down -w <ws>` (no `--var-file` flag) errors with `No value for required variable …`, you're in one of two states:

1. **The workspace was `init`-ed without `--var-file`.** No `terraform.tfvars.user` was dropped, so the lifecycle has nothing to layer in for the required variable. Re-run `roksbnkctl init -w <ws> --var-file <path>` to seed the workspace-root copy and retry the `down`.
2. **The `terraform.tfvars.user` was removed.** Either by hand, by a `ws delete --force` that took the dir out from under you, or by an out-of-band cleanup. Re-run `roksbnkctl init -w <ws> --var-file <path>` to re-drop it, or pass `--var-file <path>` on the `down` for a one-shot fix.

The fastest verification that the file is in the right place: `ls -l ~/.roksbnkctl/<ws>/terraform.tfvars.user`. It should exist at mode `0600`. (Earlier development drafts of this feature wrote two copies into `state/` and `state-cluster/`; the shipped design uses one file at the workspace root that serves both phases — if you have stale copies in those state dirs from an early build, they're harmless and can be removed.)

## The full command tree

```bash
roksbnkctl workspaces ...     # canonical name
roksbnkctl ws ...              # alias
```

### `ws new <name>` — empty skeleton

Creates `~/.roksbnkctl/<name>/` with no `config.yaml`. Useful when you want the directory to exist (so `ws use` works) before you run `init`.

```bash
roksbnkctl ws new staging
# ✓ Created workspace "staging" and made it current (run `roksbnkctl init -w staging` to configure)
```

Creating the skeleton **selects it** (sets `current_workspace`), so the next bare command runs against it. Most users skip `ws new` and use `roksbnkctl init -w staging` directly, which creates, configures, **and** selects in one go.

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

Prints the current workspace name on stdout. If no pointer is set, prints a hint like "no current workspace; run `roksbnkctl ws use <name>` or `roksbnkctl init`" to **stderr** and exits 0 with empty stdout — so `WS=$(roksbnkctl ws current)` produces an empty string in scripts rather than spurious output.

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

Removes the workspace directory and the OS-keychain entry for its API key.

**You can delete the current workspace directly** — there's no "switch first" dance. If the workspace you delete was current, the pointer moves to another existing workspace (the alphabetically-first remaining one); if it was the last workspace, the pointer is cleared (there's no fallback `default` — see [§"The current-workspace pointer"](#the-current-workspace-pointer)).

One safety rail remains: **`delete` refuses if Terraform state lists provisioned resources** (unless `--force`). That catches the foot-gun where you forget to run `roksbnkctl down` first.

```bash
roksbnkctl ws delete staging
# Delete workspace "staging"? [y/N]: y
# ✓ Deleted workspace "staging"
# ✓ Current workspace is now "default"          # only printed when "staging" was current

# Refused — state still has resources
roksbnkctl ws delete prod
# Error: workspace "prod" has terraform-managed resources; pass --force to delete anyway

# I really mean it
roksbnkctl ws delete prod --force
# ✓ Deleted workspace "prod"
```

`--force` skips both the prompt and the state-non-empty check. Use it sparingly — there's no "undo" for `rm -rf ~/.roksbnkctl/<name>/`.

> **If a `down` errored partway** and left cloud resources stranded, the state guard will (correctly) block `ws delete`. Sweep the orphans with [`roksbnkctl cleanup`](./11-tearing-down.md#roksbnkctl-cleanup--recovering-from-a-failed-down) first, then delete.

## The current-workspace pointer

The pointer lives at `~/.roksbnkctl/config.yaml`:

```yaml
current_workspace: prod
```

Every command that doesn't pass `-w` reads this pointer. The workspace verbs keep it in step with reality, so you rarely set it by hand:

- **`init`** and **`ws new`** select the workspace they create — afterwards it's current.
- **`ws use`** rewrites it.
- **`ws delete`** moves it off a deleted current workspace (to another existing one), or clears it when the last workspace is removed.

**There is no fallback `default`.** When the pointer is empty — you've deleted every workspace, or never created one — a command run without `-w` reports `no workspace selected; create one with roksbnkctl init or pick one with roksbnkctl ws use <name>` rather than silently operating on a phantom `default`. (`roksbnkctl init` with no `-w` and no current pointer asks for a workspace name, defaulting to `default` on the first run — so the bootstrap experience is unchanged.)

If the pointer references a workspace whose directory is gone (e.g. someone `rm -rf`'d it by hand), commands report `workspace "prod" is not initialised; run roksbnkctl init first` — repoint with `ws use <other>` or recreate with `init`.

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

## Deleting the workspace you're in (the parking-lot dance is retired)

Earlier versions refused to delete the current workspace, so the end-to-end test used a **parking-lot pattern** — a throwaway workspace that existed only to hold the `current_workspace` pointer while you deleted the real one. That's no longer necessary: `ws delete` removes the current workspace directly and moves the pointer to another existing workspace (or clears it when none remain).

```bash
# End-to-end test cleanup — just destroy and delete, in any order
roksbnkctl down --auto
roksbnkctl ws delete default --force
# ✓ Deleted workspace "default"
# ✓ Current workspace is now "e2e-cleanup"      # whatever remained, alphabetically first

# Delete everything — the last delete clears the pointer
roksbnkctl ws delete e2e-cleanup --force
# ✓ Deleted workspace "e2e-cleanup"
# ✓ No workspaces remain; current workspace cleared
```

With no workspaces left, the pointer is empty and the next bare command reports `no workspace selected` (no phantom `default`); `roksbnkctl init` starts fresh.

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
| Throwaway short-lived clusters | `bnk-trial-N` workspaces; delete with `--force` after `down` (delete the current one directly — it auto-switches) |
| CI vs local dev | `dev` and `ci` workspaces; `ci` uses `IBMCLOUD_API_KEY` from env, `dev` uses keychain |
| Recover a half-torn-down env | [`roksbnkctl cleanup`](./11-tearing-down.md#roksbnkctl-cleanup--recovering-from-a-failed-down) sweeps stranded `<prefix>-*` cloud resources, then `ws delete` |

Workspaces are cheap. If a flow benefits from isolation, make a new one rather than fighting with `--var-file` overrides on the existing one.

## Forward-link to Chapter 12

This chapter covers the *workspace-as-a-unit*: how to create, switch, list, delete. The schema of the per-workspace `config.yaml` itself — every field, default, valid range — is [Chapter 12 — Workspace config](./12-workspace-config.md).
