# Sprint 14 — tech-writer issues (get-well cycle)

> **Sprint 14 frame.** Get-well cycle, folds into held `v1.5.0`.
> Read-only review after staff/architect/validator. Headline check:
> the known-issue caveats are fully **removed** (not re-pointed) and
> the `v1.5.0` story reads as one coherent release where `--on jumphost
> kubectl|oc` works end-to-end. Drift sweep + caveat-removal
> verification + dogfooding + launch verdict. Only write surface is
> this file. See `prompts/sprint14/tech-writer.md`.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1: Drift sweep — staff issue ↔ code (part A + part B) ↔ CHANGELOG v1.5.0 ↔ book

**Severity**: medium (release-coherence gate)
**Status**: resolved — no provable drift; one accepted minor-incompleteness noted in Issue 4 (not a blocker)

Cross-checked `issues/issue_sprint14_staff.md` Issue 1 §Closure against
the as-landed code, the CHANGELOG `## Unreleased (v1.5.0)` block, and
book ch16/15/09.

| Claim | Staff issue | Code (read) | CHANGELOG v1.5.0 | Book | Consistent? |
|---|---|---|---|---|---|
| Part A: kubeconfig-critical `\|\| true` replaced by bounded retry + loud sentinel | §Closure "Part A" | (validator code-read; terraform unrunnable in-agent — not re-driven here, no contradiction) | line 19 §Fixed "(A) … bounded retry/readiness loop … `/var/log/jumphost-kubeconfig-FAILED` sentinel" | n/a (no book surface for cloud-init) | yes |
| Part B: `--on kubectl\|oc` pre-flight probe → heal-vs-outage discriminator, bounded retry, never silent fallback | §Closure "Part B" | `selfheal.go` `remoteKubeconfigUsable`/`healRemoteKubeconfig`/`maybeSelfHealRemoteKubeconfig`; wired `remote.go:118-149` gated by `kubectlOrOC` | line 19 §Fixed "(B) … self-heals … distinguishing 'no kubeconfig → heal' from 'cluster genuinely down → surface the real error after bounded retry, never silently fall back'" | n/a | yes |
| Part B login-extension: `ibmcloud login` run on target before `ks cluster config --admin` each heal attempt; creds via same `cred.Resolver` `workspaceEnvCore` uses; key positional/injection-safe | §"REOPENED… Part B login-extension landed 2026-05-18" + §Status RESOLVED | `selfheal.go:136-155` `remoteHealCommand` (login `&&` config, positional `$1/$2/…`); `remote.go:133-144` `cred.Resolver{Workspace, Source: …APIKeySource}` | line 19 §Fixed names only `ibmcloud ks cluster config --admin` — login-extension **not explicitly** stated (see Issue 4) | n/a | substantively yes; CHANGELOG underspecified but not false |
| New regression guards `TestE2E_SelfHeal_NotLoggedIn_LoginThenConfig` + `_BadCredentials_SurfacedAsOutage` | §Closure | both present `lifecycle_e2e_test.go:277,312` (+ 7 prior `TestE2E_*` = 9 total) | line 19 "a new `up → --on` e2e + `-tags integration` test … makes both … fail a test rather than a human" | n/a | yes |
| `v1.5.0` reads as ONE release (env leak + kubeconfig), not two half-stories | — | — | line 9 intro: "Sprints 13–14 … **two** independent causes of the same `localhost:8080` symptom … finally works **end-to-end** … held `v1.5.0` open and merged"; §Fixed line 18 (env leak, "one half … see the jumphost-kubeconfig fix below") + line 19 (kubeconfig, "Together with the `KUBECONFIG`-leak fix above … end-to-end") | ch16:246 present-tense correct behaviour + historical parenthetical | yes — coherent single release |
| `issues/issue_sprint13_architect.md` Issue 2 → `resolved` | — | — | — | — | **yes** — Issue 2 `**Status**: RESOLVED 2026-05-18 (Sprint 14, option C; folded into the held v1.5.0)`, live-verified 16:33 recorded |

No drift between the staff narrative, the as-landed `internal/cli`
code, the CHANGELOG, and the book. The per-AZ feature claims
(`testing_cluster_jumphost_ips` output name) carried correctly from
Sprint 13 and are untouched this cycle (architect made no book edits).

---

## Issue 2: Caveat-removal verification (the headline) — fully removed; per-AZ caveat correctly kept

**Severity**: high (the headline doc gate for the now-unblocked `v1.5.0`)
**Status**: resolved — no surviving standing kubeconfig caveat; per-AZ
caveat correctly preserved (no over-deletion)

Independent grep + read of `CHANGELOG.md` and `book/src/` for
`unset KUBECONFIG` / `may still fail` / `pre-v1.5.0` / `known issue` /
`localhost:8080` against the `--on` kubeconfig flow:

**CHANGELOG — clean.**
- `## Unreleased (v1.5.0)` block (intro line 9, §Fixed lines 18–19,
  §Deferred lines 21–27): no `may still fail`, no `unset KUBECONFIG`,
  no live `known issue` hedge. `known issue` appears only as a
  **past-tense historical cross-link** in the line-18 env-leak bullet
  ("disclosed as the `v1.4.1` known issue and is fully resolved in
  `v1.5.0`") — a resolution record, not a standing caveat. Correct.
- `## v1.4.1 §Deferred` (lines 38–43): the
  `**Known issue (shipped broken in v1.4.1; fixed in v1.5.0):**`
  paragraph that the Sprint 13 validator recorded at `CHANGELOG.md:44`
  is **gone**; the block is now the unchanged ops-snapshot + prior
  carry-forward only. The standing `unset KUBECONFIG` workaround is
  removed, not re-pointed a third time. Correct per architect Issue 1
  edit 3.

**Book — clean; per-AZ caveat correctly kept.**
- ch16:246 §"Environment passthrough" — present-tense statement of the
  correct post-fix behaviour with a *historical* parenthetical
  ("Before v1.5.0 the local `KUBECONFIG` path *was* forwarded; … see
  the v1.5.0 changelog"). No `may still fail` / `unset KUBECONFIG` /
  `known issue`. Accurate as-is. KEEP (confirms architect Issue 3 — no
  removable hedge existed; not over-deleted).
- ch16:218 / ch15:323 "**Pre-v1.5.0 fallback**" — about the **per-AZ
  jumphost auto-registration** feature (manual `targets add` on older
  releases), explicitly the content the prompt says to KEEP. **Intact
  and correct** — architect did NOT over-delete it.
- ch15:371 "SCP-and-cleanup for kubeconfig" — an unrelated backend
  comparison-table row, not the `--on` flow.
- ch09 — no `--on`/kubeconfig caveat (only an unrelated kubeconfig
  *download* step at :176–182).

**Verdict: the headline defect is NOT present.** No standing
known-issue / "may still fail" / "unset KUBECONFIG" / "pre-v1.5.0
broken" hedge survives anywhere for the `--on jumphost kubectl|oc`
kubeconfig flow. The unrelated per-AZ auto-registration + orphan
caveat is correctly preserved (architect did not over-delete —
matches architect Issue 3 and validator Issue 3).

---

## Issue 3: Dogfooding — `up → --on jumphost kubectl get pods` post-fix narrative

**Severity**: low (UX-narrative coherence)
**Status**: resolved — the documented happy path now reads as working
end-to-end with no surviving stale workaround

Mental walk of the canonical workflow against CHANGELOG + book +
CLI behaviour:

1. `roksbnkctl up` — post-apply seeds `jumphost` (and per-AZ
   `jumphost-<zone>`); cloud-init (part A) now reliably provisions
   `/home/ubuntu/.kube/config` with bounded retry + a loud
   `/var/log/jumphost-kubeconfig-FAILED` sentinel on exhaustion (no
   silent swallow).
2. `roksbnkctl --on jumphost kubectl get pods` — `workspaceEnvCore()`
   carries machine-portable creds, never the local `KUBECONFIG` path
   (Sprint 13 env-leak fix). The part-B pre-flight probes the target;
   on the healthy path it is a zero-round-trip no-op.
3. Already-broken/already-running jumphost (the live 2026-05-18 case):
   probe finds no usable kubeconfig → `healRemoteKubeconfig` runs
   `ibmcloud login` + `ibmcloud ks cluster config --admin` on the
   target (bounded `selfHealMaxAttempts=4`), re-probes, succeeds, and
   `kubectl` reaches the cluster API. No `terraform` recreate.
4. Genuine outage / bad key: bounded retry exhausts, the **real**
   ibmcloud error is surfaced and dispatch aborts `ExitAuthFailed` —
   never the `localhost:8080` fallback, never an infinite spin, never
   a masked outage. The error text is actionable
   (`selfheal.go:209-213`).

No stuck-point, no surviving stale "unset KUBECONFIG" / "may still
fail" workaround in the docs' narrative. The heal-vs-real-outage
behaviour reads sanely: the book/CHANGELOG describe a working flow,
and the self-heal error path is a clear actionable failure, not a
silent regression to the broken state. **Live-verified** by the
integrator 2026-05-18 16:33 (user-authorized) — the documented happy
path is not just narrated but observed end-to-end.

---

## Issue 4: CHANGELOG §Fixed bullet underspecifies the Part B login-extension (minor incompleteness — accepted, NOT a blocker)

**Severity**: low (doc-completeness; not a falsehood, not a launch gate)
**Status**: accepted — recorded as a tracked nicety; does not block
`v1.5.0` and the prompt's scope explicitly disallows tech-writer
editing the CHANGELOG

The CHANGELOG `## Unreleased (v1.5.0)` §Fixed line-19 bullet describes
Part B as: *"runs `ibmcloud ks cluster config --admin` on the target
before the wrapped command."* The integrator's 2026-05-18 Part B
**login-extension** (the actual landed code: `selfheal.go:136-155`
`remoteHealCommand` runs `ibmcloud login --apikey … && ibmcloud ks
cluster config --cluster <id> --admin` on the target *every* heal
attempt, with creds resolved via the same `cred.Resolver`
`workspaceEnvCore` uses, `remote.go:137-144`) is **not explicitly
named** in that bullet.

Assessment: this is **incompleteness, not drift/falsehood**. The
bullet's stated behaviour (heal the missing kubeconfig on the target;
distinguish heal from outage; no silent fallback) is fully accurate
and is exactly what ships; the `ibmcloud login` step is an
implementation detail of *how* the heal is made robust against an
unauthenticated `ubuntu` ibmcloud profile (the precise live
2026-05-18 16:33 finding). It does not make any CHANGELOG statement
wrong, does not affect the headline ("works end-to-end"), and the
detailed mechanism is fully captured in `issues/issue_sprint14_staff.md`
Issue 1 §Status (the cross-linked design surface) and in code
comments. The user-facing release note remains correct and coherent.

Disposition: **accepted** (low). Tech-writer cannot edit the CHANGELOG
this cycle (scope: write-surface is this ledger only). Optional
nicety for the integrator at tag-cut: one clause in the line-19
bullet — "self-heals — if the target has no usable kubeconfig it
(re)authenticates the target's ibmcloud CLI and runs `ibmcloud ks
cluster config --admin` …" — would make the note fully match the
as-landed code. **Not a `v1.5.0` blocker** (the note is accurate, the
fix is correctly represented as shipped, the story is coherent).

---

## Issue 5: Validator hand-off closures

**Severity**: low (process closure)
**Status**: resolved — no `open` validator item handed to tech-writer
remains; the live-verify hand-off is recorded as user-authorized
out-of-band action backed by the new gate test

- All five validator issues (`issues/issue_sprint14_validator.md`) are
  `resolved`. None is handed to tech-writer for closure.
- The terraform `fmt -check`/`validate` item (validator Issue 2 §"terraform
  fmt") is an **integrator/terraform-capable-shell** hand-off, not a
  tech-writer item — sandbox-blocked, staff-noted as fmt-neutral
  (heredoc-shell-only edit, no new HCL attributes). Out of
  tech-writer's read-only doc scope; noted only for completeness, not a
  doc/launch blocker.
- **Live-verify hand-off — closed.** Validator Issue 2 §"Out-of-band
  live verify" recorded the live `up → --on jumphost kubectl get pods`
  end-to-end confirm as the user's out-of-band action backed by the
  new e2e/`-tags integration` gate (no longer the only signal). That
  action is now **on record as performed**: `issues/issue_sprint14_staff.md`
  Issue 1 §Status and `issues/issue_sprint13_architect.md` Issue 2
  both record the integrator running it directly 2026-05-18 16:33
  (user-authorized: "the existing jumphost and cluster is up; you can
  run the test yourself") — `roksbnkctl exec --on jumphost kubectl get
  pods` self-healed attempt 1, `localhost:8080` gone, exit 0, no
  redeploy. The blind-spot gate test
  (`internal/cli/lifecycle_e2e_test.go`, 9 `TestE2E_*` incl. the two
  new regression guards) makes the defect class fail a test, not a
  human, going forward. Hand-off discharged.

---

## Issue 6: Launch verdict for the now-unblocked `v1.5.0`

**Severity**: n/a (verdict)
**Status**: resolved — **GREEN**

`v1.5.0` is **GREEN** for tag-cut. Rationale:

- **Drift sweep**: clean (Issue 1) — staff narrative, as-landed
  `internal/cli` code, CHANGELOG, and book agree; the per-AZ Sprint 13
  surface is consistent and untouched.
- **Caveat-removal (headline)**: PASS (Issue 2) — no standing
  known-issue / "may still fail" / "unset KUBECONFIG" / "pre-v1.5.0
  broken" caveat survives in CHANGELOG or book for the `--on`
  kubeconfig flow; the `v1.4.1 §Deferred` note is removed (not
  re-pointed); the unrelated per-AZ auto-registration + orphan caveat
  is correctly **kept** (no over-deletion).
- **Coherence**: PASS — `v1.5.0` reads as ONE release: env leak +
  jumphost kubeconfig are reconciled as two causes of one
  `localhost:8080` symptom, held-and-merged so `up → --on jumphost
  kubectl|oc` works end-to-end.
- **Fix represented as shipped**: PASS — both §Fixed bullets present;
  the kubeconfig fix (option C, parts A+B incl. the landed login
  extension in the code) is described as shipped, not aspirational.
- **Dogfooding**: PASS (Issue 3) — documented happy path works with no
  surviving stale workaround; heal-vs-outage narrative is sane.
- **Live gate — now MET (not just unit-level).** The earlier validator
  verdict was GREEN *conditional* on the user's out-of-band live
  confirm. That confirm has since been performed (integrator,
  user-authorized, 2026-05-18 16:33): self-heal on attempt 1, exit 0,
  no `localhost:8080`, no redeploy. Combined with the e2e/`-tags
  integration` gate test, the live gate is **met at both the unit and
  the live end-to-end level** — the get-well cycle's explicit purpose
  (make the fix gate-caught, not only human-caught) is satisfied and
  the previously-blocking out-of-band condition is discharged.

**Conditions on GREEN (informational, none blocking):**

1. *(integrator, tag-cut, optional nicety — NOT a gate)* Consider the
   one-clause CHANGELOG line-19 addition naming the Part B `ibmcloud
   login` step (Issue 4). The note is already accurate without it.
2. *(integrator/terraform-capable shell — sandbox hand-off, NOT a doc
   gate)* `terraform fmt -check terraform/modules/testing/` once, per
   the staff/validator note (fmt-neutral heredoc-shell-only edit).
3. *(pre-existing, explicitly not a blocker)* The lone `-tags
   integration` FAIL `TestIntegration_OpsInstall_ShowsRBACAndPod` is a
   pre-existing test-hygiene gap on the **unmodified** Sprint 4 ops
   package — not a Sprint 14 regression, does not mark `v1.5.0` RED
   (validator Issue 1).

None of (1)–(3) gate the tag. **`v1.5.0`: GREEN — clear to tag.**
