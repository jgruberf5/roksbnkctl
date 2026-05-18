# Sprint 15 — staff issues (consolidation cycle, post-v1.5.0)

> **Sprint 15 frame.** Consolidation / debt-paydown cycle targeting
> `v1.6.0` (integrator may re-designate `v1.5.1` under strict SemVer —
> **no user-visible behavior change** this cycle; version/tag is
> integrator-owned at cut). Design surface = `docs/PLAN.md` §"Sprint 15"
> (integrator-authored). **No PRD, no book surface.** Runs at the
> **consolidation tier** per `NEW_PROJECT_STARTING_POINT.md`
> §"Tiering the sprint process by change size": full staff + validator,
> light architect + tech-writer.
>
> **Integrator decisions (decided — do not relitigate; see
> `prompts/sprint15/README.md` and `docs/PLAN.md` §"Sprint 15"):**
> 1. Headline gate is **behavior parity** — entire pre-existing suite,
>    incl. the Sprint 14 e2e/`--on` suite, passes with **zero
>    test-file diffs**. An edited pre-existing test = drift, not a fix.
> 2. The Sprint 14 e2e/`--on` suite is the **parity harness**, not a
>    deliverable — consume it unchanged; do not rebuild/modify it.
> 3. `cli` decomposition is **phase 1 = exactly `lifecycle.go` +
>    `cluster.go`** → `internal/orchestration`; the other ~27 `cli`
>    files are a deferred tracked follow-up.
> 4. Must not regress the Sprint 14 kubeconfig fix (cloud-init + `--on`
>    self-heal); per-AZ stale-target reconcile option (b) stays
>    post-`v1.6.0`.

`Status: open | in-progress | resolved | wontfix | accepted`.


---

---

## Issue 1 — single path/env normalization chokepoint + `internal/cli` phase-1 decomposition + guard/coverage

`Status: resolved`

The headline consolidation deliverable: collapse the recurring "a value
correct in the invocation context is wrong once it crosses a boundary"
bug class (Sprint 12 Issues 1/2 `--var-file`/`--tf-source` + Sprint 13
Issue 1 KUBECONFIG leak) to a single chokepoint, begin the `cli`
decomposition, and add the guard test + `internal/cos` coverage — all
with **zero user-visible behavior change**.

### Closure

**Chokepoint shape — root `PersistentPreRunE` (not a per-group helper).**
Chosen because cobra runs exactly one `PersistentPreRunE` (the
most-specific in the chain — here always the root's; no subcommand
overrides it) before every command's RunE, *including*
`DisableFlagParsing` passthrough commands. That is the smallest correct
surface for "normalize every path-valued flag exactly once": one
function (`rootPersistentPreRunE` in `internal/cli/root.go`), one call,
every command. The pre-existing `warnLegacyState` nudge was folded into
it. It calls `orchestration.Resolve(flagVarFiles, flagTFSource)` once,
publishes the result as the package-level `resolvedFlags
*orchestration.ResolvedFlags`, and writes the normalized values back
into the flag globals — the **single** mutation site replacing the
8+ per-RunE `flagVarFiles = resolved` fan-out and the 2 per-init-site
`resolveLocalTFSource` re-derivations.

**Every scattered site subsumed (enumerated before deletion):**

- `resolveVarFiles(flagVarFiles)` + `flagVarFiles = resolved` fan-out —
  8 RunE sites: `lifecycle.go` runUp/runTrialUp/runPlan/runApply/
  runDown/runTrialDown, `bnk_phase.go` runBnkUp/runBnkDown,
  `cluster_phase.go` runClusterUp/runClusterDown → **all deleted**;
  each now reads the chokepoint-normalized `flagVarFiles`. The
  `resolveVarFiles` *symbol* remains in `lifecycle.go` as a one-line
  thin wrapper delegating to `orchestration.NormalizeVarFiles` (the
  pre-existing in-package tests pin it; zero test diffs).
- `resolveLocalTFSource(flagTFSource)` — 2 sites in `init.go`
  (`runUpgradeTF`, `promptTFSource`) → **deleted**; both consume the
  chokepoint-normalized `flagTFSource`. Symbol kept as a thin wrapper
  over `orchestration.NormalizeLocalPath` (test-pinned).
- `workspaceEnv` / `workspaceEnvCore` / `localPathEnvKeys` /
  `remoteSafeEnv` in `cluster.go` — canonical composition + the single
  `LocalOnlyEnvKeys` classification **moved** to
  `internal/orchestration`. `cluster.go` keeps `workspaceEnv` /
  `workspaceEnvCore` / `remoteSafeEnv` as test-pinned thin wrappers
  delegating to orchestration. `localPathEnvKeys` is **gone entirely**
  (now the single `orchestration.LocalOnlyEnvKeys`).

**Scrub: demoted to one documented boundary assertion (not deleted).**
The defensive scrub now exists at exactly one point —
`dispatchRemote` in `remote.go:78`, `envExtra = remoteSafeEnv(envExtra)`
— reframed in-code as *THE single SSH-boundary assertion*, applied at
the one point every `--on` dispatch funnels through, just before bytes
cross the wire. Demoted rather than deleted because the
`DisableFlagParsing` passthrough call graph (extractWorkspaceFlag
mutating flagWorkspace at RunE time, `test.go`, the ssh-backend
dispatch) makes proving unreachability more fragile than one assertion
consuming the same single `LocalOnlyEnvKeys` classification — the
guarantee is structural, not a scattered defense-in-depth list.

**`internal/orchestration` boundary (phase-1).** New package owns: the
chokepoint primitives (`NormalizeVarFiles`, `NormalizeLocalPath`,
`LocalOnlyEnvKeys`, `ScrubLocalOnly`, `ResolvedFlags`, `Resolve` in
`chokepoint.go`) and the env composition moved out of `cluster.go`
(`WorkspaceEnv`, `WorkspaceEnvCore` in `env.go`). `internal/cli`'s
`lifecycle.go`/`cluster.go` are now thin cobra adapters delegating the
extracted path/env logic to it. Collateral: none beyond the env
composition the `cluster.go` helpers required — the lifecycle terraform
flow and the other ~27 `cli` files were left in place per the explicit
phase-1 scope (no logic moved, only the chokepoint substitution). No
upward imports: `go list -deps` over
`orchestration`/`tf`/`remote`/`config` shows zero
`roksbnkctl/internal/cli` — clean one-directional boundary.

**Guard-test mechanism.** `internal/cli/chokepoint_guard_test.go`:
(1) `TestChokepointInvariant_NoPerRunEReDerivation` — a CI-asserted,
comment-stripped source scan that fails if any RunE/dispatch file
reintroduces a `flagVarFiles = resolved` fan-out, a
`resolveVarFiles(`/`= resolveLocalTFSource(` per-call-site
re-derivation, or a scattered `localPathEnvKeys` list outside the single
orchestration classification; (2)
`TestChokepointInvariant_ResolveIsSingleSourceOfTruth` — pins that
`orchestration.Resolve` normalizes both path flags and that
`resolvedFlags` is the single published resolved-invocation context.

**`internal/cos` coverage.** `internal/cos/cos_test.go` — 4 test
functions / 8 sub-cases (`EndpointForRegion`, `LocationConstraint`,
`New` validation-error matrix, `New` non-dialing happy path); no live
IBM Cloud calls. Coverage **0% → 19.2%** (the bucket/object ops require
a live S3 endpoint and are intentionally not stubbed this cycle).

**Behavior parity — zero test-file diffs.** The entire pre-existing
unit + integration suite, **including the Sprint 14 e2e + `--on`
parity harness** (`lifecycle_e2e_test.go`,
`lifecycle_e2e_integration_test.go`), passes **completely unchanged**:
`git diff --stat -- '*_test.go'` is empty. `go build`/`go vet`/`gofmt
-l`/`go test ./...`/`make staticcheck` all clean/green;
`go test -tags integration ./...` green except the pre-existing
`TestIntegration_OpsInstall_ShowsRBACAndPod` env failure (no IBM Cloud
API key on this host — verified identical on the unmodified baseline,
unrelated to the refactor). Manual smoke (`terraform` read-only guard,
`targets`/`--on` help, `up --help` flags) matches v1.5.0 output.

One narrow, documented behavior nuance recorded in
`rootPersistentPreRunE`: an *invalid* `--var-file` now surfaces in the
PersistentPreRunE (one step before the RunE top) — same error text,
unchanged for all valid inputs; no test pins the old ordering and the
lifecycle `--on` reject is unaffected for valid flows. This is the
inherent, intended tradeoff of the single-chokepoint shape.

_Seeded at kickoff; filled by the staff agent during dispatch._
