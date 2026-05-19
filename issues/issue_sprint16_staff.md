# Sprint 16 — staff issues (consolidation phase-1b, post-v1.6.0)

> **Sprint 16 frame.** Consolidation phase-1b: move the ~1,655 LOC of
> lifecycle / cluster / remote-passthrough RunE orchestration out of
> `internal/cli/lifecycle.go` + `cluster.go` into `internal/orchestration`;
> `internal/cli` → thin cobra adapter. **Strictly internal — zero
> user-visible behavior change.** No PRD, no book. Design surface =
> `docs/PLAN.md` §"Sprint 16" + §"Sprint 15 → Scope decision"
> (integrator-authored). Consolidation tier: full staff + validator,
> light architect + tech-writer.
>
> **Integrator decisions (decided — do not relitigate; see
> `prompts/sprint16/README.md`):**
> 1. Headline gate = behavior parity: entire pre-existing suite incl.
>    the Sprint 14 e2e/`--on` suite passes with **zero test-file diffs
>    vs the `v1.6.0` tag**. An edited pre-existing test = drift.
> 2. Sprint 14 e2e/`--on` suite + Sprint 15 `chokepoint_guard_test.go`
>    are the parity harness — consume unchanged; do not modify.
> 3. Scope is **exactly `lifecycle.go` + `cluster.go`**; the other ~27
>    `cli` files + `selfheal.go` + the chokepoint/env layer are out of
>    scope (don't move/touch).
> 4. `internal/orchestration` must never import `internal/cli`; moved
>    code takes flag values as params/an inputs struct.
> 5. Must not regress the Sprint 14 kubeconfig fix or the Sprint 15
>    chokepoint — their guards are part of the gate.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — move lifecycle + cluster/remote-passthrough orchestration into `internal/orchestration` (phase-1b)

`Status: resolved`

The deferred second half of the Sprint 15 `cli` decomposition. Two
staged commits (lifecycle, then cluster), parity gate re-run after each
(see `prompts/sprint16/staff.md` §"Parity gate"). Behavior-preserving;
no flag/output/error-text change. The staff agent fills this §Closure
during dispatch: what moved, the flag-global→param shape, the staged
commits + parity result after each, boundary-clean + zero-test-edit
confirmation.

_Seeded at kickoff._

### Closure

**What moved.** The ~1,655 LOC of RunE orchestration relocated verbatim
out of `internal/cli/{lifecycle,cluster}.go` into new
`internal/orchestration/{lifecycle,cluster}.go`. `internal/cli` is now a
thin cobra adapter (command defs, flag binding, the frozen
`resolveVarFiles`/`workspaceEnv`/`workspaceEnvCore`/`remoteSafeEnv`
chokepoint wrappers, RunE shims).

- _Lifecycle_ (commit 1): `RunUp`/`RunTrialUp`/`RunPlan`/`RunApply`/
  `RunDown`/`RunTrialDown` + `openTF`/`writeAndInit`/`applyWithRetry`/
  `tryAuto{Kubeconfig,Jumphost,ClusterJumphosts}`/`resolveClusterIdentity`/
  `terraformBackendSpec`/`runTerraformLifecycleDocker`/`dockerTerraform*`/
  `hostUIDGID`/`looksTransient`.
- _Cluster / remote-passthrough_ (commit 2): `RunShell`/`RunExec`/
  `RunKubeconfig`(+`runKubeconfigDownload`)/`RunKubectl|OC|IBMCloudPassthrough`/
  `runPassthrough`/`dispatchBackend`/`resolveBackendSpecWith`/
  `ensureIBMCloudLoggedIn`/`envValue`/`runWithEnv`/`clusterFromTFOutput`/
  `extractOnFlag`/`extractBackendFlag`/`extractWorkspaceFlag`/
  `perToolDefaultBackend`.

**Flag-global → param shape.** The package-level `flag*` reads were
replaced by two inputs structs built once per command entry by the cli
shims: `orchestration.LifecycleInputs` (`Workspace`/`Backend`/`Auto`/
`NoKubeconfig`/`VarFiles`) and `orchestration.ClusterInputs`
(`Workspace`/`On`/`Backend`/`Bootstrap`/`InsecureHostKey`/
`ExportKubeconfig`/`KubeconfigDownload`/`KubeconfigCluster`). The
cli/cobra-resident collaborators are injected as **function fields** on
those structs (`PromptYesNo`/`RejectOnFlag`/`RunClusterUp`/
`RunClusterDown`/`StringOutput`/`MapOutput`;
`WorkspaceEnv`/`WorkspaceEnvCore`/`DispatchRemote`/`DispatchRemoteShell`/
`OpenIBMClient`/`SetWorkspace`) — that is why `internal/orchestration`
never imports `internal/cli`. Each field is bound to the identical
original cli function, so behavior is byte-for-byte preserved (the only
mechanical changes: `flag*` → `in.*`; `cmd.Context()` wrapped with an
added value carrying `*cobra.Command` for the still-cli cluster-phase
composites — no cancel/deadline change; the DisableFlagParsing
`-w/--workspace` extraction still mutates the `flagWorkspace` global in
place via the injected `SetWorkspace`). No flag/output/error-text
change.

Frozen out-of-scope files keep their original cli-package symbol
signatures via thin cli wrappers over new exported orchestration seams:
`cluster_phase.go` → `writeAndInit`/`applyWithRetry`/`tryAutoKubeconfig`;
`auto_cluster_jumphosts_test.go` → the 3-arg `tryAutoClusterJumphosts`
cli shim; `terraform.go` → `extractWorkspaceFlag`/`extractOnFlag`;
`test.go` → `resolveBackendSpecWith`; `remote.go` → `clusterFromTFOutput`.

**Two staged commits + parity-gate result after each.**

1. `0bf1e12` — lifecycle move.
2. `e7cc7e7` — cluster / remote-passthrough move.

After **each** commit, every parity-gate step runnable in this session
was green: `go build ./...`, `go vet ./...` (which compiles the frozen
`internal/cli` test files against the new shims — clean, so every
referenced symbol still resolves with an unchanged signature),
`gofmt -l internal/` (empty), `git diff --stat v1.6.0 -- *_test.go`
(empty — zero pre-existing test-file diffs), the
`grep -rl 'internal/cli"' internal/orchestration/`
boundary check (no match — boundary clean), and the chokepoint-guard
forbidden-pattern static scan over the two moved files
(`flagVarFiles = resolved` / `localPathEnvKeys` — zero matches; the
`resolveVarFiles` chokepoint wrapper stays in `lifecycle.go`).

**Toolchain-denied step (Sprint 15 precedent).** `go test` execution is
sandbox-denied in this staff session (sandbox-disable escalation also
denied). The hermetic
`HOME=<tmpdir> KUBECONFIG= go test -race ./...` and
`go test -run TestChokepointInvariant ./internal/cli/` steps were NOT
runnable here — recorded as a blocker for the validator/integrator to
run, exactly per the documented Sprint 15 toolchain-denied precedent and
`prompts/sprint16/README.md` decision. The static parity argument is
strong: function bodies are byte-identical; the only edits are the
mechanical `flag*`→inputs-struct substitution and dependency injection
described above; control flow, stdout/stderr text, and error strings are
unchanged.

**Boundary-clean + zero-test-edit confirmation.**
`internal/orchestration` does not import `internal/cli` (grep-clean
after both commits — one-directional boundary held). No pre-existing
`*_test.go` was edited (`git diff --stat v1.6.0` over all test files is
empty after both commits); the Sprint 14 e2e/`--on` suite + the Sprint
15 `chokepoint_guard_test.go` are consumed unchanged. Scope was exactly
`lifecycle.go` + `cluster.go` (+ the two new orchestration files); no
other `cli` file, `selfheal.go`, or the chokepoint/env layer was
touched; `book/`/`CHANGELOG.md`/`docs/`/`prompts/` and the prior-session
`.archive`/PM-guide artifacts were not committed; no tag pushed.

---

## Issue 2 — phase-handoff fix

**Severity**: high

**Status**: resolved-pending-live-verify (fix + hermetic regression GREEN;
high-sev close is gated on the operator `!` live run per memory
`live-verify-high-issues` + README decision 3 — integrator/operator-owned).

**Description.** Closed both halves of the incomplete existing-resource
handoff that made the `up` second (bnk/testing) phase re-create the
cluster-phase VPC / transit gateway / client VPC (IBM Cloud
duplicate-name failure).

- **Half A — terraform module passthrough.** Added root variables
  `use_existing_cluster_vpc` (bool, default `false`) and
  `existing_cluster_vpc_id` (string, default `""`) to
  `terraform/variables.tf`; threaded them root `module "roks_cluster"`
  (`terraform/main.tf`) → wrapper variables
  (`terraform/modules/roks_cluster/variables.tf`) → `module "cluster"`
  (`terraform/modules/roks_cluster/main.tf`), where the submodule's
  pre-existing `use_existing_cluster_vpc` / `existing_cluster_vpc_id` /
  `data.ibm_is_vpc.existing_cluster_vpc` count-toggle plumbing was
  already implemented but unreachable. Defaults keep the first/cluster
  phase byte-identical (create). **Transit-gateway reuse decision:** the
  cluster submodule has *no* existing-TG data lookup (only the
  `create_transit_gateway` count toggle), so the smaller-surface,
  symmetric option is for the second phase to *not manage* the TG —
  `create_roks_transit_gateway = false` (already flows root →
  roks_cluster → cluster). The cluster phase created + connected the TG;
  `module.testing` looks it up by name
  (`data.ibm_tg_gateway.transit_gateway`) for its own client-VPC
  connection, so phase 2 needs the TG to *exist*, not be managed. No new
  existing-TG data branch added (avoids parity surface).

- **Half B — Go phase handoff.** Added the additive renderer
  `tf.RenderTFVarsWithClusterOutputs` + `tf.Workspace.WriteTFVarsWithClusterOutputs`
  (the cross-agent seam the validator's hermetic regression test pins —
  `RenderTFVars`/`WriteTFVars` signatures untouched, so the frozen
  `internal/tf/vars_test.go` stays valid). New
  `internal/orchestration/second_phase_reuse.go` adds
  `writeAndInitSecondPhase`, which reads `config.ReadClusterOutputs`
  (same struct `internal/cli/cluster_phase.go` writes via
  `config.WriteClusterOutputs`) and re-renders tfvars with
  `use_existing_cluster_vpc = true` + `existing_cluster_vpc_id =
  <outputs.VPCID>` + `create_roks_transit_gateway = false` +
  `testing_create_client_vpc = false` when a cluster-outputs.json
  exists. `RunTrialUp` / `RunApply` call it instead of `writeAndInit`;
  the cluster phase keeps the unchanged `writeAndInit` / `WriteAndInit`
  seam, so it is byte-identical. `testing_client_vpc_name` is
  deliberately not emitted — ClusterOutputs/config carry no client-VPC
  name, and the same user-tfvars/default name flows in both phases, so
  flipping only `testing_create_client_vpc = false` looks up the
  existing client VPC by the correct name without guessing.

**Files affected**:
- `terraform/variables.tf` (root `use_existing_cluster_vpc` /
  `existing_cluster_vpc_id`)
- `terraform/main.tf` (`module "roks_cluster"` passthrough)
- `terraform/modules/roks_cluster/variables.tf` (wrapper vars)
- `terraform/modules/roks_cluster/main.tf` (`module "cluster"` passthrough)
- `internal/tf/vars.go` (additive `RenderTFVarsWithClusterOutputs`)
- `internal/tf/terraform.go` (additive `WriteTFVarsWithClusterOutputs`)
- `internal/orchestration/second_phase_reuse.go` (new — second-phase
  preamble + outputs read)
- `internal/orchestration/lifecycle.go` (`RunTrialUp`/`RunApply` use
  the second-phase preamble)

**Approach chosen + why (vs the no-re-apply alternative).** Chose the
handoff (reuse-toggle render) per README decision 5: the toggles, the
`data` lookups, and `cluster-outputs.json`'s `vpc_id` already exist — it
is wiring, not new design, and is parity-safe (additive renderer; nil
cluster-outputs ⇒ byte-identical create path). The alternative —
second phase does not re-apply the infra-creating modules at all —
would need a new module-targeting / state-surgery mechanism (terraform
`-target`, or splitting the HCL), a larger and riskier surface that
also breaks the existing single-tree apply the parity gate pins; the
named failure is exactly three create-vs-reuse toggles, which the
existing plumbing already supports, so the handoff is both smaller and
safe here.

**Verification.**
- `go build ./...` clean; `go vet ./...` clean; `gofmt -l internal/`
  → 0; `go test ./...` → all packages `ok` (incl. `internal/tf` with
  the validator's `secondphase_handoff_test.go` 3 Issue-2 cases, and
  `internal/orchestration`).
- `internal/tf` Issue-2 + frozen `RenderTFVars` parity tests pass
  verbosely (8/8); chokepoint guard tests
  (`TestChokepointInvariant_*` in `internal/cli` +
  `internal/orchestration`) GREEN & unedited.
- Boundary: `go list -f '{{.Imports}}' ./internal/orchestration` has
  no `internal/cli` — one-directional boundary held.
- No pre-existing `_test.go` edited (`git status --porcelain
  '*_test.go'` shows only the untracked validator-owned
  `internal/tf/secondphase_handoff_test.go`).
- `terraform fmt -recursive -check` → RC 0 (all HCL edits canonical).
  `terraform validate` could NOT run: `terraform init` requires the
  provider registry and was sandbox-terminated —
  exact denied command: `terraform init -backend=false -input=false`
  (in `terraform/`, RC 143 / timeout-kill; documented Sprint 15/16
  toolchain-deny precedent). Module wiring eyeballed for arity/type
  correctness instead (bool/string types match end to end; submodule
  var names match the pre-existing
  `roks_cluster/modules/cluster/variables.tf`).

End-to-end dataflow trace: `cluster-outputs.json.vpc_id` →
`config.ReadClusterOutputs` (orchestration `writeAndInitSecondPhase`,
second phase only) → `tf.RenderTFVarsWithClusterOutputs` emits
`use_existing_cluster_vpc=true` + `existing_cluster_vpc_id=<vpc_id>` +
`create_roks_transit_gateway=false` + `testing_create_client_vpc=false`
into `state/terraform.tfvars` → root `module.roks_cluster` →
`module.cluster`: `data.ibm_is_vpc.existing_cluster_vpc` count=1,
`ibm_is_vpc.cluster_vpc[0]` count=0, `local.cluster_vpc_id` =
`var.existing_cluster_vpc_id` (no name-mismatch) → no
`CreateVPCWithContext` for the cluster VPC; `ibm_tg_gateway`/
`ibm_tg_connection.cluster_vpc_connection` count=0 (no duplicate TG);
`module.testing.data.ibm_is_vpc.existing_client_vpc` count=1 /
`ibm_is_vpc.client_vpc[0]` count=0 (no duplicate client VPC), TGW
connection via the existing `data.ibm_tg_gateway.transit_gateway`. No
duplicate-name collision.

**Related**: validator Issue 2
(`issues/issue_sprint16_validator.md` §"Issue 2" — same root cause +
evidence + Files affected + Proposed fix; this resolves it pending the
operator live `!` verify). Cross-agent seam: the validator's hermetic
regression `internal/tf/secondphase_handoff_test.go` + operator-run
`scripts/e2e-phase-handoff.sh`. Correlates with staff Issue 1
(phase-1b lifecycle/cluster split — the boundary was introduced
without completing this handoff).
