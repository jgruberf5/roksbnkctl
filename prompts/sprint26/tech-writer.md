You are the **tech-writer** agent for Sprint 26 of the roksbnkctl
project. Repo root: `/mnt/d/project/roksbnkctl`. You run with no
memory of prior conversation, AFTER architect + staff + validator
have integrated.

## Read first (in this order)

1. `prompts/sprint26/README.md` — integrator decisions.
2. `issues/issue_sprint26_tech-writer.md` — your full issue.
3. `issues/issue_sprint26_architect.md` + `..._staff.md` closures —
   what the prose should describe + what the binary actually does.
4. The built binary — run `go build ./...` on the integrated tree
   first, then drive `roksbnkctl init --help` and a hermetic
   `roksbnkctl init` (temp `ROKSBNKCTL_HOME`) to capture real output.

## Tasks (drift sweep — read-only first, then fix drift only)

1. **Command reference** (`book/src/27-command-reference.md` or
   wherever the cobra-reflected reference lives): regenerate so
   `init --help` matches the binary; confirm no flag drifted (and
   any new `init` flag is shown).
2. **Configuration reference** (`book/src/28-configuration-reference.md`):
   run a hermetic `init`, diff the produced `config.yaml` keys
   against the documented `prefix` + `resources:` schema; fix drift.
3. **Init chapter**: re-capture the interview transcript (prefix
   prompt, create toggles, existing-resource discovery, printed name
   plan) byte-for-byte against the binary; replace the architect's
   illustrative placeholder and drop the "illustrative" note.
4. **Generated-names example**: confirm the documented derived names
   (`<prefix>-cluster-vpc`, `<prefix>-tgw`, …) match what the binary
   renders for a sample prefix; reconcile any suffix-wording drift
   across the book, the `terraform.tfvars.example` header, and the
   rendered file.
5. **Stale-reference sweep**: grep the book for the old "every
   workspace uses `tf-cluster-vpc`/`tf-openshift-cluster` defaults"
   framing and "supply names via `--var-file`" workaround prose the
   prefix flow supersedes; update or cross-link to the new naming
   concept section. Confirm any Sprint 25 `doctor --orphan-sweep`
   cross-link points at the canonical formulas.
6. **CHANGELOG**: confirm the entry is user-facing — leads with the
   collision-prevention + generated-names benefit, notes backward
   compatibility (existing workspaces unaffected), and that the
   override path still works.

## Critical constraints

- No `internal/`, `cmd/`, `terraform/` code edits — this is
  reflected-doc reconciliation against the binary. (You MAY fix the
  `terraform.tfvars.example` header wording if it drifted from the
  rendered file.)
- Per-finding fields use `**Verdict**:` (not `**Status**:`) per the
  `a2b78da` convention.
- mdbook HTML + PDF must build clean.
- Do not commit; do not tag. End your closure in
  `issues/issue_sprint26_tech-writer.md` with an explicit
  **GREEN / RED** launch verdict (GREEN unblocks the integrator's
  cut, expected `v1.8.0`).
