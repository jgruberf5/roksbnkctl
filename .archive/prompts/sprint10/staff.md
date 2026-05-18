You are the staff engineer agent for Sprint 10 of the roksbnkctl project. Sprint 10 closes PRD 04's runtime cred flow (the in-pod `ibmcloud login` wrap that Sprint 9 deferred as staff Issue 2) and PRD 06's `status` integration (the requirement added post-Sprint-9 in commit `4e5f103`). Cuts `v1.3.0` at the end. Your scope is the implementation: the in-pod login wrap conditional on the SA's trusted-profile annotation, IAM_PROFILE_ID injection into the pod spec, and `runStatus` consuming `config.DetectShape` for per-phase deployment lines.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

The Sprint 9 staff agent's `issues/issue_sprint9_staff.md` Issue 2 is the canonical statement of the in-pod wrap work — the deferral reasoning, the failure mode (`missing API key` under `--trusted-profile=auto` success because the Secret carries empty data), and the target shape (`ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"` when the SA has `iam.cloud.ibm.com/trusted-profile`). Read it first; your code should match the target shape and remove the "Sprint 10 carry-over" from the deferred list.

## Read first

- `docs/prd/04-CREDENTIALS.md` §"Resolved in Sprint 9" — your authoritative source for the design. The provisioning side (Sprint 9) annotated the SA and injected the profile creation; Sprint 10 closes the runtime side (the in-pod login wrap that consumes the annotation).
- `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` §"`status` command integration (Sprint 10 scope addition)" — your authoritative source for the per-shape status output design.
- `docs/PLAN.md` §"Sprint 10" — your code deliverables (items 1 + 2 are yours; items 3 + 4 are validator's and architect's).
- `internal/exec/k8s.go` `runOnOpsPod` — current ibmcloud login wrap. The Sprint 9 implementation does `ibmcloud login -a https://cloud.ibm.com -r "${IBMCLOUD_REGION:-us-south}" --apikey "$IBMCLOUD_API_KEY" --quiet` unconditionally. Your edit makes this conditional on the pod's SA annotation.
- `internal/cli/ops.go::resolveTrustedProfileForInstall` — provisions the profile + annotates the SA when `--trusted-profile=auto|on`. Your edit extends the manifest renderer to also inject `IAM_PROFILE_ID` into the pod spec env when the trusted-profile path is taken.
- `internal/cli/inspect.go::runStatus` — current status output. Your edit consumes `config.DetectShape` + per-phase tfstate mtime for the new per-phase lines.
- `internal/exec/k8s_install.yaml` — current pod spec. Your edit adds the conditional env var (`IAM_PROFILE_ID`) — pattern: existing manifest templates use placeholder substitution at apply time.
- `issues/issue_sprint9_staff.md` Issue 2 — read in full.
- `issues/issue_sprint9_tech-writer.md` Issue 1 — describes the user-facing failure mode you're closing.
- `internal/config/testdata/` — Sprint 8's four-shape tfstate fixtures; your `inspect_test.go` table test reuses them.

## Coordinate with parallel agents

An **architect** agent is removing the v1.2.x partial-closure admonition from chapter 19, un-guarding the smoke test, adding per-shape `status` output samples to chapter 24, polishing five Sprint-9-deferred issues in chapters 14 + 19, and writing the CHANGELOG `v1.3.0` entry. **Do not touch `book/src/`, `CHANGELOG.md`, `docs/`, or `README.md`.**

A **validator** agent is running the regression sweep, live-verifying the trusted-profile end-to-end against sandbox IBM Cloud, and adding the integration-test execution to the local pre-tag gate (Makefile + maybe `scripts/integration-test.sh`). **Do not touch `Makefile` or `scripts/`.** They'll file issues against your code if the live trusted-profile path diverges from what the chapter quotes.

A **tech-writer** agent does read-only review at end of sprint.

**Your scope** is `internal/exec/k8s.go` (edit), `internal/exec/k8s_install.yaml` (edit), `internal/cli/ops.go` (edit — manifest renderer passes IAM_PROFILE_ID through), `internal/cli/inspect.go` (edit — status per-phase), and accompanying `_test.go` unit tests.

## Tasks (priority order)

### 1. In-pod `ibmcloud login` wrap — trusted-profile-aware

In `internal/exec/k8s.go::runOnOpsPod`, the existing ibmcloud login wrap is unconditional. Replace it with branching on the pod's SA trusted-profile annotation:

- **If the pod's SA carries `iam.cloud.ibm.com/trusted-profile: <name>`** (read from the Pod's ServiceAccount via the k8s client, OR via the SA name being `roksbnkctl-ops` + the wrap reading `IAM_PROFILE_ID` from the pod env): wrap is `ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}" --quiet > /dev/null 2>&1 && exec ibmcloud "$@"`. The trusted-profile login exchanges the projected SA token for an IAM token automatically; no `--apikey` needed.
- **Otherwise** (static-key path): existing v1.0.x wrap unchanged — `ibmcloud login -a https://cloud.ibm.com -r ... --apikey "$IBMCLOUD_API_KEY" --quiet ...`.

The cleanest plumbing: the manifest renderer in `internal/cli/ops.go` injects either `IBMCLOUD_API_KEY` (static-key path; existing Secret-backed env) OR `IAM_PROFILE_ID` (trusted-profile path) into the pod spec — never both. The wrap script reads whichever is set: `if [ -n "$IAM_PROFILE_ID" ]; then ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID" ...; else ibmcloud login --apikey "$IBMCLOUD_API_KEY" ...; fi`. Keeps the wrap script self-contained without requiring a k8s API call from inside the wrap.

**Brief retry for OIDC propagation delay** — the cluster's OIDC issuer URL takes 30-60s to propagate through IBM IAM after `ops install` returns. The trusted-profile login may fail with `failed to assume trusted profile` during this window. Add a 3-attempt retry with 20s backoff specifically for the trusted-profile login path; surface the final attempt's error if all three fail. The static-key path doesn't need this (no OIDC dependency).

### 2. `IAM_PROFILE_ID` injection (`internal/cli/ops.go` + `internal/exec/k8s_install.yaml`)

The manifest renderer currently emits the pod spec with `envFrom: secretRef: roksbnkctl-ibm-creds` unconditionally. Sprint 10:

- Under `--trusted-profile=auto|on` success: inject `env: [{name: IAM_PROFILE_ID, value: "<profile-id>"}]` into the pod spec; the Secret stays with empty data (Sprint 9 behavior — needed for the `envFrom` to not error on a missing Secret).
- Under fallback / `--trusted-profile=off`: existing v1.0.x shape — Secret carries the api key; pod spec has no `IAM_PROFILE_ID`.

The trusted-profile ID is available from the `CreateForOpsPod` return value in Sprint 9's `internal/ibm/trusted_profile.go`. Thread it through `resolveTrustedProfileForInstall` to the manifest renderer.

### 3. `runStatus` per-phase deployment (`internal/cli/inspect.go`)

Per PRD 06 §"`status` command integration" — consume `config.DetectShape(cctx.WorkspaceName)` after the existing workspace/region/cluster lines. Output per shape:

- **ShapeEmpty**: `Cluster phase: not deployed` + `BNK trial: not deployed`. Drop the v1.0.x `Last apply` line (no state file exists).
- **ShapeClusterOnly**: `Cluster phase: deployed (last apply <timestamp>)` + `BNK trial: not deployed`. Timestamp from `state-cluster/terraform.tfstate` mtime.
- **ShapeSplit**: per-phase lines for both. Mtimes from each state file independently.
- **ShapeLegacySingle**: print a one-line shape callout (`Shape: legacy single-state (cluster + trial in one tfstate)`) PLUS preserve the v1.0.x `Last apply` line verbatim (script-compat). Reuse the existing `os.Stat(statePath).ModTime()` lookup that's already in `runStatus`.

The new lines replace the existing single `Last apply` line for non-Legacy shapes; preserve it verbatim for Legacy. Format with `tabwriter` (already in use in `runStatus`).

### 4. Unit tests

- `internal/cli/inspect_test.go` (new or extend) — table test against `internal/config/testdata/tfstate_{empty,cluster_only,split,legacy_single}.json` fixtures. For each shape, assert the expected lines appear (or don't) in the rendered output. Use `ROKSBNKCTL_HOME=t.TempDir()` + populate `<home>/<workspace>/state*/terraform.tfstate` from the fixtures, then call `runStatus` against a `*cobra.Command` with stdout captured.
- `internal/cli/ops_test.go` — extend the `--trusted-profile` flag-validation tests with a check that the manifest renderer emits `IAM_PROFILE_ID` env under `auto|on` success and NOT under `off` / fallback.
- `internal/exec/k8s_test.go` — extend with a unit test for the new branching wrap script (the bash conditional must select the right login path based on `IAM_PROFILE_ID` presence).

### 5. Smoke verify

- `go build ./...` clean.
- `go test ./...` green on the default-tag set.
- `go vet ./...` clean.
- `gofmt -d -l .` empty.
- `staticcheck ./...` clean.
- `go build -tags integration ./...` clean.
- `go run ./cmd/roksbnkctl status` against a synthetic test workspace renders the expected shape-aware output.
- `go run ./cmd/roksbnkctl ops install --trusted-profile=auto --dry-run` (if a dry-run flag exists, or via a manifest-renderer unit test) shows `IAM_PROFILE_ID` env in the rendered pod spec.

## Issue tracking

File at `issues/issue_sprint10_staff.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

If you find a chapter or PRD inconsistency, file against architect's surface — don't edit prose yourself.

## Verification before reporting done

- All six items in §"Smoke verify" pass.
- The `TestIntegration_K8sBackend_OpsPodExec` (or equivalent) integration test, if it exists, passes against the new wrap — the `ibmcloud iam oauth-tokens` call succeeds with `--trusted-profile=auto` ops pod (live env required).
- No edit under `book/src/`, `CHANGELOG.md`, `docs/`, `README.md`, `scripts/`, `.github/`, or `Makefile`.

## Final report

Under 200 words. Include: files created, files edited (full list), line counts (rough), test counts + pass/fail, smoke-check status, issues filed (counts by severity), deferred-to-integrator items. Do NOT commit.
