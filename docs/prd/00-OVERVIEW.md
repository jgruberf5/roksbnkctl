# PRD 00 — roadmap: shrinking the host-tool footprint

Index document for the multi-phase plan to evolve `roksbnkctl` toward a single-binary tool that executes its dependent operations locally, in a container, in a remote cluster, or over SSH — eliminating the current need to install `kubectl`, `oc`, `iperf3`, and `ibmcloud` on the user's host.

## Why

Today's prereqs (per `roksbnkctl doctor`):

| Binary | Required | Used by |
|---|---|---|
| `terraform` | yes | every `up`/`down` |
| `kubectl` | yes (effectively) | passthrough, `logs`, `status` reachability check |
| `oc` | optional | passthrough |
| `ibmcloud` | optional | passthrough |
| `iperf3` | yes for `test throughput` | local client driver |

That's 4-5 binaries to install across Linux/macOS/Windows. The E2E test we just shipped found `iperf3` missing on a development host and had to skip the throughput test entirely. The "single binary" promise is currently aspirational.

## Goal

Trim the prereq list to **`terraform` only** for the happy path, while:

- Keeping power users' direct CLI access intact (`roksbnkctl kubectl <args>` passthrough still works when host kubectl exists)
- Adding execution flexibility (`--on jumphost`, `--backend docker`, `--backend k8s`) for environments where the current local-exec model doesn't fit (firewalls, air-gapped, customer compliance, frozen toolchain versions)

## Phasing

Each phase is a discrete PRD in this directory. They build on each other in the order shown, but Phase 4 (credentials) is cross-cutting and informs the design of Phase 3.

| Phase | PRD | Outcome |
|---|---|---|
| 1 | [`01-SSH-AND-ON-FLAG.md`](./01-SSH-AND-ON-FLAG.md) | `--on <target>` flag, embedded SSH client, `targets:` config block, jumphost auto-discovery from TF outputs |
| 2 | [`02-KUBECTL-INTERNAL.md`](./02-KUBECTL-INTERNAL.md) | Native `roksbnkctl get/apply/logs/exec/port-forward` via `client-go`; OpenShift subset via `openshift/client-go`; kubectl/oc passthroughs preserved as opt-in |
| 3 | [`03-EXECUTION-BACKENDS.md`](./03-EXECUTION-BACKENDS.md) | Backend abstraction with four implementations (local / docker / k8s / ssh) applied to iperf3, ibmcloud, and as an optional alternative to terraform-exec |
| ⌬ | [`04-CREDENTIALS.md`](./04-CREDENTIALS.md) | Cross-cutting: kubeconfig + IBMCLOUD_API_KEY + SSH key propagation safely across all backends |
| 5 | [`05-E2E-TEST-PLAN.md`](./05-E2E-TEST-PLAN.md) | New E2E phases I-N that exercise every backend × tool combination on a live IBM Cloud account |

## Dependency graph

```
                Phase 1 (SSH/--on)
                    │
                    ▼
                Phase 2 (kubectl)
                    │
                    ▼
                Phase 3 (backends) ──────────┐
                    │                        │
                    │   ◀── Phase 4 (creds, cross-cutting)
                    │
                    ▼
                Phase 5 (E2E)
```

Phase 1 lands the SSH client and target abstraction the SSH backend in Phase 3 will reuse. Phase 2 gives Phase 3 a "no-op" reference (kubectl-via-Go) it can compare other backends against for output equivalence. Phase 4 informs the `RunOpts` shape every backend uses.

## Success criteria

- Fresh Ubuntu / macOS dev machine: `roksbnkctl doctor` shows green for `terraform` only, with the rest as optional informationals
- E2E Phases A-H pass with `kubectl`, `oc`, `iperf3`, and `ibmcloud` removed from `$PATH`
- E2E Phases I-N (new) pass on the same host, exercising all four backends
- Pre-cluster operations (`init`, jumphost-routed `ibmcloud iam`) work via `--on jumphost` against a TF-provisioned bastion
- Credential audit: no API key strings appear in `docker inspect`, `ps`, kube events, or any log file readable by another local user

## Out of scope (this roadmap)

- Full kubectl / oc / ibmcloud command coverage — only the BNK-relevant subset that `roksbnkctl` actually uses internally + the few high-frequency passthrough verbs
- Windows-specific Docker fallback (rely on internalized Go paths for kubectl/iperf3 instead)
- HCP Terraform / Terraform Cloud integration
- A web UI / REST API surface
- Multi-region or multi-cloud (still IBM Cloud + ROKS)

## Implementation order recommendation

1. **Phase 1 first** — small (~600 LOC), unblocks the SSH backend in Phase 3, immediately useful to users who want jumphost-routed operations.
2. **Phase 2 second** — biggest UX win; eliminates the kubectl install requirement which is the most common cause of "doctor flagged a warning."
3. **Phase 4 third (informational)** — read before designing Phase 3 backends so the credential interfaces are baked in correctly.
4. **Phase 3 fourth** — biggest engineering effort (~2000+ LOC across four backend implementations + tool migration).
5. **Phase 5 last** — extends the existing e2e-test driver with new phases; can be drafted in parallel with Phase 3 implementation.

## Open meta-questions

- Should we ship phases as separate releases (`v0.7` → SSH, `v0.8` → kubectl, etc.) or land them on `main` and tag a single `v1.0` once everything is in?
- Should the docker / k8s backend tools images be hosted in `ghcr.io/jgruberf5/...` or under an F5-owned org?
- Naming: `--on` for SSH targets, `--backend` for execution mode — confusingly similar. Consolidate? `--via local|docker|k8s|ssh:<target>`?
