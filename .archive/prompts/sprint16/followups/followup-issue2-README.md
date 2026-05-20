# Sprint 16 — follow-up dispatch: close validator Issue 2 (phase-handoff regression)

**Theme:** post-`v1.6.1` get-well — fix the `up` second-phase
duplicate-create of the cluster VPC / transit gateway / client VPC
(`issues/issue_sprint16_validator.md` Issue 2), and close the
validation blind spot that let a phase-1b refactor regress live `up`
while the hermetic parity gate stayed GREEN.

_Not a new sprint — a follow-up dispatch against Sprint 16's own open
issue, found in live verify after the `v1.6.1` cut. The original
`prompts/sprint16/{architect,staff,validator,tech-writer}.md` are the
audit record of the phase-1b dispatch and are **left untouched**; this
follow-up set is additive._

## The bug (read first)

`issues/issue_sprint16_validator.md` **Issue 2** has the full root
cause + evidence. One-paragraph version: `up` applies the same
`roks_cluster`/`testing` terraform across two independent state files
(`state-cluster/` then `state/`) with no existing-resource handoff. The
cluster phase creates `<ws>-vpc`, the transit gateway, and the client
VPC and tracks them in `state-cluster/terraform.tfstate`; the second
(bnk/testing) phase runs the same modules against its own near-empty
`state/terraform.tfstate`, plans to **create** the same-named
resources, and IBM Cloud rejects the duplicates. The reuse plumbing
(`use_existing_cluster_vpc` / `existing_cluster_vpc_id` /
`data.ibm_is_vpc.existing_cluster_vpc`; `testing_create_client_vpc` /
`testing_client_vpc_name`; `cluster-outputs.json` carries `vpc_id`)
exists but is **not wired end to end** — neither
`terraform/modules/roks_cluster/main.tf` nor any Go code passes the
reuse toggles into the second phase.

## Integrator decisions baked in (do not relitigate)

1. **This is now a user-facing bugfix.** Versioning is integrator-owned
   at cut — a `v1.6.2` patch is the expected shape. CHANGELOG goes
   under `### Fixed`, not `### Changed`.
2. **E2E model = gated live-verify, NOT CI.** The e2e driver is
   operator-run via `!` against the real account using the project
   `./terraform.tfvars`; it must not be added as an automatic or
   `workflow_dispatch` CI job, and must never embed or echo the API
   key. Real `terraform apply` = real spend, so the driver is opt-in
   and self-tears-down.
3. **Issue 2 is `high` severity and cannot be closed on unit/hermetic
   tests alone** — per the `live-verify-high-issues` memory, closure is
   gated on a live `!` run. Agents deliver the fix + the driver + a
   hermetic regression test; the live run + final closure is
   integrator/operator-owned.
4. **Behavior parity guardrail still holds** — do not edit any
   pre-existing `_test.go`; the Sprint 14 `--on`/e2e + Sprint 15
   chokepoint guards stay green & unedited. New tests are additive.
5. **Prefer completing the existing-resource handoff** over inventing a
   new mechanism — the toggles, the `data` lookups, and
   `cluster-outputs.json` (`vpc_id`) already exist; the fix is wiring,
   not new design. Staff evaluates the alternative ("don't re-apply
   infra in the second phase at all") and documents why it picked what
   it picked.

## Three-way dispatch (+ tech-writer after)

- **Staff (full)** — `followup-issue2-staff.md`. The terraform + Go
  wiring fix. Owns `terraform/**`, `internal/orchestration/**`,
  `internal/tf/vars.go`, `internal/cli/{tfvars,cluster_phase}.go`,
  `internal/config/**`. Writes `issues/issue_sprint16_staff.md`.
- **Validator (full)** — `followup-issue2-validator.md`. The gated
  live-verify e2e driver + the hermetic regression test that would have
  caught this. Owns `scripts/`, `tools/`, `*_test.go` (additive only),
  `docs/E2E_TEST.md` (e2e section). **Adds no CI job.** Writes the
  Issue 2 closure section in `issues/issue_sprint16_validator.md`.
- **Architect (light)** — `followup-issue2-architect.md`. CHANGELOG
  `### Fixed` block (new `v1.6.2` section) + `docs/PLAN.md` Sprint 16
  follow-up note. Owns `CHANGELOG.md`, `docs/PLAN.md`. Writes
  `issues/issue_sprint16_architect.md`.
- **Tech-writer (light, read-only)** — `followup-issue2-tech-writer.md`.
  Dispatched after the three-way integration; reviews drift / example
  correctness; files only `issues/issue_sprint16_tech-writer.md`.

The three run in parallel; tech-writer after integration. No agent
commits — the integrator commits.

_Drafted from `issues/issue_sprint16_validator.md` Issue 2 +
`prompts/README.md` kickoff playbook._
