You are the **tech-writer** agent for Sprint 27 of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Feature branch: `sprint27-bnk-native-k8s` (do NOT merge to main). You run with no memory of prior conversation, AFTER architect + staff + validator have integrated.

## Read first
1. `prompts/sprint27/README.md` — integrator decisions.
2. `issues/issue_sprint27_tech-writer.md` — your full issue.
3. The architect + staff + validator closures in `issues/issue_sprint27_*.md` — what the docs should say, what the binary does, and the validator's **measured** speed numbers (any speedup claim must trace to these).
4. The architect-authored BNK-phase chapter + concept note under `book/src/`.

## Tasks (drift sweep — read-only first, fix drift only; build the binary first)
1. **Command reference**: `roksbnkctl bnk up --help` / `bnk down --help` / the new `bnk status --help` match the binary; the `--native` / legacy-terraform flag and any `--timeout`/`--timings` flags are reflected.
2. **BNK-phase chapter**: reconcile prose vs binary (cert-manager → FLO → CNEInstance → License native reconcile; what each phase watches; `bnk status` output); re-capture illustrative transcripts byte-for-byte; drop the illustrative note.
3. **Speed claim**: any "faster"/timing numbers in the chapter or CHANGELOG must match the validator's measured native-vs-terraform benchmark — do NOT ship an unverified "Nx faster"; quote the measured delta or phrase qualitatively if the integrator hasn't pinned a number.
4. **Stale sweep**: update prose that calls the BNK phase terraform-driven / `time_sleep`-gated; confirm the "IBM IAM trusted-profile + COS reads stay in terraform" note is present; note the legacy flag still exists during transition.
5. **CHANGELOG**: user-facing — leads with the faster watch-driven BNK phase + live `bnk status`; notes the legacy terraform path remains behind a flag this release and (per the integrator) that it's on a feature branch / not yet default if applicable.

## Critical constraints
- No `internal/`/`cmd/`/`terraform/` code edits — reflected-doc reconciliation against the binary.
- Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da` convention.
- mdbook builds clean (docker image). Do not commit to main; do not tag. End your `## Closure — tech-writer, <date>` with an explicit **GREEN / RED** verdict (GREEN signals the feature branch is doc-complete; the integrator still gates merge on the live correctness + speed verify).
