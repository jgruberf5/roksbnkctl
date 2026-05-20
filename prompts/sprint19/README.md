# Sprint 19

**Theme:** `roksbnkctl init --var-file <path>` — workspace-persistent tfvars at init time, closing the UX gap where bare `-w <ws>` commands still fail after `init` (because `terraform.applied.tfvars` doesn't exist yet, so v1.6.2's applied-tfvars replay can't help). First regular work sprint post-`v1.6.3`.

_Drafted from the integrator's decision 2026-05-20 after live use of v1.6.3 surfaced the residual gap: even with v1.6.2's `.applied-replay.tfvars` mechanism + v1.6.2's actionable error for the no-snapshot case, `roksbnkctl up --var-file ./terraform.tfvars` followed later by bare `roksbnkctl down -w <ws>` still refuses because there's no snapshot yet when the user is **between** `init` and the first successful apply. The proposed fix uses plumbing that's already in the tree: the lifecycle already auto-layers `state/terraform.tfvars.user` via `tfws.HasUserTFVars()` if it exists — today the operator has to drop that file there manually, and nobody does. This sprint automates that drop._

## Integrator decisions baked in (do not relitigate)

1. **`--var-file` on `init` is additive + recommended; the interactive interview stays as-is for back-compat.** Option A from the integrator's pre-sprint scoping. A fresh `roksbnkctl init` with no flags runs the interview exactly as today. A `roksbnkctl init --var-file <path>` reads the file, seeds `config.yaml` from any fields the interview asks about (region, cluster name, version, workers, etc.), copies the var-file verbatim to both phase state dirs as `terraform.tfvars.user`, and skips the interview prompts that the file already answered. Fields the file doesn't carry still prompt (or default).
2. **Two destination copies, not one.** `~/.roksbnkctl/<ws>/state/terraform.tfvars.user` AND `~/.roksbnkctl/<ws>/state-cluster/terraform.tfvars.user` — both phases' lifecycle codepaths read their own `tfws.HasUserTFVars()`. Skipping either leaves a phase broken on bare `-w <ws>`.
3. **The plumbing exists; do NOT re-architect the lifecycle.** `internal/orchestration/lifecycle.go` already calls `HasUserTFVars()` + layers `terraform.tfvars.user` between the auto-rendered tfvars and any `--var-file` flags. Sprint 19's work is `init`-side only: a single `--var-file` flag + file-copy + interview-skip wiring. No changes to lifecycle / orchestration / cos / ibm packages.
4. **Subsequent `--var-file` flags still override.** Terraform var-file precedence: later wins. Order is `state/terraform.tfvars` (auto-rendered) → `state/terraform.tfvars.user` (this sprint's deliverable) → caller's `--var-file <…>` flags → `bnk-phase-override.tfvars` (Sprint 16). User who wants to swap creds for one invocation passes `--var-file ./alt.tfvars` on that one call; the persisted file is unchanged.
5. **Secrets posture.** The `ibmcloud_api_key` lands on disk under `~/.roksbnkctl/<ws>/state/terraform.tfvars.user` — exactly where the operator's existing `./terraform.tfvars` already sits, just under workspace-state instead of repo-root. Permissions `0600`. Documented in the chapter update. No additional secret-handling beyond what's already in place for `terraform.tfvars.user`.

## Per-role scope

| Role | Scope |
|---|---|
| **Staff** Issue 1 | Add `--var-file <path>` flag to `init` (`internal/cli/init.go`); read the file, parse the assignments the interview cares about, seed `config.yaml`, copy the file verbatim to both phase state dirs as `terraform.tfvars.user` (mode `0600`), skip the prompts the file answered. Hermetic test (additive) pinning: file-presence at both paths post-init; `config.yaml` seeded from the tfvars; missing-file error is actionable. |
| **Architect** Issue 1 | Update the book chapter that covers `init` / the first-run workflow (likely `book/src/05-` or `06-` — verify in `book/src/SUMMARY.md`) with the `init --var-file` flow + the secrets-on-disk note. Regenerate `book/src/27-command-reference.md` via `go run ./tools/refgen/cobra-md` so the new flag is in the canonical reference. Add a short §Diagnostics paragraph naming this as the fix for the bare-`-w <ws>` UX gap. |
| **Validator** Issue 1 | Additive hermetic test in `internal/cli/` that drives `init --var-file <tempfile>` and asserts the two `terraform.tfvars.user` copies land + the interview is skipped. Opt-in live driver `scripts/e2e-init-var-file.sh` mirroring the Sprint 18 gated-live-verify shape: real `init --var-file ./terraform.tfvars`, then bare `roksbnkctl plan -w <ws>` (no `--var-file`), assert exit 0 + the applied-tfvars replay log line OR (per Sprint 16 option-(b)) the `terraform.tfvars.user`-was-used log line. Self-teardown on EXIT. |
| **Tech-writer** Issue 1 (light, runs after integration) | Drift sweep — `init --help` shows the new flag; CHANGELOG bullet user-facing; book chapter pin matches the as-shipped behaviour; existing chapters that say "supply --var-file on every command" updated to point at the new flow. GREEN/RED launch verdict. |

## Constraints (binding on every role)

- `internal/orchestration/`, `internal/cos/`, `internal/ibm/` are **out of scope** — Sprint 18 hardened them; don't re-touch.
- No edits to any pre-existing `_test.go` (parity discipline carries forward; new test files only).
- `internal/orchestration` must not import `internal/cli` (one-directional boundary; you shouldn't need to touch orchestration at all).
- Do not commit; integrator commits. Do not run `gh issue create`.

## Live-verify gate

`live-verify-high-issues` applies: integrator runs `roksbnkctl init --var-file ./terraform.tfvars -w <ws>` on a real workspace, then `roksbnkctl plan -w <ws>` (bare), then `roksbnkctl down --auto -w <ws>` (bare) — and asserts both later commands succeed without re-supplying `--var-file`. Closure on the live `!` GREEN.

## Version

Integrator-owned at cut. New flag on `init` is purely additive (no behavior change for users who don't pass `--var-file`), so `v1.6.4` under strict-SemVer is the expected shape. `v1.7.0` only if the integrator judges minor-worthy at gate close.

## Dispatch

Three roles in parallel (architect + staff + validator); tech-writer after the integration commit lands. Same playbook as Sprint 18 (the regular work-sprint shape, NOT the abandoned Sprint 17 backlog-grooming variant).
