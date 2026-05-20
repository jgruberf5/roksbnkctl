You are the **architect** agent (light) for Sprint 19 of the
roksbnkctl project. Repo root: `/mnt/c/project/roksbnkctl`. You run
with no memory of prior conversation.

## Read first

1. `prompts/sprint19/README.md` — integrator decisions; your scope
   is the book chapter that introduces `init` + the auto-generated
   CLI reference.
2. `issues/issue_sprint19_architect.md` Issue 1 — the
   authoritative spec for what doc work to land.
3. `book/src/SUMMARY.md` — find the chapter that walks the
   first-time user through `roksbnkctl init`. Likely chapter 04, 05,
   or 06; verify by checking each file's heading.
4. `book/src/27-command-reference.md` — auto-generated; the file's
   header says "re-run on every CLI surface change". Sprint 18 had
   the same regen requirement when `cos bucket get` landed.

## Tasks

1. **Update the init chapter** (the one SUMMARY.md cites) with a
   §"Skip the interview: `init --var-file`" subsection covering:
   - When to use it (you already have a `terraform.tfvars` from a
     prior run / a colleague / `cp terraform.tfvars.example
     terraform.tfvars && $EDITOR terraform.tfvars`).
   - The single-command flow:
     `roksbnkctl init -w myws --var-file ./terraform.tfvars`.
   - What gets persisted: `config.yaml` (seeded from the tfvars's
     `ibmcloud_cluster_region` / `openshift_cluster_name` / etc.)
     **and** a copy of the var-file at both phase state dirs as
     `terraform.tfvars.user`.
   - Why it matters: subsequent `up` / `plan` / `apply` / `down`
     against `-w myws` alone Just Work — no `--var-file` re-passing.
     `--var-file <path>` on later commands still overrides.
   - **Secrets on disk note**: the operator's `ibmcloud_api_key`
     lands at `~/.roksbnkctl/<ws>/state/terraform.tfvars.user` (and
     the cluster-state sibling), mode `0600`. Same posture as the
     repo-root `./terraform.tfvars` they're copying from, just under
     workspace-state instead.
   - **Diagnostics paragraph** at the end: "If `down -w <ws>` (no
     `--var-file`) errors with `No value for required variable …`,
     either the workspace was `init`-ed without `--var-file` or the
     `terraform.tfvars.user` was removed. Re-run
     `roksbnkctl init -w <ws> --var-file <path>` to re-seed, or
     pass `--var-file` on the destroy."
2. **Regenerate `book/src/27-command-reference.md`** via
   `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`
   so the new flag is in the canonical reference. (Run from repo
   root.) This is mechanical; no manual editing.
3. **Cross-reference sweep**: if other chapters say "pass
   `--var-file ./terraform.tfvars` on every command" (chapter 25's
   COS chapter, the existing `init` chapter, the lifecycle chapter,
   etc.), update them to mention the new `init --var-file` flow as
   the recommended path. Do not rewrite chapters; just add or fix a
   sentence where the existing prose is now incomplete.

## Constraints

- **Touch only `book/src/**`**. Do not edit Go code, scripts,
  workflows, or `book.toml`. The book pipeline is correct as-is
  (Sprint 18 settled it).
- Do not commit. Integrator commits.
- Do not run `gh issue create`.

## Verify before reporting done

- The init chapter has a new §"Skip the interview: `init
  --var-file`" subsection.
- `book/src/27-command-reference.md` includes the new `--var-file`
  flag on `init` (grep for `--var-file` in the init subsection).
- Diff is content-only — no behaviour-change to the docs build
  pipeline. `make book-pdf BOOK_BACKEND=docker` is not required to
  run this round (the regen + chapter updates are pure markdown);
  the integrator may verify the PDF as part of release prep.

## Issue file

Append a **Closure** section to
`issues/issue_sprint19_architect.md` documenting: which book chapter
got the new subsection, the cross-references you touched, and the
regen verification.

## Final report

≤150 words: the chapter you updated, the cross-refs touched, regen
verification, and any judgement call worth integrator attention.
State explicitly: did not commit, did not touch Go code.
