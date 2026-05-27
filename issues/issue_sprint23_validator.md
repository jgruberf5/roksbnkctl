# Sprint 23 — validator issues (phase-separation leak)

**Status**: resolved

> Sprint 23 validator deliverable: an additive hermetic regression
> test pinning the byte-identical Sprint 23 override block in
> `internal/orchestration/second_phase_reuse_test.go`, plus the
> documented `roksbnkctl !` live-verify recipe + expected outcome
> + rollback path the integrator runs before tagging `v1.7.1`.

---

## Issue 1 — additive regression test + live-verify recipe for the phase-separation-leak fix

**Severity**: medium-high (mirrors the staff issue — the resources
themselves are benign but a `roksbnkctl bnk down` against the
leaked managed entries would destroy cluster-shared infrastructure,
exactly the orphan class Sprint 8's phase split was designed to
prevent).

### Scope (additive only)

- One new test function appended to
  `internal/orchestration/second_phase_reuse_test.go`:
  `TestWriteBnkPhaseOverride_Sprint23ByteIdenticalBlock`. No
  pre-existing `_test.go` case is edited (parity discipline carries
  forward from Sprint 16 / Sprint 22).
- This closure file (`issues/issue_sprint23_validator.md`),
  documenting the `!` live-verify recipe + expected outcome +
  rollback path. The validator does NOT execute the live verify —
  the integrator runs it via `!` post-integration.

### What the test pins

The Sprint 23 staff fix adds one new line —
`create_roks_registry_cos_instance = false` — to the forced
`bnk-phase-override.tfvars` content emitted by
`writeBnkPhaseOverrideAt`, slotted between
`create_roks_transit_gateway = false` and
`testing_create_cluster_jumphosts = false`. The validator test
asserts three properties against the pure (dir-only) core via a
stubbed `config.ClusterOutputs`:

1. **Byte-identical ordered block**. The full 9-line forced tfvars
   sequence, in exact order, with single-LF separators, appears as
   a substring of the rendered override file:

   ```
   create_roks_cluster = false
   roks_cluster_id_or_name = "crt-cluster-id"
   use_existing_cluster_vpc = true
   existing_cluster_vpc_id = "r038-ef6305af-vpc"
   create_roks_transit_gateway = false
   create_roks_registry_cos_instance = false
   testing_create_cluster_jumphosts = false
   testing_create_tgw_jumphost = false
   testing_create_client_vpc = false
   ```

   Any re-ordering, any inserted whitespace, any dropped line
   fails the match.

2. **Adjacency of the new Sprint 23 line**. A targeted 3-line
   check (`create_roks_transit_gateway = false` →
   `create_roks_registry_cos_instance = false` →
   `testing_create_cluster_jumphosts = false`) catches a future
   refactor that splits the block while still passing
   per-line Contains checks elsewhere.

3. **Leak-signature absence**. The override must not carry
   `create_roks_registry_cos_instance = true` in any form
   (with or without spaces around the `=`). The header comment
   legitimately mentions the flag by name, so commented-out
   forms are not greppable as a leak.

This complements the staff-side
`TestWriteBnkPhaseOverride_SuppressesRegistryCOSAndJumphostKey`
(which asserts presence of the new gate + the two jumphost-key
drivers via `strings.Contains` per-line) with stronger
byte-identical ordered-block pinning. Together they cover both
"line is present" and "block is in exact shape" regressions.

### Pre-fix counterfactual

I verified pre-fix HEAD via
`git show HEAD:internal/orchestration/second_phase_reuse.go` —
the string `create_roks_registry_cos_instance` does not appear at
all (`NOT_PRESENT_IN_HEAD`). The validator test asserts the
9-line block as a substring; pre-fix the block is absent (one line
short), so the assertion would `t.Fatalf` on the
`!strings.Contains(got, wantBlock)` branch.
(Git stash to perform a runtime revert was blocked by the
harness; the substring-presence argument is logically equivalent
and recorded here for the integrator's audit trail.)

### Hermetic test results (post-integration tree)

```
$ go test ./internal/orchestration/...
ok  	github.com/jgruberf5/roksbnkctl/internal/orchestration	0.176s

$ go vet ./...
(no output — clean)
```

Both gates GREEN. The orchestration package's full test suite
(including the staff-side Sprint 23 test, the round-2 Sprint 16
parity tests, and the new validator test) PASSES under the
integrated tree.

### Live-verify recipe (integrator runs via `!` — DO NOT execute here)

Per the integrator's [[live-verify-high-issues]] memory and the
Sprint 23 README §"Integrator decisions baked in" point 3:
hermetic tests are necessary but not sufficient. The closure
gate is a real-cloud `up` + a jq assertion against the trial
state file.

```bash
# 1. On a fresh workspace (canada-roks is the one the leak was
#    originally observed on; any Split-shape workspace works).
roksbnkctl -w canada-roks up --auto --var-file=./terraform.tfvars

# 2. After up succeeds: assert the trial state file carries ZERO
#    managed resources under cluster-shared module prefixes.
jq '.resources[] | select(.mode == "managed" and (.module | startswith("module.roks_cluster") or startswith("module.testing")))' \
  ~/.roksbnkctl/canada-roks/state/terraform.tfstate
```

**Expected outcome**: zero output. Empty jq result means the
override now suppresses every cluster-shared CREATE in the
second/trial phase (the registry COS via the new explicit
`create_roks_registry_cos_instance = false` flag; the shared
jumphost TLS key via the new
`(testing_create_cluster_jumphosts || testing_create_tgw_jumphost) ? 1 : 0`
count gate in `terraform/modules/testing/main.tf`, driven to 0
by the existing two `testing_create_*_jumphost = false` lines in
the override).

If the jq output is **non-empty**, the fix is incomplete — the
non-empty entries (module address + type + name) identify which
cluster-shared resources are still leaking into trial state.
That is a Sprint 24 follow-up, not a Sprint 23 re-spin: the
hermetic test still pins the contract staff shipped, and the
contract is correct for the two known leaks; any newly-surfaced
third leak class is a separate investigation.

### Rollback path (if live verify surfaces a NEW leak class)

The integrator's [[no-piling-into-active-release]] memory
applies in reverse here: if a NEW leak class surfaces during
`!` live verify, do NOT try to extend Sprint 23's scope mid-cycle.

Mandatory ordering:

1. **DO NOT** run `roksbnkctl bnk down` against the leaky
   workspace. `bnk down` would destroy the leaked managed
   resources alongside the BNK trial layer — exactly the
   cluster-shared-infrastructure damage class this sprint
   exists to prevent. The fact that the leak survived the fix
   means `bnk down` is provably unsafe for that workspace.
2. Run `roksbnkctl down` (full teardown — both phases) to
   destroy the trial layer AND the cluster cleanly. Full
   teardown is safe because it owns both states.
3. File a Sprint 24 staff issue with the new leak class's
   `jq` output verbatim + the module path(s). Hold off on
   `v1.7.1` until Sprint 24 closes. Sprint 22's down-prompt +
   DetectShape fixes are independently shippable but the
   integrator's stated policy is the combined `v1.7.1` ships
   both sprints together; Sprint 24 would gate the tag.
4. If the new leak is in `module.roks_cluster.*`, follow
   staff's investigation chain: read the count gate in the
   inner module, check propagation through
   `terraform/modules/roks_cluster/main.tf`, then either fix
   propagation upstream or add another override flag. If
   it's in `module.testing.*`, add another count gate at the
   resource declaration site driven by an existing
   `testing_create_*` toggle the override already forces
   false.

### Future-sprint candidates (raised here, not for Sprint 23)

1. **Live-verify CI gate parameterisation**. Today the `jq` check
   in the closure is documented prose. Sprint 24+ could add a
   `roksbnkctl doctor --phase-leak-check` subcommand that runs
   the equivalent jq on the active workspace's trial state file
   and exits non-zero on any managed match. That would let the
   tech-writer's drift sweep + integrator's `!` invocation
   converge on a single command rather than ad-hoc jq. Out of
   scope for Sprint 23 (`internal/cli/` is settled).
2. **Test-helper consolidation**. The three Sprint 23 tests
   (round-2 `TurnsAllClusterSharedOff`, staff's
   `SuppressesRegistryCOSAndJumphostKey`, the new validator
   `Sprint23ByteIdenticalBlock`) all stub the same
   `config.ClusterOutputs` with the same canada-roks fixture.
   A tiny private helper (`fixtureCanadaROKSOutputs()`) would
   DRY them up. Deliberately not done in Sprint 23 (parity
   discipline says don't touch pre-existing tests); fair game
   for a Sprint 24 micro-refactor.
3. **`outputs.tf` smoke test**. Staff added `[0]` indexes to
   `module.testing`'s `tls_private_key.jumphost_shared_key`
   references in user_data + outputs. A `terraform validate`
   smoke test in CI (against the embedded `terraform/` tree)
   would catch a missed `[0]` index regression cheaply. Today
   the validation only happens at `up` time. Sprint 24+
   architect candidate.

### Acceptance criteria (per validator brief)

- [x] New regression test in `second_phase_reuse_test.go`
  PASSes against the integrated tree (staff's `second_phase_reuse.go`
  + override edits + the testing/main.tf count gate).
- [x] Test would have FAILed pre-fix
  (`create_roks_registry_cos_instance` absent from HEAD — verified
  via `git show HEAD:…`; runtime revert blocked by harness sandbox,
  documented in §"Pre-fix counterfactual" above).
- [x] `go test ./internal/orchestration/...` PASS (0.176s).
- [x] `go vet ./...` clean (no output).
- [x] Live-verify recipe + expected outcome + rollback path
  documented above for the integrator's `!` invocation.
- [x] No edits to any pre-existing `_test.go` case. No edits to
  `terraform/`, `internal/orchestration/second_phase_reuse.go`,
  `internal/cli/`, `internal/config/`, `cmd/`,
  `.github/workflows/`, `CONTRIBUTING.md`, `book/src/`,
  `docs/PRD/`.

### Related

- `issues/issue_sprint23_staff.md` — the originating live evidence
  + investigation hypothesis + staff fix shape this test pins.
- `prompts/sprint23/README.md` — integrator decisions; the
  live-verify-required framing for closure.
- `prompts/sprint23/validator.md` — this role's briefing.
- Integrator memory [[live-verify-high-issues]] — applies. The
  hermetic test is necessary but not sufficient; the `!` recipe
  above is the closure gate.
- Integrator memory [[no-piling-into-active-release]] — applies
  in the rollback path (don't extend Sprint 23 scope mid-cycle
  if a new leak class surfaces during the live verify).

---

## Closure — validator, 2026-05-27

**New test function**: `TestWriteBnkPhaseOverride_Sprint23ByteIdenticalBlock`
in `internal/orchestration/second_phase_reuse_test.go` (appended;
no pre-existing test edited). Pins the byte-identical 9-line
Sprint 23 forced-override block as a substring of
`writeBnkPhaseOverrideAt`'s rendered output, with a tighter
3-line adjacency check on the new
`create_roks_registry_cos_instance = false` gate's neighbours
(`create_roks_transit_gateway = false` above,
`testing_create_cluster_jumphosts = false` below), and a
leak-signature absence check on `… = true` in any spacing.

**Hermetic results**:

```
$ go test ./internal/orchestration/...
ok  	github.com/jgruberf5/roksbnkctl/internal/orchestration	0.176s

$ go vet ./...
(no output — clean)
```

**Live-verify recipe** (integrator runs via `!`; expected outcome:
empty jq output):

```bash
roksbnkctl -w canada-roks up --auto --var-file=./terraform.tfvars
jq '.resources[] | select(.mode == "managed" and (.module | startswith("module.roks_cluster") or startswith("module.testing")))' \
  ~/.roksbnkctl/canada-roks/state/terraform.tfstate
```

**Rollback path** (if jq output non-empty): full
`roksbnkctl down`, NOT `roksbnkctl bnk down` (the leaked
managed resources are cluster-shared singletons; `bnk down`
would destroy them). File a Sprint 24 staff issue with the
jq output verbatim; hold `v1.7.1` until that closes.

**Future-sprint candidates raised**: (1) a
`roksbnkctl doctor --phase-leak-check` subcommand that runs the
jq equivalent natively; (2) a private `fixtureCanadaROKSOutputs()`
helper to DRY the three Sprint 23 tests; (3) a `terraform validate`
smoke gate in CI to catch missed `[0]` index regressions in
`module.testing` outputs/user_data references.
