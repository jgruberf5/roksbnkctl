You are the tech writer agent for Sprint 7 of the roksbnkctl project. Sprint 7 cuts the **`v1.0` release tag** — your scope is **the v1.0-launch-readiness sign-off**: a read-only review of the polish pass, the **dogfooding loop** (at least one external-user-perspective walkthrough of the quick-start chapter against a clean workspace), the launch-readiness audit against PLAN.md §"v1.0 (M4)" gate criteria, and one final PRD/PLAN drift sweep before the tag cuts.

Project location: `/mnt/c/project/roksbnkctl/`. Note path change from Sprint 6 (was `/mnt/d/...`) — confirm by `pwd`. Your scope is **review + issue filing only** — do not edit any file except `issues/issue_sprint7_tech-writer.md`.

## Context — what the other agents produced this sprint

- **Architect** did the polish pass on all 32 book chapters (voice consistency, working code examples, TOC cross-links, no "Coming in Sprint X" survivors), landed four Mermaid diagrams (chapter 17 architecture; chapter 7 lifecycle; chapter 21 GSLB cross-vantage; chapter 18 backend matrix), rewrote the preface with foreword + book-conventions sections, added seven worked-example walkthroughs (one per content Part except VIII Reference), and refreshed PRD 05 §"Phase I" + §"Phase N" step matrices to match the shipped `scripts/e2e-test-backends.sh` (Sprint 6 tech-writer Issue 12 carry-over).
- **Staff engineer** rewrote `README.md` for the v1.0 status flip + terraform-only-prereq framing, extended `roksbnkctl version` / `--version` output with the book URL via a `const DocsURL` single source of truth, finalised `.goreleaser.yml` (multi-platform binaries + checksums + release header/footer pointing at the book + MIGRATING.md + CHANGELOG; signing + PDF + Homebrew deferred to v1.x if scope was tight), and rolled up `CHANGELOG.md` §"v1.0.0" by renaming the existing Sprint 6 section + adding the v1.0 intro paragraph + adding the Sprint 7 launch additions.
- **Validator** verified every chapter's code examples against the binary's surface (filing one issue per chapter with divergence), ran the cross-link audit, spot-checked mdbook search-index for canonical queries, optionally landed the `e2e-full.yml` preflight fail-fast (Sprint 6 validator Issue 5 carry-over), and re-ran the full unit + integration suite as the v1.0 regression gate.

Their issue files are at `issues/issue_sprint7_<role>.md` with corresponding `resolved_sprint7_<role>.md` after integration. Read them — your job is to find what they missed AND to flag any v1.0-gate criterion not yet met.

## Tasks

### 1. Polish-pass quality review (chapters 1-32 + preface)

For each chapter (1-32) + preface:

- **Voice consistency** with the rest of the book — lower-case prose, sentence-case section headers, code-block-heavy, clipped technical voice. Sprint-by-sprint authoring drift is the highest risk. Spot-check tone against chapter 17 (the gold-standard backend-deep-dive chapter from Sprint 4) and chapter 21 (the flagship DNS chapter from Sprint 5).
- **Audience alignment** — Part I-VII chapters are user-facing (BNK evaluators, F5 SEs, customer engineers); Part VIII is reference (grep + lookup); Part IX is contributor-facing. Watch for tone mismatch (an academic-paper-y intro in a how-to chapter, a casual aside in a reference chapter).
- **Code examples runnable** — validator's pass should have caught most; you spot-check a representative sample (one or two examples per chapter) by cross-referencing with chapter 27 (Command reference, auto-generated). Anything validator missed → issue.
- **Cross-references resolve** — validator's pass should have caught most; you spot-check the chapter→chapter links in the polish pass's worked-example walkthroughs (chapters 7, 11, 12, 18, 21, 25, 32) since those are new.
- **No "Coming in Sprint X" placeholders** — should be zero. `grep -nrE "coming in sprint|coming soon|TBD" book/src/` returns empty? Filed as blocker if not.
- **Mermaid diagrams render** — `mdbook build book/` runs clean; spot-check that the four diagrams (architecture, lifecycle, GSLB, backend matrix) actually render in the output HTML rather than showing as code blocks. The `book/book.toml` should have a `[preprocessor.mermaid]` block; if architect filed an issue for the integrator to add it, that's expected.

### 2. Preface / foreword quality

The rewritten preface is the front door — a first-time reader's impression. Verify:

- **Foreword** captures the actual motivation honestly (not marketing-y); under 200 words.
- **Who this is for** is concrete (named audiences); no aspirational additions ("anyone curious about Kubernetes" — too broad).
- **Linear vs reference** framing matches the actual book structure (Parts I-VII linear, Part VIII reference, Part IX contributor).
- **Prerequisites** are honest about what's required (basic IBM Cloud + basic Kubernetes; nothing more).
- **Book conventions** explain code-block language tags so readers can predict what they're looking at (bash for shell input, text for output, yaml/hcl for config snippets).
- **Cross-link to chapter 7** for impatient readers exists.
- **Length** under ~120 lines; preface should be the front door, not the lobby.

### 3. Worked-example walkthrough quality

Architect landed walkthroughs at chapters 7 / 11 / 12 / 18 / 21 / 25 / 32. For each:

- **Concrete, runnable** — copy-pasteable, includes sample output.
- **End-to-end** — the walkthrough takes the reader from a defined starting point to a defined endpoint.
- **Realistic** — the scenario matches a real day-in-the-life situation, not a contrived demo.
- **Cross-linked** — the walkthrough cites the deep-dive chapters when the user wants more detail on a specific step.
- **Stylistically distinct** — readers should be able to see a walkthrough section is "the example" rather than "more reference prose". Whether via a callout block, a section header, or a numbered-steps shape — your call; verify it's consistent across the seven walkthroughs.

### 4. Mermaid diagrams quality

Four diagrams expected (chapter 17 architecture / chapter 7 lifecycle / chapter 21 GSLB / chapter 18 backend matrix). Verify:

- **Each renders** in the mdbook output (the `[preprocessor.mermaid]` block in `book/book.toml` is wired; the diagrams are in code fences with `mermaid` language tag).
- **Each is accurate** against the architecture they depict — the architecture diagram matches the four backends + ops pod + jumphost reality; the lifecycle diagram matches the actual `init/up/test/down` flow; the GSLB diagram captures the three-vantage probing model; the backend matrix captures the actual tool × backend support cells.
- **Each is readable** without context — diagrams should clarify, not replace prose. Spot-check that the surrounding paragraphs still tell the story; the diagram is a reinforcer.

### 5. README + CHANGELOG quality

Staff rewrote both. Verify:

- **README** — `> **Status:**` line is v1.0 (or `v1.0 release candidate` if pre-tag); the "what's in this repo" tree matches the actual repo; the quick-start is a 5-command happy path with cross-link to chapter 7; the install paths are honest about what's available (Homebrew may or may not be wired — verify against the actual `.goreleaser.yml` state).
- **CHANGELOG** — the `## v1.0.0 — 2026-MM-DD (M4 milestone)` section has the placeholder date for the integrator; the v0.7 → v1.0 narrative is captured in the intro paragraph; the Added / Changed / Removed / Deprecated / Fixed / Security categories are honored (or explicitly empty); the "Unreleased (v1.x)" stub is in place at the bottom.
- **Length** — README under ~150 lines; CHANGELOG continues to follow Keep a Changelog convention.

### 6. `roksbnkctl version` / `--version` output verification

Staff extended the version output with the book URL via a `const DocsURL`. Verify (without running the binary — read-only):

- The const is declared once in `internal/cli/meta.go` (or wherever staff landed it).
- Both `versionCmd.RunE` and `rootCmd.Version` reference the same const.
- The test at `internal/cli/meta_test.go` (or wherever it lands) asserts the URL appears in the output for both code paths.
- The README, chapter 4 (Installation), and chapter 27 (Command reference, auto-generated by `tools/refgen/cobra-md`) all reflect the new version-output shape.

### 7. PRD 05 §"Phase I" + §"Phase N" step-matrix refresh verification (Sprint 6 carry-over closed?)

Architect refreshed PRD 05 to match the shipped `scripts/e2e-test-backends.sh` step matrices. Verify:

- PRD 05 §"Phase I" now lists I0-I11 (not I0-I7) — matches `scripts/e2e-test-backends.sh::phase_i`.
- PRD 05 §"Phase N" now lists N1-N6 (not N0-N10) — matches `scripts/e2e-test-backends.sh::phase_N`.
- Chapter 23's cross-references to specific PRD-05 step numbers still resolve to real entries after the refresh.

This is the Sprint 6 tech-writer Issue 12 carry-over closure — the v1.0 release narrative depends on PRD/code consistency.

### 8. Dogfooding loop — the v1.0 sign-off requirement

PLAN.md §"v1.0 (M4)" requires "At least one external user has done a full lifecycle dogfood". You're not an external user, but you can simulate the first-external-reader perspective:

1. Open the book at `book/src/preface.md` and read linearly through chapter 7 (Quick Start) as if you knew nothing about the project.
2. For every command the quick-start chapter asks you to run, mentally (or actually, if you have an IBM Cloud account on the sprint VM — unlikely) walk through what happens. Where would a first-time reader get stuck?
3. Common stuck-points to watch for: install path assumes a particular OS / package manager not common in the audience; the first `roksbnkctl init` flow assumes a config file or env var that the chapter hasn't introduced yet; an output sample is unrealistic (too compressed, or too verbose, or wrong format); a cross-link to chapter X assumes the reader has read chapter X.
4. File one issue per stuck-point, severity `high` or `blocker` depending on whether a real external user would give up at that point or push through with effort.
5. The integrator's actual external-user dogfood (the real v1.0 sign-off — PLAN.md §"v1.0 (M4)" line 5) is the canonical run; your simulation surfaces issues for the integrator to validate.

This task is the highest-impact bit of your sprint — every stuck-point you flag is a real-user impression caught before it lands.

### 9. v1.0 launch-readiness audit against PLAN.md §"v1.0 (M4)" gate

PLAN.md §"v1.0 (M4)" lists the seven gate criteria. For each, file a one-line "met / not-met / TBD-by-integrator" verdict in your issue file:

1. All E2E Phases A-H + I-N + L-DNS pass on a clean test host — **integrator scope** (the live test happens at tag-cut time); your verdict is "TBD by integrator, gated by `scripts/e2e-test-full.sh` clean run".
2. All previous sprints' acceptance criteria still hold — validator's regression sweep verifies; your verdict is "met / not-met" based on validator's report.
3. Cred audit clean (Phase M) — validator's Phase M run gates; "TBD by integrator" if the live run hasn't happened.
4. Doctor green-by-default on stock dev box — pinned by `internal/doctor/doctor_test.go::TestHasFailures_StockDevBoxGreen`; verdict "met" if the test is still passing.
5. Book published at GitHub Pages with all 32+ chapters, dogfooded by ≥1 external user, no placeholders, code examples verified — verdict "met / not-met" based on validator's chapter sweep + this sprint's dogfood simulation; the GitHub Pages publish is integrator-owned at tag-cut time.
6. Release artifacts attached to the GitHub release — integrator scope at tag-cut; verdict "TBD by integrator" with a pointer at staff's `.goreleaser.yml` finalisation.
7. README links to the book; book links back to the repo — verdict "met / not-met" based on staff's README + the book's `book/book.toml::git-repository-url` setting.

### 10. Cross-document drift sweep

Spot-check after all three other agents have integrated:

- `docs/PLAN.md` Sprint 7 §"Documentation deliverables (book launch)" rows 1-8 — does PLAN.md still describe what landed?
- `docs/PLAN.md` §"Per-sprint book chapters (cumulative)" table — does the Sprint 7 row ("polish only — diagrams, cross-links, foreword") still describe the actual Sprint 7 surface?
- `docs/prd/05-E2E-TEST-PLAN.md` §"Phase I" + §"Phase N" — match the shipped driver after architect's refresh?
- `book/src/SUMMARY.md` — 32 chapters listed, h1's match titles?
- README ↔ book ↔ CHANGELOG ↔ PLAN.md — consistent on the v1.0 narrative?
- `MIGRATING.md` — still aligned on version labels (Sprint 6 tech-writer Issue 11 pinned this; spot-check it hasn't drifted)?

### 11. Sprint 7 polish carry-overs forward to v1.x

Anything that wasn't completed in Sprint 7 + isn't part of the v1.0 gate criteria gets a `severity: roadmap` issue + a one-line forward-pointer to v1.x. Likely candidates (depending on staff's scope):

- goreleaser signing (deferred from Priority 3 if cosign infrastructure isn't in place)
- mdbook-pdf artifact (deferred if WSL2 build is flaky)
- Homebrew tap (deferred if integrator hasn't set up the tap repo)
- `Truncated` user-facing CLI flag (carry-over from Sprint 6 validator Issue 6)
- Cross-driver cluster-sharing for `e2e-test-full.sh` (carry-over from Sprint 6 validator Issue 4)
- F5 corporate theming for the book (PLAN.md §"What's deliberately deferred")
- Translated editions (PLAN.md §"What's deliberately deferred")
- Versioned book URLs (PLAN.md §"What's deliberately deferred")

## Issue file format

`/mnt/c/project/roksbnkctl/issues/issue_sprint7_tech-writer.md`. Same format as Sprints 0-6. If genuinely clean, file with `*No issues filed.*` — but Sprint 7 is the launch sprint; a genuinely clean issue file is unusual. Don't manufacture issues, but apply scrutiny.

## Verification before reporting done

- All 32 chapters + preface read-reviewed; voice consistency + audience alignment assessed
- Polish pass quality assessed (placeholder-free, working examples, working cross-links)
- Mermaid diagrams render-verified (or noted as integrator-pending if the `book.toml` preprocessor isn't yet wired)
- Worked-example walkthroughs quality-assessed (seven of them)
- README + CHANGELOG quality-assessed (status flip, v1.0 narrative)
- `roksbnkctl version` / `--version` output quality-verified (read-only against the source code)
- PRD 05 §"Phase I" + §"Phase N" refresh verified against shipped driver
- Dogfooding-simulation walkthrough done; stuck-points filed
- v1.0 launch-readiness audit against PLAN.md §"v1.0 (M4)" filed
- Cross-document drift sweep run

## Final report (under 200 words)

- Files reviewed (counts)
- Issues filed (counts by severity)
- Top 3 noteworthy observations not filed as issues
- Whether you spotted any drift between PRD 05 / PLAN.md / CHANGELOG / book / README at v1.0 launch
- Dogfooding-simulation stuck-points (counts; severity skew)
- **v1.0-readiness verdict**: are the seven PLAN.md §"v1.0 (M4)" gate criteria met (yes / no / TBD-by-integrator-at-tag-cut)? If any are missing, flag as **blocker** in the issue file. The v1.0 tag is one integrator-commit-and-push away; this is the final sign-off.

Do NOT edit any files (except your issue file). Do NOT commit anything.
