# Sprint 28 — tech-writer issues (post-integration drift sweep)

> **Sprint 28 frame.** Light drift sweep AFTER the three-phase split
> (Cluster / BNK / Testing) integrates. Reconciles the operator-facing
> surface — the lifecycle/phases chapter, the new `testing` command, the
> command reference, CHANGELOG — against the built binary's actual behavior.
> GREEN/RED verdict.

`Status`: open

---

## Issue 1 — Drift sweep over the integrated three-phase tree

**Severity**: low
**Status**: open

Build the binary, then sweep:

1. **Command reference**: the new `roksbnkctl testing up/down` (+ any flags)
   matches the binary; `bnk up/down`, `cluster up/down`, `up/down/plan/apply`
   help text reflects the three-phase reality (BNK no longer includes
   jumphosts; `cluster down` guard message).
2. **Lifecycle/phases chapter** (architect-authored): reconcile against the
   binary — the three-phase dependency graph, parallel `up`, per-phase
   `up`/`down`, `bnk down` leaves the jumphosts, reuse-existing-cluster, the
   teardown ordering + `cluster down` guard. Re-capture illustrative
   transcripts byte-for-byte; drop the illustrative note.
3. **`testing` vs `test` disambiguation**: ensure the docs clearly separate
   `roksbnkctl testing` (provision/destroy jumphosts — a phase) from
   `roksbnkctl test` / `test hosts` (run connectivity/DNS/throughput probes).
   This is the most likely operator confusion — make it unmissable.
4. **Migration note**: the pre-Sprint-28-workspace migration path is documented
   and matches what the binary actually does.
5. **CHANGELOG**: user-facing — the three independent phases, parallel up
   (faster), independent `bnk`/`testing` teardown (reuse jumphosts across BNK
   redeploys), reuse-existing-cluster; note the migration; feature-branch /
   integrator-gated per the integrator.

### Acceptance criteria
1. `testing`/`bnk`/`cluster`/`up`/`down` reference matches the binary.
2. Lifecycle chapter matches the three-phase reality; transcripts captured.
3. `testing` vs `test` disambiguation is explicit.
4. Migration documented; CHANGELOG user-facing. mdbook builds clean.
   GREEN/RED verdict recorded.

### Files affected (probable)
- `book/src/**` (lifecycle chapter, command reference), `CHANGELOG.md`.
- No `internal/`/`cmd/`/`terraform/` code edits.

### Related
- `issues/issue_sprint28_architect.md` + `..._staff.md` closures.
- Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da`
  convention.
