You are the **architect** agent for Sprint 22 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first

1. `prompts/sprint22/README.md` — integrator decisions; the
   three-deliverable-stream framing; the Sprint 23 release-gate.
2. `docs/PLAN.md` §"Sprint 22" — the full sprint scope.
3. `CONTRIBUTING.md` — verify where the docker-image-publishing
   flow is documented; that's the file you edit.
4. `.github/workflows/tools-images.yml` — verify the existing
   `strategy.matrix.image` list and the workflow comment about
   the `iperf3` Dockerfile having broader context than needed
   (the same statement will apply to `mdbook` after validator's
   edit lands).
5. `tools/docker/mdbook/Dockerfile` — confirm it doesn't
   reference repo-root paths (your CONTRIBUTING.md note can
   state this as fact once verified).

## Tasks

1. **One short note in `CONTRIBUTING.md`** (verify path; if the
   docker-image-publishing flow is documented somewhere else
   like a `docs/PRD/03-*` or contributor sub-doc, edit there
   instead — but check `CONTRIBUTING.md` first):
   - State that `mdbook` is now CI-managed via
     `.github/workflows/tools-images.yml`'s matrix, ride
     `ibmcloud` and `iperf3`'s flow.
   - Auto-builds + auto-pushes on every `main` push and every
     `v*` tag push to `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook`.
   - No more manual `make -C tools/docker build-mdbook` +
     `docker push` step required for routine Dockerfile edits.
   - Tone matches the rest of CONTRIBUTING.md: practical,
     terse, names the workflow file.
2. **Grep for any stale "manually push the mdbook image" /
   "remember to push the mdbook image" / "`make build-mdbook`"
   callouts** across `CONTRIBUTING.md`, `docs/`, `book/src/`,
   `tools/docker/mdbook/README.md` (if any), `Makefile`
   comments. If found, rewrite them as CI-managed (or remove
   if redundant after your new note). If none exist, document
   the sweep result.

## Out of scope

- `.github/workflows/tools-images.yml` itself — that's the
  validator's surface for this sprint.
- `internal/`, `cmd/`, `tools/sprintwatch/`, `tools/ciwatch/` —
  staff/validator territory.
- The CHANGELOG — integrator-owned at cut time, and the cut is
  gated on Sprint 23 anyway.
- `internal/orchestration/`, `internal/config/tfstate.go`,
  `internal/cli/lifecycle.go`, `internal/cli/cluster_phase.go`
  — the staff Issues already shipped; do not relitigate.
- `book/src/` chapters — that's tech-writer's surface for
  drift sweep, not architect's surface this sprint.

## Acceptance criteria

1. `CONTRIBUTING.md` (or the actual flow-doc file) carries the
   new note naming `mdbook` as CI-managed.
2. The cross-doc sweep result is documented in your closure
   (either "rewrote N callouts to <list>" or "none found").
3. No edits to the workflow file itself, the book, or any Go
   code.

## Closure

Write your closure to
`issues/issue_sprint22_architect.md` §"Closure — architect,
<date>". Include the affected doc file + line numbers, the
exact paragraph you added, and the cross-doc sweep result.
Flip status `open` → `resolved`. Create the issue file if it
doesn't exist yet — Sprint 22's only existing issue file is
`issue_sprint22_staff.md` (closure-only); yours is new.
