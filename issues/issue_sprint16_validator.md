# Sprint 16 — validator issues (consolidation phase-1b, post-v1.6.0)

> **Sprint 16 frame.** See `prompts/sprint16/README.md` + `docs/PLAN.md`
> §"Sprint 16". Headline gate = behavior parity (zero pre-existing
> test-file diffs vs the `v1.6.0` tag; Sprint 14 e2e/`--on` +
> Sprint 15 chokepoint guards green & **unedited**); full hermetic
> `go test -race ./...`; `cli` phase-1b boundary/import audit. If this
> agent's session is toolchain-denied (Sprint 15 validator Issue 1
> precedent), record a `blocker` with the exact denied commands and
> hand the gate to the integrator — do not fake results.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Behavior-parity gate + boundary audit — **integrator-run (validator/staff sessions toolchain-denied)**

`Status: resolved` — the Sprint 16 staff session had `go test` execution sandbox-denied (recorded in `issues/issue_sprint16_staff.md` §Closure, the documented Sprint 15 precedent); a separate validator agent would hit the same wall, so the integrator ran the full gate directly. **2026-05-19, all green:**

- **Behavior parity (HEADLINE):** `git diff --stat v1.6.0 -- <every _test.go tracked at v1.6.0>` → **empty** (zero pre-existing test-file diffs); Sprint 14 `lifecycle_e2e_test.go`/`lifecycle_e2e_integration_test.go`/`env_split_test.go` + Sprint 15 `chokepoint_guard_test.go` **byte-identical**. The only new test is the pre-existing `internal/orchestration/chokepoint_test.go` (already in v1.6.0). No pre-existing test edited.
- **Full hermetic `go test -race ./...`** (CI's exact command, `HOME`=empty, `KUBECONFIG` unset) → **all 14 packages `ok`, RACE_EXIT=0**, incl. `internal/cli` (thinned adapter), `internal/orchestration` (new home), and the Sprint 14/15 guards. `internal/test::TestProbe_TruncatedFlag` (pre-existing full-`-race` flake, refactor-untouched) did not recur.
- `go build ./...` / `go vet ./...` clean; `gofmt -l internal/` empty.
- **Boundary/import audit:** `internal/orchestration` does **not** import `internal/cli` (grep-clean — one-directional boundary held under the function-field dependency-injection shape); `internal/cli/lifecycle.go`+`cluster.go` are thin cobra adapters; `internal/orchestration/{lifecycle,cluster}.go` (≈64 KB) hold the moved RunE orchestration.
- Sprint 14 kubeconfig fix not regressed (`selfheal.go` untouched; e2e/`--on` guards green); Sprint 15 chokepoint guard green & unedited.

**Verdict: GREEN.** The phase-1b move is behavior-parity-proven at the test level, not just statically. Tag/version (`v1.6.1` strict-SemVer vs `v1.7.0`) is integrator-owned at cut.
