# Sprint 17 — staff issues (backlog grooming via GitHub issue drafts)

> **Sprint 17 frame.** Backlog-grooming sprint, post-`v1.6.2`. Each
> role surveys its area and drafts GitHub-issue markdown files into
> `prompts/sprint17/staging/<role>/`; the integrator reviews and files
> via `gh issue create`. No code change, no PRD, no release tag — the
> deliverable is the GitHub backlog the next development cycle pulls
> from. Cap: **≤8 drafts**. Quality > volume.

`Status: open | in-progress | resolved | wontfix | accepted`.

_Seeded at kickoff — the staff agent appends its Closure section
below when reporting done._

## Closure

- Surveyed:
  - Go-source `TODO|FIXME|XXX|HACK|BUG` sweep across `internal/`,
    `cmd/`, `tools/` (one tagged TODO in `internal/cli/meta.go:150`,
    plus the `internal/k8s/openshift.go` stub).
  - `docs/prd/*.md` §"Open questions" / §"Out of scope" lists across
    PRDs 01–09 (PRD 02 §Phase 2.1, PRD 06 §"Open questions" items 2
    and 4, PRD 07 §"Open questions" item 1, PRD 09 §"Open questions"
    items 1–2).
  - `CHANGELOG.md` `### Deferred (v1.x roadmap, post-vX.Y.Z)` blocks
    from `v1.0.0` through `v1.6.2` — the recurring `ops install` /
    `ops uninstall` snapshot and per-AZ jumphost reconcile carries.
  - `docs/PLAN.md` §"What's deliberately deferred to post-v1.0" Code
    subsection — the OpenShift CRDs / `--backend k8s|ssh` terraform /
    Multi-hop SSH ProxyJump set.
  - Coherence sweep of the Sprint 16 phase-1b code:
    `internal/orchestration/applied_replay.go`,
    `internal/orchestration/second_phase_reuse.go`,
    `internal/cli/cluster_phase.go` cluster-down override-stripping —
    surfaced the `sourceLabel()` missed-label coherence gap.
  - GitHub backlog (`gh issue list -L 100 --state all`): only #1 (cos
    bucket get) and #2 (mermaid PDF text).

- Candidates considered: 10 (the 7 below + 3 rejected as
  low-signal: `looksTransient` heuristic polish, RHEL/CentOS/Alpine
  apt-bootstrap — PRD-03-explicit out-of-scope and unchanged across
  5 sprints with no user demand, `--truncated` DNS flag — Sprint 6
  v1.0 carry with no user demand across 7 sprints).

- Dedupes against existing backlog / in-tree ledger: 0 of the 7
  surviving candidates collide. Cross-checked against GitHub #1/#2
  (both orthogonal — COS recursive download / mermaid PDF text)
  and against Sprint 12–16 ledger items marked
  `accepted`/`deferred`/`wontfix` (Sprint 13 option-(b) carry is
  explicitly named "post-v1.5.0 follow-up, do not implement this
  cycle"; the in-tree resolved item there ships option (a), the
  GitHub issue here is option (b) — distinct deliverable).

- Drafted to staging/staff/: **7** (`01-doctor-target-backend-prefix.md`,
  `02-bnk-phase-override-source-label.md`,
  `03-per-az-jumphost-reconcile-option-b.md`,
  `04-skip-cluster-refresh-flag.md`,
  `05-roksbnkctl-migrate-legacy-single.md`,
  `06-ops-install-uninstall-snapshot.md`,
  `07-openshift-typed-client-k-get.md`).

- Notable choices:
  - The `doctor` ssh-target backend-prefix item is the smallest
    bug-report on the list (one-token fix) but is the only one with
    a stale `TODO(phase3)` in tree pointing at it — kept because the
    PRD precondition has met and the fix lets the stale comment go.
  - The `roksbnkctl migrate` feature (#05) is the largest single
    item — a destructive state-move with a live-`!`-verify
    requirement per `live-verify-high-issues` discipline. Flagged
    that the validator MUST own a gated `scripts/e2e-migrate.sh`
    driver before close; the issue acceptance criteria reflect it.
  - The `bnk-phase-override.tfvars` `sourceLabel()` coherence gap
    (#02) was a judgement call as bug vs feature — settled bug
    because the symmetric `cluster-phase-override.tfvars` label
    already exists and the round-2 Sprint 16 ship missed updating
    the sibling switch arm. The Sprint 12 architect §"Optional PRD
    07 follow-up" note specifically named `sourceLabel()`'s hard-
    coded label set as the audit surface that would re-flag a new
    file; this is that re-flag.

The staff agent did **not** run `gh issue create` and did **not**
commit. On-disk deliverables are exclusively the seven drafts under
`prompts/sprint17/staging/staff/` (cap ≤8) plus this closure
section. Integrator owns the filing pass.
