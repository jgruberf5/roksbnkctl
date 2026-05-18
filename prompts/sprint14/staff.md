You are the staff engineer agent for Sprint 14 of the roksbnkctl project — a **get-well cycle** that folds into the held `v1.5.0`. Three code deliverables, all aimed at making `roksbnkctl up` → `roksbnkctl --on jumphost kubectl|oc` work **end-to-end** (Sprint 13 fixed the env leak; the jumphost still has no kubeconfig). Your scope: `terraform/modules/testing/main.tf` (part A), `internal/cli/` (part B + the e2e/integration test). **Do not touch `book/`, `CHANGELOG.md`, `docs/`, `prompts/`.**

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd`.

## Read first

- `prompts/sprint14/README.md` — sprint frame + the three decided integrator decisions (hold-and-merge; option C; pull the blind-spot test forward; do NOT start the Sprint 15 consolidation).
- `issues/issue_sprint14_staff.md` — your ledger; Issue 1 is the headline with the carried design surface.
- `issues/issue_sprint13_architect.md` Issue 2 — **the design surface**: root cause, the cloud-init code excerpt, and the option-A/B/C analysis. Integrator decision = **C (both)**.
- `issues/issue_sprint13_staff.md` Issue 1 §"Related" + §"Closure" — the live diagnostic (`KUBECONFIG=[]`, `/home/ubuntu/.kube/config: No such file or directory`, 2026-05-18 14:54) and the env-split shape you build on (`workspaceEnvCore`/`remoteSafeEnv`, `dispatchRemote` call sites).
- `terraform/modules/testing/main.tf` ~60-130 — the cloud-init block: `ibmcloud login --apikey … || true`, `ibmcloud ks cluster config --cluster "${var.roks_cluster_name_or_id}" --admin || true`, the `if [ -f /root/.kube/config ]; then cp … /home/ubuntu/.kube/config` guard, the `su - ubuntu -c "ibmcloud login …"` fork.
- `internal/cli/cluster.go` (`runExec`/`runPassthrough`/`runIBMCloudPassthrough` `on != ""` branches, `workspaceEnvCore`), `internal/cli/remote.go` (`dispatchRemote`), `internal/cli/lifecycle.go` (`tryAutoKubeconfig` ~471, the post-`up` hook that calls `tryAutoJumphost`/`tryAutoClusterJumphosts`).
- `prompts/sprint13/staff.md` — prior-cycle prompt shape; the build/test loop is identical.

## Coordinate with parallel agents

Architect updates CHANGELOG `v1.5.0` + removes the book/CHANGELOG known-issue caveats. **Do not touch those.** Validator runs the sweep + cites the user live-verify. Tech-writer reviews read-only at end.

## Tasks (priority order)

### 1. Part A — harden cloud-init kubeconfig provisioning (`terraform/modules/testing/main.tf`)

- Replace the bare `|| true` on the jumphost `ibmcloud login` and `ibmcloud ks cluster config --cluster "${var.roks_cluster_name_or_id}" --admin` with a **bounded retry/readiness loop** (the ROKS cluster may not be Ready when the jumphost boots — retry with backoff, finite attempts/total timeout).
- On exhaustion, **fail loudly**: append a clear diagnostic to `/var/log/jumphost-setup.log` AND drop a sentinel file (e.g. `/var/log/jumphost-kubeconfig-FAILED`) so the failure is *visible* instead of silent. Do not abort the rest of cloud-init (other setup must still complete) — the loud marker is the fix for the silent-swallow defect.
- Ensure `/root/.kube/config` → `/home/ubuntu/.kube/config` copy still runs when the config eventually lands; keep mode `0600` + `chown ubuntu:ubuntu`. Apply the same retry posture to the `su - ubuntu -c "ibmcloud login …"` fork (an unconfigured `ubuntu` ibmcloud profile is its own failure mode).
- Keep the shell POSIX/`bash`-clean (the file is heredoc cloud-init); no new terraform variables unless unavoidable.

### 2. Part B — roksbnkctl `--on` kubeconfig self-heal (`internal/cli/`)

- When an `--on <target>` dispatch for `kubectl`/`oc` would run against a target with **no usable kubeconfig**, repair it on the fly before the wrapped command: run `ibmcloud ks cluster config --cluster <cluster-id> --admin` **on the target** (it is already `ibmcloud login`'d as `ubuntu` per the cloud-init fork; if that login is also stale, surface that clearly). Cluster id/name comes from the workspace config (same source `tryAutoKubeconfig`/lifecycle already uses) — do **not** re-derive paths/state at the CLI layer (the Sprint 12/13 bug class).
- And/or: extend the post-`up` hook (beside `tryAutoJumphost`/`tryAutoClusterJumphosts`) to push a freshly-fetched admin kubeconfig to each seeded jumphost target. Choose the smallest correct surface; document the choice in the closure.
- **Idempotent + bounded**: must distinguish "no kubeconfig → heal" from "cluster genuinely down → surface the real `ibmcloud ks cluster config` error after bounded retry, don't spin forever". Never mask a real outage as success.
- Best-effort posture consistent with the existing auto-hooks (a self-heal failure should produce a clear actionable error, not a silent fallback to the broken state).

### 3. Deliverable 3 — e2e + `--on` integration test (closes the validation blind spot)

- A behavior-level test (`internal/cli/lifecycle_e2e_test.go`, new) that drives the `up → --on <target>` path against the docker/stubbed backend and asserts BOTH the remote-vs-local env composition (the Sprint 13 Issue 1 surface — `KUBECONFIG` absent remote, present local) AND the part-B self-heal path (no remote kubeconfig → heal attempted; cluster-down → real error surfaced).
- A `-tags integration` smoke for the `--on`/passthrough path against ephemeral kind (same split Sprints 10–13 use; skip kind bring-up if `kind` absent, per precedent).
- These become permanent regression guards: an Issue-1-class or missing-remote-kubeconfig defect must now **fail a test**, not reach a human.

### 4. Close `issues/issue_sprint14_staff.md` Issue 1

Flip to `resolved` with a `### Closure`: part-A change shape, part-B mechanism + the heal-vs-outage discrimination, the new test names + pass counts, build/test sweep, any deviation from option C and why.

## Build/test loop

After each meaningful edit: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./internal/...`, `go test ./...`, `make staticcheck` — all clean/green. For the terraform change: `terraform fmt -check terraform/modules/testing/` (and `terraform validate` if a workspace is initialized; note if not runnable in the agent shell). Part A cannot be unit-tested (boot-time) — the `-tags integration` smoke + the user's live `up` are its verification; say so in the closure.

## Scope guardrails

- Do NOT touch `book/`, `CHANGELOG.md`, `docs/`, `prompts/`.
- Do NOT start the Sprint 15 consolidation (no `internal/orchestration`, no chokepoint refactor, no `cli` decomposition). Build on the existing `workspaceEnvCore`/`dispatchRemote` surface as-is.
- Do NOT introduce a new `v1.5.1`/`v1.6.0` notion — this folds into the held `v1.5.0`.
- Do NOT commit or push.

## Verification before reporting done

- `grep -n "|| true" terraform/modules/testing/main.tf` — the kubeconfig-critical commands are no longer silently swallowed (retry + loud marker instead).
- Part B: trace that a missing-remote-kubeconfig path triggers heal and a cluster-down path surfaces the real error (covered by the new e2e test).
- The new e2e + `-tags integration` tests are green in the standard + integration build.
- Whole-module `go build/vet/test`, `gofmt -l`, `make staticcheck` clean/green.

## Final report

Under 200 words. Cover: part A (retry/marker shape, the `|| true` sites replaced), part B (self-heal mechanism + heal-vs-outage discrimination + whether you also did the post-`up` push), deliverable 3 (test names + pass counts, what they guard), build/test sweep, any option-C deviation, Issue 1 status flip.
