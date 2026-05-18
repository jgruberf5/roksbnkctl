You are the validator agent for Sprint 10 of the roksbnkctl project. Sprint 10 closes PRD 04's runtime cred flow (the in-pod `ibmcloud login` wrap) and PRD 06's `status` integration, plus hardens the local pre-tag gate against the v1.2.x cascade. Cuts `v1.3.0` at the end. Your scope is the regression sweep, the live trusted-profile end-to-end smoke test against a sandbox IBM Cloud workspace (the v1.2.0 → v1.2.1 → v1.3.0 closure verification), the local-gate extension (integration-test execution, not just compilation), and the cross-link audit on architect's chapter edits.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

Sprint 10's risk profile: the in-pod wrap closure touches the runtime IAM auth path (security-sensitive); the local-gate extension changes the pre-tag developer workflow (could slow down release cuts if the new step is too heavy). Your live verification covers the live IAM-side behaviors unit tests can't, and your gate-extension design call sets the cadence for future tag-cuts.

## Read first

- `docs/prd/04-CREDENTIALS.md` §"Resolved in Sprint 9" — source of truth for the design Sprint 10 closes. Architect refines if a gap surfaces; you live-verify what shipped.
- `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` §"`status` command integration (Sprint 10 scope addition)" — the per-shape status spec your cross-link audit checks.
- `docs/PLAN.md` §"Sprint 10" — gate criteria + test deliverables + risks.
- `scripts/e2e-test.sh` + `scripts/e2e-test-backends.sh` — existing e2e drivers; reference for what `scripts/integration-test.sh` should look like (if you go with that option).
- `Makefile` — current `release` target runs `staticcheck` + `go build -tags integration` per Sprint 9. Sprint 10 adds integration-test execution (your design call).
- `internal/exec/k8s.go` + `internal/cli/ops.go` — staff's edits land here. Your live verification exercises the rendered pod spec + wrap.
- `prompts/sprint9/validator.md` — prior-sprint prompt structure; regression-sweep block reusable.
- `~/.roksbnkctl/canada-roks/` or a sandbox-permitting equivalent — your live trusted-profile end-to-end test workspace.

## Coordinate with parallel agents

A **staff engineer** agent is implementing the in-pod login wrap (conditional on the SA's trusted-profile annotation, `IAM_PROFILE_ID` injected into the pod spec, brief retry for OIDC propagation delay) and `runStatus` per-phase deployment. **Do not touch files under `internal/` or `cmd/`.**

An **architect** agent is removing the v1.2.x partial-closure admonition from chapter 19, un-guarding the smoke test, adding per-shape `status` output samples to chapter 24, polishing five Sprint-9-deferred chapter issues, and writing the CHANGELOG `v1.3.0` entry. **Do not touch `book/src/`, `CHANGELOG.md`, or `docs/`.**

A **tech-writer** agent does read-only review at end of sprint.

**Your scope** is `Makefile` (edit), `scripts/integration-test.sh` (maybe new, your call), `.github/workflows/*.yml` (edit if needed), the regression sweep, the live trusted-profile verification, and the cross-link audit on architect's chapters.

## Tasks (priority order)

### 1. Regression sweep — Sprint 9's seven-step gate

```
go build ./...
go test ./...
go vet ./...
gofmt -d -l .
staticcheck ./...
go build -tags integration ./...
go test -tags integration ./internal/exec/...   # if kind + docker available locally
```

Any red is **blocker** — stop and file an issue against the responsible agent's surface.

### 2. Live trusted-profile end-to-end (sandbox IBM Cloud)

The headline verification of Sprint 10's main deliverable. Required: a sandbox IBM Cloud workspace with `iam-identity` perms on the resolved API key.

Sequence:

```
roksbnkctl init -w <sandbox>
roksbnkctl cluster up     # provisions cluster, registers it
roksbnkctl ops install --trusted-profile=auto
# Should now show "✓ Provisioned IAM trusted profile roksbnkctl-ops-<sandbox> (<id>)"

# The headline test — does the in-pod wrap actually work end-to-end?
roksbnkctl --backend k8s ibmcloud iam oauth-tokens
# Expected: "IAM token:  Bearer eyJ..." (NOT "missing API key")

# Verify the pod env has IAM_PROFILE_ID, not IBMCLOUD_API_KEY:
oc get pod -n roksbnkctl-ops -l app=roksbnkctl-ops -o jsonpath='{.items[0].spec.containers[0].env}'
# Expected: contains IAM_PROFILE_ID, does NOT contain IBMCLOUD_API_KEY

# Verify the Secret carries empty data:
oc get secret roksbnkctl-ibm-creds -n roksbnkctl-ops -o jsonpath='{.data}'
# Expected: empty map or no IBMCLOUD_API_KEY key
```

Capture exact stdout/stderr in the issue file as evidence. If the first `oauth-tokens` invocation hits the OIDC propagation delay (`failed to assume trusted profile`), document the retry behavior — staff's implementation has a 3-attempt retry with 20s backoff; the call should succeed by attempt 2 or 3.

Then test the fallback paths:

- `--trusted-profile=off`: regression check — v1.0.x behavior preserved.
- `--trusted-profile=auto` against a perm-missing key (synthesised by creating a service-ID key with restricted access): should fall back to static-key, and `oauth-tokens` should still work via the v1.0.x `--apikey` path.

### 3. Local-gate hardening (`Makefile`)

PLAN.md §"Sprint 10" code deliverable 3 names two options:

- **Option (a)**: add a `make integration-test` target that brings up kind + runs the integration suite. `make release` adds a `command -v kind` check; if kind isn't reachable, surfaces a strong warning and a confirmation prompt before proceeding. Contributors who don't have kind can still tag; they just get the warning.
- **Option (b)**: full kind-bringup inside `make release`. Heavier but enforces the gate uniformly. Could slow down routine tag-cuts.

Pick the option that fits the project's tag-cut cadence. The v1.0.x → v1.2.x history (5 patch tags in one session) suggests the gate could afford to be heavier — every patch tag this session traced to a CI red the local gate didn't catch. But option (a) is the less-invasive choice that still surfaces the gap loudly.

Recommend (a) unless there's a clear reason to prefer (b); document the choice in `Makefile` comments + a `### Fixed` bullet in the architect's CHANGELOG entry pointing readers at the new gate shape.

Implementation:

- New target `integration-test` runs `scripts/integration-test.sh` (which itself runs `kind create cluster --name roksbnkctl-it`, `docker daemon` reachability check, `go test -tags integration ./internal/exec/... ./internal/remote/...`, then `kind delete cluster --name roksbnkctl-it`).
- `make release`'s `[N/N]` step counts renumbered to include the new gate step (currently [1/7], becomes [1/8] for option (a)).
- The kind-check step in `make release` for option (a): `command -v kind && command -v docker && kind cluster list | grep -q roksbnkctl-it || run-integration-test || warn-and-prompt`.

### 4. Cross-link audit on architect's chapters

After architect finishes (chapters 14 + 19 + 24):

- Chapter 24's per-shape `status` samples match staff's `runStatus` output verbatim. Generate the four samples by running `roksbnkctl status` against the four-shape testdata fixtures (use `ROKSBNKCTL_HOME` env override pointing at fixture-populated workspace dirs) and diff against the chapter samples.
- Chapter 19's removed partial-closure admonition is gone; smoke test un-guarded; the `IAM token:  Bearer ...` sample output matches the actual binary output for a `--trusted-profile=auto` run.
- CHANGELOG `### Deferred` no longer lists the in-pod login-wrap (it's now `### Changed`).

### 5. CHANGELOG `v1.3.0` review

After architect finishes:

- Every bullet references a real binary surface (`go run ./cmd/roksbnkctl ops install --help` + `... status` match what the entry claims).
- The `### Changed` semantics-shift note about `status` output format names the script-compat behavior (ShapeLegacySingle preserves the v1.0.x `Last apply` line).
- No stale Sprint-9 in-pod-wrap deferral language remains.

## Issue tracking

File at `issues/issue_sprint10_validator.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

When filing against another agent's surface, include the proposed-fix patch as a markdown diff.

## Verification before reporting done

- Seven-step regression sweep status documented.
- Live trusted-profile end-to-end against sandbox: oauth-tokens returns a token, pod env has IAM_PROFILE_ID not IBMCLOUD_API_KEY, Secret carries empty data. All three exit conditions verified or deferred-with-reason.
- Local-gate hardening (option a or b) landed; Makefile / scripts updated.
- Cross-link audit on chapters 14 + 19 + 24 complete.
- `mdbook build book/` clean.
- CHANGELOG entry spot-checked.

## Final report

Under 200 words. Include: regression sweep verdict, live trusted-profile verdict (3 confirmed / divergences listed), local-gate option chosen (a / b) + rationale, cross-link audit verdict, CHANGELOG spot-check verdict, issues filed (counts by severity), regression-gate verdict (any blockers for v1.3.0 tag?). Do NOT commit.
