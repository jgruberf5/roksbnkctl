You are the **validator** agent for Sprint 23 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first

1. `prompts/sprint23/README.md` — integrator decisions; the
   live-verify-required framing.
2. `issues/issue_sprint23_staff.md` — live evidence + the
   investigation hypotheses.
3. `internal/orchestration/second_phase_reuse_test.go` — the
   existing test pinning the override content (in particular,
   the `Test_writeBnkPhaseOverrideAt` / similar test that
   asserts the byte-identical expected content). Your
   addition is additive — DO NOT edit any pre-existing
   `_test.go` parity asserts.
4. `internal/orchestration/second_phase_reuse.go` — the
   `writeBnkPhaseOverrideAt` function whose output your
   regression test pins.
5. After staff lands their fix: re-read
   `second_phase_reuse.go` to see what new tfvars staff
   added to the override.

## Tasks

1. **Hermetic regression test** — extend
   `internal/orchestration/second_phase_reuse_test.go` with a
   new test function (additive; no edits to existing tests)
   that asserts the new override content contains the new
   tfvars staff added. Examples of the assertion shape:
   - If staff added `create_roks_registry_cos_instance = false`
     to the override: assert the function's output contains
     the string `create_roks_registry_cos_instance = false`.
   - If staff added another flag: same shape, different
     string.
   The test should call `writeBnkPhaseOverrideAt` against a
   stubbed `config.ClusterOutputs` (the existing test already
   has the stub pattern; mirror it). The assertion is
   byte-identical match on the new lines, NOT a regex —
   parity discipline.
2. **Live-verify recipe documentation** — write the recipe
   into your closure file, do NOT execute it. The recipe is
   the integrator's `!` invocation:
   ```bash
   # On a fresh workspace (any workspace; canada-roks is the
   # one the leak was originally observed on):
   roksbnkctl -w <workspace> up --auto --var-file=./terraform.tfvars
   # After up succeeds:
   jq '.resources[] | select(.mode == "managed" and (.module | startswith("module.roks_cluster") or startswith("module.testing")))' \
     ~/.roksbnkctl/<workspace>/state/terraform.tfstate
   ```
   Expected outcome: zero output (empty jq result). Document
   both the command and the expected outcome. If the jq
   output is non-empty, the fix is incomplete — the
   non-empty entries identify which cluster-shared resources
   are still leaking into trial state.
3. **Document the rollback path** — if the live verify
   surfaces NEW leaks (resources not covered by the count
   gate / override flag staff added), the failure mode is
   the same as pre-fix and `roksbnkctl bnk down` is unsafe.
   Document that the integrator should `roksbnkctl down`
   (full teardown) and re-investigate, NOT `bnk down`.

## Out of scope

- ANY edit to `terraform/`, `internal/orchestration/`
  Go source (other than the new additive test), or
  `internal/cli/`, `internal/config/`, `cmd/`.
- Running `roksbnkctl up` or `down` against real cloud.
- `.github/workflows/`, `CONTRIBUTING.md`, `book/src/`,
  `docs/PRD/` — architect / tech-writer territory.
- Editing `issue_sprint22_*.md` files.

## Acceptance criteria

1. The new regression test in `second_phase_reuse_test.go`
   PASSes against the integrated tree (staff's fix +
   override edits). It would have FAILed pre-fix (you can
   confirm this by temporarily reverting staff's
   `second_phase_reuse.go` change and re-running).
2. `go test ./internal/orchestration/...` PASS;
   `go vet ./...` clean.
3. Live-verify recipe + expected outcome + rollback path are
   documented in your closure file.

## Closure

Write your closure to
`issues/issue_sprint23_validator.md` §"Closure — validator,
<date>". Include: the new test name + the assertion lines it
pins, the `go test` + `go vet` results, the live-verify
recipe verbatim (so the integrator can copy/paste it into a
`!` invocation), and any future-sprint candidates raised.
Flip the top-of-file `**Status**:` field to `resolved`.
Create the issue file — it doesn't exist yet.

Reply with a concise summary under 200 words: the test
shape, the test result, the live-verify recipe, and any
follow-ups for the integrator.
