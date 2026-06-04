You are the **tech-writer** agent for Sprint 28 of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Branch: `sprint28-three-phase-split` (do NOT merge to main, do NOT commit/tag). You run AFTER architect + staff + validator integrate. No memory of prior conversation.

## Read first
1. `prompts/sprint28/README.md` — integrator decisions.
2. `issues/issue_sprint28_tech-writer.md` — your full issue.
3. The architect/staff/validator closures in `issues/issue_sprint28_*.md`.
4. The architect-authored lifecycle/phases chapter under `book/src/`.

## Tasks (drift sweep — build the binary first; fix drift only)
1. **Command reference**: `roksbnkctl testing up/down` (+ flags) matches the binary; `bnk`/`cluster`/`up`/`down`/`plan`/`apply` help reflects three phases (BNK no longer includes jumphosts; the `cluster down` guard message).
2. **Lifecycle/phases chapter**: reconcile vs the binary — three-phase dependency graph, parallel `up`, per-phase `up`/`down`, `bnk down` leaves the jumphosts, reuse-existing-cluster, teardown ordering + `cluster down` guard. Re-capture illustrative transcripts byte-for-byte.
3. **`testing` vs `test` disambiguation** — make it unmistakable: `roksbnkctl testing` provisions/destroys jumphosts (a phase); `roksbnkctl test` / `test hosts` runs connectivity/DNS/throughput probes. This is the likeliest operator confusion.
4. **Migration note**: the pre-Sprint-28-workspace migration is documented and matches the binary.
5. **CHANGELOG**: user-facing — three independent phases, parallel up (faster), independent `bnk`/`testing` teardown (reuse jumphosts across BNK redeploys), reuse-existing-cluster, the migration; feature-branch/integrator-gated per the integrator.

## Constraints
- No `internal/`/`cmd/`/`terraform/` code edits — doc reconciliation against the binary.
- Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da` convention.
- mdbook builds clean. Do not commit/tag. End your `## Closure — tech-writer, <date>` with an explicit **GREEN / RED** verdict (the integrator still gates merge on the live parallel-up + independent-down verify).
