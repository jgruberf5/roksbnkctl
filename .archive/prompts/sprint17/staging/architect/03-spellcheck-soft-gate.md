---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: spellcheck.yml runs with `continue-on-error: true` — typos never gate a merge'
labels: []
assignees: ''
---

## Symptom

`.github/workflows/spellcheck.yml` sets `continue-on-error: true` at the **job** level (line 11). Combined with the workflow having only one job (`spell`), the practical effect is that any cspell hit — a misspelled word in `book/src/**/*.md`, `docs/**/*.md`, or a Go source file — produces a **yellow-warn check on the PR but does not block merge**, and the workflow's `Status` rolls up as success in the merge-queue's required-checks list (which `spellcheck` is not on anyway). The check exists; it does not enforce anything.

The result is that every Sprint 12–16 cycle has been able to land typos that cspell would have flagged, because (a) reviewers rarely click into a passing-yellow check, and (b) main-branch enforcement is absent. This is the discoverability-of-failure gap the Sprint 17 README explicitly calls out for the `spellcheck.yml` audit ("is it run pre-merge?").

The intent appears to have been "yellow not red, so I don't get blocked on a noisy initial rollout" — but the cspell config (`cspell.json`) has had time to settle (the project is at `v1.6.2`), and the safety wheel is now just a way for typos to merge unchallenged.

## Reproduction

```
# 1. on a throwaway branch, plant an obvious typo in the book
git checkout -b spellcheck-soft-gate-demo
sed -i '1iThe rokssbnkctl tool deploys to ROKSS.' book/src/01-what-is-bnk.md

# 2. push + open a PR
git add book/src/01-what-is-bnk.md
git commit -m "demo: typo to surface the soft-gate"
git push -u origin spellcheck-soft-gate-demo
gh pr create --fill

# 3. observe the result
gh pr checks
# expected: a red ❌ on "Spellcheck" that blocks merge until the typo is fixed
# actual:    a yellow ⚠️  on "Spellcheck"; PR is mergeable, "Merge pull request"
#            button is enabled, "All checks have passed" appears in the merge box
#            because continue-on-error converts the job's failure into a "skipped/neutral"
#            outcome that GitHub treats as success at the PR-status-rollup layer.

# 4. dig out the cspell hit from the run log to confirm it ran
gh run view --log-failed --job=spell 2>/dev/null || gh run view --log --job=spell | grep -i 'unknown word'
# the typo IS flagged by cspell internally; the soft-gate hides it from the PR-merge surface.
```

## Expected behavior

Typos in `book/src/**/*.md`, `docs/**/*.md`, and `**/*.go` (the workflow's existing scope) **block PR merge** until they are fixed or whitelisted in `cspell.json`. A reviewer's "All checks passed" signal means cspell genuinely passed, not "cspell ran and found something but we agreed to ignore it".

## Actual behavior

`continue-on-error: true` at the job level converts a cspell failure into a non-blocking warning. Combined with the workflow not being in the branch-protection required-checks list (verified via `gh api repos/jgruberf5/roksbnkctl/branches/main/protection`), there is no path by which a cspell hit prevents a merge. The workflow's signal is informational at best, decorative at worst.

```yaml
# .github/workflows/spellcheck.yml:9-14
jobs:
  spell:
    runs-on: ubuntu-latest
    continue-on-error: true         # <-- this line
    steps:
      - uses: actions/checkout@v4
      - uses: streetsidesoftware/cspell-action@v6
```

## Environment

- `roksbnkctl version`: (n/a — CI defect)
- OS / arch: (n/a — fails on ubuntu-latest, the only runner this workflow uses)
- IBM Cloud region: (n/a)
- Backend: (n/a)
- Affected workflow: `.github/workflows/spellcheck.yml` (full file).

## Suspect pipeline / hypotheses (optional)

1. **Most likely:** the `continue-on-error: true` was a deliberate soft-launch wheel for when `cspell.json` was new and false-positive-heavy. The project has shipped seven minor/patch versions since (v1.0.x → v1.6.2) and the cspell dictionary has stabilized — the wheel was never removed because nothing forces a "demote-to-hard-gate" review.
2. Less likely: the author intended the `continue-on-error` to apply only to a specific cspell-rollout step (not the whole job). The current YAML attaches it to the job, so every step in the job inherits the soft-failure semantic regardless of intent.

## Acceptance criteria

1. The reproduction's PR-with-deliberate-typo produces a **red ❌** "Spellcheck" check that blocks merge (criterion failed pre-fix per step 3 above; passes post-fix).
2. `.github/workflows/spellcheck.yml`: the `continue-on-error: true` line is removed (or, if the maintainers prefer a *narrower* soft-gate, demoted from the job level to a single named step with a comment explaining why that step's failure is informational — the bare job-level form is unambiguously wrong).
3. `cspell.json` is updated in the same PR with any whitelist entries for currently-merged terms that would have failed under a hard gate (run `npx cspell "book/src/**" "docs/**" "**/*.go"` locally; add to the `words` array the legitimate technical terms it surfaces — IBM Cloud product names, terraform variable names, etc.). This is the one-time get-clean step; subsequent PRs land on a clean tree.
4. The "Spellcheck" check is added to the branch-protection required-checks list (`gh api -X PATCH repos/.../branches/main/protection ...`), or — if branch protection is intentionally permissive — a note is added to `CONTRIBUTING.md` (or wherever the existing CI gates are documented) stating that Spellcheck is now hard-failing and PRs must keep it green.
5. Regression check: after the fix, intentionally re-introducing the demo typo on a second throwaway branch reproduces a red Spellcheck and a merge block (mirror of criterion 1, run as a one-off sanity check by the integrator before close).

## Out of scope (deliberately)

- Expanding cspell's scope to additional file globs (e.g. `internal/**/*.go`, terraform `.tf`, scripts). Tighten the existing gate first; widen scope in a separate issue once the hard-gate baseline is clean.
- Switching to a different spellchecker (codespell, hunspell, languagetool). cspell is in place and known; the defect is the gate, not the engine.
- Auditing every other workflow's `continue-on-error` usage. Spellcheck is the only one with a job-level soft-fail right now (verified `grep -rn 'continue-on-error' .github/workflows/`); file separately if a future workflow regresses.
- The dictionary maintenance policy ("who reviews `cspell.json` additions"). Out of scope for landing the gate.

## Notes

- Cross-link: this defect is sibling to the broken-anchor class — both are "the book/CI surface produces a passing signal that is, in practice, lying about quality". Fixing both raises the floor on what "green PR checks" actually means.
- The Sprint 12 cycle's tech-writer issue list (`issues/issue_sprint12_tech-writer.md`) called out drift sweeps as the dominant book-quality control. A hard spellcheck gate is the cheapest, most mechanical complement to those sweeps — it picks up the rote layer so reviewers' attention stays on the prose-level drift only humans can spot.
- The fix is a one-line YAML deletion + a one-shot `cspell.json` clean. The acceptance work is mostly in confirming that the clean baseline doesn't regress overnight (criterion 5).
