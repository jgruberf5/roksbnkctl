You are the **architect** agent for Sprint 24 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first

1. `prompts/sprint24/README.md` — integrator decisions; the
   scope (test hosts CLI surface only).
2. `issues/issue_sprint24_staff.md` — full CLI surface design.
3. `book/src/SUMMARY.md` — verify the chapter the new
   subsection lands in. Likely `book/src/12-running-tests.md`
   or similar — check the actual file structure.
4. The existing `targets` documentation in the book — find
   it via grep `targets` in `book/src/`. This is the
   ergonomic precedent + tone reference.
5. The existing `test connectivity` / `test dns` chapter
   sections — they reference `test.connectivity.extra_hosts`
   today as a YAML-only setting. You'll cross-link them to
   the new CLI subsection.

## Tasks

1. **New subsection in `book/src/12-running-tests.md`** (verify
   chapter title via SUMMARY.md — could be `12-test.md` or
   similar) titled "Managing test hosts via the CLI" or similar:
   - Names the four new subcommands: `roksbnkctl test hosts
     {list,add,remove,clear}`.
   - One worked example: an operator running
     `roksbnkctl test hosts add https://docs.f5.com`,
     `roksbnkctl test hosts list`, then
     `roksbnkctl test connectivity` in sequence.
   - Cites the `test.connectivity.extra_hosts` slice as the
     persistent backing field (operators familiar with the
     YAML config see the connection).
   - Cross-link to the existing `test connectivity` and
     `test dns` chapter sections so an operator hitting the
     "no hosts configured" error in those commands finds the
     CLI path immediately.
   - Tone matches the existing `targets` book chapter —
     practical, terse, names the recovery.
2. **Cross-link from the `test connectivity` + `test dns`
   chapter sections** — the existing prose likely says "add to
   `test.connectivity.extra_hosts` in `config.yaml`" or similar.
   Add an "or use `roksbnkctl test hosts add <url>`" pointer.
3. **Verify `book/src/SUMMARY.md` lists chapter 12** — if your
   new subsection adds a depth-2 entry that should be in the
   SUMMARY, add it. If the existing SUMMARY just lists chapter
   titles (no sub-bullets), leave SUMMARY alone.

## Out of scope

- `internal/`, `cmd/` — staff's surface.
- `.github/workflows/`, `Makefile`, `CONTRIBUTING.md` — out of
  scope this sprint.
- The CHANGELOG — integrator-owned at cut time.
- `docs/PRD/` — no PRD covers the test commands' UX.
- `internal/orchestration/`, `internal/config/tfstate.go`,
  `terraform/` — out of scope.

## Acceptance criteria

1. The new subsection carries the worked example with verbatim
   binary output (run the binary against a temp workspace if
   needed; or stub the output but mark it clearly as illustrative
   if the binary isn't built yet).
2. The `test connectivity` + `test dns` sections cross-link to
   the new subsection.
3. No edits to source code or any non-book file (except
   possibly SUMMARY.md if it lists sub-bullets).

## Closure

Write your closure to
`issues/issue_sprint24_architect.md` (NEW file) §"Closure —
architect, <date>". Include: the chapter + line numbers of the
new subsection, the cross-links added, and any future-sprint
candidates raised. Top-of-file `**Status**: resolved`.

Reply with a concise summary under 200 words: the new subsection
location + size, the cross-links added, and any follow-ups.
