You are the **staff** agent for Sprint 23 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in this order)

1. `prompts/sprint23/README.md` — integrator decisions; the
   "investigation first" framing; live-verify gate.
2. `issues/issue_sprint23_staff.md` — full live evidence,
   architecture context, and the two-resource leak with the
   propagation hypothesis already in the issue body.
3. `internal/orchestration/second_phase_reuse.go` — the file
   containing `writeAndInitSecondPhase`,
   `writeBnkPhaseOverride`, and `writeBnkPhaseOverrideAt`.
   The header comment claims "ALL cluster-shared creation off"
   — that claim needs to either become true or get qualified.
4. `internal/orchestration/second_phase_reuse_test.go` — the
   existing test that pins the override content. Your fix
   will extend it.
5. `terraform/modules/testing/main.tf:279` —
   `resource "tls_private_key" "jumphost_shared_key"`. Confirm
   NO count gate.
6. `terraform/modules/roks_cluster/modules/cluster/main.tf:232`
   — `resource "ibm_resource_instance" "cos_instance"` with
   `count = var.create_cluster && var.create_cos_instance ? 1 : 0`.
7. `terraform/modules/roks_cluster/main.tf:26-28` — propagation:
   `create_cluster = var.create_roks_cluster` and
   `create_cos_instance = var.create_roks_registry_cos_instance`.
8. `terraform/variables.tf` (or wherever the root vars live) —
   default values for the propagation variables. Critical for
   understanding why the cos_instance ends up in trial state
   despite the override.

## Investigation tasks (read-only; before any edit)

1. **Confirm the tls_private_key.jumphost_shared_key
   count-gate gap.** It has no `count`/`for_each`. The override
   already sets `testing_create_cluster_jumphosts = false` and
   `testing_create_tgw_jumphost = false`. The natural fix is
   to gate the key on `(testing_create_cluster_jumphosts ||
   testing_create_tgw_jumphost) ? 1 : 0` — the key is only
   useful if a jumphost exists. Confirm this is true by
   reading the surrounding HCL (who consumes the key — both
   jumphost types should be the only consumers).
2. **Confirm the cos_instance propagation gap.** The override
   sets `create_roks_cluster = false`. That maps to
   `var.create_cluster = false` inside the inner cluster
   module. `count = var.create_cluster && var.create_cos_instance ? 1 : 0`
   should be 0. But live trial state shows the resource
   present. Three hypotheses to test by reading:
   - (a) `create_roks_registry_cos_instance` default is `true`
     AND `create_roks_cluster` somehow doesn't reach the inner
     module on the second-phase apply.
   - (b) A separate code path in the trial-phase apply creates
     the resource (e.g. a non-cluster module also references
     `ibm_resource_instance.cos_instance` under a path that
     looks like `module.roks_cluster.module.cluster.*` but
     isn't actually gated by `var.create_cluster`).
   - (c) The override isn't reaching the trial-phase apply on
     all paths — e.g. when `cluster-outputs.json` is missing
     or partial, `writeAndInitSecondPhase` returns nil for
     `extra`, and the apply runs WITHOUT the override.
   - Identify which is true. Document in your closure.
3. **Confirm the live evidence in
   `issues/issue_sprint23_staff.md` lines 36-46** matches
   reality by reading the file. If you spot any
   misattribution (e.g. the live evidence claims a path that
   the HCL doesn't actually produce), flag it.

## Fix tasks (after investigation lands)

1. **Add the count gate to `tls_private_key.jumphost_shared_key`**
   at `terraform/modules/testing/main.tf:279`. The exact
   expression depends on what your investigation confirms;
   the obvious starting point is
   `count = (var.testing_create_cluster_jumphosts || var.testing_create_tgw_jumphost) ? 1 : 0`.
   Update every reference to the key in the same file
   (line 39, 40, 188, 193, 194, 557 — search for
   `tls_private_key.jumphost_shared_key`) to use index `[0]`
   on the resource and gate the corresponding `user_data`
   templates / consumers behind the same count expression.
2. **Fix the cos_instance leak.** Based on your
   investigation's verdict on hypotheses (a)/(b)/(c):
   - If (a) — add the missing override flag (e.g.
     `create_roks_registry_cos_instance = false`) to
     `writeBnkPhaseOverrideAt`'s generated tfvars at
     `internal/orchestration/second_phase_reuse.go:174-181`.
   - If (b) — fix the upstream HCL to gate the resource via
     `var.create_cluster` (or whatever the right variable is).
   - If (c) — fix `writeAndInitSecondPhase` to ensure the
     override is layered even when `cluster-outputs.json` is
     missing OR document why missing-cluster-outputs.json is
     a different failure mode the override shouldn't try to
     mask. (This is less likely; (a) is the safer bet.)
3. **Update the `internal/orchestration/second_phase_reuse.go`
   header comment** that claims "ALL cluster-shared creation
   off" — either make it accurate or qualify what specifically
   remains. The claim is at lines 4-14 of that file, with a
   companion in the function-level comment on
   `writeAndInitSecondPhase`.
4. **Add a regression-test assertion** in
   `internal/orchestration/second_phase_reuse_test.go`. The
   existing test pins the override content; extend it to
   assert the new tfvars are present (whatever you added in
   step 2). The test should fail pre-fix and pass post-fix.

## Out of scope

- ANY edit to `internal/cli/`, `internal/config/`, `cmd/`. The
  Sprint 22 fixes settled DetectShape + down-prompt; do not
  relitigate.
- `.github/workflows/`, `CONTRIBUTING.md`, `book/src/`,
  `docs/PRD/` — architect / tech-writer territory.
- Editing `issue_sprint22_*.md` files or
  `issue_sprint24_staff.md`.
- Running `roksbnkctl up` or `down` against real cloud. The
  integrator runs the live verify post-integration.

## Acceptance criteria

1. `tls_private_key.jumphost_shared_key` is gated; all
   downstream references in
   `terraform/modules/testing/main.tf` handle the
   count-flipped form (`[0]` or for_each).
2. `cos_instance` no longer lands in trial state — verified
   hermetically via the regression test (and live-verified by
   the integrator post-integration).
3. `internal/orchestration/second_phase_reuse.go` header
   comment is accurate (no false "ALL" claim).
4. Regression test added to `second_phase_reuse_test.go`
   passes against the new override content.
5. `go test ./...` PASS; `go vet ./...` clean.

## Closure

Write your closure to
`issues/issue_sprint23_staff.md` §"Closure — staff, <date>"
appended at the end of the existing file (the file is filed
as a placeholder; your closure fills in what shipped). Flip
the top-of-file `**Status**:` field from `open` to
`resolved`. Include: the investigation verdict (which
hypothesis was right for the cos_instance), the exact HCL +
override edits + regression-test diff, the `go test` + `go
vet` results, and any future-sprint candidates the
investigation raised.

Reply with a concise summary under 200 words: the
investigation verdict, the fix shape, the test results, and
any candidates flagged for the integrator.
