# Slice-12 Cold-Start Audit

> **Why this exists**: Slice-12 (jumphost + `awsbnkctl test traffic`) was reviewed/merged but never live-validated from a fresh `up`. The 2026-05-23 HTTP-200 success ran against a *pre-existing* cluster — provisioned manually with bigger instances. Slice-13's acceptance criterion required a fresh `up → scenarios run → down`, which exposed **5 distinct cold-start bugs** in 50 minutes.
>
> **The deeper rot**: this is not a new problem. `docs/audits/slice-09-aws-gpu-setup-audit.md` row 27 (2026-05-22) already documented "BNK_WORKER_INSTANCE_TYPE=m6i.4xlarge # MINIMUM for BNK 2.3 Small" and row 28 documented "BNK_WORKER_COUNT=3 # ≥3 for dSSM quorum, marked **DEFERRED**." `internal/intent/cluster.go:443` even has a comment mentioning aws-gpu-setup's m6i.4xlarge. **We had the right numbers, written down in two places, and we never propagated them to the default or example.** That's the systematic failure this audit answers.

## Honest answer to "why did we use too small an instance EACH time"

Git history: `examples/syd-tracer/cluster.yaml` instanceType has been `t3.medium` since `2b44b0568e` (slice-3+4, 2026-05-21). **It has never been larger.** `internal/intent/cluster.go:374` default has been `t3.medium` since the same commit. **It has never been larger.**

The slice-11 HTTP-200 live test on 2026-05-23 ran against m6i.4xlarge, but only because the operator manually provisioned that cluster outside `awsbnkctl up`. The success was recorded in handoff `2026-05-23-0215-tmm-segfault-investigation.md` (mentions m6i.4xlarge explicitly) but **not pushed back into the default or the example**.

slice-09's audit recorded the right numbers on 2026-05-22 and marked them DEFERRED. Slices 10, 11, 12 shipped without un-deferring them. Slice-13's fresh `up` was the first test of the cold path.

**This is the process bug.** We've now landed two audits (slice-09, slice-13) that both diagnose the same instance-sizing problem. The fix this time is in code, not in another markdown file: the default + the example + a preflight check.

## Honest answer to "why did the template ever change once we discovered we need larger instances"

It didn't. I (assistant, 2026-05-24) was the first to ever change it — bumping syd-tracer to m5.xlarge today, which was *still too small* because I picked the minimum ENI count without also checking control-plane sizing. The user is right that this is the same mistake as slice-09: pick a number that satisfies one constraint, miss the other.

## The 5 cold-start bugs hit today (in order)

| # | Bug | Phase | Root cause | Fix in this PR |
|---|---|---|---|---|
| 1 | `AttachmentLimitExceeded: count 4 exceeds limit for t3.medium` | 17 | Default t3.medium has 3 ENI slots; EKS CNI claims slot 1; only 1 BNK secondary fits, not 2 | Phase 00 preflight checks `DescribeInstanceTypes` for ≥4 ENIs when `pattern=host-device`. Default bumped. |
| 2 | `phase17b: MGMT_SUBNET not in state (run phase03 first)` | 17b | Phase 17b reads `PUBLIC_SUBNETS[0]` — but state.env is flat key=value, no array syntax. Fallback `MGMT_SUBNET` set by phase 19 (which runs *after* 17b). | Parse `PUBLIC_SUBNETS` csv directly in phase 17b with `mgmtSubnetIndex`. |
| 3 | `InvalidParameterValue: Character sets beyond ASCII are not supported` (SG description) | 17b | Em-dash (U+2014) in `CreateSecurityGroupInput.Description`. | Replace `—` with `-`. ASCII-sweep all AWS-facing strings. |
| 4 | `phase12: reading FAR archive ./cne_pull_64.json: no such file` | 12 | Example cluster.yaml references `./cne_pull_64.json` + `./license.jwt`; operator must supply. Not gitignored. | Add `cne_pull_64.json` + `*.jwt` to .gitignore. Document symlink workflow inline in cluster.yaml. |
| 5 | `f5-tmm Pending: Insufficient cpu, Insufficient memory` | 25 | m5.xlarge (3920m allocatable) — BNK control-plane requests ~3550m before TMM (2000m) gets a vote. Single-node host-device requires fitting TMM + all control-plane on the *same* node. | Bump default to **m6i.4xlarge** (slice-09 audit-documented BNK 2.3 minimum). Bump `desiredSize: 1 → 3` per slice-09 audit row 28 (dSSM quorum). Add CPU/memory headroom check to phase 00 preflight. |
| 6 | `DependencyViolation` deleting SG_BNK_DATA on `down` | 07 down | Phase 18 adds cross-reference ingress rules: cluster SG ← from SG_BNK_DATA, and SG_BNK_DATA ← from cluster SG. Phase 07's Down tries to delete SG_BNK_DATA without first revoking the cluster-SG ingress that references it. Cluster SG itself sometimes survives EKS delete when it has external references — orphaned. | Phase 07 Down must revoke cross-references on `down` symmetric to phase 18 setting them up. Steps: (a) `RevokeSecurityGroupIngress` on cluster SG removing SG_BNK_DATA ref; (b) `RevokeSecurityGroupIngress` on SG_BNK_DATA removing cluster SG ref; (c) delete SG_BNK_DATA; (d) attempt cluster SG delete (best-effort — EKS owns it, may already be gone). Add a unit test that simulates phase 18 setup then phase 07 down and asserts both Revoke calls fire. |

Phase 25's "40 iterations × 30 s = 20 min" hid all of the above behind a long, uninformative wait. User flagged this directly. **Trimming to 12 × 30s = 6 min** with clearer failure output below.

## §1 — AWS-facing string ASCII validity

AWS rejects non-ASCII in `Description` on SGs, IAM roles, and various `Tag.Value` fields. We hit this with one em-dash. Swept all phases for AWS-bound `Description` / `Tag` strings.

**Result**: only `phase17b_jumphost.go:410` had the em-dash. All other non-ASCII bytes in phase code are in comments / log lines / doc strings — none reach AWS.

**Status**: ✓ Fixed by 1-char edit.

## §2 — Phase 17b state-key contracts

| Key | Reader (17b) | Writer | Status |
|---|---|---|---|
| VPC_ID | line 95 | phase 02 | ✓ |
| SG_BNK_DATA | line 99 | phase 07 | ✓ |
| BNK_EXT_SUBNET | line 103 | phase 03 | ✓ |
| **MGMT_SUBNET → PUBLIC_SUBNETS** | line 116 broken, **NOW fixed** | phase 03 `PUBLIC_SUBNETS` (csv) | ✓ **FIXED** |
| JUMPHOST_INSTANCE_ID (self-idempotency) | various | self | ✓ |
| JUMPHOST_AMI_ID (cache) | line ~138 | self | ✓ |

**Status**: ✓ Fixed. Test mock now seeds `PUBLIC_SUBNETS` instead of the non-canonical `MGMT_SUBNET`.

## §3 — Phase ordering vs 17b reads

Phase order from `lifecycle.go`: `00 02 03 04 05 06 07 08 09 10 11 11b 16 17 17b 18 19 …`. Phase 17b runs **before** phase 19 (which sets the `MGMT_SUBNET` alias). The original code expected phase 19 to have run already. **No change to phase order; phase 17b now reads PUBLIC_SUBNETS directly.**

## §4 — Polling timeouts (user-flagged)

| Phase | Loop | OLD | NEW | Verdict |
|---|---|---|---|---|
| 08 | EKS cluster ACTIVE | 30 min | 30 min | AWS-bound, keep |
| 10 | Node group ACTIVE | 20 min | 20 min | AWS-bound, keep |
| 11b | Hugepages DS Ready | 5 min | 5 min | keep |
| 11b | Node hugepages-2Mi ≥4Gi | 5 min | 5 min | keep |
| 23 | CRD `licenses.k8s.f5net.com` Ready | 10 min | 3 min | **trimmed** |
| 23b | CRD `f5-spk-vlans.k8s.f5net.com` Ready | 10 min | 3 min | **trimmed** |
| 24 | CWC heal (12 × 15s) | 3 min | 3 min | keep |
| **25** | **CNEInstance + License Ready** | **40 × 30s = 20 min** | **12 × 30s = 6 min** | **trimmed (user complaint)** |

**Why trim phase 25**: if CNEInstance hasn't reached Ready in 6 min after the License CR is applied, it's not going to. The 20-min wait masks resource-shortage / image-pull / wrong-CRD problems behind dead time. The new error message dumps the last `kubectl get pods -A` output so the operator sees *why* it failed, not just *that* it failed.

## §5 — Instance type sizing for host-device pattern

Per slice-09 audit row 27: **m6i.4xlarge is the documented BNK 2.3 Small minimum.** With `desiredSize: 1` even m6i.4xlarge hits "Insufficient cpu" on supporting pods (audit row 28, marked DEFERRED — UN-DEFERRED here).

Live measurement on syd-tracer m5.xlarge (this audit's debug session, 2026-05-24):

| Workload | CPU request | Memory request |
|---|---|---|
| TMM | 2000m | 8 Gi |
| f5-spk-csrc | 750m | 1280 Mi |
| f5-downloader | 1250m | 1656 Mi |
| f5-dssm-sentinel | 850m | 1408 Mi |
| f5-analyzer | 450m | 512 Mi |
| f5-lifecycle-operator | 250m | 256 Mi |
| f5-observer | ~250m | ~512 Mi |
| f5-observer-receiver | ~250m | ~512 Mi |
| f5-spk-cwc | ~250m | ~512 Mi |
| f5-dssm-db | ~500m | ~1 Gi |
| System (aws-node, kube-proxy, ebs-csi-node, multus, hugepages-setup) | ~290m | ~218 Mi |
| **Total (single-node tally)** | **~7090m** | **~15.2 Gi** |

vs allocatable per instance (after EKS reserve):

| Instance | vCPU | Allocatable CPU | Allocatable Mem | Verdict (single-node) | With 3 nodes |
|---|---|---|---|---|---|
| t3.medium | 2 | ~1900m | ~3 Gi | unusable | unusable (also 3-ENI cap on TMM node) |
| m5.large | 2 | ~1900m | ~7 Gi | unusable | unusable (3-ENI cap) |
| m5.xlarge | 4 | ~3920m | ~14.5 Gi | unusable (today's bug) | unusable (3 nodes still TMM-pinned) |
| m5.2xlarge | 8 | ~7800m | ~30 Gi | very tight (~710m head) | comfortable |
| **m6i.4xlarge** | **16** | **~15700m** | **~62 Gi** | **comfortable** | **slice-09 documented minimum** |

**Fix**:
1. **Default instance type for `pattern: host-device`** → m6i.4xlarge (was t3.medium for all patterns).
2. **Default desiredSize for `pattern: host-device`** → 3 (was 1 for all patterns).
3. **Example `examples/syd-tracer/cluster.yaml`** → m6i.4xlarge × 3.
4. **Phase 00 preflight**: when `pattern=host-device`, query `DescribeInstanceTypes` and require:
   - `MaximumNetworkInterfaces ≥ 4` (slice-13 already does this)
   - **NEW**: `VCpuInfo.DefaultVCpus ≥ 16` (sized for full BNK control-plane + TMM)
   - **NEW**: `MemoryInfo.SizeInMiB ≥ 65536` (≥64 GB, BNK 2.3 Small minimum)
   - **NEW**: `nodeGroups[0].DesiredSize ≥ 3` (dSSM quorum)
   Otherwise: fail-fast in <5s with the specific BNK 2.3 Small minimum stated.
5. **Non-host-device patterns** keep t3.medium / desiredSize 1 default — they don't run BNK on the workers.

This is the un-deferral of slice-09's audit row 28. It's been sitting open for 2 days while we hit the same bug twice.

## §6 — Phase 17b idempotency

Every Create call in phase 17b has a Describe-first guard. Verified live during today's retries — each second `up` after a phase-17b failure correctly logged "X already exists, skipping create." No code change.

## §7 — Credential file workflow

`examples/syd-tracer/cluster.yaml` carried placeholder paths `./cne_pull_64.json` + `./license.jwt`. Operator must supply real files. Two improvements:

1. Add `cne_pull_64.json` + `*.jwt` to `.gitignore` (done).
2. Document the symlink workflow inline in cluster.yaml — one explicit `ln -sf /path/to/your/private/credentials/cne_pull_64.json ./cne_pull_64.json` instead of "REPLACE with your path."

## §8 — Helm chart preflight (user request, deferred)

User asked: "or read the manifest helm chart as part of pre-flight?" Aspirational and correct.

Today: BNK control-plane Helm chart is FLO (`v2.21.13-0.0.28` per `intent/cluster.go`), pulled from OCI registry at phase 14. To read its resource requests at preflight time, we'd need to:
- `helm pull <oci-ref>` (requires helm + OCI auth)
- `helm template` (deterministic but spawns a subprocess)
- Parse rendered YAML for `resources.requests.{cpu,memory}` per Pod template
- Sum + compare to node allocatable

That's a 1-3 minute preflight extension and a new dependency surface. Deferred to backlog `preflight-cluster-yaml-validation`.

**Today's fix** is the hardcoded BNK 2.3 Small minimum (m6i.4xlarge × 3) from the slice-09 audit. When BNK 2.4 ships, this constant needs to bump alongside the FLO version pin.

## §9 — No deferred items (applying the no-defer rule)

Per user feedback 2026-05-24 ("deferring bugs and fixes is ALWAYS wrong, we almost always NEVER come back to it"), this audit does NOT have an "Open follow-ups" section. Every issue surfaced gets fixed in this PR OR explicitly escalated to the user with a scope estimate.

Items considered + decisions:

| Item | Decision | Reasoning |
|---|---|---|
| `applyRawYAML` static GVR map (MEMORY: `project_phase23b_gvr_bug`) | **Escalated to user** | Real refactor: 4+ callers in phase 11b/12/13b/etc; touches ~150 lines. Slice-13 already sidesteps it via `internal/k8s.ApplyOptions`. User to decide: bundle into this PR or its own follow-up PR (NOT a backlog item). |
| Helm-chart-driven preflight (read FLO chart for resource requests) | **Hardcoded constants now; helm-driven escalated** | The BNK 2.3 Small numbers (m6i.4xlarge / 16 vCPU / 64 GB / 3 nodes) are pinned in the preflight as constants today. They live one line from the FLO version pin in `intent/cluster.go`, so when FLO version bumps, the constants must bump together. Helm-driven preflight is a larger feature; escalate. |
| Other preflight checks (AZ availability, K8s version, service quotas) | **Escalated to user** | Each is ~30 LoC; user to decide whether to bundle. |

Backlog updated: previous BACKLOG.md entry `preflight-cluster-yaml-validation` REPLACED by an open escalation in the slice-13 PR description. No silent rotting.

## Verification gates

After this audit's fixes land:

1. `go build ./...` clean. `go test ./...` clean.
2. Fresh `awsbnkctl up --config examples/syd-tracer/cluster.yaml --auto` reaches phase 25 in ~25 min **with zero firefighting**.
3. Phase 25 either succeeds in ≤6 min or fails with a clear error pointing at the stuck Pod's status.
4. `awsbnkctl scenarios run http-routing-e2e` returns 5/5 HTTP 200.
5. `awsbnkctl down --yes` cleans up to zero AWS-side residue.

If gate 2 fails, this audit was insufficient — capture the new failure as row 6+ above and recurse.
