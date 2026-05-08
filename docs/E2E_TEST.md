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

### Phase C — register an existing cluster (~30 seconds)

Validates that `cluster register` correctly discovers and persists the identity of a cluster bnkctl didn't itself create.

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
| D3 | `roksbnkctl kubectl get pods -n f5-bnk` | exits 0; pods listed (state checks deferred to test phase) |
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
