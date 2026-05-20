# Sprint 14 — architect issues (get-well cycle, light)

> **Sprint 14 frame.** Get-well cycle, folds into the held `v1.5.0`.
> Architect scope is **light**: no PRD; CHANGELOG (fold the kubeconfig
> fix into the open `## Unreleased (v1.5.0)` + **remove** the carried
> known-issue notes) and book ch16/15/09 (delete the now-false
> "may still fail / pre-v1.5.0 / unset KUBECONFIG" caveats).
> `docs/PLAN.md` §"Sprint 14"/"Sprint 15" are integrator-authored — do
> not rewrite. See `prompts/sprint14/architect.md`.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1: fold the kubeconfig fix into the held `v1.5.0`; remove the now-false known-issue caveats

**Severity**: medium (release-coherence + doc-accuracy gate for the held `v1.5.0`)
**Status**: **resolved** — staff landed parts A+B + deliverable 3
concurrently during the architect pass (lockstep satisfied; see Issue 2
for the re-verification), so the prepared CHANGELOG edits were applied.
`v1.5.0` now reads as one coherent release; the standing known-issue
caveat is removed. Cross-links `issues/issue_sprint13_architect.md`
Issue 2 (which flips to `resolved` once the validator/live gate
passes). The book required no edit (Issue 3).

### Lockstep timeline (2026-05-18, architect pass)

Initial check on architect return: staff Sprint 14 work **absent**
(empty `git diff terraform/`; `|| true` guards intact at
`main.tf:86-87,94`; no `internal/cli` self-heal; no
`lifecycle_e2e_test.go`; staff Issue 1 `Status: open`). The removal was
**held** and the divergence filed (Issue 2). On re-verification later in
the same pass, `git status` showed staff had landed concurrently:

- **Part A — landed.** `git diff --stat terraform/` →
  `terraform/modules/testing/main.tf | 108 +…` : bounded
  retry/readiness loop around `ibmcloud login` + `ibmcloud ks cluster
  config --admin` (`:112-163`), `/var/log/jumphost-setup.log`
  diagnostic logging, `/var/log/jumphost-kubeconfig-FAILED` sentinel +
  loud failure on exhaustion (`:165-181`), replacing the silent
  `|| true`.
- **Part B — landed.** New `internal/cli/selfheal.go`
  (`remoteKubeconfigUsable` probe, `healRemoteKubeconfig` bounded-retry
  heal-vs-outage discriminator, never silently falls back), wired into
  the `--on` dispatch at `internal/cli/remote.go:106-123` (gated by
  `kubectlOrOC(argv)`).
- **Deliverable 3 — landed.** `internal/cli/lifecycle_e2e_test.go` +
  `internal/cli/lifecycle_e2e_integration_test.go` present.
- Staff Issue 1's `**Status**` line still read `open` at architect
  finish (status-line lag behind the code landing); the prompt's
  lockstep gate is the **working-tree** check ("check `git
  status`/the staff files"), which is unambiguously satisfied. Noted
  for staff/validator to flip staff Issue 1 → resolved.

### Applied edits

1. **CHANGELOG intro (`## Unreleased (v1.5.0)`)** — reframed from
   "Sprint 13 — minor feature cycle" to "Sprints 13–14 — minor feature
   cycle plus its get-well fold-in": headline is `up → --on jumphost
   kubectl|oc` working **end-to-end**; both `localhost:8080` causes
   (env leak + missing jumphost kubeconfig) named as one held-and-merged
   release; hold-and-merge rationale stated; PLAN §Sprint 13/14 + the
   Sprint 13/14 issue cross-links added.
2. **CHANGELOG `### Fixed`** — env-leak bullet's trailing parenthetical
   reworded from the "re-pointed note in `v1.4.1 §Deferred`" phrasing
   to "one half … see the jumphost-kubeconfig fix below … fully
   resolved in `v1.5.0`". Added the second `### Fixed` bullet
   (**Jumphost kubeconfig is now reliably provisioned end-to-end**):
   symptom, root cause (silent `|| true` cloud-init), option-C two-layer
   fix (A cloud-init retry/loud-failure + sentinel, B `--on` self-heal
   with heal-vs-outage discrimination), the e2e/integration test, and
   the explicit "the two fixes are one release" reconciliation. Cross-
   links `issue_sprint13_architect.md` Issue 2 + `issue_sprint14_staff.md`
   Issue 1.
3. **CHANGELOG — removed** the entire `**Known issue (shipped broken in
   v1.4.1; fixed in v1.5.0):**` paragraph from the `## v1.4.1`
   `### Deferred` block (symptom + `unset KUBECONFIG` workaround +
   re-target history). The v1.5.0 §Fixed bullet is now the cross-link
   record. *(There was no separate standing callout inside the
   `## Unreleased (v1.5.0)` block itself — the only such text was the
   env-leak bullet's parenthetical, handled in edit 2 — so the
   "carried known-issue note in the v1.5.0 block" the prompt
   anticipated was discharged by the reword, not a separate deletion.)*
4. **Book — no edits.** See Issue 3: the Sprint 13 cycle added no
   removable kubeconfig hedge to ch16/15/09; prompt task 2 had no
   target. Not editing the book was the correct action (avoids
   over-deleting the per-AZ auto-registration content).

### Verification

- `grep -n "unset KUBECONFIG\|Known issue\|may still fail"
  CHANGELOG.md` → **no lines** (zero standing caveat).
- `grep -rn "may still fail\|unset KUBECONFIG\|known issue" book/src/`
  for the `--on` kubeconfig flow → none; the per-AZ "Pre-v1.5.0
  fallback" + orphan caveats are intact (Sprint 13 content, KEEP).
- `mdbook build book/` HTML backend **exit 0** (`HTML book written to
  …/book/book/html`); pandoc `/opt/render-mermaid.lua` miss is the
  known orthogonal host-tooling issue. No dangling cross-link (no
  in-book anchor added or removed; the removed §Deferred prose linked
  *to* `#unreleased-v150`, not the reverse).
- No `internal/`, `cmd/`, `terraform/`, `prompts/` files modified by
  architect (only `CHANGELOG.md` + this ledger). PLAN §Sprint 14/15
  unmodified (Issue 4).

### Scope

1. **CHANGELOG `## Unreleased (v1.5.0)` `### Fixed`**: add the jumphost
   kubeconfig provisioning fix (symptom: `--on jumphost kubectl|oc` →
   `localhost:8080` even after the env-leak fix; root cause: silent
   `|| true` cloud-init; fix: option C — cloud-init retry/loud-failure
   + roksbnkctl `--on` self-heal). Cross-link
   `issues/issue_sprint13_architect.md` Issue 2. Reconcile so the
   `v1.5.0` block reads as **one coherent release** (env leak +
   kubeconfig together make `--on jumphost kubectl|oc` work
   end-to-end), not two half-stories.
2. **Remove** the carried known-issue note in the `## Unreleased
   (v1.5.0)` block and the `**Known issue (fix targeted for v1.5.0):**`
   note in the `## v1.4.1` `### Deferred` block. The bug is fixed in
   this same release — the standing "may still fail / workaround
   `unset KUBECONFIG`" callout must go (it is no longer true), not be
   re-pointed a third time.
3. **Book ch16/15/09**: delete every "pre-v1.5.0 fallback / may still
   fail / `unset KUBECONFIG` / known-issue" hedge the Sprint 13 cycle
   added around the `--on jumphost kubectl|oc` flow. Post-Sprint-14 the
   documented happy path works — state it plainly. **Keep** the per-AZ
   auto-registration + option-(a) orphan caveat (unrelated, still
   accurate — do not over-delete).

### Acceptance

- `grep -n "unset KUBECONFIG\|Known issue\|may still fail" CHANGELOG.md`
  → no standing caveat; `v1.5.0` reads as one release.
- `grep -rn "pre-v1.5.0\|may still fail\|unset KUBECONFIG\|known issue"
  book/src/` → no live caveat about the `--on` kubeconfig flow; per-AZ
  caveat intact.
- `mdbook build book/` HTML exit 0; no dangling cross-link to a removed
  anchor.
- Couples to `issues/issue_sprint14_staff.md` Issue 1 — the caveats may
  only be removed once staff's fix has landed (lockstep; if staff slips,
  hold the removal and file the divergence).

### Out of scope

- PRD authoring (none this cycle). Sprint 15 consolidation framing.
  Any `v1.5.1`/`v1.6.0` heading.

---

## Issue 2: LOCKSTEP DIVERGENCE — staff Sprint 14 fix (parts A+B + deliverable 3) not in the working tree; caveat removal held

**Severity**: high (release-gate blocker — the held `v1.5.0` cannot
have its known-issue caveats removed until the fix they describe
actually exists)
**Status**: resolved — transient. The divergence was real at architect
return (staff work absent) and the removal was correctly **held**.
Staff then landed parts A+B + deliverable 3 concurrently within the
same architect pass; on working-tree re-verification the lockstep gate
was satisfied and the held edits were applied (see Issue 1
§"Lockstep timeline" + §"Applied edits"). Left as a record of the
hold/unblock sequence. **Residual hand-off:** staff Issue 1's
`**Status**` line still read `open` at architect finish — staff/
validator should flip `issues/issue_sprint14_staff.md` Issue 1 and
`issues/issue_sprint13_architect.md` Issue 2 to `resolved` once the
build/test + live `--on jumphost kubectl` gate passes.

### Description

The prompt and Issue 1's Acceptance bind the CHANGELOG/book caveat
removal in strict lockstep to `issues/issue_sprint14_staff.md` Issue 1:
"the caveats may only be removed once staff's fix has landed (lockstep;
if staff slips, hold the removal and file the divergence)." At
architect return (2026-05-18) staff's Sprint 14 fix is **absent** from
the working tree — see Issue 1 §"Lockstep evidence" for the file-level
proof (empty `git diff terraform/`; `|| true` guards intact at
`main.tf:86-87,94`; no `--on` self-heal in `internal/cli/`; no
`lifecycle_e2e_test.go`; staff Issue 1 `Status: open`).

### Decision

Architect **holds** the CHANGELOG known-issue removal and the env-leak→
two-fix reconciliation. Removing them now would produce a `v1.5.0`
CHANGELOG asserting `--on jumphost kubectl|oc` works end-to-end while
the deterministic `localhost:8080` provisioning failure is still
present in the tree — exactly the "headline fix looks broken" outcome
the hold-and-merge integrator decision exists to prevent. The prepared
edits are staged verbatim in Issue 1 §"Prepared (NOT applied) edits"
and apply mechanically once staff lands.

### Hand-off / unblock condition

- **Staff:** land `issues/issue_sprint14_staff.md` Issue 1 parts A
  (cloud-init hardening, `terraform/modules/testing/main.tf`), B
  (`internal/cli` `--on` self-heal), and deliverable 3
  (`internal/cli/lifecycle_e2e_test.go` + `-tags integration` `--on`
  test). Flip staff Issue 1 → resolved.
- **Architect (follow-up pass, or integrator at tag-cut):** apply
  Issue 1's prepared edits 1+3 to `CHANGELOG.md`; re-run
  `grep -n "unset KUBECONFIG\|Known issue\|may still fail" CHANGELOG.md`
  (expect only the v1.5.0 §Fixed historical cross-link) and
  `mdbook build book/` (HTML exit 0). Flip Issue 1 → resolved and
  `issues/issue_sprint13_architect.md` Issue 2 → resolved.
- **No book change is gated** by this — see Issue 3 (the book had no
  removable caveat to begin with).

### mdbook gate (run now, independent of the hold)

`mdbook build book/` (binary at `~/.cargo/bin/mdbook`): HTML backend
**exit 0** — `HTML book written to .../book/book/html`. The pandoc
backend fails on `cannot open /opt/render-mermaid.lua: No such file or
directory` — the known orthogonal host-tooling issue (pandoc filter not
installed on this host), unrelated to book content. HTML is the gate;
gate **passes**. No book files were modified this cycle.

---

## Issue 3: book audit — the Sprint 13 cycle added NO removable kubeconfig caveat to ch16/15/09 (no-op, recorded to prevent over-deletion)

**Severity**: low (scope-clarification finding — prevents an
over-deletion regression)
**Status**: resolved — audit complete; no book edit warranted this
cycle (and none would be warranted post-staff either).

### Finding

The prompt's task 2 directs deleting "the now-false 'pre-v1.5.0 / may
still fail / unset KUBECONFIG / known-issue' caveats from book
ch16/15/09". A full grep+read audit of all three chapters found **no
such caveat exists**:

- **ch16:246 §"Environment passthrough"** already states the post-fix
  behaviour in plain present tense ("`KUBECONFIG` is **not**
  forwarded … the target's own kubeconfig … is the correct
  behaviour"). Its trailing parenthetical ("Before v1.5.0 the local
  `KUBECONFIG` path *was* forwarded; … see the v1.5.0 changelog") is
  correct historical context for the env-leak fix, not a live
  "may still fail" hedge. It is accurate as written and stays.
- **ch16:218 / ch15:323 "Pre-v1.5.0 fallback"** notes are about the
  **per-AZ jumphost auto-registration** feature (manual `targets add`
  on pre-v1.5.0 releases) — explicitly the content the prompt says to
  **KEEP**, not a kubeconfig-brokenness hedge.
- **ch16:239 / ch15:315 orphaned-target caveat (option (a))** — the
  per-AZ reconcile follow-up; explicitly KEEP.
- **ch09** — no `--on`/kubeconfig caveat at all (only an unrelated
  worker-pool scheduling note at `:212`).

There is no `localhost:8080` / `unset KUBECONFIG` / "jumphost has no
kubeconfig" / "may still fail after the env fix" aside anywhere in
ch16/15/09. Sprint 13 documented the env-leak fix as the complete
story; it never wrote a forward-looking kubeconfig-provisioning hedge.

### Implication

Prompt task 2 has **no target**. No book edit is performed (now or
post-staff). Recorded explicitly so a later pass does not "find
something to delete" and erroneously strip the per-AZ
auto-registration content. The book is already correct for the
post-Sprint-14 world; only the CHANGELOG needs the Issue 1 edits once
staff lands.

---

## Issue 4: PLAN drift check — no provable drift found

**Severity**: n/a (verification record)
**Status**: resolved — checked, no drift to file.

`docs/PLAN.md` §"Sprint 14"/"Sprint 15" are integrator-authored and
were not rewritten. Spot-checked against the as-staged CHANGELOG +
book + the staff/architect Sprint 14 issue framing: PLAN's hold-and-
merge / option-C / blind-spot-test-pulled-forward / Sprint-15-out-of-
scope statements are consistent with the as-landed Sprint 13 surface
and the (held) Sprint 14 plan. No provable inconsistency → nothing
filed against PLAN, sections unmodified.

---

## Sprint 14 ledger closeout — `v1.5.0` shipped 2026-05-18

**Status: CLOSED.** All 4 issues terminal — Issues 1–4 resolved.

`v1.5.0` is cut, released, and published:

- **Tag:** annotated `v1.5.0` on `5113b74` (the `chore: prep v1.5.0 release` commit — matches the `v1.4.1`/`v1.3.0`/`v1.2.1` tag-placement convention).
- **CI gate green pre-tag:** `ci.yml` (vet/fmt/staticcheck/test, ubuntu+macos, goreleaser-check) ✅; `book.yml` (`mdbook build`) ✅ on the book-touching commit `d6c8bf8`; plus the integrator's live `roksbnkctl --on jumphost kubectl` verify 2026-05-18 16:33 (self-healed attempt 1, `localhost:8080` gone, exit 0, no redeploy).
- **GitHub Release:** live, not draft — 8 assets (6 platform archives + `checksums.txt` + `roksbnkctl-book-v1.5.0.pdf`); `release.yml`/goreleaser run completed success.
- **Book:** HTML → `gh-pages` live at <https://jgruberf5.github.io/roksbnkctl/book/> (HTTP 200); PDF attached to the Release.
- **Release notes:** curated `v1.5.0` announcement published (headline = the end-to-end `--on jumphost` fix; Fixed / Added / Install).

All Sprint 13 + Sprint 14 gate criteria met (Sprint 13 §"Gate to `v1.5.0`" + Sprint 14 §"Gate to the (finally tag-ready) `v1.5.0`"). The post-v1.4.0 jumphost thread is closed end-to-end. The only forward items are the explicitly-tracked post-`v1.5.0` follow-ups: per-AZ stale-target reconcile option (b), and the path/env chokepoint + `cli` consolidation (`docs/PLAN.md` §"Sprint 15"). **Ledger closed 2026-05-18.**
