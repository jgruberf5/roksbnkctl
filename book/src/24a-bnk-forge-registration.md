# Registering the cluster with BNK Forge

[BNK Forge](https://github.com/jgruberf5) is a separate platform — a FastAPI/React
app with its own `bnk-forge` CLI — for operating BIG-IP Next for Kubernetes across
a fleet of clusters. When you run BNK Forge co-located with your ROKS deployments,
`roksbnkctl` can hand each cluster it provisions straight to BNK Forge, so the
cluster shows up in the Forge fleet the moment `cluster up` finishes.

This is **opt-in** and **best-effort**: it never blocks or fails a deploy. If you
don't enable it (the default), nothing changes.

## Quick start

Three commands cover everything. None of them edits `config.yaml` by hand —
`enable` writes the opt-in for you, exactly like `registry target` or `targets
add` elsewhere in the tool:

```bash
roksbnkctl -w acme-eu bnkforge enable     # auto-register on every `cluster up`
roksbnkctl -w acme-eu bnkforge register   # register the current cluster right now
roksbnkctl -w acme-eu bnkforge status     # show config + readiness (CLI, session, cluster id)
```

`enable` turns on the post-`cluster up` hook; `register` is the on-demand path
(no re-`up` needed) for a cluster that already exists. Either way the actual work
is done by the `bnk-forge` CLI, which **ships with BNK Forge, not roksbnkctl** —
see [Prerequisites](#prerequisites).

## Why credential-backed

The naive way to register a cluster with an external platform is to hand it a
kubeconfig. ROKS admin kubeconfigs are short-lived — they carry an embedded token
that expires — so a stored kubeconfig goes stale and the platform loses access
until someone re-uploads a fresh one.

`roksbnkctl` registers **credential-backed** instead. It points BNK Forge at an
IBM Cloud **credential template** (your IBM Cloud API key, stored once in Forge)
plus the cluster's id and region. BNK Forge then **re-derives the kubeconfig on
demand** from that credential whenever it needs cluster access. Nothing
perishable is stored, so the registration doesn't go stale.

```
roksbnkctl                      BNK Forge
-----------                     ---------
cluster id + region   ───────▶  credential template (IBM Cloud API key)
                                        │
                                        ▼
                                derive kubeconfig on demand (never stale)
```

## Prerequisites

- **A co-located BNK Forge install** that supports IBM ROKS **credential-backed**
  registration — i.e. a Forge that can re-derive a ROKS kubeconfig from an IBM
  Cloud credential template. (This backend support is being added on a BNK Forge
  branch; an older Forge that only accepts a static kubeconfig won't work with
  this flow.)
- **The `bnk-forge` CLI on your `PATH`.** roksbnkctl doesn't build or bundle it —
  it's a separate script that ships with BNK Forge. roksbnkctl shells out to
  whatever `bnk-forge` is on `PATH`; if none is found, registration is skipped
  with a one-line note. `roksbnkctl bnkforge status` tells you whether it's found.
- **A BNK Forge session** — run `bnk-forge login` once so a session is stored in
  `~/.bnk-forge/config.json`. Without a stored session, roksbnkctl lets the CLI
  prompt you interactively (a separate, in-memory session); on a non-interactive
  run (no TTY) the registration is simply skipped.
- **An IBM Cloud API key** resolvable for the workspace (the usual env / keychain /
  config chain — see [Chapter 14](./14-credentials-resolver.md)). It's passed to
  the CLI so Forge can create a credential template if you don't have one yet.

## The `bnkforge` commands

| Command | What it does |
|---|---|
| `bnkforge enable [--url U] [--project P]` | Persist `register: true` (+ optional overrides) so every `cluster up` registers the cluster. |
| `bnkforge disable` | Persist `register: false` — turn the auto-hook off. |
| `bnkforge status` | Show the effective config (register / url / project), whether the `bnk-forge` CLI is on `PATH`, and whether a cluster id is recorded. |
| `bnkforge register [--url U] [--project P]` | Register the current workspace's cluster **now**, regardless of the opt-in. Surfaces errors (the auto-hook swallows them). |

`enable`, `disable`, and `register` all accept `--url` (override the Forge URL
the CLI would read from its stored session) and `--project` (target Forge project
id). On `enable` those are **persisted**; on `register` they're **one-off**
overrides that aren't written to `config.yaml`.

### What `enable` writes

`enable` writes a `bnkforge:` block into the workspace `config.yaml` for you (the
same marshaller every other roksbnkctl command uses — no hand-edit):

```yaml
bnkforge:
  register: true
  url: https://forge.example.com   # only if you passed --url
  project: "42"                    # only if you passed --project
```

| Field | Type | Default | Set by |
|---|---|---|---|
| `register` | bool | `false` | `bnkforge enable` / `disable`. `false` / omitted ⇒ no-op. |
| `url` | string | (CLI's stored-session URL) | `bnkforge enable --url`. Overrides the URL the CLI reads from `~/.bnk-forge/config.json`. |
| `project` | string | (CLI auto-selects) | `bnkforge enable --project`. Empty ⇒ the CLI uses the active/sole project, or prompts. |

The block is additive — an absent `bnkforge:` block (any existing `config.yaml`)
loads unchanged.

## What happens on `cluster up`

> **Forge kubeconfig for module-based registration.** Independently of the
> credential-backed CLI flow below, every `cluster up` also writes a portable,
> self-contained **cert-based** kubeconfig to `$ROKSBNKCTL_HOME/forge/kubeconfig.yaml`
> (one cluster entry with the public server + CA-if-any, and the cluster's admin
> client certificate/key). ROKS is OpenShift: its API server authenticates via
> client certs or OAuth tokens and **rejects raw IBM IAM bearer tokens (401)**, so
> the forge kubeconfig carries the admin client cert rather than a token. A BNK
> Forge that registers from a declared kubeconfig file can point at this path;
> roksbnkctl keeps it current automatically by re-fetching the admin kubeconfig as
> the certs near expiry (see [`kubeconfig --refresh`](./27-command-reference.md#roksbnkctl-kubeconfig)). This is separate from, and complementary to, the
> credential-backed `bnkforge register` flow documented below.

When `register: true`, `roksbnkctl` runs a post-apply hook right after it fetches
the admin kubeconfig (the same spot whether the apply made changes or was a
no-op). The hook is exactly what `bnkforge register` runs, with errors swallowed.
It:

1. **Checks the opt-in.** No `bnkforge.register: true` ⇒ it does nothing.
2. **Finds the `bnk-forge` CLI** on `PATH`. Not found ⇒ a one-line note, and the
   deploy proceeds normally.
3. **Reads the cluster identity** from `cluster-outputs.json` (cluster id, region,
   name). No recorded cluster id yet ⇒ skipped with a note.
4. **Resolves the IBM Cloud API key non-interactively** (env → keychain → config)
   and passes it through as `IBMCLOUD_API_KEY` so the CLI can create a credential
   template if needed. If it can't be resolved without a prompt, it's left to the
   CLI (which can use an existing template or ask).
5. **Invokes `bnk-forge clusters register`** with the cluster facts, passing your
   terminal through so any login / selection prompts work. The CLI then:
   - **Resolves auth** — reuses the stored `~/.bnk-forge/config.json` session if
     it's still valid; otherwise, if interactive, prompts for a separate login;
     otherwise (no TTY, no valid session) it skips.
   - **Selects or creates an IBM Cloud credential template** — uses an existing
     one if there's exactly one (or you pick), else creates one from the API key.
   - **Registers the cluster** by id + region + credential template (no static
     kubeconfig).

Sample output on a successful run:

```
→ Registering cluster "acme-eu-roks" with BNK Forge…
  Using stored BNK-Forge session (https://forge.example.com)
  Using IBM credential template 'roksbnkctl-eu-de' (id=3)
✓ Registered cluster acme-eu-roks (id=17) with BNK-Forge
  BNK-Forge will derive the kubeconfig on demand from the credential template.
```

## Registering on demand (no re-`up`)

`roksbnkctl bnkforge register` registers the current workspace's cluster right
now, without enabling the auto-hook or re-running `cluster up`. Use it to
register a cluster that was provisioned before you enabled the feature, or one you
[registered with an existing-cluster record](./09-registering-existing-cluster.md):

```bash
roksbnkctl -w acme-eu bnkforge register
roksbnkctl -w acme-eu bnkforge register --project 42   # one-off project override
```

It reads the cluster id + region from `cluster-outputs.json`, resolves your IBM
Cloud API key the usual way, and runs exactly what the post-`up` hook runs — the
only difference is that errors are **surfaced** rather than swallowed.

### Driving the `bnk-forge` CLI directly

`bnkforge register` is a thin wrapper around the `bnk-forge clusters register`
command, which you can also run yourself. Look up the cluster's id and region
from the recorded identity:

```bash
roksbnkctl -w acme-eu cluster show     # cluster_id, region, cluster_name
```

Then register it (this is exactly what the wrapper runs, minus the auto-resolved key):

```bash
bnk-forge clusters register \
  --name acme-eu-roks \
  --cluster-id cre6h4l20jjsg4kvt3a0 \
  --region eu-de \
  --provider IBM \
  --ibmcloud-api-key "$IBMCLOUD_API_KEY"
```

Preview the exact request without sending it:

```bash
bnk-forge clusters register \
  --name acme-eu-roks --cluster-id cre6h4l20jjsg4kvt3a0 --region eu-de \
  --dry-run
```

#### `bnk-forge clusters register` flags

| Flag | Required | Notes |
|---|---|---|
| `--name` | yes | The cluster name as it appears in BNK Forge. |
| `--cluster-id` | yes | The provider cluster id (the ROKS cluster id). |
| `--region` | yes | The cloud region (e.g. `eu-de`). |
| `--provider` | no | Cloud provider. roksbnkctl passes `IBM`. |
| `--project` | no | Target Forge project id. Else active / sole / prompt. |
| `--ibmcloud-api-key` | no | IBM Cloud API key for the credential template (or set `IBMCLOUD_API_KEY`). |
| `--resource-group` | no | IBM Cloud resource group for a newly-created template. Default `default`. |
| `--credential-template-id` | no | Use this existing template id; skip select/create. |
| `--credential-template-name` | no | Name for a newly-created template. Default `roksbnkctl-<region>`. |
| `--new-credential` | no | Force creating a new credential template. |
| `--url` | no | BNK Forge URL, overriding the stored session. |
| `--save-session` | no | Persist a fallback login to `~/.bnk-forge/config.json`. |
| `-y`, `--yes` | no | Non-interactive: auto-select when unambiguous, never prompt. |
| `--dry-run` | no | Print the registration request without sending it. |

roksbnkctl's wrapper supplies `--name`, `--cluster-id`, `--region`, `--provider
IBM`, and — when set — `--project` and `--url`, plus the resolved
`IBMCLOUD_API_KEY` in the environment.

## Behaviour notes

- **Best-effort — it never blocks or fails the deploy.** Whatever happens with
  Forge, `cluster up` succeeds if the cluster came up. Worst case you get a
  one-line note and register later with `roksbnkctl bnkforge register`.
- **Idempotent.** Re-registering a cluster that's already in Forge is safe — the
  CLI reports `already registered — leaving it as is` (the backend returns a
  conflict, which the CLI treats as a no-op). Running `cluster up` again on an
  unchanged cluster re-runs the hook harmlessly.
- **Registering later.** If it was skipped (CLI missing, no session, no TTY), fix
  the prerequisite and run `roksbnkctl bnkforge register` — no re-`up` needed.

## Troubleshooting

- **`the bnk-forge CLI is not on PATH`** — install BNK Forge's `bnk-forge` CLI
  (it doesn't come with roksbnkctl) and make sure it's on the `PATH` roksbnkctl
  sees. `roksbnkctl bnkforge status` confirms whether it's found.
- **`No valid BNK-Forge session and no TTY to prompt`** — run `bnk-forge login`
  once to store a session, then re-run. On a CI / non-interactive runner a stored
  session is required (there's no prompt to fall back to).
- **`Multiple IBM credential templates — pass --credential-template-id <id>`** —
  Forge has more than one IBM credential template and can't pick non-interactively.
  Run [`bnk-forge clusters register`](#driving-the-bnk-forge-cli-directly) with
  `--credential-template-id <id>` (or run it interactively to choose).
- **`HTTP 403` / lacks permission to manage credential templates** — your BNK
  Forge session's role can't create/list credential templates. Log in as an
  operator/admin, or have one pre-create the template and pass its id.
- **`no cluster id recorded in cluster-outputs.json yet`** — the cluster identity
  wasn't written. Run `roksbnkctl cluster register <name>` (or re-run `cluster
  up`) so `cluster-outputs.json` is populated, then register.

## Cross-references

- [Chapter 8 — The cluster phase](./08-cluster-phase.md) — where the post-apply
  hook fires.
- [Chapter 9 — Registering an existing cluster](./09-registering-existing-cluster.md)
  — how `cluster-outputs.json` (the cluster id + region the hook reads) gets
  populated for clusters `roksbnkctl` didn't provision.
- [Chapter 14 — Credentials and the resolver chain](./14-credentials-resolver.md)
  — how the IBM Cloud API key passed to Forge is resolved.
- [Chapter 28 — Configuration reference](./28-configuration-reference.md#bnkforge-block)
  — the `bnkforge:` field schema.
