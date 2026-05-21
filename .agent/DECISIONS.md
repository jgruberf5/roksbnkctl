# Architecture Decision Records

> Decisions are permanent — never delete, only supersede.

Last Updated: 2026-05-21

---

## Accepted Decisions

### D-001 — Remove Terraform; provisioning is strict Go SDK with imperative phases (2026-05-21)

**Context.** Terraform caused four real, compounding pains: TF-binary distribution overhead, `.tfstate` fragility, two control planes for the same AWS account (TF + bash/SDK), and slow iteration on the apply→fix→reapply cycle. The aws-gpu-setup PoC (`/Users/j.lucia/Code/aws-gpu-setup/`) proved an imperative awscli + YAML method works for the same job at much higher iteration velocity.

**Decision.** Remove Terraform from awsbnkctl. Provisioning becomes **strict Go SDK only** (no shell-out to `aws` or `kubectl`), organized as sequential imperative **phase functions** ported from aws-gpu-setup's `up.sh` / `down.sh` shape — NOT a reconciler framework.

**Consequences.**
- Delete: `terraform/`, `internal/tf/`, `embedded.go` (TF embed), `install_build_dependencies.sh`, `internal/cli/tfvars.go`.
- `internal/aws/` grows to cover what TF did (already has typed wrappers for ec2/eks/iam/s3/vpc/sts).
- Single-binary distribution restored. forge MCP integration keeps typed Go contracts.
- Adopts the *method* from aws-gpu-setup; does **not** vendor or subprocess the bash.
- Current TF stack and TF-created clusters: ignored (no in-flight clusters depend on it; manual cleanup by operator).

**See:** [`docs/POST_TERRAFORM_DIRECTION.md`](../docs/POST_TERRAFORM_DIRECTION.md) — full buildable spec.

---

### D-002 — State model: AWS tags as truth + local IDs cache as convenience (2026-05-21)

**Context.** With TF state gone, awsbnkctl needs a way to know "what did I create" for status and destroy. Two real options: a local state file (recreates the `.tfstate` problem in a new format) or AWS tags + discovery (matches the existing "AWS is truth" thesis from [`docs/FORGE_MCP_INTEGRATION.md`](../docs/FORGE_MCP_INTEGRATION.md)).

**Decision.** **AWS tags are the source of truth.** Every awsbnkctl-created resource carries `awsbnkctl:cluster=<name>` plus component/pattern tags. A small `.awsbnkctl/<cluster>/state.env` file caches resource IDs for speed, but it is **rebuildable from tags at any time** and tag-discovery is the fallback when the cache is missing or corrupt.

**Consequences.**
- `status`, `doctor`, `inspect` query AWS directly by tag, never read forge or rely solely on the cache.
- `down` reads the cache first; on missing/corrupt cache, falls back to tag-discovery — never silently no-ops.
- `state.env` is in `.gitignore`. Lossy by design.
- Tag scheme: `awsbnkctl:cluster`, `awsbnkctl:component`, `awsbnkctl:pattern`, `awsbnkctl:managed=true` + `Name`. Plus any `cluster.yaml: tags:` / `metadata.labels:` merged in.

**See:** `docs/POST_TERRAFORM_DIRECTION.md` §9–10.

---

### D-003 — Intent format: `cluster.yaml` (structured, canonical) (2026-05-21)

**Context.** With TF gone, awsbnkctl needs a declarative intent surface. aws-gpu-setup uses flat `vars.env`; dpubnkctl uses YAML examples as named topologies. Strict Go SDK removes the bash-source benefit of vars.env. Forge MCP wants typed I/O.

**Decision.** **`cluster.yaml`** — Kubernetes-style (`apiVersion: awsbnkctl/v1`, `kind: Cluster`), structured, Go-struct-validated, forge-readable. One file per cluster. No `vars.env` emit; bash-source compatibility is not a goal under strict Go SDK.

**Consequences.**
- `internal/intent/cluster.go` defines the schema. Unknown fields are errors in v1.
- `metadata.name` is the value of `awsbnkctl:cluster=<name>` tag and the directory name under `.awsbnkctl/`. Must match EKS cluster name regex.
- `network.azs` is explicit (no auto-pick) for reproducibility.
- `pattern: <name>` selects k8s manifest variant directory (see D-004).
- Schema evolution: `apiVersion: awsbnkctl/v1` reserves room for v2 without breakage.

**See:** `docs/POST_TERRAFORM_DIRECTION.md` §5.

---

### D-004 — K8s manifests live in variant directories selected by `cluster.yaml: pattern:` (2026-05-21)

**Context.** Cluster-side YAMLs differ across deployment patterns — currently host-device ENI, planned next: SR-IOV to TMM. Switching patterns must NOT require a Go SDK code change. aws-gpu-setup keeps these in `manifests/*.yaml` flat; dpubnkctl uses examples/ as named topologies.

**Decision.** **Variant directories** under `internal/k8s/manifests/`:
- `internal/k8s/manifests/shared/` — pattern-agnostic (cert chain, license CR, pull secrets, otel)
- `internal/k8s/manifests/host-device/` — current pattern
- `internal/k8s/manifests/sr-iov-tmm/` — planned

`cluster.yaml: pattern: <name>` selects the directory. Adding a third pattern = add a directory. Manifests are embedded via `go:embed`, rendered with Go `text/template` (NOT envsubst), applied via existing `internal/k8s/apply.go` (already client-go-based, already strict).

**Consequences.**
- Operator/agent changes pattern by editing one field in `cluster.yaml` — no recompile.
- Apply order: `shared/` first (alphabetical within), then `<pattern>/` (alphabetical within).
- Namespace stamped with `awsbnkctl.io/pattern: <pattern>` label after apply, so forge GUI can surface it without a bnk-forge schema change.
- Templating uses typed struct from `cluster.yaml`, not env vars.

**See:** `docs/POST_TERRAFORM_DIRECTION.md` §11.

---

### D-005 — SSO sentinel pattern from aws-gpu-setup ported to Go middleware (2026-05-21)

**Context.** aws-gpu-setup's `lib/lab-core.sh` solves a hard mid-run failure mode: SSO token expiry during a multi-phase script causes downstream phases to silently no-op (because `... || true` swallows the error), producing a false-positive "DONE" exit. The fix in bash: `aws_q` wrapper writes a sentinel file on auth errors; `check_auth_or_die` at every phase banner reads it and hard-exits.

**Decision.** **Port the sentinel pattern into a Go SDK middleware** (`internal/aws/awsmw/sso.go`). Every AWS client constructed by awsbnkctl is wrapped with `WithSSOWatch`, which inspects deserialized errors and sets a process-level `atomic.Bool` on `ExpiredToken` / `InvalidClientTokenId` / `UnauthorizedException` / SSO-session-expired codes. Every phase function begins with `CheckAuthOrDie(profile)`, which hard-exits with the exact `aws sso login --profile <X>` hint.

**Consequences.**
- No silent no-op cascade on mid-run SSO expiry.
- Up-front protection too: `sts.GetCallerIdentity` in phase 00.
- Unit-testable by injecting fake auth errors via SDK middleware.
- Operators get an actionable error message, not a confusing AccessDenied stack.

**See:** `docs/POST_TERRAFORM_DIRECTION.md` §8.

---

### D-006 — Forge register fires on EKS-Active (not at end of `up`); soft-fail with retry (2026-05-21)

**Context.** [`docs/FORGE_MCP_INTEGRATION.md`](../docs/FORGE_MCP_INTEGRATION.md) defines the forge handoff model (peer-read; awsbnkctl creates project + cluster records, forge reads AWS on its own creds). Two questions it left unresolved: *when* in the up flow register fires, and what happens if forge is unreachable.

**Decision.**
- **Register fires on EKS-Active, before BNK install** — not at end of `up`. Forge's own scan polling then surfaces BNK-install progress in the GUI during the longest phase. STS-presigned bootstrap kubeconfig has 15-min TTL, plenty of headroom.
- **Soft-fail with retry** (3 attempts, exponential backoff). On final failure, AWS infra stays up; `up` exits 0 with a warning; `forge-link.json` written with `status: pending`; operator runs `awsbnkctl forge register` later. AWS infra is the expensive thing; do not roll it back for a localhost dev-server hiccup.
- **`down` calls forge `unregister` by default**; `--keep-forge-link` flag preserves the project record. Matches the `--keep-iam` / `--keep-keypair` flag family.

**Consequences.**
- Operators watching forge see the cluster appear within ~10 min of `up` start, not after ~20+ min.
- `internal/cli/up.go` calls `internal/forge/register.go` between phase 08 (EKS active) and phase 09 (node group).
- Pattern variant (`host-device`, `sr-iov-tmm`) is exposed to forge via namespace label `awsbnkctl.io/pattern: <name>` — no bnk-forge schema change.

**See:** `docs/POST_TERRAFORM_DIRECTION.md` §3.

---

### D-007 — Tracer-bullet first slice: cluster.yaml → tagged VPC + subnets only (2026-05-21)

**Context.** D-001 through D-006 commit to a large architectural shift. Doing it as one big-bang rewrite would mean weeks with no shippable work. Module-by-module migration over the old TF code adds coexistence cost. User direction: greenfield build, no migration tax.

**Decision.** **Tracer-bullet first slice** = `cluster.yaml` → tagged VPC + subnets + IGW + NAT + RTs via Go SDK, symmetric `up`/`down`, IDs cache write, idempotent re-run. **No EKS, no IAM, no k8s, no forge.** Smallest deliverable that exercises every architectural commitment: intent format, SDK shape, tag scheme, IDs cache, auth sentinel, post-condition waits, idempotency, symmetric destroy.

**Consequences.**
- ~10-file PR scope: intent loader, middleware, tags, state, 5 phase files, up/down CLI, example, tests.
- Acceptance criteria spelled out in spec §13. Critical ones: idempotent re-run, tag-discovery fallback when cache deleted, SSO mid-run expiry triggers hard exit with the right hint.
- Subsequent slices stack on top: IAM (slice 2), EKS + node group (slice 3), forge register (slice 4), k8s + variants (slice 5), polish (slice 6).

**See:** `docs/POST_TERRAFORM_DIRECTION.md` §13.

---

### D-008 — `awsbnkctl validate <cluster.yaml>` subcommand (2026-05-21)

**Context.** Operators iterating on a cluster.yaml want to confirm it parses + validates without burning an SSO session or running `up --dry-run` (which requires AWS auth even though no resources are mutated, because Phase00Preflight calls `sts.GetCallerIdentity`). mwiget/kindbnkctl has the same shape (`kindbnkctl validate poc.yaml`), confirming this is a useful ergonomic.

**Decision.** Add `awsbnkctl validate <path>` — reads the file, runs `intent.Load` (strict YAML decode + regex + non-empty checks), exits non-zero on failure, prints a one-line success summary otherwise. **Makes zero AWS API calls.**

**Consequences.**
- New `internal/cli/validate.go` (~50 LOC) + tests. No new dependencies.
- Useful as a CI gate for any cluster.yaml committed under `examples/`.
- Lives outside the phased path entirely — no preflight, no AWS auth, no state.env touched.
- Forward-compat: when more fields land (`cluster:` block in slice 3, addons in slice 5), the validator picks up the new validation rules automatically via `intent.Load`.

**See:** `internal/cli/validate.go`. Pattern inspiration: `mwiget/kindbnkctl/internal/cli/validate.go`.

---

### D-009 — Treat mwiget/kindbnkctl as a peer-reference for the *bnkctl family (2026-05-21)

**Context.** `mwiget/kindbnkctl` is a sibling Go CLI that deploys BNK 2.3 onto a 2-node `kind` cluster. Same author as dpubnkctl ([[reference_dpubnkctl_architecture]]). Architecture is consistent with the direction we adopted in D-001..D-007: single-binary Go CLI, Kubernetes-style YAML manifest (`apiVersion: kindbnkctl.f5.com/v1alpha1, kind: PoC`), peer-read forge integration, embedded assets, scenarios framework.

**Decision.** Treat `mwiget/kindbnkctl` (and `mwiget/dpubnkctl`) as **peer references** for the *bnkctl family. Specifically:

1. **Adopt `forge:` block in cluster.yaml** (this PR — `ForgeSpec` in `internal/intent/cluster.go`). Operator-declared forge integration in the intent file rather than per-invocation flags. Matches kindbnkctl's `bnk_forge:` block shape.

2. **Adopt `ErrNotRunning` + soft-skip pattern when slice 4 implements the forge handoff.** kindbnkctl's `internal/bnkforge/launcher.go` returns a sentinel error when forge isn't reachable; the caller decides whether to soft-skip (auto-hook from `cluster up`) or hard-fail (explicit `bnk-forge launch`). awsbnkctl will use this pattern in slice 4 + retry the 3 attempts per [[D-006]] before writing `pending` to the link file.

3. **REST fallback is canonical, not exceptional.** kindbnkctl uses REST (`POST /api/auth/login`, `POST /api/projects`) — never MCP. Our spec ([[project_forge_mcp_integration]]) prefers MCP but allows REST when the MCP catalog is behind. The user's slice 3/4 /goal explicitly endorsed REST fallback. Slice 4 should ship both paths from day one, MCP-first.

4. **Capture the scenarios framework** (`internal/scenarios/{aisemcache,aitokencount,bgppeer,clusterwidewatch,corefiles,cwcadminaccess,extrespool,httproutee2e,proxyprotocol}/` + `runner.go` + Green/Amber/Red rating) **as the reference for slice 5+ when we add variant patterns / e2e scenarios**. Apply()/Verify() interface + rating-by-environment is a clean pattern. Don't port the code now (slice 5+ scope); just keep the link.

5. **Verb-split (`cluster up/down` infra vs `deploy/destroy` BNK) is worth considering for a future major version**, but DOES NOT block our current `up/down` flow. Captured here so we don't reinvent it later. Slice 6 or beyond.

**Consequences.**
- Schema gains an optional `forge:` block now (this commit).
- Slice 4 has a clear reference implementation pattern.
- Slice 5+ has named patterns to crib from when designing the manifest-variant + scenario layer.
- `mwiget/kindbnkctl` joins `mwiget/dpubnkctl` as a peer-architecture-reference in the *bnkctl family. Not a fork relationship — independent Go CLIs sharing patterns.

**See:**
- `mwiget/kindbnkctl` repo on GitHub
- `mwiget/dpubnkctl` (already covered in [[reference_dpubnkctl_architecture]])
- `docs/FORGE_MCP_INTEGRATION.md` §0 (peer-read model)
- `internal/intent/cluster.go` `ForgeSpec` (this commit)

---

## D-010 — OIDC Provider Scope: Per-Cluster, Tagged, Torn Down with Cluster

**Date:** 2026-05-22
**Status:** Accepted
**Recorded during:** slice-07 Pass 3 (Architect mandate)

**Context.** Phase 18 creates an IAM OIDC Identity Provider (OIDC IdP) for the EKS cluster to enable IRSA (IAM Roles for Service Accounts). The question was whether to use a shared OIDC provider across multiple clusters or a per-cluster provider.

**Decision.** OIDC provider scope is **per-cluster, torn down with the cluster**.

Rationale:
1. Each EKS cluster has a unique OIDC issuer URL (`https://oidc.eks.<region>.amazonaws.com/id/<unique-id>`) guaranteed by AWS — there is no shared-provider case to engineer.
2. The per-cluster provider is tagged with `awsbnkctl:cluster=<name>` so it is discoverable and can be torn down via tag-discovery fallback (D-003).
3. `--keep-irsa` flag is available for operators who want to retain the OIDC provider across `down`/`up` cycles (e.g. iterative cluster rebuilds during development). Default: delete on `down`.
4. No multi-tenant OIDC sharing pattern is needed in the current architecture; if required in future, model it as a new cluster.yaml field (`sharedOIDC: true`) in a separate slice.

**Consequences.**
- Phase 18 (`Phase18IRSAOIDC`) always creates a new OIDC provider for the cluster.
- Phase 18 down (`Phase18IrsaOidcDown`) deletes it unless `--keep-irsa` is passed.
- Operators rebuilding a cluster quickly can use `--keep-irsa` to avoid OIDC provider churn.
- No shared-provider engineering needed now; scope this ADR as a constraint on future slice design.

**See:** `internal/aws/phases/phase18_irsa_oidc.go`

---

## D-011 — CWC DNS-Warmup Heal Threshold Hardcoded at restartCount >= 3

**Date:** 2026-05-22
**Status:** Accepted
**Recorded during:** slice-07 Pass 3 (Architect mandate)

**Context.** The `f5-spk-cwc` pod (CWC = Connection Watchdog Component) in `f5-cne-core` sometimes crash-loops during initial DNS warmup on fresh EKS clusters. Phase 24 implements a heuristic heal: if the restart count reaches a threshold, force-delete the pod to break the loop and allow the scheduler to restart it cleanly.

**Decision.** The restart threshold is **hardcoded at `restartCount >= 3`** (constant `cwcRestartThreshold` in `phase24_cwc_heal.go`).

Rationale:
1. **Empirical data:** syd-test-lab 2026-05-19 observed CWC stabilising after a force-delete at `restartCount=3`. Values 1 or 2 would trigger unnecessary restarts during normal slow start; values > 5 delay recovery.
2. **Not a user-tunable knob:** The DNS-warmup crash loop is an infrastructure pathology, not a workload configuration. Exposing the threshold in `cluster.yaml` would be false complexity — operators cannot meaningfully choose a different value without deep CWC internals knowledge.
3. **Best-effort semantics:** Phase 24 returns nil regardless of outcome. If the heal does not help, Phase 25's activation poll will time out and provide a clear error for human diagnosis. The threshold is not a hard guarantee.

**Consequences.**
- `phase24_cwc_heal.go` exports the constant `cwcRestartThreshold = 3` (package-internal, accessible to tests).
- If future BNK releases change the DNS-warmup behaviour (e.g. CWC no longer crash-loops), Phase 24 becomes a no-op (pod is Ready on first iter).
- If the threshold needs adjustment, it is a one-line change in `phase24_cwc_heal.go` with a test update — reviewers see the intent clearly.
- Phase 24 is a heuristic heal, NOT a verification gate. It always returns nil. Phase 25 is the authoritative gate.

**See:** `internal/aws/phases/phase24_cwc_heal.go`, constant `cwcRestartThreshold`

---

## Superseded Decisions

(None)
