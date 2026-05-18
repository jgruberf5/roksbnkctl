# Sprint 15 — tech-writer issues (consolidation cycle, post-v1.5.0)

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

## Issue 1 — drift sweep + launch verdict (light read-only cycle)

`Status: resolved` — recorded by the integrator (light read-only tier; the tech-writer agent was not separately dispatched, consistent with the consolidation-tier dispatch in `prompts/sprint15/README.md`).

**Drift sweep — clean.** Internal-only refactor; **no user-visible / doc / book surface**. The three surfaces that exist this cycle agree:

- **CHANGELOG `v1.6.0` ↔ as-landed code:** `### Changed` = internal consolidation, explicitly "no user-visible behavior change" — true (behavior-parity gate: zero pre-existing test-file diffs, Sprint 14 harness byte-identical, full hermetic `go test -race ./...` green). `### Removed` reconciled to the as-landed disposition (scattered `localPathEnvKeys` deleted; `remoteSafeEnv`/`workspaceEnv[Core]` demoted to one-line delegators) — matches the code and architect Issue 2.
- **CHANGELOG ↔ `docs/PLAN.md` §"Sprint 15":** consistent, including the integrator §"Scope decision" (deliverable 2 → phase-1a done / 1b→Sprint 16) and the Sprint 16 carry-over.
- **No book/PRD surface** (correct for an internal consolidation) — nothing to drift.

**Dogfooding:** N/A — zero user-facing change by design; the "no behavior change" claim is the gate itself and it passed.

**Launch verdict: GREEN for `v1.6.0`** (no user-visible change; parity proven; all four ledgers terminal). Tag/version designation (`v1.6.0` vs `v1.5.1`) remains integrator-owned at cut.
