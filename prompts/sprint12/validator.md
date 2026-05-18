You are the validator agent for Sprint 12 of the roksbnkctl project. Sprint 12 is a **patch cycle** — `v1.4.1` — landing the `--var-file` relative-path fix surfaced post-v1.4.0. Your scope: the seven-step regression sweep, reproducing the bug per `issues/issue_sprint12_staff.md` §"Reproduce" and confirming staff's fix makes it pass, a cross-link audit on architect's CHANGELOG + chapter edits, and the `mdbook build book/` gate (which the parent session confirmed is runnable on this host — `mdbook` + `mdbook-mermaid` + `mdbook-pandoc` are all on `PATH` under `~/.cargo/bin/`).

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Confirm by `pwd` before editing.

## Read first

- `issues/issue_sprint12_staff.md` — Issue 1 §"Reproduce" gives you the bug-reproduce recipe; §"Acceptance criteria" gives you the verify list.
- `prompts/sprint11/validator.md` — prior-sprint validator prompt. The seven-step sweep is the same. The kind-cluster-bring-up step is still gated on `kind` being installed on the host; if absent, skip with the same exit-2 short-circuit Sprint 10 / 11 used.
- `issues/issue_sprint11_validator.md` — Sprint 11's seven gates and the `mdbook build` Issue 6 closure. The slug `id="terraformappliedtfvars--whats-deployed-right-now"` was the rendered chapter 6 anchor — useful baseline if your `mdbook build` produces a different slug post-architect-edit.
- `internal/cli/lifecycle.go` and `internal/cli/lifecycle_test.go` — read after staff lands to confirm the helper + tests look reasonable. You don't review staff's code; you verify the fix works.

## Coordinate with parallel agents

A **staff engineer** agent is implementing the `resolveVarFiles` helper + wire-ups in `internal/cli/`.

An **architect** agent is updating CHANGELOG `v1.4.1` entry, PLAN.md Sprint 12 section, and two chapter 6 polish nudges.

A **tech-writer** agent does read-only review at end of sprint (after staff/architect/validator return).

## Tasks (priority order)

### 1. Regression sweep — seven gates

Match Sprint 11's gate exactly:

| Step | Command | Expected |
|---|---|---|
| 1 | `go build ./...` | clean |
| 2 | `go test ./...` | green; new `internal/cli` tests pass + existing suites unchanged |
| 3 | `go vet ./...` | clean |
| 4 | `gofmt -d -l .` | clean (no output) |
| 5 | `make staticcheck` | clean |
| 6 | `make build-integration-tags` (i.e., `go build -tags integration ./...`) | clean |
| 7 | `go test -tags integration ./internal/exec/... ./internal/remote/...` | green (kind-less integration tests; skip the full `scripts/integration-test.sh` if `kind` isn't installed, matching Sprint 10/11 precedent) |

Record the literal command + result in your issue file as a table.

### 2. Reproduce the bug + confirm fix

The bug is the headline of this sprint — verify both directions:

**Pre-fix repro (sanity check the bug existed)**: you can confirm via `git log --oneline -5` that staff's commit landed; if it hasn't yet when you start, wait or note in your issue file. Don't `git stash` staff's changes to fake the pre-fix state; instead, write a focused unit test that calls `resolveVarFiles` with a relative path and asserts the resolved-against-CWD output. If staff's test surface already covers this (per their closure block), cite the test name and verify it passes.

**Post-fix verify**: the `internal/cli/lifecycle_test.go` test that covers `issues/issue_sprint12_staff.md` §"Acceptance criteria" should pass. Run `go test -run VarFile -count=1 -v ./internal/cli/` (or whatever pattern matches staff's chosen test names). All three acceptance-criteria subtests (absolute pass-through, relative resolved against CWD, missing-file error message naming both paths) must pass.

**Out-of-band live verify** is the user's responsibility — they ran `roksbnkctl up --var-file=./terraform.tfvars --auto` to surface this bug originally. Once v1.4.1 lands they'll re-run it. Cite this as the out-of-band action in your issue file (same shape as Sprint 11 Issue 2's hand-off).

### 3. Cross-link audit

Architect lands CHANGELOG + PLAN.md + chapter 6 nudges. Audit:

- CHANGELOG `v1.4.1` `### Fixed` bullet matches the actual fix (relative `--var-file` resolves against invocation CWD; cite staff's helper name + file).
- PLAN.md Sprint 12 section's named code deliverables match what staff actually landed.
- Chapter 6 polish nudges (defaults caveat + cred-resolver context, both deferred from Sprint 11 tech-writer) land at sensible spots and don't introduce drift against `internal/config/applied_tfvars.go`.

### 4. `mdbook build book/` gate

Run `PATH="$HOME/.cargo/bin:$PATH" mdbook build book/` (or just `mdbook build book/` if `PATH` is already extended). Confirm:

- HTML backend: exit 0; `book/book/html/06-workspaces.html` exists; the architect's chapter 6 nudges render and any new anchors are reachable.
- Pandoc backend may fail with `/opt/render-mermaid.lua` missing on this host (Sprint 11 saw this — known orthogonal host config issue, not a gate failure). Note in your issue file and move on; the HTML backend is what GitHub Pages serves.
- Chapter 6 PRD cross-links: `grep 'href=.*docs/prd' book/book/html/06-workspaces.html` should still show two absolute `https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/...` URLs and zero `../../docs/prd/` paths (the Sprint 11 published-book-404 fix should still be in place).

### 5. Flag analogous shell-CWD-vs-state-dir gotchas

While you're in the regression sweep, sweep for analogous path-shaped flags that flow into terraform from the user's argv:

- `--backend-config=<path>` if `init` exposes it
- Any other `*File` / `*Path` flags surface in `internal/cli/*.go` that get passed verbatim to a sub-process running with a different CWD

`grep -rn "Flags().String.*[Ff]ile\|Flags().String.*[Pp]ath" internal/cli/` will list candidates. If anything looks suspect, file as a separate validator issue (low-severity, "out-of-scope for v1.4.1 but worth Sprint 13 follow-up") so it doesn't get lost.

## Issue tracking

File at `issues/issue_sprint12_validator.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix | accepted`.

When filing against another agent's surface, include the proposed-fix patch as a markdown diff.

## Scope guardrails

- Do NOT modify `internal/`, `cmd/`, `book/`, `docs/`, `CHANGELOG.md`, `prompts/`.
- You may overwrite `book/book/` build artifacts via `mdbook build` (it's in `.gitignore`); don't commit them.
- Do NOT commit. Do NOT push.

## Verification before reporting done

- All seven sweep gates have a recorded result.
- Bug-fix verification has a literal test-run trace (`go test -run … -v` output, or equivalent).
- Cross-link audit table compares architect's claims to actual staff output.
- `mdbook build` outcome recorded with the HTML backend's exit code + the cross-link grep result.

## Final report

Under 200 words. Cover: seven-step sweep verdict (one-line per step); bug-fix verification (the specific test name + pass count); cross-link audit verdict; `mdbook build` HTML-backend verdict; any analogous-gotcha findings filed as separate issues; final GREEN/RED verdict for the v1.4.1 tag.
