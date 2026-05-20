You are the **architect** agent (light scope) for a Sprint 16
follow-up dispatch on the `roksbnkctl` project. Repo root:
`/mnt/c/project/roksbnkctl`. You run with no memory of prior
conversation.

## Read first

1. `issues/issue_sprint16_validator.md` — **Issue 2** (the bug being
   fixed: `up` second phase duplicate-creates the cluster VPC /
   transit gateway / client VPC).
2. `prompts/sprint16/followup-issue2-README.md` — integrator
   decisions. Decision 1 binds you: this is a **user-facing bugfix**,
   `### Fixed`, expected shape `v1.6.2` patch.
3. `CHANGELOG.md` (top — the `v1.6.1` Sprint 16 entry is the format
   template; note the Keep-a-Changelog + SemVer conventions in the
   header) and `docs/PLAN.md` §"Sprint 16" (~line 1074) for the
   existing Sprint 16 framing you are appending a follow-up note to.

## Tasks

1. **CHANGELOG.md** — add a new `## v1.6.2 — <today's date,
   YYYY-MM-DD>` section above `## v1.6.1`, with a `### Fixed` block. One
   tight entry: `up` no longer fails with IBM Cloud "Provided Name …
   is not unique" / "gateway … already exists" when the bnk/testing
   phase runs after the cluster phase — the second phase now reuses the
   cluster-phase VPC / transit gateway / client VPC
   (`use_existing_cluster_vpc` + `existing_cluster_vpc_id` +
   `testing_create_client_vpc=false` handoff via
   `cluster-outputs.json`) instead of re-creating same-named
   resources. Reference `docs/PLAN.md §"Sprint 16"` and note it closes
   `issues/issue_sprint16_validator.md` Issue 2. User-facing tone,
   no internal jargon. Do **not** assert the version is final — write
   the section but note in your issue file that the tag is
   integrator-owned at cut and is gated on the live `!` verify
   (decision 3).

2. **docs/PLAN.md** — append a short follow-up note to the §"Sprint 16"
   section (do not rewrite existing content): a 3–5 line subsection
   recording that a post-`v1.6.1` live-verify surfaced the
   phase-handoff regression (validator Issue 2), that the phase-1b
   parity gate was correct-but-blind to it (no hermetic test exercised
   a post-cluster-phase workspace), the fix (existing-resource handoff
   completed in terraform + Go), and that closure is gated on the live
   `!` verify per the `live-verify-high-issues` discipline. Cross-link
   the validator Issue 2.

## Constraints

- Light scope: only `CHANGELOG.md` and `docs/PLAN.md`. Do not touch
  Go, terraform, scripts, tests, or the other agents' issue files.
- `### Fixed`, not `### Changed` (decision 1) — `v1.6.1` was "no
  user-visible behavior change"; this is the opposite, a user-facing
  fix.
- Do **not** commit. The integrator commits. Do not push.

## Verify before reporting done

- CHANGELOG renders (valid markdown, date filled, section ordered
  above `v1.6.1`, `### Fixed` used).
- PLAN follow-up note is additive (existing §"Sprint 16" text
  unchanged) and cross-links Issue 2.

## Issue file

Write `issues/issue_sprint16_architect.md` (append a
`## Issue 2 — CHANGELOG/PLAN follow-up` section if the file exists; do
not delete prior content). Schema: `**Severity**`, `**Status**`,
`**Description**`, `**Files affected**`, `**Related**`. Note explicitly
that the version tag is integrator-owned and gated on the live `!`
verify.

## Final report

≤150 words: the CHANGELOG `### Fixed` wording, the PLAN follow-up note,
confirmation it is `### Fixed`/additive, and the "tag + closure are
integrator-owned, gated on live `!` verify" caveat. State you did not
commit.
