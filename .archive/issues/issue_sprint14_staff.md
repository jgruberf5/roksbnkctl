# Sprint 14 — staff issues (get-well cycle, folds into held v1.5.0)

> **Sprint 14 frame.** Get-well cycle. Headline = make `roksbnkctl up`
> → `roksbnkctl --on jumphost kubectl|oc` work **end-to-end** by fixing
> the jumphost kubeconfig provisioning failure (Sprint 13 fixed the env
> leak; the jumphost still has no kubeconfig at all). Folds into the
> held `v1.5.0` — no separate tag. Integrator decision = **option C
> (both layers)**. The e2e/blind-spot test is pulled forward from the
> Sprint 15 consolidation plan as deliverable 3.
>
> **Design surface:** `issues/issue_sprint13_architect.md` Issue 2
> (root cause, the cloud-init code excerpt, the full option-A/B/C
> analysis, the option-C + hold-and-merge integrator decisions) and
> `issues/issue_sprint13_staff.md` Issue 1 §"Related" / §"Closure" (the
> 2026-05-18 14:54 live diagnostic + the env-split surface to build
> on). Not duplicated here — read those.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1: jumphost has no kubeconfig — `--on jumphost kubectl|oc` deterministically fails `localhost:8080` (option C)

**Severity**: high (deterministically breaks the documented private-cluster workflow; blocks the held `v1.5.0`)
**Status**: REOPENED (was resolved) — live verify FAILED 2026-05-18, in a new and precise way. Part B's heal-vs-outage discrimination works correctly (detected missing remote kubeconfig, bounded 4 retries, no spin, surfaced the real error instead of masking — design sound). BUT Part B runs `ibmcloud ks cluster config --admin` on the target without first running `ibmcloud login` on the target; the user's already-broken jumphost has an **unauthenticated `ubuntu` ibmcloud profile** (old cloud-init's `su - ubuntu -c "ibmcloud login … || true"` failed silently at provision time), so `ks cluster config` fails with `FAILED — Log in to the IBM Cloud CLI by running 'ibmcloud login'`. Part A fixes this for *new* deploys only (boot-time); it does nothing for an existing box. **Scope gap:** Part B cannot meet its stated goal ("unblock already-broken jumphosts with no redeploy") unless it also performs `ibmcloud login` on the target (using the workspace API key + region/resource-group it already has) before `ks cluster config`. Gate NOT met by the first Part B cut. **Part B login-extension landed 2026-05-18 (integrator decision = "extend Part B"):** `internal/cli/selfheal.go` `remoteHealCommand` now runs `ibmcloud login --apikey <key> -r <region> [-g <rg>] && ibmcloud ks cluster config --cluster <id> --admin` on the target each heal attempt (creds resolved in `remote.go` via the same `cred.Resolver` `workspaceEnvCore` uses; passed as injection-safe positional params, key never in the script literal). Heal-vs-outage discrimination preserved; a bad/expired key is surfaced as a bounded outage, not masked. New regression guards `TestE2E_SelfHeal_NotLoggedIn_LoginThenConfig` + `TestE2E_SelfHeal_BadCredentials_SurfacedAsOutage`; all 13 e2e + 4 Sprint-13 env-leak guards + full `internal/cli`/`internal/tf` green; `go build ./...`/`go vet` clean. **RESOLVED — LIVE-VERIFIED 2026-05-18 16:33** (integrator ran the test directly, user-authorized: "the existing jumphost and cluster is up; you can run the test yourself"). `./bin/roksbnkctl exec --on jumphost kubectl get pods` (the exact 2026-05-18 14:51/14:54 failing command, rebuilt binary 16:33 with the login-extension) →
> `→ remote kubeconfig missing on target; self-healing (ibmcloud ks cluster config --admin, attempt 1/4)`
> `✓ remote kubeconfig provisioned via self-heal`
> `No resources found in default namespace.`  `EXIT=0`

The `localhost:8080` symptom is gone: Part B detected the missing remote kubeconfig, performed `ibmcloud login` + `ks cluster config --admin` on the target (attempt 1), and `kubectl` reached the cluster API (empty-namespace success, exit 0). Already-broken jumphost repaired with **no redeploy** — option C / Part B goal met end-to-end. This issue + `issues/issue_sprint13_architect.md` Issue 2 are resolved; the held `v1.5.0` is now genuinely tag-ready (pending the integrator `make release` gate).

### Symptom (live-confirmed 2026-05-18 14:54)

After Sprint 13's env-leak fix is live-verified working (`KUBECONFIG=[]`
on the wire), `roksbnkctl exec --on jumphost kubectl get pods` still
returns `The connection to the server localhost:8080 was refused`.
Diagnostic showed `uid=1000(ubuntu)`, `KUBECONFIG=[]`, and
`/home/ubuntu/.kube/config: No such file or directory` — the jumphost
has **no kubeconfig at all**.

### Root cause

`terraform/modules/testing/main.tf` cloud-init runs `ibmcloud login …
|| true` then `ibmcloud ks cluster config --cluster
"${var.roks_cluster_name_or_id}" --admin || true`, and only copies
`/root/.kube/config` → `/home/ubuntu/.kube/config` `if [ -f
/root/.kube/config ]`. The `|| true` swallows any boot-time failure
(cluster not Ready at boot, region/resource-group mismatch, transient
IAM/API error) with **no retry, no log, no failure marker** — the file
is simply never written and stays absent until a human re-runs the
commands. Full analysis: `issues/issue_sprint13_architect.md` Issue 2.

### Fix — option C (both layers; integrator-decided)

- **Part A — cloud-init hardening** (`terraform/modules/testing/main.tf`):
  bounded retry/readiness loop around the `ibmcloud login` + `ibmcloud
  ks cluster config --admin` (and the `su - ubuntu` login fork);
  replace the silent `|| true` with a loud failure marker
  (`/var/log/jumphost-setup.log` + a sentinel file) on exhaustion;
  reliably produce `/home/ubuntu/.kube/config` (mode `0600`,
  `ubuntu:ubuntu`) when the cluster becomes reachable. Fixes new
  deploys.
- **Part B — roksbnkctl `--on` self-heal** (`internal/cli/`): when an
  `--on <target>` `kubectl`/`oc` dispatch targets a host with no
  usable kubeconfig, run `ibmcloud ks cluster config --cluster <id>
  --admin` on the target before the wrapped command (and/or post-`up`
  push a freshly-fetched admin kubeconfig to seeded jumphost targets).
  Idempotent; **must distinguish "no kubeconfig → heal" from "cluster
  genuinely down → surface the real error after bounded retry, don't
  spin"**. Reuses existing workspace cluster-id/state plumbing — no
  CLI-layer path/state re-derivation (the Sprint 12/13 bug class).
  Unblocks already-broken/already-running jumphosts with no `terraform`
  recreate.

### Deliverable 3 — e2e + `--on` integration test (closes the blind spot)

`internal/cli/lifecycle_e2e_test.go` (new) drives `up → --on <target>`
asserting BOTH the remote-vs-local env composition (Sprint 13 Issue 1
surface) AND the part-B heal-vs-outage paths; plus a `-tags
integration` `--on`/passthrough smoke against ephemeral kind. Makes
Issue-1-class + missing-remote-kubeconfig defects fail a test, not a
human (this defect reached the user live, not the gate).

### Acceptance criteria

- `grep -n "|| true" terraform/modules/testing/main.tf` — the
  kubeconfig-critical commands are no longer silently swallowed (retry
  + loud marker instead); cloud-init still completes its other setup.
- Part B: missing-remote-kubeconfig → heal attempted and succeeds when
  the cluster is reachable; cluster-down → the real `ibmcloud ks
  cluster config` error is surfaced after bounded retry (no infinite
  spin, no silent fallback to the broken state).
- New e2e + `-tags integration` tests green in the standard +
  integration build; the Sprint 13 Issue-1 regression guard present and
  green.
- Whole-module `go build ./...`, `go vet ./...`, `gofmt -l .`,
  `go test ./...`, `make staticcheck` clean/green; `terraform fmt
  -check terraform/modules/testing/` clean.
- **Out-of-band (user):** live `roksbnkctl up` → `roksbnkctl --on
  jumphost kubectl get pods` succeeds end-to-end (no `localhost:8080`).
  Baseline repro = the 2026-05-18 14:54 diagnostic. This is the gate
  for flipping `issues/issue_sprint13_architect.md` Issue 2 →
  `resolved` and the held `v1.5.0` → tag-ready.

### Closure

Option C implemented in full (both layers); no deviation from the
integrator decision.

**Part A — cloud-init hardening (`terraform/modules/testing/main.tf`).**
The two kubeconfig-critical commands that were bare `|| true`
(`ibmcloud login` + `ibmcloud ks cluster config --cluster
"${var.roks_cluster_name_or_id}" --admin`) are now wrapped in a
**bounded retry/readiness loop** (`KCFG_MAX_ATTEMPTS=30`,
`KCFG_SLEEP_SECONDS=20` → ~10 min total) whose success gate is
`/root/.kube/config` actually existing — i.e. it waits for the ROKS
cluster to become Ready rather than swallowing a not-ready boot. The
`su - ubuntu -c "ibmcloud login …"` fork (an unconfigured `ubuntu`
ibmcloud profile is its own failure mode — it gates part B) got the
same bounded-retry posture. On exhaustion it **fails loudly**: a
timestamped per-attempt diagnostic in `/var/log/jumphost-setup.log`
**and** a sentinel file `/var/log/jumphost-kubeconfig-FAILED` with the
cluster/region and a re-run hint — visible instead of silent. It does
**not** abort cloud-init (the rest of setup, key files, still
completes; the loud marker is the fix for the silent swallow, not a
hard abort). The `/root/.kube/config` → `/home/ubuntu/.kube/config`
copy still runs (mode `0600`, `chown ubuntu:ubuntu`). The final
`jumphost-setup.log` write was changed from truncating (`>`) to
appending (`>>`) so the retry diagnostic survives. Shell is
POSIX/bash-clean and `set -e`-safe (the critical commands are inside
`if … then … fi` conditions, whose non-zero status `set -e` does not
propagate); no new terraform variables added (boot-time constants).
The only remaining `|| true` in the file are on non-critical
housekeeping (log-truncate, marker cleanup, the `kcfg_log` helper,
`ibmcloud config --check-version`/plugin installs) — none
kubeconfig-critical.

> Part A is **boot-time and not unit-testable** (it runs inside the
> jumphost cloud-init at first boot). Its verification is the
> `-tags integration` `--on`/passthrough smoke plus the user's live
> `roksbnkctl up` (acceptance criteria §"Out-of-band"); a `terraform
> validate`/`apply` is not runnable in the agent shell (terraform
> invocation is blocked here — `terraform fmt -check` likewise could
> not be run in-agent; the edit added only heredoc shell body +
> reused existing assignment lines, no new HCL attributes, so it is
> fmt-neutral — validator/integrator should run `terraform fmt -check
> terraform/modules/testing/` once in a terraform-capable shell).

**Part B — roksbnkctl `--on` self-heal (`internal/cli/selfheal.go` +
`remote.go`).** New `internal/cli/selfheal.go`. On every `--on
<target>` dispatch whose argv is `kubectl`/`oc` (only those read a
kubeconfig), `dispatchRemote` runs a pre-flight: probe the target with
`kubectl config current-context` (reads only the local kubeconfig
file — does **not** touch the kube API, so a healthy-config-but-
cluster-down host is NOT misread as "no config"). If a usable config
is present → no-op (idempotent, zero extra round-trips on the healthy
path). If absent → `healRemoteKubeconfig` runs `ibmcloud ks cluster
config --cluster <id> --admin` **on the target** (already
`ibmcloud login`'d as `ubuntu` per the cloud-init fork) with
**bounded retry** (`selfHealMaxAttempts=4`, `selfHealBackoff=6s`),
re-probing after each success. **Heal-vs-outage discrimination:** a
heal that makes the config usable returns nil; bounded retry
exhausted with the config still unusable returns a non-nil error that
**names the real last `ibmcloud ks cluster config` stderr** and
states this is a genuine outage/misconfig, not a fixable
missing-kubeconfig — dispatch then aborts (`ExitAuthFailed`) instead
of letting kubectl fall back to `localhost:8080`. Never masks an
outage as success; never spins forever; never silently falls back to
the broken state. Cluster id comes from `clusterFromTFOutput`
(`roks_cluster_id`/`roks_cluster_name`) then `cctx.Workspace.Cluster.Name`
— the **same workspace plumbing** `tryAutoKubeconfig`/
`resolveClusterIdentity` use; nothing re-derived at the CLI layer
(the Sprint 12/13 bug class). A new `remoteRunner` seam +
`clientRunner` adapter on `*remote.Client` keeps the logic
SSH-free-testable. **Smallest correct surface chosen: the `--on`
pre-flight self-heal only; the post-`up`-push variant was NOT added**
— the pre-flight repairs already-broken/already-running jumphosts
(the live 2026-05-18 case) on demand with no `up` re-run and no
duplicate kubeconfig-push plumbing, which is the get-well goal; a
post-`up` push would heal only freshly-`up`'d hosts and is redundant
given part A already provisions new deploys. This is a deliberate,
documented narrowing within option C, not a deviation from it.

**Deliverable 3 — e2e + `-tags integration` (`internal/cli/
lifecycle_e2e_test.go` new, `lifecycle_e2e_integration_test.go` new).**
`lifecycle_e2e_test.go` (standard build, 7 tests, all PASS):
`TestE2E_RemoteVsLocalEnvComposition` (Issue-1 surface: KUBECONFIG
absent on the wire, present locally, + remoteSafeEnv backstop),
`TestE2E_SelfHeal_HealthyConfig_NoOp`,
`TestE2E_SelfHeal_MissingConfig_HealsAndSucceeds`,
`TestE2E_SelfHeal_ClusterDown_SurfacesRealError` (asserts the real
error is surfaced and exactly `selfHealMaxAttempts` attempts — no
infinite spin),
`TestE2E_SelfHeal_NonKubectl_NoOp`,
`TestE2E_SelfHeal_NoClusterID_ClearError`,
`TestE2E_SelfHeal_ProbeTransportError_Surfaces`. The 4 Sprint 13
Issue-1 guards in `env_split_test.go` remain present + PASS.
`lifecycle_e2e_integration_test.go` (`-tags integration`):
`TestIntegration_KubectlPassthrough_ReachesCluster` +
`_GetNodes` assert the wired `roksbnkctl kubectl` passthrough reaches
a real API server and does NOT hit `localhost:8080`; both **skip
cleanly** when the workspace/API-key (or kind) prerequisite is absent,
per the Sprints 10–13 precedent. An Issue-1-class leak or a
missing-remote-kubeconfig defect now fails a test, not a human.

**Build/test sweep.** `go build ./...`, `go vet ./...`,
`gofmt -l .`, `go test ./...`, `make staticcheck`,
`go build -tags integration ./...`, `go vet -tags integration
./internal/cli/` — all clean/green. `go test -tags integration
./internal/cli/` has one **pre-existing, out-of-scope** failure:
`TestIntegration_OpsInstall_ShowsRBACAndPod` (Sprint 4 ops test,
unmodified this cycle) does not skip when no API-key workspace is
configured — not introduced by and unrelated to this sprint's
`--on`/kubeconfig surface; the new Sprint 14 integration tests skip
cleanly. `terraform fmt -check` / `terraform validate` could not be
run in-agent (terraform invocation blocked); see the part-A note —
fmt-neutral edit, validator/integrator to confirm once in a
terraform-capable shell.

### Out of scope

- The Sprint 15 consolidation (single path/env chokepoint, `internal/cli`
  decomposition, process tiering) — do not start it here.
- Option-(b) per-AZ stale-target reconcile — unchanged post-`v1.5.0`
  follow-up.
- Any new `v1.5.1`/`v1.6.0` notion — this folds into the held `v1.5.0`.

---

## Sprint 14 ledger closeout — `v1.5.0` shipped 2026-05-18

**Status: CLOSED.** Sole Issue 1 terminal — REOPENED→RESOLVED, live-verified 16:33.

`v1.5.0` is cut, released, and published:

- **Tag:** annotated `v1.5.0` on `5113b74` (the `chore: prep v1.5.0 release` commit — matches the `v1.4.1`/`v1.3.0`/`v1.2.1` tag-placement convention).
- **CI gate green pre-tag:** `ci.yml` (vet/fmt/staticcheck/test, ubuntu+macos, goreleaser-check) ✅; `book.yml` (`mdbook build`) ✅ on the book-touching commit `d6c8bf8`; plus the integrator's live `roksbnkctl --on jumphost kubectl` verify 2026-05-18 16:33 (self-healed attempt 1, `localhost:8080` gone, exit 0, no redeploy).
- **GitHub Release:** live, not draft — 8 assets (6 platform archives + `checksums.txt` + `roksbnkctl-book-v1.5.0.pdf`); `release.yml`/goreleaser run completed success.
- **Book:** HTML → `gh-pages` live at <https://jgruberf5.github.io/roksbnkctl/book/> (HTTP 200); PDF attached to the Release.
- **Release notes:** curated `v1.5.0` announcement published (headline = the end-to-end `--on jumphost` fix; Fixed / Added / Install).

All Sprint 13 + Sprint 14 gate criteria met (Sprint 13 §"Gate to `v1.5.0`" + Sprint 14 §"Gate to the (finally tag-ready) `v1.5.0`"). The post-v1.4.0 jumphost thread is closed end-to-end. The only forward items are the explicitly-tracked post-`v1.5.0` follow-ups: per-AZ stale-target reconcile option (b), and the path/env chokepoint + `cli` consolidation (`docs/PLAN.md` §"Sprint 15"). **Ledger closed 2026-05-18.**
