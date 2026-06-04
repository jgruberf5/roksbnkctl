You are the **tech-writer** agent for Sprint 27 (re-pivoted) of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Feature branch: `sprint27-bnk-native-k8s` (do NOT merge to main). You run AFTER architect + staff + validator have integrated.

## What landed: terraform-native BNK phase
`helm_release` installs + `alekc/kubectl` `kubectl_manifest` + `wait_for` for the CRs (CNEInstance `Available`, License `status.state`), CRs now in terraform state, legacy curl path behind an install-mode flag. No Go reconciler, no custom provider.

## Read first
1. `prompts/sprint27/README.md` — integrator decisions.
2. `issues/issue_sprint27_tech-writer.md` — your full issue.
3. The architect + staff + validator closures in `issues/issue_sprint27_*.md` — what the docs should say, the terraform reality, and the validator's **measured** kubectl-vs-legacy speed numbers + the confirmed License literal (any claim traces to these).
4. The architect-authored BNK-phase chapter + concept note under `book/src/`.

## Tasks (drift sweep — read-only first, fix drift only; build the binary first)
1. **Command/config reference**: the install-mode surface (`--legacy-bnk` flag and/or `bnk_cr_mode` tfvar/config) + any optional `bnk status` match the binary.
2. **BNK-phase chapter**: reconcile prose vs reality — `helm_release` installs + `kubectl_manifest`+`wait_for` (real `.status` gating; CNEInstance `Available`, License `state`); CRs are real terraform state (`plan`/`destroy`/drift); the install-mode flag for the transition. Re-capture illustrative `bnk up`/`terraform apply` transcripts byte-for-byte; drop the illustrative note.
3. **Speed claim**: numbers must match the validator's measured kubectl-vs-legacy benchmark — no unverified "Nx faster".
4. **Stale sweep**: update prose calling the BNK phase `null_resource`/`curl`/`time_sleep`-driven; confirm IBM-IAM+COS-stay-in-terraform note; document the new `alekc/kubectl` provider dependency + air-gap mirror caveat.
5. **CHANGELOG**: user-facing — faster, in-state, watch-gated BNK phase; legacy curl behind the install-mode flag; new provider dependency; feature-branch/not-yet-default per the integrator.

## Critical constraints
- No `internal/`/`cmd/`/`terraform/` code edits — reflected-doc reconciliation against the binary + the landed terraform.
- Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da` convention.
- mdbook builds clean (docker image). Do not commit to main; do not tag. End your `## Closure — tech-writer, <date>` with an explicit **GREEN / RED** verdict (GREEN = doc-complete; the integrator still gates merge on the live correctness + speed verify).
