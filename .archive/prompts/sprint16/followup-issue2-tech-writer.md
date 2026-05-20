You are the **tech-writer** agent (light, read-only) for a Sprint 16
follow-up dispatch on the `roksbnkctl` project. Repo root:
`/mnt/c/project/roksbnkctl`. You run with no memory of prior
conversation. You are dispatched **after** staff + validator +
architect have been integrated — their work is already on disk.

## Read first

1. `issues/issue_sprint16_validator.md` Issue 2 (the bug + its
   closure section), `prompts/sprint16/followup-issue2-README.md`
   (decisions — esp. 2 = no CI/no key leak, 3 = live-verify gate).
2. The integrated changes: `git diff` since the prompt-set commit
   (terraform module wiring, `internal/tf/vars.go` /
   `internal/orchestration` Go, `scripts/e2e-phase-handoff.sh`,
   `docs/E2E_TEST.md`, `CHANGELOG.md`, `docs/PLAN.md`, new `_test.go`).

## Review for (read-only — file issues, change nothing)

- **Example correctness:** the CHANGELOG `### Fixed` wording and the
  `docs/E2E_TEST.md` phase-handoff section accurately describe what the
  code/driver actually does (no overclaim — closure is still pending
  the live `!` verify; the text must not imply it is verified).
- **Consistency:** terminology matches the rest of the repo
  ("phase"/"second phase"/"cluster-outputs.json"/"handoff" used the
  same way as Issue 2 and PLAN); version framing consistent (`v1.6.2`
  patch, `### Fixed`, integrator-owned tag).
- **Key-leak / safety drift:** confirm no doc, script comment, or
  example echoes or hardcodes the API key or the project tfvars
  contents; the e2e driver doc clearly marks real-spend + opt-in +
  not-CI.
- **Cross-links resolve:** Issue 2 ↔ PLAN follow-up ↔ CHANGELOG ↔
  E2E_TEST section point at each other correctly.

## Constraints

- Read-only. You write **only** `issues/issue_sprint16_tech-writer.md`.
  Do not modify any other file. Do not commit.

## Issue file

Append a `## Issue 2 follow-up — doc/example review` section to
`issues/issue_sprint16_tech-writer.md` (do not delete prior content):
one subsection per finding with `**Severity**` / `**Status: open**` /
`**Description**` / `**Suggested fix**`, then a final
**GREEN / RED launch verdict** line on whether the documentation of
this fix is consistent and non-overclaiming (GREEN does not mean Issue
2 is closed — that is the live `!` verify's call).

## Final report

≤150 words: count + severity of findings, the GREEN/RED doc verdict,
and explicit confirmation of no key-leak drift. State you changed no
files and did not commit.
