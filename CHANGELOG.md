# Changelog

All notable changes to `roksbnkctl` are documented in this file. Format follows the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) convention; the project uses [semantic versioning](https://semver.org/spec/v2.0.0.html) starting at `v0.9.0`.

Per-sprint design rationale lives in [`docs/PLAN.md`](docs/PLAN.md); per-PRD design specs live under [`docs/prd/`](docs/prd/). This file is the user-facing summary of what changed between releases.

## v1.5.0 — 2026-05-18

Sprints 13–14 — minor feature cycle plus its get-well fold-in. Closes the post-v1.4.0 per-AZ-jumphost user-testing thread in one coherent release: the headline is that `roksbnkctl up` → `roksbnkctl --on jumphost kubectl|oc …` finally works **end-to-end** against a private cluster. That required fixing **two** independent causes of the same `connection to the server localhost:8080 was refused` symptom — the local `KUBECONFIG` path leaking across the SSH boundary (Sprint 13; the bug disclosed as the `v1.4.1` known issue and originally designated a `v1.4.2` fast-follow) **and** the jumphost having no kubeconfig at all because cloud-init swallowed provisioning failures (Sprint 14 get-well, option C). Because the two causes are indistinguishable to a user, the integrator held `v1.5.0` open and merged the Sprint 14 fix into the same release rather than ship the headline fix still reproducible. The cycle also lands two ergonomic features (a read-only `roksbnkctl terraform` escape hatch and automatic registration of the per-AZ cluster jumphosts) and the book docs that tie them together — all surfaced in one user session, all about reaching/operating the deployed cluster from the workstation. See [PLAN.md §"Sprint 13"/"Sprint 14"](docs/PLAN.md), [PRD 08](docs/prd/08-TERRAFORM-READONLY.md), [PRD 09](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md), [`issues/issue_sprint13_staff.md`](issues/issue_sprint13_staff.md) Issues 1–3, [`issues/issue_sprint13_architect.md`](issues/issue_sprint13_architect.md) Issue 2, and [`issues/issue_sprint14_staff.md`](issues/issue_sprint14_staff.md) Issue 1 for the design surface.

### Added

- **Read-only `roksbnkctl terraform` escape hatch** (alias `tf`) ([PRD 08](docs/prd/08-TERRAFORM-READONLY.md); [`issues/issue_sprint13_staff.md` Issue 2](issues/issue_sprint13_staff.md)) — a gated, **read-only-by-allowlist** passthrough to terraform against a workspace's managed state, replacing the fragile, undocumented `cd ~/.roksbnkctl/<ws>/state[-cluster] && TF_DATA_DIR=$PWD/terraform terraform …` workaround. Permitted subcommands: `output`, `show`, `state list`, `state show`, `state pull`, `providers`, `version`, `graph`, `validate`, `fmt -check`. Everything else is rejected **before terraform runs** — every mutating subcommand (`apply`/`destroy`/`init`/`import`/`taint`/…), mutating sub-verbs of `state` (`state rm`/`mv`/`push`/`replace-provider` are rejected even though top-level `state` is allowlisted), mutation flags (`-auto-approve`/`-replace`/`-target`/`-destroy`/write-mode `fmt`), and `--on` (managed state is workstation-local). Phase-correct cwd + `TF_DATA_DIR` are reused from the existing `tf.Open` plumbing — the CLI layer never re-derives them (the bug class this whole cycle addresses). Against a never-applied workspace phase it errors with `run roksbnkctl up first` and produces **no** source fetch / `init` side effect. `--phase cluster` selects the cluster-phase state. Mutations remain the exclusive domain of `up`/`plan`/`apply`/`down` (and `cluster`/`bnk` up/down) — this gate is permanent by design.
- **Per-AZ cluster-jumphost auto-registration** ([PRD 09](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md); [`issues/issue_sprint13_staff.md` Issue 3](issues/issue_sprint13_staff.md)) — when `testing_create_cluster_jumphosts = true`, `roksbnkctl up`'s post-apply hook now also auto-registers one target per cluster-VPC availability zone, named `jumphost-<zone>` (e.g. `jumphost-ca-tor-1`), alongside the existing singular `jumphost`. The hook reads the `testing_cluster_jumphost_ips` terraform output (a `{zone => floating-IP}` map) and reuses the shared `jumphost_shared_key` output, so every per-AZ jumphost is a first-class `--on` target (full `kubectl`/`oc`/`ibmcloud`/`shell` passthrough, no SSH hop) with no manual `targets add`. Registration is best-effort/non-fatal (a parse or write failure logs one `warning:` and does not fail `up`, mirroring the singular `jumphost` seed); a `testing_create_cluster_jumphosts = false` / absent / empty-map deploy is a silent no-op with no behavior change. Idempotent — re-running `up` after a floating-IP rotation refreshes each `jumphost-<zone>` host in place. **Caveat (option (a) upsert-only, decided by the integrator):** if a zone is removed or `testing_create_cluster_jumphosts` is flipped to `false`, the now-orphaned `jumphost-<oldzone>` target lingers until you run `roksbnkctl targets remove jumphost-<oldzone>` by hand. A reconcile mode that prunes orphans automatically (option (b)) is a tracked post-`v1.5.0` follow-up — see `### Deferred`. Documented in [Chapter 15 §"Auto-discovery from terraform outputs"](book/src/15-ssh-targets.md) and [Chapter 16](book/src/16-on-flag-ssh-jumphosts.md).

### Fixed

- **Local `KUBECONFIG` path no longer leaks into the `--on <target>` remote environment** ([`issues/issue_sprint13_staff.md` Issue 1](issues/issue_sprint13_staff.md)) — after any successful local `roksbnkctl up` (which writes the admin kubeconfig to the local `~/.kube/config`), a subsequent `roksbnkctl --on <target> kubectl|oc …` deterministically failed with `connection to the server localhost:8080 was refused`. Root cause: `workspaceEnv()` appended `KUBECONFIG=<local filesystem path>` and `runPassthrough` forwarded the *same* env slice across the SSH boundary, where the local path is meaningless on the target *and* shadowed the cloud-init-provisioned `/home/ubuntu/.kube/config`. The env that crosses the SSH boundary is now machine-portable only — value-grade vars (`IBMCLOUD_API_KEY` / `IC_API_KEY` / `IBMCLOUD_REGION` / `IBMCLOUD_VERSION_CHECK`) still forward; the local-only `KUBECONFIG` path does not. Local `roksbnkctl kubectl` (no `--on`) is unchanged — it still resolves `KUBECONFIG` via the local chain. Correctness comes from never *sending* the local path, so the fix is independent of the target sshd's `AcceptEnv`. This is one half of restoring the canonical private-cluster workflow documented in [Chapter 16](book/src/16-on-flag-ssh-jumphosts.md) and [Chapter 9](book/src/09-registering-existing-cluster.md) — see the jumphost-kubeconfig fix below for the other half; together they make `--on jumphost kubectl|oc` work end-to-end. (This symptom was disclosed as the `v1.4.1` known issue and is fully resolved in `v1.5.0` together with the jumphost kubeconfig provisioning fix below.)
- **Jumphost kubeconfig is now reliably provisioned end-to-end** ([`issues/issue_sprint13_architect.md` Issue 2](issues/issue_sprint13_architect.md); [`issues/issue_sprint14_staff.md` Issue 1](issues/issue_sprint14_staff.md)) — after the `KUBECONFIG`-leak fix above, `roksbnkctl --on jumphost kubectl|oc …` could *still* fail with `connection to the server localhost:8080 was refused` because the jumphost had **no kubeconfig at all**: cloud-init's `ibmcloud login` + `ibmcloud ks cluster config --cluster <id> --admin` were guarded by `|| true`, so any boot-time failure was swallowed with no retry, no log, and no failure marker — `/home/ubuntu/.kube/config` was simply never written. Fixed in two layers (option C): (A) the cloud-init provisioning in the upstream HCL now wraps the `ibmcloud login` + `ks cluster config --admin` path in a bounded retry/readiness loop and writes a loud failure marker (`/var/log/jumphost-setup.log` + a `/var/log/jumphost-kubeconfig-FAILED` sentinel) on exhaustion instead of silently continuing — new deploys reliably produce `/home/ubuntu/.kube/config`; and (B) `roksbnkctl --on <target> kubectl|oc` self-heals — if the target has no usable kubeconfig it runs `ibmcloud login` (with the workspace's API key + region/resource-group, so an already-broken jumphost whose cloud-init login fork failed silently is re-authenticated too, not just re-configured) followed by `ibmcloud ks cluster config --admin` on the target before the wrapped command (distinguishing "no kubeconfig → heal" from "cluster genuinely down or bad/expired credentials → surface the real error after bounded retry, never silently fall back to the broken state"), so an already-running/already-broken jumphost is repaired with no `terraform` recreate. Together with the `KUBECONFIG`-leak fix above, this makes `roksbnkctl up` → `roksbnkctl --on jumphost kubectl|oc …` work **end-to-end**: the leak fix stops the local path shadowing the remote kubeconfig, and this fix guarantees the remote kubeconfig exists. A new `up → --on` e2e + `-tags integration` test (`internal/cli/lifecycle_e2e_test.go`) makes both the env-composition and the heal-vs-outage paths fail a test rather than a human.

### Deferred (v1.x roadmap, post-v1.5.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). Carried forward from [v1.4.1 §"Deferred"](#deferred-v1x-roadmap-post-v141), plus one new follow-up from this cycle:

- **Per-AZ jumphost stale-target reconcile (option (b))** ([PRD 09 §"Open questions"](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md#open-questions); [`issues/issue_sprint13_architect.md`](issues/issue_sprint13_architect.md)) — `v1.5.0` ships option (a) upsert-only: orphaned `jumphost-<oldzone>` targets linger after a zone removal until manually removed. A reconcile mode that prunes them automatically needs unambiguous ownership semantics (a constrained zone-pattern match or a `config.TargetCfg` `auto:`/`managed_by:` schema marker, so a user's hand-named `jumphost-mybox` is never deleted) — a config-schema change deliberately out of `v1.5.0` scope. Tracked as a post-`v1.5.0` follow-up.
- **`ops install` / `ops uninstall` snapshot** ([PRD 07 §"Open questions" item 1](docs/prd/07-DEPLOYED-TFVARS.md#open-questions)) — carry-forward from v1.4.1 / v1.4.0.
- All prior-cycle deferred items from [v1.4.1 §"Deferred"](#deferred-v1x-roadmap-post-v141), [v1.4.0 §"Deferred"](#deferred-v1x-roadmap-post-v140), and [v1.3.0 §"Deferred"](#deferred-v1x-roadmap-post-v130) remain deferred.

## v1.4.1 — 2026-05-18

Sprint 12 closure cycle — `v1.4.1`. Focused patch closing **two** sibling relative-path-resolution bugs surfaced post-v1.4.0, both instances of the same shell-CWD-vs-state-dir trap. The headline fix: when a user passed `--var-file=./terraform.tfvars` from a directory containing that file, terraform reported `Failed to read variables file. Given variables file ./terraform.tfvars does not exist.` because the flag value was forwarded verbatim to a terraform invocation whose working directory is the per-phase state dir, not the user's shell PWD. Relative `--var-file` paths now resolve against the invocation CWD before reaching either backend. The second fix (pulled forward from the Sprint 13 backlog per integrator decision) closes the analogous trap for a relative `--tf-source=./...` local path, which was persisted relative into `config.yaml` at `init` and detonated on a later `up` / `plan` / `apply` run. No new PRDs this cycle. See [PLAN.md §"Sprint 12"](docs/PLAN.md), [`issues/issue_sprint12_staff.md` Issue 1](issues/issue_sprint12_staff.md), and [`issues/issue_sprint12_validator.md` Issue 5](issues/issue_sprint12_validator.md) for the design surface.

### Fixed

- **`--var-file` relative paths now resolve against the invocation CWD** ([`issues/issue_sprint12_staff.md` Issue 1](issues/issue_sprint12_staff.md)) — `roksbnkctl up --var-file=./terraform.tfvars`, `cluster up --var-file=./...`, `bnk up --var-file=./...`, `plan` / `apply` / `down` with the same flag, all now resolve relative `--var-file` paths against the user's shell CWD (the directory they invoked `roksbnkctl` from), matching terraform's own `-var-file=./...` semantics. Prior to v1.4.1 the value was passed verbatim to terraform; terraform's CWD is the per-phase state dir (`~/.roksbnkctl/<workspace>/state[-cluster]/`), so a relative path resolved there and produced `Failed to read variables file. Given variables file ./<path> does not exist.` Absolute paths continue to work unchanged. The pre-flight error message when the resolved file is missing now names both the user-supplied path and the absolute path it resolved to, so typos and wrong-CWD invocations are distinguishable. The docker backend's prior absolute-only requirement (introduced in v1.0.x because docker bind-mounts need absolute host paths) is now redundant for the common case — every reachable `--var-file` is absolute by the time it reaches the backend dispatch — and remains in place as a defensive guard. Implementation lands in `internal/cli/` via a small `resolveVarFiles` helper called at each `--var-file`-consuming command's `RunE` entry point.
- **`--tf-source` relative local paths are now resolved to absolute before being persisted** ([`issues/issue_sprint12_validator.md` Issue 5](issues/issue_sprint12_validator.md)) — `roksbnkctl init --tf-source=./mytf` (and `up --tf-source=./...`) with a relative local-directory value now records an absolute path in the workspace's `config.yaml`, so the source still resolves on a later `up` / `plan` / `apply` run regardless of the directory those commands are invoked from. Prior to v1.4.1 the relative value passed the existence check at `init` time (checked against the shell CWD) but was stored verbatim; a subsequent lifecycle command handed it to terraform, whose CWD is the per-phase state dir, so the source directory could no longer be found — the same shell-CWD-vs-state-dir trap as the `--var-file` case, but worse because it survived into `config.yaml` and detonated on a *later* run rather than the same invocation. Absolute `--tf-source` paths, and the URL / GitHub source forms, are unchanged. This fix was pulled forward from the Sprint 13 backlog per integrator decision so v1.4.1 closes both siblings of the path-resolution trap together.

### Deferred (v1.x roadmap, post-v1.4.1)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). The carry list is unchanged from v1.4.0:

- **`ops install` / `ops uninstall` snapshot** ([PRD 07 §"Open questions" item 1](docs/prd/07-DEPLOYED-TFVARS.md#open-questions)) — carry-forward from v1.4.0.
- All prior-cycle deferred items from [v1.4.0 §"Deferred"](#deferred-v1x-roadmap-post-v140) and [v1.3.0 §"Deferred"](#deferred-v1x-roadmap-post-v130) remain deferred.

## v1.4.0 — 2026-05-14

Sprint 11 closure cycle. Lands PRD 07's `terraform.applied.tfvars` snapshot per workspace phase — after every successful `terraform apply`, `roksbnkctl` writes a canonical-HCL var-file capturing the effective inputs that produced the current state. Re-create / audit / handoff scenarios that previously required `config.yaml` or memory now become file-driven: the snapshot is on disk at a predictable path, mode `0600`, with `ibmcloud_api_key` redacted and every other variable verbatim. The file is never read back by `roksbnkctl` (it's an output, not an input); `cluster down` / `bnk down` leave it in place so the prior `up`'s snapshot stays available for re-apply or audit. See [PRD 07](docs/prd/07-DEPLOYED-TFVARS.md) for the design rationale and [PLAN.md §"Sprint 11"](docs/PLAN.md) for the cycle's deliverables.

### Added

- **`terraform.applied.tfvars` snapshot per workspace phase** ([PRD 07](docs/prd/07-DEPLOYED-TFVARS.md)) — after every successful `terraform apply`, the effective var-file inputs land at `~/.roksbnkctl/<workspace>/state-cluster/terraform.applied.tfvars` (cluster phase) or `~/.roksbnkctl/<workspace>/state/terraform.applied.tfvars` (trial phase, and the union file on `ShapeLegacySingle`). Canonical HCL — one assignment per line, alphabetic within each source section, source-attribution comments for `config.yaml`-derived vars / `terraform.tfvars.user` / cluster-phase override. `ibmcloud_api_key` is rendered as `<redacted>`; every other variable is verbatim. File mode is `0600`. The file is **not** read back by `roksbnkctl` — it's an output for the user (re-create / audit / handoff workflows), never an input the tool depends on. Implementation lands in `internal/config/applied_tfvars.go` (`WriteAppliedTFVars`) and hooks into `internal/tf/terraform.go::Workspace.Apply` after a successful apply (log-and-continue on write failure — the apply's exit code reflects the apply, not the snapshot's bookkeeping). See [Chapter 6 §"`terraform.applied.tfvars` — what's deployed right now"](book/src/06-workspaces.md) for the user-facing description with a worked example.

### Deferred (v1.x roadmap, post-v1.4.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). Not in v1.4.0:

- **`ops install` / `ops uninstall` snapshot** ([PRD 07 §"Open questions" item 1](docs/prd/07-DEPLOYED-TFVARS.md#open-questions)) — `ops install` and `ops uninstall` change cluster-side state (Kubernetes objects, IAM trusted profile bindings) but don't run Terraform, so the tfvars-shaped snapshot doesn't apply. A future cycle may add a parallel record (SA annotations, Secret state, the `--trusted-profile=…` value used). File a follow-up PRD if there's user demand.
- All prior-cycle deferred items from [v1.3.0 §"Deferred"](#deferred-v1x-roadmap-post-v130) remain deferred.

## v1.3.0 — 2026-05-14

Sprint 10 closure cycle. Closes the runtime side of PRD 04's trusted-profile flow (the in-pod `ibmcloud login` wrap Sprint 9 deferred), lands PRD 06's `roksbnkctl status` per-phase integration (Sprint 10 scope addition), and folds four of the five tech-writer polish issues deferred from Sprint 9 (the fifth — chapter 14 §"What's new in v1.2" section position — is deferred again as a v1.x polish item; see `### Deferred` below). The headline reframe: `roksbnkctl ops install --trusted-profile=auto` followed by `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` now returns a fresh IAM token end-to-end — the v1.2.x partial-closure callout in chapter 19 comes out. See [PLAN.md §"Sprint 10"](docs/PLAN.md) for cycle deliverables and [PRD 04 §"Resolved in Sprint 9"](docs/prd/04-CREDENTIALS.md#resolved-in-sprint-9) + [PRD 06 §"`status` command integration"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#status-command-integration-sprint-10-scope-addition) for the design surface.

### Added

- **`roksbnkctl status` per-phase deployment** ([PRD 06 §"`status` command integration"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#status-command-integration-sprint-10-scope-addition)) — `runStatus` consumes `config.DetectShape` and each phase's `terraform.tfstate` mtime, emitting two per-phase deployment lines instead of the v1.0.x single `Last apply` line that conflated the cluster and trial phases. Output by shape: `ShapeEmpty` reports both phases as `not deployed`; `ShapeClusterOnly` reports the cluster phase as `deployed (last apply <ts>)` and the trial as `not deployed`; `ShapeSplit` reports both phases independently; `ShapeLegacySingle` preserves the v1.0.x `Last apply:` line verbatim plus a `Shape:` callout so the reader sees they're on a legacy workspace at a glance. See [Chapter 24 §"`roksbnkctl status`"](book/src/24-day-2-ops.md) for per-shape samples. Implementation lands in [`internal/cli/inspect.go`](internal/cli/inspect.go) with a four-shape table test against the `internal/config/testdata/` fixtures from Sprint 8.

### Changed

- **In-pod `ibmcloud login` wrap is now trusted-profile-aware** ([PRD 04 §"Resolved in Sprint 9" carry-over](docs/prd/04-CREDENTIALS.md#trusted-profile-auto-provisioning-k8s-backend); closes the v1.2.x partial-closure documented in [v1.2.0 §"Deferred"](#deferred-v1x-roadmap-post-v120)) — `runOnOpsPod`'s ibmcloud login wrap detects whether the ops pod's ServiceAccount carries the `iam.cloud.ibm.com/trusted-profile` annotation. Trusted-profile-annotated pods run `ibmcloud login -a https://cloud.ibm.com --cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}" --quiet`; the `--cr-token @<path>` form reads a projected SA token (audience `iam`, mounted at the cited path) and IBM IAM validates that JWT against the trusted profile's `ROKS_SA` compute-resource link. Static-key pods continue the v1.0.x `--apikey "$IBMCLOUD_API_KEY"` path. The `IAM_PROFILE_ID` env var and the projected SA-token volume are injected into the pod spec at `ops install` time via the manifest renderer when the trusted profile is provisioned (`internal/cli/ops.go`, `internal/exec/k8s_install.yaml`). Under `--trusted-profile=auto` success, `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` now returns a fresh IAM token end-to-end; the static API key never transits the pod env. The first invocation may take 30–60 seconds while IBM IAM picks up the cluster's OIDC issuer URL; the wrap includes a brief retry to absorb this propagation window.
- **`roksbnkctl status` output for non-Legacy workspaces** replaces the v1.0.x single `Last apply` line with per-phase `Cluster phase:` / `BNK trial:` lines. Scripts that parse `Last apply` continue to work on `ShapeLegacySingle` workspaces (where the line is preserved verbatim) but will need to switch to the per-phase lines on `ShapeEmpty`, `ShapeClusterOnly`, and `ShapeSplit` workspaces. Anyone on a non-legacy workspace running such a script was already affected by Sprint 8's phase split (the v1.1+ `Last apply` line, when emitted at all, would have been the trial-only mtime, not the cluster's) — this release makes the change visible rather than silently misleading.
- **`make release` now runs `-tags integration` tests against an ephemeral kind cluster** ([PLAN.md §"Sprint 10 → Code deliverable 3"](docs/PLAN.md)) — closes the v1.2.0 → v1.2.1 cascade gap where the local pre-tag gate compile-checked the integration-tagged code but didn't execute it. New [`scripts/integration-test.sh`](scripts/integration-test.sh) brings up a kind cluster, runs `go test -tags integration` for `internal/exec/...` + `internal/remote/...`, tears down on exit. Contributors without `kind` installed see a warning + confirmation prompt instead of a hard fail (so a doc-only change on a workstation without kind isn't blocked); `SKIP_INTEGRATION_TEST=1` bypasses explicitly. The `Makefile` step also detects a missing or unreachable docker daemon and aborts with a remediation hint. See `make integration-test` for the standalone invocation.

### Fixed

- **In-pod `ibmcloud login` wrap closure** — closes the [v1.2.0 §"Deferred"](#deferred-v1x-roadmap-post-v120) "In-pod `ibmcloud login` wrap for the trusted-profile path (Sprint 10)" bullet. The v1.2.x partial-closure admonition in chapter 19 §"Trusted-profile flow (v1.2+)" is gone; the documented behavior matches the binary end-to-end. Staff Issue 2 from Sprint 9 resolved.
- **Chapter 19 `ops show` shape under `--trusted-profile=auto`** (Sprint 9 tech-writer Issue 4) — the section now documents the two-line `trusted-profile:` + `secret:` shape that `runOpsShow` actually emits, with both static-key and trusted-profile cases called out.
- **Chapter 19 `<workspace>` vs `sandbox-roks` placeholder consistency** (Sprint 9 tech-writer Issue 13) — all concrete sample names standardized on `canada-roks` (matching the v1.1 release-notes / Chapter 9 workspace convention); abstract `<workspace>` reserved for prose generalizations.
- **Chapter 19 §"Credential propagation" v1.2 callout placement** (Sprint 9 tech-writer Issue 9) — step 4 ("Create or update the credential Secret") in §"`roksbnkctl ops install`" now opens with a `v1.2+ note` mirroring the existing one in §"Credential propagation"; readers skimming the install walkthrough see the trusted-profile cross-link without having to scroll past §"Wait for readiness".
- **Chapter 14 "warning block" → "warning line" wording** (Sprint 9 tech-writer Issue 7) — the §"Compatibility note" paragraph now says "one extra stderr warning line", matching the single-line shape of all three fallback warnings in `internal/cli/ops.go`.

### Deferred (v1.x roadmap, post-v1.3.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). Not in v1.3.0:

- **Workspace-config customisation of trusted-profile policies** — v1.2+ ships with no default policies attached; a future cycle will surface `ibmcloud.trusted_profile.policies` as a workspace-config block.
- **Trusted-profile path for the SSH backend** — out of scope; the SSH backend's cred model (SetEnv + wrapper-script fallback) doesn't have a projected SA token to trade.
- **`--trusted-profile` flag on `roksbnkctl up` / `cluster up`** — out of scope; the terraform-driven lifecycle commands still use the workspace's resolved API key directly for HCL provider auth.
- **Long-running ops pod with kubeconfig refresh on token rotation** — the trusted-profile path makes this mostly moot (IAM tokens are short-lived and refreshed by the SDK transparently), but a multi-day pod lifetime against a projected SA token whose own rotation cadence shifts is on the v1.x watch-list.
- **Chapter 14 §"What's new in v1.2" section position** (Sprint 9 tech-writer Issue 8) — restructuring would extensively reshape an otherwise version-agnostic chapter; deferred as a v1.x polish item.
- **Chapter 19 §"5. Create the Pod" YAML — `env:` block** (Sprint 10 tech-writer Issue 14) — the pod-spec sample doesn't show the actual `env:` block (`HOME: /tmp` since v1.2.1, conditional `IAM_PROFILE_ID` under `--trusted-profile=auto|on` success). The prose at the same section's "What just happened, in order" step 5 mentions `IAM_PROFILE_ID` so the reader gets the concept; expanding the YAML to show the conditional shape requires either two side-by-side samples or a comment-annotated conditional block, neither of which is local. Deferred to a v1.4 chapter-polish pass.

## v1.2.1 — 2026-05-13

CI-recovery patch on top of `v1.2.0`. The `v1.2.0` cut passed the now-extended local pre-tag gate (build / vet / fmt / test / staticcheck / `-tags integration` build) but CI runs `go test -tags integration` against a live kind cluster, which surfaced an image-level gap the local gate doesn't exercise: the Sprint 9 image switch on `TestIntegration_K8sBackend_JobMode_Echo` (busybox → tools-ibmcloud, adding `USER 1000` for `RunAsNonRoot` admission) left uid 1000 without a writable `$HOME`. The ibmcloud CLI's first-run config write to `$HOME/.bluemix/` failed with `Configuration error: mkdir /.bluemix: permission denied` even for `ibmcloud --version`. Functionally identical to `v1.2.0` for the release binaries (the failure was in the tools image used by integration tests; goreleaser-built end-user binaries are unaffected). **End users should install v1.2.1**; the `v1.2.0` Release page is retained as a historical artifact only.

### Fixed (CI recovery)

- **`tools/docker/ibmcloud/Dockerfile`**: provision `/home/runner` owned by uid 1000 before the `USER 1000` drop; `ENV HOME=/home/runner`; `WORKDIR /home/runner`. ibmcloud's first-run config dir creation now lands at `/home/runner/.bluemix/` (writable) instead of `/.bluemix/` (root-only). The Build tools images workflow republishes `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v1.2.1` automatically on tag push; `TestIntegration_K8sBackend_JobMode_Echo` passes against the rebuilt image.
- **Local pre-tag gate gap noted**: `make release` runs `go build -tags integration ./...` (compile check) but not `go test -tags integration` (which requires a kind cluster + docker daemon). Adding a kind-bringup step to the local gate is non-trivial and not yet planned; the CI-side gate continues to catch image-level integration gaps that the local gate can't. Tracked as a Sprint 10 candidate.

## v1.2.0 — 2026-05-13 — SUPERSEDED by v1.2.1

Intended as the headline Sprint 9 release (PRD 04 cred-passing closure + CI polish) but the local pre-tag gate passed while CI's live integration-test job surfaced a tools-image gap. See `v1.2.1` above for the corrected cut. v1.2.0 release binaries are functionally identical to v1.2.1; only the tools image used by `--backend k8s` integration tests differs.

Sprint 9 closure cycle — closes the two PRD 04 §"Open questions" items that have been open since the v0.9 cycle (the cred-tmpfile-bind-mount pattern for the docker backend, and the trusted-profile auto-provisioning for the k8s backend), plus the CI / Makefile polish that prevents another v1.1.0 → v1.1.1 → v1.1.2 cascade. The headline reframe: from v1.0.x-style "static API key in env / Secret" to "no static API key on the wire when it can be avoided". Both backends get sane fallbacks for environments where the new pattern doesn't apply. See [PRD 04 §"Resolved in Sprint 9"](docs/prd/04-CREDENTIALS.md#resolved-in-sprint-9) for the design rationale and [PLAN.md §"Sprint 9"](docs/PLAN.md) for the cycle's deliverables.

### Added

#### Sprint 9 — PRD 04 closure (cred tmpfile + trusted profile) + CI polish

- **Cred tmpfile-bind-mount pattern for the docker backend** ([PRD 04 §"Resolved in Sprint 9" → "Cred tmpfile-bind-mount pattern (docker backend)"](docs/prd/04-CREDENTIALS.md#cred-tmpfile-bind-mount-pattern-docker-backend))
  - The resolved `IBMCLOUD_API_KEY` is written to a per-run `0600` tempfile under `$TMPDIR/roksbnkctl-creds-<rand>/api-key`, bind-mounted read-only at `/run/secrets/ibmcloud_api_key` in the container.
  - Container env carries only `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key`; the legacy `IBMCLOUD_API_KEY=<value>` form is gone. `docker inspect <id>` shows the path and the bind-mount entry, never the key value.
  - Container command is wrapped in `sh -c 'export IBMCLOUD_API_KEY="$(cat "$IBMCLOUD_API_KEY_FILE")" && exec …'` so tools that read from env (the existing `dockerImageBinary["ibmcloud"]` login wrap, terraform's IBM provider, ad-hoc `ibmcloud` invocations) continue to find the value at process-spawn time.
  - Tempfile cleanup runs via `defer` on backend `Run` exit, with a `context.AfterFunc` backstop so interrupted runs still scrub the file. Long-running invocations (e.g., `roksbnkctl up --backend docker` with a 30-min terraform apply) hold the file open via the bind mount for the duration; cleanup fires after the container exits.
  - Closes the v1.0.x → v1.1.0 trade-off documented at [`internal/exec/docker.go`](internal/exec/docker.go) `buildMountsAndEnv` and unblocks `TestIntegration_DockerBackend_NoLeakInInspect` (was `t.Skip`'d on commit `776fe56`).
  - Sample (no flag change required — the pattern is the default for `--backend docker` on v1.2 and up):
    ```bash
    roksbnkctl --backend docker ibmcloud iam oauth-tokens
    # docker inspect on the spawned container shows IBMCLOUD_API_KEY_FILE only, never the value
    ```
- **Trusted-profile auto-provisioning for the k8s backend** ([PRD 04 §"Resolved in Sprint 9" → "Trusted-profile auto-provisioning (k8s backend)"](docs/prd/04-CREDENTIALS.md#trusted-profile-auto-provisioning-k8s-backend); closes PRD 04 §"Implementation tasks" task 8)
  - New `--trusted-profile=auto|on|off` flag on `roksbnkctl ops install`, default `auto`, validated at flag-parse time.
  - `auto`: probe the resolved API key for IAM `iam-identity` perms; on present, provision `roksbnkctl-ops-<workspace>` trusted profile linked to the ops pod's ServiceAccount via its projected SA token. On perm-missing (`403` from the IAM probe), fall back to the v1.0.x static-key Secret with a single stderr warning line naming the missing perm and how to silence (`--trusted-profile=off`).
  - `on`: try to provision, fail loudly on perm-missing with a non-zero exit. For CI / hardened environments where the static-key path is unacceptable.
  - `off`: skip the trusted-profile path; provision the v1.0.x static-key Secret. Compatibility / debugging / air-gapped clusters.
  - Profile name is namespaced per workspace (`roksbnkctl-ops-<workspace>`) so multiple workspaces against the same IBM Cloud account don't race for a single shared name.
  - ServiceAccount carries `iam.cloud.ibm.com/trusted-profile: <name>` (the IBM IAM CSI hook reads this) plus `roksbnkctl.io/trusted-profile-managed: "true"` (signals `ops uninstall --confirm` to delete the profile on teardown — best-effort, with a warning line if IAM perms have since changed).
  - New `internal/ibm/trusted_profile.go` package wraps the IBM IAM Identity SDK calls (`CreateProfile`, `CreateClaimRule`, `CreatePolicy`, `DeleteProfile`); reusable for future trusted-profile use cases beyond the ops pod.
  - Implementation lands in [`internal/exec/k8s.go`](internal/exec/k8s.go) (`installOpsPod` branch on flag value), [`internal/cli/ops.go`](internal/cli/ops.go) (cobra flag wiring + validation), and the new [`internal/ibm/trusted_profile.go`](internal/ibm/trusted_profile.go).
  - Sample:
    ```bash
    roksbnkctl ops install --trusted-profile=auto    # default; auto-falls-back
    roksbnkctl ops install --trusted-profile=on      # CI / fail-loud on perm-missing
    roksbnkctl ops install --trusted-profile=off     # v1.0.x static-key path
    ```
- **Book chapter edits** for the new surface:
  - **Chapter 14 (`Credentials and the resolver chain`)** — new §"What's new in v1.2: the cred-tmpfile and trusted-profile paths" with the one-paragraph docker pattern explainer + the three-row `--trusted-profile` flag table + compatibility note.
  - **Chapter 19 (`The in-cluster ops pod`)** — new §"Trusted-profile flow (v1.2+)" with the `ops install --trusted-profile=auto` sample output, the SA verification command, the auto-fallback warning shape, the `--trusted-profile=off` opt-out path, and the `ops uninstall` trusted-profile cleanup behaviour. Existing §"Credential propagation" + §"Rotation" sections gain v1.2+ pointer notes so they're not stale.

### Changed

- **`--backend docker` cred propagation** — the v1.0.x bare-name `Env: ["IBMCLOUD_API_KEY"]` form and the v1.1.0 explicit `IBMCLOUD_API_KEY=<value>` form are both gone. The container's env carries only `IBMCLOUD_API_KEY_FILE` (pointing at the bind-mounted tempfile); the value reaches tools that read from env via a `sh -c export …` shim. `docker inspect` is now clean per [PRD 04 §"Anti-patterns to avoid"](docs/prd/04-CREDENTIALS.md#docker-container) item 1. The user-facing invariant — set `IBMCLOUD_API_KEY` in your shell or workspace config and `roksbnkctl --backend docker` works — is unchanged.
- **`roksbnkctl ops install` defaults to `--trusted-profile=auto`** — previously the install always provisioned the v1.0.x static-key Secret with no trusted-profile path. Workspaces whose API key has IAM `iam-identity` perms now get the trusted-profile path transparently on first `ops install` after the upgrade; the static-key Secret is replaced. Workspaces whose key lacks the perms see one new warning line per `ops install` and otherwise continue to work as in v1.0.x.

### Fixed

- **`TestIntegration_DockerBackend_NoLeakInInspect`** re-enabled — the `t.Skip` marker landed on commit `776fe56` is removed. The test asserts that a known `IBMCLOUD_API_KEY` value never appears in `docker inspect` output for a container spawned by `--backend docker`. Closed by the cred tmpfile-bind-mount pattern (this release's headline cred work).
- **`TestIntegration_K8sBackend_JobMode_Echo`** re-enabled — the `t.Skip` marker landed on commit `776fe56` is removed. The Job-mode echo test now runs against `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>` (already runs as uid 1000) instead of `busybox:1.36` (default USER root, which collided with `runAsJob`'s `RunAsNonRoot: true` SecurityContext). Picked option 1 from the test body's two-options TODO so the production `runAsJob` SecurityContext stays unchanged.
- **`TESTCONTAINERS_RYUK_DISABLED=true`** in the CI integration job — kills the docker-hub `testcontainers/ryuk` pull that produced the intermittent `429 too many requests` flake on `TestIntegration_Connect_Whoami` (the v1.1.2 §"Not fixed" carry-over). Ephemeral CI runners don't need the testcontainers reaper. Implementation lands in [`.github/workflows/ci.yml`](.github/workflows/ci.yml).
- **`Makefile` pre-tag checklist** (the v1.1.2 carry-over) — the `release` target now runs `staticcheck ./...` AND `go build -tags integration ./...` AND the default-build sweep, closing the three-configuration gap that produced the v1.1.0 → v1.1.1 → v1.1.2 cascade. The new gate matches what CI runs and surfaces all three build configs' failures locally before the tag goes out.

### Deferred (v1.x roadmap, post-v1.2.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). Not in v1.2.0:

- **In-pod `ibmcloud login` wrap for the trusted-profile path** (Sprint 10) — Sprint 9 lands the **provisioning** side of `--trusted-profile=auto` (profile creation, SA annotation, manifest rendering with empty Secret data when auto-success). The existing `runOnOpsPod` login wrap at [`internal/exec/k8s.go`](internal/exec/k8s.go) still does `ibmcloud login --apikey "$IBMCLOUD_API_KEY"` regardless of mode, so under `auto`-success the wrap fails with `missing API key` when stateful `ibmcloud` subcommands actually run inside the pod (the Secret data is empty by design). Sprint 10 ships the conditional wrap (`ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"` when the SA carries `iam.cloud.ibm.com/trusted-profile`, with `IAM_PROFILE_ID` injected into the pod spec at install time). Tracked in [staff Issue 2](issues/issue_sprint9_staff.md). The v1.2.0 security-side win is real but **partial** — no static API key sits at rest in any Kubernetes Secret in etcd under `auto`-success; the runtime cred flow still uses static-key. Pass `--trusted-profile=off` if you need the runtime wrap to actually work today.
- **Workspace-config customisation of trusted-profile policies** — v1.2 ships with minimal defaults (Viewer on container-registry, Operator on cloud-object-storage). A future cycle will surface `ibmcloud.trusted_profile.policies` as a workspace-config block so users can layer custom IAM policies onto the provisioned profile.
- **Trusted-profile path for the SSH backend** — out of scope; the SSH backend ships its own cred-passing model (SetEnv + wrapper-script fallback) and the trusted-profile path requires a projected k8s SA token, which the SSH-target side doesn't have.
- **`--trusted-profile` flag on `roksbnkctl up` / `cluster up`** — out of scope; the terraform-driven lifecycle commands still use the workspace's resolved API key directly for HCL provider auth. The trusted-profile path is exclusively for the ops pod.

## v0.9.0 — 2026-05-10 (M3 milestone)

The four-backend, GSLB-validation, in-cluster-ops release. Cumulative surface across Sprints 3–5.

### Added

#### Sprint 5 — DNS probe + terraform docker (v0.9 gate sprint)

- **GSLB-aware DNS probe** (`roksbnkctl test dns`)
  - `miekg/dns`-based `Probe` (replaces the std-lib `net.Resolver` impl) with full record-type coverage (A / AAAA / CNAME / MX / NS / TXT / SRV / SOA / PTR / CAA / DS / DNSKEY / ANY plus everything else `dns.StringToType` accepts)
  - New flags: `--target`, `--type`, `--server`, `--iterations`, `--timeout`, `--gslb-compare`, `--require-divergence`
  - Server resolution: literal `<ip>[:<port>]`, `system` (host `/etc/resolv.conf`), `cluster` (in-pod CoreDNS, k8s-backend only), or named-from-workspace-config (`test.dns.resolvers`)
  - RTT distribution (`p50`/`p95`/`p99`) when `--iterations > 1`
  - JSON output: `roksbnkctl.dns.v1.vantage` (single-vantage) and `roksbnkctl.dns.v1` (`--gslb-compare`)
  - `--gslb-compare` fans the probe across `local` + `k8s` (when a kubeconfig is reachable) + every `ssh:<target>` registered in workspace targets; emits `gslb_divergence` boolean
  - `--require-divergence` flips the exit code when no divergence is observed (CI assertion that GSLB is doing something)
  - In-cluster path runs as a one-shot Job re-execing the bundled tools image (no separate `roksbnkctl-cli` image)
  - Workspace config: new `test.dns.resolvers` (named resolver map) and `test.dns.default_target` fields
- **Terraform via docker** (`roksbnkctl up/plan/apply/down --backend docker`)
  - `hashicorp/terraform:1.5.7` pinned upstream image
  - Workspace state directory bind-mounted at `/state` (read-write); embedded HCL materialised under `/state/tf-source/<source>/`
  - `--user $(id -u):$(id -g)` keeps state-file ownership aligned with the host user (Linux/WSL2; macOS Docker Desktop transparent)
  - `--backend k8s` and `--backend ssh:<target>` for terraform deferred to v1.x with a clear error pointing at PRD 03 §"State concerns"
- **Doctor extensions** (`roksbnkctl doctor`)
  - DNS-probe sanity check (when workspace has `test.dns.default_target`)
  - K8s ops-pod env runtime probe (`kubectl exec -- printenv`, value redacted in output)
  - Cred rotation freshness warning when the Secret's `roksbnkctl.io/rotated-at` annotation is more than 30 days old
- **Book chapters**: 20 (Connectivity testing), 21 (DNS testing for GSLB — flagship), 22 (Throughput testing); chapter 17 expanded with terraform-via-docker subsection

#### Sprint 4 — k8s + SSH backends, in-cluster ops pod

- **`--backend k8s`** (`internal/exec/k8s.go`)
  - Long-lived ops pod path for ad-hoc commands (`ibmcloud`, future interactive shells); SPDY-channel `kubectl exec` with redactor-wrapped stdout/stderr
  - One-shot Job path for ephemeral tools (iperf3 client, future probes); `ttlSecondsAfterFinished: 60` auto-cleanup; logs streamed via `client-go`
  - `roksbnkctl ops install/show/uninstall` — install/inspect/teardown of namespaces, ServiceAccount, ClusterRole, ClusterRoleBinding, Secret, Pod
  - Embedded RBAC manifests (`internal/exec/k8s_install.yaml`) — least-privilege ClusterRole with `resourceNames`-restricted `secrets/get`
- **`--backend ssh:<target>`** (`internal/exec/ssh.go`)
  - File materialisation to `/tmp/roksbnkctl.<rand>/` on the remote with `trap … EXIT` cleanup
  - Env propagation: SetEnv (preferred, requires sshd `AcceptEnv`) → wrapper-script-with-trap fallback (silent `set +x` source from a 0700 env-file)
  - Per-tool apt-bootstrap behind `--bootstrap` opt-in (Ubuntu only); 126/127 split for sudo / non-Ubuntu / repo-unreachable failures
  - Doctor `--backend k8s` / `--backend ssh:<target>` checks
- **iperf3 SCC fix** for OpenShift `restricted-v2` (`runAsNonRoot`, `runAsUser: 1000`, `seccompProfile: RuntimeDefault`, `capabilities.drop: [ALL]`)
- **Per-tool default backend map**: iperf3 → `k8s`, ibmcloud → `local`, terraform → `local`
- **126/127 backend-failure split** — `127` for "couldn't start" (daemon down, target unreachable), `126` for "started then failed" (container OOMKilled, ssh session died mid-run)
- **Book chapters**: 17 (Execution backends — full deep-dive), 18 (Choosing a backend per tool), 19 (The in-cluster ops pod)

#### Sprint 3 — credential abstraction + first backends

- **`internal/cred.Resolver`** — single-source-of-truth API key resolution chain (env → keychain → config-b64 → prompt)
- **`internal/exec.Backend` interface** + `RunOpts` + `Credentials` shared shape across all backends
- **`--backend local`** + **`--backend docker`** — first two backends; `--backend` persistent root flag wins over workspace-config default
- **Output stream redactor** (`internal/exec/redact.go`) — wraps `io.Writer` to mask the IBM API key value if it ever appears in stream content; defense-in-depth across all backends
- **Vendored tool images** — `ghcr.io/jgruberf5/roksbnkctl-tools-{ibmcloud,iperf3}:<v>`; tag pinned to the binary's `internal/version.Version` value at runtime (release tag → matching image tag)
- **Workspace config `exec:` block** — per-tool default backend selection
- **`tools-images.yml` GitHub Actions workflow** — builds + pushes the tools images on tag (Sprint 5 added `:dev` push on `main` for `go install ./cmd/roksbnkctl@main` UX)
- **Book chapters**: 12 (Workspace config), 13 (Terraform variables), 14 (Credentials and the resolver chain), 15 (SSH targets), 17 intro (Execution backends)

### Changed

- **`hashicorp/terraform:1.5.7`** is the literal pin for the terraform docker backend (not version-resolved like the per-tool tools images)
- **DNS probe schema strings** are now namespaced: `roksbnkctl.dns.v1.vantage` for single-vantage, `roksbnkctl.dns.v1` for multi-vantage `--gslb-compare`
- **`tools/docker/iperf3/Dockerfile`** ships `USER 1000` so the bundled image satisfies `runAsNonRoot: true` policies on plain k8s clusters
- **K8s Job names** now sanitise docker-style argv[0] image refs (colons / slashes / `@`) so the test fallback path doesn't trip k8s label-validation regex

### Deferred (post-v1.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). High-water-mark:

- terraform `--backend k8s` and `--backend ssh:<target>` (state-handling design open; v1.x)
- SSH backend `apt-get` bootstrap on RHEL/CentOS/Alpine (Ubuntu-only in v0.9)
- Native Windows Docker Desktop UID/GID handling for terraform-via-docker

### Documentation

The book at <https://jgruberf5.github.io/roksbnkctl/book/> covered the v0.9 surface in **22 published chapters**: 0 (Preface) through 22 (Throughput testing). Sprint 6 landed chapters 23-32 (E2E plan, COS supply chain, troubleshooting, command + config reference, glossary, building from source); Sprint 7 launched the polished book alongside the v1.0 tag.

Per-PRD design rationale (cred propagation, execution backends, kubectl internalisation, etc.) lives under [`docs/prd/`](docs/prd/).

## v1.0.0 — 2026-05-11 (M4 milestone)

The first stable release. roksbnkctl bundles seven sprints of work (M1 → M4) into a single-binary CLI: a 4-command lifecycle (`init` → `up` → `test` → `down`), four execution backends (`local` / `docker` / `k8s` / `ssh:<target>`), a GSLB-aware DNS probe, terraform-via-docker, an in-cluster ops pod, and a full kubectl-internalised cluster-ops surface — all in one statically linked binary with terraform as the only required host install. The published book at <https://jgruberf5.github.io/roksbnkctl/book/> ships alongside the binary as the canonical user documentation.

Milestone history: **v0.7** (M1) landed `--on jumphost` for customer-firewalled environments. **v0.8** (M2) internalised kubectl + oc via client-go. **v0.9** (M3) added the four-backend matrix, the GSLB-aware DNS probe, and terraform-via-docker. **v1.0** (M4) closes out with full E2E coverage, doctor green-by-default on a stock dev box with only `terraform` installed, the polished book launch, and the release artifacts (signed binaries deferred to v1.x — see Deferred below).

### Added

#### Sprint 7 — book launch + v1.0 release artifacts

- **Book published** at <https://jgruberf5.github.io/roksbnkctl/book/> — _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_. 32 chapters + preface + worked-example walkthroughs in each Part, Mermaid diagrams for architecture / lifecycle / GSLB cross-vantage / execution-backend matrix, foreword/preface rewrite, every code example re-verified in a fresh workspace. Dogfooded by ≥1 external user against a real IBM Cloud account before the tag cut (per PLAN.md §"v1.0 (M4)" gate).
- **`roksbnkctl --version` / `roksbnkctl version`** now emits a second line `Docs: https://jgruberf5.github.io/roksbnkctl/book/` pointing at the canonical user-documentation surface. The first line ("`roksbnkctl <ver> (commit <c>, built <d>)`") is byte-identical to the pre-v1.0 shape so scripts that grep on it continue to parse. The shape is pinned by `internal/cli/meta_test.go::TestVersionCmd_OutputShape`. Constant of record: `internal/cli/meta.go::DocsURL`.
- **GitHub Release artifacts** — Linux / macOS / Windows × amd64 / arm64 archives + `checksums.txt` + offline **`roksbnkctl-book-v1.0.0.pdf`** (the same book that ships at GitHub Pages, packaged for offline reading via mdbook-pandoc + XeLaTeX). The release page header links at the book and the footer at `CHANGELOG.md`. Archives now include `LICENSE`, `README.md`, `CHANGELOG.md`, and `MIGRATING.md` alongside the binary so the downloaded tarball is self-contained.
- **PDF release pipeline** — `make release` from the repo root drives a docker-containerised build (via `tools/docker/mdbook/Dockerfile` — bundles mdbook + mdbook-mermaid + mdbook-pandoc + pandoc + texlive-xetex + mermaid-cli) that produces both the HTML (for GitHub Pages) and the PDF (for the GitHub Release page) in one shot. Mermaid diagrams pre-render to SVG via mermaid-cli so the PDF embeds real diagrams rather than literal source text. Local dev iteration on HTML stays lightweight via `make book` + `make book-serve` (host install, no docker required).
- **README rewritten** for the v1.0 narrative — single-line status, terraform-only prereq table, install options (go install / pre-built binary / from-source / self-update), pointer block to the book + CHANGELOG + MIGRATING + PLAN + per-PRD design rationale. Trimmed from 700+ lines to ~90; the book is the canonical documentation surface.

#### Sprint 6 — testing build-out + reference chapter coverage

- **Full e2e Phases I + M + N** — `scripts/e2e-test-backends.sh` expanded with Phase I (SSH backend, 12 steps I0-I11), full Phase M (cred audit including the SSH-side M5/M6 steps), and Phase N (mixed-mode lifecycle N1-N6). LD9 (SSH vantage for DNS probe) wired alongside.
- **`scripts/e2e-test-full.sh`** — combined A-H + I-N + L-DNS runner (~4-6 hour wall time); designed for release branches + manual-trigger CI.
- **`.github/workflows/e2e-full.yml`** — manual-trigger + release-branch CI workflow for the combined runner.
- **`TestProbe_TruncatedFlag`** — dual-stack UDP+TCP mock server pins the TC=1 projection through the TCP retry path (closes Sprint 5 validator Issue 4).
- **`tools/refgen/cobra-md`** + **`tools/refgen/tfvars-md`** — Go-based auto-generators for chapters 27 (Command reference) and 29 (Terraform variable reference). Re-run on every CLI / variables.tf change.
- **`MIGRATING.md`** — top-level migration guide for users coming from v0.6.x `bnkctl` or from manual BNK deployments.
- **`internal/cred/resolver_invariance_test.go`** — pins the cred-resolver contract across all four backends (Phase N Go-side contract).
- **`internal/doctor/doctor_test.go`** — pins the green-by-default contract.
- **EDNS Client Subnet surfacing** — `DNSProbeResult.EDNSClientSubnet` is populated from the resolver's RFC 7871 echo (when present); `omitempty` so non-ECS resolvers don't pollute the JSON.
- **Book chapters 23, 25, 26, 28, 30, 31, 32** — hand-written reference / troubleshooting / glossary; chapters 27 and 29 auto-generated.

### Changed

- **`roksbnkctl doctor`** is **green-by-default on a stock dev box with only `terraform` installed**. The historical checks for `kubectl`, `oc`, `ibmcloud`, `iperf3`, and `dig` are now **informational** rather than warnings/errors — the binary has internalised those surfaces (chapter 2 / chapter 17 for backends; chapter 21 for DNS). Exit code semantic (0 on green / 1 on red) unchanged.
- **`tools/docker/ibmcloud/Dockerfile`** dropped `ENTRYPOINT ["ibmcloud"]`. The docker backend's dispatch layer now prepends the tool binary name explicitly via a new `dockerImageBinary` map; the k8s `jobToolCmdOverride` map mirrors it. Sprint 5's `jobToolCmdOverride` shim for `roksbnkctl` self-exec dns-probe is now unnecessary — the cross-backend invariant is pinned in `TestDockerImageBinary_MirrorsK8sOverrides`.
- **Chapter 22** reordered to surface the bundled-image / SCC story before sample output (Sprint 5 tech-writer Issue 14 carry-over).

### Documentation

The book at <https://jgruberf5.github.io/roksbnkctl/book/> launched alongside the v1.0 tag with **32 chapters + preface + worked-example walkthroughs**. Sprint 6 landed chapters 23-32 (E2E plan, day-2 ops, COS supply chain, troubleshooting, command + config + terraform variable reference, glossary, building from source, extending). Sprint 7 added Mermaid diagrams (architecture, lifecycle, GSLB cross-vantage, execution-backend matrix), rewrote the preface, added per-Part worked-example walkthroughs, re-verified every code example against a fresh workspace, and refreshed PRD 05 §"Phase I" + §"Phase N" step matrices to match the shipped surface.

Per-PRD design rationale (cred propagation, execution backends, kubectl internalisation, DNS probe, lifecycle, …) lives under [`docs/prd/`](docs/prd/). Sprint-by-sprint development history lives in [`docs/PLAN.md`](docs/PLAN.md).

### Deferred (v1.x roadmap)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). High-water-mark v1.x items the v1.0 cut explicitly does NOT ship:

- **Cosign / sigstore release signing** — the `.goreleaser.yml` has a placeholder; the signing infra in `.github/workflows/release.yml` lands in v1.x.
- **Homebrew formula / tap repo** — the `brews:` block is wired but commented out pending an `homebrew-tap` repo.
- terraform `--backend k8s` and `--backend ssh:<target>` (state-handling design open).
- `--truncated` user-facing CLI flag for the DNS probe (Sprint 6 validator carry-over).
- Cross-driver cluster-sharing for `scripts/e2e-test-full.sh`.
- SSH backend `apt-get` bootstrap on RHEL/CentOS/Alpine (Ubuntu-only).
- Native Windows Docker Desktop UID/GID handling for terraform-via-docker.
- F5 corporate theming for the book.

## v1.0.1 — 2026-05-11

Re-cut of the v1.0 release. The original `v1.0.0` tag landed on an earlier commit than intended, so the sprint 7 polish (32-chapter book pass, Mermaid diagrams, release-pipeline containerisation, README v1.0 rewrite, `--version` book URL, `make release` driver) never made it into the `v1.0.0` binaries on the GitHub Release page. `v1.0.1` is the corrected cut — everything the `v1.0.0` CHANGELOG entry above describes plus the two deltas below. **End users should install v1.0.1**; the `v1.0.0` Release page is retained as a historical artifact only.

### Added

- **`install_build_dependencies.sh`** — per-OS prereq installer (Linux apt / macOS brew / Windows WSL2). Drives the same toolchain the book chapter 4 walks readers through (Go, terraform, docker, mdbook stack for contributors). Idempotent — skips anything already present.
- **Book chapter 4 (`Installing roksbnkctl`)** expanded with per-OS prereq install steps mirroring the installer script, so the path from "fresh box" to "first `roksbnkctl up`" is one block of commands per platform.

### Changed

- **Book CI shifted from build-and-deploy to validate-only.** `.github/workflows/book.yml` no longer publishes to GitHub Pages from CI — the pandoc backend required for the PDF output isn't present on the runner, and pulling the multi-GB `tools/docker/mdbook` image on every push is wasteful. The workflow now runs `mdbook test` + `mdbook build` for syntax and link validation on PRs and pushes to main; publishing is driven locally by the release integrator.
- **New publish targets** in the Makefile: `make book-publish` pushes the locally-built `book/book/html/` tree to the `gh-pages` branch under `/book/` via a `git worktree` round-trip (preserves `.nojekyll`, CNAME, anything else on the branch). `make release-publish VERSION=v1.0.1` runs `book-publish` AND uploads the PDF to the GitHub Release as `roksbnkctl-book-v1.0.1.pdf` via `gh release upload`. The combined effect: a single command from the integrator's machine handles both publish surfaces, with no CI image pull.
- **`book/book.toml`** marks `[output.pandoc]` as `optional = true` so host-install mdbook (no pandoc on PATH) skips PDF rendering with a warning instead of failing the entire build. Fixes the underlying CI failure that prompted this re-cut.
- **`.gitignore`** excludes `.env`, `.env.local`, `.env.*.local` — local-secrets files sourced by `scripts/e2e-test-full.sh`. Never commit (contain `IBMCLOUD_API_KEY`).

### Fixed (CI recovery)

The first v1.0.1 tag-cut surfaced two latent CI bugs that the previous PR-only validate gate had hidden. Both fixed in this same v1.0.1 cut:

- **`.goreleaser.yml`** no longer references `./book/book/pandoc/pdf/book.pdf` via `release.extra_files`. The previous comment claimed goreleaser would warn-and-continue on a missing path; in practice it fail-stops the release. The PDF is now uploaded separately by `make release-publish` (which runs `gh release upload` from the integrator's machine after the CI workflow finishes), so the `extra_files` reference had no remaining purpose.
- **`mdbook test` dropped from `.github/workflows/book.yml`'s validate job.** mdbook's test step invokes rustdoc on every untagged code fence, treating it as Rust by default. This book contains zero Rust (it's a Go project's operator-facing docs; the actual languages used are bash / go / hcl / json / yaml / text / mermaid / powershell), so the test step generated only false positives. The `mdbook build` step still validates markdown rendering, link integrity, and structural correctness.
- **Chapter 31 (`Building from source`)** — three untagged code fences (Go version snippet, `tools/docker/` tree, `dist/` tree) explicitly tagged as `text` so they render identically and don't trip any future code-fence-aware tooling.

### Release-flow documentation

Integrator tag-cut sequence is now:

```sh
make release                 # stamp, build HTML+PDF, lint, snapshot, verify Pages
git add -A && git commit -m "chore: prep v1.0.1 release"
git tag v1.0.1 && git push origin main --tags
# wait for .github/workflows/release.yml to publish the GitHub Release
make release-publish VERSION=v1.0.1
```

The old `.github/workflows/book.yml build-deploy` step is gone. See `Makefile`'s `release-publish` target and the `book-publish` target it composes.

## v1.0.2 — 2026-05-13

Live-run validation pass. The first chained `scripts/e2e-test-full.sh` run (baseline `A-H` followed by the backend matrix `I-N`) against a real IBM Cloud ROKS cluster surfaced ten latent bugs ranging from binary correctness to test-orchestration to terraform cloud-init prep. All fixed in this release.

### Fixed

#### Binary correctness

- **`roksbnkctl test dns` exits non-zero on any non-NOERROR Rcode.** `internal/cli/test.go::runDNSSingleVantage` now treats NXDOMAIN, SERVFAIL, REFUSED, NOTAUTH as failures (exit 1), not just transport-layer TIMEOUT/ERROR. The text rendering already classified them as `⚠` distinct from `✓`; the exit code now mirrors that classification, matching PRD 03's CI-assertion contract.
- **SSH backend strips local-only env vars before propagation.** `internal/exec/ssh.go::mergeSSHEnv` no longer forwards `HOME`, `USER`, `LOGNAME`, `PWD`, `OLDPWD`, `SHELL`, `PATH`, `TMPDIR` from the caller's local shell to the remote shell. These are per-user / per-session values that don't make sense on a different machine — the remote sshd sets them from `/etc/passwd`. Without the filter, the remote `ibmcloud` CLI tried to `mkdir /home/<caller-local-user>` and fail-stopped with `permission denied`.

#### Tools-image architecture

- **`tools/docker/ibmcloud` Dockerfile bundles the `roksbnkctl` binary.** Sprint 5's k8s-backend DNS-probe Job design assumed the bundled tools image carried `/usr/local/bin/roksbnkctl` (per the inline comment at `internal/cli/test.go::runDNSProbeK8s`), but the Dockerfile until now only installed `ibmcloud`. Added a multi-stage build: Stage 1 compiles roksbnkctl from the repo source (so the image's bundled binary matches the host binary's version), Stage 2 copies it into the runtime image alongside `ibmcloud`. `tools/docker/Makefile` shifts the build context to the repo root with `--build-arg ROKSBNKCTL_VERSION/COMMIT/BUILD_DATE` so the bundled binary's `--version` output matches the host's.

#### Terraform / cloud-init

- **Jumphost cloud-init now logs `ibmcloud` in as the `ubuntu` user.** `terraform/modules/testing/main.tf::jumphost_user_data` ran `ibmcloud login --apikey` only as root, leaving the `ubuntu` user's `~/.bluemix/` empty. When `roksbnkctl --on jumphost ibmcloud …` SSHed in as ubuntu, ibmcloud reported `No API endpoint set` and aborted. Added a `su - ubuntu -c "ibmcloud login …"` step (plus container-service + vpc-infrastructure plugin installs under ubuntu's profile).

#### E2E orchestration scripts

- **`scripts/e2e-test.sh` Phases D8 and H are now env-flag-gated.** `SKIP_PHASE_D_DOWN=1` skips the `D8 down` (cluster teardown at end of Phase D); `SKIP_PHASE_H=1` skips the final workspace-delete. Defaults preserve historical behaviour (both phases run). `scripts/e2e-test-full.sh::run_baseline_AtoG` sets both flags when chaining baseline → backends so the cluster + workspace survive the transition — without this the backends driver hit Phase L (`ops install`) against a destroyed cluster.
- **`preflight_ssh_target` in `scripts/e2e-test-backends.sh` seeds `~/.roksbnkctl/known_hosts` via `ssh-keyscan -t ecdsa`** before any SSH-using phase runs. Without this, the first SSH connection in Phase I fail-stopped with `unknown host` because the binary's `--insecure-host-key` flag is silently dropped by `exec --on jumphost` (DisableFlagParsing interaction — see Known v1.0.3 candidates below).
- **LD3 and LD10 capture patterns fixed.** Both were `out=$(cmd || true); rc=$?` which always read `rc=0` regardless of the binary's actual exit code (the `|| true` makes the command substitution return 0 unconditionally). Switched to `set +e; out=$(cmd); rc=$?; set -e`. Side effect: these tests had been silently always-failing since they were written; this is the first release where they actually validate the binary.
- **LD5 assertion string matches the binary's actual JSON output format.** Was `"\"backend\":\"k8s\""` (compact); the binary uses `json.Encoder.SetIndent("", "  ")` and emits `"backend": "k8s"` (with a space). Added the space.
- **Chapter 31 (`Building from source`) — three untagged code fences explicitly tagged as `text`** so `mdbook test` doesn't try to compile them as Rust.

#### CI

- **`.github/workflows/book.yml` no longer runs `mdbook test`.** The step invoked rustdoc on every untagged code fence in the book; this book has zero Rust and the step generated only false positives. The `mdbook build` step still validates markdown rendering, link integrity, and structural correctness.
- **`.goreleaser.yml` no longer references the PDF book via `release.extra_files`.** The previous comment claimed goreleaser would warn-and-continue on a missing path; in practice it fail-stops the release publish. The PDF is now uploaded separately by `make release-publish` (which runs `gh release upload` from the integrator's machine after the CI workflow finishes).
- **`book/book.toml` marks `[output.pandoc]` as `optional = true`** so host-install mdbook (no pandoc on PATH) skips PDF rendering with a warning instead of failing the build.

### Known v1.0.3 candidates

Surfaced during this validation pass; not fixed in v1.0.2 because they require deeper changes:

- **SSH backend `ibmcloud` session refresh.** IBM Cloud IAM tokens expire after ~60 min. Cloud-init's `ibmcloud login` happens at instance-boot time; by the time a 70+ minute cluster bring-up finishes and tests start, the jumphost's ubuntu session is past its TTL. The SSH backend doesn't currently auto-relogin from `IBMCLOUD_API_KEY` before each invocation. Workaround: trigger backend-matrix tests within the session lifetime of cluster bring-up, or manually `ibmcloud login` on the jumphost before each phase.
- **`--insecure-host-key` flag silently dropped by `exec --on jumphost`.** `internal/cli/cluster.go::runExec` sets `DisableFlagParsing` so cobra doesn't grab flags meant for the wrapped binary; this also discards `--insecure-host-key` as a persistent flag. `extractOnFlag` pulls `--on` out manually; needs an analogous `extractInsecureHostKey` to plumb the flag through. Workaround for v1.0.2: the e2e script seeds `~/.roksbnkctl/known_hosts` via `ssh-keyscan` in preflight, sidestepping the binary path entirely.

### Release-flow

Integrator sequence is unchanged from v1.0.1:

```sh
make release VERSION=v1.0.2
git tag v1.0.2 && git push origin main --tags
# wait for .github/workflows/release.yml to publish the GitHub Release
make release-publish VERSION=v1.0.2
```

## v1.1.2 — 2026-05-13

Second CI-recovery patch on top of `v1.1.0` / `v1.1.1`. The `v1.1.1` cut fixed staticcheck (the only CI signal visible at the time) but the fix — removing the unused `ptrInt64` helper — broke a second CI job: `internal/exec/k8s_integration_test.go` uses `ptrInt64` under the `//go:build integration` tag, which staticcheck and the default-tag `go test ./...` don't compile. Functionally identical to `v1.1.0` / `v1.1.1` — release binaries are byte-near-identical (the helper's source-or-no-source state doesn't affect linker output). **End users should install v1.1.2**; `v1.1.0` and `v1.1.1` Release pages are retained as historical artifacts only.

### Fixed (CI recovery, take 2)

- **Restored `ptrInt64` inside `internal/exec/k8s_integration_test.go`** (its sole caller) instead of in `k8s.go`. Lives under the `//go:build integration` tag now, so staticcheck on the default build doesn't see it AND the integration test compiles. Tighter scoping than the v1.1.1 deletion.
- **`Makefile` pre-tag checklist** should grow a `go build -tags integration ./...` step alongside the `staticcheck` step from v1.1.1's note. CI runs three build configurations (default, integration, plus the staticcheck inheritance) and the local gate only ran one — this gap is what produced the v1.1.0 → v1.1.1 → v1.1.2 cascade. Documented here as the lesson; mechanical Makefile update tracked separately.

### Not fixed in v1.1.2

- **Flaky `TestIntegration_Connect_Whoami`** (`internal/remote/`) — the test pulls an sshd container via testcontainers-go, which hits Docker Hub. The runner's anonymous pull was rate-limited (`429 too many requests`) during the v1.1.1 CI run. Not a code regression and not solvable from the source side; tracked as a known intermittent on shared CI infra. Will re-run cleanly when the rate-limit window clears.

## v1.1.1 — 2026-05-13 — SUPERSEDED by v1.1.2

Intended as the CI-recovery patch for `v1.1.0` but turned out to be incomplete — the fix (removing unused `ptrInt64`) broke a second CI job (`internal/exec/k8s_integration_test.go` references the helper under the `//go:build integration` tag). See `v1.1.2` above for the corrected cut. v1.1.1 binaries are functionally identical to v1.1.0 / v1.1.2; only CI plumbing differs.

### Fixed (CI recovery — incomplete)

- **Removed unused `ptrInt64` helper** in `internal/exec/k8s.go` (staticcheck `U1000`). v1.1.2 restored the helper inside the integration test file, the only place that uses it.

## v1.1.0 — 2026-05-13

The first post-v1.0 feature cycle (Sprint 8). Ships the cluster/trial phase split as a first-class command surface — `roksbnkctl bnk up/down` lets you iterate on a BNK trial without destroying its cluster, and the unscoped `roksbnkctl up/down` become shape-aware composites that preserve v1.0.x behaviour byte-for-byte on legacy single-state workspaces. See [PRD 06](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md) for the design rationale and [PLAN.md §"Sprint 8"](docs/PLAN.md) for the cycle's deliverables.

> **CI note**: the `v1.1.0` tag-cut commit failed staticcheck (unused `ptrInt64` helper in `internal/exec/k8s.go`). Functionally inert; `v1.1.1` is the corrected cut. v1.1.0 binaries on the GitHub Release page work, but new installs should use v1.1.1.

### Added

#### Sprint 8 — `bnk` command group + shape-aware lifecycle

- **`roksbnkctl bnk` command group** ([PRD 06 §"Scope"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#scope)) — the trial-layer counterpart to `roksbnkctl cluster`, so the BNK trial can be torn down and re-deployed without destroying the cluster underneath.
  - `roksbnkctl bnk up` — applies the trial phase against an existing cluster (~7 minutes vs ~50 for a full from-scratch deploy). On an empty workspace it offers to bootstrap the cluster phase first (`~30 min ROKS provision + transit gateway + registry COS + cert-manager + jumphost`) with a confirmation prompt; `--auto` threads through both prompts.
  - `roksbnkctl bnk down` — destroys the trial phase only; the cluster persists for the next iteration. Headline win: a `bnk down` / `bnk up` round-trip is the 5-10 minute trial-apply window, not the 30-minute cluster rebuild that a v1.0.x `down` / `up` cost.
  - Flag surface mirrors `cluster up` / `cluster down`: `--auto`, `--var-file` (repeatable), `--no-kubeconfig` on `bnk up`.
- **`config.DetectShape` workspace-shape classifier** ([PRD 06 §"Shape detection"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#shape-detection)) — on-disk-only (no `terraform` calls), parses the workspace's tfstate files and emits one of:
  - `ShapeEmpty` — neither phase has resources.
  - `ShapeClusterOnly` — cluster phase applied, trial empty.
  - `ShapeSplit` — both phases applied independently (the v1.1.0 default for new workspaces).
  - `ShapeLegacySingle` — trial state contains cluster modules (`module.roks_cluster`, `module.cert_manager`, `module.testing`) from a pre-split `roksbnkctl up`. Verified against the real `canada-roks` workspace (135 resources).
  - Missing tfstate files → treated as "no resources". Malformed JSON → surfaced as error so dispatch doesn't silently misroute.
- **Shape-aware refusal messages** on the phase-scoped commands. Every refusal names the verb that would actually work. The full catalogue is in [Chapter 11 §"Refusal messages catalogue"](https://jgruberf5.github.io/roksbnkctl/book/11-tearing-down.html#refusal-messages-catalogue); the highlights:
  - `cluster up` / `bnk up` / `bnk down` refuse on `ShapeLegacySingle` — there's no way to isolate the cluster or trial phase when both share one tfstate. Points readers at `roksbnkctl up` / `down` for the in-place v1.0.x behaviour.
  - `cluster down` refuses on `ShapeSplit` with a hard error pointing at `bnk down` first (replaces the v1.0.x warning-but-prompt — see §"Changed" below).
  - `bnk down` refuses on `ShapeEmpty` and `ShapeClusterOnly` ("no BNK trial state to destroy in this workspace").
- **Book chapter edits** for the new surface:
  - **Chapter 8** — reframed from "opt-in two-phase mode" to "the default for new workspaces", with a new §"Legacy single-state workspaces" subsection that helps v1.0.x users identify their shape.
  - **Chapter 10** — new §"The `bnk up` / `bnk down` command group" with the bootstrap-prompt sample output, the four-shape dispatch matrix (user-facing simplification of [PRD 06 §"Dispatch table"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#dispatch-table)), and a worked iteration example showing the explicit time savings.
  - **Chapter 11** — new §"The phase-aware decision tree" at the top + §"Refusal messages — catalogue" near the middle; "two destroys" → "three destroys" with `bnk down` documented alongside `down` and `cluster down`.

### Changed

- **`roksbnkctl up` and `roksbnkctl down` are now shape-aware composites** ([PRD 06 §"Dispatch table"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#dispatch-table)). Their semantics shift from "monolithic apply/destroy against the trial state" to "detect the workspace shape and dispatch to the right phase commands in the right order":
  - **Split / Empty / ClusterOnly**: `up` runs `cluster up` (provision or refresh) then trial up; `down` runs trial down then `cluster down`.
  - **LegacySingle**: `up` and `down` run the v1.0.x monolithic trial apply / destroy **byte-for-byte** — same plan output, same resource count. v1.0.x workspaces continue to work without migration.
  - **Empty** + `down`: errors `nothing to destroy in this workspace` (was: same error, semantics unchanged).
  - The composites are pure dispatchers — no business logic of their own. The leaf commands (`runTrialUp`, `runTrialDown`, `runClusterUp`, `runClusterDown`) carry the apply / destroy logic.
  - Implementation: `internal/cli/lifecycle.go` renames the existing `runUp` / `runDown` bodies to `runTrialUp` / `runTrialDown` (the v1.0.x behaviour, factored out) and introduces the composite `runUp` / `runDown` keyed on `config.DetectShape`. `internal/cli/cluster_phase.go` and `internal/cli/bnk_phase.go` add the refusal logic.
- **`roksbnkctl cluster down` enforces trial-then-cluster ordering with a hard refusal**, replacing the v1.0.x warning-but-prompt copy. Previously, `cluster down` would warn `Any BNK trial state on top of this cluster will be orphaned — run roksbnkctl down first if needed` and proceed on confirm; with `--auto` it would proceed silently. v1.1.0 instead refuses with ``BNK trial state exists in this workspace; run `roksbnkctl bnk down` first (or `roksbnkctl down` to tear down both phases)`` — and `--auto` does **not** bypass it (correctness, not confirmation, is the issue). The motivating case: `scripts/e2e-test.sh` runs that destroyed the cluster while trial finalisers were still pending now fail loudly instead of silently leaking resources.

### Fixed

Carry-in fixes to the `--backend docker` and `--backend k8s` paths, folded into v1.1.0 alongside the phase-split work rather than cut as a separate `v1.0.3` patch (no functional change to the cluster/trial split itself; these touch `internal/exec/` only):

- **`--backend docker` for `ibmcloud` was silently broken** — the docker SDK path materialised `IBMCLOUD_API_KEY` as a defined-but-empty env var in the container (the v1.0.x `Env: ["IBMCLOUD_API_KEY"]` bare-name form, which works for the docker CLI's `--env VAR` but not the SDK). Phase K e2e tests false-positive-matched the ibmcloud help banner. v1.1.0 passes `IBMCLOUD_API_KEY=<value>` (and `IC_API_KEY`, `TF_VAR_ibmcloud_api_key`) explicitly. Trade-off noted in [`internal/exec/docker.go`](internal/exec/docker.go) `buildMountsAndEnv` doc: the api key is now visible in `docker inspect` output until the Phase M2 cred audit closes the cred-tmpfile-bind-mount design (deferred per PLAN.md).
- **Host env vars (`HOME`, `USER`, `PATH`, `SHELL`, …) no longer leak into the container.** `internal/exec/docker.go::buildContainerEnv` now filters a host-only set. Previously the bundled `ibmcloud` image's plugin lookup landed at `/home/<user>/.bluemix/plugins/` (host path) instead of `/root/.bluemix/plugins/` (image's `$HOME`) and the plugin list came back empty.
- **`ibmcloud` invocations now self-prime with `ibmcloud login` inside the container.** Both backends apply a `sh -c 'ibmcloud login … --quiet >/dev/null 2>&1 && exec ibmcloud "$@"'` wrap before stateful subcommands (`iam`, `ks`, `account`, `target`, …) so the container's cold-start `$HOME/.bluemix` doesn't error with "Not logged in". `login` / `logout` skip the wrap. Region defaults to `$IBMCLOUD_REGION` or `us-south`. Docker applies the wrap via `dockerImageBinary["ibmcloud"]`; k8s applies the same wrap dynamically in `runOnOpsPod` (no static `jobToolCmdOverride` entry needed).
- **K8s Job `Container.Command` / `Args` shape corrected** for tools without a `jobToolCmdOverride`. v1.0.x set `Command = argv[1:]`, which **overrides** the image's ENTRYPOINT — the kubelet then tried to exec the first arg (e.g., `-c` for an iperf3 client) as the binary, producing `CreateContainerError`. v1.1.0 sets `Args = argv[1:]` so the image's ENTRYPOINT picks the binary (which is what the inline comment had always claimed). Fixes the L2 throughput Job's `--backend k8s` execution.
- **`iperf3` tool image switched to `networkstatic/iperf3:latest`** (public on Docker Hub) from `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<tag>` (private — ROKS workers can't pull without an image-pull-secret). The bundled image returns in v1.x once the ghcr package is flipped public or a per-pod pull-secret is wired per PRD 03 §"K8s backend image pull".
- **`-w` / `--workspace` flag no longer leaks through `roksbnkctl kubectl` / `oc` / `ibmcloud` passthroughs.** `internal/cli/cluster.go::extractWorkspaceFlag` mirrors the existing `extractOnFlag` and strips the root persistent flag from passthrough argv (cobra's `DisableFlagParsing` couldn't see it).
- **Unit tests refreshed to match the new shapes**: `TestResolveDockerImageAndArgv` covers the sh-c wrap for `ibmcloud` and the public iperf3 image; `TestDockerImageBinary_MirrorsK8sOverrides` adds a `mirrorExempt` set for `ibmcloud` (the docker static wrap and k8s dynamic wrap are equivalent at exec time but the map shapes diverge by design); `TestRunOpts_TFVarsEnvPassthrough` asserts host-only vars are filtered in addition to TF_VAR_* being passed through.

### Deferred (v1.x roadmap, post-v1.1.0)

Not in v1.1.0 — see [PRD 06 §"Out of scope"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#out-of-scope) for full rationale.

- **`roksbnkctl migrate`** — splitting a legacy single-state workspace's tfstate into separate `state/` + `state-cluster/` trees via `terraform state mv`. Real engineering work and one-shot state surgery. Deferred until a real legacy user asks; refusal messages reference it as future work so the wording stays valid when it lands.
- **`roksbnkctl bnk plan` / `bnk apply` / `cluster plan` / `cluster apply`** — top-level `plan` / `apply` already operate on the trial state and that behaviour is unchanged. Symmetry additions deferred to a later cycle.
- **Docker-backend composition** for the composite `up` / `down` on empty/split workspaces — `cluster up` has no docker shortcut today, so composing it with a docker-backend trial apply would mix backends mid-run. The composite explicitly disables itself on non-local backends for the empty/split paths; legacy single-state and the direct `cluster up` / `bnk up` calls retain v1.0.x docker behaviour. Full multi-phase docker composition is a follow-up PRD.
- **Multi-trial UX** — a cluster can host multiple BNK trials in principle (different workspaces sharing `cluster-outputs.json`); polish around naming trials and "which trial is current" prompts is deferred.
