# End-to-end test plan

Real-system shake-out for `roksbnkctl` against a live IBM Cloud account. Validates every roksbnkctl verb against the cluster + BNK deployment that `~/bnkfun/terraform.tfvars` describes.

> **Cost warning:** A full pass provisions a 3-zone ROKS cluster, transit gateway, COS instance, and the BNK workload. End-to-end runtime is ~3-4 hours; cloud spend is whatever IBM charges for the resources during that window. Phases B and D are the expensive ones — the rest are seconds-to-minutes.

## Inputs

- `~/bnkfun/terraform.tfvars` — supplies the cluster name (`canada-roks`), region (`ca-tor`), resource group (`default`), COS instance names (`canada-roks-cos-instance` for the registry, `bnk-orchestration` for orchestration), and the `create_roks_*` flags (all true → full provisioning path).
- `IBMCLOUD_API_KEY` env var (or the `ibmcloud_api_key` in tfvars) — IAM credential.
- `terraform`, `kubectl`, `oc`, `ibmcloud`, `iperf3` on `$PATH`.

## Workspace conventions

The test uses workspace `e2e` to avoid touching the user's existing workspaces. State lives at `~/.roksbnkctl/e2e/`. The driver script (`scripts/e2e-test.sh`) deletes that workspace at the start of each pass and at the end of a successful run.

## Pass criteria

Each phase has explicit assertions — the driver script exits non-zero on the first failed assertion. Every long-running command (`up`, `down`, `cluster up`, `cluster down`) runs with `--auto`; no interactive prompts.

## Phases

### Phase A — sanity (no cloud cost; ~1 minute)

| Step | Command | Pass criterion |
|---|---|---|
| A1 | `roksbnkctl version` | exits 0; prints a version line |
| A2 | `roksbnkctl doctor` | exits 0; the credential check passes (other warnings allowed) |
| A3 | `roksbnkctl init -w e2e --auto` (no prompts; defaults pulled from tfvars + env) | exits 0; writes `~/.roksbnkctl/e2e/config.yaml` |
| A4 | `roksbnkctl ws list` | exits 0; `e2e` appears in output |
| A5 | `roksbnkctl tfvars -o /tmp/e2e-tfvars.tf` | exits 0; file exists and references `openshift_cluster_name` |

### Phase B — cluster-only lifecycle (~50 minutes)

| Step | Command | Pass criterion |
|---|---|---|
| B1 | `roksbnkctl cluster up --auto -w e2e --var-file ~/bnkfun/terraform.tfvars` | exits 0; `Apply complete!` in stdout |
| B2 | `roksbnkctl cluster show -w e2e` | exits 0; output includes `cluster_name: canada-roks` and a `registry_cos_crn` line |
| B3 | `roksbnkctl ibmcloud ks cluster get --cluster canada-roks` | exits 0; cluster `State` is `normal` |
| B4 | `roksbnkctl kubectl get nodes` | exits 0; >= 3 nodes; all `Ready` |
| B5 | `roksbnkctl oc whoami` | exits 0; prints a username |
| B6 | (cluster stays up — Phase C uses it) | — |
| B7 | `roksbnkctl -w e2e targets list` | exits 0; output contains `jumphost` (auto-populated by `cluster up` from the upstream HCL's TGW jumphost outputs — PRD 01 §Auto-discovery) |
| B8 | `roksbnkctl -w e2e exec --on jumphost -- whoami` | exits 0; output contains `root` (the upstream HCL provisions the jumphost as root) |
| B9 | `roksbnkctl -w e2e ibmcloud --on jumphost iam oauth-tokens` | exits 0; output contains `IAM token` — validates IBMCLOUD_API_KEY env propagates over SSH |
| B10 | `roksbnkctl -w e2e ibmcloud --backend docker iam oauth-tokens` | exits 0; output contains `IAM token` — validates the **docker backend** (PRD 03 first half, Sprint 3) propagates IBMCLOUD_API_KEY without leaking it via `docker inspect`. Skipped with a yellow `⊘` when the docker daemon isn't reachable on the runner. |

Steps B7-B9 require the upstream HCL's `testing_create_tgw_jumphost` to be true (the default). When users override that to false in their tfvars, the jumphost target won't be auto-populated and B7-B9 are skipped with a yellow `⊘` marker rather than failing the phase. See `scripts/e2e-test.sh phase_B` for the gating logic.

B10 is the **Phase K-prelim** in PLAN.md's terminology — a minimal docker-backend smoke test that lands in Sprint 3 alongside the docker plumbing. The full Phase K (canonical multi-tool docker backend phase covering iperf3 + terraform) is scoped for Sprint 6.

### Phase C — register an existing cluster (~30 seconds)

Validates that `cluster register` correctly discovers and persists the identity of a cluster roksbnkctl didn't itself create.

| Step | Command | Pass criterion |
|---|---|---|
| C1 | `rm ~/.roksbnkctl/e2e/cluster-outputs.json` (simulate "I made the cluster some other way") | exits 0 |
| C2 | `roksbnkctl cluster register canada-roks --registry-cos-name canada-roks-cos-instance -w e2e` | exits 0; "✓ Wrote ~/.roksbnkctl/e2e/cluster-outputs.json" |
| C3 | `roksbnkctl cluster show -w e2e` | exits 0; same `cluster_name`, `cluster_id`, `vpc_id` as Phase B's show output |
| C4 | `roksbnkctl cluster down --auto -w e2e --var-file ~/bnkfun/terraform.tfvars` | exits 0; `Destroy complete!` |
| C5 | `roksbnkctl cluster show -w e2e` | exits non-zero with "no cluster-outputs.json" (down deletes the file) |

### Phase D — full lifecycle (cluster + BNK; ~70 minutes)

The everyday `roksbnkctl up` happy path: TF brings up cluster + BNK in one shot with `create_roks_*=true` in tfvars.

| Step | Command | Pass criterion |
|---|---|---|
| D1 | `roksbnkctl up --auto -w e2e --var-file ~/bnkfun/terraform.tfvars` | exits 0; `Apply complete! Resources: 77 added` (or near — TF can shift counts) |
| D2 | `roksbnkctl status -w e2e` | exits 0; reports cluster reachable |
| D3 | `roksbnkctl k get pods -n f5-bnk` | exits 0; pods listed (state checks deferred to test phase). **Sprint 2 / PRD 02**: replaces the previous `roksbnkctl kubectl get pods` passthrough with the internalised `k get` verb. |
| D3b | `env PATH=<stripped> roksbnkctl k get nodes` | exits 0; output contains `Ready`. Validates the v0.8 "no kubectl required" claim — strips every PATH entry that holds a `kubectl` or `oc` executable, then runs `roksbnkctl k get nodes` against the stripped PATH. If the in-process implementation accidentally shells out to host `kubectl`, this step fails. We use `env PATH=…` rather than `mv kubectl kubectl.hidden` so the host filesystem stays untouched (the dry-run path also remains side-effect-free). |
| D4 | `roksbnkctl logs flo` (capture 50 lines, then break) | exits 0; output lines > 0 |
| D5 | `roksbnkctl test connectivity -o json` | exits 0; `schema: roksbnkctl.v1`; all checks pass |
| D6 | `roksbnkctl test dns -o json` | exits 0; all checks pass |
| D7 | `roksbnkctl test throughput -o json` | exits 0; iperf3 pod runs and tears down |
| D8 | `roksbnkctl down --auto -w e2e --var-file ~/bnkfun/terraform.tfvars` | exits 0; `Destroy complete!` |

### Phase E — workspace ops (no cloud cost; ~10 seconds)

Run *during* Phase D's idle windows (between up and down) to amortize wall time.

| Step | Command | Pass criterion |
|---|---|---|
| E1 | `roksbnkctl ws new e2e-second` | exits 0 |
| E2 | `roksbnkctl ws list` | exits 0; both `e2e` and `e2e-second` appear |
| E3 | `roksbnkctl ws current` | exits 0; prints `e2e` (set by `init -w e2e` earlier) |
| E4 | `roksbnkctl ws use e2e-second` then `ws current` | second call prints `e2e-second` |
| E5 | `roksbnkctl ws use e2e` | exits 0 |
| E6 | `roksbnkctl ws delete e2e-second --force` | exits 0; not in `ws list` afterward |

### Phase F — COS bucket + object CRUD (no cloud cost beyond bytes stored; ~30 seconds)

Validates Resource Controller + S3 plumbing. Uses `bnk-orchestration` (the COS instance the user's tfvars references for general orchestration storage; assumed pre-existing in the account). Creates and deletes its own scratch bucket — never writes into a pre-existing bucket — so the test is fully self-contained.

| Step | Command | Pass criterion |
|---|---|---|
| F1 | `roksbnkctl cos instance list` | exits 0; `bnk-orchestration` appears |
| F2 | `roksbnkctl cos bucket list --instance bnk-orchestration` | exits 0 |
| F3 | `roksbnkctl cos bucket create roksbnkctl-e2e-<unique> --instance bnk-orchestration` | exits 0 (bucket name globally unique) |
| F4 | `roksbnkctl cos object put <bucket>/blob /tmp/blob --instance bnk-orchestration` | exits 0 |
| F5 | `roksbnkctl cos object get <bucket>/blob /tmp/blob.out --instance bnk-orchestration` | exits 0; bytes match |
| F6 | `roksbnkctl cos object delete <bucket>/blob --instance bnk-orchestration` | exits 0 |
| F7 | `roksbnkctl cos bucket delete <bucket> --instance bnk-orchestration` | exits 0 |

### Phase G — passthrough commands (no cloud cost; ~10 seconds; runs during Phase D)

Validates that `roksbnkctl` passes workspace credentials to subprocesses correctly.

| Step | Command | Pass criterion |
|---|---|---|
| G1 | `roksbnkctl ibmcloud account show` | exits 0; account info prints |
| G2 | `roksbnkctl kubectl version --client` | exits 0 |
| G3 | `roksbnkctl oc version --client` | exits 0 |
| G4 | `roksbnkctl exec env \| grep KUBECONFIG` | exits 0; KUBECONFIG points at `~/.roksbnkctl/e2e/state/kubeconfig` |

### Phase H — final cleanup (~10 seconds)

| Step | Command | Pass criterion |
|---|---|---|
| H1 | `roksbnkctl ws delete e2e --force` | exits 0; `e2e` no longer in `ws list` |
| H2 | `ls ~/.roksbnkctl/e2e 2>&1` | "No such file or directory" |

## Sprint 4 — backend matrix driver

Sprint 4 introduces a sibling driver — [`scripts/e2e-test-backends.sh`](../scripts/e2e-test-backends.sh) — that exercises the four-backend matrix from PRDs 03 + 04. It covers:

- **Phase K** (docker backend, full coverage — K1 through K6) per [PRD 05 §K](./prd/05-E2E-TEST-PLAN.md#phase-k--docker-backend-ibmcloud--iperf3)
- **Phase L** (k8s backend, full coverage — L0 through L7) per [PRD 05 §L](./prd/05-E2E-TEST-PLAN.md#phase-l--k8s-backend-iperf3--ops-pod)
- **Phase M** (cred-leak audit — M1 through M7, minus M5+M6 which require the SSH e2e from PRD 05 Phase I — that lands in Sprint 6) per [PRD 05 §M](./prd/05-E2E-TEST-PLAN.md#phase-m--credential-propagation-audit)

The backends driver REUSES the cluster brought up by `scripts/e2e-test.sh`'s Phase D — it does NOT bring its own cluster up. **Run order**:

```bash
# 1. Bring up the cluster + BNK via the baseline driver:
IBMCLOUD_API_KEY=... ./scripts/e2e-test.sh                 # Phases A-H, ~3-4h

# 2. (Between Phase D's apply and final teardown) — exercise the matrix:
IBMCLOUD_API_KEY=... ./scripts/e2e-test-backends.sh        # K + L + M, ~10m
```

A combined runner `scripts/e2e-test-full.sh` that orchestrates both is scheduled for Sprint 6 (per [docs/PLAN.md](./PLAN.md) Sprint 6 deliverables).

The backends driver supports the same `PHASE_FROM=` resume hook and `DRY_RUN=1` plan-rendering mode as the baseline driver. See `CONTRIBUTING.md` "Running scripts/e2e-test-backends.sh locally" for the full local-run recipe.

## Failure recovery

If a phase fails:

1. The driver script captures the full log to `/tmp/roksbnkctl-e2e-<phase>-<timestamp>.log` and exits.
2. Cause is diagnosed from that log.
3. Code is fixed, committed with a descriptive message, pushed.
4. Binary is rebuilt and reinstalled.
5. Driver re-runs from the failing phase (state from earlier passes is preserved where reasonable; `cluster up` has its own idempotence).

This loop repeats until a full pass with zero failed assertions.

## Out of scope (this round)

- `roksbnkctl shell` — interactive subshell; can't be auto-tested without a fake TTY.
- `roksbnkctl install` — would overwrite the running binary; tested manually.
- `roksbnkctl self update` — requires a published GitHub release.
- BNK-on-existing-cluster path (cluster persists across multiple BNK trial workspaces) — that requires upstream HCL changes (gating cert_manager + testing modules behind a `deploy_cluster_services` variable) that aren't yet in this repo.
