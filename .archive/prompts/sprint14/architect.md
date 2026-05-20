You are the architect agent for Sprint 14 — a **get-well cycle** folding into the held `v1.5.0`. **Light cycle**: no PRD; the only book surface is caveat **removal**. Your scope: `CHANGELOG.md` (fold the kubeconfig fix into the open `## Unreleased (v1.5.0)` block + **remove** the carried known-issue notes) and `book/src/` chapters 15/16/09 (delete the now-false "may still fail / pre-v1.5.0 caveat" prose). **Do not touch `internal/`, `cmd/`, `terraform/`. Do not rewrite `docs/PLAN.md` §"Sprint 14"/"Sprint 15" — integrator-authored; touch only to fix a provable drift.**

Project location: `/mnt/c/project/roksbnkctl/`. Confirm by `pwd`.

## Read first

- `prompts/sprint14/README.md` — the three decided integrator decisions (hold-and-merge; option C; blind-spot test pulled forward; Sprint 15 consolidation out of scope).
- `issues/issue_sprint14_architect.md` — your ledger; Issue 1 is the CHANGELOG/book caveat-removal deliverable.
- `issues/issue_sprint13_architect.md` Issue 2 — the root cause + option-C decision + the hold-and-merge integrator note. This issue **flips to `resolved`** when Sprint 14's gate passes; reference it from the CHANGELOG.
- `CHANGELOG.md` top — there is an open `## Unreleased (v1.5.0)` block (NOT a cut `## v1.5.0 — <date>`; confirm). It already has the env-leak `### Fixed` bullet and a carried known-issue note; the `v1.4.1` entry's `### Deferred` block has the `**Known issue …**` note re-pointed to v1.5.0. Both known-issue notes must now be **removed** (the bug is fixed), not re-pointed again.
- `book/src/16-on-flag-ssh-jumphosts.md`, `book/src/15-ssh-targets.md`, `book/src/09-registering-existing-cluster.md` — locate (by section text, line numbers drift) any "pre-v1.5.0 / may still fail / unset KUBECONFIG / known-issue" caveat the Sprint 13 cycle added; these become false once the kubeconfig fix lands.
- `prompts/sprint12/architect.md` — prior light-cycle shape.

## Coordinate

Staff lands parts A+B + the e2e test in `terraform/` + `internal/`. Validator runs the sweep + cites the user live-verify. Tech-writer confirms (read-only) the caveats are fully gone. **Do not touch `internal/`, `terraform/`.**

## Tasks (priority order)

### 1. CHANGELOG — fold the kubeconfig fix into the held `v1.5.0`, remove the known-issue notes

- In the open `## Unreleased (v1.5.0)` `### Fixed`: add a bullet for the jumphost kubeconfig provisioning fix — name the symptom (`up` → `--on jumphost kubectl|oc` → `localhost:8080` even after the env-leak fix), the root cause (silent `|| true` on cloud-init `ibmcloud login` + `ks cluster config --admin`; jumphost never got `/home/ubuntu/.kube/config`), and the two-layer fix (option C: cloud-init retry/loud-failure + roksbnkctl `--on` self-heal). Cross-link `issues/issue_sprint13_architect.md` Issue 2.
- Reconcile the `v1.5.0` story so it reads as **one coherent release**: the env-leak fix and the kubeconfig fix together make `--on jumphost kubectl|oc` work end-to-end. The two `### Fixed` bullets should not read as two half-stories.
- **Remove** the carried known-issue note in the `## Unreleased (v1.5.0)` block (the "may still fail … fix targeted for …" text) — the bug is fixed in this same release.
- **Remove** the `**Known issue (fix targeted for v1.5.0):**` note from the `## v1.4.1` `### Deferred` block. v1.4.1 still shipped with the leak; reference that it is resolved in v1.5.0 via a normal cross-link if useful, but the standing "known issue / workaround `unset KUBECONFIG`" callout must go (it is no longer true post-v1.5.0).

### 2. Book — delete the now-false caveats (ch 16 / 15 / 09)

- Remove every "pre-v1.5.0 fallback / may still fail / `unset KUBECONFIG` workaround / known-issue" aside the Sprint 13 cycle added to chapters 16, 15, 09 around the `--on jumphost kubectl|oc` flow and the per-AZ jumphost docs. Post-Sprint-14 the documented happy path simply **works** — the prose should state it plainly with no hedge.
- Keep the per-AZ auto-registration content and the orphan caveat (option (a)) — those are unrelated and still accurate. Only the kubeconfig-brokenness hedges go.
- `mdbook build book/` after edits — HTML backend exit 0 (pandoc `/opt/render-mermaid.lua` miss is the known orthogonal host issue; note and move on). Confirm no dangling cross-link to a removed caveat anchor.

### 3. (Optional) PLAN drift check

`docs/PLAN.md` §"Sprint 14"/"Sprint 15" are integrator-authored. Only if you find a provable inconsistency with the as-landed CHANGELOG/book, file it in your ledger with a proposed one-line fix — do not rewrite the sections.

## Issue tracking

File at `issues/issue_sprint14_architect.md`. One issue per finding. Severity/Status conventions as prior sprints. Proposed fixes against another agent's surface as markdown diffs.

## Scope guardrails

- Do NOT touch `internal/`, `cmd/`, `terraform/`, `prompts/`, `Makefile`, `scripts/`.
- Do NOT re-point the known-issue notes again — **remove** them (the fix ships in the same v1.5.0).
- Do NOT introduce a `v1.5.1`/`v1.6.0` heading — this is the held `v1.5.0`.
- Do NOT commit or push.

## Verification before reporting done

- `## Unreleased (v1.5.0)` has both fixes (env leak + kubeconfig) reading as one coherent release; zero remaining "known issue / may still fail / unset KUBECONFIG" text in CHANGELOG (`grep -n "unset KUBECONFIG\|Known issue\|may still fail" CHANGELOG.md` → only historical-accurate references, no standing caveat).
- `grep -rn "pre-v1.5.0\|may still fail\|unset KUBECONFIG\|known issue" book/src/0*.md book/src/1*.md` → no live caveat about the `--on` kubeconfig flow.
- `mdbook build book/` HTML exit 0; no `internal/`/`terraform/` files touched; PLAN.md §Sprint 14/15 unmodified (or only a proven-drift fix).

## Final report

Under 200 words. Cover: the `v1.5.0` `### Fixed` reconciliation (how the two fixes read as one release); exactly which known-issue notes were removed (CHANGELOG v1.5.0 block + v1.4.1 §Deferred); which book chapters/sections had caveats deleted; mdbook verdict; any drift filed against staff/validator/PLAN.
