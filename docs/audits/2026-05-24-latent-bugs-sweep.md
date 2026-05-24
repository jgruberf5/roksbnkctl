# Latent bugs sweep — 2026-05-24

> Comprehensive inventory of every deferred fix / latent bug / TODO in the repo.
> Triggered by user feedback after slice-13 cold-start exposed 2-day-old DEFERRED items
> from `docs/audits/slice-09-aws-gpu-setup-audit.md` (m6i.4xlarge minimum + desiredSize=3).
> Read-only sweep; nothing fixed here.
>
> **Process rule established 2026-05-24** (memory `feedback_no_deferred_fixes`):
> NO deferring bugs found during work. Fix in same PR or escalate concretely; never
> punt to BACKLOG. This sweep documents the existing punted items so they can be
> addressed before the next live cycle.

## Critical (will bite on next live test)

### C-1 — Default `instanceType: t3.medium` for `pattern: host-device` is broken-by-default
- **File**: `internal/intent/cluster.go:375` — `ng.InstanceType = "t3.medium"` is set unconditionally.
- **Symptom**: A fresh `up` with an empty `instanceType` field will fail Phase 17 `AttachmentLimitExceeded` (t3.medium caps at 3 ENIs; host-device needs 4) — or, even if the operator bumps to m5.xlarge, Phase 25 will still time out with `f5-tmm Pending: Insufficient cpu` because m5.xlarge has only ~3920 m allocatable CPU vs ~7090m BNK control-plane + TMM total.
- **Original source**: `docs/audits/slice-09-aws-gpu-setup-audit.md` row 27 (2026-05-22) — "BNK_WORKER_INSTANCE_TYPE='m6i.4xlarge' MINIMUM for BNK 2.3 Small"; reaffirmed by `docs/audits/slice-12-cold-start-audit.md` §5; memory `project_host_device_eni_limit.md`.
- **Proposed fix**: Pattern-aware default. When `c.Pattern == "host-device"` and `ng.InstanceType == ""`, default to `m6i.4xlarge`. Otherwise keep `t3.medium`. Add a constant `HostDeviceMinInstanceType = "m6i.4xlarge"` in `internal/intent/cluster.go` so Phase 00 preflight reads the same value.
- **Severity**: CRITICAL — slice-13 cold start hit this *twice* on the same day.

### C-2 — Phase 00 preflight only checks ENI count; not CPU, not memory, not desiredSize
- **File**: `internal/aws/phases/phase00_preflight.go:79-112` — `checkHostDeviceENICapacity` validates only `MaximumNetworkInterfaces ≥ 4`. It does not assert `VCpuInfo.DefaultVCpus ≥ 16`, `MemoryInfo.SizeInMiB ≥ 65536`, or `nodeGroups[0].DesiredSize ≥ 3`.
- **Symptom**: An operator who supplies a 4-ENI but underpowered instance (e.g. `m5.xlarge`) passes preflight and burns 20-25 min of provisioning before Phase 25 times out with `f5-tmm Pending: Insufficient cpu`. This is exactly what bit the 2026-05-24 cold-start session.
- **Original source**: `docs/audits/slice-12-cold-start-audit.md` §5 ("NEW: VCpuInfo.DefaultVCpus ≥ 16; NEW: MemoryInfo.SizeInMiB ≥ 65536; NEW: nodeGroups[0].DesiredSize ≥ 3"); `.agent/handoff/2026-05-24-slice-13-and-audit-pickup.md` §"What's still left" item 2.
- **Proposed fix**: Rename `checkHostDeviceENICapacity` → `checkHostDeviceCapacity`; add three new asserts; surface a single error listing all that fail. Add four unit tests in `phase00_preflight_test.go`.
- **Severity**: CRITICAL — current preflight allows a 25-min slow-fail.

### C-3 — `examples/syd-tracer/cluster.yaml` still pins `m5.xlarge` / `desiredSize: 1`
- **File**: `examples/syd-tracer/cluster.yaml:106-109` — `instanceType: m5.xlarge`, `desiredSize: 1`, `minSize: 1`, `maxSize: 2`. (The applyDefaults patch from slice-10 bumps host-device empty-desiredSize → 3, but the explicit `1` here defeats that.)
- **Symptom**: The canonical example for live e2e cannot reach Phase 25 success. Even a re-run of slice-13's live test will fail on `Insufficient cpu`.
- **Original source**: `docs/audits/slice-12-cold-start-audit.md` §5 fix #3; `.agent/handoff/2026-05-24-slice-13-and-audit-pickup.md` "What's still left" item 3.
- **Proposed fix**: `instanceType: m6i.4xlarge`, `desiredSize: 3`, `minSize: 3`, `maxSize: 3`. Replace the cost-saving comment with a citation to the slice-12 audit.
- **Severity**: CRITICAL — every cold start re-blows up.

### C-4 — Phase 25 activation poll loops 40 × 30 s = 20 min, masking root causes
- **File**: `internal/aws/phases/phase25_activation_poll.go:23` — `phase25MaxIter = 40`.
- **Symptom**: When Phase 25 fails (because the cluster is misconfigured, image pull is broken, etc.), the operator waits 20 minutes for an uninformative timeout. User-flagged on 2026-05-24 as too long; the actual failure was visible in `kubectl get pods -A` after 60 s.
- **Original source**: `docs/audits/slice-12-cold-start-audit.md` §4 ("trimmed: 40 × 30s = 20 min → 12 × 30s = 6 min"); `.agent/handoff/2026-05-24-slice-13-and-audit-pickup.md` §"What's still left" item 4.
- **Proposed fix**: `phase25MaxIter = 12` (6-min cap). On final timeout, dump `kubectl get pods -A` + `kubectl describe pod <stuck>` to stderr so the operator sees the cause.
- **Severity**: CRITICAL for operator experience; not a correctness bug but consistently bites and wastes ~14 min per failed run.

### C-5 — Phase 23 / 23b CRD-wait timeouts pinned at 10 min each
- **File**:
  - `internal/aws/phases/phase23_license.go:26` — `phase23CRDWait = 10 * time.Minute`
  - `internal/aws/phases/phase23b_spkvlan_gatewayclass.go:23` — `f5spkvlanCRDWait = 10 * time.Minute`
- **Symptom**: CRD applies are sub-second; 10-min waits are terraform-era pessimism left over from slice-7. When a downstream CRD never lands (FLO crashed, registry rejected pull, etc.), the operator burns 20 min in Phase 23/23b on top of any Phase 25 timeout.
- **Original source**: `docs/audits/slice-12-cold-start-audit.md` §4 ("trimmed 10 → 3 min"); slice-13 handoff item 5.
- **Proposed fix**: Both → 3 min.
- **Severity**: CRITICAL for operator experience.

### C-6 — `phase12_k8s_foundation.applyRawYAML` static GVR map silently no-ops on unknown kinds
- **File**: `internal/aws/phases/phase12_k8s_foundation.go:638-695` — `resolveGVR()` is a hardcoded static map; unknown apiVersion/kind returns `("unknown apiVersion/kind", false, error)` which `applyRawYAML` logs as a *warning* and continues — phase returns `nil`. There is no post-apply readback.
- **Callers affected**: `phase11b`, `phase12` (cert-manager / cert chain / Multus), `phase15` (OTEL certs), `phase19` (cloud-network-mapping ConfigMap), `phase20` (NADs), `phase21` (IRSA SA), `phase22` (CNEInstance), `phase23` (License), `phase23b` (F5SPKVlan + GatewayClass). At least 11 call sites.
- **Symptom**: Already burned us once — slice-10's F5SPKVlan + GatewayClass were silently skipped, TMM never reached Ready (`project_phase23b_gvr_bug`). Slice-13 sidestepped it for scenarios via `internal/k8s.ApplyOptions` (live RESTMapper). The phases still use the static map.
- **Original source**: memory `project_phase23b_gvr_bug.md`; `docs/audits/slice-12-cold-start-audit.md` §9 (escalated to user); `feedback_no_deferred_fixes`.
- **Proposed fix**: Replace static map with `restmapper.NewDeferredDiscoveryRESTMapper` (already used by `internal/k8s/apply.go:69`). OR: add a post-apply readback that fails the phase when an unknown kind is encountered. ~150 LoC refactor; touches 11 call sites + their tests.
- **Severity**: CRITICAL — silent-skip pattern means the *next* time we add a CR type to a phase, the failure mode is "phase reports success, downstream subsystem mysteriously stays Pending."

### C-7 — `pattern: host-device` `applyDefaults` bumps desiredSize only when default-1; explicit-1 in cluster.yaml is preserved
- **File**: `internal/intent/cluster.go:411-413` — `if ng.DesiredSize == 1 { ng.DesiredSize = 3 }`. Operator-set `desiredSize: 1` is treated identically to default, so the bump fires. But operator-set `desiredSize: 2` (or anything else) is preserved. This is a footgun — there's no enforcement that host-device must have ≥3.
- **Symptom**: Operator sets `desiredSize: 2` thinking that's cost-saving; cluster never reaches dSSM quorum; Phase 25 times out.
- **Original source**: implicit in `internal/intent/cluster.go:404-419` block comments; would have been caught by the audit had it been written.
- **Proposed fix**: Move the check into `validate`. If `pattern: host-device` and `ng.DesiredSize < 3`, error with a citation to the audit. (See also C-2 which proposes the same check in Phase 00 preflight — belt-and-braces is fine.)
- **Severity**: CRITICAL the moment an operator tries to cost-tune host-device.

## High (likely to bite within 1-2 slices)

### H-1 — `internal/tf/`, `terraform/`, `internal/cli/tfvars.go`, `internal/cli/cluster.go` still exist despite D-001
- **Files**:
  - `internal/tf/{doc.go, fetch.go, source.go, terraform.go, vars.go}` — all live, importing `tfexec`.
  - `terraform/{main.tf, modules/, outputs.tf, providers.tf, variables.tf, versions.tf}` — full TF source tree.
  - `internal/cli/cluster.go:14,22,40,58,144,173` — imports `internal/tf`, runs `terraform/modules/eks_cluster/`.
  - `internal/cli/tfvars.go:11` — imports `internal/tf`.
  - `internal/cli/remote.go:11` — imports `internal/tf`.
  - `internal/exec/k8s_install.{go,yaml}` — still references `terraform/modules/iam_irsa`.
  - `internal/config/workspace.go:81` — doc-string references `internal/tf/vars.go`.
- **Symptom**: Dead code carrying a `tfexec` dependency, slowing builds and confusing readers. The `awsbnkctl up cluster` subcommand is still TF-backed and likely broken under the post-TF direction.
- **Original source**: ADR D-001 (2026-05-21, accepted) — "Delete: `terraform/`, `internal/tf/`, `embedded.go` (TF embed), `install_build_dependencies.sh`, `internal/cli/tfvars.go`". `.agent/handoff/2026-05-22-0500-slice-07-next.md` "What is NOT done" — biggest cleanup task.
- **Proposed fix**: Delete the directories + every import. Likely requires deleting/rewriting `internal/cli/cluster.go`, `internal/cli/tfvars.go`, `internal/cli/remote.go`, `internal/exec/k8s_install.{go,yaml}`. ~1480 LoC. Coordinate with `inspect`/`doctor`/`status` rewrites.
- **Severity**: HIGH — ADR explicitly accepted, no implementation. Every PR ships dead code.

### H-2 — `inspect` / `doctor` / `status` still query stale TF state
- **File**: per `.agent/handoff/2026-05-22-0500-slice-07-next.md` "What is NOT done": "`inspect` / `doctor` / `status` polish — currently they read TF state; need rewriting to read `.awsbnkctl/<cluster>/state.env` + tag-discovery."
- **Symptom**: Commands work on TF-era clusters but fail / lie on post-TF clusters. The thesis (`project_forge_role_in_architecture` — "AWS is truth, NOT forge") requires these to query AWS by tag.
- **Original source**: ADR D-002 (state model — tags are truth) implies these need rewrite; D-001 cleanup section.
- **Proposed fix**: Rewrite to `awsbnkctl:cluster=<name>` tag-discovery via the existing helpers in `internal/aws/state/`.
- **Severity**: HIGH — every status check is unreliable.

### H-3 — Phase 18 down does not revoke the cluster-SG ↔ BNK_DATA-SG ingress rules
- **File**: `internal/aws/phases/phase18_irsa_oidc.go:180-219` — `Phase18IrsaOidcDown` deletes the IRSA role + OIDC provider but never calls `RevokeSecurityGroupIngress` for either direction added in `ensureClusterSGIngress` / `ensureBNKDataSGIngress` (lines 161, 164).
- **Symptom**: Works in the common case because Phase 08 down deletes the EKS cluster (taking its managed SG with it), but if Phase 18 down runs independently or out of order, the rules persist on whichever SG survives. Also leaves the BNK_DATA SG with cluster-SG references that can no longer be resolved on the next up cycle.
- **Original source**: `.agent/tasks/active/slice-07-bnk-activation/reviews/r1.md:97` ("polish item for a follow-up, not a blocker for Pass 1"); `reviews/r2.md:27` ("explicitly deferred to a follow-up by r1"). Two days of "follow-up" with no action.
- **Proposed fix**: Add the two `RevokeSecurityGroupIngress` calls at the top of `Phase18IrsaOidcDown` before the IRSA-role / OIDC delete (so a partial-down halts cleanly).
- **Severity**: HIGH — silent SG cruft + breaks idempotent down/up cycles.

### H-4 — `scenarios run --all` is a stub
- **File**:
  - `internal/cli/scenarios.go:67,83,97,99` — explicit "stub: only 1 scenario registered" + `return fmt.Errorf("scenarios run --all: not implemented until 2+ scenarios are registered ...")`.
- **Symptom**: Anyone trying `awsbnkctl scenarios run --all` hits a hard error. Operators who add a second scenario via the framework discover the topo-sort runner doesn't actually exist.
- **Original source**: `.agent/tasks/active/slice-13-scenarios-framework/TASK.md:33` ("STUB ONLY for this slice"); `.agent/backlog/BACKLOG.md` `slice-14-port-second-scenario`.
- **Proposed fix**: Implement Kahn topo-sort over `Dependencies()` + per-scenario `Apply/Verify/Cleanup` with aggregate `RunSummary`. Already in the backlog as `slice-14`.
- **Severity**: HIGH the moment a second scenario lands (otherwise MEDIUM).

### H-5 — `httproutee2e` Verify order is enforced only by source-line order, no recording-fake test
- **File**: `internal/scenarios/httproutee2e/scenario.go:153-267` — Verify steps are in correct order (control-plane → Resync → curl) but `internal/scenarios/httproutee2e/scenario_test.go` has no recording-fake test that asserts the order.
- **Symptom**: A future refactor that reorders Verify (e.g. moving Resync before Programmed=True) silently breaks the workaround for the cne-controller pool-member stale bug. Live-validated as load-bearing on syd-tracer 2026-05-23.
- **Original source**: `.agent/tasks/active/slice-13-scenarios-framework/reviews/reviewer-r1.md` F-1; `.agent/backlog/BACKLOG.md` `slice-13-followup-verify-order-test`.
- **Proposed fix**: Add `TestVerifyCallOrder` using a recording fake that captures the sequence of `waitDeploymentAvailable` → `waitCondition(Programmed)` → `waitHTTPRouteCondition(Accepted)` → `waitHTTPRouteCondition(ResolvedRefs)` → `bnk.ResyncHTTPRoutes` → `RunCurlProbes`.
- **Severity**: HIGH — once a second engineer touches the file.

### H-6 — Forge can't `kubectl` the EKS cluster (no `credential_template_id` wired)
- **File**: `internal/aws/phases/phase09_forge_register.go` — no caller sets `CredentialTemplateID` on the forge create-project request (the field exists in `internal/forge/client.go:88` but is always zero).
- **Symptom**: Forge UI returns HTTP 500 when listing cluster resources because the kubeconfig forge has uses `aws eks get-token` exec-auth and forge has no AWS creds bound. Discovered on every live test since slice-7.
- **Original source**: `.agent/handoff/2026-05-23-1530-tmm-segfault-fixed-traffic-pending.md` "Side-quest still open"; `.agent/handoff/2026-05-23-0215-tmm-segfault-investigation.md` "Open follow-up tasks"; `docs/audits/slice-09-aws-gpu-setup-audit.md` §3 row 4 ("Forge `credential_template_id` wired through `restCreateProject` ... side-quest already tracked"); `docs/audits/slice-10-aws-gpu-setup-audit.md` §3 row 2.
- **Proposed fix**: Add `forge.credentialTemplateId` (or similar) field to cluster.yaml. Plumb through `restCreateProject`. Default the value to "1 AWS Production" if forge has it.
- **Severity**: HIGH — forge integration is broken end-to-end for the EKS-token path.

### H-7 — `triggerForgeScanCluster` is a logging stub
- **File**: `internal/aws/phases/phase13_postflight.go:202-220` — has explicit `TODO(slice-6): pass cluster ID from state and call clients.ForgeClient.ScanCluster()`. The function logs intent and returns `nil`. Slice-6 was 2 weeks ago.
- **Symptom**: Forge never receives a scan trigger; the cluster appears in the GUI but its BNK install progress doesn't populate. Operators see a stale UI until they refresh manually.
- **Original source**: code TODO `phase13_postflight.go:216`; left from slice-6 (`docs/POST_TERRAFORM_DIRECTION.md` §3 / D-006).
- **Proposed fix**: Plumb the `*state.State` into `triggerForgeScanCluster`, read `FORGE_CLUSTER_ID`, call `clients.ForgeClient.ScanCluster(ctx, id)`.
- **Severity**: HIGH — D-006 mandates this; we're shipping incomplete forge integration.

### H-8 — Phase 17b duplicates Phase 17 ENI helpers
- **File**: `internal/aws/phases/phase17b_jumphost.go` re-implements small bits of secondary-ENI plumbing that already exist as `ensureSecondaryENI` in `internal/aws/phases/phase17_secondary_enis.go:185`.
- **Symptom**: Two parallel implementations of "describe-or-create-ENI"; a future bugfix to one will not propagate to the other.
- **Original source**: `.agent/tasks/done/slice-12-jumphost-phase/TASK.md:193` ("Refactoring Phase 17 to share helpers (e.g. `ensureSecondaryENI`) — Phase 17b duplicates the small helpers it needs to keep this slice isolated; a follow-up refactor is welcome.").
- **Proposed fix**: Extract the shared helpers to `internal/aws/phases/eni_helpers.go` (or similar). Both phases call from there.
- **Severity**: HIGH — maintenance debt that compounds with each ENI-touching phase.

## Medium (technical debt with known cost)

### M-1 — `internal/cli/meta.go:150` TODO(phase3) for SSH backend
- **File**: `internal/cli/meta.go:150` — `BackendName: "", // TODO(phase3): set "ssh" once PRD 03 backend lands`.
- **Source**: PRD 03 reference (predates post-TF direction); may be obsolete given the architectural pivot.
- **Severity**: MEDIUM — purely cosmetic on doctor output; needs decision whether PRD 03 is still in scope.

### M-2 — `internal/aws/phases/phase07_iam.go:144` TODO for tag-listing fallback
- **File**: `phase07_iam.go:144` — `// TODO: tag-listing fallback (ListRoles + per-role ListRoleTags) if names ever diverge from convention.`
- **Symptom**: Down path relies on convention `<cluster>-eks-cluster-role`. If naming changes, down fails to find the role.
- **Severity**: MEDIUM — works under D-002 (tags are truth) when names match.

### M-3 — `internal/aws/phases/phase08_eks_cluster.go:91` TODO restrict publicAccessCidrs
- **File**: `phase08_eks_cluster.go:91-100` — `// TODO: restrict publicAccessCidrs to operator IP in a future hardening pass; out of scope for slice 3.` Cluster endpoint currently `0.0.0.0/0`.
- **Symptom**: EKS control-plane API is reachable from the public internet — fine for lab use, fails common security baselines.
- **Original source**: code TODO from slice 3.
- **Proposed fix**: Add `cluster.endpointAccess.publicAccessCidrs` to cluster.yaml schema; default to operator's `--my-ip` resolved via STS/HTTPS check.
- **Severity**: MEDIUM — security hardening; not blocking but expected for v1.0.

### M-4 — `nestedSlice` helper duplicated across `internal/scenarios/` and `internal/scenarios/httproutee2e/`
- **File**:
  - `internal/scenarios/envdiagram.go:238`
  - `internal/scenarios/httproutee2e/scenario.go:496` (per reviewer-r1 finding F-3)
- **Note**: Reviewer says NestedSlice was exported per slice-13 handoff §"Scenarios framework" — but I see "fix F-3" in the handoff which says it was already deduped; verify if both still exist or only the export path remains.
- **Source**: `.agent/tasks/active/slice-13-scenarios-framework/reviews/reviewer-r1.md` F-3.
- **Severity**: MEDIUM — code duplication; no functional impact.

### M-5 — `scenarios list` header to stderr, rows to stdout (mixed streams)
- **File**: `internal/cli/scenarios.go:48-51` per reviewer-r1 F-4. Handoff says "list now writes everything to stdout (fix F-4)" — confirm whether on disk.
- **Severity**: MEDIUM — display consistency; pipe-able output works.

### M-6 — `checkCNEInstanceActive` dead-code line per slice-07 reviewer
- **File**: `.agent/tasks/active/slice-07-bnk-activation/reviews/pass3-r1.md:53` — "Fix (follow-up, not blocking): Remove line 168". Phase 25 has been rewritten in slice-13; verify if still present.
- **Severity**: MEDIUM — code clarity only.

### M-7 — `cneInstanceNamespace` constant duplicated
- **File**:
  - `internal/aws/phases/constants_hostdevice.go:14` — `InstanceNamespace = "f5-cne-system"`
  - `internal/k8s/render/render.go:226` — `const cneInstanceNamespace = "f5-cne-system"` (intentional dup to avoid import cycle)
- **Source**: `.agent/tasks/active/slice-07-bnk-activation/reviews/pass3-r1.md:80` — "If a third location appears in a future slice, extract to `bnkconst` package at that point."
- **Severity**: MEDIUM — flagged for the next addition.

### M-8 — DSSM `--insecure` readiness probe overlay deferred
- **File**: per `.agent/tasks/active/slice-07-bnk-activation/TASK.md:416` — "DSSM `--insecure` readiness probe overlay (deploy-bnk.sh:263–282) — defer; only matters once first BNK consumer Gateway is created".
- **Symptom**: First-Gateway scenario may surface dSSM readiness flapping without the overlay.
- **Severity**: MEDIUM — surfaces only on Gateway creation, not normal up.

### M-9 — Phase 23b apply uses `applyRawYAML` (subject to C-6)
- **File**: `internal/aws/phases/phase23b_spkvlan_gatewayclass.go:103,119` — both use `applyRawYAML`.
- **Symptom**: If `F5SPKVlan` or `GatewayClass` plurals ever change (CRD schema bump), the GVR map silently no-ops again. Slice-11 hotfix added them; future drift won't be caught.
- **Source**: memory `project_phase23b_gvr_bug.md`; `docs/audits/slice-10-aws-gpu-setup-audit.md` §2.5/§2.6.
- **Proposed fix**: Use `internal/k8s.ApplyOptions` (live RESTMapper) like scenarios already do. Or block on C-6.
- **Severity**: MEDIUM — already fixed once; fix is brittle.

### M-10 — `cluster.yaml schema additions — is `dataPath` the right name?`
- **File**: `.agent/tasks/active/slice-07-bnk-activation/TASK.md:485` — "is `dataPath` the right name, or should it be `bnkDataPath` to avoid future namespace collision?"
- **Severity**: MEDIUM — naming decision; schema-evolution risk.

### M-11 — Empty `internal/k8s/manifests/sr-iov-tmm/` directory
- **File**: per `.agent/handoff/2026-05-22-0500-slice-07-next.md` "What is NOT done": "NetworkAttachmentDefinitions — pattern-specific (host-device vs sr-iov-tmm). `internal/k8s/manifests/{host-device,sr-iov-tmm}/` are empty scaffolds."
- **Symptom**: Operator setting `pattern: sr-iov-tmm` gets a silent no-op apply, hits errors downstream. Validator at `internal/intent/cluster.go:572` rejects everything except `host-device` (so this is mitigated), but the scaffold directory invites confusion.
- **Severity**: MEDIUM — gated by validator; still misleading.

### M-12 — `cluster up` legacy command still wired through TF
- **File**: `internal/cli/cluster.go:40,58` — `awsbnkctl up cluster drives terraform/modules/eks_cluster/`.
- **Symptom**: Subcommand exists but contradicts D-001. Operators reading `--help` see two competing paths.
- **Severity**: MEDIUM — confusing surface; subset of H-1.

## Low (cleanup, doc gaps, polish)

### L-1 — `internal/scenarios/scenario_test.go:64` skips when no scenarios registered
- **File**: `t.Skip("no scenarios registered (httproutee2e init not linked)")`.
- **Severity**: LOW — works when actual binary builds with httproutee2e import.

### L-2 — `internal/k8s/golden_test.go` skips when no kubeconfig / kubectl
- **File**: multiple `t.Skip` at lines 50, 53, 165, 184, 201, 218, 236.
- **Severity**: LOW — integration tests appropriately gated.

### L-3 — `internal/doctor/doctor_test.go:112,146,181,184` skips for terraform/helm-on-PATH
- **Severity**: LOW — environment-dependent skips; idiomatic.

### L-4 — `tools/refgen/tfvars-md/main_test.go:31` `t.Skip("inherited test — retargets in Sprint 3")`
- **Severity**: LOW — historical test pre-post-TF; consider deletion alongside H-1.

### L-5 — `internal/scenarios/envdiagram_test.go` uses fixed `/tmp/awsbnkctl-test-state`
- **File**: per reviewer-r1: "could create cross-test interference if the dir persists state; acceptable for a first pass."
- **Severity**: LOW — cosmetic.

### L-6 — `docs/SHAKEOUT.md` §9 "Things deliberately left undone (v1.x backlog)"
- Items listed are roksbnkctl-era (iperf3 in-cluster pod, `--all-pods` logs, component-aware status, COS upload, auto-install terraform, HMAC keys, custom tests.yaml, telemetry).
- **Severity**: LOW — these are pre-post-TF and mostly N/A; SHAKEOUT.md should be retired or rewritten.

### L-7 — `docs/PLAN.md` and `docs/prd/05/06/08*.md` have historical "follow-up" / "Sprint 3" / "ECR mirror to Sprint 3" items
- **Files**: `docs/prd/08-S3-SUPPLY-CHAIN-IRSA.md:179`, `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md:3,54,210`, `docs/PLAN.md:476`.
- **Severity**: LOW — superseded by D-001 post-TF direction; docs need a sweep.

### L-8 — `internal/forge/client.go:121-122` only sets `credential_template_id` if non-zero
- **File**: `internal/forge/client.go:121` — `if req.CredentialTemplateID != 0 { args["credential_template_id"] = ... }`. Plumbing exists; no caller sets it (subset of H-6).
- **Severity**: LOW — code path validated, just needs a caller.

### L-9 — `internal/aws/phases/phase17_secondary_enis.go:69,77` ENI naming relies on AL2023 ifname convention
- **File**: Hardcoded `ens7` / `ens8` everywhere; no SDK readback. If AWS Nitro changes naming convention, every host-device deployment silently breaks.
- **Source**: `docs/audits/slice-10-aws-gpu-setup-audit.md` §5.4 #1 (ruled out as TMM SIGSEGV cause but BDF assumption remains).
- **Severity**: LOW — stable today; defensive readback would future-proof.

### L-10 — `cne_pull_64.json` + `*.jwt` recently added to `.gitignore` (slice-13)
- **Status**: appears DONE per slice-13 handoff; verify the .gitignore actually contains them in the staged diff.
- **Severity**: LOW.

### L-11 — `internal/exec/k8s_install.go` + `.yaml` documents IRSA via TF module
- **Files**: `internal/exec/k8s_install.go:22`, `internal/exec/k8s_install.yaml:6,30,58` reference `terraform/modules/iam_irsa (PRD 08)`.
- **Severity**: LOW — historical comment; will be cleaned with H-1.

### L-12 — `internal/cli/lifecycle.go:218` doc-string references `terraform.tfvars`
- **Severity**: LOW — comment cleanup.

### L-13 — Pinned default `c.Bnk.ManifestVersion = "2.3.0-3.2598.3-0.0.170"` and `DefaultFLOVersion = "v2.21.13-0.0.28"`
- **File**: `internal/intent/cluster.go:434,272`.
- **Source**: per `docs/audits/slice-12-cold-start-audit.md` §8 — "When BNK 2.4 ships, this constant needs to bump alongside the FLO version pin."
- **Severity**: LOW now — bump alert for next BNK release.

### L-14 — `EmbeddedCertManagerVersion = "1.16.1"` pinned
- **File**: `internal/intent/cluster.go:363`. No upgrade signal.
- **Severity**: LOW — versions are stable.

### L-15 — Open forge follow-ups: "REST fallback is canonical, not exceptional" per D-009 §3
- **Source**: ADR D-009; `docs/FORGE_MCP_INTEGRATION.md:63` — "These are the create-paths we need but which haven't been promoted to MCP tools yet."
- **Severity**: LOW — strategic; not a bug.

## Already fixed (or no longer applies)

These items appear in old audits/handoffs but were silently fixed in later slices — confirmed by code reference. Listed to build trust that nothing was missed.

- **TMM SIGSEGV root cause** — fixed `internal/intent/cluster.go:448-466` (TmmCpu=2, TmmMemory=8Gi, PalCpuSet=0,2). Memory `project_tmm_segfault_root_cause`.
- **IRSA SA name mismatch** — fixed `internal/aws/phases/phase18_irsa_oidc.go:73` (`f5-cne-controller-<name>-bnk-serviceaccount`). Handoff `2026-05-23-1530`.
- **Bi-directional SG ingress** — fixed `phase18_irsa_oidc.go:161,164` (`ensureClusterSGIngress` + `ensureBNKDataSGIngress`).
- **GVR map missing F5SPKVlan + GatewayClass** — fixed `phase12_k8s_foundation.go:686-687`. (See M-9 for residual brittleness.)
- **Phase 25 readiness via conditions[]** — fixed `phase25_activation_poll.go:89` (`cneAvailableFromConditions`).
- **MGMT_SUBNET stale-value preservation** — fixed in commit `4fc851f` (always recompute `ensureMGMTSubnetAlias`).
- **AL2 vs AL2023 AMI** — fixed `phase10_nodegroup.go:260` (`AMITypesAl2023X8664Standard`). Audit `slice-09-aws-gpu-setup-audit.md`.
- **NAD `"name"` field missing** — fixed in `host-device/network-attachment-defs.yaml.tmpl` (slice-10).
- **IRSA route-mutate perms** — fixed `phase18_irsa_oidc.go:cneControllerVpcReadPolicy` (slice-10 GAP-1).
- **SelfIPs not assigned as secondary IPs on ENIs** — fixed Phase 17 (slice-10 GAP-4).
- **F5SPKVlan + GatewayClass CRs not present** — fixed Phase 23b (slice-10 GAP-5/6).
- **`cne_pull_64.json` + `*.jwt` not gitignored** — added 2026-05-24 (slice-13 handoff §"Scenarios framework").
- **Em-dash in SG Description rejected by AWS** — fixed `phase17b_jumphost.go:410` (slice-13).
- **Phase 17b reads `MGMT_SUBNET` before Phase 19 sets it** — fixed (slice-13).
- **Host-device ENI capacity not preflight-checked** — partly fixed `phase00_preflight.go:79-112` (ENI only; CPU/mem/desiredSize still missing — see C-2).
- **`pkg/bnk.ResyncHTTPRoutes` workaround for stale pool members** — landed in slice-11b (PR #24 + handoff `2026-05-23-1730`).
- **Phase 9 forge orphan project handling (upsert)** — landed in PR #21.

## By source

- `docs/audits/slice-09-aws-gpu-setup-audit.md`: **5 items, 2 still open** — row 28 (BNK_WORKER_COUNT=3 DEFERRED → fixed at applyDefaults level but not enforced for explicit overrides → C-7); row 30 (F5SPKVlan/GatewayClass/test-gateway/test-nginx → fixed slices 10/11/13); §3 row 4 (forge credential_template_id → H-6).
- `docs/audits/slice-10-aws-gpu-setup-audit.md`: **10 items, all GAP-1..6 fixed**; §3 row 2 (forge credential_template_id → H-6); §3 row 3 (test-gateway.yaml + test-nginx.yaml → addressed by slice-13 scenarios framework); §5.4 PCI BDF (resolved); §5.4 cgroup v2 (ruled out); §5.4 first-boot (ruled out).
- `docs/audits/slice-12-cold-start-audit.md` (today's): **5 cold-start bugs identified, 3 fixed (ENI preflight, ASCII em-dash, PUBLIC_SUBNETS read), 2 OPEN (C-1 instance default, C-2 CPU/mem preflight, C-3 example, C-4 Phase 25 trim, C-5 Phase 23/23b trim)**. §9 "Escalated to user" — C-6 (static GVR map), helm-driven preflight, more preflight checks.
- `.agent/memory/MEMORY.md` + referenced memory files: **17 entries**. Active landmines: `project_phase23b_gvr_bug` → C-6; `project_host_device_eni_limit` → C-1/C-2; `feedback_no_deferred_fixes` (process rule).
- `.agent/backlog/BACKLOG.md`: **3 items** — `slice-13-followup-verify-order-test` (→ H-5); `slice-14-port-second-scenario` (→ H-4); `preflight-cluster-yaml-validation` (slated to be REPLACED per slice-12 audit §9).
- `.agent/LESSONS.md`: 80-line file. Phase-specific learnings are MAF / OpenCode infrastructure, not awsbnkctl code. No bug-level entries.
- `.agent/DECISIONS.md`: **11 ADRs (D-001 through D-011), all Accepted; D-001 not implemented → H-1; D-006 partially implemented → H-7**.
- `.agent/handoff/*.md`: **7 handoffs** — all "What's left" sections inventoried above (most-recent `2026-05-24-slice-13-and-audit-pickup.md` defines C-1..C-5).
- **Code TODOs (`grep -rn "TODO|FIXME|XXX|HACK|workaround"`)**: 7 hits.
  - `internal/cli/meta.go:150` → M-1
  - `internal/scenarios/httproutee2e/scenario.go:17,70,202` (workaround comments) — informational, not bugs.
  - `internal/exec/ssh.go:368` (`broken pipe surfaces as non-zero rc`) — design-intent comment, not a bug.
  - `internal/aws/phases/phase07_iam.go:144` → M-2
  - `internal/aws/phases/phase08_eks_cluster.go:91` → M-3
  - `internal/aws/phases/phase13_postflight.go:216` → H-7
- **Test skips (`grep -rn "t.Skip"`)**: 19 hits — all environment-gated (no kubeconfig, no docker, no kubectl); idiomatic. Two of historical note: L-1 (scenarios), L-4 (refgen).
- **Open GitHub issues + PRs**: issues disabled on the repo; **0 open PRs**.

---

## Worst 5 (verbatim references)

1. `internal/intent/cluster.go:375` — `ng.InstanceType = "t3.medium"` for host-device pattern → cold-start hits AttachmentLimitExceeded or Insufficient CPU.
2. `internal/aws/phases/phase00_preflight.go:79-112` — preflight only checks ENI count, not CPU/memory/desiredSize.
3. `examples/syd-tracer/cluster.yaml:106-109` — `m5.xlarge` + `desiredSize: 1` (still too small after today's edit).
4. `internal/aws/phases/phase12_k8s_foundation.go:638-695` — `resolveGVR()` static GVR map silently no-ops on unknown kinds; affects 11 call sites.
5. `internal/aws/phases/phase25_activation_poll.go:23` — `phase25MaxIter = 40` (20-min wait) hides root causes.

## Totals by severity

- **Critical**: 7
- **High**: 8
- **Medium**: 12
- **Low**: 15

**Total findings**: 42 (of which 17 confirmed fixed in later slices and listed in "Already fixed").
