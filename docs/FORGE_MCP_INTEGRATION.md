# Forge MCP Integration — Plan

**Status:** stable. P1 + P2 shipped (PR #1, PR #2). MCP-only (Option B); REST fallback eliminated by `bnk-forge#114`.
**Owner:** awsbnkctl maintainers
**Companion repo:** `bnk-forge-v2` (localhost dev at `http://localhost:8000`; MCP at `http://localhost:8081/mcp/`)

## 0 · Architecture: peer-read model (load-bearing)

**The cloud (AWS / Azure / GCP) is the single source of truth.** Both `*bnkctl` tools and forge read it directly, each using its own credentials. They are peers on the read path, not producer/consumer.

```
       [cloud — single source of truth]
            ▲                  ▲
            │ reads            │ reads (its own auth via cloud_auth MCP module)
            │                  │
       [*bnkctl] ── register pointers ──► [forge] ──► [user GUI]
```

### Forge plays two different roles depending on who deployed the infrastructure

| Source of the deployment | Forge's role | Operations forge performs |
|---|---|---|
| Forge's own **blueprint** | Full IaC manager | terraform plan / apply / destroy, project-module lifecycle, state ownership |
| `*bnkctl` (this tool, sibling tools) | **GUI + k8s/BNK CRUD only** | Read cluster state; manage k8s objects, BNK CRDs, Helm releases on the running cluster. **Never** touches the IaC layer of these deployments. |

What `*bnkctl` uniquely does: provision the deployment with its own tofu/terraform run (forge has no visibility into this), then call MCP to **create the minimum records forge needs to find the cluster** — project + cluster (+ optional cloud-auth credential template). After that handoff, forge reads cluster state directly and CRUDs k8s/BNK objects. `*bnkctl` retains exclusive ownership of `up` / `down` / state. There is no `create_project_module` call for awsbnkctl's TF modules: forge has no IaC-layer view of them, by design.

Consequences:
- `awsbnkctl status` / `awsbnkctl doctor` always query AWS. They never ask forge "is my cluster healthy?" — that would introduce a stale-cache class of bug.
- The kubeconfig `awsbnkctl` pushes to `create_cluster` is a **bootstrap seed** (a 15-min presigned STS URL on our identity). Forge swaps it via `refresh_kubeconfig` using its own identity at first refresh; we never re-push.
- Sibling tools (`azurebnkctl`, `gcpbnkctl`, etc.) follow the same pattern in their own clouds.

## 1 · What we're trying to do

After `awsbnkctl up` finishes provisioning AWS infra + EKS + BNK, **register the scaffolding records** forge needs to find and read the deployment. The handoff happens over forge's **MCP server**.

Concretely, after a successful `up`, awsbnkctl will:

1. Create a forge **project** (metadata: name, region, labels).
2. Register the **EKS cluster** under that project, seeding a short-lived kubeconfig forge will refresh on its own identity.
3. (Optional) Trigger a forge **scan** to populate the operator-facing view promptly rather than waiting for forge's own poll cadence.

From that point forward, forge reads the cluster directly on its own credentials, renders the GUI, and lets the operator CRUD k8s / BNK objects. `awsbnkctl` retains exclusive ownership of the IaC layer (terraform plan / apply / destroy); forge never touches it.

---

## 2 · Forge MCP surface — what's available today

Read directly from `bnk-forge-v2/mcp-server/tools/mcp_tool_catalog.json` (69 tools, ≥`pre-2.10.71`).

| Module | Tools | What we need from it |
|---|---|---|
| `bnk_operations` (19) | `bnk_health`, `bnk_gateway_topology`, `bnk_data`, `bnk_upgrade_*`, `bnk_license_status`, `bnk_recovery_*` | Post-handoff day-2: forge reads BNK directly once cluster is registered. |
| `cluster_management` (14) | `list_clusters`, `get_cluster`, `scan_cluster`, `test_cluster_connectivity`, `list_namespaces`, `list_resources`, `get_pod_logs`, … | After registration: forge surfaces the cluster + scans for capabilities. |
| `iac_operations` (15) | `create_project`, `get_project`, `list_projects` | We only call the project-creation paths. We do NOT call `project_plan` / `project_apply` / `project_destroy` / `create_project_module` against awsbnkctl-deployed infra — those are reserved for forge's own blueprints. |
| `helm` (11) | `helm_list_releases`, `helm_get_release`, `helm_get_values`, `helm_install`, `helm_upgrade`, `helm_rollback`, `helm_uninstall` | Verify FLO / CIS / CNEInstance helm releases that awsbnkctl installed. |
| `config_management` (4) | `config_export`, `config_diff`, `config_import`, `config_promote` | Snapshot BNK config; promote between environments later. |
| `system` (6) | `system_health`, `system_version`, `system_settings`, `audit_log`, `system_queue_metrics`, `list_users` | Pre-flight; verify forge is reachable + at compatible version. |

### Gaps (mutating REST endpoints not yet in MCP catalog)

These are the create-paths we need but which haven't been promoted to MCP tools yet. We'll call them as REST in v1 and propose MCP-catalog additions as a follow-up PR against `bnk-forge-v2`.

| Operation | REST endpoint | MCP catalog status |
|---|---|---|
| Create project | `POST /api/projects` | not exposed |
| Create cluster under project | `POST /api/projects/{project_id}/k8s/clusters` | not exposed |
| Login (token) | `POST /api/auth/login` | n/a (out-of-band) |

(`create_project_module` and `detect_eks_clusters` exist in PR #114 but **awsbnkctl does not call them** — see § 0 two-roles framing. They're for forge's own blueprint workflows.)

---

## 3 · Data model mapping (awsbnkctl → forge)

```
awsbnkctl workspace                    forge
                                       ├── project (created via MCP)
                                       │     • name:   awsbnkctl-<workspace>
                                       │     • region: <aws-region>
                                       │
                                       └── cluster (created under project via MCP)
EKS cluster (live in AWS)          →           • name:   <eks-cluster-name>
                                               • region: <aws-region>
                                               • kubeconfig: bootstrap seed (forge refreshes)
                                               │
                                               └─► forge reads from here:
                                                   • k8s objects via the kubeconfig
                                                   • helm releases (flo, cis, cert-manager, cneinstance)
                                                   • BNK CRDs (via scan)
                                                   ALL using forge's own credentials
```

**Project name convention:** `awsbnkctl-<workspace-name>` (e.g. `awsbnkctl-default`, `awsbnkctl-aws-syd-test`).

**Cluster slug:** `<aws-region>-<eks-cluster-name>` (e.g. `us-east-1-bnk-prod`).

**Note what is NOT in this mapping:** the workspace's terraform modules (eks, sriov-nodegroup, irsa-*, ecr-mirror, s3-supply-chain) have no corresponding entry in forge. The IaC layer is invisible to forge — `awsbnkctl` owns it exclusively.

---

## 4 · End-to-end handoff sequence

The integration runs as the final step of `awsbnkctl up` (gated behind a new flag — see §6). All calls go to `${AWSBNKCTL_FORGE_URL:-http://localhost:8000}`.

```
┌─────────────────────────────────────────────────────────────────────┐
│  awsbnkctl up  (existing flow: TF apply → EKS → IRSA → BNK helm)    │
│       │                                                              │
│       ▼                                                              │
│  awsbnkctl forge register   ← NEW subcommand (also runnable          │
│       │                       standalone post-`up`)                  │
│       ▼                                                              │
│  1. POST /api/auth/login                  →  bearer token            │
│  2. POST /api/projects                    →  project_id              │
│  3. POST /api/projects/{id}/k8s/clusters  →  cluster_id              │
│         (with a 15-min presigned-STS kubeconfig as bootstrap seed)   │
│  4. MCP scan_cluster(cluster_id)          →  populate namespaces +   │
│         (optional)                            helm releases + BNK    │
│  5. MCP bnk_health(cluster_id)            →  smoke-test the handoff  │
│         (optional)                                                   │
│  6. Write forge_link.json to workspace dir:                          │
│         { project_id, cluster_id, forge_url, registered_at }         │
└─────────────────────────────────────────────────────────────────────┘
```

After step 6, the operator opens forge at `http://localhost:8000/projects/<id>` and sees the cluster. Forge reads it directly using its own AWS credentials (configured via the `cloud_auth` MCP module out-of-band — not part of this handoff).

---

## 5 · How awsbnkctl talks MCP

Two options. **Recommendation: Option A** (lower complexity for v1).

### Option A · awsbnkctl as plain HTTP client (recommended for v1)

Forge's MCP server is a thin facade over the REST backend (`mcp-server/src/bnk_forge_mcp/client.py` proxies tool calls to the same REST routes). So in practice awsbnkctl can hit the REST routes directly with a bearer token, without speaking MCP protocol at all.

- **Pro:** no MCP client dependency in Go; one HTTP client covers both "REST gaps" and "tools that happen to also exist in MCP".
- **Pro:** identical to how forge's own frontend talks to its backend.
- **Con:** doesn't exercise the MCP transport, so we don't catch MCP-only regressions.

### Option B · awsbnkctl as MCP client

Bring a Go MCP client library (e.g. `github.com/modelcontextprotocol/go-sdk` once stable, or a hand-rolled JSON-RPC over stdio/HTTP). Call MCP tools by name where available; REST-fallback for gaps.

- **Pro:** future-proof — if forge promotes more endpoints to MCP, we get them for free by switching the call site from REST to MCP-tool-name.
- **Pro:** lets ops chains (Claude Code, other MCP clients) reuse the same tool definitions awsbnkctl uses.
- **Con:** MCP Go ecosystem is still pre-1.0; adds a moving dependency.
- **Con:** auth flow over MCP is less well-defined than REST + bearer.

**Decision criterion:** if forge ships an MCP `register_cluster` tool in the next release, switch to Option B. Until then, Option A.

---

## 6 · CLI surface (awsbnkctl side)

New subcommand, three verbs:

```
awsbnkctl forge register     # one-shot: register current workspace into forge
awsbnkctl forge status       # show forge_link.json + ping MCP/REST
awsbnkctl forge unregister   # remove cluster + project from forge (idempotent)
```

Flags:

```
--forge-url string        forge base URL (default $AWSBNKCTL_FORGE_URL, fallback http://localhost:8000)
--forge-user string       username (default admin)
--forge-password-env      env var to read the password from (default AWSBNKCTL_FORGE_PASSWORD)
--forge-token string      pre-obtained bearer token (bypasses login)
--project-name string     forge project name (default awsbnkctl-<workspace>)
--dry-run                 print the planned API calls without executing
```

**Auto-trigger:** `awsbnkctl up` accepts `--register-with-forge` (also `workspace.yaml: forge.auto_register: true`) to run `forge register` as the final step. Default is **off** in v1 — opt-in to keep the existing offline flow intact.

---

## 7 · State + auth handling

- **Bearer token:** obtained once per `awsbnkctl forge` invocation via `POST /api/auth/login` with `{username, password}` (returns `{token: "..."}`). Token is held in-memory only — never written to disk. Tokens currently expire after ~hours; awsbnkctl re-logs in for each invocation.
- **Workspace link:** `<workspace-dir>/forge_link.json` records `{project_id, cluster_id, forge_url, registered_at, forge_version}`. This is what `forge status` and `forge unregister` read. Versioned alongside `terraform.tfstate` bundles.
- **TF state adoption:** awsbnkctl does NOT push tfstate to forge. Per the peer-read architecture (§ 0), forge reads state from the cloud directly using its own credentials. The handoff is metadata only (project + cluster + module paths); state is not mirrored.
- **Idempotency:** all writes are keyed by `(project_name, cluster_slug)`. Re-running `forge register` on an already-registered workspace updates rather than duplicating.

---

## 8 · Phasing

| Phase | Scope | Deliverable | Status |
|---|---|---|---|
| **P0 — design** *(this doc)* | Define mapping, flow, CLI surface | This file | ✅ 2026-05-18 |
| **P3 — MCP catalog gap-fill** | PR against `bnk-forge-v2` adding the missing endpoints | `bnk-forge#114` (initial 4 + Tier A–E = 62 tools; new `cloud_auth` governed module) | ✅ 2026-05-18 (PR open against `staging`) |
| **P1 — MCP integration (Option B, skipped REST)** | `awsbnkctl forge {register, status, unregister}` over MCP transport | New `internal/forge/` + `internal/forge/mcp/` Go packages; `internal/cli/forge.go` Cobra subcommand | ✅ 2026-05-18 (`feat/forge-mcp-integration` branch) |
| **P2 — auto-register on `up`** | `--register-with-forge` flag | `awsbnkctl up --register-with-forge` calls `forge register` after a successful apply (no-op in `--dry-run`). | ✅ 2026-05-18 (PR #2 merged at `d03bc03`) |
| **P5 — observability hooks** | `awsbnkctl doctor` gains a forge-reachability row (separate from AWS rows); `awsbnkctl status` optionally pushes a refresh to forge via `--push-to-forge`. Both stay on AWS for their actual verdict — forge is never authoritative for cluster health per the peer-read thesis (see § 0). | doctor row + optional status push | ⏳ deferred |

P0 + P3 + P1 + P2 shipped in one session — Option A (REST) was leapfrogged because PR #114 made the MCP surface complete enough to skip it entirely.

---

## 9 · Testing strategy

- **Unit tests** (`internal/forge/*_test.go`): mock REST client; verify request bodies, idempotency keys, error mapping.
- **`forge register --dry-run`**: prints the planned API calls (`POST /api/projects {...}`, etc.) without executing — covered by a golden test.
- **Localhost E2E** (`scripts/forge-e2e.sh`): given a running `bnk-forge-v2` localhost stack (`make install` in that repo) + a dry-run `awsbnkctl up` workspace, exercise `forge register` end-to-end and assert forge's `/api/projects/{id}` reflects the expected shape. Skipped in CI unless `FORGE_E2E=1` is set.
- **Spike integration**: when an operator runs the real-AWS spike against a fresh cluster, `forge register` is part of the spike checklist (covers the spike-to-production transition).

---

## 10 · Open questions

These don't block writing the code, but worth resolving before P1 lands.

1. **Cluster identity collision.** What happens if two awsbnkctl workspaces register the same EKS cluster name under different projects? *Probably an error from forge's unique constraint — define UX.*
2. **EKS auth flow handoff.** awsbnkctl pushes a 15-min presigned-STS kubeconfig as bootstrap seed; forge refreshes via `refresh_kubeconfig` using its own AWS credentials. Confirm forge has its own AWS auth configured (via the `cloud_auth` MCP module) BEFORE the bootstrap seed expires — otherwise the first refresh fails and the operator has to recover manually.
3. **Multi-tenant credentials.** Localhost forge runs as admin/changeme. In a shared / prod forge, awsbnkctl needs scoped creds. Define credential provisioning UX (likely: forge issues a service-account API key per workspace).
4. **`forge unregister` semantics.** Sever-the-link (cluster delete only) vs full purge (cluster + project). Implemented today as "sever by default, `--purge` for full delete." Consider whether `down` should auto-unregister on success.

---

## 11 · Out of scope (explicit)

- **Forge managing the IaC layer for awsbnkctl deployments.** Permanent non-goal. Forge has no `project_module` record of awsbnkctl's TF modules, no state, no plan/apply/destroy access. `awsbnkctl up` and `awsbnkctl down` own that lifecycle exclusively. Forge's IaC capabilities (`project_plan` / `project_apply` / `project_destroy` / `create_project_module`) are reserved for forge's own blueprint workflow.
- **Pushing tfstate or any IaC state to forge.** Doc once contemplated `project-module` records with adopted state bundles; that was the wrong model. See § 0.
- **Multi-cloud parity in this repo.** AWS-only. Sibling tools (`azurebnkctl`, `gcpbnkctl`, …) follow the same pattern in their own clouds; the cross-cloud abstraction surfaces in forge, not here.
- **Replacing `awsbnkctl test` with forge-side tests.** Some overlap (forge has `test_cluster_connectivity`), but `awsbnkctl test dns` / `awsbnkctl test bandwidth` stay in the CLI because they need workspace-local context.
- **MCP server *exposed by awsbnkctl*.** That would be Option B-prime: awsbnkctl-mcp surfaces awsbnkctl's own commands as MCP tools so `claude` agents could orchestrate awsbnkctl + forge over MCP. Worth doing eventually, not required for the integration this doc describes.

---

## 12 · References

- Forge MCP catalog: `bnk-forge-v2/mcp-server/tools/mcp_tool_catalog.json` (69 tools)
- Forge MCP server source: `bnk-forge-v2/mcp-server/src/bnk_forge_mcp/`
- Forge cluster routes: `bnk-forge-v2/backend/routes/k8s/clusters.py`
- Forge IaC routes: `bnk-forge-v2/backend/routes/` (projects, project-modules, stacks)
- Localhost dev creds: `admin` / `changeme` at `POST http://localhost:8000/api/auth/login`
- awsbnkctl CLI lifecycle: README + [`docs/POST_TERRAFORM_DIRECTION.md`](POST_TERRAFORM_DIRECTION.md)
