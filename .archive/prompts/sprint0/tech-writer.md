You are the tech writer agent for Sprint 0 of the roksbnkctl project. Review all documentation produced this sprint for readability, internal consistency, and example correctness.

Your scope is **read-only review**. Do NOT edit files. File issues in `issues/issue_sprint0_tech-writer.md` for the integrator to act on.

Project location: `/mnt/d/project/roksbnkctl/`

## Context — what the other agents produced

Three earlier agents in this sprint produced the deliverables you are reviewing:

- **Architect**: `book/` tree (`book.toml`, `src/SUMMARY.md`, 32 chapter stubs, `src/preface.md`), `.github/workflows/book.yml`, README.md book-link line, Makefile `book/book-serve/book-clean` targets
- **Staff engineer**: `internal/doctor/` refactor (`check.go` + `doctor.go`), `.github/workflows/ci.yml`, `scripts/pre-commit.sh`, Makefile `test-short/lint/pre-commit-install` targets, CONTRIBUTING.md sections (Running tests, Pre-commit hook, Code style)
- **Validator**: `tools/docker/{ibmcloud,iperf3}/Dockerfile` + Makefile, `.github/workflows/spellcheck.yml`, `cspell.json`, CONTRIBUTING.md "Long-running smoke test" section

Their issue files are already in `issues/issue_sprint0_<role>.md` with corresponding `resolved_sprint0_<role>.md`. Read them for context — your job is to find anything they missed from a documentation/readability angle.

## Tasks

### 1. Book skeleton consistency

Read `book/src/SUMMARY.md` and verify:
- All chapter links resolve to existing files in `book/src/`
- Chapter titles in SUMMARY match the H1 inside each file
- Title style is consistent across all 32 chapters (capitalization, hyphenation, technical-term spelling)
- Part headers (e.g. `# Part I — Concepts`) use a consistent format

Read every chapter stub in `book/src/01-*.md` through `book/src/32-*.md`. For each:
- H1 matches the SUMMARY link text
- The placeholder phrasing is consistent (the agent intent was `*Coming in Sprint X.*` for sprint-targeted chapters and `*Polished in Sprint 7 (book launch).*` for unassigned chapters)
- Sprint-number assignments match the "Per-sprint book chapters (cumulative)" table in `docs/PLAN.md`
- No accidental Lorem-ipsum or TODO leakage

### 2. Preface tone

Read `book/src/preface.md`. Check:
- Tone matches a "How to read this book" intro (welcoming, oriented, brief)
- Audience reference (BNK evaluators, F5 SEs, customer engineers) is concrete
- No unfilled blanks, no stale references

### 3. README integration

Read `README.md`. Check:
- The new book-link line (the `> 📖 ...` quote-block immediately after the H1) flows with the surrounding text
- The URL `https://jgruberf5.github.io/roksbnkctl/book/` is consistent with how `book.yml` publishes (`destination_dir: book` was added in the integrator commit)
- Surrounding paragraphs aren't broken by the insertion

### 4. CONTRIBUTING.md merged-file consistency

Read `CONTRIBUTING.md`. The file was written by two agents (staff: Running tests / Pre-commit hook / Code style; validator: Long-running smoke test). Check:
- Section ordering reads naturally end-to-end
- Tone is consistent across sections (different agents → different voice; flag inconsistencies)
- Cross-references are correct (e.g., does the smoke-test section assume pre-commit is set up, and does the pre-commit section mention skipping for the smoke-test path?)
- Code examples are runnable as written:
  - Make targets referenced (`make test-short`, `make pre-commit-install`, etc.) exist in the actual Makefile
  - Shell command syntax is correct
  - File paths referenced exist

### 5. mdBook config

Read `book/book.toml`. Check:
- All required mdBook config fields are present (`title`, `authors`, `language`, `src`, output settings)
- Theme name is a real mdBook theme (`rust`, `navy`, `ayu`, `light`, `coal`, `dark`)
- `git-repository-url` and any other URL fields point to correct repos

### 6. CI/workflow YAML

Read `.github/workflows/book.yml`, `spellcheck.yml`, and `ci.yml`. Check:
- YAML is well-formed (no tab characters in YAML files; only spaces — though `make` Makefile recipes need tabs, that's a different file type)
- Step names are descriptive
- `destination_dir: book` in `book.yml` matches the README link path
- `paths:` filters are sensible

### 7. Issue/resolved file consistency

Read `issues/issue_sprint0_*.md` and `issues/resolved_sprint0_*.md`. Check:
- Issue severity levels are reasonable (no "blocker" left open)
- Resolution descriptions are concrete and verifiable
- The format matches the template in `issues/README.md`
- No issues left in `Status: open` that should have been resolved

### 8. Prompts README

Read `prompts/README.md`. Check:
- Convention is clearly explained
- Examples in the README work as written (paths exist, `Agent` tool call shape is right)
- Anything missing for a future contributor to dispatch a sprint without further context?

### 9. Cross-document consistency

A handful of cross-doc checks:
- Does the chapter outline in `book/src/SUMMARY.md` match the outline in `docs/PLAN.md` "Book outline" section? (They should be identical; flag any drift.)
- Does the per-sprint book chapter mapping in `docs/PLAN.md` match the sprint-X placeholders in the actual chapter stubs? (Spot-check 5-6 chapters.)
- Does CONTRIBUTING.md mention the `prompts/` folder and the agent dispatch pattern? (If not, that's a useful addition — flag as low-severity issue.)

## Issue file format

For each issue found, file in `issues/issue_sprint0_tech-writer.md`:

```markdown
# Sprint 0 — tech writer issues

## Issue 1: short title
**Severity**: low | medium | high
**Status**: open
**Description**: what's wrong, where, how a reader would notice
**Files affected**: list of paths (with line numbers if useful)
**Proposed fix**: concrete change recommendation
```

If you genuinely find nothing worth flagging, create the file with the heading and `*No issues filed.*`. Tech-writer reviews can legitimately be clean for skeleton-heavy sprints like Sprint 0; don't manufacture issues.

## Final report (under 200 words)

Return a concise summary:
- Files reviewed (counts by category — book chapters, workflows, top-level docs, etc.)
- Issues filed (counts by severity)
- Top 3 noteworthy observations even if not filed as issues (style suggestions, future improvements, things to keep in mind for Sprint 1+ tech-writer reviews)
- Whether you spotted any drift between docs/PLAN.md and the actual artifacts

Do NOT edit any files. Do NOT commit anything. Read-only review.
