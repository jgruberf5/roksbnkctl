# PRD 05 — E2E test plan for new backends and remote execution

> Prerequisites: Phases 1-4 complete (or in-progress with feature flags so each phase can be tested independently).
>
> Estimated effort: medium (~600 LOC of new bash + 300 LOC of Go test fixtures); 1 week.

## Goal

Validate every backend × tool combination on a live IBM Cloud account, plus verify credential propagation works correctly across them. Extends the existing E2E (Phases A-H) with new Phases I-N that exercise the SSH client, kubectl internalization, and the four execution backends.

## Approach

The existing `scripts/e2e-test.sh` (Phases A-H, see `docs/E2E_TEST.md`) becomes the **baseline** — it tests today's local-exec path. New phases I-N extend coverage:

| Phase | What it tests | PRD it validates |
|---|---|---|
| **I** | SSH backend / `--on jumphost` | [01](./01-SSH-AND-ON-FLAG.md) |
| **J** | Kubectl internalization (no host kubectl on PATH) | [02](./02-KUBECTL-INTERNAL.md) |
| **K** | Docker backend (ibmcloud + iperf3) | [03 § Docker](./03-EXECUTION-BACKENDS.md) |
| **L** | K8s backend (iperf3 + ops pod) | [03 § K8s](./03-EXECUTION-BACKENDS.md) |
| **L-DNS** | DNS probe (miekg/dns); per-server / per-type / latency; GSLB cross-vantage comparison | [03 § DNS probe](./03-EXECUTION-BACKENDS.md) |
| **M** | Cred propagation audit (no leak in docker inspect, ps, kube events, ssh wrappers) | [04](./04-CREDENTIALS.md) |
| **N** | Mixed-mode lifecycle: kubectl native + terraform local + ibmcloud SSH + iperf3 k8s | all of the above |

Phases I-N share a cluster brought up by Phase D (full lifecycle in baseline). The new phases run between D's apply and D's destroy, before the final teardown.

## Phase I — SSH backend / `--on jumphost`

**Prereqs**: Phase D apply complete, `targets.<SSH_TARGET>` populated (auto-discovered jumphost from terraform outputs, or registered manually via `roksbnkctl targets add`).

The driver implementation lives in [`scripts/e2e-test-backends.sh::phase_I`](https://github.com/jgruberf5/roksbnkctl/blob/main/scripts/e2e-test-backends.sh); the table below reflects what shipped at v1.0 (12 steps, I0-I11). Steps marked **opt-in** require an environment variable to enable (the e2e runner skips them cleanly when unset, since they need specific remote-host shapes that not every CI environment has).

| Step | Command / check | Pass criterion |
|---|---|---|
| I0 | `roksbnkctl targets show $TARGET` | exits 0; target present with host + key_source |
| I1 | `roksbnkctl ibmcloud --on $TARGET iam oauth-tokens` (Sprint 1 `--on` flag path) | exits 0; output contains `IAM token` (validates `IBMCLOUD_API_KEY` propagation over SSH via the lightweight `--on` flag) |
| I2 | `roksbnkctl ibmcloud --backend ssh:$TARGET iam oauth-tokens` (Sprint 4 unified Backend interface path) | exits 0; output contains `IAM token` (same end-to-end behaviour as I1 routed through the `Backend.Run` interface) |
| I3 | `roksbnkctl exec --backend ssh:$TARGET --bootstrap -- iperf3 -v` (apt-bootstrap on Ubuntu target) | exits 0; output contains `iperf` (the bootstrap path installs iperf3 via apt-get if absent, then runs the version check; idempotent on subsequent calls) |
| I4 | Cred-audit: `exec --backend ssh:$TARGET -- bash -lc 'env \| grep -i IBMCLOUD'` | output does NOT contain the API key VALUE (the wrapper script sources an env-file inside the command's process tree; SSH login env doesn't carry the key) |
| I5 | Wrapper-script cleanup: `exec --backend ssh:$TARGET -- ls -d /tmp/roksbnkctl.* 2>/dev/null` | output empty (trap-on-EXIT in wrappers removed all per-invocation tempdirs) |
| I6 | **Informational**: SetEnv silent-drop fallback note | logged only; the wrapper-script path activates automatically if sshd's `AcceptEnv` rejects `IBMCLOUD_API_KEY` (production sshd default) |
| I7 | **Opt-in** (`ROKSBNKCTL_E2E_SSH_NON_UBUNTU=<target>`): apt-bootstrap against a non-Ubuntu target | exits non-zero; error message mentions Ubuntu (the bootstrap is Ubuntu-only at v1.0; RHEL/CentOS/Alpine must pre-install) |
| I8 | **Opt-in** (`ROKSBNKCTL_E2E_SSH_NO_NOPASSWD=<target>`): apt-bootstrap against a target without passwordless sudo | exits non-zero; error message mentions `sudo` |
| I9 | **Manual**: repo-unreachable failure (integrator mutates remote's `/etc/apt/sources.list` or severs DNS) | skipped in automated runs — would affect remote's stable state |
| I10 | Context-cancel: background SSH-backed `sleep 30; echo done`, send SIGINT after 1s | roksbnkctl process exits within 5s of the signal (clean cancellation of the in-flight SSH session) |
| I11 | `roksbnkctl doctor --backend ssh:$TARGET` | exits 0; output mentions the `ssh` backend (no API-key value in logs after redactor) |

The `--use-existing-cluster` flag from the early PRD draft is not implemented in the shipped driver; the driver expects an active cluster (Phase D apply complete) or skips Phase I cleanly when the SSH target isn't reachable. Set `ROKSBNKCTL_E2E_SSH_TARGET=<name>` to enable Phase I; unset, the phase is skipped with a yellow `⊘` marker.

## Phase J — kubectl internalization (PATH-stripped)

**Prereqs**: Phase D apply complete (cluster + BNK up); host kubectl + oc temporarily moved out of PATH.

| Step | Command | Pass criterion |
|---|---|---|
| J0 | `KUBECTL_PATH=$(which kubectl); OC_PATH=$(which oc); sudo mv $KUBECTL_PATH $KUBECTL_PATH.hidden; sudo mv $OC_PATH $OC_PATH.hidden` | both binaries no longer on PATH |
| J0b | `roksbnkctl doctor` | shows green for terraform; shows informational (not warning) for kubectl/oc absence |
| J1 | `roksbnkctl k get nodes` | exits 0; lists 3 nodes Ready (no kubectl install needed) |
| J2 | `roksbnkctl get nodes` (top-level alias) | same as J1 |
| J3 | `roksbnkctl logs flo` | exits 0; produces output |
| J4 | `roksbnkctl k describe node <name>` | exits 0; output shape matches kubectl describe (visual / shape comparison; not byte-strict) |
| J5 | `echo 'apiVersion: v1\nkind: ConfigMap\nmetadata: {name: e2e-cm, namespace: default}' \| roksbnkctl k apply -f -` | exits 0; ConfigMap created |
| J6 | `roksbnkctl k get cm e2e-cm -o yaml` | exits 0; contains `name: e2e-cm` |
| J7 | `roksbnkctl k exec <flo-pod> -n bigip-next-system -- echo hello` | exits 0; prints `hello` |
| J8 | `roksbnkctl k port-forward <flo-pod> 18080:8080 &` then `curl localhost:18080/healthz`; kill the port-forward | curl returns 200 (or expected response); port-forward dies cleanly on signal |
| J9 | `roksbnkctl k delete cm e2e-cm` | exits 0 |
| J10 | `roksbnkctl k get projects` (OpenShift extension) | exits 0; lists projects |
| J11 | (cleanup) restore PATH | `sudo mv $KUBECTL_PATH.hidden $KUBECTL_PATH; sudo mv $OC_PATH.hidden $OC_PATH` |

**Byte-equivalence check** (Phase J supplementary — run separately, not gated on PATH stripping):

```bash
diff <(kubectl get nodes -o yaml) <(roksbnkctl k get nodes -o yaml) | grep -v "managedFields\|resourceVersion\|creationTimestamp" | wc -l
# expect: 0
```

## Phase K — Docker backend (ibmcloud + iperf3)

**Prereqs**: Phase D cluster up; Docker daemon running locally; tool images published (`ghcr.io/jgruberf5/roksbnkctl-tools-*`).

| Step | Command | Pass criterion |
|---|---|---|
| K1 | `docker info \| head -1` | exits 0 (Docker available) |
| K2 | `roksbnkctl ibmcloud --backend docker iam oauth-tokens` | exits 0; pulls image first call (informational log: "pulling roksbnkctl-tools-ibmcloud:..."); subsequent calls skip pull |
| K3 | `roksbnkctl ibmcloud --backend docker ks cluster ls` | exits 0; lists clusters; output matches `roksbnkctl ibmcloud ks cluster ls` modulo CLI version banner |
| K4 | (cred isolation) `docker inspect $(docker ps -lqa) \| jq '.[].Config.Env' \| grep -F 'IBMCLOUD_API_KEY=oJwJ5M'` | should NOT find the key value (value passed via `-e KEY` from caller env, not exposed) |
| K5 | `roksbnkctl test throughput --backend docker --mode north-south` | exits 0; iperf3 client image runs locally against the in-cluster server |
| K6 | (no daemon negative) `sudo systemctl stop docker; roksbnkctl ibmcloud --backend docker iam oauth-tokens; sudo systemctl start docker` | exits non-zero; clear "Docker daemon unreachable" error |

## Phase L — K8s backend (iperf3 + ops pod)

**Prereqs**: Phase D cluster up; `roksbnkctl ops install` run as part of K8s-backend setup.

| Step | Command | Pass criterion |
|---|---|---|
| L0 | `roksbnkctl ops install` | exits 0; creates `roksbnkctl-ops` namespace + ops pod; doctor passes for `--backend k8s` |
| L1 | `roksbnkctl ibmcloud --backend k8s iam oauth-tokens` | exits 0; runs inside ops pod; output matches local-backend |
| L2 | `roksbnkctl test throughput --backend k8s` | exits 0; iperf3 server + client both run in cluster; bandwidth reported in JSON; both fixtures torn down |
| L3 | `kubectl get jobs -n roksbnkctl-test` | empty after L2 (cleanup ran) |
| L4 | (cred check) `kubectl get secret roksbnkctl-ibm-creds -n roksbnkctl-ops -o yaml \| grep -E '^\s*IBMCLOUD_API_KEY:'` | data is base64-encoded (not plaintext); decode matches the workspace's key |
| L5 | (RBAC check) `kubectl auth can-i delete pods --as=system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops -n default` | returns `no` (least-privilege RBAC) |
| L6 | (RBAC check) `kubectl auth can-i create jobs --as=...:roksbnkctl-ops -n roksbnkctl-test` | returns `yes` (granted by ClusterRole) |
| L7 | `roksbnkctl ops uninstall` (cleanup before Phase D's down) | exits 0; namespace + Secret + RBAC removed |

## Phase L-DNS — DNS probe (GSLB-aware) across backends

**Prereqs**: Phase D cluster up; `roksbnkctl ops install` from L0 still in place. `dig` removed from PATH (or never installed) to confirm internalization.

| Step | Command | Pass criterion |
|---|---|---|
| LD0 | `which dig` | not found (or, if installed, the test still runs without invoking it) |
| LD1 | `roksbnkctl test dns --target www.cloudflare.com --type A --server 8.8.8.8 --backend local` | exits 0; JSON output schema=`roksbnkctl.dns.v1`; `answers[].rdata` is a v4 IP; `rtt_ms.p50` populated |
| LD2 | `roksbnkctl test dns --target www.cloudflare.com --type AAAA --server 8.8.8.8 --backend local` | exits 0; answers contain v6 records |
| LD3 | `roksbnkctl test dns --target nonexistent-zzz.example.invalid --type A --server 8.8.8.8 --backend local` | exits 1; rcode=`NXDOMAIN`; clear error |
| LD4 | `roksbnkctl test dns --target www.cloudflare.com --type A --server 8.8.8.8 --iterations 10 --backend local -o json` | output includes `rtt_ms.p50/p95/p99` from 10 samples |
| LD5 | `roksbnkctl test dns --target www.cloudflare.com --type A --server 8.8.8.8 --backend k8s` | exits 0; probe runs as a Job in `roksbnkctl-test`, reuses the roksbnkctl binary in-cluster; Job auto-deleted after; `rtt_ms` reflects in-cluster→8.8.8.8 path |
| LD6 | `roksbnkctl test dns --target www.cloudflare.com --type A --server cluster --backend k8s` | exits 0; uses cluster CoreDNS (`/etc/resolv.conf` inside the pod); answers reflect cluster's view |
| LD7 | (GSLB comparison happy path) `roksbnkctl test dns --target www.cloudflare.com --type A --server 8.8.8.8 --gslb-compare -o json` | runs `local` + `k8s` vantages in parallel; `gslb_divergence: false` (Cloudflare anycast usually returns the same record from both); `vantages[]` has 2 entries with both RTTs |
| LD8 | (GSLB comparison divergence) Use a name that's geo-resolved (e.g., `www.google.com`) where local and cluster IPs hit different DCs: `... --gslb-compare ...` | `gslb_divergence: true`; summary explains divergence |
| LD9 | (SSH vantage, if jumphost configured) `roksbnkctl test dns --target www.cloudflare.com --type A --server 8.8.8.8 --backend ssh:jumphost` | binary exists on jumphost (auto-scp'd if missing); probe runs on jumphost; RTT measured remote→8.8.8.8 |
| LD10 | (negative — docker rejected by design) `roksbnkctl test dns --backend docker --target ...` | exits non-zero; clear "DNS probe doesn't benefit from docker; use local" error |

## Phase M — credential propagation audit

Cross-cutting check that runs **after** Phases I-L — confirms no creds leaked during the prior phases.

| Step | Check | Pass criterion |
|---|---|---|
| M1 | After K2: `docker history ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v>` | no `IBMCLOUD_API_KEY=...` ENV layer baked into the image |
| M2 | After K2: scan `docker inspect <last-container>` for the API key value | not found |
| M3 | After L1: `kubectl get events -n roksbnkctl-ops -o yaml \| grep <api-key-value>` | not found |
| M4 | After L1: `kubectl logs <ops-pod> -n roksbnkctl-ops \| grep <api-key-value>` | not found (the redactor wrapping should mask it if a tool prints it) |
| M5 | After I3: `ssh jumphost ls /tmp/roksbnkctl.* 2>/dev/null` | empty (tempfiles cleaned up by trap) |
| M6 | After I3: on jumphost, `cat /var/log/auth.log \| tail -50` | sshd shows `Accepted publickey` for the SSH session; if SetEnv was used, the var name is logged but not the value |
| M7 | Audit roksbnkctl's own log files (`~/.roksbnkctl/*/state/*.log` if any) for the API key | not found |

## Phase N — mixed-mode lifecycle

A realistic scenario: fresh `up`/`down` cycle that exercises a *different* backend for `down` than for `up`, validating cross-backend state-file portability. The driver implementation lives in [`scripts/e2e-test-backends.sh::phase_N`](https://github.com/jgruberf5/roksbnkctl/blob/main/scripts/e2e-test-backends.sh); the table below reflects what shipped at v1.0 (6 steps, N1-N6, restructured from the original PRD draft to be a single end-to-end lifecycle assertion rather than a fine-grained per-tool sequence).

The init-backend is auto-selected: `local` if `terraform` is on PATH, `docker` otherwise. The teardown-backend is whichever of `local`/`docker` was NOT used for init (or the same one if the alternative is unavailable). The integrator can override the init-backend with `ROKSBNKCTL_E2E_INIT_BACKEND=<name>`.

| Step | Command / check | Pass criterion |
|---|---|---|
| N1 | `roksbnkctl up --auto -w $WORKSPACE --backend $INIT_BACKEND --var-file $TFVARS` | exits 0; cluster + BNK come up (50-70 min wall time); 77 resources created |
| N2 | `roksbnkctl test throughput --backend k8s -w $WORKSPACE` (against cluster from N1) | exits 0; iperf3 client+server both run in cluster; bandwidth reported; fixtures torn down (skipped cleanly if no kube context reachable) |
| N3 | `roksbnkctl ibmcloud --backend ssh:$SSH_TARGET ks cluster ls -w $WORKSPACE` | exits 0; output contains `OK`; cluster from N1 visible from the SSH target's egress IP (skipped cleanly if no SSH target configured) |
| N4 | `roksbnkctl test dns --target www.cloudflare.com --type A --server 8.8.8.8 --backend k8s --gslb-compare -o json -w $WORKSPACE` | exits 0; output contains `gslb_divergence` boolean (multi-vantage probe emits the JSON aggregate; covers Phase L-DNS happy path) |
| N5 | `roksbnkctl down --auto -w $WORKSPACE --backend $TEARDOWN_BACKEND --var-file $TFVARS` (cross-backend) | exits 0; 77 resources destroyed; the .tfstate written by `$INIT_BACKEND` is readable by `$TEARDOWN_BACKEND` (state-file portability assertion) |
| N6 | Post-teardown state check: `test ! -f ~/.roksbnkctl/$WORKSPACE/cluster-outputs.json` | file removed by `down` (cluster-state cleanup invariant — Phase C in the baseline driver depends on the same shape) |

## Test infrastructure

- **New driver script**: `scripts/e2e-test-backends.sh` — runs Phases I-N. Depends on Phases A-H having set up a cluster (or accepts `--use-existing-cluster` to skip the up).
- **Existing `scripts/e2e-test.sh`** stays as the canonical "everything default" pass.
- **Combined runner**: `scripts/e2e-test-full.sh` runs A-H followed by I-N against the same cluster — saves ~50 min by sharing the cluster apply.
- **Per-phase logs**: each phase logs to `/tmp/roksbnkctl-e2e-backends/<phase>-<ts>.log` for forensics on failure.
- **PATH-strip helper**: J's PATH manipulation needs sudo (or a `mv` in user-writable bin dir). Document in script's `--help`.

## Acceptance criteria

- All phases I-N pass on a fresh test run against a live IBM Cloud account
- No credential leaks detected by any of M1-M7
- Each backend × tool combination exercised at least once across I-N
- E2E pass takes <5 hours total (the existing A-H baseline + the new I-N phases reuse the same cluster)
- Combined runner script (`e2e-test-full.sh`) provides a "one button" full-coverage test for CI

## Out of scope (this PRD)

- Performance benchmarking (latency comparison across backends — separate effort)
- Chaos / failure-injection testing (kill the docker daemon mid-call, drop SSH connection mid-stream, kill the ops pod, etc.)
- Windows test coverage (Linux + macOS for first round)
- Comparison against running cluster operations from inside vs. outside the customer's firewall (real-world simulation deferred)

## Open questions

- **Phase L's RBAC checks**: should we exercise the negative case (try a denied verb, confirm it's blocked) as part of the test, or trust the RBAC manifest? **Recommendation: yes, include the negative — it's the only way to verify the role binding is correct.**
- **Phase J PATH-stripping**: doing this with `sudo mv` is brittle and may interfere with concurrent CI jobs. Alternative: invoke `roksbnkctl` with a sanitized `PATH=$(echo $PATH | tr ':' '\n' | grep -v -E '/(kubectl|oc)$' | paste -sd:)` env. **Recommendation: env-var approach** — no filesystem mutations.
- **Tool image versions**: which version of ibmcloud-cli to bundle? Tracking the latest is moving target; pinning to a tested version risks staleness. **Recommendation: pin per roksbnkctl release tag**, with a doctor warning if a newer ibmcloud-cli is available upstream.
- **Trusted-profile path testing**: the trusted-profile auto-provisioning in Phase 3.1 needs IAM permissions to provision profiles. Account-level permission requirement is documented in [PRD 04](./04-CREDENTIALS.md); the test plan should explicitly note "skip Phase L's trusted-profile check if the account lacks IAM Identity Management Service Authority."

## Related work

- This PRD validates the deliverables of [PRDs 01-04](./00-OVERVIEW.md)
- Existing [docs/E2E_TEST.md](../E2E_TEST.md) (the baseline A-H plan) is preserved unchanged; this is purely additive
- The existing `scripts/e2e-test.sh` driver pattern (`step` / `capture` / `assert_contains`, `PHASE_FROM=` resume) is reused for the new driver
