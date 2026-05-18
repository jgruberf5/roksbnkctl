You are the validator agent for Sprint 14 — a **get-well cycle** folding into the held `v1.5.0`. Scope: the seven-step regression sweep (now including the new e2e + `--on` integration test), confirming the kubeconfig fix at the gate level, the doc-caveat-removal audit, and the `mdbook build book/` gate. The live `roksbnkctl up → --on jumphost kubectl get pods` end-to-end verify is the **user's out-of-band action** (baseline repro = the 2026-05-18 14:54 diagnostic) — but, unlike Sprint 13, it is now backed by staff's e2e/integration gate, which is the point of this cycle.

Project location: `/mnt/c/project/roksbnkctl/`. Confirm by `pwd`.

## Read first

- `prompts/sprint14/README.md` — decided integrator decisions (hold-and-merge; option C; blind-spot test pulled forward; Sprint 15 out of scope).
- `issues/issue_sprint14_staff.md` Issue 1 + `issues/issue_sprint13_architect.md` Issue 2 — the design surface + acceptance.
- `issues/issue_sprint13_staff.md` Issue 1 §"Closure" — the 2026-05-18 14:54 live diagnostic (the repro baseline: `KUBECONFIG=[]`, `/home/ubuntu/.kube/config: No such file or directory`).
- `prompts/sprint13/validator.md` + `issues/issue_sprint13_validator.md` — the seven-step sweep is unchanged; kindless skip precedent unchanged.
- After staff lands: `terraform/modules/testing/main.tf` (part A), `internal/cli/` part-B + the new `lifecycle_e2e_test.go` / integration test.

## Tasks (priority order)

### 1. Regression sweep — seven gates

Record literal command + result as a table (same shape as Sprint 13):

| Step | Command | Expected |
|---|---|---|
| 1 | `go build ./...` | clean |
| 2 | `go test ./...` | green incl. the new `lifecycle_e2e_test.go` |
| 3 | `go vet ./...` | clean |
| 4 | `gofmt -d -l .` | clean |
| 5 | `make staticcheck` | clean |
| 6 | `make build-integration-tags` | clean |
| 7 | `go test -tags integration ./internal/...` | green; the new `--on` integration smoke runs (skip kind bring-up if `kind` absent, per Sprint 10–13 precedent; the `internal/exec` `/home/runner/.bluemix` host-perm FAIL is the known sandbox limit, not a regression) |

### 2. Kubeconfig-fix gate verification (the blind-spot closure)

- Confirm the new `internal/cli/lifecycle_e2e_test.go` asserts BOTH the remote-vs-local env composition AND the part-B self-heal path (no remote kubeconfig → heal; cluster-down → real error surfaced, no infinite spin). Cite test names + a literal `go test -run … -v` trace.
- Confirm part A: `grep -n "|| true" terraform/modules/testing/main.tf` shows the kubeconfig-critical `ibmcloud login` / `ks cluster config --admin` are no longer silently swallowed (retry + loud `/var/log/...` marker / sentinel instead). `terraform fmt -check terraform/modules/testing/` clean (note if terraform unavailable in shell).
- The Sprint 13 Issue-1 regression guard must be present and green (the human-found bug is now a permanent test).
- **Live verify hand-off**: `roksbnkctl up` then `roksbnkctl --on jumphost kubectl get pods` succeeding end-to-end (no `localhost:8080`) is the user's out-of-band action — cite it as such with the 2026-05-18 14:54 diagnostic as the before-state. Do not fake it from the agent shell.

### 3. Doc-caveat-removal audit (architect surface)

- `grep -n "unset KUBECONFIG\|Known issue\|may still fail" CHANGELOG.md` — no standing known-issue caveat remains; the `## Unreleased (v1.5.0)` block reads as one coherent release (env leak + kubeconfig both fixed); the `v1.4.1 §Deferred` known-issue note is removed.
- `grep -rn "pre-v1.5.0\|may still fail\|unset KUBECONFIG\|known issue" book/src/` — no live caveat about the `--on` kubeconfig flow in ch 16/15/09; per-AZ auto-registration + orphan (option a) caveat still intact (unrelated, must NOT be removed).

### 4. `mdbook build book/` gate

`PATH="$HOME/.cargo/bin:$PATH" mdbook build book/` — HTML exit 0; ch15/16/09 render; no dangling cross-link to a removed caveat anchor. Pandoc `/opt/render-mermaid.lua` miss is the known orthogonal host issue — note and move on.

### 5. Continued analogous-gotcha sweep

Briefly re-confirm no *other* local-context value crosses the SSH boundary (the Sprint 13 sweep + this fix); file anything new as a low-severity Sprint 15 (consolidation) input, not a Sprint 14 blocker.

## Issue tracking

File at `issues/issue_sprint14_validator.md`. Severity/Status conventions as prior sprints. Proposed cross-surface fixes as markdown diffs.

## Scope guardrails

- Do NOT modify `internal/`, `cmd/`, `terraform/`, `book/` (source), `docs/`, `CHANGELOG.md`, `prompts/`. You may overwrite gitignored `book/book/` artifacts.
- Do NOT evaluate the Sprint 15 consolidation here (chokepoint/decomposition/tiering are not Sprint 14).
- Do NOT commit or push.

## Verification before reporting done

- All seven gates recorded; the new e2e/`--on` test has a literal trace.
- Part A `|| true` removal + part B heal-vs-outage discrimination confirmed (via the e2e test + grep).
- Caveat-removal grep results recorded for both CHANGELOG and book.
- mdbook HTML exit code recorded.

## Final report

Under 200 words. Cover: seven-step sweep verdict (one line/step); kubeconfig-fix gate verification (test names + the part-A grep); live-verify hand-off citation; caveat-removal audit verdict; mdbook verdict; final GREEN/RED verdict for the now-unblocked `v1.5.0` tag (note explicitly that the kubeconfig fix is now gate-caught, not only human-caught).
