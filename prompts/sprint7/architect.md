You are the architect agent for Sprint 7 of the roksbnkctl project. Sprint 7 is **the launch sprint** — the book at `https://jgruberf5.github.io/roksbnkctl/book/` ships alongside the `v1.0` binary tag, and your scope is **the polish pass on all 32 chapters + the preface rewrite + Mermaid diagrams + worked-example walkthroughs + the PRD 05 step-matrix refresh carry-over from Sprint 6**.

Project location: `/mnt/c/project/roksbnkctl/`. Note the path change from Sprint 6 (was `/mnt/d/...`) — confirm by `pwd` before editing. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25.

Sprint 7 cuts the **`v1.0` release tag** at end-of-sprint. Your chapters must be true of the binary that ships at v1.0 — if a code example or feature description is wrong, the dogfooding loop will surface it and we'll have to either patch v1.0 or block the tag. Aim for "no surprises for the first external reader" rather than "perfect prose".

## Read first

- `docs/PLAN.md` §"Sprint 7" — your authoritative deliverables list (rows 1-8 under "Documentation deliverables (book launch)").
- `docs/PLAN.md` §"v1.0 (M4)" — the gate criteria your work must meet.
- `book/src/SUMMARY.md` — the 32-chapter TOC. Confirm titles match each chapter's h1 (Sprint 6 tech-writer already pinned this; verify it still holds after your polish).
- `book/src/preface.md` — current 31-line preface; Sprint 7 expands this with a proper foreword (motivation + who this is for + how to read it). Read it first; the existing structure is sound and worth keeping.
- All 32 chapter files at `book/src/{01..32}-*.md` — your polish surface.
- `issues/resolved_sprint6_*.md` — every Sprint 6 resolution note. Several of them touched chapters you'll be polishing (chapter 22 SCC reorder, chapter 23 phase tier renumbering, chapter 26 troubleshooting entries, chapters 27 + 29 auto-generated content). Don't undo any of those fixes.
- `docs/prd/05-E2E-TEST-PLAN.md` §"Phase I" + §"Phase N" — the **step-matrix carry-over** from Sprint 6 tech-writer Issue 12. PRD lists I0-I7 + N0-N10; shipped driver implements I0-I11 + N1-N6. PRD prose needs to match shipped.
- `scripts/e2e-test-backends.sh` — the actual shipped Phase I + N step sequences. Diff against PRD 05's tables to land the correct refresh.
- `prompts/sprint6/architect.md` for prompt-structure reference; `prompts/sprint6/README.md` for sprint-cadence reminders.

## Coordinate with parallel agents

A **staff engineer** agent is rewriting `README.md` for the v1.0 status flip (terraform-only prereq framing, book-URL banner upgrade); adding the book URL to `roksbnkctl --version` / `roksbnkctl version` output via the `internal/cli/meta.go` flow + matching test; finalising `.goreleaser.yml` for multi-platform binaries + signing + checksums + optional PDF book artifact attached to the GitHub release; and producing release notes at `CHANGELOG.md` §"v1.0.0" (rolling up the existing "Unreleased — Sprint 6" section + summarising v0.7 → v1.0). **Do not touch their files.**

A **validator** agent is re-verifying every `roksbnkctl ...` code example in every chapter against a fresh workspace + a real cluster where possible; doing the cross-link audit (every `[Chapter X](./XX-...)` resolves; every anchor links to the actual mdbook-derived slug); spot-checking mdbook search for canonical queries (`GSLB`, `jumphost`, `kubeconfig`, `--backend k8s`, `--on jumphost`); and optionally polishing the `e2e-full.yml` workflow preflight fail-fast. They'll file issues against any chapter whose code examples diverge from the binary's actual surface — you fold those into the polish pass.

A **tech-writer** agent does read-only review at the end of the sprint, including a **dogfooding loop** against the quick-start chapter from a clean workspace's perspective. Their issue file is the v1.0-launch-readiness sign-off; your polish needs to clear it.

**Your scope** is everything under `book/src/`, the PRD 05 §I/§N step-matrix refresh, and (if validator surfaces an issue) chapter-23's disk-size estimate refinement.

## Tasks

### 1. Preface / foreword rewrite

The existing `book/src/preface.md` is 31 lines covering "how to read this book", "who this book is for", "linear vs reference", and "prerequisites". Sprint 7 expands this into a proper preface that the first external reader encounters before chapter 1:

- **Foreword** — what motivated the tool. The honest framing: BNK on ROKS is a multi-step deployment that historically required `terraform init/plan/apply` against an HCL tree, manual kubeconfig fetches, manual IBM Cloud CLI installs, manual iperf3 SCC fixes, manual cred plumbing across SSH/k8s/docker surfaces. roksbnkctl collapses that into a single binary + four execution backends + the in-cluster ops pod. Keep this concrete and under 200 words; readers want the tool, not the autobiography.
- **Who this book is for** — keep the existing three audiences (BNK evaluators, F5 SEs, customer engineers) but tighten the prose; consider adding a fourth (contributors who want to extend the tool — pointer to Part IX).
- **How to read it (linear vs reference)** — keep the existing framing; cross-link to the quick-start chapter for impatient readers ("If you have 30 minutes and an IBM Cloud account, skip to [Chapter 7 — Quick Start](./07-quick-start.md)").
- **Prerequisites** — keep the existing list; add a "what you don't need to know" callout for clarity (no prior Terraform / OpenShift / F5 BIG-IP Next experience required).
- **Book conventions** — new short section: code blocks use `bash` syntax for shell commands; `yaml` for `config.yaml` snippets; `hcl` for terraform; output is shown in `text` blocks to distinguish from input.

The existing preface is at `book/src/preface.md`; rewrite in place. Keep it under ~120 lines total — the preface should be the front door, not the lobby.

### 2. Mermaid diagrams

Per PLAN.md §"Sprint 7" row 2 (Documentation deliverables), four authoritative diagrams must land in version-controlled Mermaid form. mdBook renders Mermaid via the `mdbook-mermaid` preprocessor — check whether `book/book.toml` already has the `[preprocessor.mermaid]` block; if not, add it (the integrator will handle the `mdbook-mermaid install` step at integration time).

The four diagrams:

#### 2a. Architecture diagram (place in chapter 17, top-of-chapter)

A swimlane / box-and-arrows diagram showing the four execution backends side-by-side with their relationship to the cluster + ops pod + jumphost + IBM Cloud API. Mermaid `graph LR` or `graph TB`. Concrete elements: laptop (local backend) → docker-host (docker backend) → ROKS cluster (k8s backend + ops pod) → SSH jumphost (ssh backend) → IBM Cloud API. Each backend labelled with its tool list (local: everything; docker: ibmcloud, terraform; k8s: ibmcloud, iperf3, dns-probe Job; ssh: ibmcloud, iperf3). Cross-link from chapter 1 + chapter 17 + chapter 18 §"Choosing a backend".

#### 2b. Lifecycle diagram (place in chapter 7, top-of-chapter)

A simple sequence diagram showing the happy-path lifecycle: `init` (workspace dir + config) → `up` (terraform apply → cluster + BNK + cert-manager + jumphost) → `test` (connectivity / DNS / throughput) → `down` (terraform destroy). Mermaid `sequenceDiagram` or `graph LR`. Includes the embedded HCL → terraform-exec → IBM Cloud API → state-file persistence chain at a high level.

#### 2c. GSLB cross-vantage diagram (place in chapter 21)

The flagship DNS-testing chapter needs a diagram showing why GSLB cross-vantage probing matters: laptop (local vantage; sees public DNS / GSLB outside-the-cluster answer) + ops-pod (k8s vantage; sees cluster-internal CoreDNS + cluster-routed GSLB answer) + jumphost (ssh vantage; sees a third network path's GSLB answer). Each vantage feeds into `--gslb-compare`'s divergence detector. Mermaid `graph TB`. Refer to PRD 03 §"DNS probe" for the model the diagram should reflect.

#### 2d. Execution-backend matrix (place in chapter 18)

A 2D matrix showing tools (rows: terraform, ibmcloud, kubectl, oc, iperf3, dig/dns-probe) × backends (columns: local, docker, k8s, ssh) with cell content indicating support level (✓ supported / default, ◐ supported / opt-in, ✗ not supported, ⊘ N/A). This may render better as a markdown table than as Mermaid — your call; if a markdown table, drop the Mermaid framing and just polish the existing chapter-18 table to be the authoritative one.

Land each diagram inline at the natural insertion point in the chapter's existing structure (not as a separate diagrams chapter). Reference back to the architectural prose around them; the diagram clarifies, it doesn't replace.

### 3. Worked-example walkthroughs per Part

PLAN.md §"Sprint 7" row 4 says "Worked example walkthroughs in each Part — concrete end-to-end scenarios users can copy-paste". One per Part (nine Parts total in the SUMMARY.md):

- **Part I (Concepts)**: the quick-start chapter (chapter 7) is already a concrete walkthrough — the polish pass should make it the canonical "first 30 minutes" walkthrough. Cross-link from chapters 1 + 2 + 3.
- **Part II (Getting Started)**: chapter 7 doubles as Part II's walkthrough. Keep the framing consistent (single happy-path scenario; sample output for each command).
- **Part III (Cluster Lifecycle)**: add a "Worked example: register an existing cluster, deploy BNK, tear down" walkthrough at the end of chapter 11. Concrete `cluster register` → `up` → `test` → `down` flow against a hypothetical pre-existing ROKS cluster.
- **Part IV (Configuration)**: add a "Worked example: bootstrap a workspace from scratch" callout at the end of chapter 12 — `roksbnkctl init` → editing `config.yaml` → `roksbnkctl tfvars` → setting `IBMCLOUD_API_KEY` in keychain. Cross-link to chapter 14.
- **Part V (Remote Execution)**: add a "Worked example: bare metal + jumphost office workflow" at the end of chapter 18 — concrete `--on jumphost` + `--backend ssh:jumphost` + `--backend k8s` mix-and-match for a real day-in-the-life scenario.
- **Part VI (Testing)**: add a "Worked example: GSLB divergence troubleshooting" at the end of chapter 21 — a concrete scenario where `--gslb-compare` reveals an actual divergence and the steps the operator takes from there. Cross-link to chapter 26's troubleshooting entries.
- **Part VII (Operations)**: add a "Worked example: rotating COS supply-chain assets" at the end of chapter 25 — concrete `cos object put` of a new FAR image → swap the deployer reference → re-apply. Cross-link to chapter 14 for cred-rotation context.
- **Part VIII (Reference)**: no walkthrough — reference chapters are lookup surfaces; a walkthrough would be miscategorised.
- **Part IX (Contributing)**: add a "Worked example: adding a new execution backend" at the end of chapter 32 — concrete walkthrough of the `Backend` interface, where to register, what test fixtures to add. Already partially covered in chapter 32; tighten into a walkable example.

Aim for ~60-150 lines per walkthrough — enough to copy-paste, short enough to read in one sitting.

### 4. Polish pass on every chapter (1-32 + preface)

PLAN.md §"Sprint 7" row 1: "Polish pass on every chapter — consistent voice, working code examples, TOC cross-links, no 'coming in Sprint X' placeholders left". A pass-by-pass list:

- **Voice consistency** — lower-case prose, sentence-case section headers, clipped technical voice, code-block-heavy. Sprint-by-sprint authoring drift is the main risk; chapters from Sprint 1 and Sprint 6 should sound the same on a re-read. Spot-check tone against chapter 17 (the gold-standard backend-deep-dive chapter from Sprint 4).
- **Code-example freshness** — every `roksbnkctl ...` snippet should be a real command (validator will catch divergence; you fold their findings). Especially watch the early chapters (4 Installation, 7 Quick Start) where the install path may have changed since the original authoring.
- **TOC cross-links** — every `[Chapter X](./XX-...)` resolves; every anchor link points at a real mdbook-derived slug. Validator runs the full scan; you fold findings.
- **No "coming in Sprint X" markers** — should be zero by start of Sprint 7; any survivors are blockers. Grep `grep -nrE "coming in sprint" book/src/` should return empty.
- **Forward-references to v1.x roadmap** — explicit future-tense framing where present (e.g., terraform `--backend k8s`/`ssh`, Windows full TTY, multi-hop SSH ProxyJump). Cross-link to PLAN.md §"What's deliberately deferred to post-v1.0" so readers know where the roadmap lives.
- **PRD links use GitHub-canonical URLs** — `[PRD 03](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md)`, not relative-from-book paths (the published book at GitHub Pages can't resolve relative `../docs/prd/...` links — Sprint 1 surfaced this).
- **Auto-generated chapters 27 + 29** — do NOT hand-edit. If the underlying surface changed (e.g., staff added the book URL to `roksbnkctl version` output), re-run the generators at integration time so chapter 27 picks it up. Same contract as Sprint 6.

A reasonable rhythm: chapters 1-11 (Parts I-III) in pass 1; chapters 12-19 (Parts IV-V) in pass 2; chapters 20-26 (Parts VI-VII) in pass 3; chapters 27-32 (Parts VIII-IX) + preface in pass 4. The auto-generated 27 + 29 are skipped in your hand-edit pass; the others all get a paragraph-level read-through.

### 5. PRD 05 §"Phase I" + §"Phase N" step-matrix refresh (Sprint 6 carry-over)

The Sprint 6 tech-writer (Issue 12) flagged that PRD 05's Phase I lists I0-I7 (8 steps) and Phase N lists N0-N10 (11 steps), but the shipped `scripts/e2e-test-backends.sh` implements I0-I11 (12) and N1-N6 (6 restructured). The PRD is the design doc; chapters 23 + 26 reference it; the integrator's v1.0 sign-off cross-checks PRD against shipped. Land the refresh:

- **Phase I**: extend the PRD's step table from I0-I7 → I0-I11 to match the shipped driver. The current driver covers: I0 target list, I1 `--on $TARGET` ibmcloud, I2 `--backend ssh:$TARGET` ibmcloud, I3 apt-bootstrap, I4 cred audit (env), I5 wrapper cleanup, I6 SetEnv silent-drop, I7 non-Ubuntu detection, I8 sudo-password-required, I9 repo-unreachable, I10 context-cancel, I11 SSH backend doctor. Source-of-truth: read `scripts/e2e-test-backends.sh::phase_i` for the actual implementation.
- **Phase N**: collapse the PRD's N0-N10 to the shipped N1-N6. Source-of-truth: read `scripts/e2e-test-backends.sh::phase_N` for the actual implementation (N1 up-local, N2 throughput-k8s, N3 ibmcloud-ssh, N4 dns-gslb-compare, N5 down-docker, N6 verify).
- **Chapter 23 cross-link** — if chapter 23 cites specific PRD-05 step numbers (`PRD 05 §"Phase I" I3`, etc.), spot-check those references after the PRD edit. Update both ends so they stay consistent.

Do **not** rewrite the PRD prose around the tables — the design rationale is unchanged; only the step matrices need to reflect what shipped. The PRD remains a design doc; the chapter 23 user-facing version is where polish prose belongs.

### 6. Chapter-23 disk-size estimate (Sprint 6 architect Issue 11 carry-over)

If the tech-writer's dogfooding loop measures a workspace state dir size materially different from the chapter-23 §"Pre-requisites" "approximately 200 MB" line, refresh the number. The carry-over is conditional: no dogfood number → no edit. If the dogfood number lands in your inbox via tech-writer's issue file, edit chapter 23's line + add a one-line measurement note.

### 7. Search-index spot-check (cross-functional with validator)

Validator runs the search-index spot-check; you fold their findings if any chapter's H1 or section headings need adjustment so mdbook's search picks up the right chapter for a canonical query. Examples of canonical queries: `GSLB`, `jumphost`, `kubeconfig`, `--backend k8s`, `--on jumphost`, `cred resolver`, `ops pod`, `terraform-via-docker`, `iperf3 north-south`. If a query returns the wrong chapter as the top hit, the chapter's H1 or top-of-chapter framing is probably under-keyed for that term — adjust the prose so the relevant term appears in the chapter's first 200 characters.

## Style guidance

- Same as Sprints 1-6: lower-case prose, sentence-case section headers, code blocks for any command, inline code for filenames + identifiers, cross-reference other chapters with relative links, short paragraphs (one idea per paragraph), examples runnable as-written.
- PRD links use the GitHub-canonical URL form: `[PRD 03](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md)`. Relative `../docs/prd/...` paths don't resolve from the published book at GitHub Pages.
- Diagrams: prefer markdown tables for simple matrices, Mermaid for relationships / sequences / hierarchies. Mermaid renders inline in mdbook via the `mdbook-mermaid` preprocessor (the integrator wires this; you author the diagrams).
- Worked examples should show **input + output**. Real-ish output (or representative sample output) for every command — readers should see what the success signal looks like.

## Issue tracking

`/mnt/c/project/roksbnkctl/issues/issue_sprint7_architect.md`:

```markdown
# Sprint 7 — architect issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: ...
**Files affected**: ...
**Proposed fix**: ...
```

If clean, file with `*No issues filed.*`.

## Verification before reporting done

- All 32 chapter files have had a paragraph-level polish read-through; voice is consistent
- Four Mermaid diagrams land at their natural insertion points (chapters 17 / 7 / 21 / 18)
- `book/book.toml` has the `[preprocessor.mermaid]` block (or you've filed an issue for the integrator to add it)
- Seven worked-example walkthroughs land at the ends of chapters 7 / 11 / 12 / 18 / 21 / 25 / 32 (Part I-IX coverage minus VIII reference)
- Preface rewritten and under ~120 lines
- PRD 05 §"Phase I" + §"Phase N" tables match the shipped `scripts/e2e-test-backends.sh` step matrices
- `grep -nrE "coming in sprint" book/src/` returns zero hits
- `grep -nrE "docs/prd/" book/src/` shows only GitHub-canonical URLs in published-book contexts (relative paths inside the book itself are fine, but cross-doc links to PRDs use the GitHub-canonical form)
- `mdbook build book/` succeeds locally if mdbook is installed; otherwise rely on book CI

## Final report (under 200 words)

- Per-chapter line-delta summary (how much each chapter grew/shrunk through the polish pass)
- Diagrams landed (count) + insertion points
- Walkthroughs landed (count) + insertion points
- PRD 05 §I + §N step-matrix refresh: confirmed against shipped driver (yes/no)
- Issues filed (counts by severity)
- Anything the integrator should know before cutting the `v1.0` tag

Do NOT commit. The integrator commits the aggregated work.
