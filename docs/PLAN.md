# Phased development & testing plan

Execution plan for `awsbnkctl` — the AWS-retargeted port of [`roksbnkctl`](https://github.com/jgruberf5/roksbnkctl). Synthesises the PRDs in [`docs/prd/`](./prd/) into sequenced work, development and testing interleaved per sprint. References the PRDs by number; read those for the *what*, this for the *when* and *how*.

> **Fork inheritance.** This plan starts from a hard fork of `jgruberf5/roksbnkctl@v1.2.1`. The Go scaffolding (cobra CLI, terraform-exec wrapper, client-go k8s wrapper, miekg/dns probe, four-role sprint pattern, mdBook framework, doctor framework, execution backends, cred resolver) is **inherited intact**. Sprint 0 strips IBM-specific surface; subsequent sprints layer AWS-specific surface on top. The roksbnkctl PRDs 01-05 (SSH/`--on`, internalised `kubectl`, execution backends, credentials, E2E plan) are inherited and their implementations carried forward; only `06-CLUSTER-TRIAL-PHASE-SPLIT.md` and the to-be-authored PRDs 07+ (EKS cluster, S3 supply chain, SR-IOV data plane) are net-new.

## Goals & top-level milestones

| Milestone | Tag | Outcome |
|---|---|---|
| **M0** | _(no tag)_ | Sprint 0: identity rewrite complete; `cmd/awsbnkctl/` builds green; IBM-specific surface stripped or stubbed; CI matrix passes on Linux + macOS. |
| **M1** | `v0.2` | Sprint 1: `terraform/modules/eks_cluster/` stands up an EKS cluster with a self-managed node group on ENA-enabled instances (`c5n`/`m5n`); Multus + SR-IOV CNI + SR-IOV device plugin land via DaemonSets; nodes advertise SR-IOV VFs to the scheduler. |
| **M2** | `v0.3` | Sprint 2: S3 supply chain (FAR pull keys, JWT licence, optional ECR mirror) replaces IBM COS; IRSA via the EKS OIDC provider replaces the IBM Trusted Profile workload-identity wiring. |
| **M3** | `v0.5` | Sprint 3: `cert_manager`, `flo`, `cne_instance`, `license` modules ported to consume AWS-shaped inputs (S3 bucket, IRSA role ARN). `awsbnkctl up` runs the full lifecycle against a real AWS account; BNK comes up healthy. |
| **M4** | `v0.7` | Sprint 4: `awsbnkctl test` (DNS + connectivity + throughput) passes against the AWS deployment; `awsbnkctl doctor` reports AWS-shaped checks (STS caller-identity, EKS describe-cluster permissions, instance-type quotas). |
| **M5** | `v0.9` | Sprint 5: book retargeted at AWS — every chapter that references IBM-Cloud-specific behaviour rewritten; new chapters for the SR-IOV node-group decision and the S3/ECR supply chain. Web book published at `https://JLCode-tech.github.io/awsbnkctl/book/`. |
| **M6** | `v1.0` | Sprint 6: E2E phases pass on a stock dev host (no `kubectl`/`aws` installed); security review clean; first stable release with goreleaser-built binaries for Linux/macOS/Windows × amd64/arm64. |

Estimated calendar time: **~13 weeks** (Sprint 0 ≈ 1 week + six 2-week sprints) for a single focused engineer who already knows EKS, Terraform, and the roksbnkctl codebase. Doubling for "real-world with reviews, distractions, integration debt, and SR-IOV surprises" puts M6 around **6 months out**.

### What we inherit (vs. roksbnkctl's 14-week build-from-scratch plan)

awsbnkctl's plan is shorter than roksbnkctl's because the following work is **already done** in the inherited tree and does not need redoing — only retargeting where it touches cloud-specific surface:

- SSH client + `--on` flag (roksbnkctl PRD 01)
- Internalised `kubectl` via client-go (roksbnkctl PRD 02)
- Execution backends: local, docker, k8s, ssh (roksbnkctl PRD 03)
- Credential resolver chain (roksbnkctl PRD 04) — needs AWS adapter
- E2E test framework (roksbnkctl PRD 05) — needs AWS test phases
- Workspace config + cobra CLI scaffolding
- mdBook framework + `make book` / `make book-serve` targets
- goreleaser config + release CI workflow
- Doctor framework — needs AWS-shaped checks

## Phase overview — sequencing decisions

```
┌────────────────────────────────────────────────────────────────────┐
│ Sprint 0 (week 0)        Identity rewrite + IBM strip              │
│                          README/CHANGELOG/MIGRATING done; module   │
│                          path + cmd/ rename; remove internal/ibm,  │
│                          internal/cos, terraform/modules/roks_*;   │
│                          stub internal/aws; green build + CI       │
├────────────────────────────────────────────────────────────────────┤
│ Sprint 1 (weeks 1-2)     EKS cluster + self-managed SR-IOV node    │
│                          group (PRD 07 - to author). Spike first:  │
│                          can we get SR-IOV VFs schedulable on a    │
│                          c5n.4xlarge node? Then productise.        │
│   ↓                                                                │
│ Sprint 2 (weeks 3-4)     S3 supply chain + IRSA (PRD 08 - to       │
│                          author). FAR pull-key upload, JWT licence │
│                          handling, optional ECR mirror, FLO        │
│                          service-account → IAM role binding.       │
│   ↓                                                                │
│ Sprint 3 (weeks 5-6)     Port the four reusable modules:           │
│                          cert_manager, flo, cne_instance, license. │
│                          Swap IBM inputs for AWS inputs; first     │
│                          end-to-end `awsbnkctl up` against AWS.    │
│   ↓                                                                │
│ Sprint 4 (weeks 7-8)     Test surface + doctor: AWS-shaped checks, │
│                          DNS/connectivity/throughput tests pass    │
│                          against the AWS deployment, AWS-flavoured │
│                          E2E phases.                               │
│   ↓                                                                │
│ Sprint 5 (weeks 9-10)    Book retarget: rewrite all IBM-specific   │
│                          chapters, add SR-IOV + S3/ECR chapters,   │
│                          publish at GitHub Pages.                  │
│   ↓                                                                │
│ Sprint 6 (weeks 11-12)   Hardening, security review, E2E gate,     │
│                          v1.0 cut with goreleaser binaries.        │
└────────────────────────────────────────────────────────────────────┘
```

**Dependency rationale:**
- The SR-IOV node group (Sprint 1) blocks every subsequent BNK deployment — if it can't work on EKS without surprise, the whole plan needs to revisit the data-plane decision.
- S3 + IRSA (Sprint 2) shapes how `flo` and `cne_instance` modules are parameterised in Sprint 3, so it must precede module porting.
- Module porting (Sprint 3) is the first sprint that produces a working `awsbnkctl up`. Everything before is enablement.
- Test surface (Sprint 4) gates the v0.7 milestone and surfaces correctness issues that drive Sprint 5/6 polish.
- Book retarget (Sprint 5) is large but mostly mechanical — done in one focused sprint so dev sprints aren't slowed by chapter writes.
- Hardening (Sprint 6) is the final v1.0 gate.

---

## Sprint 0 — identity rewrite + IBM strip (week 0)

### Goal

Make the fork look and build like `awsbnkctl`: module path, binary name, repo identity, CI green. Strip the IBM-specific surface so subsequent sprints layer AWS-specific surface against a clean base.

### Code deliverables

| Item | Detail |
|---|---|
| Identity rewrite | `README.md`, `CHANGELOG.md`, `MIGRATING.md` (done, uncommitted at the time of writing) |
| Go module path | `go.mod`: `module github.com/JLCode-tech/awsbnkctl`. Update every import; `gofmt`/`go vet` clean. |
| Binary rename | `cmd/roksbnkctl/` → `cmd/awsbnkctl/`. Update `Makefile`, `.goreleaser.yml`, `embedded.go`, CI workflows. |
| IBM strip | Delete `internal/ibm/`, `internal/cos/`, `terraform/modules/roks_cluster/`. Delete `tools/docker/ibmcloud/`. |
| AWS stub | Empty `internal/aws/` package with a doc.go explaining future scope. Empty `terraform/modules/eks_cluster/` with a placeholder `main.tf` that errors out cleanly. |
| Variable + reference cleanup | Strip `ibmcloud_*` variables from `terraform/variables.tf`; replace top-level `main.tf` module wiring with TODO stubs that fail-loud rather than silently misbehave. |
| Build green | `make build && make test && go vet ./...` all pass. The binary doesn't *do* anything yet — but it builds, parses flags, and prints help. |

### Documentation deliverables

| Item | Detail |
|---|---|
| `README.md` / `CHANGELOG.md` / `MIGRATING.md` | Already drafted in this fork's working tree; commit alongside the rename. |
| `docs/PLAN.md` | This file. |
| `docs/prd/00-OVERVIEW.md` | Inherited from roksbnkctl; rewrite the top section to frame awsbnkctl's scope (AWS, not IBM Cloud). PRDs 01-05 stay inherited; PRD 06 stays inherited; flag PRDs 07-08 as "to author in Sprints 1-2". |
| `agents/` | Inherited unchanged — the role definitions are tool-agnostic. |
| `prompts/sprint0/{architect,staff,validator,tech-writer}.md` | Author from scratch using roksbnkctl's `prompts/sprint0/` as the template. Sprint scope = identity rewrite + IBM strip + AWS stub. |

### Test deliverables

- Existing test suite still green after the strip (most IBM-specific tests deleted alongside their packages; the `internal/cli`, `internal/tf`, `internal/k8s` test suites should pass unchanged).
- CI matrix runs: Linux + macOS, `go test`, `gofmt`, `go vet`, `staticcheck`.
- `mdbook build book/` still passes (chapter content will be rewritten in Sprint 5; the framework stays).

### Gate to Sprint 1

- `awsbnkctl --help` runs and prints the expected command list.
- `awsbnkctl doctor` runs (may report "not implemented for AWS yet" — but no panics).
- No `ibmcloud` / `roks` / `cos` string references remain in the build path (allowed in `book/` and `CHANGELOG.md` historical sections).
- A working tree on `main` with one or more commits attributable to "fork + retarget".

### Risks

- Import-path rewrite touches every Go file. Budget half a day; use a single `gofmt -w` + `find . -name '*.go' -exec sed -i …` pass, then `go build` to find stragglers.
- Some inherited tests assume IBM environment vars or live ROKS connectivity. Skip with `t.Skip("requires AWS retarget in Sprint N")` rather than deleting — flag for replacement in the relevant later sprint.

---

## Sprint 1 — EKS cluster + SR-IOV node group (PRD 07 — to author)

### Goal

Stand up an EKS cluster with a self-managed node group that exposes SR-IOV VFs to the scheduler. Validate the load-bearing data-plane decision before any further work depends on it.

### Spike (days 1-3)

Before productising in Terraform, hand-stand-up the target shape:

1. Create an EKS cluster (1.30+) via `eksctl` or raw `aws` CLI.
2. Add a self-managed node group on `c5n.4xlarge` (or `m5n.4xlarge`) with a custom launch template that exposes ENA + the SR-IOV-capable ENIs.
3. Install Multus (`multus-cni`) + SR-IOV CNI + SR-IOV device plugin via the upstream manifests.
4. Verify VFs appear as `intel.com/sriov` (or whatever `sriov_resource_name` the eks_cluster module is configured with) in `kubectl describe node`.
5. Schedule a test pod that requests one VF; confirm it gets an additional interface from the SR-IOV pool.

If the spike fails or surfaces blockers (e.g., AWS ENI VF semantics don't match BNK's expectations), pause and revisit the data-plane decision in PRD 07.

### Productisation (days 4-10)

| Item | Detail |
|---|---|
| `terraform/modules/eks_cluster/` | Wraps `terraform-aws-modules/eks/aws ~> 20.x`. Inputs: `region`, `cluster_name`, `cluster_version`, `vpc_id` (or create-new), `node_instance_types` (default `["c5n.4xlarge"]`), `node_min_size`, `node_max_size`. Outputs: `cluster_name`, `cluster_endpoint`, `cluster_ca_data`, `oidc_provider_arn`, `cluster_ready_id`. |
| Self-managed node group | Custom launch template (`aws_launch_template`) with ENA + SR-IOV ENI configuration; uses the EKS-optimised AL2023 AMI for the cluster K8s version. |
| Multus + SR-IOV stack | Deployed via the `kubernetes` provider as part of the module: Multus DaemonSet, SR-IOV CNI DaemonSet, SR-IOV device plugin DaemonSet with a `ConfigMap` declaring the VF pool. |
| `internal/aws/` | aws-sdk-go-v2 client for STS (caller identity), EC2 (describe instance type availability + quotas), EKS (describe-cluster, fetch kubeconfig post-apply). |
| `awsbnkctl up cluster` | Drives `terraform/modules/eks_cluster/` only. `awsbnkctl up` (no subcommand) still gated — Sprint 3 closes the full lifecycle. |

### Documentation deliverables

- `docs/prd/07-EKS-CLUSTER-SRIOV.md` — PRD authored in parallel with the spike; codifies the SR-IOV-on-self-managed-nodes decision, the instance-type matrix, the upgrade path implications.

### Test deliverables

- Spike post-mortem written into the PRD (what worked, what surprised).
- Integration test: `awsbnkctl up cluster` against a real AWS account in a CI-controlled sub-account; cluster reports `Ready`; SR-IOV device plugin advertises VFs.
- Unit tests for the new `internal/aws/` helpers (STS caller identity, region/AZ enumeration).

### Gate to Sprint 2

- A `c5n.4xlarge` node in the cluster reports SR-IOV VFs as schedulable resources.
- `awsbnkctl up cluster --workspace test-sriov` is idempotent (rerun is a no-op when state is current).
- PRD 07 committed; spike learnings folded in.

### Risks

- **AWS ENI VF semantics may not match what BNK expects.** AWS exposes "Elastic Network Adapter" SR-IOV, which is not always interchangeable with the Mellanox-flavoured SR-IOV that BNK is typically validated against. Surface this in the spike; if BNK rejects the VF, fall back plan is the `vpc-cni` + multi-ENI shape (worse perf but possibly sufficient).
- Quota limits on `c5n` / `m5n` family in greenfield AWS accounts often hit `Running On-Demand instances` limits at low numbers. Doctor should pre-flight check.
- Launch-template AMI lifecycle is now user-managed. Document the upgrade path.

---

## Sprint 2 — S3 supply chain + IRSA (PRD 08 — to author)

### Goal

Replace the IBM COS supply chain with S3 (or ECR mirror) for FAR pull keys, JWT licence, and any other BNK artefact. Wire FLO's service account to an IAM role via IRSA so no static API key lives in cluster.

### Code deliverables

| Item | Detail |
|---|---|
| `terraform/modules/s3_supply_chain/` | S3 bucket (server-side encrypted with `aws:kms`), bucket policy restricting access to the FLO IRSA role, `aws_s3_object` resources for FAR pull-keys + JWT licence. Inputs: `bucket_name`, `kms_key_arn`, `far_repo_url`, `jwt_file_local_path`, `far_auth_file_local_path`. |
| `terraform/modules/ecr_mirror/` (optional) | Stretch: mirror FAR images into ECR via `aws_ecr_repository` + a `null_resource` running `skopeo copy`. Drives the v1.x ECR option without blocking v0.5. |
| `terraform/modules/iam_irsa/` | IAM OIDC provider (referencing the EKS OIDC URL from Sprint 1), trust-policy-shaped role for FLO. Outputs: `flo_role_arn` consumed by the `flo` module in Sprint 3. |
| `internal/aws/s3.go` | Wraps aws-sdk-go-v2 S3 for put/get; used by `awsbnkctl init` to upload local pull-key + JWT files. |
| `internal/aws/iam.go` | OIDC provider lookup, role existence checks for doctor. |
| `awsbnkctl init` AWS path | Wizard prompts: region, VPC, instance types, bucket name (default derived from workspace), and either "upload my local FAR tarball" or "I'll point at an existing S3 path". |

### Documentation deliverables

- `docs/prd/08-S3-SUPPLY-CHAIN-IRSA.md`.
- Note in CHANGELOG: the FAR artefacts that previously lived under `keys/` (per roksbnkctl convention) now live in S3; local-only fallback is a `tf_source.local` mode.

### Test deliverables

- Integration test: upload a dummy FAR tarball to S3 via `awsbnkctl init`; assert it's readable via the IRSA role from an in-cluster pod.
- Unit tests for `internal/aws/s3` and `internal/aws/iam`.

### Gate to Sprint 3

- A pod running under the FLO service account can read the S3-stored FAR + JWT artefacts using IRSA (no static credentials in the pod spec).
- PRD 08 committed.

### Risks

- IRSA trust policy is fiddly — typos in the OIDC subject claim are silent failures. Add `doctor` checks that test the assume-role flow end-to-end.
- BNK's licence artefact format hasn't (as of this writing) been validated against an S3 source by F5 directly. Spike: read the FLO source to confirm it accepts an HTTPS URL (S3 presigned) for licence pulls; if not, the `cne_instance` module needs a download-step shim.

---

## Sprint 3 — port the four reusable modules; first end-to-end `up` (weeks 5-6)

### Goal

`awsbnkctl up` runs end-to-end against an AWS account and lands a healthy BNK deployment. The four "reusable" Terraform modules — `cert_manager`, `flo`, `cne_instance`, `license` — get parameter-swapped to consume AWS inputs.

### Code deliverables

| Item | Detail |
|---|---|
| `terraform/modules/cert_manager/` | Port: replace `ibmcloud_*` inputs with `aws_*` equivalents (region, role ARNs). Module body is unchanged (pure k8s manifests). |
| `terraform/modules/flo/` | Port: swap `ibmcloud_cos_*` for `s3_bucket_name` + `flo_role_arn` (IRSA). FLO values rendered to point at S3 URLs instead of COS endpoints. |
| `terraform/modules/cne_instance/` | Port: takes `flo_namespace`, `flo_trusted_profile_id` → renamed to `flo_irsa_role_arn`. CNEInstance CRD body unchanged. |
| `terraform/modules/license/` | Port: licence JWT pulled from S3 (presigned URL) instead of COS. |
| `terraform/modules/testing/` | Port: replaces `roks_transit_gateway_name` input with `aws_vpc_id` + `aws_subnet_ids`. |
| Top-level `terraform/main.tf` | Wire `eks_cluster` → `cert_manager` / `iam_irsa` / `s3_supply_chain` → `flo` → `cne_instance` → `license` / `testing`. Mirrors roksbnkctl's dependency graph. |
| `awsbnkctl up` (full lifecycle) | Runs init → plan → confirm → apply → fetch kubeconfig → wait for CNEInstance to report `Ready`. |
| `awsbnkctl down` | Runs the reverse: destroy modules in dependency-graph order, with stuck-finalizer cleanup baked in (lifted from roksbnkctl). |

### Documentation deliverables

- Update `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` if the phase-split semantics changed.
- Mark PRDs 07 + 08 as `Resolved in Sprint 3` where Sprint 3 closes their consumption.

### Test deliverables

- End-to-end smoke test on a CI-controlled AWS sub-account: `awsbnkctl init → up → kubectl get cneinstance → down`. Total runtime budget: ~30 min on EKS.
- Unit tests for terraform-exec wrapper updates (new inputs, new module wiring).

### Gate to Sprint 4

- `awsbnkctl up` on a clean AWS account completes inside 30 minutes and lands a healthy `CNEInstance`.
- `awsbnkctl down` on the same workspace returns the account to clean state (no leaked S3 objects, IAM roles, ENIs, or VPCs).

### Risks

- Module wiring order is fragile — terraform's dependency graph cares about explicit `depends_on` for cross-module readiness gates. roksbnkctl's pattern (each module exports a `ready_id` consumed downstream) carries over verbatim.
- BNK CNEInstance reconciliation can take 8-15 min; CI test timeouts need explicit budget.

### Sprint 3 close (actual)

What shipped (architect surface — confirmed at this commit):

- **PRD 04 update** — added a top-level §"Resolved in Sprint 3" section documenting the AWS standard credential chain (env / profile / SSO / IMDS / container / web-identity), the IRSA replacement of the upstream IBM Trusted Profile path, the AWS-retargeted backend × credential matrix, the AWS-shaped doctor rows, and the migration steps from `roksbnkctl`. The inherited "Resolved in Sprint 9" IBM-cloud sections are retained as historical context (the upstream's v1.2 surface).
- **PRD 08 versioning correction** — Decision § now documents that `aws_s3_bucket_versioning.status = "Enabled"` ships unconditionally (no `var.enable_bucket_versioning` toggle), with the audit-trail-outweighs-storage-cost rationale and a corresponding entry in the Trade-offs accepted section. Resolves the Sprint 2 tech-writer Issue 2 PRD ↔ module drift.
- **Chapter 26 first-pass** — `book/src/26-troubleshooting.md` replaces the inherited IBM-flavoured catalogue with ~1,700 words of AWS-shaped symptom → root-cause → fix entries across cluster + node group (SR-IOV VF advertisement, CNEInstance pending), AWS creds + auth (STS chain resolution, IRSA `AccessDenied`), EKS access (kubeconfig context, access-entry mapping), terraform + quotas (vCPU `VcpuLimitExceeded`, two-AZ subnet requirement, orphan ENI cleanup), and CI-specific (provider-cache invalidation, cred-leak audit). Cross-links to chapters 23, 25, 33 + PRD 04.
- **Chapter 25 cross-link refresh** — chapter 25's chapter-26 cross-reference now points at a concrete section (`§"AWS credentials + auth"`) instead of the prior forward-reference framing.

What was deferred (carries into Sprint 4 or later):

- **Operator-run spike** (PRD 07 § Spike status) still gates `v0.2`. Sprint 3 closes offline work; live `awsbnkctl apply` against AWS waits on the operator.
- **Doctor refresh** (`internal/doctor/aws.go`) is Sprint 4. The AWS-shaped doctor rows the PRD 04 update documents land then; PRD 04 codifies the contract Sprint 4 implements.
- **ECR mirror v1.x first-class** (PRD 08 § Trade-offs).
- **Chapter 14 deep rewrite** (current chapter targets `IBMCLOUD_API_KEY`; Sprint 5 retargets at the AWS chain) — the PRD 04 cred-chain section is the design surface that rewrite will draw from.
- **Chapter 25 filename rename** (`25-cos-supply-chain.md` → `25-s3-supply-chain.md`) — Sprint 5 architect, per Sprint 2 architect Issue 2 cascade plan.

Sibling agent surfaces (staff terraform module ports, validator CI matrix, tech-writer read-only pass) report independently; see the per-agent issue files at `issues/issue_sprint3_{staff,validator,tech-writer}.md` for what landed on those surfaces. The integrator reconciles at sprint close.

---

## Sprint 4 — test surface + doctor (weeks 7-8)

### Goal

`awsbnkctl test` and `awsbnkctl doctor` give AWS-shaped feedback. DNS / connectivity / throughput tests pass against the Sprint 3 deployment.

### Code deliverables

| Item | Detail |
|---|---|
| `internal/doctor/aws.go` | New checks: AWS credentials present (env / profile / instance role / SSO); STS caller-identity resolves; EKS describe-cluster permission; EC2 quota for `c5n` / `m5n` family in the chosen region; S3 PutObject permission on the workspace bucket. |
| `internal/test/throughput.go` | Inherited from roksbnkctl; verify the `--backend k8s` Job pattern works on EKS (no OpenShift-specific SCC assumptions). |
| `internal/test/dns.go` | Inherited; the multi-vantage probe works against AWS-hosted GSLB targets without modification. Add an AWS-flavoured vantage if needed. |
| `internal/test/connectivity.go` | Inherited; validate against EKS LoadBalancer service shape (ALB/NLB). |
| `awsbnkctl test` AWS-aware | Plumb workspace region + cluster outputs through so the test surface picks them up automatically. |

### Documentation deliverables

- Update the troubleshooting chapter (Sprint 5 will fold these into the book) with common Sprint 4-surfaced issues.

### Test deliverables

- All three test verbs (`dns`, `connectivity`, `throughput`) pass against a Sprint 3 deployment.
- Doctor reports green on a stock dev box with only `terraform` and AWS credentials installed.

### Gate to Sprint 5

- `awsbnkctl doctor` reports green on the CI runner.
- `awsbnkctl test` passes end-to-end on the Sprint 3 deployment.

### Risks

- OpenShift's SCC-based pod security (which roksbnkctl's tools image accommodates) doesn't apply on EKS; instead, EKS 1.25+ enforces Pod Security Admission. Verify the tools image runs unprivileged under `restricted` PSS.

### Sprint 4 close (actual)

What shipped (architect surface — confirmed at this commit):

- **PRD 04 wording fix** (Sprint 3 tech-writer Issue 1, HIGH carry-over). § "Where the AWS chain lives in the tree" now describes the as-shipped two-package split: `internal/aws/` houses the AWS standard chain (`NewClients` wraps `config.LoadDefaultConfig`; `CredentialsConfigured` returns the resolved provider Source string; `Clients.CallerIdentity` wraps `sts:GetCallerIdentity`); `internal/cred/` retains the IBM IBMCloud-shaped resolver as deprecated for back-compat-naming-only, no production caller materialises a non-empty value; in-cluster IRSA is auto-injected by the EKS-managed pod-identity webhook (not awsbnkctl code). Sprint 5 deletes the dormant `internal/cred/` package alongside the docker tmpfile-bind-mount path and the SSH wrapper-script env propagation, per Sprint 3 tech-writer Issue 2's per-file breakdown.
- **Chapter 20 (connectivity testing)** — `book/src/20-connectivity-testing.md`. The `awsbnkctl test connectivity` surface: HTTPS-only probe, 10-second timeout, 2xx/3xx pass criterion, no retries, no expected-body, no L4-only mode. Documents `extra_hosts` workspace-config schema, the `--insecure` flag for pre-production self-signed certs, the JSON output envelope (`awsbnkctl.v1`), the failure-mode reading guide (`dial tcp: i/o timeout`, `x509: certificate signed by unknown authority`, `no such host`, SERVFAIL, 502/503/504), the post-`up` NLB / ALB shape recognition, and the cross-references to chapters 21 / 22 / 26.
- **Chapter 21 (DNS testing for GSLB)** — `book/src/21-dns-testing-gslb.md`. The `awsbnkctl test dns` surface, with emphasis on the GSLB-aware multi-vantage `--gslb-compare` workflow: probe library (`miekg/dns`), server resolution (literal IP, `system`, `cluster`, named-from-config), AWS-specific resolver shapes (Route 53 Resolver `.2` address, public hosted zones, Route 53 weighted/latency/geo routing), JSON schemas (`awsbnkctl.dns.v1.vantage`, `awsbnkctl.dns.v1`), the divergence detector's fingerprint logic (sorted `{type, rdata}` tuples, TTL excluded), and a worked us-west-2/us-east-1/eu-west-1 example. The `--backend docker` rejection rationale is included.
- **Chapter 22 (throughput testing)** — `book/src/22-throughput-testing.md`. The `awsbnkctl test throughput` surface: TCP throughput between iperf3 endpoints (`end.sum_received.bits_per_second` is the headline), the two modes (`north-south` via NLB LoadBalancer Service, `east-west` via ClusterIP), the EKS 1.25+ Pod Security Admission compliance contract (the bundled image's `USER 1000`, the `awsbnkctl-test` namespace's `pod-security.kubernetes.io/enforce: restricted` label, the failure shapes for PSA admission rejections), what "normal" looks like on `c5n.4xlarge` (9-15 Gbps north-south, 12-18 Gbps east-west same-node, 20-24 Gbps with SR-IOV VF), the workspace tuning knobs, and the `--keep` debug path.
- **Chapter 23 (E2E test plan)** — `book/src/23-e2e-test-plan.md`. The user-facing guide to the layered E2E suite: phase-letter system (baseline A-H, backends I-N + L-DNS, manual J), the SPIKE DEFERRAL gate, dry-run vs. live-tier semantics, per-phase coverage, cost and time budgeting ($5-8 per live run), resuming via `PHASE_FROM=`, and how CI (`.github/workflows/ci.yml`) runs the dry-run tier on every PR while the live tier gates on the operator-run PRD 07 spike.
- **PLAN.md Sprint 4 close** — this section.

What was deferred (carries into Sprint 5 or later):

- **Operator-run spike** (PRD 07 § Spike status) still gates `v0.2`; Sprint 4 closes the offline test surface, but the live tier of the E2E plan documented in chapter 23 will not exercise against real AWS until the spike clears.
- **`internal/cred/` package deletion** + cred-shim retirement across docker.go, ssh.go, k8s.go (Sprint 3 staff Issue 1, Sprint 3 tech-writer Issue 2). Sprint 5 work; the surface area is mechanically deletable but pulls in test-fixture retargeting on the way out.
- **Chapter 14 deep rewrite** (current chapter still targets `IBMCLOUD_API_KEY`; the AWS-shaped chain section in PRD 04 is now the design surface that rewrite draws from).
- **Chapter 25 filename rename** (`25-cos-supply-chain.md` → `25-s3-supply-chain.md`) — Sprint 5 architect, per Sprint 2 architect Issue 2 cascade plan.
- **Chapter 18 §"Per-tool default backends"** consistency check vs. iperf3-default-backend wiring — Sprint 5 architect or earlier (Sprint 4 staff is wiring iperf3 default = k8s into `internal/cli/test.go::resolveBackendSpecWith`'s `perToolDefaultBackend` map; once that lands, chapter 18 is accurate).
- **Service Quotas check in doctor** (Sprint 4 staff carry; feature-flagged off by default). Documented in PRD 04's doctor surface as the v1.x quota row.

Sibling agent surfaces (staff test-surface refresh + PSA verification + Service-Quotas wiring + up --dry-run first-run UX, validator CI extension + e2e marker refresh, tech-writer read-only pass) report independently; see the per-agent issue files at `issues/issue_sprint4_{staff,validator,tech-writer}.md` for what landed on those surfaces. The integrator reconciles at sprint close.

---

## Sprint 5 — book retarget (weeks 9-10)

### Goal

The book at `book/src/` reads as awsbnkctl's documentation, not roksbnkctl's. Web book publishes at `https://JLCode-tech.github.io/awsbnkctl/book/`.

### Documentation deliverables

| Chapter | Action |
|---|---|
| 1. What is BNK | Inherited; minor edits for AWS context |
| 2. Why ROKS | **Rewrite** as "Why EKS + self-managed SR-IOV node groups"; cite PRD 07 |
| 3. What awsbnkctl does | Rewrite |
| 4. Installation | Edit: drop `ibmcloud` mentions; update download URL |
| 5. Doctor | Edit: replace IBM-flavoured checks with AWS checks |
| 6. Workspaces | Inherited |
| 7. Quick start | Rewrite around `awsbnkctl up` AWS path |
| 8-11. Cluster lifecycle | Edit per AWS specifics |
| 12. Workspace config | Edit: new fields (`aws_region`, `node_instance_types`) |
| 13. Terraform variables | Auto-regenerate via `tools/tfvars-md` |
| 14. Credentials and the resolver chain | Edit: AWS credential chain replaces IBM Cloud chain |
| 15. SSH targets | Inherited |
| 16-19. Remote execution | Inherited |
| 20-23. Testing | Inherited mostly; edit DNS chapter for AWS GSLB nuances |
| 24. Day-2 ops | Edit per AWS specifics |
| 25. COS supply chain | **Rewrite** as "S3 (and optional ECR) supply chain"; cite PRD 08 |
| 26. Troubleshooting | Rewrite top-N issues for AWS |
| 27-30. Reference | Auto-regenerate via `tools/cobra-md` + `tools/tfvars-md` |
| 31-32. Contributing | Edit: update build-from-source instructions |
| _new_ 33. The data-plane decision | **New chapter**: walk through SR-IOV options on EKS, why self-managed node groups won, AMI lifecycle implications |

### Code deliverables

- GitHub Actions workflow `.github/workflows/book.yml` — already inherited; update the `gh-pages` deploy target so it lands at JLCode-tech.github.io.
- Update `tools/cobra-md` and `tools/tfvars-md` reference generators to emit awsbnkctl-shaped output.

### Test deliverables

- `mdbook test book/` passes (link integrity).
- `cspell` clean on `book/src/**/*.md`.
- Visual spot-check: walk Chapters 1-7 as a first-time reader; record issues in `issues/issue_sprint5_tech-writer.md`.

### Gate to Sprint 6

- Book builds clean, deploys to GitHub Pages, no broken cross-links.
- A first-time reader can follow the quick start without referring back to roksbnkctl's docs.

### Risks

- Book rewrites are scope-creep magnets. Stick to the chapter-by-chapter triage above; defer rewording to v1.x polish.

### Sprint 5 close (actual)

What shipped (architect surface — confirmed at this commit):

- **Preface + chapter 1** — preface status banner updated to "v0.9 Sprint 5 complete"; chapter 1's F5 support-matrix bullet preserves multi-cloud reality (EKS, AKS, GKE, ROKS, OpenShift Dedicated) but targets the book at EKS; the cross-link to chapter 17 references the EC2 bastion (not the upstream TGW jumphost).
- **Chapter 4 (Installation)** — full rewrite. IBM Cloud CLI / `oc` install steps removed; `aws` CLI install added as optional (production paths use the embedded aws-sdk-go-v2, not the CLI). `helm` still required (inherited modules' local-exec provisioners). Repo URL points at `JLCode-tech/awsbnkctl`. New `## Migrating from roksbnkctl (upstream fork)` section explains the coexistence story.
- **Chapter 5 (Doctor)** — full rewrite. Six AWS rows documented (`aws creds`, `aws sts`, `aws region`, `aws eks perms`, `aws s3 perms`) + optional feature-flagged `aws quotas`. IBM IAM verify replaced by `sts:GetCallerIdentity`. The `--target` SSH probe carries.
- **Chapter 6 (Workspaces)** — `~/.roksbnkctl/` → `~/.awsbnkctl/`; `ROKSBNKCTL_HOME` → `AWSBNKCTL_HOME`; cluster-outputs.json schema updated for EKS (cluster_arn, region, account_id, vpc_id, subnet_ids, oidc_provider_arn, supply_chain_bucket); workspace patterns table updated.
- **Chapter 7 (Quick start)** — full rewrite. Mermaid diagram redrawn (IBM → AWS, ROKS → EKS, COS → S3, TGW jumphost → bastion). Worked example uses AWS-shaped outputs (~82 resources, ~30 min elapsed). The 4-command lifecycle is intact.
- **Chapter 8 (Cluster phase)** — cluster-phase resources updated (EKS + VPC + SR-IOV node group + S3 + IRSA + bastion); deploy_bnk=false override carries; cluster-outputs.json schema matches chapter 6.
- **Chapter 9 (Registering an existing cluster)** — full rewrite around `awsbnkctl cluster register <eks-cluster-name>`; required-input vs auto-discovery table updated for EKS (cluster_arn, OIDC provider, subnet AZs); supply-chain bucket naming convention. Carries forward Issue 5 (verb may not yet be implemented).
- **Chapter 10 (Deploying BNK trials)** — trial-phase modules retargeted at AWS (S3, IRSA); `~80`-resource shape updated; AWS-shaped Terraform output names.
- **Chapter 11 (Tearing down)** — destroy ordering, AWS-shaped destroy noise (ENIs, NLBs), refusal catalogue carries.
- **Chapter 12 (Workspace config)** — full rewrite. New `aws:`, `cluster:`, `s3:` block schemas; `cos:` block removed; `exec:` defaults updated (terraform→local, iperf3→k8s).
- **Chapter 13 (Terraform variables)** — IBM tfvars references replaced with AWS shapes; the `AWS_ACCESS_KEY_ID never on disk` exception preserved as the AWS-equivalent of the `ibmcloud_api_key` exception. Issue 1 caveat: refgen output pending.
- **Chapter 14 (Credentials)** — full rewrite. AWS standard chain (env → shared-config → SSO → IMDS → container task role → web-identity) documented in detail; IRSA in-cluster path documented; redactor + per-backend cred propagation table updated.
- **Chapter 15 (SSH targets)** — `jumphost_shared_key` → `bastion_shared_key`; ubuntu user → ec2-user; auto-discovery from `testing_bastion_public_ip`; yum/apt bootstrap.
- **Chapter 16 (--on flag)** — AWS-shaped scenarios (private EKS endpoint, customer firewall to `*.amazonaws.com`); env passthrough updated (AWS_PROFILE, AWS_REGION); the bastion is an EC2 instance.
- **Chapters 17 / 18 / 19 (Execution backends, choosing, in-cluster ops pod)** — `ibmcloud` exec adapter replaced by direct AWS SDK; IRSA-based ops pod auth replaces trusted-profile flow; per-tool defaults table updated.
- **Chapter 24 (Day-2 ops)** — `oc` references removed (not OpenShift); EKS-shaped status output; the internalised `awsbnkctl k get/apply/...` verbs carry.
- **Chapter 25 (S3 supply chain)** — already AWS-shaped from Sprint 2/3; this sprint adds the IRSA trust-chain anchor that chapters 20-22 cross-link to. Filename rename (`25-cos-supply-chain.md` → `25-s3-supply-chain.md`) is Issue 3, deferred to Sprint 6 integrator.
- **Chapter 26 (Troubleshooting)** — added `### AWS LoadBalancer` (three failure shapes: timeout, target-group RED, 502/503) and `### DNS` (three failure shapes: no-such-host, no-divergence, SERVFAIL) sub-sections per Sprint 4 tech-writer Issue 4. Anchors `#aws-loadbalancer` and `#dns` resolve.
- **Chapter 30 (Glossary)** — IBM-flavoured entries replaced with AWS (VPC Endpoint, EKS, S3, IRSA added).
- **Chapter 31 (Building from source)** — build instructions retargeted at `cmd/awsbnkctl/`; the `internal/aws/` directory called out.
- **Chapter 32 (Extending awsbnkctl)** — chapter content reads as "Extending awsbnkctl". The fork-relationship paragraph (roksbnkctl upstream) is the documented exception per the architect brief.
- **PLAN.md Sprint 5 close** — this section.

What was deferred (carries into Sprint 6 or later):

- **Operator-run spike** (PRD 07 § Spike status) still gates `v0.2`; the book describes the design as-shipped, not as-validated against live AWS.
- **`internal/cred/` package deletion** + cred-shim retirement (Sprint 4 architect Issue 4 carry-over). Sprint 5 staff fold; the architect surface assumes the deletion lands in the same Sprint 5 landing commit.
- **Reference chapter regeneration** (27 command-ref, 28 config-ref, 29 tfvars-ref) — Sprint 5 staff regenerates via `tools/refgen/`; integrator commits the diff in the same Sprint 5 landing commit (Issue 1).
- **Chapter 25 filename rename** (`25-cos-supply-chain.md` → `25-s3-supply-chain.md`) — Sprint 6 integrator (Issue 3).
- **`cluster register` EKS verb implementation** — Sprint 5/6 staff verifies presence; if absent, lift from roksbnkctl's `cluster_register.go` and retarget at `internal/aws/eks.go` (Issue 5).
- **Sprint 4 tech-writer Issue 1** (chapter 22 `Iperf3DefaultImage` flip after image publishes) — Sprint 6 hardening.

Sibling agent surfaces (staff: chapter 22 image fix + IBM-residue sweep + refgen + chapter 26 sub-anchors; validator: book CI workflow + cspell + CHANGELOG + GitHub Pages deployment; tech-writer: first-time-reader dogfood + ready-for-v0.9 verdict) report independently; see the per-agent issue files at `issues/issue_sprint5_{staff,validator,tech-writer}.md` for what landed on those surfaces. The integrator reconciles at sprint close.

---

## Sprint 6 — hardening + v1.0 cut (weeks 11-12)

### Goal

E2E phases pass on a stock dev host. Security review clean. v1.0 binary attached to a GitHub Release.

### Code deliverables

| Item | Detail |
|---|---|
| E2E test phases | Adapt roksbnkctl's E2E phases A-N + L-DNS to AWS shapes. Document in `docs/prd/05-E2E-TEST-PLAN.md`. |
| Security audit | `gosec ./...` clean; secrets scan clean (no AWS keys, no JWT bodies, no FAR tarballs accidentally committed). |
| Doctor refresh | Final pass: every check has a useful failure-mode message. |
| `awsbnkctl self update` | Validate the inherited self-update path against awsbnkctl's release tags + checksums. |
| Release prep | `make release` end-to-end; goreleaser builds Linux/macOS/Windows × amd64/arm64; book PDF attached. |

### Documentation deliverables

- Final book pass — diagrams, screenshots, cross-link integrity.
- `CHANGELOG.md` rolls up the seven-sprint history into a `v1.0.0` entry.
- `docs/PLAN.md` (this file) gets a "v1.x deferred work" appendix.

### Test deliverables

- All E2E phases pass on a stock dev host (no `kubectl`, no `aws` CLI, no `iperf3`, no `dig` installed).
- CI matrix green on Linux + macOS (Windows compile check is a stretch).

### Gate to v1.0

- E2E green.
- Security review green.
- Book published and dogfooded.
- A tagged `v1.0.0` Release with binaries + checksums + book PDF.

### Risks

- Late-discovered AWS quotas or instance-type availability issues in untested regions. Mitigate: declare a "tested regions" list (us-east-1, us-west-2, eu-west-1) in the docs; everything else is best-effort.

### Sprint 6 close (actual)

> OBSOLETE per D-001 (post-TF direction) — this section documents roksbnkctl Sprint 6 history and is retained as audit trail only.

What shipped (architect surface — confirmed at this commit):

- **Chapters 8 / 9 / 11 "Available in v1.x" annotations** — top-of-chapter banners on `book/src/08-cluster-phase.md`, `book/src/09-registering-existing-cluster.md`, and `book/src/11-tearing-down.md` flag the `awsbnkctl cluster up`/`cluster down`/`cluster show`/`cluster register`/`bnk up`/`bnk down` subverbs as v1.x roadmap surface. The v0.9 binary ships only the single-phase unscoped lifecycle (`up` / `down` / `apply` / `plan` / `init` / `status`); the chapter prose describes the v1.x two-phase design as the staff lift from `roksbnkctl/internal/cli/cluster.go` + `bnk.go` will land it. Closes Sprint 5 tech-writer Issue 2 BLOCKER (path b: explicit "Available in v1.x" notes on absent subverbs) — first-time readers no longer hit `unknown command "cluster"` and bounce. The v1.x retarget itself is folded into the "Subverb subtrees: `cluster up/down/show/register` + `bnk up/down`" entry of the deferred-work appendix below.
- **Chapter 19 "Available in v1.x" annotation** — top-of-chapter banner on `book/src/19-in-cluster-ops-pod.md` flags the as-shipped `internal/exec/k8s_install.yaml` as the **inherited `roksbnkctl` IBM-shape**: the v0.9 `awsbnkctl ops install` lands a pod named `roksbnkctl-ops` in a `roksbnkctl-ops` namespace with Secret `roksbnkctl-ibm-creds` populated from `IBMCLOUD_API_KEY` — working IBM mechanics, wrong cloud on EKS. The banner directs readers away from running `ops install` against real EKS clusters until the Sprint 6 staff retarget (ops-pod manifest IBM → IRSA-injected AWS-creds shape) lands, and points at alternative backends via [Chapter 18 §"Decision tree"](../book/src/18-choosing-backend.md#decision-tree). Closes Sprint 5 tech-writer Issue 1 BLOCKER on the prose side; the YAML retarget is the staff surface this sprint and is the gate for IRSA-by-default mention everywhere else in the book.
- **Chapter 30 glossary cleanup** — `book/src/30-glossary.md` rewritten against the as-shipped AWS shape. Entries deleted or retargeted: **CRN** deleted (IBM-only concept; `awsbnkctl cos` subtree doesn't exist); **S3** rewritten as Amazon Simple Storage Service (was framed as IBM Cloud Object Storage); **CIS** disambiguated to F5 Container Ingress Services (IBM Cloud Internet Services confusion removed); **EKS** rewritten as Amazon Elastic Kubernetes Service (was "Red Hat OpenShift on AWS — IBM's managed OpenShift offering", a triple factual error); **OpenShift** rewritten to clarify EKS ≠ OpenShift and ROSA is the AWS-managed-OpenShift product, framed as fork-relationship context; **Trusted Profile** rewritten as the IBM upstream concept with IRSA as the AWS equivalent + see-IRSA cross-link; **VPE** rewritten as **VPC endpoint** (the AWS primitive name); **TGW** rewritten to reflect that the bundled HCL doesn't provision a TGW by default; **VSI** rewritten as **EC2 instance**; **SCC** kept as legacy-context entry with PSA called out as the EKS-equivalent admission shape; **PSA** added as a new top-level entry; **IRSA** added as a new top-level entry; **IMDS** added; **redactor** rewritten against AWS-cred values (was "the IBM API key value"); **envFrom** / **Secret** entries rewritten to reference `awsbnkctl-aws-creds` and the IRSA-default v1.x retarget; **cred resolver chain** entry collapsed to a see-AWS-standard-credential-chain cross-link; stale anchor cross-references (chapter 14 `#source-3--workspace-api_key_b64`, chapter 25 `#licence-rotation`, chapter 26 `#orphan-ibm-cloud-resources`) replaced with current heading slugs. Closes Sprint 5 tech-writer Issue 3 HIGH end-to-end.
- **Chapters 17 / 18 / 32 IBM-residue sweep** — `book/src/17-execution-backends.md` L442 (`Credentials.IBMCloudAPIKey`) rewritten to describe the AWS-shaped resolved-creds Secret-projection path with IRSA-default v1.x note; the `Credentials` struct example at L627 rewritten to surface `AWSCredentials` (`AccessKeyID` / `SecretAccessKey` / `SessionToken` / `Region` / `Source`) replacing the deleted `IBMCloudAPIKey` field; the SSH bootstrap recipe at L508 retargeted from "IBM's apt repo + GPG key" to the AWS CLI v2 zip-distribution install. `book/src/18-choosing-backend.md` worked-example terminal output L309 updated from `Secret awsbnkctl-ibm-creds applied` to `Secret awsbnkctl-aws-creds applied`; L159 "no IBM repo + GPG key dance" rewritten as "no zip-and-unzip dance". `book/src/32-extending-roksbnkctl.md` L59 docker tool-images map worked example rewritten from `"ibmcloud": …awsbnkctl-tools-ibmcloud` to `"aws": …awsbnkctl-tools-aws`; L70 ops-pod long-lived pattern example rewritten from `ibmcloud` (state-shared) to `aws` (interactive-debug-shared); L82-87 ssh `toolPackages` map rewritten from `ibmcloud` (IBM apt repo) to `aws` (AWS CLI v2 zip dist). Sweep verification: `grep -rn 'roksbnkctl\|ibmcloud\|ROKS\|COS' book/src/{17,18,32}-*.md` returns 0 hits; chapter 19's hits are contained in the explicit "Available in v1.x" banner that documents the inherited shape. Closes Sprint 5 tech-writer Issue 4 / Sprint 5 tech-writer Issue 5 (renumbered) HIGH.
- **README sprint-count refresh** — `README.md` status banner rewritten from "pre-release (M0 — Sprint 0 just landed; first tagged release `v0.2` gated on Sprint 1)" to "**Sprint 6 complete; v0.9-rc ready; v1.0 awaits spike**" with the operator-run PRD 07 spike framing surfaced inline. The "Planned quick start (post-Sprint 1)" heading is now "Quick start" against the as-shipped binary; "**Nothing in this README works yet until Sprint 1 closes**" pulled. The `book/` row in the repo layout now reads "AWS-retargeted in Sprint 5; published at GitHub Pages" rather than "to be retargeted in later sprints". The fork-relationship paragraph now points readers at the awsbnkctl book first; the roksbnkctl book is framed as the ROKS-shaped counterpart for shared-concept reference. Closes Sprint 5 tech-writer Issue 6 MEDIUM.
- **PLAN.md "What's deferred to post-v1.0" appendix** — new section at the end of the document (below) consolidating every Sprint 1+ "v1.x revisit" note from PRDs 07 + 08 and every "Sprint N+1" issue still open from Sprints 0-5. The five-line v1.x section that was here pre-Sprint-6 has been folded into the richer appendix.
- **PLAN.md Sprint 6 close** — this section.

What was deferred (carries into post-v1.0):

- **Operator-run spike** (PRD 07 § Spike status) still gates `v1.0`. The v0.9-rc1 release artefacts ship without the "officially supported on this account" tag; anyone with operator-run spike validation can cut v1.0 immediately.
- **`internal/exec/k8s_install.yaml` IRSA retarget** (Sprint 5 tech-writer Issue 1 BLOCKER staff side, Sprint 6 staff carry per the brief). The architect chapter 19 annotation closes the prose-side correctness gap; the YAML + `internal/cli/ops.go` retarget is staff scope this sprint. If it doesn't land by Sprint 6 close, the "ops-pod IRSA retarget" entry in the deferred-work appendix carries it.
- **`cluster register` EKS verb implementation** (lift from `roksbnkctl/internal/cli/cluster_register.go`, retarget at `internal/aws/eks.go`). Sprint 5 staff Issue 5 → architect chapter 9 annotation closes the prose side; the implementation lift is the deferred-work appendix entry under "Subverb subtrees".
- **`cluster up/down/show` + `bnk up/down` EKS verbs implementation** (lift from `roksbnkctl/internal/cli/cluster.go` + `bnk.go`). Same shape as `cluster register`.
- **Chapter 25 filename rename** (`25-cos-supply-chain.md` → `25-s3-supply-chain.md`). Sprint 5 architect Issue 3 → Sprint 5 tech-writer Issue 8 → Sprint 6 integrator at sprint close.

Sibling agent surfaces (staff: `internal/exec/k8s_install.yaml` IRSA retarget + `gosec` + secrets-scan + goreleaser six-archive build + ops.go template substitution + `K8sOpsSecretName` constant; validator: full security audit workflow + release CI workflow validation + book PDF build + final cspell + CI matrix end-state documentation; tech-writer: v1.0-readiness preview + full book read-through + release-artefact spec-check + MIGRATING.md sanity check) report independently; see the per-agent issue files at `issues/issue_sprint6_{staff,validator,tech-writer}.md` for what landed on those surfaces. The integrator reconciles at sprint close and cuts the `v0.9-rc1` candidate tag (v1.0 candidate follows once the operator-run spike validates).

---

## What's deferred to post-v1.0

This appendix catalogues every deliberate scope-cut and Sprint-N+1 carry-over that lands past the v1.0 tag. The list draws from PRDs 07 + 08 "v1.x revisit" notes, every "Sprint N+1" / "v1.x roadmap" issue still open at Sprint 6 close, and the architect-surface decisions to ship-as-roadmap rather than ship-as-implemented for v1.0.

The shape is deliberate: v1.0 is the AWS-retarget structural-completeness gate (binaries, security review, book, E2E phases), not the feature-completeness gate. The items below are real work to do, not scope creep — they were sequenced past v1.0 to keep the M0-M6 scope finite and the v1.0 cut achievable on the 13-week plan.

### Gating v1.0 itself

- **Operator-run PRD 07 spike validation** (`docs/prd/07-EKS-CLUSTER-SRIOV.md` § Spike status). The v0.9-rc1 release artefacts ship structurally complete but without the "officially supported on this account" tag. v1.0 is cut once the spike validates SR-IOV VFs on a real `c5n.4xlarge` EKS node and the device-plugin advertisement (`intel.com/sriov` resources schedulable) is confirmed in the operator's account. Until then the binary is shipped, the docs are shipped, the CI is green — but the data-plane decision in PRD 07 is "validated offline / design-complete" rather than "validated against live AWS".

### Subverb subtrees (currently annotated as v1.x in the book)

- **`awsbnkctl cluster up` / `cluster down` / `cluster show`** (chapter 8). The two-phase split — durable cluster phase vs. iterative trial phase, separate state directories, `deploy_bnk=false` override, `cluster-outputs.json` artefact — is documented in chapter 8 as v1.x roadmap surface. Implementation lift from `roksbnkctl/internal/cli/cluster.go` retargeted at the EKS shape; the upstream tree's pattern carries across cleanly, so the v1.x landing is mostly mechanical retarget + AWS-SDK calls in place of IBM Cloud SDK calls. Cross-link: chapter 8's "Available in v1.x" banner.
- **`awsbnkctl cluster register <eks-name>`** (chapter 9). The metadata-only attach-to-existing-cluster path. Implementation lift from `roksbnkctl/internal/cli/cluster_register.go` retargeted at `internal/aws/eks.go` (`eks:DescribeCluster` lookup, OIDC-provider matching, supply-chain-bucket discovery). Cross-link: chapter 9's "Available in v1.x" banner; Sprint 5 architect Issue 5 + Sprint 5 tech-writer Issue 3.
- **`awsbnkctl bnk up` / `bnk down`** (chapter 10 / chapter 11). The trial-phase-scoped commands plus the legacy-single-state-workspace refusal catalogue. Implementation lift from `roksbnkctl/internal/cli/bnk.go`. Cross-link: chapter 11's "Available in v1.x" banner.
- **`awsbnkctl migrate`** — the state-migration command referenced in the chapter 11 refusal catalogue ("…or migrate the state first"). v0.9 / v1.0 ship without the verb; the refusal text references it so the wording stays valid once it lands.

### Ops-pod surface (currently annotated as v1.x in chapter 19)

- **`internal/exec/k8s_install.yaml` IRSA retarget** — full sweep of the embedded ops-pod manifest from `roksbnkctl-ops` / `roksbnkctl-ibm-creds` / `IBMCLOUD_API_KEY` / Trusted-Profile shape to `awsbnkctl-ops` / `awsbnkctl-aws-creds` / standard AWS env-var names / IRSA-via-OIDC-provider shape. The `--trusted-profile=auto` flow (chapter 19 §"Trusted-profile flow") becomes the IRSA-auto-provisioning flow with the AWS IAM role replacing the IBM Cloud Trusted Profile.
- **`internal/cli/ops.go` retarget** — template substitution + `K8sOpsSecretName` constant + `probeOpsPodIRSA` doctor probe rewritten against the AWS-shaped manifest. The `resolveTrustedProfileForInstall` function retargets at AWS IAM Identity perm probing instead of IBM IAM Identity.
- **In-pod `aws sso login` wrap retarget** (paralleling the Sprint 10 upstream "Sprint 10 carry-over" from chapter 19's partial-closure admonition). The pod's startup script needs to use the IRSA-projected web-identity token directly rather than `--apikey "$IBMCLOUD_API_KEY_B64"`.
- Closes Sprint 5 tech-writer Issue 1 BLOCKER staff side (the architect-side prose annotation is the chapter 19 banner landing this sprint).

### S3 supply chain (PRD 08 v1.x revisit notes)

- **ECR mirror story first-class** (PRD 08 § Trade-offs). Sprint 2 shipped the optional `ecr_mirror` module; v1.x makes it the default for air-gapped customers and adds an automated FAR-image sync workflow (`skopeo copy` on a cron'd CodeBuild project, or a `null_resource` that runs at apply time with explicit user opt-in). Until then, air-gapped customers run the v1.0 stretch module manually.
- **AWS Secrets Manager for the JWT** (PRD 08 § Decisions). Considered for v1.0, deferred. The JWT lives in S3 server-side encrypted via the supply-chain bucket's KMS key; Secrets Manager would add per-secret cost without rotation-semantics gain for an artefact whose threat model is read-only at apply time. v1.x revisit if the threat model evolves.
- **SSE-S3 as a documented alternative to customer-managed KMS** (PRD 08 § Trade-offs). v1.0 ships customer-managed KMS (CMK) by default because the supply-chain bucket holds the JWT (sensitive). v1.x evaluates SSE-S3 (free, AWS-managed) for cost-sensitive customers.
- **Per-component FLO IRSA roles** (PRD 08 § Trade-offs). v1.0 ships a single FLO IRSA role that has permission for both FAR-image pulls and FLO controller actions. v1.x revisits if F5 surfaces per-component permission needs.
- **`internal/aws/s3.go` live-rotation path** — the SDK helpers (`PutObject` / `HeadObject` / `GetObject`) exist for doctor probes today; the "rotate a single object without re-running `terraform apply`" path is documented in PRD 08 as a v1.x first-class workflow. Until then, FAR-archive and JWT rotation is `terraform apply` after editing the local source paths in tfvars.

### EKS cluster + SR-IOV node group (PRD 07 v1.x revisit notes)

- **EKS Auto Mode + Karpenter integration** (PRD 07 § Alternatives considered). Rejected for v1.0 because Karpenter has no clean integration with SR-IOV device plugins as of the spike. v1.x adds support for elastic / Karpenter-managed node groups if AWS ENA-SR-IOV semantics permit.
- **EFA (Elastic Fabric Adapter) for the high-throughput tier** (PRD 07 § Decision). v1.0 ships with ENA-SR-IOV enabled and EFA off (not needed for BNK's control + data plane; EFA narrows the instance family). v1.x evaluates EFA for the high-throughput tier.
- **Mixed instance families per cluster** (PRD 07 § Trade-offs). v1.0 recommends single-family clusters because the SR-IOV device-plugin config is per-vendor/device-ID and mixing `c5n` + `m5n` doubles the config matrix. v1.x revisits the multi-family story.

### Doctor (Sprint 4 architect Issue 4 carry-over)

- **Service Quotas check in doctor** (`internal/doctor/aws.go`). Sprint 4 carry-over; PRD 04 § doctor surface documents the v1.x quota row. Feature-flagged off by default in v1.0 because the AWS Service Quotas API is region-scoped and the lookups add cred-roundtrip latency to every doctor invocation. v1.x makes it on-by-default with cached lookups.

### Throughput testing (Sprint 5 tech-writer + Sprint 4 tech-writer carry-over)

- **`Iperf3DefaultImage` flip post-publish** (Sprint 4 tech-writer Issue 1 → Sprint 6 hardening). The bundled `awsbnkctl-tools-iperf3` image-tag default needs to flip from `:dev` to `:v0.9.0` once the GHCR release publishes the matching tag.

### Cross-region GSLB testing (Sprint 4 tech-writer note)

- **Multi-region `--gslb-compare` vantages** (chapter 21). v1.0 handles single-region GSLB via the multi-backend `--gslb-compare` workflow against local / k8s / ssh:<bastion> vantages within one region. Multi-region (an ssh:<bastion> in us-west-2 and another in eu-west-1) is the v1.x stretch.

### Cred resolver chain (PRD 04 v1.x revisit notes)

- **AWS-native SSO + IAM Identity Center first-class** (PRD 04 § Where the AWS chain lives). v1.0 reads SSO tokens via the aws-sdk-go-v2 default chain (the SSO source produces an `aws.Credentials` like any other). v1.x evaluates surfacing IAM Identity Center as a first-class workspace-config block with profile-discovery from the SSO directory.
- **`internal/cred/` package deletion** (Sprint 4 architect Issue 4). The dormant IBMCloud-shaped resolver in `internal/cred/` is kept for back-compat-naming-only at v0.9; no production caller materialises a non-empty value. Sprint 5 staff scoped the deletion alongside the docker tmpfile-bind-mount path and the SSH wrapper-script env-propagation retirement; if any residual surface survives Sprint 6, the deletion is the post-v1.0 cleanup pass.

### Distribution + packaging

- **Homebrew tap** (Sprint 0 carry-over). Same as roksbnkctl — on the v1.x roadmap. v1.0 ships goreleaser-built binaries via GitHub Releases only.
- **Windows Docker Desktop backend coverage** (chapter 17 § Docker backend). Linux/macOS docker daemons are in scope for v1.0; Windows is deferred.
- **`awsbnkctl ops show --output json`** (chapter 19 § ops show). v1.0 ships a fixed six-line key/value block; structured JSON output is on the v1.x roadmap once `ops show` grows additional fields (image-id hash, env-hash reconciliation against the live pod).

### Documentation polish (low-priority carry-overs)

- **Chapter 25 filename rename** (`25-cos-supply-chain.md` → `25-s3-supply-chain.md`). Sprint 5 architect Issue 3 → Sprint 5 tech-writer Issue 8 → Sprint 6 integrator at sprint close.
- **Chapter 02 / 03 / 32 filename renames** (`02-why-roks.md`, `03-what-roksbnkctl-does.md`, `32-extending-roksbnkctl.md` → AWS-shaped slugs). Sprint 5 tech-writer Per-prose-surface verdict notes the title vs. filename mismatch is fine for mdbook but the slug should eventually catch up. Same atomic-rename window as chapter 25.

The above is the canonical list — anything not on it that gets discovered as a deferred item should be folded back in here as part of the post-v1.0 backlog grooming.

---

## Book outline (the full SUMMARY.md target)

Sprint 5 lands the rewrites. The chapter map below is **the SUMMARY.md target** — chapters carry through from roksbnkctl unless noted, with the AWS-specific additions called out.

```
PART I — CONCEPTS
  1. What is BIG-IP Next for Kubernetes (BNK)
  2. Why EKS + self-managed SR-IOV node groups            [rewritten]
  3. What awsbnkctl does (and doesn't do)                 [rewritten]

PART II — GETTING STARTED
  4. Installation
  5. Doctor: checking your environment                    [AWS-flavoured]
  6. Workspaces
  7. Quick start: from AWS credentials to deployed BNK    [rewritten]

PART III — CLUSTER LIFECYCLE
  8. The cluster phase (cluster up/down)
  9. Registering an existing cluster
  10. Deploying BNK trials on top
  11. Tearing down

PART IV — CONFIGURATION
  12. Workspace config (config.yaml)                      [new AWS fields]
  13. Terraform variables (terraform.tfvars)              [auto-regen]
  14. Credentials and the AWS resolver chain              [rewritten]
  15. SSH targets

PART V — REMOTE EXECUTION
  16. The --on flag and SSH jumphosts
  17. Execution backends: local, docker, k8s, ssh
  18. Choosing a backend per tool
  19. The in-cluster ops pod

PART VI — TESTING
  20. Connectivity testing
  21. DNS testing for GSLB
  22. Throughput testing
  23. The E2E test plan

PART VII — OPERATIONS
  24. Day-2 ops: status, logs, k get/apply/exec
  25. S3 (and optional ECR) supply chain                  [rewritten]
  26. Troubleshooting                                     [AWS-flavoured]

PART VIII — REFERENCE
  27. Command reference                                   [auto-regen]
  28. Configuration reference                             [auto-regen]
  29. Terraform variable reference                        [auto-regen]
  30. Glossary

PART IX — CONTRIBUTING
  31. Building from source
  32. Extending awsbnkctl

PART X — DESIGN DECISIONS
  33. The data-plane decision: SR-IOV on EKS              [new]
```

---

## How this plan is maintained

- **Sprint kickoff:** the integrator drafts four `prompts/sprint<N>/{architect,staff,validator,tech-writer}.md` briefs referencing the relevant sections above, then dispatches the four-role parallel-agent pattern per [`prompts/README.md`](../prompts/README.md).
- **Sprint close:** the architect agent updates this file's per-sprint section with what actually shipped vs. planned, and folds any deferred work into the v1.x section.
- **PRD drift:** when a sprint surfaces a design decision that conflicts with a PRD, the PRD is updated in the same commit as the implementation. The integrator catches PRD ↔ PLAN drift at tag-cut time.
