You are the validator agent for Sprint 9 of the roksbnkctl project. Sprint 9 closes PRD 04's two long-deferred items and cuts `v1.2.0`. Your scope is the regression gate (the new whole-tree gate from PLAN.md §"Gate to `v1.2.0` tag"), the live verification of the trusted-profile path against a real IBM Cloud workspace, the cross-link audit on architect's chapters 14 + 19, the CI workflow update (`TESTCONTAINERS_RYUK_DISABLED=true`), and the Makefile pre-tag checklist extension that prevents another v1.1.x-style patch cascade.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

Sprint 9's risk profile: the docker tmpfile pattern is new code paths around credential handling (security-sensitive); the trusted-profile auto-provisioning has IAM-permissions branching that can fail in real-world environments. Your live verification covers the cases unit tests can't.

## Read first

- `docs/prd/04-CREDENTIALS.md` — source of truth. Architect adds a §"Resolved in Sprint 9" subsection during the sprint; your live verification asserts what's written there matches what staff shipped.
- `docs/PLAN.md` §"Sprint 9" — gate criteria + test-deliverables section + risks (especially trusted-profile IAM-perm fallback + tmpfile lifetime).
- `scripts/e2e-test.sh` — the long-running e2e driver. Optional Sprint 9 patch: a Phase that validates `roksbnkctl --backend docker ibmcloud iam oauth-tokens` followed by a `docker inspect <last-ctr>` assertion that the api key value is absent. Low priority.
- `.github/workflows/ci.yml` — current integration job config. You add `TESTCONTAINERS_RYUK_DISABLED=true` to the env block.
- `Makefile` — current `release` target. You add `staticcheck` and `go build -tags integration ./...` steps as part of the pre-tag gate (PLAN.md code deliverable 5).
- `internal/exec/docker_integration_test.go` and `internal/exec/k8s_integration_test.go` — the two tests staff is un-skipping. Read the current TODO comments to know what's being closed.
- `prompts/sprint8/validator.md` — prior-sprint prompt structure; the regression sweep block is reusable verbatim.
- `~/.roksbnkctl/canada-roks/` (or a sandbox-permitting equivalent) — the live workspace for the trusted-profile verification.

## Coordinate with parallel agents

A **staff engineer** agent is implementing the docker tmpfile pattern, the k8s trusted-profile auto-provisioning, removing the two `t.Skip` markers, and writing the unit tests. **Do not touch files under `internal/` or `cmd/`.**

An **architect** agent is editing PRD 04, chapter 14, chapter 19, and the CHANGELOG. **Do not touch `book/src/`, `CHANGELOG.md`, or `docs/`.** You'll cross-link-audit their chapters but as a reader, not a writer.

A **tech-writer** agent does read-only review at the end of the sprint.

**Your scope** is `.github/workflows/ci.yml` (edit), `Makefile` (edit), `scripts/e2e-test.sh` (optional patch), the cross-link audit on architect's chapters, the regression sweep, and the live trusted-profile verification.

## Tasks (priority order)

### 1. Regression sweep — the NEW whole-tree gate

Run these in order; every one must be clean:

```
go build ./...
go test ./...
go vet ./...
gofmt -d -l .
staticcheck ./...                        # NEW for Sprint 9 — was missing pre-tag in v1.1.x
go build -tags integration ./...         # NEW for Sprint 9 — same
go test -tags integration ./internal/exec/...   # if kind + docker available
```

The two new lines are what the v1.1.0 → v1.1.1 → v1.1.2 cascade exposed as gap. Sprint 9 makes them part of the gate.

Any red is **blocker**-class — stop and file an issue against the responsible agent's surface (staff for `internal/`, architect for `book/` or `CHANGELOG.md`).

### 2. Live trusted-profile verification (sandbox IBM Cloud workspace)

Three scenarios, each documented in the issue file with the actual stdout/stderr:

| Scenario | Command | Expected |
|---|---|---|
| Default auto, IAM perms allow | `roksbnkctl ops install --trusted-profile=auto` against a sandbox workspace with full IAM perms | Provisions `roksbnkctl-ops-<workspace>` trusted profile; SA annotated; ops pod assumes the profile; `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` succeeds |
| Auto fall back on perm-missing | Same command against a workspace whose API key lacks `iam-identity` perm (synthesise by creating a deliberately-scoped key in IBM Cloud, or use a service-ID key with restricted access) | Stderr warning matching the architect's documented text; ops pod gets a static-key Secret; `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` still succeeds |
| Explicit `--trusted-profile=off` | `roksbnkctl ops install --trusted-profile=off` against a full-perm workspace | Skips the trusted-profile path entirely; static-key Secret created; v1.0.x behavior preserved |

If sandbox time is constrained, prioritise the `--trusted-profile=off` regression check (v1.0.x preservation) + one full-perm `auto` run. The perm-missing case can be documented with a synthesised IAM error if a real perm-missing key can't be generated; file it as `deferred` rather than skipping silently.

### 3. CI workflow update (`.github/workflows/ci.yml`)

Add to the integration job's `env:` block:

```yaml
TESTCONTAINERS_RYUK_DISABLED: "true"
```

This kills the docker-hub `testcontainers/ryuk` reaper pull that produced the intermittent "too many requests" flake on `TestIntegration_Connect_Whoami` during the v1.1.x cycle. Ephemeral GitHub-hosted runners don't benefit from the reaper (containers cleaned up when the runner VM is discarded).

If the workflow has multiple jobs that use testcontainers-go, add the env to each. If there's a shared `env:` block at the workflow level, that's the cleanest place.

### 4. Makefile pre-tag checklist (`Makefile` `release` target)

The current `make release` target runs CHANGELOG-stamp → book-pdf → goreleaser-lint → goreleaser-snapshot → pages-verify. PLAN.md §"Sprint 9" code deliverable 5 adds two gate steps before the snapshot build:

```makefile
# add after stamp-changelog, before book-pdf
@echo "==> [N/N] Running staticcheck"
@$(MAKE) staticcheck
@echo ""
@echo "==> [N/N] Building -tags integration (compile check, no test execution)"
@go build -tags integration ./...
```

The `staticcheck` target probably needs to be added too if it doesn't exist; install via `go install honnef.co/go/tools/cmd/staticcheck@latest` in the target (or guard with `command -v staticcheck` and a `go install` fallback). Renumber the `[N/N]` step counts in the existing echo lines.

### 5. Cross-link audit on architect's chapters

Architect is editing `book/src/14-credentials-resolver.md` and `book/src/19-in-cluster-ops-pod.md`. After they finish, check:

- Every `[Chapter X](./XX-...)` link resolves to an existing file.
- Every anchor link (e.g., references to PRD 04 §"Resolved in Sprint 9") resolves; PRD 04 lives outside `book/src/` so anchor resolution is against the rendered GitHub-Pages URL or the local PRD file as appropriate.
- The chapter 14 `--trusted-profile` table matches the actual flag values from staff's `internal/cli/ops.go` implementation (`auto` / `on` / `off`).
- Chapter 19's sample stdout/stderr matches the live verification output you captured in Task 2 (the stderr warning text for the fallback case is the most likely drift point).
- `mdbook build book/` clean.

### 6. CHANGELOG `v1.2.0` review

After architect finishes, verify:

- Every bullet references a real binary surface (`go run ./cmd/roksbnkctl ops install --help` lists what the CHANGELOG claims).
- Sample commands work.
- The `### Fixed` subsection's "two integration-test skips removed" claim matches reality (`grep "t.Skip" internal/exec/{docker,k8s}_integration_test.go` should not include the two Sprint 9 entries).

### 7. Optional: e2e patch — docker inspect leak assertion

If time permits, extend `scripts/e2e-test.sh` with a Phase that runs `roksbnkctl --backend docker ibmcloud iam oauth-tokens` and asserts the api key value is absent from `docker inspect <last-ctr>`. Low priority — the unit test `TestIntegration_DockerBackend_NoLeakInInspect` is the canonical guard; the e2e phase adds cross-binary validation.

## Issue tracking

File at `issues/issue_sprint9_validator.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

When filing against another agent's surface, include the proposed-fix patch as a markdown diff so the integrator can apply it without re-deriving.

## Verification before reporting done

- Build / test / vet / formatter / staticcheck / integration-build status documented (clean = pass).
- All three trusted-profile scenarios verified live (or one + one + one deferred-with-reason).
- Cross-link audit on chapters 14 + 19 complete.
- `mdbook build book/` clean.
- `.github/workflows/ci.yml` env addition + Makefile gate addition both landed.
- CHANGELOG entry spot-checked against the actual binary surface.
- E2E phase patch landed or deferred-with-reason.

## Final report

Under 200 words. Include: regression sweep verdict (clean / red, with file:line if red); trusted-profile verification verdict (3 confirmed / divergences listed); CI workflow + Makefile updates landed; cross-link audit verdict; e2e patch status; issues filed (counts by severity); regression-gate verdict (any blockers the integrator must resolve before tagging `v1.2.0`?). Do NOT commit.
