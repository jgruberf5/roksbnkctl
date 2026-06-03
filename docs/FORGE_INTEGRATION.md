# Forge Integration

`awsbnkctl` can register the clusters it provisions with **forge**, a GUI for
operating BNK deployments. The integration is a **write-only handoff**:
`awsbnkctl` operates AWS (via the AWS SDK) and reports the resulting cluster to
forge; forge then reads the cluster directly and renders it for operators.

This document describes what the handoff does, when it fires, how it talks to
forge, and how it is configured.

---

## 1 · The peer-read model

The cloud (AWS) is the single source of truth. Both `awsbnkctl` and forge read
it directly, each with its own credentials. They are peers on the read path, not
producer and consumer.

```
        [AWS — single source of truth]
             ▲                  ▲
             │ reads            │ reads (forge's own credentials)
             │                  │
        [awsbnkctl] ── register pointers ──► [forge] ──► [user GUI]
```

`awsbnkctl` owns the cluster lifecycle exclusively: `up`, `down`, and all state.
After provisioning, it calls forge to create the minimum records forge needs to
find the cluster — a **project** plus a **cluster** under it. From that point
forge reads cluster state on its own identity, renders the GUI, and lets
operators manage Kubernetes objects, Helm releases, and BNK custom resources on
the running cluster. Forge never touches the infrastructure lifecycle of an
awsbnkctl-deployed cluster.

Consequences:

- `awsbnkctl status` and `awsbnkctl doctor` always query AWS directly. They never
  ask forge "is my cluster healthy?", which would introduce a stale-cache class
  of bug.
- The kubeconfig `awsbnkctl` hands to forge is a **bootstrap seed** — a
  short-lived presigned credential on the operator's identity. Forge refreshes
  it on its own identity at first refresh; `awsbnkctl` never re-pushes it.
- `awsbnkctl` does not push any infrastructure state to forge. The handoff is
  metadata only (project + cluster pointers). There is no IaC layer, no state
  file, and nothing to mirror — `awsbnkctl` provisions through the AWS SDK and
  records what it created via AWS tags and its local IDs cache.

---

## 2 · When the handoff fires

Forge integration is **opt-in**, controlled by the `forge:` block in
`cluster.yaml`. When `forge.enabled` is false (or the block is omitted), the
provisioning path skips the handoff silently.

When `forge.enabled` is true:

- **On `up`**, the forge-registration phase fires once the EKS control plane is
  active — before the long BNK install — so forge's own polling surfaces install
  progress in the GUI during the longest part of provisioning. It creates the
  forge project and cluster, seeds the bootstrap kubeconfig, and writes a
  `forge_link.json` record into the cluster's state directory.
- **On `down`**, the same phase unregisters the cluster from forge by default.
  Pass `--keep-forge-link` to preserve the forge project record and the local
  link file instead.

`awsbnkctl` never auto-installs forge. If `forge.enabled` is true but forge is
unreachable, the soft-fail behaviour below applies.

---

## 3 · MCP-preferred, REST-fallback

Forge exposes both an MCP (Model Context Protocol) server and a REST backend.
`awsbnkctl` prefers MCP and falls back to REST:

- Registration and unregistration are attempted over **MCP first**.
- If a call hits an MCP **catalog gap** (a create/delete path not yet exposed as
  an MCP tool), `awsbnkctl` falls back to the corresponding forge **REST**
  endpoint. REST is the canonical fallback, not an exceptional path.
- For non-catalog-gap failures on `down` (auth, connectivity), the phase
  soft-fails and preserves the link for manual cleanup rather than blocking
  teardown.

The handoff itself is small: create project → create cluster (with the bootstrap
kubeconfig). An optional post-register scan can seed forge's view immediately
rather than waiting for its poll cadence.

The registration is **idempotent and keyed by the workspace link**. If a
completed `forge_link.json` already exists, re-running registration reuses it
after a sanity check rather than creating duplicates.

---

## 4 · Soft-fail with retry

Forge availability must never block expensive AWS provisioning. On `up`, the
registration phase wraps the attempt in a short retry loop with exponential
backoff. If every attempt fails:

- AWS infrastructure stays up.
- The phase writes `forge_link.json` with `status: pending` and returns success
  so `up` continues.
- The operator can recover later by running `awsbnkctl forge register` (or by
  re-running `up`), which sees the `pending` link and retries the handoff.

A completed link records `status: registered` (an empty status is treated as
registered for backward compatibility). The standalone
`awsbnkctl forge {register, status, unregister}` commands operate on the same
link file in the cluster's state directory.

---

## 5 · Configuration

The handoff is configured by the `forge:` block in `cluster.yaml`, validated by
the `ForgeSpec` in `internal/intent/cluster.go`:

```yaml
forge:
  enabled: true
  url: http://localhost:8000          # forge REST base
  mcpUrl: http://localhost:8081/mcp/  # forge MCP endpoint
  username: admin                     # REST login username
  credentialTemplateId: 1             # forge credential template to attach
  # password: ...                     # dev-only; prefer the env var below
```

| Field | Meaning | Default |
|---|---|---|
| `enabled` | Master switch. When false/omitted, the handoff is skipped. | `false` |
| `url` | Forge REST base URL. | `http://localhost:8000` |
| `mcpUrl` | Forge MCP endpoint. | `http://localhost:8081/mcp/` |
| `username` | Forge REST login username. | `admin` |
| `credentialTemplateId` | Forge credential template attached to the new project so forge can reach the EKS cluster. `0`/unset attaches none. | unset |
| `password` | Forge REST login password. **Dev-only** — discouraged in a checked-in file. | see below |

**Resolution overrides.** Environment variables and flags take precedence over
the YAML so secrets never need to live in the file:

- **URL:** `AWSBNKCTL_FORGE_URL` env → `forge.url` → default.
- **Username:** `forge.username` → `admin`.
- **Password:** `AWSBNKCTL_FORGE_PASSWORD` env → `forge.password` → the built-in
  dev default. When the built-in default is used, a one-line warning is emitted —
  set `AWSBNKCTL_FORGE_PASSWORD` for any real deployment.

Always supply the password via `AWSBNKCTL_FORGE_PASSWORD`; never write a real
password into `cluster.yaml`.

---

## 6 · The link file

A successful (or pending) registration writes
`.awsbnkctl/<cluster-name>/forge_link.json`, recording the forge project ID,
cluster ID, project/cluster names, the forge URLs, the registration timestamp,
and the status. This is what `awsbnkctl forge status` and
`awsbnkctl forge unregister` read to act on the registration, and what the
`up` / `down` phases use for idempotency.

The link file is written into the cluster's state directory and is git-ignored,
alongside `state.env` and the generated kubeconfig.

---

## 7 · Out of scope

- **Forge managing the infrastructure lifecycle of awsbnkctl clusters.** A
  permanent non-goal. `awsbnkctl up` / `down` own that lifecycle; forge has no
  view of it and never plans, applies, or destroys it.
- **Pushing any infrastructure state to forge.** The handoff is metadata only.
- **Forge-side health as authoritative.** `awsbnkctl status` / `doctor` always
  verify against AWS directly; forge is never the authority for cluster health.

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for the overall provisioning model.
