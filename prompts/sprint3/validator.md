You are the validator agent for Sprint 3 of the roksbnkctl project. Your scope is **unit tests + integration tests + cred audit + tools-image build workflow + e2e Phase K-prelim** for the cred abstraction + first two backends the staff agent is implementing per PRDs 04 + 03.

Project location: `/mnt/d/project/roksbnkctl/`. Min Go: 1.25.

## Read first

- `docs/prd/04-CREDENTIALS.md` — the spec staff is implementing for the cred half. Pay attention to the cross-backend principles section (never log creds, never put creds in argv, etc.) — those become your audit-test assertions.
- `docs/prd/03-EXECUTION-BACKENDS.md` — backend interface design + per-backend specifics
- `docs/PLAN.md` Sprint 3 "Test deliverables" — your acceptance criteria
- `prompts/sprint2/validator.md` for prompt-structure reference; `issues/resolved_sprint2_validator.md` for the cli-runtime-can't-be-fakeable note (similar trade-off may apply here for `docker/docker/client`)
- `scripts/e2e-test.sh` — existing Phase D + B7-B9 from Sprint 1; Sprint 2 added D3b (PATH-strip)

## Coordinate with parallel agents

An architect agent is replacing 5 chapter stubs with real prose under `book/src/` (12, 13, 14, 15, 17 intro). A staff-engineer agent is implementing PRDs 04 + 03 in `internal/cred/` + `internal/exec/`, refactoring `internal/cli/cluster.go` passthroughs, filling in `tools/docker/{ibmcloud,iperf3}/Dockerfile`, and adding the `--backend` CLI flag + workspace config `exec:` block. **Do not touch their files.** You own all `*_test.go`, `.github/workflows/*.yml`, `scripts/e2e-test.sh`, `docs/E2E_TEST.md`, and CONTRIBUTING.md additions.

## Tasks

### 1. Cred resolver unit tests (`internal/cred/resolver_test.go`)

Table-driven tests covering the resolver chain (env → keychain → config-b64 → prompt). Cases:

- env-only: `IBMCLOUD_API_KEY` set, others unset → returns env value
- keychain-only: env empty, keychain has key → returns keychain value
- config-b64-only: env + keychain empty, workspace config has `api_key_b64` → returns decoded
- prompt path: NonInteractive=false, all prior empty → returns prompt input (use a stub Stdin)
- non-interactive miss: NonInteractive=true, all sources empty → returns error
- env-shadows-keychain: both set, env wins (verifies the resolver order)

Use `github.com/zalando/go-keyring` (already a dep). Mock via the `keyring.MockInit()` helper for tests. The keychain backend on CI may not be available — `t.Skip` cleanly when keyring tests can't run.

### 2. Redactor unit tests (`internal/exec/redact_test.go`)

Cover:
- Single-write key match: secret appears in one write call → redacted
- Split-across-writes: secret split across two `Write()` calls → still redacted (validates the buffering)
- No false positives: a string that's a prefix of the secret but doesn't match → not redacted
- Multiple secrets: both API keys present → both redacted
- Empty secrets list: no redaction, pass-through
- Edge: secret at exact write boundary; secret repeated in stream

### 3. Backend interface unit tests (`internal/exec/local_test.go`, `internal/exec/docker_test.go`)

For `local`:
- Run echo: argv → stdout
- Exit code propagation: command that exits 7 → returns 7
- Env propagation: caller sets `Env`, command sees the var
- Stdin: pipe input → command sees on stdin
- Context cancellation: `sleep 30` cancelled after 100ms → returns within 5s

For `docker` (these may need to skip if Docker daemon not running locally — `t.Skip` cleanly):
- Container creation: builds correct `container.Config` with env/mounts
- Cred propagation: `Credentials{IBMCloudAPIKey: "abc"}` → docker run gets `--env IBMCLOUD_API_KEY` (NO `=value`)
- Auto-remove: container is removed on exit
- Auto-kill on ctx-cancel

### 4. Cred-leak audit unit test (`internal/exec/audit_test.go`)

This is the security-spine test PRD 04 calls for. After running each backend with a known-secret cred, scan multiple inspection surfaces and assert the secret value never appears:

- `os.Environ()` after Backend.Run returns
- The `argv` passed to Backend.Run (the test stubs Backend and inspects what argv it was called with)
- The captured stdout/stderr (validates the redactor)
- (Docker only) `docker inspect <last-container>` output

Format:

```go
func TestCredAudit_NoLeakInArgv(t *testing.T) {
    secret := "test-key-roksbnkctl-audit"
    // Set up the wrapped command to use the secret
    // Run via Backend
    // Inspect argv via a captured Backend wrapper
    // Assert secret not in argv
}
```

### 5. GitHub Actions tools-image build workflow (`.github/workflows/tools-images.yml`)

Build + push the tools images (`ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud`, `roksbnkctl-tools-iperf3`) on tag pushes:

```yaml
name: Build tools images
on:
  push:
    tags: ['v*']
  workflow_dispatch:
jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    strategy:
      matrix:
        image: [ibmcloud, iperf3]
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v5
        with:
          context: tools/docker/${{ matrix.image }}
          push: true
          tags: |
            ghcr.io/jgruberf5/roksbnkctl-tools-${{ matrix.image }}:${{ github.ref_name }}
            ghcr.io/jgruberf5/roksbnkctl-tools-${{ matrix.image }}:latest
```

This runs only on tag pushes (e.g. `v0.9-rc1`); it doesn't fire on every PR.

### 6. CI workflow updates — `.github/workflows/ci.yml`

Add a `docker-backend` integration job (linux-only, similar to Sprint 1's `integration` job):

```yaml
docker-backend:
  needs: test
  runs-on: ubuntu-latest
  if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name == github.repository
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with: { go-version-file: 'go.mod' }
    - run: go test -tags integration -timeout 5m ./internal/exec/...
```

Make sure the Sprint 1 `integration` job still runs.

### 7. E2E patch — `scripts/e2e-test.sh` Phase K-prelim

PLAN.md sequences a "Phase K-prelim" into Sprint 3 that exercises `--backend docker` for `ibmcloud iam oauth-tokens`. Phase K is currently scoped for Sprint 6's full E2E plan; Sprint 3 lands a minimal precursor.

Add to `phase_B` (after step B9, before "Phase C uses it"):

```bash
# B10: --backend docker prelim — validates the docker backend gets
# IBMCLOUD_API_KEY propagated correctly without leaking it via
# `docker inspect`. Skipped if the Docker daemon isn't reachable.
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    capture "B10 docker backend ibmcloud iam" \
        "$ROKSBNKCTL" -w "$WORKSPACE" ibmcloud --backend docker iam oauth-tokens \
        | assert_contains "IAM token" "B10 docker backend produces token"
else
    yellow "  ⊘ B10 skipped — Docker daemon not reachable"
fi
```

Update `docs/E2E_TEST.md` to reflect B10.

### 8. CONTRIBUTING.md additions

Append (do not edit other agents' content):

- "Running cred-audit tests" subsection — what `make test-cred-audit` runs (or `go test -run CredAudit ./...`), and why it's the security spine
- "Building tool images locally" — point at `tools/docker/Makefile`'s `build-ibmcloud`/`build-iperf3` targets

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean
- `go test -tags integration -timeout 5m ./internal/exec/...` runs (skip-or-pass; not a hard-fail if Docker isn't available)
- `bash -n scripts/e2e-test.sh` clean
- `DRY_RUN=1 ./scripts/e2e-test.sh` shows B10 cleanly
- `gofmt -d -l .` clean for any Go file you touched

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint3_validator.md`. `Severity: roadmap` for forward-looking items.

## Final report (under 200 words)

- Files created
- Files edited
- Test results (unit + integration if Docker available)
- Issues filed (counts by severity)
- Whether `DRY_RUN=1` shows B10 cleanly
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
