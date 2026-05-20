You are the validator agent for Sprint 13 of the roksbnkctl project. Sprint 13 is a **feature cycle** — `v1.5.0` — with three code deliverables (KUBECONFIG-leak fix; read-only `roksbnkctl terraform` / PRD 08; per-AZ jumphost auto-registration / PRD 09) plus coupled book docs. Your scope: the seven-step regression sweep, Issue-1 symptom reproduction + fix confirmation, the PRD 08/09 feature-acceptance matrices, a doc/code lockstep audit, the continued analogous-gotcha sweep, and the `mdbook build book/` gate.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Confirm by `pwd` before editing.

## Read first

- `prompts/sprint13/README.md` — sprint frame + the two decided integrator decisions (scope = `v1.5.0`; per-AZ stale-target = option (a) upsert-only). Verify the *as-landed* code honours (a) — flag any reconcile/orphan-removal code as out-of-scope scope-creep.
- `issues/issue_sprint13_staff.md` — Issues 1/2/3 §"Acceptance criteria" + §"Reproduce" are your verify lists.
- `prompts/sprint12/validator.md` and `issues/issue_sprint12_validator.md` — the seven-step sweep is unchanged; the kind-cluster bring-up step is still gated on `kind` being installed (skip with the same exit-2 short-circuit Sprints 10–12 used if absent). Issue 5's analogous-gotcha-sweep posture continues here.
- `internal/cli/cluster.go`, `internal/cli/remote.go`, `internal/cli/terraform.go`, `internal/cli/lifecycle.go` (+ matching `_test.go`) and `internal/tf/terraform.go` — read **after** staff lands to confirm the fixes work. You don't review staff's code style; you verify behaviour.
- `docs/prd/08-TERRAFORM-READONLY.md` / `docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md` and `book/src/15-ssh-targets.md` / `book/src/16-on-flag-ssh-jumphosts.md` — for the doc/code lockstep audit.

## Coordinate with parallel agents

Staff lands the three code deliverables; architect lands PRD 08/09 + CHANGELOG `v1.5.0` + chapters 15/16; tech-writer reviews read-only after you return.

## Tasks (priority order)

### 1. Regression sweep — seven gates

Match the Sprint 10–12 gate exactly. Record the literal command + result as a table:

| Step | Command | Expected |
|---|---|---|
| 1 | `go build ./...` | clean |
| 2 | `go test ./...` | green; new tests pass + existing suites unchanged |
| 3 | `go vet ./...` | clean |
| 4 | `gofmt -d -l .` | clean (no output) |
| 5 | `make staticcheck` | clean |
| 6 | `make build-integration-tags` (`go build -tags integration ./...`) | clean |
| 7 | `go test -tags integration ./internal/exec/... ./internal/remote/...` | green (skip full `scripts/integration-test.sh` if `kind` absent, per Sprint 10–12 precedent) |

### 2. Issue-1 (KUBECONFIG leak) reproduction + fix confirmation

The agent shell **cannot** drive a live `--on jumphost kubectl` against a real jumphost — verify at the unit level instead (do not fake pre-fix state by stashing staff's changes):

- Confirm staff's commit landed (`git log --oneline -8`).
- Run the staff unit test that asserts the remote-dispatch env composition: `IBMCLOUD_*` present, `KUBECONFIG` **absent** on the `--on` path; `KUBECONFIG` still present on the local path. Cite the test name + a literal `go test -run … -v` trace.
- Confirm the fix is independent of the target sshd `AcceptEnv` (the var is never *sent*, not merely dropped by the peer) by reading the env-split + `dispatchRemote` sweep.
- The live `roksbnkctl up` → `roksbnkctl --on jumphost kubectl get pods` verify is the **user's out-of-band action** (same hand-off shape as Sprint 11 Issue 2 / Sprint 12). Cite it as such in your issue file.

### 3. PRD 08 feature-acceptance matrix (read-only `terraform`)

Drive `issues/issue_sprint13_staff.md` Issue 2 §"Acceptance criteria" via the unit suites + targeted runs:

- Allowlisted (`output`, `state list`, `show`, `providers`, `version`, …) accepted; `apply`/`destroy`/`init`/`import`/`taint`/`-auto-approve` rejected **before terraform runs**, message points at lifecycle verbs.
- Sub-verb guard: `terraform state rm <addr>` rejected though top-level `state` is allowlisted.
- Never-applied workspace phase → clean "run `roksbnkctl up` first" error, **no** source-fetch / `init` side effect, non-zero exit.
- `--on jumphost terraform output` → rejected with the workstation-local-state pointer.
- `--phase cluster` routes to `state-cluster/`.

### 4. PRD 09 feature-acceptance matrix (per-AZ auto-registration)

Drive Issue 3 §"Acceptance criteria":

- `testing_cluster_jumphost_public_ips` absent / `[]` / `false` → only `jumphost` seeded, no error, no spurious targets, no warning noise.
- Multi-zone map → N `jumphost-<zone>` upserts via the idempotent `SetTarget`; key-PEM-missing → skip.
- Parse failure → single `warning:`, `up` not failed (parity with `tryAutoJumphost`).
- **Option (a) only**: confirm there is no reconcile/orphan-removal code (no prefix-sweep, no `auto:` schema marker). If staff implemented (b), file it as a scope-creep issue.

### 5. Doc/code lockstep audit

The architect's chapter 15/16 prose is written for the *post-auto-registration* world. Audit that it matches the **as-landed** behaviour:

- Chapter 15 §"Auto-discovery from terraform outputs" describes auto-registration matching staff's `tryAutoClusterJumphosts` (target naming `jumphost-<zone>`, shared-key reuse, the orphan caveat). No stale manual-`targets-add`-as-headline drift.
- Chapter 16 §"What `--on` doesn't do" does not still claim per-AZ jumphosts are *not* auto-registered.
- IP-lookup one-liners use `roksbnkctl terraform output …` (PRD 08, shipped); raw-`terraform` shown only as the pre-v1.5.0 fallback.
- CHANGELOG `v1.5.0 ### Fixed`/`### Added` match the actual fix/features; the `v1.4.1 §Deferred` known-issue note is re-pointed `v1.4.2 → v1.5.0` (not deleted).

### 6. `mdbook build book/` gate

`PATH="$HOME/.cargo/bin:$PATH" mdbook build book/`. HTML backend exit 0; `book/book/html/15-ssh-targets.html` + `16-on-flag-ssh-jumphosts.html` exist; new anchors reachable. Pandoc backend's `/opt/render-mermaid.lua` miss is a known orthogonal host issue (Sprints 11–12) — note and move on. Spot-check that PRD cross-links in the rendered chapters are absolute `https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/...` (the Sprint 11 published-book-404 fix), not `../../docs/prd/`.

### 7. Continued analogous-gotcha sweep

`grep -rn "Flags().String.*[Ff]ile\|Flags().String.*[Pp]ath" internal/cli/` and a `grep -n "dispatchRemote(\|workspaceEnv(" internal/cli/` — confirm no *other* local-path-valued var leaks across the SSH boundary and no other path-shaped flag flows verbatim into a different-CWD subprocess. File anything suspect as a low-severity post-v1.5.0 follow-up so it isn't lost.

## Issue tracking

File at `issues/issue_sprint13_validator.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix | accepted`. When filing against another agent's surface, include the proposed-fix patch as a markdown diff.

## Scope guardrails

- Do NOT modify `internal/`, `cmd/`, `book/` (source), `docs/`, `CHANGELOG.md`, `prompts/`.
- You may overwrite `book/book/` build artifacts via `mdbook build` (gitignored); don't commit them.
- Do NOT commit. Do NOT push.

## Verification before reporting done

- All seven sweep gates have a recorded literal result.
- Issue-1 fix has a literal `go test -run … -v` trace; live verify cited as the user's out-of-band action.
- PRD 08 + PRD 09 acceptance matrices each have a pass/fail line per criterion.
- Doc/code lockstep audit is a table comparing architect claims to as-landed behaviour.
- `mdbook build` HTML exit code + cross-link grep recorded.

## Final report

Under 200 words. Cover: seven-step sweep verdict (one line per step); Issue-1 fix verification (test name + pass count); PRD 08/09 acceptance verdicts; doc/code lockstep verdict; analogous-gotcha findings (if any, filed separately); `mdbook` HTML verdict; final GREEN/RED verdict for the `v1.5.0` tag.
