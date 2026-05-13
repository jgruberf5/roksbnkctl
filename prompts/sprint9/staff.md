You are the staff engineer agent for Sprint 9 of the roksbnkctl project. Sprint 9 closes PRD 04's two long-deferred items (cred-tmpfile-bind-mount for docker, trusted-profile auto-provisioning for k8s) and cuts `v1.2.0`. Your scope is the implementation: the tmpfile pattern in `internal/exec/docker.go`, the trusted-profile path across `internal/exec/k8s.go` + `internal/cli/ops.go` + new `internal/ibm/trusted_profile.go`, and removing the two `t.Skip` markers landed in commit `776fe56`.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

The two `t.Skip` markers' TODO comments are the canonical statement of what you must close — they name the exact design choice and the rationale. Re-read them at sprint start; your code should match the option you pick, and you should remove the skip when the test goes green.

## Read first

- `docs/prd/04-CREDENTIALS.md` — your authoritative source. Specifically §"Backend × credential matrix" (current state), §"Implementation tasks" task 8 (trusted-profile), and §"Open questions" first two items. Architect adds a §"Resolved in Sprint 9" subsection during the sprint; your code should match the design they document there.
- `docs/PLAN.md` §"Sprint 9" — your authoritative deliverables list (the code-deliverables table). Items 1-3 are yours (the docker tmpfile, the k8s trusted-profile, the test image switch); items 4-5 are validator's.
- `internal/exec/docker.go` — current `buildMountsAndEnv` passes `IBMCLOUD_API_KEY=value` directly (v1.0.2 fix that the WIP comment explicitly calls out as a deferred trade-off). You replace this with the tmpfile-bind-mount pattern.
- `internal/exec/k8s.go` — current `installOpsPod` (or equivalent — search for the Secret-creation path) puts the static API key in a Secret. You add the trusted-profile path alongside the static fallback.
- `internal/cli/ops.go` — current `roksbnkctl ops install` cobra command. You add the `--trusted-profile=auto|on|off` flag here.
- `internal/exec/docker_integration_test.go:75-86` + `internal/exec/k8s_integration_test.go:101-119` — the two `t.Skip` markers. Read the TODO comments; pick the option named there for each test.
- `internal/ibm/` — existing IBM Cloud API client patterns. The new `trusted_profile.go` follows the same shape (constructor, methods on a typed struct, error wrapping).
- `internal/exec/k8s_install.yaml` (or equivalent embedded RBAC manifests) — current ServiceAccount + ClusterRole. The trusted-profile path may need an annotation on the SA.
- `prompts/sprint8/staff.md` — prior-sprint prompt structure; the verification block is reusable verbatim.

## Coordinate with parallel agents

An **architect** agent is closing PRD 04 §"Open questions" with a new §"Resolved in Sprint 9" subsection, updating chapter 14 (cred resolver) with a tmpfile + `--trusted-profile` section, updating chapter 19 (ops pod) with the auto-provisioning flow, and writing the CHANGELOG `v1.2.0` entry. **Do not touch `book/src/`, `CHANGELOG.md`, `docs/`, or `README.md`.**

A **validator** agent is running the regression sweep, adding `TESTCONTAINERS_RYUK_DISABLED=true` to CI, extending the `make release` checklist with `staticcheck` + `-tags integration` build, live-verifying the trusted-profile path against a sandbox IBM Cloud workspace. **Do not touch `.github/workflows/` or `Makefile`.** They'll file issues against your code if examples in the chapters diverge from your actual implementation.

A **tech-writer** agent does read-only review at the end of the sprint.

**Your scope** is `internal/exec/docker.go` (edit), `internal/exec/k8s.go` (edit), `internal/exec/k8s_install.yaml` (edit, if SA annotation needed), `internal/cli/ops.go` (edit), `internal/ibm/trusted_profile.go` (new), `internal/exec/docker_integration_test.go` (edit — remove `t.Skip`), `internal/exec/k8s_integration_test.go` (edit — remove `t.Skip` + switch image), and accompanying `_test.go` unit tests.

## Tasks (priority order)

### 1. Cred tmpfile-bind-mount pattern (`internal/exec/docker.go`)

Replace the current `Env: ["IBMCLOUD_API_KEY=value", "IC_API_KEY=value", "TF_VAR_ibmcloud_api_key=value"]` shape with a tmpfile-bind-mount pattern. Design:

- Backend acquires the API key value from `opts.Credentials.IBMCloudAPIKey`.
- Write the value to a per-run `0600` tempfile at `<TMPDIR>/roksbnkctl-creds-<rand>/api-key`. Use `os.MkdirTemp` + `os.WriteFile` with `0600` mode.
- Bind-mount the file read-only into the container at `/run/secrets/ibmcloud_api_key`.
- Set `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key` in container env.
- For the existing `dockerImageBinary["ibmcloud"]` login-wrap (and any tool that reads `IBMCLOUD_API_KEY` directly rather than `_FILE`), inject a small shell shim at the head of the Cmd: `export IBMCLOUD_API_KEY=$(cat $IBMCLOUD_API_KEY_FILE); export IC_API_KEY=$IBMCLOUD_API_KEY; export TF_VAR_ibmcloud_api_key=$IBMCLOUD_API_KEY; exec original-cmd`. The export-then-exec means the env var is only set inside the container's shell scope, not in the container metadata visible to `docker inspect`.
- Cleanup: register the tempdir for removal on backend exit via the existing context-cancel cleanup goroutine (search for the `defer` patterns in `runOnOpsPod` and similar). Tempfile must outlive every container that needs it (terraform docker runs can take 20+ min), so the cleanup is keyed on backend lifecycle, not per-container.

The existing `dockerImageBinary["ibmcloud"]` sh-c wrap already does an `exec ibmcloud "$@"` shape — extend it to first source the key from the file. The terraform / iperf3 / roksbnkctl entries also need updating to source the file before exec.

Acceptance: `TestIntegration_DockerBackend_NoLeakInInspect` passes (remove its `t.Skip`).

### 2. K8s trusted-profile auto-provisioning (`internal/exec/k8s.go`, `internal/cli/ops.go`, new `internal/ibm/trusted_profile.go`)

Implement the `--trusted-profile=auto|on|off` flag flow:

- New `internal/ibm/trusted_profile.go` — typed `TrustedProfile` struct + methods: `CreateForOpsPod(ctx, name, saNamespace, saName)` (creates `roksbnkctl-ops-<workspace>` linked to the ops pod's SA + projected SA token issuer), `Get(ctx, name)`, `Delete(ctx, name)`. Uses the existing `ibm.New(apiKey, region)` pattern. Surface a typed `ErrIAMPermDenied` for the auto-fallback path to switch on.
- `internal/cli/ops.go` — add `--trusted-profile string` flag with default `auto`. Validate `auto|on|off` at flag-parse time.
- `internal/exec/k8s.go::installOpsPod` (or equivalent) — branch on the flag:
  - `auto`: try `ibm.TrustedProfile.CreateForOpsPod`; on `ErrIAMPermDenied`, fall back to static-key Secret with a stderr warning ("IAM perm 'iam-identity' missing; using static-key Secret. Pass `--trusted-profile=off` to silence."). On other errors, fail.
  - `on`: try; fail loudly on `ErrIAMPermDenied`. Don't fall back.
  - `off`: skip the trusted-profile path; create the static-key Secret directly (v1.0.x behavior).
- Pod spec: when the trusted profile is provisioned, annotate the ops pod's SA with `iam.cloud.ibm.com/trusted-profile: <name>` and add the volume + projected SA token (the upstream `flo` module's `cne_controller` profile is a reference pattern). When falling back, keep the v1.0.x Secret + envFrom path.

Acceptance: a sandbox IBM Cloud workspace can run `roksbnkctl ops install --trusted-profile=auto`, get a trusted-profile-backed ops pod, and `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returns a valid IAM token. Validator covers this; you smoke-verify locally if you have sandbox access.

### 3. Remove `t.Skip` markers

- `internal/exec/docker_integration_test.go:75` — delete the `t.Skip` block. The test should now pass against the new tmpfile pattern.
- `internal/exec/k8s_integration_test.go:101` — delete the `t.Skip` block AND change the test's pod image from `busybox:1.36` to `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>` (the tag is from the same source as `toolImages["ibmcloud"]` in `docker.go`). The tools image runs as uid 1000 by default, so `runAsJob`'s `RunAsNonRoot: true` admission check passes. Update the test's expected echo argv / expected output to match (the tools image's ENTRYPOINT is the ibmcloud binary, so the test needs `["ibmcloud", "--version"]` or similar — pick whatever's shortest and most stable).

### 4. Unit tests

- `internal/exec/docker_test.go` — extend `TestRunOpts_TFVarsEnvPassthrough` and the `TestResolveDockerImageAndArgv/ibmcloud_*` subtests to cover the new shape (tempfile path, the shell-shim that exports from `$IBMCLOUD_API_KEY_FILE`).
- `internal/ibm/trusted_profile_test.go` (new) — table tests against a mocked IBM Cloud HTTP client; cover create / get / delete / ErrIAMPermDenied detection.
- `internal/cli/ops_test.go` — flag-validation tests for `--trusted-profile` (reject invalid values; default to `auto`).

### 5. Smoke verify

- `go build ./...` clean.
- `go test ./...` green on the default-tag set.
- `go vet ./...` clean.
- `gofmt -d -l .` empty.
- `staticcheck ./...` clean.
- `go build -tags integration ./...` clean.
- `go run ./cmd/roksbnkctl ops install --help` lists `--trusted-profile`.
- `go run ./cmd/roksbnkctl ops install --trusted-profile=invalid` errors with a parse-time validation message.

## Issue tracking

File at `issues/issue_sprint9_staff.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

If you find a chapter or PRD inconsistency, file against architect's surface — don't edit prose yourself.

## Verification before reporting done

- All six items in §"Smoke verify" pass.
- Both `t.Skip` markers removed; both tests compile + pass under `go test -tags integration ./internal/exec/...` (live verification of the k8s test requires a kind cluster + docker daemon; the docker test requires just a docker daemon — staff smoke-tests both locally if available).
- No edit under `book/src/`, `CHANGELOG.md`, `docs/`, `README.md`, `scripts/`, `.github/`, or `Makefile`.

## Final report

Under 200 words. Include: files created, files edited (full list), line counts (rough), test counts + pass/fail, smoke-check status, issues filed (counts by severity), deferred-to-integrator items. Do NOT commit.
