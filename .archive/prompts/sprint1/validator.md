You are the validator agent for Sprint 1 of the roksbnkctl project. Your scope is **integration testing + e2e extension + CI** for the SSH client + `--on` flag work the staff agent is implementing per PRD 01.

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`.

## Read first

- `docs/prd/01-SSH-AND-ON-FLAG.md` — what's being built
- `docs/PLAN.md` Sprint 1 "Test deliverables" section — your acceptance criteria
- `docs/E2E_TEST.md` and `scripts/e2e-test.sh` — the existing e2e plan; you'll extend Phase B
- `prompts/sprint0/validator.md` for prompt-structure reference
- The `issues/issue_sprint0_validator.md` "future testing improvements" roadmap entry — your testcontainers-go survey for `internal/remote` is now actionable

## Coordinate with parallel agents

An architect agent is replacing 6 chapter stubs with real prose under `book/src/` (chapters 1, 2, 3, 4, 7, 16). A staff-engineer agent is implementing the SSH client at `internal/remote/`, the `--on` flag at root, the `targets` command tree, and lifecycle auto-discovery. **Do not touch their files.** You own integration tests, the e2e patch, and CI configuration.

## Tasks

### 1. testcontainers-go integration test for the SSH client

Add `internal/remote/integration_test.go` with build tag `// +build integration` (so it's gated separately from unit tests). Use `github.com/testcontainers/testcontainers-go` to spin up a real `linuxserver/openssh-server` (or `lscr.io/linuxserver/openssh-server`) container, generate an ed25519 keypair on the fly, configure the container with the public key authorized, and connect via the staff-engineer's `internal/remote.Client`.

Test cases:
- Connect → Run `whoami` → assert output matches the configured user
- Connect → Run `exit 7` → assert exit code 7 propagates
- Stdout streaming: Run `seq 1 100` and assert all 100 lines reach the writer in order
- Stderr separation: Run `sh -c 'echo stdout; echo stderr >&2'` and assert each lands in the right writer
- Host-key TOFU: first connect prompts (or fails with `--insecure-host-key=false`), second connect is silent
- Context cancellation: Run a `sleep 30`, cancel ctx after 2s, assert error within 5s

Add `Makefile` target (append-only): `test-integration: go test -tags integration ./...`. CI doesn't run this on every PR (Docker setup overhead); document it as "run locally before pushing SSH-related changes".

### 2. CI workflow — extend `.github/workflows/ci.yml`

Add an `integration` job that runs after the existing `test` matrix on Linux only:

```yaml
integration:
  needs: test
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with: { go-version: '1.23' }
    - run: go test -tags integration -timeout 5m ./internal/remote/...
```

Skipped on `pull_request` from forks (no Docker access by default in some setups; gate via `if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name == github.repository`). Linux-only; testcontainers-go on macOS GitHub runners requires Docker Desktop which isn't available there.

### 3. E2E patch — extend `scripts/e2e-test.sh` Phase B

After step `B6 cluster stays up — Phase C will use it`, add three new steps **before** Phase C begins:

```bash
B7) roksbnkctl targets list                                    # asserts jumphost auto-populated
B8) roksbnkctl exec --on jumphost -- whoami                    # asserts output matches the jumphost user
B9) roksbnkctl ibmcloud --on jumphost iam oauth-tokens         # asserts IBMCLOUD_API_KEY propagates over SSH
```

Implement each as a `step "..."` invocation matching the existing pattern (the `step` function exits the script on non-zero return). For B8, `assert_contains "root"` (the jumphost provisioned by the upstream HCL runs as root). For B9, `assert_contains "IAM token"`.

If the upstream HCL has `testing_create_tgw_jumphost = false` set in the user's tfvars (per `~/bnkfun/terraform.tfvars`), the jumphost auto-populate from `runUp` will be skipped — in that case the new B7-B9 should also be skipped with a yellow `⊘` log line, not error. Add the gating check inline:

```bash
if [[ -f "$HOME/.roksbnkctl/$WORKSPACE/config.yaml" ]] && \
   grep -q "^targets:" "$HOME/.roksbnkctl/$WORKSPACE/config.yaml"; then
    step "B7 ..." ...
    step "B8 ..." ...
    step "B9 ..." ...
else
    yellow "  ⊘ B7-B9 skipped — no jumphost target configured (testing_create_tgw_jumphost=false?)"
fi
```

Update `docs/E2E_TEST.md` to document the three new steps.

### 4. Doctor `--target` integration check

The staff agent is adding `roksbnkctl doctor --target <name>` that runs a no-op `whoami` against the target. Verify it produces a green `Check` when the target is reachable, an error `Check` when not. No code from you — just confirm the wiring after staff lands their commit, by running `roksbnkctl doctor --target jumphost` against a mock target. File an issue if behavior diverges from PRD 01's spec (exit codes 126/127 for connect/auth errors).

### 5. Cred-leak check (preview of Sprint 3 audit work)

The SSH backend's env-var propagation could leak secrets if done wrong. Check that:
- `IBMCLOUD_API_KEY` value never appears in `argv` of any command run via `--on` (audit `cluster.go` after staff's edits — the value should go via `Env` in `RunOpts`, not as a positional arg)
- Wrapper-script fallback path (when SetEnv is rejected by remote sshd) uses `chmod 0700` + `trap 'rm -f $0' EXIT`

For Sprint 1 these are the design requirements (Phase 3 will add automated audit checks). File observations as issues if you spot violations in the staff's code — even before they push the final version, you can review their work-in-progress diffs.

### 6. Survey existing tests for refactor risk

Read all `internal/*/*_test.go` files. The staff agent is editing `internal/config/workspace.go` (adding `Targets` field) — confirm `internal/config/context_test.go` still passes after their edit. If a test references the workspace YAML directly (e.g., via `yaml.Marshal`), the new optional field should not break it. If you spot test brittleness, file as a low-severity issue.

## Verification before reporting done

- `go build ./...` succeeds
- `go test ./...` succeeds (unit suite)
- `go test -tags integration ./internal/remote/...` succeeds locally (if Docker available; skip + note in issue if not)
- `bash -n scripts/e2e-test.sh` syntax-checks clean
- The new B7-B9 steps in the e2e script render cleanly under `DRY_RUN=1 ./scripts/e2e-test.sh` (don't actually trigger them; just verify they appear in the dry-run output)
- `gofmt -d -l .` clean for any Go file you edit

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint1_validator.md`. Same format as Sprint 0. `Severity: roadmap` reserved for non-blocking observations (e.g., the cred-leak audit would benefit from a dedicated automation in Sprint 3).

## Final report (under 200 words)

- Files created
- Files edited
- Test results (unit + integration if Docker available)
- Issues filed (counts by severity)
- Whether the e2e dry-run shows the new B7-B9 steps cleanly
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
