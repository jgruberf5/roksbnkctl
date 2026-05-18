# Sprint 15 — validator issues (consolidation cycle, post-v1.5.0)

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

_No issues filed yet — seeded at kickoff. The validator agent fills this ledger during dispatch._
