# Post-Terraform Direction — Implementation Spec

**Status:** Approved 2026-05-21 (via grill-me session). Not yet implemented.
**Owner:** awsbnkctl maintainers
**Last updated:** 2026-05-21
**Companion docs:** [`docs/FORGE_MCP_INTEGRATION.md`](FORGE_MCP_INTEGRATION.md) (forge handoff already designed; this doc realigns the rest of the tool to the same model)

---

## 0 · TL;DR

Terraform was the wrong implementation choice for awsbnkctl. The aws-gpu-setup PoC (`/Users/j.lucia/Code/aws-gpu-setup/`) proved that an imperative awscli + YAML method delivers the same result with dramatically faster iteration. We adopt the **method**, not the code — porting the imperative phased shape into Go against the AWS SDK, keeping the Go binary as the typed contract surface for forge MCP and the single-binary distribution story.

**Bottom line:**
- Delete `terraform/`, `internal/tf/`, `embedded.go` (TF embed), `install_build_dependencies.sh`.
- Provisioning becomes sequential phase functions in Go, using only the AWS SDK (no `aws` CLI exec).
- Intent is a structured `cluster.yaml` per cluster.
- K8s manifests live in **variant directories** (`internal/k8s/manifests/<pattern>/`) selected by `cluster.yaml: pattern: …`.
- State = AWS tags (truth) + a small `.awsbnkctl/<cluster>/state.env` IDs cache (rebuildable from tags).
- Forge handoff stays as designed in `FORGE_MCP_INTEGRATION.md`, with one timing change: register on EKS-Active, not at the very end.

---

## 1 · Why (the four pains TF caused)

All four mattered — that's why the move is wholesale, not patch:

| Pain | Symptom | Fix in new model |
|---|---|---|
| **Distribution / binary fetch** | `internal/tf/fetch.go`, version pinning, embedded modules, `install_build_dependencies.sh` | TF binary stops shipping. Single Go binary. |
| **State-file fragility** | `terraform.tfstate` drift, `terraform.applied.tfvars` dance, lock contention, manual `state rm` recovery | AWS tags are truth. Local cache is regenerable. |
| **Two control planes** | TF for infra + bash/SDK for runtime = two mental models, divergent failure surfaces | One Go program, one mental model. |
| **Iteration speed** | Plan/apply cycles slow; errors opaque; hard for AI agents (forge/MCP) to reason about TF graph | Imperative, sequential, log-able. Mirror aws-gpu-setup's velocity. |

---

## 2 · Architectural commitments (locked)

| Layer | Decision |
|---|---|
| AWS writes + reads | **Strict Go SDK only.** No exec to `aws` CLI. `internal/aws/` grows to cover what TF did. |
| Provisioning shape | **Imperative phased functions** ported from aws-gpu-setup's `up.sh` / `down.sh`. NOT a reconciler framework. |
| Auth | **SSO sentinel pattern** from `aws-gpu-setup/lib/lab-core.sh`'s `aws_q` + `check_auth_or_die`, ported into a Go middleware wrapper around the SDK client. Up-front `sts.GetCallerIdentity` + mid-run phase-boundary checks. Hard-exit on `ExpiredToken` / `InvalidClientTokenId` with the exact `aws sso login --profile <X>` hint. |
| State | **AWS tags (truth) + `.awsbnkctl/<cluster>/state.env` IDs cache.** Required tag on every resource: `awsbnkctl:cluster=<name>`. Cache is rebuildable from tags. Loss of cache → tag-discovery fallback. |
| Intent | **`cluster.yaml`** — structured, Go-struct-validated, forge-MCP-readable. No vars.env emit. |
| K8s manifests | **Variant directories.** `internal/k8s/manifests/<pattern>/*.yaml` (e.g. `host-device/`, `sr-iov-tmm/`). `cluster.yaml: pattern: <name>` selects. Adding a third pattern = add a directory. Shared manifests in `internal/k8s/manifests/shared/`. |
| K8s apply path | client-go via existing `internal/k8s/apply.go`. Already strict (no kubectl exec). Confirmed by grep of `internal/k8s/*.go` — uses `k8s.io/apimachinery`, `k8s.io/cli-runtime`. |
| Idempotency | Per-call "tolerate already-gone" (swallow `NotFound` / `AlreadyExists`). Post-condition waits (`WaitUntilReady`) only where the AWS object has a delay-to-usable (EKS cluster active, node group active, NAT EIP unassociation, ENI detach). |
| Subcommand surface | `plan` dies. `up --dry-run` replaces it. |
| Destroy | Single interactive confirm + flags: `--keep-iam`, `--keep-keypair`, `--skip-bnk`, `--keep-forge-link`, `--yes`/`-y`. Idempotent reverse-order. |

---

## 3 · Forge integration timing (delta from existing plan)

Forge integration model is **locked in [`FORGE_MCP_INTEGRATION.md`](FORGE_MCP_INTEGRATION.md)** (peer-read; AWS is truth; awsbnkctl never asks forge for cluster state). This doc only changes two things:

| Aspect | Decision |
|---|---|
| **Register timing** | Fire on EKS-Active, **before BNK install**, not at end of `up`. Forge's own poll surfaces BNK-install progress in the GUI during the longest phase. Bootstrap STS-presigned kubeconfig has 15-min TTL, plenty of headroom. |
| **Failure semantics** | **Soft-fail with retry** (3 attempts, exponential backoff). AWS infra stays up. Link file `forge-link.json` marked `pending`. Operator runs `awsbnkctl forge register` to recover. Rationale: don't roll back expensive AWS infra for a localhost dev-server hiccup. |
| **Down + forge** | `awsbnkctl down` calls `forge unregister` **by default**. `--keep-forge-link` preserves the project record. Matches the `--keep-iam` / `--keep-keypair` flag family. |
| **Pattern variant visibility** | Stamp namespace label/annotation: `awsbnkctl.io/pattern: host-device`. No bnk-forge schema change required. Forge GUI can surface it later if it grows the feature. |

### Implementation status (slice 4, 2026-05-21)

Phase 09 (`Phase09ForgeRegister` / `Phase09ForgeRegisterDown`) is **implemented** in
`internal/aws/phases/phase09_forge_register.go` and wired into `runPhasedUp` /
`runPhasedDown` in `internal/cli/lifecycle.go`.

Key implementation notes:
- **MCP-first, REST fallback.** `forge.Register()` (MCP) is tried first. On a catalog-gap
  error (`IsMCPCatalogGapErr` in `internal/forge/rest.go`) the phase retries via
  `forge.RegisterREST()` which speaks directly to the forge REST API. This mirrors the
  kindbnkctl precedent (D-009): MCP is preferred but REST is the canonical fallback, not
  exceptional.
- **REST fallback credentials.** Hardcoded `admin/changeme` matching the localhost
  bnk-forge dev stack. Real forge auth is out of scope for slice 4.
- **Soft-fail retry loop.** 3 iterations × exponential backoff (1s → 3s → 9s). Uses
  `select { case <-time.After(d): case <-ctx.Done() }` so `ctx` cancellation propagates.
- **`Link.Status` field.** `internal/forge/link.go` `Link` struct gains a `Status` field
  (`"registered"` or `"pending"`). Empty string = `"registered"` for backward compat.
  `IsRegistered()` helper method used by Phase09 idempotency check.
- **`--keep-forge-link` flag.** Added to `downCmd` only; bound to `flagKeepForgeLink`
  (single-owner per the cobra anti-pattern comment in lifecycle.go).
- **`Clients.ForgeClient`.** Added to `internal/aws/phases/clients.go`. Populated via
  `AttachForgeClient(enabled, mcpURL)` called in `runPhasedUp` / `runPhasedDown` after
  `NewClients`, keeping the existing constructor signature unchanged.

---

## 4 · Repository layout — what changes

### Delete

- `terraform/` (entire directory — 1,480 LOC across 9 modules)
- `internal/tf/` (terraform binary fetcher, wrapper, vars handling)
- `embedded.go` (TF embed; recheck — may be repurposed for manifest embed)
- `install_build_dependencies.sh` (TF binary install)
- `internal/cli/tfvars.go` (TF-vars CLI command)
- TF-binary fetch logic in scripts/

### Keep (with growth)

- `internal/aws/` — already has typed wrappers for ec2, eks, iam, s3, vpc, servicequotas, sts. Grows to cover what TF did. **Stays the SDK-only contract surface.**
- `internal/k8s/` — already client-go-based and strict. No structural change.
- `internal/forge/` — no structural change; timing change in caller.
- `internal/cli/` — `up`, `down`, `status`, `doctor`, `inspect`, `init`, `forge`, `k`, `install`, `test` survive; `plan` is removed.
- `internal/config/`, `internal/exec/`, `internal/remote/`, `internal/ui/`, `internal/test/` — unaffected.

### New

- `internal/intent/` — `cluster.yaml` schema (Go struct), loader, validator.
- `internal/aws/awsmw/` — SDK middleware: SSO sentinel wrapper, structured logger, retry policy.
- `internal/aws/phases/` — phased provisioning functions: `Phase01Preflight`, `Phase02VPC`, `Phase03Subnets`, etc. Each is a top-level function that mutates a `*State` (in-memory) and tags every resource it creates.
- `internal/aws/tags/` — tag constants + helpers (`Required(name)`, `Component(name)`, `Pattern(name)`).
- `internal/aws/state/` — IDs cache reader/writer (`state.env`), tag-discovery fallback.
- `internal/k8s/manifests/<pattern>/*.yaml` — variant manifests. Embedded via `go:embed`.
- `internal/k8s/manifests/shared/*.yaml` — pattern-agnostic manifests (cert chain, license CR).
- `internal/k8s/render/` — Go `text/template` rendering against the cluster intent struct.
- `docs/POST_TERRAFORM_DIRECTION.md` — this file.

### Operator-visible files at runtime

- `cluster.yaml` — user-authored intent (typically in repo root or `examples/<topology>/`).
- `.awsbnkctl/<cluster-name>/state.env` — IDs cache (in `.gitignore`).
- `.awsbnkctl/<cluster-name>/forge-link.json` — forge project + cluster IDs.
- `.awsbnkctl/<cluster-name>/kubeconfig` — generated by `awsbnkctl` after EKS-Active.

---

## 5 · `cluster.yaml` schema (v1)

Kubernetes-style for familiarity; the `apiVersion` lets us evolve the schema later without breaking older intents.

```yaml
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: jl-gpu-lab                  # used as the awsbnkctl:cluster=<name> tag value
  region: ap-southeast-2
  labels:                           # optional, propagated to AWS resource tags
    owner: jarrod
    purpose: gpu-lab

network:
  vpcCidr: 10.0.0.0/16
  azs:                              # explicit AZ list for reproducibility
    - ap-southeast-2a
    - ap-southeast-2b
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
      - cidr: 10.0.2.0/24
        az: ap-southeast-2b
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
      - cidr: 10.0.12.0/24
        az: ap-southeast-2b
  natGateways: 1                    # 1 (cost-optimized) or per-az (HA)

cluster:                            # OPTIONAL for slices 1+2 (network + IAM only)
                                    # REQUIRED for slices 3+ (EKS phases 08/10/11)
  kubernetesVersion: "1.30"         # default "1.30" if omitted
  nodeGroups:
    - name: default                 # required; forms <cluster>-ng-<name>; lowercase alphanumeric + hyphens
      instanceType: t3.medium       # default t3.medium
      desiredSize: 1                # default 1
      minSize: 1                    # default 1
      maxSize: 2                    # default 2
      diskSize: 50                  # GiB; default 50
      labels:                       # optional Kubernetes node labels
        node-role: worker
    # For GPU workloads (future slice):
    # - name: gpu
    #   instanceType: p5.48xlarge
    #   desiredSize: 1
    #   minSize: 0
    #   maxSize: 2
    #   diskSize: 200
    #   labels:
    #     node-role: gpu

pattern: host-device                # selects internal/k8s/manifests/host-device/
                                    # alternatives: sr-iov-tmm (planned)

# BNK supply-chain artefacts loaded as k8s Secrets by slice 5.
# Paths are local files on the operator's machine; awsbnkctl reads and
# creates the Secrets directly (NO S3 round-trip, matching aws-gpu-setup).
bnk:                                # REQUIRED for slice 5 (k8s install)
  farArchive: ./cne_pull_64.json    # F5 FAR pull credentials (JSON)
  jwt: ./license.jwt                # F5 subscription JWT
  manifestVersion: "2.3.0-3.2598.3-0.0.170"   # default — overrides come per pattern

addons:                             # OMITTED for tracer-bullet slice
  flo:
    enabled: true
    version: "v2.21.13-0.0.28"
  cneInstance:
    cisVersion: "3.9.0"

tags:                               # additional tags applied to all AWS resources
  cost-center: "RnD-AI"
```

Field-level notes:
- `metadata.name` is **load-bearing**: it becomes the value of the `awsbnkctl:cluster=<name>` tag and the directory name under `.awsbnkctl/`. Must match regex `^[a-z][a-z0-9-]{0,38}[a-z0-9]$` (EKS cluster name rules).
- `network.azs` is **explicit by design** — we will not "pick N AZs from the region" for you, because that produces non-deterministic infra across runs.
- `cluster` block is **optional for slices 1+2** (network + IAM only); **required for slice 3+** (phases 08/10/11 error clearly if it's absent).
- `cluster.nodeGroups` must be **non-empty** when the `cluster` block is present.
- `cluster.nodeGroups[].name` must match `^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$` (lowercase alphanumeric + hyphens).
- `pattern` is **required** when the cluster.yaml drives k8s manifests. For the tracer-bullet slice (VPC + subnets only), it is ignored.
- Unknown fields are **errors**, not warnings, in v1.

---

## 6 · Phase ordering — `up`

Sequential phases, each a Go function. Each phase calls `checkAuthOrDie()` at its start (the SSO sentinel pattern), uses `internal/aws/awsmw`-wrapped SDK clients, and writes IDs to `state.env` as it creates resources.

```
up
├─ 00. preflight (Phase00Preflight)
│   ├─ load cluster.yaml + validate schema
│   ├─ sts.GetCallerIdentity (auth check, account verification)
│   ├─ ensure .awsbnkctl/<name>/ exists; load state.env if present
│   └─ servicequotas spot-check (vCPU, EIPs, NAT GW count for region)
├─ 02. vpc (Phase02VPC)               ◄── slice 1 (shipped)
├─ 03. subnets (Phase03Subnets)       ◄── slice 1
├─ 04. igw (Phase04IGW)               ◄── slice 1
├─ 05. nat gateway + EIP (Phase05NAT) ◄── slice 1
├─ 06. route tables (Phase06RouteTables) ◄── slice 1
├─ 07. iam: cluster role, node instance role, node instance profile (Phase07IAM) ◄── slice 2 (shipped)
├─ 08. eks cluster (Phase08EKSCluster)       ◄── slice 3 (shipped)
│       wait until ACTIVE (~10 min); capture endpoint/CA/OIDC URL/security group
├─ 09. forge register (Phase09ForgeRegister) ◄── slice 4 (shipped)
│       fires on EKS-Active, before node group; MCP-first with REST fallback;
│       soft-fail with 4-attempt 1s/3s/9s backoff; pending-link on give-up
├─ 10. eks node group (Phase10NodeGroup)     ◄── slice 3 (shipped)
│       creates EC2 launch template (<cluster>-bnk-lt) before the node group (idempotent);
│       wait until ACTIVE (~7 min); subnets: public only; AMI: AL2_x86_64
├─ 11. kubeconfig (Phase11Kubeconfig)        ◄── slice 3 (shipped)
│       write .awsbnkctl/<name>/kubeconfig (exec-auth via `aws eks get-token`)
├─ 16. tmm node label (Phase16TMMNodeLabel)  ◄── slice 7 (schema + prereqs, activation-inert)
│       discovers the first worker node (K8s node list); stamps node label
│       awsbnkctl.f5.com/tmm-node=true; captures EC2 instance ID → TMM_INSTANCE_ID state key
│       skipped when pattern ≠ host-device; dry-run sets placeholder TMM_INSTANCE_ID
├─ 17. secondary ENIs (Phase17SecondaryENIs) ◄── slice 7 (schema + prereqs, activation-inert)
│       creates one ENI in BNK_EXT_SUBNET + one in BNK_INT_SUBNET (SG: SG_BNK_DATA);
│       attaches both to TMM_INSTANCE_ID at device index 1 (ext) / 2 (int);
│       idempotent: tag-discovery finds existing ENIs before create;
│       state keys: ENI_EXT_ID, ENI_INT_ID
├─ 18. IRSA / OIDC (Phase18IRSAOIDC)        ◄── slice 7 (schema + prereqs, activation-inert)
│       per-cluster OIDC provider (thumbprint via crypto/tls — NO openssl exec, D-001);
│       IRSA role f5-cne-controller-irsa-<cluster> with CneControllerVpcRead inline policy
│       (EC2 read/write: DescribeVpcs, DescribeSubnets, DescribeNetworkInterfaces,
│        CreateNetworkInterface, DeleteNetworkInterface, ModifyNetworkInterfaceAttribute, …);
│       adds cluster SG ingress from SG_BNK_DATA; state keys: OIDC_PROVIDER_ARN, IRSA_ROLE_ARN
│       --keep-irsa flag on down retains the OIDC provider (role always deleted)
├─ 12. k8s install foundation (Phase12K8sFoundation) ◄── slice 5 (shipped)
│       12.1 create namespaces: cert-manager, f5-cne-core, f5-bnk-instance, f5-utils
│       12.2 load FAR archive + license JWT as k8s Secrets (4× dockerconfigjson + 1× opaque)
│       12.3 apply cert-manager v1.16.1 via embedded upstream YAML; wait Deployments + CRD
│       12.4 apply BNK cert chain (ClusterIssuer → Certificate → CAIssuer); wait CA cert Ready
│       variant manifest scaffold: internal/k8s/manifests/{shared,host-device,sr-iov-tmm}/
│       deferred to slice 7+: CNEInstance CR, F5SPKVlan, License CR, DNS-warmup
├─ 14. FLO Helm install (Phase14FLOHelm) ◄── slice 6 (shipped)
│       OCI registry login → oci://repo.f5.com/charts/f5-lifecycle-operator v2.21.13-0.0.28
│       Helm install-or-upgrade (idempotent); --wait 15 min timeout
│       wait cneinstances.k8s.f5.com CRD (5 min)
│       values from shared/flo-values.yaml.tmpl; skip when addons.flo.enabled: false
├─ 15. OTEL Certificate CRs (Phase15OTELCerts) ◄── slice 6 (shipped)
│       external-otelsvr + external-f5ingotelsvr in f5-cne-core
│       signed by <cluster>-ca-cluster-issuer; wait Ready=True
├─ 19. cloud-network-mapping CM (Phase19CloudNetworkMapping) ◄── slice 7b (Pass 2, activation-inert)
│       ConfigMap cloud-network-mapping in f5-cne-system
│       maps AZ → mgmt/external/internal subnet-id + CIDR
│       reads MGMT_SUBNET (alias for PUBLIC_SUBNETS[0]), BNK_EXT_SUBNET, BNK_INT_SUBNET from state
│       persists host-device constants to state: INSTANCE_NS, OPERATOR_NS, EXTERNAL_IFNAME,
│         INTERNAL_IFNAME, EXTERNAL_PCI, INTERNAL_PCI, CLOUD_HOST_DEVICE_TAG, CLOUD_HOST_DEVICE_NAME
├─ 20. host-device NADs (Phase20NADs) ◄── slice 7b (Pass 2, activation-inert)
│       NetworkAttachmentDefinitions external-netdevice (ens8) + internal-netdevice (ens7)
│       applied into BOTH f5-cne-system AND default namespaces
│       (per aws-gpu-setup/deploy-bnk.sh:143 NAD_NS_SET)
├─ 21. IRSA ServiceAccount pre-creation (Phase21IRSASA) ◄── slice 7b (Pass 2, activation-inert)
│       ServiceAccount f5-cne-controller-<cluster>-bnk-serviceaccount in f5-cne-system
│       annotation: eks.amazonaws.com/role-arn=<CNE_IRSA_ROLE_ARN>
│       persists CNE_SA_NAME to state
├─ 22. CNEInstance CR (Phase22CNEInstance) ◄── slice 7c (Pass 3) *** BURNS F5 LICENSE ***
│       render + SSA-apply cneinstances.k8s.f5.com/v1/CNEInstance <cluster>-bnk in f5-cne-system
│       2-min "reconcile started" gate: polls status.conditions non-empty before proceeding
│       (catches "reconcile never started" pathology early per Architect mandate)
│       --dry-run: logs plan, skips apply+poll, sets placeholder state
│       state keys: CNEINSTANCE_NAME, CNEINSTANCE_APPLIED_AT, CNEINSTANCE_RECONCILE_STARTED_AT
├─ 23. License CR (Phase23License) ◄── slice 7c (Pass 3) *** BURNS F5 LICENSE ***
│       wait up to 10 min for licenses.k8s.f5net.com CRD (installed by FLO operator)
│       render + SSA-apply k8s.f5net.com/v1/License bnk-license in f5-cne-core
│       JWT file is read from cl.Bnk.JWT, trimmed, and inlined as spec.jwt string value
│       state keys: LICENSE_CRD_READY_AT, LICENSE_APPLIED_AT, LICENSE_NAME
├─ 24. CWC DNS-warmup heal (Phase24CWCHeal) ◄── slice 7c (Pass 3)
│       best-effort heuristic: polls cwc pod (app=cwc, f5-cne-core) up to 12× / 15s
│       if restartCount >= 3 (ADR D-011): force-delete pod (GracePeriodSeconds=0) to break loop
│       returns nil regardless — this is a recovery action, not a verification gate
│       --dry-run: logs plan, skips loop, returns nil
├─ 25. Activation poll (Phase25ActivationPoll) ◄── slice 7c (Pass 3) *** LONG WAIT ***
│       initial sleep 30s; poll up to 40× / 30s (= 20 min cap)
│       per iteration: reads cneinstance.status.state + license.status.state + pod counts
│       success: cne ∈ {Ready, Running} AND license=Active → sets CNEINSTANCE_READY_AT
│       timeout: hard error with last-seen states + kubectl diagnostic hint
│       --skip-activation-poll: returns nil immediately (for reviewer re-runs)
│       --dry-run: logs plan, skips poll, returns nil
└─ 13. postflight smoke (Phase13Postflight) ◄── slice 5 (shipped); extended slice 7c
        verify: namespaces, cert-manager ready, CA cert Ready, FAR secrets present
        + FLO Deployment ready, cneinstances.k8s.f5.com CRD, OTEL certs Ready
        + CNEInstance status.state ∈ {Ready, Running} (when CNEINSTANCE_READY_AT set)
        + License bnk-license status.state=Active (when CNEINSTANCE_READY_AT set)
        + warning log when --skip-activation-poll used (CNEINSTANCE_READY_AT absent)
        optional: forge scan_cluster (best-effort)
        NOTE: Phase 13 runs LAST so postflight covers the full installed set.
              call-site ordering: Phase12 → Phase14 → Phase15 → Phase19 → Phase20 → Phase21
                                  → Phase22 → Phase23 → Phase24 → Phase25 → Phase13.
```

> **Removed from the roadmap:** the original TF graph had `terraform/modules/ecr_mirror/`
> and `terraform/modules/s3_supply_chain/` (~281 LOC combined). aws-gpu-setup
> demonstrates BNK works without either — FAR archive + JWT load as k8s Secrets
> from local files, and EKS pulls F5 images directly via the FAR pull secret.
> If a future deployment needs air-gap or image mirroring, model it as an
> opt-in cluster.yaml field (`airGap: true`) in a separate slice — don't
> pre-build it.

> **Phase numbering note:** Phase 01 is reserved; the network phases are 02–06; IAM is 07.
> The original spec had IAM at §6.06 — the actual code uses 07 to leave room for future
> additions between network and IAM without renumbering existing phases.

Each phase function signature:

```go
type Phase func(ctx context.Context, intent *intent.Cluster, st *state.State) error
```

`State` holds:
- All known IDs (VPC ID, subnet IDs, EKS cluster ARN, …)
- Persisted to `state.env` on every successful phase
- Reloaded on re-run for idempotency

---

## 7 · Phase ordering — `down`

Reverse of `up`, with destructive guardrails:

```
down
├─ 00. preflight (interactive confirm unless --yes)
│   ├─ sts.GetCallerIdentity
│   ├─ load state.env; if missing, tag-discovery / name-based fallback
│   └─ unless --keep-forge-link: forge unregister
│
├─ 15. OTEL certs down (Phase15OTELCertsDown) ◄── slice 6 (shipped)
│       delete external-otelsvr + external-f5ingotelsvr Certificates; tolerates NotFound
├─ 25. Activation poll down (Phase25ActivationPollDown) ◄── slice 7c (Pass 3) — no-op
├─ 24. CWC heal down (Phase24CWCHealDown) ◄── slice 7c (Pass 3) — no-op
├─ 23. License CR down (Phase23LicenseDown) ◄── slice 7c (Pass 3)
│       delete k8s.f5net.com/v1/License bnk-license from f5-cne-core; tolerates NotFound
│       (CRD cleanup happens at FLO/CNEInstance down time, not here)
├─ 22. CNEInstance CR down (Phase22CNEInstanceDown) ◄── slice 7c (Pass 3)
│       delete cneinstances.k8s.f5.com/v1/CNEInstance <cluster>-bnk from f5-cne-system
│       tolerates NotFound; waits 30s for finalizer cleanup before returning
├─ 21. IRSA SA down (Phase21IRSASADown) ◄── slice 7b (Pass 2, activation-inert)
│       delete f5-cne-controller-<cluster>-bnk-serviceaccount from f5-cne-system; tolerates NotFound
├─ 20. NADs down (Phase20NADsDown) ◄── slice 7b (Pass 2, activation-inert)
│       delete external-netdevice + internal-netdevice NADs from f5-cne-system + default
│       tolerates NotFound in both namespaces
├─ 19. cloud-network-mapping CM down (Phase19CloudNetworkMappingDown) ◄── slice 7b (Pass 2)
│       delete cloud-network-mapping ConfigMap from f5-cne-system; tolerates NotFound
├─ 14. FLO Helm down (Phase14FLOHelmDown) ◄── slice 6 (shipped)
│       helm uninstall f5-lifecycle-operator from f5-cne-core
├─ 12. k8s foundation down (Phase12K8sFoundationDown) ◄── slice 5 (shipped)
│       cert chain CRs → cert-manager resources → FAR secret from default ns → F5 namespaces
│       (tolerates NotFound everywhere)
├─ 11. kubeconfig down (Phase11KubeconfigDown) ◄── slice 3 (shipped)
│       delete .awsbnkctl/<name>/kubeconfig (best-effort; tolerates absent)
├─ 18. IRSA / OIDC down (Phase18IrsaOidcDown) ◄── slice 7a (Pass 1)
│       delete IRSA role + inline policy; delete OIDC provider (unless --keep-irsa);
│       tolerates "not found" for both; clears OIDC_PROVIDER_ARN + IRSA_ROLE_ARN from state
├─ 17. secondary ENIs down (Phase17SecondaryENIsDown) ◄── slice 7
│       detach ENI_EXT_ID + ENI_INT_ID from TMM instance; delete both ENIs;
│       tolerates missing attachment or absent ENI; clears state keys
├─ 16. tmm node label down (Phase16TMMNodeLabelDown)  ◄── slice 7
│       removes awsbnkctl.f5.com/tmm-node label from the node; clears TMM_INSTANCE_ID;
│       tolerates node-not-found; no-op when K8s client is nil
├─ 10. node group down (Phase10NodeGroupDown) ◄── slice 3 (shipped)
│       delete EKS managed node group + launch template (<cluster>-bnk-lt); wait until gone
├─ 09. forge unregister (Phase09ForgeRegisterDown) ◄── slice 4 (shipped)
│       MCP delete first; REST fallback on catalog gap; --keep-forge-link
│       opt-out preserves the project record
├─ 08. eks cluster down (Phase08EKSClusterDown) ◄── slice 3 (shipped)
│       delete EKS control plane; wait until ResourceNotFoundException
├─ 07. iam down (Phase07IAMDown) ◄── slice 2 (shipped)
│       remove role from profile → delete profile → detach + delete inline
│       policies on node role → delete node role → same for cluster role
├─ 06. route tables down (Phase06RouteTablesDown) ◄── slice 1
├─ 05. nat gateway + EIP down (Phase05NATDown)    ◄── slice 1
├─ 04. igw down (Phase04IGWDown)                  ◄── slice 1
├─ 03. subnets down (Phase03SubnetsDown)          ◄── slice 1
└─ 02. vpc down (Phase02VPCDown)                  ◄── slice 1
```

**Idempotency:** every phase tolerates "already gone" by swallowing the relevant AWS error codes:

| Service | "already gone" codes to swallow |
|---|---|
| ec2 | `InvalidVpcID.NotFound`, `InvalidSubnetID.NotFound`, `InvalidRouteTableID.NotFound`, `InvalidInternetGatewayID.NotFound`, `InvalidNatGatewayID.NotFound`, `InvalidAllocationID.NotFound`, `InvalidNetworkInterfaceID.NotFound` |
| eks | `ResourceNotFoundException` |
| iam | `NoSuchEntity` |

**Post-condition waits** (port from aws-gpu-setup's `lib/lab-core.sh: wait_gone`):
- NAT GW deletion → wait for `State == deleted`
- EIP unassociation → wait for `AssociationId == ""` before release
- ENI detach → poll until ENI is gone before deleting its SG
- EKS cluster delete → wait until `DescribeCluster` returns `ResourceNotFoundException`

---

## 8 · SSO auth sentinel — Go port

aws-gpu-setup's pattern in `lib/lab-core.sh`:
1. Every `aws` CLI call goes through `aws_q`, which captures stderr to a tempfile and grep's for token-expiry strings.
2. On match, `aws_q` writes a sentinel file (`/tmp/aws-gpu-setup.auth-fail.$$`) and returns the error.
3. Every phase begins with `banner`, which calls `check_auth_or_die`, which reads the sentinel and hard-exits with the `aws sso login` hint.

Go port (sketch):

```go
// internal/aws/awsmw/sso.go
package awsmw

var authFail atomic.Bool

// Middleware wraps every SDK client. On ExpiredToken-class errors it sets
// authFail and returns the original error. Phase code calls CheckAuthOrDie()
// at the top of every phase function.
func WithSSOWatch(stack *middleware.Stack) error {
    return stack.Deserialize.Add(middleware.DeserializeMiddlewareFunc("SSOWatch",
        func(ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler) (
            middleware.DeserializeOutput, middleware.Metadata, error,
        ) {
            out, md, err := next.HandleDeserialize(ctx, in)
            if err != nil && isAuthError(err) {
                authFail.Store(true)
            }
            return out, md, err
        }), middleware.After)
}

func isAuthError(err error) bool {
    var ae smithy.APIError
    if !errors.As(err, &ae) { return false }
    switch ae.ErrorCode() {
    case "ExpiredToken", "ExpiredTokenException",
         "InvalidClientTokenId", "UnauthorizedException",
         "InvalidIdentityToken", "AccessDeniedException":
        return true
    }
    return strings.Contains(ae.ErrorMessage(), "SSO session")
}

func CheckAuthOrDie(profile string) {
    if !authFail.Load() { return }
    fmt.Fprintln(os.Stderr, "")
    fmt.Fprintln(os.Stderr, "FATAL: AWS auth failure detected mid-run — refusing to continue.")
    fmt.Fprintln(os.Stderr, "  Re-authenticate, then re-run:")
    fmt.Fprintf(os.Stderr,  "    aws sso login --profile %s\n", profile)
    os.Exit(99)
}
```

Apply via `config.WithAPIOptions` when constructing each AWS service client. Test by injecting a fake `ExpiredToken` error in unit tests.

---

## 9 · Tag scheme

Every AWS resource created by awsbnkctl carries:

| Key | Value | Purpose |
|---|---|---|
| `awsbnkctl:cluster` | `<cluster.metadata.name>` | **Required.** Identifies the cluster a resource belongs to. Drives `down` discovery. |
| `awsbnkctl:component` | `vpc`, `subnet-public`, `subnet-private`, `igw`, `nat`, `rtb`, `eks-cluster`, `eks-nodegroup`, `iam-cluster-role`, `iam-node-role`, `iam-node-profile` | Per-resource category. Lets `inspect` produce structured output. |
| `awsbnkctl:pattern` | `host-device`, `sr-iov-tmm`, … | The data-path pattern used. Stamped on cluster-level resources. |
| `awsbnkctl:managed` | `true` | Marker for any future bulk-listing tool. |
| `Name` | `<cluster.metadata.name>-<component>` | Human-readable AWS console label. |

Any additional tags from `cluster.yaml: tags:` and `metadata.labels:` are merged in. Operator-applied tags on awsbnkctl-created resources are preserved across `up` re-runs (we only update the four `awsbnkctl:*` keys + `Name`).

---

## 10 · IDs cache file format

Path: `.awsbnkctl/<cluster-name>/state.env`. Format: shell-source-compatible `KEY=VALUE` (vars.env parity for human grep, even though Go reads it).

```
# Generated by awsbnkctl on 2026-05-21T15:32:01Z — do not edit by hand
CLUSTER_NAME=jl-gpu-lab
AWS_REGION=ap-southeast-2
VPC_ID=vpc-0abc123def456
IGW_ID=igw-0123abc
PUBLIC_SUBNETS=subnet-0aaa,subnet-0bbb
PRIVATE_SUBNETS=subnet-0ccc,subnet-0ddd
NAT_GW_ID=nat-0xyz
NAT_EIP_ALLOC=eipalloc-0qqq
PUBLIC_RTB=rtb-0pub
PRIVATE_RTB=rtb-0priv
EKS_CLUSTER_ARN=arn:aws:eks:ap-southeast-2:…
EKS_OIDC_URL=https://oidc.eks.ap-southeast-2.amazonaws.com/id/…
NODEGROUP_ARN=arn:aws:eks:…:nodegroup/…
FORGE_PROJECT_ID=42
FORGE_CLUSTER_ID=99
```

**Rules:**
- Written by `up` after every successful phase (so a mid-run failure leaves a valid partial cache).
- Read by `down` first; on missing/corrupt, fall back to tag-discovery.
- In `.gitignore` — never committed.
- `awsbnkctl inspect` prints the cache plus a tag-list diff (drift report).

---

## 11 · Variant manifest layout

```
internal/k8s/manifests/
├── shared/
│   ├── bnk-cert-chain.yaml
│   ├── far-pull-secret.yaml
│   ├── license-cr.yaml
│   └── otel-certs.yaml
├── host-device/
│   ├── cloud-network-mapping.yaml
│   ├── cneinstance.yaml
│   ├── f5spkvlan.yaml
│   ├── flo-values.yaml
│   ├── gatewayclass.yaml
│   └── network-attachment-defs.yaml
└── sr-iov-tmm/                 # planned, mirrors host-device with SR-IOV plumbing
    └── … (same filenames; different bodies)
```

- Embedded via `go:embed all:manifests` in `internal/k8s/render/embed.go`.
- Each YAML is a Go `text/template` rendered against a typed struct derived from `cluster.yaml`. Placeholders use Go's `{{ .Field }}` syntax (NOT envsubst — we have a strict Go SDK).
- Apply order: walk `shared/` first (alphabetical within), then walk `<pattern>/` (alphabetical within).
- Apply via existing `internal/k8s/apply.go` (client-go server-side apply).
- After apply, stamp the namespace with `awsbnkctl.io/pattern: <pattern>` label so forge GUI can surface it.

---

## 12 · Subcommand surface (post-TF)

| Command | Purpose | Notes |
|---|---|---|
| `awsbnkctl init` | Drop a `cluster.yaml` skeleton + `.awsbnkctl/` workspace dir | Optionally drop AGENTS.md (dpubnkctl pattern) |
| `awsbnkctl up [--config cluster.yaml] [--dry-run] [--phase N]` | Run all phases, or up to phase N, or print what would happen | `--phase N` lets you resume / debug |
| `awsbnkctl down [--yes] [--keep-iam] [--keep-keypair] [--skip-bnk] [--keep-forge-link] [--purge]` | Reverse-order destroy | `--purge` also removes `.awsbnkctl/<name>/` after success |
| `awsbnkctl status` | Tag-driven AWS query → table of resources for this cluster | NEVER asks forge |
| `awsbnkctl doctor` | Deeper health check: AWS + cluster reachability + BNK CR status | Uses Go SDK + client-go directly |
| `awsbnkctl inspect` | Print state.env + tag-list diff | Drift report |
| `awsbnkctl forge register / unregister / status` | Existing forge commands | Caller of `internal/forge/register.go` |
| `awsbnkctl k <kubectl-equivalent>` | Existing kubectl multiplexer | Unchanged |
| `awsbnkctl install / test` | Existing install + test commands | Unchanged |

**Removed:**
- `awsbnkctl plan` — fold into `up --dry-run`
- `awsbnkctl tfvars` — gone with TF
- `awsbnkctl cluster` (the old TF cluster subcommands) — superseded by `up`/`down`/`status`

---

## 13 · Tracer-bullet first slice (smallest deliverable)

**Scope:** `cluster.yaml` → tagged VPC + subnets + IGW + NAT + RTs via Go SDK, symmetric `up`/`down`, IDs cache write, idempotent re-run. **No EKS, no IAM, no k8s, no forge.**

**Deliverable PR contents:**

1. `internal/intent/cluster.go` — Cluster struct + YAML loader + validation.
2. `internal/aws/awsmw/sso.go` — auth sentinel middleware + `CheckAuthOrDie`.
3. `internal/aws/tags/tags.go` — tag constants + helpers.
4. `internal/aws/state/state.go` — `state.env` reader/writer + tag-discovery fallback.
5. `internal/aws/phases/phase00_preflight.go` — preflight phase.
6. `internal/aws/phases/phase02_vpc.go` through `phase05_routetables.go` — the five network phases.
7. `internal/cli/up.go` — replaces TF up; loads intent, runs phases through 05.
8. `internal/cli/down.go` — destroys phases 05→01 in reverse.
9. `examples/tracer/cluster.yaml` — minimal working example.
10. `internal/aws/phases/*_test.go` — unit tests with mocked SDK clients (one per phase + idempotency test that runs `up` twice).

**Acceptance criteria:**
- `awsbnkctl up --config examples/tracer/cluster.yaml` provisions a tagged VPC + subnets + IGW + NAT in a real account.
- Re-running `up` is a no-op (every phase reports "already exists, skipping").
- `.awsbnkctl/tracer/state.env` exists with all expected IDs.
- `aws ec2 describe-vpcs --filter Name=tag:awsbnkctl:cluster,Values=tracer` lists the VPC.
- Deleting `.awsbnkctl/tracer/state.env` and running `down` still works (tag-discovery fallback).
- `awsbnkctl down --yes` removes everything; second `down` is a no-op.
- Mid-run SSO expiry produces the auth-sentinel hard-exit with the `aws sso login` hint, not silent no-op.

**Slice roadmap (updated 2026-05-22 post slice-07c Pass 3):**
- ✅ Slice 1 — VPC + subnets + IGW + NAT + RTs (tracer bullet) **[shipped, PR #8]**
- ✅ Slice 2 — IAM cluster role + node role + instance profile **[shipped, PR #8]**
- ✅ Slice 3 — EKS cluster + node group + kubeconfig **[shipped, PR #11]**
- ✅ Slice 4 — Phase 09 forge register (MCP-first, REST fallback, soft-fail) **[shipped, PR #11]**
- ✅ Slice 5 — K8s install foundation (shipped): namespaces + FAR/JWT Secrets + cert-manager v1.16.1 + BNK cert chain. Phase 12 (foundation) + Phase 13 (postflight) in up order; Phase12K8sFoundationDown first in destroy order. Variant manifest scaffold `internal/k8s/manifests/{shared,host-device,sr-iov-tmm}/` in place.
- ✅ Slice 6 — FLO Helm install + OTEL certs (shipped, PR #14): Phase 14 (FLO Helm install via OCI FAR-key auth) + Phase 15 (OTEL Certificate CRs). Phase ordering: Phase12 → Phase14 → Phase15 → Phase13. Down order: Phase15Down → Phase14Down → Phase12Down. Adds `helm.sh/helm/v3` as a direct dep.
- ✅ Slice 7a — BNK activation prerequisites Pass 1 (shipped): Phase 16 (TMM node label), Phase 17 (secondary ENIs for host-device data path), Phase 18 (OIDC provider + IRSA role for f5-cne-controller). Activation-inert. Adds `network.dataPath` to cluster.yaml schema, `--keep-irsa` down flag.
- ✅ Slice 7b — BNK k8s prerequisites Pass 2 (shipped): Phase 19 (cloud-network-mapping CM), Phase 20 (host-device NADs in f5-cne-system + default), Phase 21 (IRSA SA pre-creation). Activation-inert.
- ✅ Slice 7c — BNK activation Pass 3 (shipped, in review): Phase 22 (CNEInstance CR + 2-min reconcile gate), Phase 23 (License CRD wait + License CR), Phase 24 (CWC DNS-warmup heal), Phase 25 (20-min activation poll). **THIS IS THE ONLY PASS THAT BURNS A REAL F5 LICENSE.** Adds `--skip-activation-poll` up flag. Phase ordering: ... → Phase21 → Phase22 → Phase23 → Phase24 → Phase25 → Phase13. Down order: Phase25Down → Phase24Down → Phase23Down → Phase22Down → Phase21Down → ...
- ⏳ Slice 8 — Polish: `inspect`/`doctor`/`status` reading state.env + tag scheme. Deletion of `terraform/` + `internal/tf/` + `embedded.go`. SR-IOV TMM pattern (variant manifests). Optionally split commands per kindbnkctl pattern (D-009).
- ⏳ Future (separately scoped, not in the slice plan):
  - Air-gap / ECR mirror — opt-in via cluster.yaml `airGap: true`; only build if/when a deployment requires it.
  - Multi-cluster workspace
  - Scenarios framework port from kindbnkctl (D-009 reference)

---

## 14 · Test strategy (sketch, deferred for own session)

Two layers planned, not yet detailed:
- **Unit:** SDK mocks via `aws-sdk-go-v2`'s middleware injection. Every phase function has a unit test that simulates "already exists," "create succeeds," "auth expired mid-run." Pattern matches the existing `internal/aws/*_test.go`.
- **Integration:** Real-account harness, region-scoped, cluster-name-prefixed (`tracer-ci-<sha>`). Skipped without `AWSBNKCTL_INTEGRATION=1` + valid SSO session. Tear-down in `t.Cleanup`. Aws-gpu-setup's `tests/` directory worth surveying for prior art.

---

## 15 · What we explicitly are NOT doing

- **Not** wrapping aws-gpu-setup as a subprocess. We adopt the *method*; we don't ship the bash.
- **Not** building a reconciler abstraction (`Reconcile()` interface, dependency graph engine). Sequential phase functions are sufficient and far easier to debug.
- **Not** adding a `--native` feature flag to coexist TF and Go paths. Greenfield build per user direction; current TF clusters cleaned up manually.
- **Not** changing the forge MCP integration model. Existing `docs/FORGE_MCP_INTEGRATION.md` stands; only the *timing* of register changes.
- **Not** introducing two-factor `--confirm-cluster` typo protection. The single confirm + per-cluster `.awsbnkctl/<name>/` directory is enough.

---

## 16 · Open questions (resolve before slice ships)

- `cluster.yaml`: should `network.azs` default from `metadata.region` (auto-pick first N) or stay strictly explicit? Current spec says explicit; revisit if it bites.
- IRSA OIDC URL: passed via `*State` struct between phases — confirm the EKS Go SDK returns the URL in `DescribeCluster.Identity.Oidc.Issuer` (it does) and that's our source.
- Helm SDK vs raw client-go for FLO / cert-manager install: deferred to k8s-side slice. Default lean is raw `unstructured.Unstructured` + Server-Side Apply via existing `internal/k8s/apply.go`. Revisit if FLO requires Helm-specific upgrade semantics.
- `awsbnkctl init` content: should it drop AGENTS.md + persona files (dpubnkctl style)? Likely yes — confirm when building `init`.

---

## Phase 17b: jumphost (slice 12)

Phase 17b provisions a multi-ENI EC2 jumphost + EC2 Instance Connect Endpoint (EICE) inside the cluster VPC. It runs in `runPhasedUp` between Phase 17 (secondary ENIs) and Phase 18 (IRSA OIDC), and tears down in `runPhasedDown` between Phase 18 down and Phase 17 down.

Feature gate: `testing.jumphost.enabled: true` in cluster.yaml. Default off — existing cluster.yaml files without a `testing:` block are unaffected.

### Resources provisioned (when enabled)

| Resource | Name | Tags |
|----------|------|------|
| IAM role | `<cluster>-jumphost-role` | `awsbnkctl:component=jumphost-iam-role` |
| IAM instance profile | `<cluster>-jumphost-profile` | `awsbnkctl:component=jumphost-instance-profile` |
| Security group | `<cluster>-jumphost` | `awsbnkctl:component=jumphost-sg` |
| EC2 Instance Connect Endpoint | `<cluster>-jumphost-eice` | `awsbnkctl:component=jumphost-eice` |
| EC2 instance (t3.small, AL2023) | `<cluster>-jumphost` | `awsbnkctl:component=jumphost-instance` |
| Secondary ENI (BNK_EXT subnet) | `<cluster>-jumphost-bnk-ext` | `awsbnkctl:component=jumphost-eni-bnk-ext` |

### state.env keys

`JUMPHOST_INSTANCE_ID`, `JUMPHOST_MGMT_ENI_ID`, `JUMPHOST_MGMT_ENI_IP`, `JUMPHOST_BNK_EXT_ENI_ID`, `JUMPHOST_BNK_EXT_ENI_IP`, `JUMPHOST_EICE_ID`, `JUMPHOST_EICE_SG_ID`, `JUMPHOST_SG_ID`, `JUMPHOST_AMI_ID`, `JUMPHOST_INSTANCE_TYPE`, `JUMPHOST_INSTANCE_PROFILE_NAME`, `JUMPHOST_ROLE_NAME`.

### SG_BNK_DATA cross-reference ordering

The secondary ENI joins `SG_BNK_DATA` (created by Phase 07) so its traffic reaches TMM SelfIPs. The teardown ordering guarantees no "SG in use" errors: Phase 17b down (instance terminate → secondary ENI delete) runs BEFORE Phase 17 down (TMM ENIs) BEFORE Phase 07 down (SG_BNK_DATA delete). No changes to SG_BNK_DATA are needed; the existing intra-SG self-ingress rule covers the jumphost secondary ENI automatically.

### AMI resolution

The latest AL2023 x86_64 AMI is resolved at runtime via SSM Parameter Store: `/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64`. The resolved AMI ID is cached in `JUMPHOST_AMI_ID` for idempotent re-runs.

---

## 17 · Acknowledgements

- The aws-gpu-setup PoC (`/Users/j.lucia/Code/aws-gpu-setup/`) is the proof that an imperative awscli + YAML method works for this problem. Treat that repo as a reference implementation we are porting (not vendoring) into Go.
- The dpubnkctl architectural direction (mwiget/dpubnkctl) is the long-term polestar. Patterns to keep adopting as awsbnkctl evolves: PoC-as-repo state model, examples/ as named topologies, AGENTS.md numbered-gotcha catalog, validate command, journal/decisions.md.
