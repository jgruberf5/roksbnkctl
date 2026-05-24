# Handoff — slice-13 scenarios framework + cold-start audit (pickup)

**Written**: 2026-05-24 (AEST)
**Branch**: `slice-13-scenarios-framework` (off origin/main `86b8987`)
**State**: code mostly built, live e2e blocked on instance-sizing default; sweep agent inventorying latent bugs in parallel.
**Cluster state**: `awsbnkctl down --config examples/syd-tracer/cluster.yaml --yes` is running in background (task `bft8ijl56`) — tearing down the half-built m5.xlarge cluster from the firefighting session.

## The /goal that opened this session

Land slice-13: port mwiget/kindbnkctl's scenarios framework into awsbnkctl, replace `awsbnkctl test traffic` with a generic scenario runner, and prove HTTP 200 e2e against a freshly-provisioned syd-tracer with the slice-12 jumphost.

PRD anchor: `docs/prd/09-SCENARIOS-FRAMEWORK.md` §"Slice-13".
Account: 292785712872, ap-southeast-2, profile Users-292785712872.
Live test still pending — see §"What's left" below.

## What's been built this session

Architect → Builder → Reviewer cycle on slice-13 ran cleanly. APPROVE verdict, all unit gates green. Builder output: ~3.6k LoC across these files (all on disk, not yet committed):

### Scenarios framework + first scenario

- `internal/scenarios/scenario.go` — Scenario interface + Rating + Result + Assertion + Context (explicit `Clientset`, `Dynamic`, `RESTConfig`, `KubeconfigPath` — no Runner shellout) + Registry. Schema `awsbnkctl.scenario.v1`.
- `internal/scenarios/runner.go` — Run/Cleanup/EnsureScenarioDir/WriteManifest/WriteRunSummary + inline safeSlug + NewContext. RunSummary schema `awsbnkctl.scenario.run.v1`.
- `internal/scenarios/envdiagram.go` — `Render(EnvDiagramInput) string` ASCII renderer; live reads fall back to `(unknown)`. **Exports `NestedSlice` (was deduped from per-package copies — fix F-3 from reviewer).**
- `internal/scenarios/httproutee2e/{scenario.go, README.md, manifests/01-05.yaml}` — first scenario. 5 templated manifests (Namespace, F5BnkGateway IP pool, nginx, Gateway, HTTPRoute). Verify order: control-plane assertions → `ResyncHTTPRoutes` → SSH+EICE curl probes. Dependencies `[]string{}` (no FRR sibling).
- `internal/scenarios/scenario_test.go` — registry tests + duplicate-Register panic test (fix F-2).
- `internal/scenarios/envdiagram_test.go`, `internal/scenarios/httproutee2e/scenario_test.go`, etc.
- `internal/jumphost/jumphost.go` — extracted from `test_traffic.go`. Pure leaf (no imports of `internal/cli` or `internal/scenarios`, verified by `go list -deps`). Public API: `RunCurlProbes`, `GenerateEphemeralED25519`, `PushSSHPublicKey`, `SSHCurlViaEICE`, `ProbeOptions`, `ProbeResult`.
- `internal/cli/scenarios.go` — `awsbnkctl scenarios {list,run,clean}` cobra subtree. `--all` returns clear "not implemented (only 1 scenario)" stub. **list now writes everything to stdout (fix F-4).**
- `internal/cli/test_traffic.go` — refactored to alias `scenarios run http-routing-e2e`. Flags preserved exactly.
- `internal/cli/test_traffic_test.go` — golden dry-run regression test + Context-population mock (fix architect §3.1).
- `internal/intent/cluster.go` — added `DefaultVIP() (string, error)` method. Tests in `cluster_test.go`.

### Cold-start bug fixes (live-validation surfaced 5 bugs; all fixed in this PR per no-defer rule)

- `internal/aws/phases/clients.go` — added `DescribeInstanceTypes` to EC2API.
- `internal/aws/phases/mock_ec2_test.go` — mock impl + test fixture.
- `internal/aws/phases/phase00_preflight.go` — new `checkHostDeviceENICapacity` requires ≥4 ENIs when `pattern: host-device`. Live-tested catching t3.medium correctly.
- `internal/aws/phases/phase00_preflight_test.go` — 5 unit tests for the new check.
- `internal/aws/phases/phase17b_jumphost.go` — TWO live bugs fixed:
  - **State key bug**: was `st.Get("PUBLIC_SUBNETS[0]")` (state.env has no array syntax). Now parses `PUBLIC_SUBNETS` csv directly.
  - **ASCII bug**: em-dash (U+2014) in `CreateSecurityGroupInput.Description` rejected by AWS. Replaced with `-`.
- `internal/aws/phases/phase17b_jumphost_test.go` — test mock updated to seed `PUBLIC_SUBNETS` (canonical), not `MGMT_SUBNET` (which is phase 19's downstream alias).
- `examples/syd-tracer/cluster.yaml` — `testing.jumphost.enabled: true` (slice-13 lead-side flip for live test), `instanceType: m5.xlarge` (still incorrect — see §"What's left"). **Both edits need final reconciliation per audit §5 before commit.**
- `.gitignore` — `cne_pull_64.json` + `*.jwt` added so the symlinked F5 credentials don't get tracked.

### Audit + memory + backlog

- `docs/audits/slice-12-cold-start-audit.md` — full systematic audit. Documents the 5 bugs, the honest answer to "why was the template always too small" (it never wasn't; slice-09 audit row 28 marked m6i.4xlarge minimum as DEFERRED 2 days ago), and the no-defer rule going forward. §9 has NO open items — every audit finding is either fixed or explicitly escalated for user decision.
- `docs/audits/2026-05-24-latent-bugs-sweep.md` — IN PROGRESS by a background sweep agent (Opus). Inventories every TODO/FIXME/DEFERRED/skip across the repo. Path may not exist yet at handoff time; check `ls docs/audits/` before reading.
- `.agent/backlog/BACKLOG.md` — added two earlier items: `slice-13-followup-verify-order-test`, `slice-14-port-second-scenario`. The `preflight-cluster-yaml-validation` item is to be REPLACED (per no-defer rule) — see audit §9.
- Memory entries (in `/Users/j.lucia/.claude/projects/-Users-j-lucia-Code-github-awsbnkctl/memory/`):
  - `project_slice13_scenarios_framework.md` — framework structure for future scenario ports.
  - `project_host_device_eni_limit.md` — the 4-ENI requirement + m5.xlarge minimum (now superseded for sizing by audit §5 → m6i.4xlarge).
  - `feedback_no_deferred_fixes.md` — NEW. The principle the user established this session.

## What's still left (in priority order)

### CRITICAL — must do before re-running live e2e

The 5th cold-start bug (m5.xlarge too small for BNK control-plane + TMM on a single node) is **identified but not yet fixed in code**. The audit §5 prescribes:

1. **`internal/intent/cluster.go`** — change the default to be pattern-aware:
   - `pattern: host-device` + empty `instanceType` → `m6i.4xlarge` (slice-09 audit-documented BNK 2.3 Small minimum).
   - `pattern: host-device` + empty `desiredSize` → `3` (dSSM quorum per slice-09 audit row 28, was DEFERRED).
   - Non-host-device patterns keep `t3.medium / 1` as before.
   - Add a top-of-file constant block `HostDeviceMinInstanceType = "m6i.4xlarge"` etc so phase 00 preflight reads the same value the default writes.

2. **`internal/aws/phases/phase00_preflight.go`** — extend `checkHostDeviceENICapacity` (rename to `checkHostDeviceCapacity` since it's no longer just ENI):
   - Already: `MaximumNetworkInterfaces ≥ 4` ✓
   - Add: `VCpuInfo.DefaultVCpus ≥ 16` (sized for full BNK control-plane + TMM single-node packing).
   - Add: `MemoryInfo.SizeInMiB ≥ 65536` (≥64 GB, BNK 2.3 Small minimum).
   - Add: `nodeGroups[0].DesiredSize ≥ 3` (dSSM quorum).
   - Tests in `phase00_preflight_test.go` — 4 new test cases (one per check).

3. **`examples/syd-tracer/cluster.yaml`** — bump to `instanceType: m6i.4xlarge` + `desiredSize: 3`. Replace the cost-saving comment block at line 100 with a comment citing `docs/audits/slice-12-cold-start-audit.md` §5.

4. **Phase 25 retry trim** — `internal/aws/phases/phase25_activation_poll.go`:
   - `phase25MaxIter = 40 → 12` (40 iters × 30s = 20 min, user-flagged as too long; 12 × 30s = 6 min)
   - On final timeout, capture `kubectl get pods -A` + `kubectl describe pod <stuck>` and stream to stderr so the operator sees *why* it failed, not just *that* it failed.

5. **Phase 23 + 23b CRD-wait trim** — both currently `10m`; trim to `3m`. CRD apply is sub-second; 10 min is leftover terraform-era pessimism.

6. **Phase 07 down SG cross-reference unwinding (Bug #6)** — surfaced during the audit-session's `down`. Phase 18 sets up cross-reference ingress rules (cluster SG ← SG_BNK_DATA + SG_BNK_DATA ← cluster SG). Phase 07's Down tries to delete SG_BNK_DATA without revoking those first → `DependencyViolation`. Two SGs orphaned on syd-tracer right now (`sg-036b319ed24c0c8dd` SG_BNK_DATA + `sg-03c1cd83aaca378ce` eks-cluster-sg-syd-tracer-...) plus VPC `vpc-0b6665bc65c84d273`. **Manual cleanup deferred** — SGs are free; cleanup blocked by the user's "wait" directive. Fix in phase 07 Down code: revoke both ingress rules before SG delete; tests in `phase07_iam_test.go` (or wherever phase 07 lives).

After these 5 land:
```
go build ./... && go test ./...
git diff
# then commit
```

### CRITICAL (live test)

6. `awsbnkctl down --config examples/syd-tracer/cluster.yaml --yes` (already running; should finish shortly).
7. Verify clean teardown via tag-discovery audit:
   ```
   aws ec2 describe-instances --filters Name=tag:awsbnkctl:cluster,Values=syd-tracer --profile Users-292785712872 --region ap-southeast-2
   aws ec2 describe-vpcs --filters Name=tag:awsbnkctl:cluster,Values=syd-tracer --profile Users-292785712872 --region ap-southeast-2
   aws eks describe-cluster --name syd-tracer --profile Users-292785712872 --region ap-southeast-2
   ```
8. Fresh `awsbnkctl up --config examples/syd-tracer/cluster.yaml --auto` — should reach phase 25 success in ~25 min with zero firefighting.
9. `awsbnkctl scenarios run http-routing-e2e --config examples/syd-tracer/cluster.yaml` — expect 5/5 HTTP 200.
10. `awsbnkctl down --config examples/syd-tracer/cluster.yaml --yes` to free the cluster.

### ESCALATIONS for user decision (per no-defer rule, NOT in backlog)

These are real items I did NOT silently punt — user needs to pick scope for the slice-13 PR vs follow-up PRs:

- **`phase12_k8s_foundation.applyRawYAML` static GVR map** (MEMORY: `project_phase23b_gvr_bug`) — known silent-skip bug, affects 4+ callers, ~150 LoC refactor to use live RESTMapper. Slice-13 already sidesteps it via `internal/k8s.ApplyOptions`. Should it bundle into slice-13 PR or its own PR?
- **Helm-chart-driven preflight** — user asked. Today's fix uses hardcoded constants (m6i.4xlarge, 16 vCPU, 64 GB) pulled from slice-09 audit. Long-term, preflight should `helm pull` + `helm template` the FLO chart and sum actual resource requests. Adds a helm binary dependency.
- **More preflight checks** — AZ availability, K8s version supported, service quotas (VPC/NAT/EIP/vCPU), BNK credential file existence, jumphost mgmtSubnetIndex validity. Each is ~30 LoC; user picks bundle vs separate PR.
- **`pattern: host-device` default `desiredSize: 3`** — affects non-syd-tracer examples too if any exist. Audit + bump? Only one example today (syd-tracer).

### Audit follow-through (completed)

The comprehensive sweep finished. Output: **`docs/audits/2026-05-24-latent-bugs-sweep.md`** (32 KB). 42 unique findings:

- **7 Critical** — must fix before next cold-start `up`. C-1 through C-5 are the same items §"CRITICAL — must do" above prescribes; C-6 is the static GVR map (also escalated below); C-7 is a nuance on `applyDefaults` only bumping `desiredSize` when zero-default (explicit `1` is preserved — implications for the `m6i.4xlarge × 3` default work).
- **8 High** — H-3 is bug #6 above (phase 18 down ↔ phase 07 down SG cross-reference unwinding), independently confirmed. H-1/H-2 flag legacy TF code that survived D-001's architectural pivot. H-4 (`scenarios run --all` stub) + H-5 (`httproutee2e` Verify-order pin-test) are slice-13 LOW findings that the sweep promoted to High when it noticed the multiplier effect over future slices.
- **12 Medium** — technical debt with known cost. M-4 (`nestedSlice` dup) and M-5 (`scenarios list` stdout/stderr) were fixed in slice-13 this session (the sweep may not have noticed the edits); confirm by re-reading the report's "Already fixed" section.
- **15 Low** — cleanup, doc gaps, polish.

The sweep agent's process note: *"Every Critical finding aligns with at least one already-documented audit/handoff/memory entry; none are new discoveries."* In other words — **the no-defer rule is the entire fix**. The bugs already exist on paper across slice-09 audit, slice-12 audit (this session), and 3 memory entries; the work is to act on them, not re-find them.

Resume order: read `2026-05-24-latent-bugs-sweep.md` first. Treat C-1 through C-7 as the must-fix list for the slice-13 PR + cold-start follow-up PR. H-1 through H-8 are explicit user-escalations for scope (do they bundle into slice-13 PR or split into a follow-up PR?).

## Process rules established this session (DURABLE, don't drop)

1. **No deferring bugs.** Per memory `feedback_no_deferred_fixes.md`. If a bug surfaces during work, fix in same PR or escalate to user with scope — don't punt to BACKLOG with "follow-up." Slice-09 audit's DEFERRED items bit slice-13 cold-start exactly because of this pattern.
2. **No TODO comments in code.** File an issue or fix the line. Same memory entry.
3. **Audit docs must have Verification gates, not DEFERRED columns.** See `docs/audits/slice-12-cold-start-audit.md` for the shape.
4. **Live-validate cold path, not just `kubectl apply`-on-existing-cluster.** The bugs we found in slice-13 had all been "live-tested" earlier but only against a manually-provisioned cluster. Every slice touching `up` phases needs a `down → up` cycle.
5. **Preflight at phase 00.** Don't burn 25 min of EKS provisioning before failing on a known-bad config. Phase 00 ENI/CPU/memory check pattern is the seed; more checks per user request.

## How to resume

```bash
cd /Users/j.lucia/Code/github/awsbnkctl
git fetch origin
git status                              # confirm slice-13-scenarios-framework branch with uncommitted work
git log --oneline -3                    # should be 86b8987 + descendants on the branch

# Read first:
cat docs/audits/slice-12-cold-start-audit.md        # full audit, this session's work
ls docs/audits/                                       # check if 2026-05-24-latent-bugs-sweep.md is there
cat .agent/tasks/active/slice-13-scenarios-framework/TASK.md
cat .agent/tasks/active/slice-13-scenarios-framework/reviews/architect-r0.md
cat .agent/tasks/active/slice-13-scenarios-framework/reviews/reviewer-r1.md
cat .agent/tasks/active/slice-13-scenarios-framework/work/v1.md

# Resume work:
# 1. Apply the §"CRITICAL — must do before re-running live e2e" 5 changes above
# 2. go build ./... && go test ./...
# 3. Verify down completed cleanly (tag-discovery audit)
# 4. Re-run live e2e (steps 8-10 above)
# 5. If 5/5 HTTP 200: commit slice-13 work + audit + bug fixes as a single PR
# 6. Address user escalations (§"ESCALATIONS for user decision")

# AWS prereqs:
export AWS_PROFILE=Users-292785712872
aws sso login --profile Users-292785712872       # only if SSO expired
```

## Reference files

- TASK + STATUS: `.agent/tasks/active/slice-13-scenarios-framework/`
- Reviews (architect-r0 + reviewer-r1): `.agent/tasks/active/slice-13-scenarios-framework/reviews/`
- Builder output: `.agent/tasks/active/slice-13-scenarios-framework/work/v1.md`
- Audit: `docs/audits/slice-12-cold-start-audit.md`
- Sweep: `docs/audits/2026-05-24-latent-bugs-sweep.md` (in progress)
- PRD: `docs/prd/09-SCENARIOS-FRAMEWORK.md` §"Slice-13"
- Upstream reference (kindbnkctl): `/tmp/kindbnkctl-scenarios/` (gitignored)
- Slice-13 logs of attempted ups/downs: `.awsbnkctl/syd-tracer/logs/`
- BNK credentials (gitignored symlinks): `cne_pull_64.json -> /Users/j.lucia/Code/aws-gpu-setup/cne_pull_64.json`, `license.jwt -> /Users/j.lucia/Code/aws-gpu-setup/jl-gpu-lab.jwt`
