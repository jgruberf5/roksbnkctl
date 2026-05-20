You are the architect agent for Sprint 12 of the roksbnkctl project. Sprint 12 is a **patch cycle** — `v1.4.1` — landing the `--var-file` relative-path fix surfaced post-v1.4.0. Your scope is `CHANGELOG.md` (`v1.4.1` entry under `## Unreleased (v1.x)`), `docs/PLAN.md` (new Sprint 12 section, much shorter than Sprint 11's), and two small `book/src/06-workspaces.md` discoverability nudges deferred from Sprint 11 tech-writer Issues 2 + 4. **Do not touch `internal/`, `cmd/`.**

There is **no PRD authoring** this sprint — the design surface is `issues/issue_sprint12_staff.md` §"Root cause" + §"Proposed fix". PRD 07 refinement only if staff or validator surfaces a design gap mid-sprint (unlikely for a bugfix patch).

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Confirm by `pwd` before editing.

## Read first

- `issues/issue_sprint12_staff.md` — Issue 1 spells out the bug, root cause, proposed fix, and acceptance criteria. Your CHANGELOG bullet describes the user-visible change ("relative `--var-file` paths now resolve against the invocation directory"); your PLAN.md section names the bug + the fix surface.
- `CHANGELOG.md` top — `## v1.4.0 — <date>` is the current top (or `## Unreleased (v1.x)` if the tag-cut hasn't happened yet — check the actual state). The v1.4.1 block goes above whichever it is.
- `docs/PLAN.md` — Sprint 11 section (around line 807-849) for shape reference. Sprint 12 is shorter (single-bug scope), but the same subsection layout: theme, drivers, code deliverables, test deliverables, risks, gate criteria.
- `book/src/06-workspaces.md` — current state of the chapter. The two nudges target:
  - §"Worked example" (around line 99-116): tech-writer Sprint 11 Issue 2 recommended one sentence noting "embedded Terraform module defaults are not captured" near the worked example, so users in disaster-recovery mode see the caveat before scrolling to §"What it's not".
  - §"Redaction" (around line 74) and the redacted-line inline comment text: tech-writer Sprint 11 Issue 4 suggested clarifying where to learn about the cred resolver. The chapter already cross-links chapter 14 — read it and decide if the existing prose is sufficient or if one more sentence near the worked example helps.
- `prompts/sprint11/architect.md` — prior-sprint prompt structure; reuse the section-shape conventions.
- `issues/resolved_sprint11_*` — *do not exist*; Sprint 11 issue files stayed as `issue_sprint11_*.md` (committed). Don't try to read renamed files.

## Coordinate with parallel agents

A **staff engineer** agent is implementing the `resolveVarFiles` helper + wire-ups in `internal/cli/`. **Do not touch `internal/`, `cmd/`.**

A **validator** agent will do the seven-step regression sweep + reproduce the bug + cross-link audit on your CHANGELOG + chapter edits.

A **tech-writer** agent does read-only review at end of sprint (after staff/architect/validator return).

## Tasks (priority order)

### 1. CHANGELOG `v1.4.1` entry

Add a new `## Unreleased (v1.x)` block (or extend the existing one if present) above v1.4.0. Subsections, mirroring the v1.4.0 entry style:

- `### Fixed` — the `--var-file` relative-path resolution headline. Name the symptom ("Failed to read variables file" from terraform when `--var-file=./foo.tfvars` is passed), the root cause one-liner (terraform's CWD is the per-phase state dir, not the user's shell PWD), and the new behavior (relative paths resolve against the invocation CWD).
- `### Added` — none expected; leave the subsection out.
- `### Changed` — none expected; leave the subsection out unless a chapter-6 polish nudge produces user-visible doc shift worth naming.
- `### Deferred` — carry forward the v1.4.0 deferred items (`ops install/uninstall` snapshot + all prior-cycle items) with a note that the carry list is unchanged.

Intro paragraph (~2 sentences) frames it as a focused patch closing the `--var-file` regression and cross-links `docs/PLAN.md` §"Sprint 12" + `issues/issue_sprint12_staff.md` Issue 1 for context.

### 2. PLAN.md Sprint 12 section

Add a `## Sprint 12 — `--var-file` relative-path fix (patch cycle, post-v1.4.0)` section after Sprint 11. Much shorter than Sprint 11's — a patch cycle has minimal design surface. Same subsection layout, but each subsection is 1-2 sentences:

- **Theme** — patch release closing the relative-`--var-file` bug; no new PRDs.
- **Drivers / why now** — user surfaced via post-v1.4.0 live `cluster up` (validator Sprint 11 Issue 2's out-of-band action). One-line cross-link.
- **Code deliverables** — `resolveVarFiles` helper in `internal/cli/` + wire-ups at the five `flagVarFiles` consumption sites + unit test. Cross-link `issues/issue_sprint12_staff.md` Issue 1.
- **Test deliverables** — staff's unit-test trio (absolute pass-through, relative-against-CWD, missing-file error message), validator's regression sweep, validator's bug-repro confirmation.
- **Risks** — `~`-expansion semantics if not already handled (small risk; check during implementation); analogous shell-CWD-vs-state-dir gotchas in other path-shaped flags (validator's sweep should surface any).
- **Gate criteria for `v1.4.1` tag** — regression sweep green, bug reproduces against pre-fix `main`, fix makes it pass, all four agents' issue files at `Status: resolved` / `wontfix` / `accepted`.

### 3. Chapter 6 polish nudges

Two small additions, both deferred from Sprint 11 tech-writer review:

#### 3a. Defaults caveat near §"Worked example"

Tech-writer Sprint 11 Issue 2 §"Recommendation" suggested either:

> Add one sentence to the §"Worked example" prose along the lines of *"Re-applying from this snapshot alone reconstructs the inputs the user wrote; embedded Terraform module defaults are not captured (see §'What it's not')."*

Land this. One sentence, immediately after the worked-example code block, cross-linking the existing §"What it's not" subsection. Goal: a user in disaster-recovery mode who reads only the worked example sees the caveat without having to scroll further.

#### 3b. Cred-resolver context near §"Redaction"

Tech-writer Sprint 11 Issue 4 §"Recommendation" floated extending the inline comment in the binary to point at a docs URL. The implementation surface for that is staff (`internal/config/applied_tfvars.go:205`), but Sprint 12 is scoped to the `--var-file` fix — **don't ask staff to take this**. Instead, judge whether the existing §"Redaction" prose (chapter 6 around line 74) is sufficient for an external teammate who receives the file out-of-band. If you think one more sentence helps (e.g., naming the cred resolver's role explicitly before the chapter 14 cross-link), add it. If the existing prose is already discoverable enough, file as `accepted` in your architect issue file and move on.

### 4. (Optional) PRD 07 follow-up

PRD 07's §"Resolved design decisions" mentions the redaction list is hardcoded by intent. If staff's `resolveVarFiles` work surfaces a related design gap (e.g., the new normalization changes how var-files are recorded in the snapshot's `source-attribution` comments), surface as an architect-surface issue and fix the PRD; otherwise leave PRD 07 alone.

## Issue tracking

File at `issues/issue_sprint12_architect.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix | accepted`.

When filing against another agent's surface, include the proposed-fix patch as a markdown diff.

## Scope guardrails

- Do NOT touch `internal/`, `cmd/`, `prompts/`, `Makefile`, `scripts/`.
- Do NOT commit. Do NOT push.
- `mdbook build book/` is available on this host (`mdbook` + `mdbook-mermaid` + `mdbook-pandoc` under `~/.cargo/bin/`). Run after your chapter edits to confirm clean build.

## Verification before reporting done

- CHANGELOG `v1.4.1` block reads naturally and cross-links `docs/PLAN.md` + `issues/issue_sprint12_staff.md`.
- PLAN.md Sprint 12 section is roughly 1/3 the length of Sprint 11's (patch scope).
- Chapter 6 polish nudges land at sensible spots — read the surrounding prose to confirm the new sentences flow.
- `mdbook build book/` clean (HTML backend exit 0).
- No `internal/` / `cmd/` files touched.

## Final report

Under 200 words. Cover: what landed in CHANGELOG, PLAN.md, chapter 6 (which nudges, which deferred); mdbook verdict; any findings filed against staff/validator; deferred-to-v1.4.x or v1.5 items (if any).
