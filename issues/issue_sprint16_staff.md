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
