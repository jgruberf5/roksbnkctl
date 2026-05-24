# Handoff — Next session: cold-start reliability (all 42 findings)

**Written**: 2026-05-24 (AEST), end of session
**Branch to pick up**: `slice-13-scenarios-framework` is open as a DRAFT PR on GitHub. Do NOT merge it until this work lands first.
**Cluster state**: clean. All AWS-side syd-tracer infra torn down manually. Verified zero residue (no VPC, no IAM, no OIDC, no EICE).

## Sprint-0 prerequisite: a local CI gate that actually matches CI

User feedback at session close: *"you seem to have drifted a long way away and hence we are doing snowflake fixes causing more brittleness."* Per `A_Project_Managers_Guide_to_Agentic_Developed_Products.md` Chapter 6 § "What the integrator does" item 6 + Chapter 9 (Sprint 0 foundations): **before any further feature work, ensure the local lint+test gate matches CI exactly.**

Current gap: `Makefile:408` `make lint` calls `gofmt -d -l . && go vet ./... && (command -v staticcheck >/dev/null && staticcheck ./... || echo "skipping")`. The skip is silent — staticcheck never runs locally on this dev machine because `go install honnef.co/go/tools/cmd/staticcheck@latest` is classifier-blocked. CI runs it via the `dominikh/staticcheck-action@v1` GitHub Action. **Every staticcheck failure today is a push → CI fail → patch → push cycle**, which is exactly the snowflake-fix antipattern.

**First action of the new session, before any of the 42 sweep findings:**

1. Vendor staticcheck via `tools.go` + go.mod `tool` directive (Go ≥1.24) OR via a `tools/` package that uses `//go:build tools`. Both are standard Go patterns. After this, `make lint` becomes `gofmt -d -l . && go vet ./... && go run honnef.co/go/tools/cmd/staticcheck@v0.5.x ./...` with the version pinned in `go.mod` — no global install needed.
2. Make `make lint` NOT silently skip staticcheck — if it fails to acquire, the target fails. Same for any other missing tool (cspell can stay external since it's nodejs).
3. Add `make ci-local` as the canonical pre-push gate matching the CI matrix: vet + fmt + staticcheck + race tests + spellcheck on changed `.md` files. Or rename `lint` → `ci-local`.
4. Update `scripts/pre-commit.sh` (referenced by `Makefile:412 pre-commit-install`) to call `make ci-local`. Run `make pre-commit-install` once on a fresh clone.
5. Document in `book/src/` (architect role per the guide) — "How to develop against awsbnkctl without surprising CI."

Once this Sprint-0 foundation is in place, the 42 findings get fixed in batches with confidence that each push will be green.

## The goal proper (after Sprint-0)

**Make `awsbnkctl up → install → traffic test → down` cold-start reliable.** This is the primary user-facing promise of the tool.

User clarification (2026-05-24): *"slice-13 is part of cold-start reliability — it's just the last part of it."* The scenarios framework isn't a separate feature; it's the validator that proves the foundation works. The single PR has one acceptance criterion: a clean fresh `up → scenarios run http-routing-e2e → down` cycle returns 5/5 HTTP 200 and zero AWS residue, with zero human intervention.

So this is ONE PR, not two. The slice-13 framework code (already on the branch) + the 42 sweep findings + the live e2e green = the deliverable. The "DRAFT" label flips to "ready" only when all of that holds together.

User direction verbatim (2026-05-24): *"deferring bugs and fixes is ALWAYS wrong, we almost always NEVER come back to it"* and *"42 findings… are they not worthy of fixing? I don't want 7 fixed and then the other 36 bite us."* Translation: **the next PR fixes all 42, or escalates each individual exception with a concrete scope reason.** No silent backlog rot.

## The 42 findings

Authoritative source: **`docs/audits/2026-05-24-latent-bugs-sweep.md`** (32 KB, 600 lines). Counts:

- **7 Critical** (C-1 … C-7) — will bite on the next cold-start
- **8 High** (H-1 … H-8) — will bite within 1-2 slices
- **12 Medium** (M-1 … M-12) — technical debt with known cost
- **15 Low** (L-1 … L-15) — cleanup, doc gaps, polish

Read the sweep doc first. It already cross-references each finding back to its original audit / memory / handoff source — none are new discoveries; every one was previously documented and deferred.

## Recommended commit chunking (for the cold-start reliability PR)

To keep the PR reviewable, split into 5 sub-commits. The chunks roughly match severity tier × subject. You may rebase / re-order — what matters is that all 42 are fixed or escalated by the time the PR opens.

### Chunk 1 — Critical (cold-start blocking) — must do first

- **C-1**: `intent/cluster.go` default → m6i.4xlarge + desiredSize=3 for `pattern: host-device` (**ALREADY DONE in this session, in the slice-13 branch — verify still present**)
- **C-2**: extend `phase00_preflight.go` checker — currently checks only `MaximumNetworkInterfaces ≥ 4`. Add: `VCpuInfo.DefaultVCpus ≥ 16`, `MemoryInfo.SizeInMiB ≥ 65536`, `nodeGroups[0].DesiredSize ≥ 3`. Each gets a unit test in `phase00_preflight_test.go`. Rename `checkHostDeviceENICapacity` → `checkHostDeviceCapacity`.
- **C-3**: `examples/syd-tracer/cluster.yaml` instanceType=m6i.4xlarge × 3 (**ALREADY DONE in this session**)
- **C-4**: `phase25_activation_poll.go` `phase25MaxIter = 40 → 12`. On exhaustion, capture `kubectl get pods -A` + `kubectl describe pod <stuck>` to stderr so failure is informative.
- **C-5**: `phase23_license.go` + `phase23b_spkvlan_gatewayclass.go` CRD-wait timeouts `10m → 3m`. CRD apply is sub-second.
- **C-6**: `phase12_k8s_foundation.applyRawYAML` — replace static `resolveGVR` map with a live RESTMapper. 11 callers depend on this; coordinate carefully. **This was the most-deferred item in the repo** — `MEMORY.md project_phase23b_gvr_bug` recorded "Followup: replace static map with live RESTMapper or add post-apply readback verification" on 2026-05-23 and never landed.
- **C-7**: `applyDefaults` nuance — bumps desiredSize only when zero-default; explicit `desiredSize: 1` in cluster.yaml is preserved. Decide: either re-validate against `pattern: host-device` after `applyDefaults` runs, OR refuse `desiredSize < 3` in `validate()`. Recommendation: refuse in validate — explicit < 3 with host-device pattern is always wrong.

### Chunk 2 — High (1-2 slice horizon)

- **H-1, H-2**: Legacy TF code still exists despite D-001 (post-terraform pivot). Delete `internal/tf/`, `terraform/`, `internal/cli/tfvars.go`, `internal/cli/cluster.go`. Update `inspect` / `doctor` / `status` callers to query AWS directly via Phase clients (per `project_forge_role_in_architecture` memory — AWS is the source of truth).
- **H-3**: Phase 18 sets up cluster SG ↔ SG_BNK_DATA cross-references; phase 07 down doesn't revoke them. Add symmetric revoke before SG delete. Test simulates phase 18 setup then phase 07 down.
- **H-4**: `scenarios run --all` is currently a stub. Needs a real topo-sort impl based on `Dependencies()`. Trivially unblocked once a 2nd scenario lands; until then keep the stub but improve the message.
- **H-5**: `httproutee2e/scenario.go` Verify order (control-plane → resync → curl) is enforced only by source-line order. Add a recording-fake test that asserts the sequence. Slice-13 reviewer F-1 finding.
- **H-6**: Forge cluster-9 401 — `credential_template_id` not wired; forge can't `kubectl` the EKS cluster. Investigate, fix the credential template registration on forge-side, or document workaround.
- **H-7**: `triggerForgeScanCluster` is a logging stub — implement the actual scan trigger via forge REST API.
- **H-8**: Phase 17b duplicates Phase 17 ENI helper logic. Extract shared helpers into a sub-package or shared file.

### Chunk 3 — Medium (technical debt)

- **M-1**: `internal/cli/meta.go:150` TODO(phase3) for SSH backend — implement or remove.
- **M-2**: `internal/aws/phases/phase07_iam.go:144` TODO for tag-listing fallback — implement.
- **M-3**: `internal/aws/phases/phase08_eks_cluster.go:91` TODO restrict publicAccessCidrs — make configurable in cluster.yaml.
- **M-4**: `nestedSlice` helper duplicated — **ALREADY DEDUPED in this session as `scenarios.NestedSlice`** (verify on disk).
- **M-5**: `scenarios list` header to stderr, rows to stdout — **ALREADY FIXED in this session** (verify on disk).
- **M-6**: `checkCNEInstanceActive` dead-code line per slice-07 reviewer — remove.
- **M-7**: `cneInstanceNamespace` constant duplicated — consolidate.
- **M-8**: DSSM `--insecure` readiness probe overlay deferred — implement.
- **M-9**: Phase 23b apply uses `applyRawYAML` — superseded by C-6 fix.
- **M-10**: cluster.yaml schema additions — is `dataPath` the right name? **Escalation candidate** (debate, not bug — user picks bundle vs separate PR).
- **M-11**: Empty `internal/k8s/manifests/sr-iov-tmm/` directory — delete or populate.
- **M-12**: (read the sweep doc for the 12th)

### Chunk 4 — Low (polish)

L-1 through L-15. Most are 1-line fixes (typos, dead comments, log message inconsistencies). Read sweep doc §"Low" and apply.

### Chunk 5 — Acceptance test + docs

- `awsbnkctl up --config examples/syd-tracer/cluster.yaml --auto` succeeds end-to-end in ~25 min with **zero human intervention**.
- `awsbnkctl scenarios run http-routing-e2e` (or `awsbnkctl test traffic`) returns 5/5 HTTP 200 (this validates the slice-13 PR's branch from behind the cold-start fixes).
- `awsbnkctl down --yes` cleans up to **zero AWS-side residue** (no orphaned VPC, IAM, SG, EIP, EICE, OIDC).
- Update `docs/audits/2026-05-24-latent-bugs-sweep.md` with a closing entry per finding: "FIXED-IN-PR-#NNN" or "ESCALATED-USER-CHOSE-X."

## Escalation policy (per the no-defer rule)

If during the work you find a finding that genuinely needs its own PR (not bundleable):

1. **Do NOT** add it to BACKLOG.md.
2. **Do** raise it to the user explicitly with: a scope estimate (LoC, files touched), a reason it can't bundle (e.g. "this changes the public CLI surface and needs a versioning discussion"), and a concrete next-step.
3. **Only** after explicit user confirmation can it become a separate PR.

The audit sweep doc already noted: *"Every Critical finding aligns with at least one already-documented audit/handoff/memory entry; none are new discoveries."* The bugs exist on paper. Acting on them is the work.

## What's already on the slice-13 branch (DRAFT PR — keep extending it)

The slice-13 branch carries the scenarios framework AND the first batch of cold-start fixes. Keep building on it; don't open a separate branch. The PR is DRAFT and will stay so until live e2e passes.

What's in already:

- **Scenarios framework**: `internal/scenarios/` + `internal/scenarios/httproutee2e/` + `internal/jumphost/` + `internal/cli/scenarios.go` + tests
- **test traffic alias**: refactored to delegate to `scenarios run http-routing-e2e`, flag surface preserved
- **C-1 + C-3 cold-start fixes**: `intent/cluster.go` host-device defaults (m6i.4xlarge × 3) + `examples/syd-tracer/cluster.yaml` updated to match
- **Phase 00 preflight ENI capacity check** (slice-12 audit bug #1 fix, partial — C-2 extends it)
- **Phase 17b PUBLIC_SUBNETS state-key fix** (slice-12 audit bug #2)
- **Em-dash → ASCII in phase 17b SG description** (slice-12 audit bug #3)
- **.gitignore credentials** (slice-12 audit bug #4)
- **3 audit docs**: slice-12 cold-start audit + latent-bugs sweep + this handoff
- **3 memory entries**: scenarios framework structure, host-device ENI limit (now superseded by handoff §"sizing"), no-defer rule

What the next session adds on top of this:

- **C-2**: extend phase 00 preflight to CPU/memory/desiredSize (not just ENIs).
- **C-4**: phase 25 polling trim 40 → 12 + informative failure dump.
- **C-5**: phase 23 + 23b CRD-wait trim 10m → 3m.
- **C-6**: replace `applyRawYAML` static GVR map with live RESTMapper (the most-deferred item in the repo).
- **C-7**: validate() refuses `desiredSize < 3` on host-device pattern.
- **H-1 through H-8** per sweep.
- **M-1 through M-12** per sweep (verify M-4/M-5 already done; the others outstanding).
- **L-1 through L-15** per sweep.
- **Live e2e validation**: clean `up → scenarios run → down`, zero residue, zero intervention.

## How to start the next session

```bash
cd /Users/j.lucia/Code/github/awsbnkctl
git fetch origin --prune
git status                         # confirm clean (or that slice-13-scenarios-framework is checked out)
git log --oneline -5               # confirm origin/main matches local main

# Read the three docs in order:
cat .agent/handoff/2026-05-25-cold-start-reliability-pickup.md     # this file
cat docs/audits/2026-05-24-latent-bugs-sweep.md                     # the 42 findings
cat docs/audits/slice-12-cold-start-audit.md                        # context for C-1..C-6 and H-3
cat .agent/handoff/2026-05-24-slice-13-and-audit-pickup.md         # context for slice-13 PR

# Branch strategy: continue on the existing slice-13-scenarios-framework branch.
# It already carries the scenarios framework + the first 4 cold-start fixes.
# The next session adds C-2/C-4/C-5/C-6/H-3 + the rest of the 42 sweep items
# + lives the e2e validation, then flips the PR from DRAFT to ready.

git checkout slice-13-scenarios-framework
git pull origin slice-13-scenarios-framework      # if remote moved
# work through chunks 1-5 above (each becomes a commit on this branch)

# When acceptance criterion holds (clean up → 5/5 HTTP 200 → clean down):
git push origin slice-13-scenarios-framework
gh pr ready <pr-number>                            # un-draft

# AWS prereqs (still valid):
export AWS_PROFILE=Users-292785712872
aws sso login --profile Users-292785712872       # only if SSO expired
```

## Open-decision items (genuine escalations, NOT bugs)

These ARE worth fixing but they need user direction on scope or design:

1. **M-10**: Is `dataPath` the right cluster.yaml field name, or should it be `tmm`? Naming debate.
2. **Helm-chart-driven preflight** (sweep §"out-of-scope" / user request 2026-05-24): read FLO chart and sum resource requests live, instead of hardcoded constants. Adds helm binary dep.
3. **H-1/H-2 scope**: deleting `internal/tf/` + downstream callers is ~150 LoC across 4-5 files. Bundle into cold-start PR or its own "kill legacy TF" PR?
4. **H-6 (forge cluster-9 401)**: cross-repo fix (awsbnkctl + bnk-forge). May need a separate forge-side PR first.

Surface each to the user at the time you reach it.

## Reference files (snapshot at handoff)

- Sweep doc: `docs/audits/2026-05-24-latent-bugs-sweep.md`
- Cold-start audit: `docs/audits/slice-12-cold-start-audit.md`
- Slice-13 handoff: `.agent/handoff/2026-05-24-slice-13-and-audit-pickup.md`
- Slice-13 task tree: `.agent/tasks/active/slice-13-scenarios-framework/`
- PRD: `docs/prd/09-SCENARIOS-FRAMEWORK.md` §"Slice-13"
- Memory entries (in `/Users/j.lucia/.claude/projects/-Users-j-lucia-Code-github-awsbnkctl/memory/`):
  - `feedback_no_deferred_fixes.md` (THE rule)
  - `project_slice13_scenarios_framework.md`
  - `project_host_device_eni_limit.md`
  - `project_phase23b_gvr_bug.md`
  - `feedback_systematic_aws_gpu_setup_audit.md`

## Definition of done for the cold-start reliability PR

- [ ] All 42 findings closed (FIXED-IN-PR or ESCALATED-USER-CHOSE).
- [ ] Fresh `awsbnkctl up` → 5/5 HTTP 200 → `awsbnkctl down` → zero AWS residue, with zero human intervention.
- [ ] `go build ./... && go test ./... && go vet ./...` all clean.
- [ ] PR description has a checklist matching the 42 findings.
- [ ] Slice-13 PR un-drafts, live-validates, and merges immediately after.
