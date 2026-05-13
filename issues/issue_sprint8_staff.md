# Sprint 8 — staff engineer issues

Sprint 8 ships PRD 06 (cluster/trial phase split) as a first-class command surface and cuts `v1.1.0`. Staff scope landed all five code/test deliverables: `WorkspaceShape` enum + `DetectShape`, new `bnk` cobra group, refactor of `runUp`/`runDown` into shape-aware composites with `runTrialUp`/`runTrialDown` leaf helpers, and refusals on `cluster up` / `cluster down`. Shape detection has 9 table cases + 6 standalone (file edges + module-match edges + enum stringer); bnk/cluster refusal matrix has 10 dedicated tests. All `go build/test/vet/gofmt` checks on the Sprint 8 surface are green.

Three issues filed: 1 high (pre-existing `internal/exec` gofmt + test failures noted in starting git status — outside Sprint 8 scope, handed off to validator); 2 informational (deferred test coverage for `runUp` composite happy paths; deferred coverage of `cluster up` happy path on Empty/ClusterOnly/Split).

## Issue 1 (HIGH — pre-existing `internal/exec` test failures + gofmt drift) — handed off to validator

**Severity**: high
**Status**: handed off — outside Sprint 8 scope

The initial git status (per the Sprint 8 prompt context) lists `M internal/exec/docker.go`, `M internal/exec/k8s.go`, and `M internal/exec/k8s_install.yaml`. These pre-existed at sprint start (the staff prompt explicitly scopes the engineer to `internal/config/tfstate.go`, `workspace.go`, `internal/cli/bnk_phase.go`, `lifecycle.go`, `cluster_phase.go`, and their tests + fixtures — `internal/exec/` is not in the staff-touchable list).

After stashing my Sprint 8 work, `go test ./internal/exec/...` passes; with the pre-existing `M` changes applied (no Sprint 8 work involved), four `internal/exec` tests fail:

- `TestRunOpts_TFVarsEnvPassthrough` — expected `PATH=/usr/local/bin` env entry missing from container env.
- `TestResolveDockerImageAndArgv/ibmcloud_prepends_binary` — argv length mismatch (got 6, want 3); the ibmcloud-login wrap shell-script prepend is interacting with the test's argv expectation.
- `TestResolveDockerImageAndArgv/iperf3_keeps_legacy_shape_(image_ENTRYPOINT_picks_the_binary)` — image resolved to `networkstatic/iperf3:latest` instead of the `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:` prefix the test expects.
- `TestDockerImageBinary_MirrorsK8sOverrides/ibmcloud` — docker binary slice diverges from the k8s override slice (login wrap injected on docker side, not k8s).

Additionally `gofmt -d -l .` reports drift in `internal/exec/docker.go` (two unrelated alignment changes around the `toolImages` map; same drift would re-appear in `internal/exec/k8s.go` if those tests passed).

Likely root cause: someone iterated on the ibmcloud `login -a … --apikey` wrap (PRD 04 cred polish?) and updated `docker.go` but didn't refresh the tests in `docker_test.go` / `docker_terraform_test.go`. The k8s side's mirror test is the symptom.

Handoff: validator picks this up as part of the Sprint 8 regression sweep. Either the tests need updating to match the new docker wrap shape, or the `internal/exec/docker.go` changes should be reverted before tag-cut. Sprint 8 staff did **not** touch `internal/exec/` per scope.

## Issue 2 (INFORMATIONAL — deferred test coverage for `runUp` composite happy paths) — accepted

**Severity**: informational
**Status**: accepted (covered by live verification + `runDown` empty-refusal unit)

`internal/cli/bnk_phase_test.go::TestRunDown_EmptyRefuses` pins the one composite path that surfaces as a clean error (no terraform call). The other composite cells — `up` on Empty / Split / ClusterOnly / LegacySingle and `down` on LegacySingle / Split / ClusterOnly — all eventually dispatch to terraform-exec, which the unit test environment can't satisfy (no upstream HCL fetched, no IBM Cloud creds, no real apply). Asserting "we reached terraform" is brittle.

Coverage falls back to:

1. The leaf `runTrialUp` / `runTrialDown` paths are unchanged from v1.0.x (just renamed) — pre-existing live verification continues to apply.
2. `runBnkUp` / `runBnkDown` refusals are independently tested (cover the same `DetectShape`-based switch the composites use; one wiring divergence would surface as a refusal-message regression).
3. Sprint-integration live verification per PLAN.md §"Sprint 8 — Test deliverables" exercises the full `cluster up` → `bnk up` → `bnk down` → `cluster down` cycle against a sandbox IBM Cloud workspace.

If the validator's e2e patch lands the full cycle (per the parallel agent's scope), this issue resolves automatically.

## Issue 3 (INFORMATIONAL — `cluster up` happy path on Empty/ClusterOnly/Split untested at unit level) — accepted

**Severity**: informational
**Status**: accepted (same rationale as Issue 2)

`internal/cli/bnk_phase_test.go::TestClusterUp_LegacySingleRefuses` pins the one `cluster up` refusal. The three non-refusal cells (Empty → real apply, ClusterOnly → no-op refresh, Split → no-op refresh) dispatch through `openClusterTF` which requires a workspace `config.yaml`, IBM Cloud creds, and a reachable terraform binary — same unit-test environment constraints as Issue 2. Live verification covers them.

If a future sprint lands a `tfWorkspace` interface or test-mode short-circuit on `openTF` / `openClusterTF`, fold these cells into the unit test then.

---

## Architectural / PRD notes (read-only, no action)

- The implementation matches the spike on `spike/bnk-phase-split@00181d0` modulo small editorial differences (doc comments expanded to cite PRD 06 sections; `runBnkDown` uses an explicit `switch` rather than chained `if`; both equivalent semantically).
- One semantic divergence from the spike: `runBnkUp` correctly classifies `ShapeSplit` as a non-bootstrap case (just dispatches to `runTrialUp`), matching PRD 06 §"Dispatch table" row "bnk up | Split → trial up". The spike's behavior on that cell is the same.
- The cluster-module prefix list (`module.roks_cluster`, `module.cert_manager`, `module.testing`) is hard-coded in `clusterPhaseModules`. If `cluster_phase.go` ever broadens `deploy_bnk=false` to skip additional modules, this list must be updated in lockstep — added a comment on the var pointing at `cluster_phase.go` for the next maintainer.
