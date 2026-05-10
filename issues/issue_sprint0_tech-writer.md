# Sprint 0 — tech writer issues

## Issue 1: cspell.json typo — "SSC" should be "SCC"
**Severity**: medium
**Status**: open
**Description**: `cspell.json` line 23 lists `"SSC"` in the allowed-words array. The acronym used everywhere in the project is **SCC** (OpenShift Security Context Constraint). The misspelling lets typos of "SCC" → "SSC" through the spell check while genuine uses of "SCC" (already in `book/src/26-troubleshooting.md`, `book/src/30-glossary.md`, and `docs/PLAN.md` lines 348 / 380 / 469 / 473 / 599) will start tripping the spell check the moment Sprint 4's iperf3-on-OpenShift work lands real prose around SCC violations. Currently silent because spellcheck.yml runs `continue-on-error: true`, so reviewers are unlikely to notice until the warning becomes load-bearing.
**Files affected**: `/mnt/d/project/roksbnkctl/cspell.json` (line 23)
**Proposed fix**: Replace `"SSC"` with `"SCC"`. One-character edit. Consider also adding common companion terms on the same pass: `kube-apiserver`, `restricted-v2`, `securityContext` are already glossary-bound for Sprint 4.

## Issue 2: CONTRIBUTING.md missing the "How to add a chapter" section
**Severity**: medium
**Status**: open
**Description**: `docs/PLAN.md` line 101 explicitly lists this as a Sprint 0 deliverable: *"CONTRIBUTING.md: 'How to add a chapter' section: edit `SUMMARY.md`, drop a markdown file in `book/src/`, link from a feature PR"*. The current `CONTRIBUTING.md` covers Running tests, Pre-commit hook, Code style, and Long-running smoke test — none of which mention the book. A first-time contributor adding a chapter has no documented workflow. PLAN.md line 605 (Risks: "Book chapter drift behind code") explicitly relies on this guidance landing so feature PRs include the matching chapter update.
**Files affected**: `/mnt/d/project/roksbnkctl/CONTRIBUTING.md`
**Proposed fix**: Add a "Working on the book" section to `CONTRIBUTING.md` covering: (1) `mdbook serve book/` for local preview (or `make book-serve`), (2) editing `book/src/SUMMARY.md` + dropping a new chapter markdown file, (3) the convention that feature PRs include the matching chapter update, (4) the `cspell.json` word-list path for new acronyms. Five to eight lines is enough; this is a Sprint 0 onboarding gap, not a Sprint 7 polish item.

## Issue 3: CONTRIBUTING.md doesn't mention the prompts/ folder or agent dispatch pattern
**Severity**: low
**Status**: open
**Description**: The repo uses a per-sprint agent dispatch pattern (`prompts/sprint<N>/<role>.md` + `issues/issue_sprint<N>_<role>.md` + `issues/resolved_sprint<N>_<role>.md`) that is essential context for any future contributor or LLM integrator. `prompts/README.md` documents it well, but `CONTRIBUTING.md` — the canonical onboarding doc — never mentions that this pattern exists. The tech-writer prompt explicitly flags this as a reasonable low-severity addition. A new human contributor opening `CONTRIBUTING.md` cold has no signpost to the sprint-execution workflow.
**Files affected**: `/mnt/d/project/roksbnkctl/CONTRIBUTING.md`
**Proposed fix**: One-paragraph "Sprint execution and the prompts/ folder" section near the bottom of `CONTRIBUTING.md` linking to `prompts/README.md` and `issues/README.md`. Two to four sentences; explain that sprint work runs through dispatched agent prompts, that issue files live under `issues/issue_sprint<N>_<role>.md`, and that the integrator aggregates output. Keeps the agent-dispatch pattern discoverable for future contributors.

## Issue 4: book.yml does not run on pull_request — PLAN.md says it should
**Severity**: medium
**Status**: open
**Description**: `docs/PLAN.md` line 107 specifies: *"Book CI: `mdbook test book/` runs on every PR — fails on broken internal links, malformed code blocks"*. The current `.github/workflows/book.yml` triggers only on `push: branches: [main]` with `paths: book/** + .github/workflows/book.yml`. There is no `pull_request:` trigger and no `mdbook test` step — only `mdbook build`. Result: a broken-internal-link PR can land on `main` before anyone notices. The miss is small today (32 stub chapters with no cross-links yet) but compounds the moment Sprint 1 starts publishing real content with cross-references.
**Files affected**: `/mnt/d/project/roksbnkctl/.github/workflows/book.yml`
**Proposed fix**: Either add a separate `book-validate` job that runs `mdbook test book/` (and optionally `mdbook build`) on `pull_request` with the same `paths:` filter, or extend the existing `build-deploy` job's trigger to include `pull_request` while gating the deploy step behind `if: github.event_name == 'push'`. The first option is cleaner.

## Issue 5: chapter 17 placeholder deviates from the documented `*Coming in Sprint X.*` pattern
**Severity**: low
**Status**: open
**Description**: 31 of the 32 chapter stubs use the exact pattern `*Coming in Sprint X.*` on the second line. Chapter 17 (`book/src/17-execution-backends.md`) instead reads `*Coming in Sprint 3 (intro) and Sprint 4 (full deep-dive).*`. This is defensible — PLAN.md does split chapter 17 across Sprint 3 (intro) and Sprint 4 (full) — but the inconsistency means a future grep for `Coming in Sprint 3.` to find Sprint-3-targeted chapters will silently miss this one. The tech-writer prompt explicitly calls out this consistency check.
**Files affected**: `/mnt/d/project/roksbnkctl/book/src/17-execution-backends.md` (line 3)
**Proposed fix**: Two acceptable resolutions: (a) normalize to `*Coming in Sprint 3.*` and add a one-line italic note in the body that the chapter receives a deep-dive expansion in Sprint 4; or (b) leave as-is and document the hybrid case in `prompts/README.md` so future tech-writer agents know to expect it. Option (a) keeps grep-ability; option (b) preserves the richer signal. Either is fine — the integrator should pick one and stay consistent.

## Issue 6: README has two consecutive blockquote lines separated by one paragraph
**Severity**: low
**Status**: open
**Description**: After the Sprint 0 README integration, the top of `README.md` reads: H1 → book-link blockquote (`> 📖 ...`) → tagline paragraph → status blockquote (`> **Status:** ...`). Two blockquote lines this close together render visually as a stack of pull-quotes that compete with each other for the reader's first eye. Not a bug — the prompt asked whether the insertion "flows with surrounding text"; it flows fine but loses some of the punch the original status blockquote had as the only callout above the fold.
**Files affected**: `/mnt/d/project/roksbnkctl/README.md` (lines 3 and 7)
**Proposed fix**: Optional. Either (a) demote the book link to a non-blockquote line under the H1 (e.g. `> 📖` → plain `📖 [Read the book] — ...`), or (b) merge the two blockquotes into a single status block that includes the book link as one of its lines. (a) is the lower-risk edit.

## Issue 7: cspell.json missing words that already appear in published chapter stubs
**Severity**: low
**Status**: open
**Description**: A handful of words are used in the chapter stubs and PLAN.md but not in `cspell.json`'s allow-list. With `continue-on-error: true` in `spellcheck.yml` they're warnings only, but the warning noise will grow as Sprint 1+ chapters fill in. Examples spotted in the stubs / preface: `tfvars` (used in chapters 13, 27, README, CONTRIBUTING), `kubeconfig` is already on the list but `kubeconfigs` (plural) might trip; `passthrough` / `passthroughs` (chapter 24); `cobra` (chapter 27); `securityContext` (Sprint 4 chapter 18 territory but not in any chapter yet); `restricted-v2` (Sprint 4); `staticcheck` is on the list ✓.
**Files affected**: `/mnt/d/project/roksbnkctl/cspell.json`
**Proposed fix**: Low-priority sweep — add `tfvars`, `passthrough`, `cobra`, `Mermaid`, `xrefs` (PLAN line 605) on the next pass. Since spellcheck is non-blocking today, this can ride along with the Sprint 1 chapter content rather than being its own change.

## Issue 8: CONTRIBUTING.md "Long-running smoke test" PHASE_FROM example uses an unverified phase letter
**Severity**: low
**Status**: open
**Description**: `CONTRIBUTING.md` line 82 shows `PHASE_FROM=D ./scripts/e2e-test.sh` as an example. `docs/E2E_TEST.md` documents phases A through F in the e2e plan; the script's actual phase letters were not cross-checked in this review. If a reader copies the example verbatim and the script doesn't accept `D` (or its semantics differ from what `docs/E2E_TEST.md` calls phase D), the example is misleading. Cosmetic risk only — would surface immediately on a real run.
**Files affected**: `/mnt/d/project/roksbnkctl/CONTRIBUTING.md` (line 82)
**Proposed fix**: Spot-check `scripts/e2e-test.sh` — confirm the `PHASE_FROM` env var exists and that `D` is a valid phase letter. If the script uses different letters, swap to a verified one (or use `B` since that's documented as the cluster-up phase in `docs/E2E_TEST.md`). Trivial verification, low effort.
