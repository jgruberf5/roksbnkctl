> **⚠️ ARCHIVED (2026-05-26):** This document describes the pre-pivot Terraform-embedded design and is retained for history only. The project now uses the Go-SDK phased provisioner driven by cluster.yaml — see [README.md](../README.md) and [docs/POST_TERRAFORM_DIRECTION.md](POST_TERRAFORM_DIRECTION.md).

# NEXT_STEPS — session handoff

Snapshot of where the awsbnkctl AWS-retarget left off, and what to do when picking it back up.

## 🎯 Picking this up cold? Read this first (2026-05-19)

Most recent session's work, in order:

1. **Detached the fork from `jgruberf5/roksbnkctl`** — `upstream` remote removed, 11 inherited tags deleted from local + origin. GitHub platform-level fork badge intentionally kept.
2. **Wrote `docs/FORGE_MCP_INTEGRATION.md`** — canonical plan for the awsbnkctl → bnk-forge handoff over MCP.
3. **Opened `bnk-forge#114`** — companion PR adding 62 MCP tools (`create_project`, `create_cluster`, full Tier A–E lifecycle + new `cloud_auth` governed module). Still open against forge `staging` last we checked.
4. **Shipped PR #1 + PR #2** — `awsbnkctl forge {register, status, unregister}` over MCP; `awsbnkctl up --register-with-forge` post-apply hook.
5. **Shipped PR #3** — `docs/FORGE_MCP_INTEGRATION.md` correction: peer-read model + two-roles framing (forge is GUI + k8s/BNK CRUD for awsbnkctl-deployed infra; forge owns IaC ONLY for its own blueprints).
6. **Backports from `jgruberf5/roksbnkctl`** — three small ports re-implemented (not cherry-picked due to codebase divergence): PR #4 = `--var-file` relative-path resolution + threading the flag through to `tfws.Plan` (it was a silent no-op on our side post-retarget); PR #5 = `terraform.applied.tfvars` snapshot after a successful apply; PR #6 = `awsbnkctl status` per-phase deployment lines. PRs #4 + #5 merged. **PR #6 still open** last we checked.
7. **Interrogated `mwiget/dpubnkctl`** — sibling *bnkctl tool for BlueField DPUs. **This is the architectural direction we want awsbnkctl to evolve toward.** Memory entry at `reference_dpubnkctl_architecture.md` carries the ranked port-priority list; the load-bearing item is **PoC-as-repo state model** (replaces `~/.awsbnkctl/<workspace>/` with a local git dir containing `poc.yaml`, `keys/`, `inventory/`, `artifacts/`, `journal/`, `AGENTS.md`, `personas/`). User endorsed direction 2026-05-19.

**Where awsbnkctl's open work sits:**

| Item | State |
|---|---|
| `bnk-forge#114` (62 MCP tools) | open on forge side, waiting on review |
| PoC-as-repo migration | not started — needs a PRD draft first |
| AGENTS.md + personas via `init` | not started — small, good first item |
| `forge:` block in workspace.yaml | not started — small, replaces `--register-with-forge` flag with persistent config |
| Named topology examples dir | not started — small |
| `validate` command | not started — small |
| `--yolo` + `--confirm-cluster` two-factor gate | not started — small, infrastructure for live destroy |
| PRD 07 spike | operator-driven, $5–15 AWS cost, gates v1.0 |
| `awsbnkctl status` / `doctor` push-to-forge | deferred (was originally P5 of forge integration plan) |

Architectural memory entries worth reading before changing anything load-bearing:
- `project_forge_role_in_architecture.md` — peer-read thesis, scaffolding-not-state-mirror, two-roles framing
- `project_forge_mcp_integration.md` — phasing of the forge integration work
- `reference_dpubnkctl_architecture.md` — direction we're heading

## State as of last session

All six AWS-retarget sprints (Sprint 0 → Sprint 6) committed and pushed to `JLCode-tech/awsbnkctl@main`. **`v0.9.0-rc1` cut, published as prerelease, and live at https://github.com/JLCode-tech/awsbnkctl/releases/tag/v0.9.0-rc1** with 6 binary archives + checksums (linux/macOS/windows × amd64/arm64). v1.0 awaits the operator-run PRD 07 spike.

Commit timeline:

| Commit | Sprint | Outcome |
|---|---|---|
| `9a59806` | 0 prep | Fork retarget docs (README/CHANGELOG/MIGRATING/PLAN/PRDs/prompts) |
| `7861e63` | 0 | IBM strip + AWS stub + identity rewrite (Go module path, binary rename) |
| `203f28a` | 1 | EKS cluster + SR-IOV node group module + `internal/aws/` (offline) |
| `dc8e5a6` | 2 | S3 supply chain + IRSA + workspace AWS retarget (offline) |
| `c40c238` | 3 | Five reusable modules ported + first end-to-end `up --dry-run` |
| `727b2bb` | 4 | Test surface + doctor Service Quotas + AWS E2E chapters |
| `018e2bf` | 5 | Book retarget + IBM-residue 297 → 1 + reference chapter regen |
| `e2debed` | 6 | Hardening + ops-pod IRSA retarget + v0.9-rc release-artefact prep |

Verification state (last green check on commit `e2debed`):
- `go build / vet / test / gofmt` clean across 13 packages
- `terraform validate` clean on root + all 8 modules
- `awsbnkctl init/up/down/test/doctor --dry-run` all work offline
- `awsbnkctl doctor` reports 6 AWS pre-flight rows when workspace configured
- `goreleaser build --snapshot --clean` produces 6 archives (linux/macOS/windows × amd64/arm64)
- `gosec` + `govulncheck` clean (findings documented + accepted)
- `mdbook build book/` + `cspell` zero findings (verified in CI)
- 10 CI jobs configured

## Done since previous handoff

- **`v0.9.0-rc1` cut + published.** Annotated tag pushed to `main`; release workflow built and attached 6 binary archives + `checksums.txt`; release flagged as prerelease. Note: tag pattern in `.github/workflows/release.yml` is `"v*.*.*"` (three-dot), so the original `v0.9-rc1` plan was renamed to `v0.9.0-rc1`.
- **Fork-Actions gotcha resolved.** GitHub disables Actions on new forks by default; had to enable them via Settings → Actions UI before workflows fired (operator-side click; no API path). First release was triggered manually via `workflow_dispatch` after enabling. Subsequent pushes auto-fire normally.
- **Functional fork detachment (2026-05-18).** Removed `upstream` git remote; deleted 11 inherited tags (`v0.7-rc1`, `v0.8-rc1`, `v0.9.0`, `v1.0.0`, `v1.0.1`, `v1.0.2`, `v1.1.0`, `v1.1.1`, `v1.1.2`, `v1.2.0`, `v1.2.1`) from local and origin. Only `v0.9.0-rc1` remains. `go install github.com/JLCode-tech/awsbnkctl@vX.Y.Z` no longer misresolves to a roksbnkctl commit. GitHub platform-level fork badge intentionally kept — operator chose to skip the support-ticket / delete-and-recreate path.
- **Forge MCP integration plan written (2026-05-18).** Canonical plan at `docs/FORGE_MCP_INTEGRATION.md`. Defines the post-`up` handoff to `bnk-forge-v2` (localhost dev at `:8000`): awsbnkctl creates a forge project, registers the EKS cluster, adopts TF state as project-modules, triggers a forge scan. Phased P0 → P5 (start with REST in P1; switch to MCP transport in P4 after gap-fill PR against forge lands in P3).
- **Forge MCP gap-fill PR opened (2026-05-18).** `sp-prod-field/bnk-forge#114` against `staging`. Adds 62 MCP tools (4 initial + Tier A–E) covering project/cluster/module CRUD + execution + diagnostics, stack instance lifecycle, AWS SSO + credentials + STS, k8s resource lifecycle + rollouts + node ops + tunnels, project secrets. Promotes new `cloud_auth` to governed modules. 220/220 mcp-server tests green; no backend changes. With this merged, MCP becomes the complete surface — no REST fallback required.
- **awsbnkctl ↔ forge MCP wiring implemented (2026-05-18).** Branch `feat/forge-mcp-integration`. New packages `internal/forge/` + `internal/forge/mcp/` (minimal JSON-RPC over Streamable HTTP client). New CLI: `awsbnkctl forge {register, status, unregister}` — kubeconfig generated in-process via the existing EKS presigned-URL flow, handed to forge via `create_project` + `create_cluster` MCP tools. Idempotent (re-running reuses existing `forge_link.json`). 49 new unit tests; full `go test ./...` green. Skips Option A (REST) — PR #114 made the MCP surface complete, so we jumped to Option B directly.

## Immediate next actions

### 1. (Optional, ~10 min) Attach book PDF to the release

The current `v0.9.0-rc1` ships without the PDF (Sprint 6 validator Issue 3 flagged that the docker-based PDF build wasn't exercisable in their sandbox). To attach:

- Build locally: `make book-pdf` (uses the docker-based renderer — bakes Mermaid diagrams as vector SVG)
- Upload: `gh release upload v0.9.0-rc1 awsbnkctl-book-v0.9.0-rc1.pdf -R JLCode-tech/awsbnkctl`
- Or extend `.goreleaser.yml` to attach the PDF on future tag pushes (v1.0 candidate)

### 2. Run the PRD 07 spike (requires AWS account, ~$5–15 cost)

Operator-driven validation that gates `v1.0`. Full protocol at `docs/prd/07-EKS-CLUSTER-SRIOV.md` § "Spike protocol".

Summary (day-by-day):

**Day 1 — cluster + node group**
- `eksctl create cluster --without-nodegroup --version 1.30 --region us-east-1 ...`
- `eksctl create nodegroup --managed=false --node-type c5n.4xlarge --nodes 2 ...`
- Verify nodes report `Ready` and surface primary ENA interface

**Day 2 — SR-IOV stack**
- Install Multus thick-plugin DaemonSet (upstream `k8snetworkplumbingwg/multus-cni`)
- Install SR-IOV CNI binaries (upstream `k8snetworkplumbingwg/sriov-cni`)
- Install SR-IOV device plugin (upstream `k8snetworkplumbingwg/sriov-network-device-plugin`) configured to discover ENA VFs by vendor/device ID
- Verify `kubectl describe node <node>` shows `intel.com/sriov: <N>` in `Allocatable`

**Day 3 — pod schedules onto VF + BNK compatibility**
- Apply `NetworkAttachmentDefinition` referencing SR-IOV CNI
- Schedule pod requesting `intel.com/sriov: 1`
- `kubectl exec` in; verify second interface; verify `ethtool -i <iface>` reports ENA driver + SR-IOV active
- **BNK-specific check:** verify `ip link show` surfaces the VF in a state BNK's CNEInstance reconciler accepts — this is the load-bearing question

Spike fail modes + mitigations are catalogued in PRD 07 § "Spike fail modes". The hypothesis to validate: **does ENA SR-IOV match BNK's reference (Mellanox SR-IOV) closely enough that CNEInstance reconciles cleanly?**

After the spike:
1. Fill in `docs/prd/07-EKS-CLUSTER-SRIOV.md` § "Resolved in spike" with findings
2. If hypothesis holds → cut `v1.0` annotated tag matching the `v*.*.*` pattern (`git tag -a v1.0.0 -m "..."  && git push origin v1.0.0`); release workflow fires automatically now that Actions is enabled on the fork
3. If hypothesis breaks → file Sprint 7 work to address (PRD 07 lists mitigations: ENA tuning, FLO patch, multi-ENI fallback, EC2-metal as last resort)

## How to resume work with Claude Code

The four-role parallel-agent dispatch pattern documented in `prompts/README.md` is what got us here. If you want to dispatch a Sprint 7 cleanup pass or a v1.x feature sprint:

1. Author `prompts/sprint7/{README,architect,staff,validator,tech-writer}.md` (use sprint6 as template)
2. Dispatch architect + staff + validator in parallel via Claude Code's Agent tool
3. Dispatch tech-writer after the three return
4. Integrator (you, or another agent) folds the four issue files and commits

Each sprint's task brief is checked in at `prompts/sprint<N>/` for auditability — that pattern carries forward.

### Practical operational notes (from this session)

- **Disk hygiene matters.** `terraform/modules/*/.terraform/` directories grow to 600-800MB each during validate; clean with `find terraform -name '.terraform' -type d -exec rm -rf {} +` after any module work. `.gitignore` excludes them but they fill the local disk fast.
- **Agent 529 errors.** Anthropic API can return `529 Overloaded` mid-dispatch; agents may complete substantial work before the error. Always check git state on disk before retrying — sometimes the work landed and you just need to take over the final tasks (file issues, commit) as integrator.
- **Test contract carry-over.** `internal/doctor/doctor_test.go::TestRunWithWhy_StockDevBox_NoWorkspace` enforces an inherited "stock dev box = no warnings" contract. Doctor changes that surface new warnings on a workspace-less box require updating this test.
- **Fork Actions disabled by default.** Even with `actions/permissions` reporting `enabled:true`, a freshly-forked repo has Actions disabled at the UI layer. The user must click "I understand my workflows, go ahead and enable them" in the Actions tab before any workflow fires. There's no API path to flip this. Tags pushed before this is done don't retroactively trigger — re-trigger via `workflow_dispatch` or delete-and-re-push.
- **`gh` defaults to upstream on forks.** `git remote -v` shows both `origin` (the fork) and `upstream` (the source); `gh` picks one heuristically. Use `-R JLCode-tech/awsbnkctl` explicitly when querying the fork to avoid confusing "no runs" output from the upstream repo.
- **Release tag pattern.** `.github/workflows/release.yml` matches `"v*.*.*"` (three dots required). `v0.9-rc1` wouldn't trigger; `v0.9.0-rc1` does.

## v1.x backlog (deferred from v1.0)

Consolidated in `docs/PLAN.md` § "What's deferred to post-v1.0". Highlights:

- **`--irsa-role=auto`** flow for `awsbnkctl ops install` (the AWS equivalent of the inherited IBM `--trusted-profile=auto`). v1.0 requires operator to pre-create the IRSA role and pass via `OPS_IRSA_ROLE_ARN`.
- **ECR mirror first-class story** for air-gapped customers. Sprint 2 shipped the `terraform/modules/ecr_mirror/` module as optional (gated on `var.enable_ecr_mirror`); v1.x makes it the default with automated FAR-image sync workflow.
- **Karpenter / EKS Auto Mode integration** — currently no clean integration with SR-IOV device plugins.
- **Calico-on-EKS as alternative CNI** to AWS VPC CNI.
- **AWS Secrets Manager** as the JWT home instead of S3 (current design uses S3 for both FAR archive + JWT).
- **Multi-region GSLB testing** — `awsbnkctl test dns` is single-region in v1.0.
- **Air-gapped install path** — currently assumes outbound HTTPS to AWS APIs + ECR + FAR registry.
- **Homebrew tap** — same as roksbnkctl's v1.x roadmap.
- **Doctor visibility on stock dev box** — Sprint 1 tech-writer Issue 1: AWS rows currently workspace-gated to preserve inherited test contract. v1.x relaxes the contract.
- **Book PDF in releases** — `.goreleaser.yml` doesn't attach `awsbnkctl-book-<tag>.pdf` today. Either extend goreleaser to invoke `make book-pdf` + add an archive entry, or run the build locally pre-tag and `gh release upload`. v1.0 candidate decision.
- ~~**Inherited roksbnkctl release tags on the fork**~~ — resolved 2026-05-18. All 11 inherited tags deleted from local + origin.
- **Forge MCP integration (P1–P5)** — `docs/FORGE_MCP_INTEGRATION.md` is the plan. Next concrete work is P1 — implement `awsbnkctl forge {register, status, unregister}` over REST against `bnk-forge-v2` localhost (`:8000`, admin/changeme). New Go package `internal/forge/`. Tests: unit + `forge register --dry-run` golden + opt-in `FORGE_E2E=1` script.

## Open issue files worth a read before resuming

The sprint-by-sprint audit trail lives in `issues/issue_sprint<N>_<role>.md`. Especially:

- `issues/issue_sprint6_tech-writer.md` — final v1.0-readiness preview with residual findings
- `issues/issue_sprint5_tech-writer.md` — book-retarget verification
- `issues/issue_sprint3_tech-writer.md` — first end-to-end `up --dry-run` review
- `issues/issue_sprint1_staff.md` — staff agent's design notes from the EKS module build

## Memory pointers

This session built up auto-memory at `~/.claude/projects/-Users-j-lucia-Code-github/memory/`. A future Claude Code session will load `MEMORY.md` automatically; specific entries referencing this project will surface relevant context.

## Quick orientation for a future agent

If you're a Claude Code agent picking this up cold:

1. `git log --oneline -10` to see the sprint-commit timeline (latest: `4aa2689` docs, `e2debed` Sprint 6, plus `v0.9.0-rc1` tag)
2. Read this file (`docs/NEXT_STEPS.md`) and `docs/PLAN.md` § Sprint 6 close + "What's deferred to post-v1.0"
3. Skim `docs/prd/07-EKS-CLUSTER-SRIOV.md` for the load-bearing design + spike protocol
4. Skim `agents/README.md` + `prompts/README.md` for the four-role dispatch pattern
5. `go build ./... && go test ./...` to verify the green-gate still holds
6. `gh release view v0.9.0-rc1 -R JLCode-tech/awsbnkctl` to confirm the rc is still live
7. Confirm with the operator before:
   - cutting tags (visible action, can't easily un-tag in releases — and remember `v*.*.*` pattern requirement)
   - running `terraform apply` against live AWS (cost + irreversible state)
   - running the PRD 07 spike (~$5-15 in real AWS resources)
   - deleting any inherited roksbnkctl tags (historical refs; deletion affects anyone with cached `go install` paths)
