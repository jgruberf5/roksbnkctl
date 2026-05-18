# Sprint 13 — tech-writer issues

> **Sprint 13 frame.** Feature cycle, `v1.5.0`. Read-only review pass,
> dispatched **after** staff / architect / validator return. Scope:
> drift sweep across `issues/issue_sprint13_staff.md` ↔ code ↔ PRD
> 08/09 ↔ CHANGELOG `v1.5.0` ↔ chapters 15/16; dogfooding the
> now-working `--on jumphost kubectl` flow + the `roksbnkctl terraform
> output` one-liner + the auto-registered `jumphost-<zone>` targets;
> PRD 08/09 review; chapter 15/16 lockstep check; validator hand-off
> closures; GREEN/CONDITIONAL/RED launch-readiness verdict for
> `v1.5.0`.
>
> Only write surface is this file. See `prompts/sprint13/tech-writer.md`
> for the task breakdown.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1: Drift sweep — all surfaces agree

**Severity**: low (audit record — no defect found)
**Status**: resolved

Per-claim cross-surface verification (issue ledgers ↔ as-landed code ↔
PRD/CHANGELOG ↔ book). All rows **agree**; no divergence requiring a
proposed-fix diff.

| Claim | Issue ledger | Code (read-only) | PRD / CHANGELOG | Book |
|---|---|---|---|---|
| `--on <target>` no longer leaks local `KUBECONFIG`; only machine-portable `IBMCLOUD_*` cross the boundary | staff Issue 1 §Closure (LIVE-VERIFIED `KUBECONFIG=[]` 2026-05-18 14:54) | `workspaceEnvCore()` + `remoteSafeEnv` backstop; every `dispatchRemote` `on!=""` site sources core (`cluster.go:115/339/559`, `remote.go:55`) | CHANGELOG `v1.5.0 §Fixed` | ch16 §"Environment passthrough" (l.246) — KUBECONFIG **not** forwarded; pre-v1.5.0 history noted | **agree** |
| Read-only `roksbnkctl terraform` by allowlist; mutations rejected before exec; `--on` rejected (state workstation-local); never-applied → "run `roksbnkctl up` first", no side effect | staff Issue 2 §"Hard requirements" + §Closure | `terraform.go` `terraformReadOnlyTop` + `terraformReadOnlyStateSub` sub-verb guard + mutation-flag scrub + `terraformFmtIsReadOnly`; `tf.OpenReadOnly` | PRD 08 + CHANGELOG `v1.5.0 §Added` | ch15/16 pre-v1.5.0 fallback one-liners | **agree** |
| Per-AZ jumphosts auto-register as `jumphost-<zone>` from `testing_cluster_jumphost_ips`; option (a) upsert-only; orphans linger | staff Issue 3 §Closure; architect Issue 4 (output-name) | `tryAutoClusterJumphosts` (`lifecycle.go:595`) reads `testing_cluster_jumphost_ips` (legacy `…_public_ips` fallback), upserts `jumphost-<zone>`, no prune; wired into all post-`up` sites (`lifecycle.go:259/272/328/852`) | PRD 09 (incl. §"Output-name deviation" box) + CHANGELOG `v1.5.0 §Added` + caveat | ch15 §"Per-AZ cluster jumphosts" + orphan caveat; ch16 §"Per-AZ cluster jumphosts" | **agree** |
| `v1.4.1` known-issue re-pointed `v1.4.2 → v1.5.0` (not deleted) | — | — | CHANGELOG.md:44 — note retained, "fixed in v1.5.0", links to `#unreleased-v150` | — | **agree** |
| Option (b) reconcile = tracked post-v1.5.0 follow-up | staff Issue 3 / architect Issue 2 area | code is pure upsert loop, no reconcile/`auto:` marker | PRD 09 §"Open questions" + CHANGELOG `§Deferred` l.24 | ch15 caveat ("post-v1.5.0 follow-up") | **agree** |

Output name landed = `testing_cluster_jumphost_ips` (the real top-level
name per architect Issue 4); chapters/PRD/CHANGELOG all use it — the
superseded `…_public_ips` design-surface name appears only as a
defensive code fallback, never in user-facing prose. No drift.

---

## Issue 2: Dogfooding loop — no stuck-points

**Severity**: low (walkthrough record)
**Status**: resolved

- **`up` → `roksbnkctl --on jumphost kubectl get pods`.** No book
  chapter (07, 09, 15, 16) carries a stale `unset KUBECONFIG`
  workaround or implies the old broken behaviour. The only
  `unset KUBECONFIG` text in the tree is the CHANGELOG `v1.4.1 §Deferred`
  known-issue note, correctly scoped as a v1.4.1-only workaround and
  explicitly marked "fixed in v1.5.0". ch16 §"Environment passthrough"
  states the post-fix behaviour and version-gates the pre-v1.5.0
  history. **The residual live `localhost:8080` the user still sees is
  architect Issue 2 (cloud-init never wrote the jumphost kubeconfig) —
  a separate, independent root cause, OUT of v1.5.0 scope, tracked as
  the Sprint 14 get-well headline. The docs do not misrepresent this:
  the env-leak fix is described accurately and the cloud-init failure
  is not conflated into the v1.5.0 fix.** Not a docs defect.
- **`roksbnkctl terraform output testing_cluster_jumphost_ips` →
  `--on jumphost-<zone> kubectl get pods`.** End-to-end
  copy-pasteable from ch15/16 using only documented outputs/flags.
  `terraformCmd.Short`/`Long` (`terraform.go:30-57`) makes the
  read-only contract explicit ("READ-ONLY", permitted-subcommand list,
  "Mutations go through `roksbnkctl up`/…"). No stuck-point.
- **`testing_create_cluster_jumphosts=true` → `up` → `targets list`.**
  ch15/16 set the right expectation: auto-registered `jumphost` +
  `jumphost-<zone>` per AZ, summary line shown, orphan caveat
  documented where auto-registration is described. No stuck-point.

---

## Issue 3: PRD 08/09 review — canonical, consistent

**Severity**: low (review record)
**Status**: resolved

- **PRD 08** reads as a canonical design doc in the `docs/prd/` house
  shape (Why / Goal / Design / Resolved decisions / Open questions /
  Out of scope / Cross-references). The load-bearing invariant
  ("`roksbnkctl` owns terraform's cwd + `TF_DATA_DIR`; the CLI layer
  must not re-derive them") is stated explicitly in §"Phase-correct
  cwd + env" and locked decision 3, and matches as-landed code
  (`RunReadOnly` uses `cmd.Dir = w.sourceDir`; `OpenReadOnly` never
  calls `Init()`). Allowlist / sub-verb guard / mutation-flag scrub /
  never-applied behaviour all match `terraform.go`.
- **PRD 09** is consistent with the landed `tryAutoClusterJumphosts`.
  The option-(a) decided / option-(b) deferred split is recorded
  unambiguously in §"Stale-target handling — option (a) (decided)",
  locked decision 7, §"Open questions" 1, and §"Out of scope". The
  output-name deviation (`…_public_ips` → `testing_cluster_jumphost_ips`)
  is captured in the boxed §"Design" note and locked decision 1,
  matching the code. No mismatch to file.

---

## Issue 4: Chapter 15/16 lockstep — confirmed (extends validator Issue 5)

**Severity**: low (lockstep confirmation)
**Status**: resolved

Validator Issue 5 already audited doc/code lockstep and returned PASS;
this confirms and extends it. Independently verified against the
as-landed binary:

- ch15 §"Per-AZ cluster jumphosts (`jumphost-<zone>`)" describes
  auto-registration, output name `testing_cluster_jumphost_ips`,
  `jumphost-<zone>` naming, shared-key reuse, best-effort/idempotent,
  empty→no-op — all present in `tryAutoClusterJumphosts`.
- No stale "not auto-registered" claim in ch16: §"What `--on` doesn't
  do (yet)" (l.253) states the per-AZ jumphosts **are**
  auto-registered since v1.5.0; §"Per-AZ cluster jumphosts" headline is
  the auto-registered targets, the manual `targets add` path is a
  clearly version-gated **pre-v1.5.0 fallback** aside (ch15 l.323-339,
  ch16 l.218), not the headline.
- The raw-`terraform` (`cd …/state && TF_DATA_DIR=… terraform`) form is
  correctly an "even older release" sub-aside, not the headline.
- Orphan caveat (option (a) upsert-only) documented in both chapters
  where auto-registration is described, cross-linked to Host-key TOFU.

No behaviour described that is absent from the binary. Lockstep: PASS.

---

## Issue 5: Validator hand-off closures

**Severity**: low (process)
**Status**: resolved — no open validator items handed to tech-writer

All eight validator issues are `resolved`/`accepted`; none was left
`open` for tech-writer. Validator Issue 5 (doc/code lockstep) is
confirmed/extended by tech-writer Issue 4 above. Validator Issue 6
(architect Issue 2, cloud-init kubeconfig failure) is a HIGH but
explicitly OUT-of-v1.5.0-scope, NOT-a-regression known-issue tracked as
the Sprint 14 get-well headline (integrator decision = option C); it
ships as a documented known-issue per the v1.4.1 §Deferred pattern and
does **not** make the launch verdict RED. Nothing to close.

---

## Final verdict — launch readiness for `v1.5.0`: **GREEN**

All five drift-sweep rows agree across issue ledgers ↔ code ↔ PRD 08/09
↔ CHANGELOG ↔ chapters 15/16. The dogfooding loop hits **no
stuck-points**: the now-working `--on jumphost kubectl` env-fix is
documented honestly (post-fix behaviour stated, pre-v1.5.0 history
version-gated, no stale `unset KUBECONFIG` workaround anywhere in the
book), the `roksbnkctl terraform output` one-liner and per-AZ
`jumphost-<zone>` flow are copy-pasteable end-to-end from ch15/16, and
the read-only contract is explicit in the command help. PRD 08/09 read
as canonical design docs in the house shape and match the as-landed
code (cwd/`TF_DATA_DIR` invariant stated in PRD 08; option-(a)-decided
vs option-(b)-deferred split unambiguous in PRD 09). Chapters 15/16 are
in lockstep — no behaviour described that is absent from the binary, no
stale "not auto-registered" claim, manual path correctly a pre-v1.5.0
aside. Validator's gates are all green; no open hand-off.

**No pre-tag conditions.** The launch is unconditionally GREEN for the
three in-scope deliverables (KUBECONFIG-leak fix + read-only
`terraform`/PRD 08 + per-AZ auto-registration/PRD 09).

Architect Issue 2 (cloud-init never provisions the jumphost
kubeconfig → residual live `localhost:8080`) is a **separate,
independent root cause**, OUT of v1.5.0 scope, NOT a v1.5.0 regression,
and is honestly carried as a documented known-issue (the v1.4.1
§Deferred pattern) tracked as the Sprint 14 get-well headline
(integrator decision = option C). It does **not** block the v1.5.0 tag.

The tag-cut itself (`make release` pre-tag gate, `v1.5.0` tag,
goreleaser, `make release-publish`) remains integrator-owned.
