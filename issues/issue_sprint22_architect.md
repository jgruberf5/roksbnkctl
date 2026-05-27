# Sprint 22 — architect issues (mdbook CI-managed doc note + cross-doc sweep)

> **Sprint 22 frame.** Architect's scope is the contributor-doc
> companion to validator's one-line edit folding `mdbook` into
> `.github/workflows/tools-images.yml`'s `strategy.matrix.image`
> matrix. One short note in `CONTRIBUTING.md` naming the image
> as CI-managed, plus a cross-doc sweep for any stale
> "manually push the mdbook image" callouts elsewhere in the
> contributor docs / Makefile comments.

**Status**: resolved

---

## Issue 1 — Document `mdbook` as CI-managed via `tools-images.yml`

**Severity**: low (documentation; no behavior change, no
operator-visible surface). Maps to architect's "light" scope
in `prompts/sprint22/README.md`.
**Status**: resolved

### Motivation

Validator Issue 1 adds `mdbook` to the matrix in
`.github/workflows/tools-images.yml`. The release-cut host had
been manually running `make -C tools/docker build-mdbook` +
`docker push` to refresh `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`
before each release; per `prompts/sprint22/README.md` that
manual step was the proximate cause of the `v1.7.0`
`make release-publish` step-2 Puppeteer/Chromium cold-start
flakiness (stale image bytes). With the matrix fold-in, the
image republishes on every `main` push automatically; the
contributor docs must reflect that so future maintainers don't
re-introduce the manual ritual.

### Tasks

1. Verify the docker-image-publishing flow doc location.
   `CONTRIBUTING.md` §"Building tool images locally" (lines
   333–363) is the canonical home — `docs/PRD/03-EXECUTION-BACKENDS.md`
   contains zero `mdbook` references (verified by
   `grep -n -i mdbook docs/prd/03-EXECUTION-BACKENDS.md` returning
   empty), so the contributor doc is the right surface, not the PRD.
2. Add a note to that section naming `mdbook` as CI-managed via
   `tools-images.yml`'s matrix, riding `ibmcloud` and `iperf3`'s
   flow. Tone: practical, terse, names the workflow file.
3. Cross-doc sweep across `CONTRIBUTING.md`, `docs/`, `Makefile`
   comments for any "manually push the mdbook image" /
   "remember to push the mdbook image" / `make build-mdbook`
   callouts. Rewrite or remove as CI-managed wording demands;
   document the sweep result here.

### Closure — architect, 2026-05-27

**Files edited**: `CONTRIBUTING.md` only. No other doc, no PRD,
no Makefile, no workflow file. The validator's editable surface
(`.github/workflows/tools-images.yml`) was not touched. The
book is tech-writer's surface for the drift sweep and was not
touched.

**Doc-flow location verification**:

- `docs/prd/03-EXECUTION-BACKENDS.md` — `grep -n -i mdbook
  docs/prd/03-EXECUTION-BACKENDS.md` returns zero hits. PRD 03
  documents the docker backend's runtime image-pull contract,
  not the publishing flow. Not the right surface for the note.
- `CONTRIBUTING.md` §"Building tool images locally" (the
  pre-existing section at lines 333–363) is the canonical
  docker-image-publishing flow doc. It already names
  `tools-images.yml` for `ibmcloud` and `iperf3` and lists the
  `tools/docker/Makefile` build targets. Natural anchor for the
  `mdbook` addition.
- `book/src/31-building-from-source.md` mentions the workflow
  too, but the book is tech-writer's drift-sweep surface
  (out of scope here per the architect prompt's "Out of scope"
  list).

**Edits in `CONTRIBUTING.md`** (three sites; tone-matched to the
surrounding prose throughout):

1. **Line 23** (§"What it deliberately does NOT install" in the
   `install_build_dependencies.sh` walkthrough). Rewrote the
   `mdbook` bullet from `bundled in tools/docker/mdbook/Dockerfile;
   build once via make -C tools/docker build-mdbook` to:

   > `mdbook` / `mdbook-pandoc` / `pandoc` / `texlive` /
   > `mermaid-cli` — bundled in `tools/docker/mdbook/Dockerfile`.
   > CI publishes `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`
   > on every `main` push (via `.github/workflows/tools-images.yml`);
   > `docker pull` it or build locally via `make -C tools/docker
   > build-mdbook`

2. **Lines 333–363** (§"Building tool images locally" — the
   canonical docker-image-publishing flow doc). Three changes:
   - Added a third bullet to the image list for
     `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook` (release-time
     book builder; Debian base + mdbook + mdbook-pandoc +
     pandoc + texlive-xetex + mermaid-cli).
   - Replaced the single-line "Released images are built and
     pushed by `.github/workflows/tools-images.yml` on every
     `v*` tag push" sentence with a full paragraph naming the
     `strategy.matrix.image` matrix, naming both publish paths
     (`main` push → `:dev`; `v*` tag push → `:<tagname>` +
     `:latest`), and explicitly stating that `mdbook` was folded
     into the matrix in Sprint 22 so routine Dockerfile edits no
     longer require a manual `make -C tools/docker build-mdbook` +
     `docker push` step on the release-cut host.
   - Extended the local-build code block to include
     `make build-mdbook` as a third entry; updated the
     `build-all` comment from "both" to "all three".

   Verbatim paragraph (lines 340–350):

   > All three images are built and pushed by
   > `.github/workflows/tools-images.yml`'s `strategy.matrix.image`
   > matrix. The workflow auto-builds + auto-pushes on every
   > push to `main` (publishes `:dev`) and on every `v*` tag
   > push (publishes `:<tagname>` + `:latest`). `mdbook` was
   > folded into the matrix in Sprint 22 — routine edits to
   > `tools/docker/mdbook/Dockerfile` no longer require a manual
   > `make -C tools/docker build-mdbook` + `docker push` step
   > on the release-cut host; the next push to `main`
   > republishes `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`
   > automatically. For local development against the docker
   > backend (without waiting on a tag), build the images
   > yourself via the `tools/docker/Makefile`:

3. **Line 500** (§"Release-time tooling" table row for the
   `mdbook` toolchain). Rewrote the right-hand cell from
   `Bundled in tools/docker/mdbook/Dockerfile — ~6 GB image,
   build once with make -C tools/docker build-mdbook.` to a CI-
   aware sentence naming `tools-images.yml`, both publish paths,
   and the `make release` / `make release-publish` pull
   behavior, keeping the local `make -C tools/docker build-mdbook`
   path as a fallback for local iteration:

   > Bundled in [`tools/docker/mdbook/Dockerfile`](./tools/docker/mdbook/Dockerfile).
   > CI-managed via [`.github/workflows/tools-images.yml`](./.github/workflows/tools-images.yml):
   > every push to `main` republishes
   > `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`, every `v*`
   > tag push publishes `:<tagname>` + `:latest`. `make release`
   > / `make release-publish` pull whichever tag matches the
   > binary's version, so no host build is required at release
   > time. For local iteration without waiting on a push,
   > `make -C tools/docker build-mdbook` builds `:dev` locally
   > (~6 GB image).

**Cross-doc sweep result**:

Sweep methodology — `grep -rn -i 'build-mdbook|mdbook image|push.*mdbook|manually.*push|remember.*push'`
across `--include=*.md --include=Makefile --include=*.mk` from
the repo root, plus a focused re-check of the three editable
surfaces (`CONTRIBUTING.md`, `Makefile`, `docs/PLAN.md`)
post-edit.

Hit triage:

| File | Line | Verdict |
|------|------|---------|
| `CONTRIBUTING.md` | 23, 333–363, 500 | **Rewritten** as above. Three sites updated; all now CI-aware. |
| `Makefile` | 47, 100, 107, 419 | **Out of scope.** Line 47 is the `BOOK_BACKEND` block comment naming the image as a build target — factual, no manual-push framing. Line 100/107 is the `make book-pdf` error path that prints `make -C tools/docker build-mdbook` as a local-fallback recovery hint when `BOOK_BACKEND=docker` is missing the image — still correct as a local fallback (the user could also `docker pull`, but the existing hint is fine for a contributor without GHCR access). Line 419 is the `release-publish` prereqs comment naming "the tools/docker/mdbook image" as a prereq — factual, doesn't say anything about manual push. The architect's editable scope per the prompt is "essentially CONTRIBUTING.md (or wherever the docker-image-publishing flow is documented)"; Makefile comments are infrastructure, not flow-doc, and none of these four lines carry a "remember to push" framing. Left untouched. |
| `docs/PLAN.md` | 98, 1144, 1145 | **Out of scope** (sprint-narrative; integrator-owned). Line 98 is the Sprint 0 plan describing `book.yml` (the HTML-deploy workflow, not the tools-images workflow) — orthogonal. Lines 1144–1145 are the Sprint 22 per-role scope descriptions for validator and architect — those describe the work; they're correct as written. Not rewritten. |
| `CHANGELOG.md` | 377 | **Out of scope** (integrator-owned at cut time; Sprint 22 release tag is gated on Sprint 23 per integrator decision). Historical entry describing the v1.0.1 book CI shift; unrelated to Sprint 22's CI fold-in. |
| `.archive/...` | various | **Out of scope.** Archive copies of prior sprint prompts / issues. Not editable per repo convention. |
| `book/src/31-building-from-source.md` | 92–116 | **Out of scope** (tech-writer's drift-sweep surface, explicitly listed under the architect prompt's "Out of scope"). The chapter's tools-docker tree omits `mdbook/` from its listing and doesn't name the `:dev` publish behavior for any image; that drift is tech-writer's to sweep. Flagged for the integrator as a follow-up candidate (see below). |
| `tools/docker/Makefile` | 1, 31, 34, 37 | **Out of scope.** Source-of-truth build target file; not a doc. The `build-mdbook` target itself is unchanged by Sprint 22 — local builds still work; the only difference is the CI now also builds via the same Dockerfile. |
| All other matches | various | False positives — sprint-historical issue files in `issues/` archive, prior-sprint architect prompts under `.archive/prompts/`, etc. None carry a live "manually push the mdbook image" instruction. |

**Net result**: three edits to `CONTRIBUTING.md` (one bullet
rewrite + one section rewrite + one table-row rewrite); no other
doc changes required. The pre-existing "build once via
`make -C tools/docker build-mdbook`" phrasings at line 23 and
line 500 were the closest things to manual-push callouts in
the editable doc surface; both rewritten as CI-managed with the
local-build path preserved as a fallback. No "remember to push"
or "manually push" verbatim callouts existed in any active doc
or Makefile (the only such phrasings live in
`.archive/issues/issue_sprint18_architect.md:185–200` describing
the pre-Sprint-22 state, which is correct as a historical record
and out of scope for editing).

**Files touched this closure**:

- `CONTRIBUTING.md` — three sites (line 23 bullet; lines
  333–363 section additions; line 500 table-row rewrite). No
  structural reordering, no other content altered.

**Discipline checks**:

- Edited only `CONTRIBUTING.md` (the architect-prompt's
  "essentially CONTRIBUTING.md" scope, verified via the
  doc-flow location check above).
- No edits to `.github/workflows/tools-images.yml` (validator's
  surface).
- No edits to `internal/`, `cmd/`, `tools/sprintwatch/`,
  `tools/ciwatch/` (staff/validator territory).
- No edits to `book/src/` (tech-writer's drift-sweep surface).
- No edits to `CHANGELOG.md` (integrator-owned at cut time; Sprint
  22 release tag is gated on Sprint 23 per integrator decision —
  see `prompts/sprint22/README.md` §"Integrator decisions baked in").
- No edits to `internal/orchestration/`,
  `internal/config/tfstate.go`, `internal/cli/lifecycle.go`,
  `internal/cli/cluster_phase.go` (staff Issues already shipped;
  not relitigated).
- No commits; no `gh issue create`; no `git push`. All
  integration is the integrator's surface.
- The new prose in `CONTRIBUTING.md` matches the surrounding
  voice (practical, terse, names files and behaviors directly;
  no marketing-speak).

### Follow-up candidates flagged for the integrator

1. **`book/src/31-building-from-source.md` drift** — the chapter's
   `tools/docker/` directory listing (lines 96–103) shows only
   `ibmcloud/` and `iperf3/`, omitting `mdbook/`. The
   `make ibmcloud` / `make iperf3` / `make all` examples at
   lines 107–112 also predate the `mdbook` target. This is
   tech-writer's drift-sweep surface for Sprint 22, not the
   architect's surface; flagged so the tech-writer prompt's
   sweep covers it. (The architect prompt explicitly lists the
   book as out-of-scope here.)
2. **`Makefile` line 107 error hint** — the `make book-pdf
   BOOK_BACKEND=docker` failure path currently suggests
   `make -C tools/docker build-mdbook` as the recovery. Now
   that the image is published on every `main` push, the
   recovery could also be `docker pull
   ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`. Not urgent —
   the local build still works — but a future sprint could
   shorten the time-to-recovery for a contributor without docker
   pull access constraints. Lower-priority candidate; consider
   for a future low-stakes sprint alongside other Makefile
   ergonomics improvements.
3. **No PRD update needed.** `docs/prd/03-EXECUTION-BACKENDS.md`
   documents the runtime image-pull contract, not the publishing
   flow; the Sprint 22 fold-in changes the publish path only and
   the runtime contract (binary version → image tag resolution)
   is unaffected. Verified by `grep -n -i mdbook` returning zero
   hits in PRD 03.

### Related

- `prompts/sprint22/architect.md` — this issue's source prompt.
- `prompts/sprint22/README.md` — integrator decisions
  (three-deliverable-stream framing; Sprint 23 release-gate).
- `issues/issue_sprint22_staff.md` — sibling closure-only
  staff issue documenting the two shipped fixes
  (`DetectShape` heuristic tightening + `RunDown` composite
  single-prompt UX). Independent scope; no overlap with this
  doc-only work.
- `.github/workflows/tools-images.yml` — validator's editable
  surface; the matrix fold-in is the source-of-truth change
  this closure documents.
- `docs/PLAN.md` §"Sprint 22" — full sprint narrative; this
  closure mirrors the per-role scope row for the architect.
