You are the **validator** agent for a Sprint 16 follow-up dispatch on
the `roksbnkctl` project. Repo root: `/mnt/c/project/roksbnkctl`. You
run with no memory of prior conversation.

## Read first (in order)

1. `issues/issue_sprint16_validator.md` — **Issue 2** is the
   regression. You own the closure section of this file. Read its full
   root cause + evidence.
2. `prompts/sprint16/followup-issue2-README.md` — integrator decisions.
   Decisions 2, 3, 4 bind you hard.
3. `scripts/e2e-test.sh` (existing baseline driver — phase
   functions, `TFVARS`/`WORKSPACE`/`DRY_RUN` knobs, assertion
   helpers), `scripts/e2e-test-full.sh`, `docs/E2E_TEST.md` (phase
   plan), `terraform.tfvars` in the repo root **for structure only —
   never echo, log, or commit its contents; it holds a live API key**.

## Why the parity gate missed this

Validator Issue 1's hermetic parity gate is GREEN and *correct* — it
proves the phase-1b move is behavior-identical at the unit level. It is
structurally blind to Issue 2 because no hermetic test exercises a
workspace that has already completed the cluster phase. Your job is to
close that blind spot at **two levels**.

## Deliverable 1 — hermetic regression test (would have caught it)

Add an additive Go test (new `_test.go`, do **not** edit any
pre-existing test — parity guardrail) that asserts the second-phase
tfvars contract: given a workspace with a `cluster-outputs.json`
present (`config.ClusterOutputs` with a `VPCID`), the rendered
second-phase tfvars contains `use_existing_cluster_vpc = true` and
`existing_cluster_vpc_id = "<vpc id>"` and
`testing_create_client_vpc = false`; and given **no** cluster outputs,
it contains the create-path values (parity with today's first-phase
behavior). Put it next to the renderer (`internal/tf/vars.go`) or the
orchestration seam, whichever the staff agent's fix makes testable.
Coordinate only via the code interface — staff owns the fix, you own
the test.

## Deliverable 2 — gated live-verify e2e driver (NOT CI)

Per README decision 2, this is **operator-run via `!`, never a CI
job**. Add `scripts/e2e-phase-handoff.sh` (mirror `e2e-test.sh`'s
style: `set -euo pipefail`, colored `log`/`green`/`red`, `DRY_RUN=1`
support, `LOG_DIR`, exit non-zero on first failed assertion with a
clear message). It must:

- Use the project tfvars: `TFVARS=${TFVARS:-./terraform.tfvars}`,
  `WORKSPACE=${WORKSPACE:-e2e-handoff}`. Never print the file's
  contents or the API key; redact in any echoed command.
- Run the real reproduction: `roksbnkctl up <ws>` end to end (cluster
  phase **then** bnk/testing phase — the exact path Issue 2 fails on).
- Assert the fix: after `up`, the second-phase state
  (`~/.roksbnkctl/<ws>/state/terraform.tfstate`) **reuses** rather than
  re-creates — i.e. it does not contain a freshly managed
  `module.roks_cluster.module.cluster.ibm_is_vpc.cluster_vpc` /
  `ibm_tg_gateway.transit_gateway` / `module.testing.ibm_is_vpc.client_vpc`
  duplicating the cluster phase; `up` exits 0; no "not unique" /
  "already exists" in the run log. Assert the rendered second-phase
  `terraform.tfvars` carries `use_existing_cluster_vpc = true`.
- Self-teardown: `roksbnkctl down <ws>` in a trap so a failed run does
  not strand billable infra. Make teardown best-effort and loud.
- Document at the top: real spend, ~70+ min, requires
  `IBMCLOUD_API_KEY` in env (not from the tfvars echo), opt-in.

Add a short §"Phase-handoff regression (Issue 2)" to `docs/E2E_TEST.md`
describing how/when an operator runs this driver and what GREEN means.
Do **not** add or modify any `.github/workflows/*` — decision 2.

## Constraints

- **No CI job**, no `workflow_dispatch`, no key in any file/log/echo.
- Do not edit pre-existing `_test.go`; new tests additive only. Sprint
  14 `--on`/e2e + Sprint 15 chokepoint guards stay green & unedited.
- Do **not** commit. Do not run the live driver yourself (it needs the
  real account + spend; that is integrator/operator-owned via `!`).
  Deliver the driver + the hermetic test; `DRY_RUN=1` self-check is
  fine and expected.
- If `go test` is sandbox-denied, record the exact denied command in
  your issue file — do not fake results.

## Verify before reporting done

- New hermetic test compiles and passes (`go test ./<pkg>/...`), or
  denied-command record.
- `bash -n scripts/e2e-phase-handoff.sh` clean; `DRY_RUN=1
  ./scripts/e2e-phase-handoff.sh` prints the intended steps without
  executing and without leaking the key.
- Full hermetic `go test -race ./...` still green (the parity gate must
  not regress); pre-existing test files byte-unchanged
  (`git diff --stat -- '*_test.go'` shows only your new file).

## Issue file

Update `issues/issue_sprint16_validator.md` — add an Issue 2
**closure** subsection: what the hermetic test asserts, the live-verify
driver + how the operator runs it, and an explicit
**`Status: open — pending live \`!\` verify`** line (per README
decision 3 / the `live-verify-high-issues` rule, you must NOT mark
Issue 2 `resolved`; only the integrator does that after the live run).

## Final report

≤200 words: the hermetic test (what it asserts), the live-verify driver
(invocation + GREEN criteria), confirmation no CI/key leak, parity gate
still GREEN, and the explicit "Issue 2 stays open pending live `!`
verify" note. State you did not commit and did not run live infra.
