# Sprint 14

**Theme:** get-well — jumphost kubeconfig provisioning fix (option C: cloud-init hardening + roksbnkctl `--on` self-heal) + the e2e/`--on` test that closes the validation blind spot — **folds into the held `v1.5.0`** (no separate tag)

_Get-well cycle. The design surface is the carried `issues/issue_sprint13_architect.md` Issue 2 (root cause + the option-A/B/C analysis, integrator decision = **C**) plus `issues/issue_sprint13_staff.md` Issue 1 §"Related". No new PRD — this is a defect fix, like the Sprint 12 patch shape. Run at a get-well tier: full staff + validator, lighter architect/tech-writer (no PRD, the only book surface is caveat **removal**)._

Integrator decisions baked in (see `docs/PLAN.md` §"Sprint 14"):

1. **Hold-and-merge.** `v1.5.0` was **not** cut at Sprint 13 close. The `## Unreleased (v1.5.0)` CHANGELOG block stays open; this sprint lands the kubeconfig fix **into the same `v1.5.0`** so the release that finally ships makes `roksbnkctl up` → `roksbnkctl --on jumphost kubectl|oc` work end-to-end. There is no `v1.5.1`/`v1.6.0` here.
2. **Option C (both layers).** Part A: harden the cloud-init kubeconfig provisioning in `terraform/modules/testing/main.tf`. Part B: roksbnkctl-side `--on` self-heal in `internal/cli` so an already-broken/already-running jumphost is repaired with no `terraform` recreate.
3. **Pull the blind-spot test forward.** The Sprint 15 consolidation plan's e2e + `--on` integration test is pulled into this sprint (staff deliverable 3) so the kubeconfig fix is **gate-verifiable**, not only live-verified-by-the-user. The rest of the consolidation (chokepoint refactor, `cli` decomposition, process tiering) stays Sprint 15 — do **not** start it here.

Why: Sprint 13's KUBECONFIG env-leak fix is correct and live-verified (`KUBECONFIG=[]` on the wire 2026-05-18 14:54), but the user still hits `localhost:8080` because the jumphost has **no kubeconfig at all** — cloud-init's `ibmcloud login` + `ibmcloud ks cluster config --cluster … --admin` are `|| true`-guarded and any boot failure is swallowed (`/home/ubuntu/.kube/config: No such file or directory`). The two causes are indistinguishable to a user, so v1.5.0 cannot ship until both are fixed.

Four-agent dispatch (get-well tier):

- **Staff** — part A (`terraform/modules/testing/main.tf` cloud-init: bounded retry/readiness + loud failure marker replacing silent `|| true`), part B (`internal/cli` `--on` kubeconfig self-heal + optional post-`up` push to seeded jumphost targets), and deliverable 3 (the e2e + `-tags integration` `--on` test that makes Issue-1-class + missing-remote-kubeconfig defects fail a test, not a human). Closes `issues/issue_sprint14_staff.md` Issue 1.
- **Architect** — CHANGELOG: fold the kubeconfig fix into the open `## Unreleased (v1.5.0)` `### Fixed`, and **remove** (not re-point) the `v1.4.1 §Deferred` + `v1.5.0` carried known-issue notes once the flow works; book ch15/16/09: **delete** the "may still fail / pre-v1.5.0 caveat" prose. `docs/PLAN.md` §"Sprint 14" is integrator-authored — do not rewrite it. No PRD. Light cycle.
- **Validator** — seven-step regression sweep (the new e2e/`--on` test runs inside it); the live `up → --on jumphost kubectl get pods` end-to-end verify is the user's out-of-band action (baseline repro = the 2026-05-18 14:54 diagnostic), now backed by the gate test so it is no longer the only signal.
- **Tech-writer** — read-only; drift sweep + confirm the known-issue caveats are fully **removed** across CHANGELOG + book (the central doc check this cycle), GREEN/RED launch verdict for the now-unblocked `v1.5.0`.

The `v1.5.0` tag remains integrator-owned and is only cut after this sprint's gate (live `--on jumphost kubectl` works end-to-end + e2e/integration green + caveats removed).

## Carry-over considerations

- `issues/issue_sprint13_architect.md` Issue 2 is the design surface; it flips to `resolved` when this sprint's gate passes.
- Sprint 13's three deliverables are complete + GREEN, staged uncommitted under the held `v1.5.0` (same working-tree posture as Sprints 12–13). Do not re-touch them; build on them.
- Part B must distinguish "jumphost has no kubeconfig" (heal) from "cluster genuinely down" (surface the real error, bounded retry, don't spin).
- Sprint 15 (consolidation: single path/env chokepoint, `internal/cli` decomposition, process tiering) is preserved in `docs/PLAN.md` §"Sprint 15" — **out of scope here**.
- Prior-session untracked files (`NEW_PROJECT_STARTING_POINT.md`, `.archive/*`, etc.) remain the integrator's call at tag-cut; out of scope.
