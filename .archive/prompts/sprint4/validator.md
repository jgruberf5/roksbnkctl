You are the validator agent for Sprint 4 of the roksbnkctl project. Your scope is **unit tests + kind-based integration tests + cred-leak audits for k8s + ssh + a new `scripts/e2e-test-backends.sh` covering PRD 05 Phases K + L + CI workflow updates + cspell + CONTRIBUTING** for the k8s + SSH backends the staff agent is implementing per PRD 03 second half.

Project location: `/mnt/d/project/roksbnkctl/`. Min Go: 1.25.

## Read first

- `docs/prd/03-EXECUTION-BACKENDS.md` — backend interface design + per-backend specifics (especially §"K8s" and §"SSH").
- `docs/prd/04-CREDENTIALS.md` — cross-backend cred-propagation rules; the "anti-patterns to avoid" list becomes your audit-test assertions for k8s + ssh (in addition to the local + docker audit Sprint 3 already landed).
- `docs/prd/05-E2E-TEST-PLAN.md` §"Phase K" + §"Phase L" + §"Phase M" — the full e2e coverage the new `scripts/e2e-test-backends.sh` is implementing. Phase K covers the docker backend deeply (Sprint 3 only had B10 prelim); Phase L covers k8s end-to-end; Phase M is the cred-propagation audit that runs after K + L.
- `docs/PLAN.md` Sprint 4 "Test deliverables" — your acceptance criteria.
- `prompts/sprint3/validator.md` for prompt-structure reference; `issues/resolved_sprint3_validator.md` for the explicit Sprint 4 carry-over (k8s + ssh cred-leak audit deferred to Sprint 4).
- `scripts/e2e-test.sh` — Sprint 1+2+3's e2e driver. Sprint 3 added Phase B10 (docker prelim). The new `scripts/e2e-test-backends.sh` is a sibling driver focused on the four-backend matrix; it reuses Phase D's cluster from `e2e-test.sh` (run after baseline e2e brings the cluster up).
- `.github/workflows/ci.yml` — existing Sprint 0 matrix + Sprint 1 integration job + Sprint 3 docker-backend job. You add a kind-based k8s-backend job.
- `cspell.json` — Sprint 0 tech-writer Issue 1 carry-over: `"SSC"` typo to fix.

## Coordinate with parallel agents

An architect agent is replacing/extending 3 book chapters (17 full deep-dive, 18 new, 19 new). A staff-engineer agent is implementing PRD 03's k8s + ssh backends, the in-cluster ops pod (`roksbnkctl ops install/show/uninstall`), iperf3 SCC fix, iperf3 + ibmcloud backend-selection wiring, doctor extensions, and Sprint 3 polish carry-overs. **Do not touch their files.** You own all `*_test.go`, `.github/workflows/*.yml`, `scripts/e2e-test-backends.sh` (NEW), `cspell.json`, CONTRIBUTING.md additions, and `docs/E2E_TEST.md` updates.

## Tasks

### 1. K8s backend argv-builder + spec-builder unit tests (`internal/exec/k8s_test.go`)

Table-driven tests covering the k8s backend's argv handling + Job/Pod spec construction. Use `client-go/kubernetes/fake` for the in-process clientset. Cases:

- Job spec construction: argv → Job's container args; image resolves correctly per tool; `Files` materialise as a projected Secret with the right keys
- Long-lived ops-pod path: argv → kubectl-exec stream wiring (mock the SPDY executor; verify exec request shape — pod name, namespace, command)
- Cred propagation: `Credentials{IBMCloudAPIKey: "abc"}` → Job env-from-secretRef references `roksbnkctl-ibm-creds`; argv does NOT contain the key value
- `ttlSecondsAfterFinished: 60` set on Job spec
- Context cancellation: `Run` ctx cancel → explicit Job + pod delete sent to the fake client

### 2. SSH backend argv-builder + wrapper-script unit tests (`internal/exec/ssh_test.go`)

Table-driven tests covering the SSH backend's wrapper-script construction + env propagation paths. Mock the `internal/remote.Client` (interface-based dependency injection — coordinate with staff if the current Client is concrete-only; file an issue if a refactor is needed). Cases:

- File materialization: `RunOpts.Files = {"kubeconfig": ...}` → wrapper-script writes to `/tmp/roksbnkctl.<random>/kubeconfig` and sets `WorkDir`
- Env propagation — SetEnv path: `RunOpts.Env = ["FOO=bar"]` → SSH session's `SetEnv` called with FOO=bar
- Env propagation — wrapper-script fallback path: when SetEnv is detected as dropped (test forces this via mock), env file written to tempdir + sourced silently (no `set -x` leaks; verify wrapper script content explicitly)
- `--bootstrap` opt-in: tool missing on remote without `--bootstrap` → exit 127 with clear message (no apt-get spawned); with `--bootstrap` → apt-get spawn observed
- Bootstrap failure modes: simulate `sudo -n` failing → exit 126; non-Ubuntu (mock returns `RHEL` from `lsb_release`) → exit 126; package-repo unreachable → exit 127
- Cleanup-on-exit: `defer` triggers tempdir removal even on ctx cancel
- Per-PRD-04: argv must NEVER contain the API key value; assert across all SSH cases

### 3. K8s + SSH cred-leak audit unit tests (`internal/exec/audit_test.go` extension)

Sprint 3 landed the audit unit test for local + docker. Extend it with k8s + ssh assertions per `resolved_sprint3_validator.md` carry-over (Issue 4):

- After running each backend with a known-secret cred, scan multiple inspection surfaces and assert the secret value never appears:
  - `os.Environ()` after Backend.Run returns
  - argv passed to the backend (via captured wrapper)
  - captured stdout/stderr (validates redactor wraps both new backends)
  - **K8s only**: scan Job/Pod spec returned by the fake client — no env value in `container.env[].value` (must use `valueFrom: secretKeyRef`); no key in `metadata.annotations`; no key in `metadata.labels`
  - **K8s only**: scan the projected Secret's data — present (base64-encoded) but **only** in the Secret, nowhere else
  - **SSH only**: scan the wrapper script content captured by the mock SSH client — env file path is referenced but the literal API key value is NOT in the wrapper script body (`set +x` discipline confirmed)
  - **SSH only**: scan the `argv` sent to the remote process — no key value

### 4. Kind-based k8s backend integration tests (CI workflow + tests)

Add a kind-based integration tier: the test spins up a kind cluster, runs `roksbnkctl ops install`, exercises the k8s backend's Job + ops-pod paths against it, then tears down.

- **CI workflow update** in `.github/workflows/ci.yml` — add a `k8s-backend` matrix job (linux-only, like Sprint 3's `docker-backend`):

  ```yaml
  k8s-backend:
    needs: test
    runs-on: ubuntu-latest
    if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name == github.repository
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: 'go.mod' }
      - uses: helm/kind-action@v1
        with: { wait: 120s, version: v0.24.0 }
      - run: go test -tags integration -timeout 10m ./internal/exec/... ./internal/cli/ops_test.go
  ```

- **Integration tests** in `internal/exec/k8s_integration_test.go` and `internal/cli/ops_integration_test.go` (build-tag `integration`):
  - `roksbnkctl ops install` succeeds; ops pod becomes Ready within timeout; RBAC objects created
  - `roksbnkctl ops show` reports correct status
  - K8s backend Job-mode: run a no-op probe (busybox `echo hello`), assert pod ran + cleaned up, log captured
  - K8s backend exec-mode: exec `echo hello` into the ops pod, assert stdout matches
  - RBAC negative: `kubectl auth can-i delete pods --as=...:roksbnkctl-ops -n default` returns `no`
  - RBAC positive: `... can-i create jobs ... -n roksbnkctl-test` returns `yes`
  - `roksbnkctl ops uninstall` removes everything cleanly

### 5. New e2e driver — `scripts/e2e-test-backends.sh` (PRD 05 Phases K + L + M)

A new sibling driver to `scripts/e2e-test.sh`. Reuses the cluster from baseline e2e Phase D — runs **after** the baseline driver's Phase D brings the cluster up. The PLAN.md Sprint 4 "E2E" deliverable is this file.

Structure to mirror `e2e-test.sh`:
- Source the same `lib/` helpers (`capture`, `assert_contains`, `assert_exit_code`, etc.)
- `WORKSPACE` reuse from baseline driver (or a `--workspace` flag if standalone)
- Per-phase logging to `/tmp/roksbnkctl-e2e-backends/<phase>-<ts>.log`
- DRY_RUN=1 support

Phases to implement, copying the step tables verbatim from PRD 05:

- **Phase K (docker, deep coverage)**: K1-K6 from PRD 05 §"Phase K"
  - K1: docker info exits 0
  - K2: ibmcloud --backend docker iam oauth-tokens
  - K3: ibmcloud --backend docker ks cluster ls
  - K4: cred isolation — `docker inspect | jq` should NOT find key value
  - K5: throughput --backend docker --mode north-south
  - K6: no-daemon negative path (skipped in CI by default; opt-in via `RUN_K6=1`)
- **Phase L (k8s, full coverage)**: L0-L7 from PRD 05 §"Phase L"
  - L0: ops install
  - L1: ibmcloud --backend k8s iam oauth-tokens
  - L2: throughput --backend k8s
  - L3: kubectl get jobs cleanup check
  - L4: cred check — Secret data is base64
  - L5: RBAC negative
  - L6: RBAC positive
  - L7: ops uninstall
- **Phase M (cred audit)**: M1-M5 + M7 from PRD 05 §"Phase M" — runs after K + L. M5 + M6 are SSH-specific (SSH backend audit defers to a later sprint per PRD 05's own ordering — file an issue if M6 should land in this sprint's e2e or if it's intentionally deferred).

Update `docs/E2E_TEST.md` to point at the new driver and document the run order (baseline Phase D first, then `scripts/e2e-test-backends.sh`).

### 6. cspell.json fix — Sprint 0 tech-writer Issue 1 carry-over

Replace `"SSC"` with `"SCC"` in the allowed-words array. Per Sprint 0 tech-writer Issue 1: this typo is silent today (spellcheck.yml runs `continue-on-error: true`), but Sprint 4's iperf3-on-OpenShift work and chapter 19's RBAC discussion will surface real "SCC" usage; the typo would let `SSC` typos through while the genuine `SCC` usage trips the spell check.

Also add the Sprint 4 vocabulary that's about to land in chapters 17/18/19 + tests: `kubeconfigs`, `passthrough`, `passthroughs`, `cobra`, `tfvars`, `restricted-v2`, `seccompProfile`, `RuntimeDefault`, `kubectl-exec`, `secretKeyRef`, `secretRef`, `subjectaltname`, `noproxy`, plus any others surfacing in Sprint 4 chapters/tests as you read them.

### 7. CONTRIBUTING.md additions

Append (do not edit other agents' content):

- "Running kind-based integration tests" subsection — what `make test-k8s-integration` runs (or `go test -tags integration -timeout 10m ./internal/exec/... ./internal/cli/ops_test.go`); how to install kind locally; how to point at an existing kind cluster vs spinning a fresh one.
- "Running scripts/e2e-test-backends.sh locally" subsection — pre-reqs (baseline e2e Phase D's cluster up; Docker daemon for K phase; kind or any kube cluster for L phase); how DRY_RUN=1 works for inspection.

### 8. README.md highlight bullet — Sprint 4 (append-only)

Per Sprint 1+2+3 cadence, add a Sprint 4 highlight bullet to README.md's "Highlights" section, immediately after the Sprint 3 v0.9 docker-backend bullet:

```
- **--backend k8s + --backend ssh** (v0.9) — k8s backend uses an in-cluster ops pod for ad-hoc commands and one-shot Jobs for ephemeral runs (iperf3 client, future terraform); SSH backend wraps Sprint 1's `--on` SSH client with file materialization, env propagation (SetEnv + wrapper fallback), and Ubuntu apt-bootstrap behind `--bootstrap`. See chapter 17.
```

(Sprint 3's tech-writer flagged the missing v0.9 highlight as Issue 4 medium — Sprint 4 lands the second half of the highlight pair, completing the four-backend story.)

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean
- `go test -tags integration -timeout 10m ./internal/exec/... ./internal/cli/ops_test.go` runs (skip-or-pass; not a hard-fail if kind isn't available locally — the CI job covers it)
- `bash -n scripts/e2e-test-backends.sh` clean
- `DRY_RUN=1 ./scripts/e2e-test-backends.sh` shows all phases cleanly
- `gofmt -d -l .` clean for any Go file you touched
- `cspell --no-progress 'book/**' 'docs/**' 'README.md' 'CONTRIBUTING.md'` reports zero (or only known) misses; `SSC` not present anywhere as an allowed word

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint4_validator.md`. `Severity: roadmap` for forward-looking items.

## Final report (under 200 words)

- Files created
- Files edited
- Test results (unit + kind-based integration if available)
- Issues filed (counts by severity)
- Whether `DRY_RUN=1 ./scripts/e2e-test-backends.sh` shows all phases cleanly
- Anything the integrator should know (especially regarding kind setup gotchas, the SSH backend mock surface, and PHASE M6 SSH inclusion)

Do NOT commit. The integrator commits the aggregated work.
