# Registering the cluster with BNK Forge

[BNK Forge](https://github.com/jgruberf5) is a separate platform — a FastAPI/React
app — for operating BIG-IP Next for Kubernetes across a fleet of clusters. When you run BNK Forge co-located with your ROKS deployments,
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
roksbnkctl -w acme-eu bnkforge status     # show config + readiness (url, session, cluster id)
```

`enable` turns on the post-`cluster up` hook; `register` is the on-demand path
(no re-`up` needed) for a cluster that already exists. Both talk to BNK Forge v3's
REST API directly — there is no external CLI to install; see
[Prerequisites](#prerequisites).

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
- **The Forge URL and credentials.** `BNK_FORGE_URL`, `BNK_FORGE_USER` and
  `BNK_FORGE_PASSWORD` (or `bnkforge.url` / `--url` / `--username`). The password is
  **never persisted** — the resulting session token is cached in the OS keychain, and
  reused until it expires. `roksbnkctl bnkforge status` shows what is resolved.

  On a terminal, `bnkforge register` prompts for anything missing. The automatic
  post-`up` hook runs **non-interactively**: with no cached token and no env
  credentials it declines with a one-line note rather than blocking a deploy on a
  prompt.
- **An IBM Cloud API key** resolvable for the workspace (the usual env / keychain /
  config chain — see [Chapter 14](./14-credentials-resolver.md)). It becomes the
  credential template Forge derives kubeconfigs from.

> **No external CLI.** Nothing is looked up on `PATH`, and there is no
> `~/.bnk-forge/config.json`. Earlier roksbnkctl versions shelled out to a v1
> `bnk-forge` binary; v3 has no such CLI, so `roksbnkctl` speaks to the REST API
> itself.

## The `bnkforge` commands

| Command | What it does |
|---|---|
| `bnkforge enable [--url U] [--project P]` | Persist `register: true` (+ optional overrides) so every `cluster up` registers the cluster. |
| `bnkforge disable` | Persist `register: false` — turn the auto-hook off. **Local only: it does not unregister anything.** |
| `bnkforge status` | Show the effective config (register / url / project), whether a Forge session token is cached, and whether a cluster id is recorded. |
| `bnkforge register [--url U] [--project P] [--force]` | Register the current workspace's cluster **now**, regardless of the opt-in. Surfaces errors (the auto-hook swallows them). `--force` takes over a cluster held by another Forge project — see [Registration is non-destructive](#registration-is-non-destructive). |
| `bnkforge unregister [--url U] [--project P]` | Remove the cluster from its Forge project — the inverse of `register`. Every "not there" case exits 0, so it is safe on a teardown path. |

`enable`, `disable`, and `register` all accept `--url` (override the Forge URL) and
`--project` (target Forge project). On `enable` those are **persisted**; on
`register` they're **one-off** overrides that aren't written to `config.yaml`.

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
| `url` | string | (`BNK_FORGE_URL`) | `bnkforge enable --url`. Falls back to the `BNK_FORGE_URL` env var. |
| `project` | string | (the workspace name) | `bnkforge enable --project`. Empty ⇒ a Forge project named after the workspace, created if absent. |

The block is additive — an absent `bnkforge:` block (any existing `config.yaml`)
loads unchanged.

## What happens on `cluster up`

> **Forge kubeconfig for module-based registration.** Independently of the
> credential-backed flow below, every `cluster up` also writes a portable,
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
2. **Reads the cluster identity** from `cluster-outputs.json` (cluster id, region,
   name). No recorded cluster id yet ⇒ skipped with a note.
3. **Resolves the IBM Cloud API key non-interactively** (env → keychain → config).
   This is what gets stored as the credential template, so Forge can re-derive the
   kubeconfig on demand instead of holding a perishable one.
4. **Authenticates to Forge over its REST API.** A cached session token from the OS
   keychain is reused when still valid; otherwise it logs in with
   `BNK_FORGE_USER` / `BNK_FORGE_PASSWORD` and caches the new token. The password
   itself is never persisted. The auto-hook runs **non-interactively**, so with no
   valid token and no env credentials it declines rather than blocking a deploy on
   a prompt.
5. **Ensures the credential template and the project**, then **registers the
   cluster** by id + region + template.

Sample output on a successful run:

```
→ Registering cluster "acme-eu-roks" with BNK Forge (https://forge.example.com)…
✓ Registered cluster "acme-eu-roks" with BNK Forge (project "acme-eu", forge cluster id 17).
```

### Registration is non-destructive

Re-registering used to DELETE any same-named cluster and re-POST it. Within one
project that was called idempotent, and very nearly is — except the **cluster id
changed**, so anything referencing it broke and any scan history attached to the old
id was discarded. Across projects it was worse: the lookup was project-scoped, so a
cluster held by *another* project was invisible, and the POST either moved it
silently or failed with a bare exit 1 that named nothing.

Since v1.42.0 ([#54](https://github.com/jgruberf5/roksbnkctl/issues/54)):

| The cluster name is held by | What happens |
|---|---|
| **this** project | Updated in place — **the cluster id is preserved** |
| **another** project | **Refused**, naming the owning project and both ids. `--force` takes it over |
| nobody | Created |

```console
$ roksbnkctl -w acme-eu bnkforge register
Error: cluster is registered to another BNK Forge project: "acme-eu-roks" is held by
project "platform-team" (id 93, cluster id 35). Re-registering would move it, changing
its cluster id and removing it from that project's view. Pass --force to take it over
deliberately, or unregister it there first
```

`--force` is a real **move** — the cluster is removed from the owning project and
re-created in yours, so its id changes. That is inherent to moving it, and is what
`--force` opts into.

> **The automatic post-`up` hook never forces.** An unattended step silently taking
> another project's cluster is precisely the harm the refusal exists to prevent. Only
> an explicit `roksbnkctl bnkforge register --force` can do it.

Two behaviours worth knowing when Forge is older or your account is narrowly scoped:

- **An older Forge build with no `PUT`** for the cluster resource falls back to the
  historical delete-and-recreate, so registration still works; the id changes, which
  is the cost of the older server. The fallback fires only on `404`/`405` — a
  transient `500` is reported as an error rather than escalated into a destructive
  retry.
- **A project you cannot read** produces a warning, not a failure. The cross-project
  scan is a second opinion on top of a direct query of *your* project; refusing to
  register because some other project was unreadable would block a least-privilege
  Forge account from its own work.

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

## Unregistering on teardown

`bnkforge disable` reads like the inverse of `register`, but it is not: it clears
the local `register` flag and **never contacts the server**. A cluster already
registered stays on Forge's Kubernetes page, in a project that outlives everything
it was created alongside, pointing at a cluster that may no longer have BNK on it —
or may no longer exist.

`roksbnkctl bnkforge unregister` is the actual inverse:

```bash
roksbnkctl -w acme-eu bnkforge unregister
✓ unregistered cluster "acme-eu" (id 13) from BNK Forge project "acme-eu"
```

**Absence is success.** A destroy runs when things are already half dismantled, and
may run twice, so each of these reports and exits 0 rather than failing:

| Situation | Output |
|---|---|
| No project of that name | `✓ no BNK Forge project "acme-eu" — nothing to unregister` |
| Project exists, cluster does not | `✓ cluster "acme-eu" is not registered in project "acme-eu" — nothing to do` |
| Cluster already deleted (404) | treated as done |

Only a genuine server failure surfaces as an error. Treating absence as failure
would make the command unusable in exactly the situation it exists for.

It also **never creates anything**. `register` ensures the project exists; a
teardown asking *"is this still here?"* must not bring it into being, or it would
leave behind the very thing being removed.

> **Registration is not removed automatically on `cluster down`.** The post-`up`
> hook registers, but there is no matching hook on the way down — run `bnkforge
> unregister` as part of your teardown (this is what the BNK Forge blueprints do in
> their destroy step).

### Doing it by hand against the REST API

There is no CLI to drive, but the flow is four ordinary REST calls, so it is
reproducible with `curl` when you need to see exactly what Forge receives. Look up
the cluster's identity first:

```bash
roksbnkctl -w acme-eu cluster show     # cluster_id, region, cluster_name
```

| Step | Call |
|---|---|
| 1. Log in | `POST /api/auth/login` → `{"token": …}` |
| 2. Credential template | `GET`/`POST /api/credential-templates` (IBM provider, your API key) |
| 3. Project | `GET`/`POST /api/projects`, then `PUT /api/projects/{id}` to set the ROKS/IBM platform |
| 4. Register | `POST /api/projects/{id}/k8s/clusters`, or `PUT /api/k8s/clusters/{id}` to update in place |

Step 3's `PUT` matters: without it Forge shows the project's platform as *Unknown*.
Step 4 is the one that changed in v1.42.0 — `roksbnkctl` prefers the in-place `PUT`
so the cluster id survives, and only falls back to `DELETE` + `POST` against a Forge
build that has no `PUT` route.

## TLS: pin the CA, don't disable verification

Forge installs commonly carry a self-signed certificate, and `--insecure` makes
that connect. What it costs is easy to miss: **the session token is sent on
every request**, so disabling verification leaves the connection encrypted but
*unauthenticated*. Anyone positioned on the path can present a certificate for
the Forge host, terminate TLS, and read the token. Encryption without
authentication protects the traffic from a passive observer and not from an
active one.

That would matter less if the setting were transient, but `bnkforge.insecure:
true` persists in `config.yaml` — it is typically set once for a lab and then
forgotten, including when the same workspace is later pointed at a production
Forge. So every request made with it prints a warning naming the host:

```
⚠ BNK Forge TLS verification is DISABLED for forge.lab.example.com:8443 — the API token
  is sent over a connection that is encrypted but NOT authenticated.
  Anyone able to intercept it can present any certificate for that host and read the token.
  Pin the Forge CA instead — `roksbnkctl bnkforge enable --forge-ca <file>` — then drop --insecure.
```

**Pin the CA instead.** For a self-signed Forge you generated the CA, so you
already hold it — and pinning it authenticates the connection rather than
abandoning authentication:

```bash
roksbnkctl bnkforge enable --url https://forge.lab.example.com:8443 --forge-ca ./forge-ca.pem
```

That stores the PEM as `bnkforge.ca_b64` (base64 only for single-line YAML
safety — a certificate is public data) and clears `insecure`. `--forge-ca` also
works as a one-shot on `bnkforge register` and `unregister`. When both are set
the pin wins and the certificate *is* verified.

Non-interactively, set `ROKSBNKCTL_BNKFORGE_CA_B64` (`base64 -w0 forge-ca.pem`).

`roksbnkctl bnkforge status` reports which of the three is in force:

```
tls:         verified (pinned CA from bnkforge.ca_b64)
tls:         verified (system roots)
tls:         NOT VERIFIED (insecure: true) — the API token is sent over an unauthenticated connection; pin a CA with `bnkforge enable --forge-ca <file>`
```

If the pinned CA is wrong, the connection fails at the handshake with an
`x509` error rather than silently falling back — which is the point.

## Behaviour notes

- **Best-effort — it never blocks or fails the deploy.** Whatever happens with
  Forge, `cluster up` succeeds if the cluster came up. Worst case you get a
  one-line note and register later with `roksbnkctl bnkforge register`.
- **Re-registering preserves the cluster id.** Running `cluster up` again on an
  unchanged cluster re-runs the hook harmlessly: the cluster is updated in place,
  so references to its Forge id and any scan history attached to it survive. Before
  v1.42.0 the id changed on every re-register
  ([#54](https://github.com/jgruberf5/roksbnkctl/issues/54)).
- **The auto-hook never forces.** A cluster held by another Forge project is left
  alone and the note says so; only an explicit `bnkforge register --force` moves it.
- **Registering later.** If it was skipped (no session, no credentials, no TTY), fix
  the prerequisite and run `roksbnkctl bnkforge register` — no re-`up` needed.

## Troubleshooting

- **`no BNK Forge URL (set bnkforge.url, BNK_FORGE_URL, or --url)`** — nothing told
  `roksbnkctl` where Forge lives. `roksbnkctl bnkforge status` shows what resolved.
- **`no BNK Forge username` / `no BNK Forge password`** — the cached session token
  is absent or expired and there is nothing to log in with. Set `BNK_FORGE_USER` and
  `BNK_FORGE_PASSWORD`, or run `roksbnkctl bnkforge register` at a terminal to be
  prompted. On a CI runner the env vars are the only path — there is no prompt to
  fall back to.
- **`cluster is registered to another BNK Forge project`** — the name is held by a
  different project; the message names it and both ids. Either unregister it there,
  or pass `--force` to move it deliberately (its cluster id changes, because a move
  re-creates it). This refusal is the point: silently taking another project's
  cluster is the harm.
- **`HTTP 403` / lacks permission to manage credential templates** — your BNK
  Forge session's role can't create or list credential templates. Log in as an
  operator/admin, or have one pre-create the template.

  A `403` on a *project's cluster list* is different and is **not** fatal: the
  cross-project ownership scan warns and continues, so a least-privilege account can
  still register into its own project.
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

## Two behaviours that exist because of how Forge drives this tool

The [Forge modules](https://github.com/jgruberf5/roksbnkctl-bnk-forge) run one
container per roksbnkctl phase, argv-only, each step re-running
`init --override-from-env --non-interactive` from a **curated environment** over
a deployment-scoped shared `/work`, against a **digest-pinned** runner image.
Two of roksbnkctl's checks are shaped by that, and both would otherwise break
blueprints:

**An unknown BNK line warns instead of refusing.** `manifest_version` is a
free-text input on the `bnk-install` module, the support matrix ships inside the
binary, and the image is pinned by digest — so a user selecting a release newer
than the pinned build would hit a hard refusal on a combination never known to be
wrong, with no action available to them. It warns and proceeds instead.

**An unset `network_mode` defers to the recorded cluster.** Each step's
environment carries only what that step needs, so a mode set by `cluster-create`
is simply absent when `bnk-install` regenerates `config.yaml`. Reading that
absence as a demand for `single-nic` would refuse a correct multi-NIC deployment
at its second module. Only an *explicit* contradiction is refused.

The practical rule for blueprint authors: **only the module that CREATES a
cluster needs to know the network mode.** Everything downstream reads the record.