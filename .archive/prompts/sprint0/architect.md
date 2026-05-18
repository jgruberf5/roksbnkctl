You are the architect agent for Sprint 0 of the roksbnkctl project. Set up the mdBook infrastructure for the web book titled "Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl".

Project location: `/mnt/d/project/roksbnkctl/`. This is a Go CLI tool that deploys F5 BIG-IP Next for Kubernetes onto IBM Cloud ROKS, with vendored Terraform under `terraform/`. The book will be the canonical user-facing documentation surface.

The full 32-chapter outline lives in `/mnt/d/project/roksbnkctl/docs/PLAN.md` — search that file for "Book outline" to find the complete chapter list across 9 Parts (Concepts, Getting Started, Cluster Lifecycle, Configuration, Remote Execution, Testing, Operations, Reference, Contributing). The same PLAN.md has a "Per-sprint book chapters (cumulative)" table mapping each chapter number to the sprint that will draft it — use that to write the right "coming in Sprint X" placeholder in each stub.

## Your tasks

**Coordinate with parallel agents**: A staff-engineer agent is editing internal/cli/doctor.go + .github/workflows/ci.yml + scripts/pre-commit.sh + Makefile (build/test/lint targets) + CONTRIBUTING.md (Style/Tests/Pre-commit sections). A validator agent is editing tools/docker/ + .github/workflows/spellcheck.yml + cspell.json + CONTRIBUTING.md (Smoke test section). Do not touch their files. For Makefile, append new targets only — do not edit existing targets. For CONTRIBUTING.md, do not write to it (the others will).

1. Create `book/book.toml` with:
   - `title = "Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl"`
   - `authors = ["roksbnkctl maintainers"]`
   - `language = "en"`
   - `src = "src"`
   - `[output.html]` section with `git-repository-url = "https://github.com/jgruberf5/roksbnkctl"`, `default-theme = "rust"`, `[output.html.search]` with `enable = true`

2. Create `book/src/SUMMARY.md` reflecting the 32-chapter outline from docs/PLAN.md. Use kebab-case filenames (e.g. `[What is BIG-IP Next for Kubernetes (BNK)](./01-what-is-bnk.md)`). Group by Part with `# Part I — Concepts` style headers (mdBook accepts these).

3. Create one stub markdown file per chapter under `book/src/`. Each stub:
   - `# Chapter title` h1 matching the SUMMARY entry
   - 2-3 line "*Coming in Sprint X.*" placeholder paragraph in italics, where X is the sprint slated to draft it (look up in docs/PLAN.md "Per-sprint book chapters" table)
   - For Sprint 7 chapters or those without a clear sprint, say "*Polished in Sprint 7 (book launch).*"

4. Create `book/src/preface.md` — a brief "How to read this book" intro: who it's for (BNK evaluators, F5 SEs, customer engineers), linear vs reference, prerequisites (basic IBM Cloud + Kubernetes familiarity).

5. Create `.github/workflows/book.yml` that:
   - Triggers on `push: branches: [main], paths: ['book/**', '.github/workflows/book.yml']`
   - Job: ubuntu-latest, steps:
     1. `actions/checkout@v4`
     2. `peaceiris/actions-mdbook@v2` with `mdbook-version: 'latest'`
     3. `run: mdbook build book/`
     4. `peaceiris/actions-gh-pages@v4` with `github_token: ${{ secrets.GITHUB_TOKEN }}`, `publish_dir: ./book/book`, `publish_branch: gh-pages`
     5. `permissions:` block at job level granting `contents: write`

6. Update `README.md` (one targeted edit only): add a single line near the top of the README, after the H1 "# roksbnkctl" but before the existing intro text:
   `> 📖 **[Read the book](https://jgruberf5.github.io/roksbnkctl/book/)** — _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_`

7. Update `Makefile` — APPEND ONLY (do not modify existing targets). Add at the bottom:
   ```
   .PHONY: book book-serve book-clean

   book:
       mdbook build book/

   book-serve:
       mdbook serve book/ --open

   book-clean:
       rm -rf book/book
   ```
   (Note: real Makefile recipe lines need literal tab indentation, not spaces.)

## Issue tracking

If you encounter ambiguities, conflicts, or things you can't complete cleanly, document them in `/mnt/d/project/roksbnkctl/issues/issue_sprint0_architect.md` using this format:

```markdown
# Sprint 0 — architect issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: what was found
**Files affected**: list of paths
**Proposed fix**: how to resolve
```

If everything goes cleanly, create the issues file with just the heading and a `*No issues filed.*` note.

## Verification before reporting done

- All chapter files referenced in SUMMARY.md exist (no broken links)
- `mdbook build book/` succeeds locally if mdBook is installed (try `which mdbook` first; if not installed, skip this check and note in the issues file)
- Existing files were not deleted; only README.md and Makefile were edited (append-only on Makefile)
- The book.yml workflow YAML is valid (no tabs in YAML, only spaces)

## Final report

Return a concise summary (under 200 words):
- Files created (counts + key paths)
- Files edited
- Whether mdbook was available locally and whether the build worked
- Whether you filed any issues
- Anything the integrator should be aware of

Do NOT commit anything. The integrator will commit the aggregated work.
