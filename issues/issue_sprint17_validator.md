# Sprint 17 — validator issues (backlog grooming via GitHub issue drafts)

> **Sprint 17 frame.** Backlog-grooming sprint, post-`v1.6.2`. Each
> role surveys its area and drafts GitHub-issue markdown files into
> `prompts/sprint17/staging/<role>/`. Cap: **≤6 drafts**.
> Quality > volume. Lessons from the Sprint 16 follow-up
> (`live-verify-high-issues`, `no-piling-into-active-release`) are
> validator-relevant — file issues that close the gaps that *let* the
> violations happen.

`Status: open | in-progress | resolved | wontfix | accepted`.

_Seeded at kickoff — the validator agent appends its Closure section
below when reporting done._

## Closure

- **Surveyed:**
  - Per-package test coverage (hermetic `go test -cover ./...` from
    repo root with `HOME=$(mktemp -d) KUBECONFIG=`; not sandbox-denied
    in this session).
  - `scripts/**` driver gaps (`e2e-test.sh`, `e2e-test-full.sh`,
    `e2e-test-backends.sh`, `e2e-phase-handoff.sh`, `pre-commit.sh`,
    `integration-test.sh`, `archive-sprint.sh`) — especially the
    A5/A6 assertion shape Issue 3 round-3 added to one driver only,
    and the missing `orphan-check.sh` the Sprint 16 stranded-billing
    incident named.
  - CI matrix gaps (`.github/workflows/ci.yml`, `release.yml`,
    `e2e-full.yml`, `book.yml`, `spellcheck.yml`,
    `tools-images.yml`) — specifically (a) hermetic `HOME` /
    `KUBECONFIG` shape, (b) `gofmt -l` already enforced, (c)
    `internal/orchestration ⊄ internal/cli` boundary grep, (d)
    pre-tag gates for live-verify run-id and release-cycle hygiene.
  - Live-verify-discipline gaps surfaced by Sprint 16 lessons
    (`live-verify-high-issues`, `no-piling-into-active-release`) —
    the `v1.6.2` round-1/round-2 hermetic-GREEN/live-RED cycle is
    the evidence chain the new pre-tag gates close.
  - Cross-referenced Sprint 12-16 ledger
    (`issues/issue_sprint{12,13,14,15,16}_*.md`) and existing
    GitHub backlog (`gh issue list -L 100 --state all` —
    `#1 cos bucket get`, `#2 mermaid PDF`) for dedup.

- **`go test -cover ./...` (run from `/mnt/c/project/roksbnkctl`,
  `HOME=$(mktemp -d) KUBECONFIG= go test -cover ./...`,
  not sandbox-denied):**

  ```
  ?   	github.com/jgruberf5/roksbnkctl	[no test files]
  	github.com/jgruberf5/roksbnkctl/cmd/roksbnkctl		coverage: 0.0% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/cli	1.295s	coverage: 16.9% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/config	0.649s	coverage: 54.2% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/cos	0.118s	coverage: 19.2% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/cred	0.055s	coverage: 30.8% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/doctor	12.567s	coverage: 43.3% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/exec	0.839s	coverage: 64.6% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/ibm	0.250s	coverage: 19.1% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/k8s	0.195s	coverage: 22.9% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/orchestration	0.258s	coverage: 12.1% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/remote	0.504s	coverage: 60.9% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/test	0.904s	coverage: 38.9% of statements
  ok  	github.com/jgruberf5/roksbnkctl/internal/tf	0.335s	coverage: 29.1% of statements
  ?   	github.com/jgruberf5/roksbnkctl/internal/ui	[no test files]
  ok  	github.com/jgruberf5/roksbnkctl/tools/refgen/cobra-md	0.152s	coverage: 84.6% of statements
  ok  	github.com/jgruberf5/roksbnkctl/tools/refgen/tfvars-md	0.915s	coverage: 77.7% of statements
  ```

  Lowest test-bearing package: `internal/orchestration` 12.1% — the
  post-Sprint-16 home of `RunUp`/`RunDown`/`RunPlan`/`RunApply`/
  `RunShell`/`RunExec`/`RunKubeconfig`/`Run*Passthrough`. That number
  is the evidence quoted in draft 05.

- **Candidates considered:** 9 — (1) CI hermetic env, (2) boundary
  grep, (3) pre-tag live-verify run-id gate, (4) `orphan-check.sh`,
  (5) orchestration coverage hole, (6) e2e-driver A5/A6 parity,
  (7) `pre-commit.sh` hermetic-env parity (deferred — same family
  as #1, low marginal value as its own issue), (8) `internal/cos` +
  `internal/ibm` low coverage (deferred — both are SDK-wrapper
  packages where the right test tier is integration, named
  out-of-scope in draft 05), (9) a separate
  `no-piling-into-active-release` checklist issue (folded — draft 03
  already names it as the companion concern; a standalone checklist
  issue would have low independent signal).

- **Dedupes against existing backlog / Sprint 12-16 ledger:** 0
  direct duplicates filed; the close-adjacent items I noted +
  skipped:
  - The chokepoint-invariant guard (Sprint 15 validator Issue 3 /
    `internal/cli/chokepoint_guard_test.go`) already covers the
    per-RunE re-derivation defect class; draft 02 is the orthogonal
    *import-boundary* gate the chokepoint guard's comments claim to
    enforce but in fact do not. Not a dupe — different invariant,
    different failure mode; the issue body names the relationship.
  - Sprint 16 validator Issue 4 (`scripts/e2e-phase-handoff.sh`
    teardown) is `resolved` and ships the inline residue check.
    Draft 04 (`orphan-check.sh`) extracts that inline check into a
    reusable repo utility; the issue body cites Issue 4 as the
    incident that motivated the extraction.
  - Sprint 16 validator Issue 3 round-3 (A5) + option (b) (A6) are
    `resolved` in `e2e-phase-handoff.sh`; draft 06 ports those same
    assertions to the *other* drivers — not a dupe, the validator
    prompt explicitly asks for this gap.
  - GitHub `#1 cos bucket get` (feature) and `#2 mermaid PDF`
    (bug): neither overlaps any draft.

- **Drafted to `prompts/sprint17/staging/validator/`:** 6 (cap).

- **Notable choices:** Capped at 6 by design; the hermetic-CI gap
  (draft 01) and the boundary grep (draft 02) are the two most
  defensible "this would have caught a known defect class" issues
  in the survey, and the orchestration-coverage hole (draft 05)
  cites a hard 12.1% number rather than a vibe. Live-verify
  discipline is closed on two complementary surfaces — the
  CHANGELOG-side runid gate (draft 03) and the IBM-Cloud-side
  residue check (draft 04) — which together backstop the
  `live-verify-high-issues` and `no-piling-into-active-release`
  memories the prompt named. No draft proposes editing an existing
  `_test.go`; every "add coverage" criterion names a new test file
  path.

