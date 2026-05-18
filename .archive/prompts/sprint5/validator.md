You are the validator agent for Sprint 5 of the roksbnkctl project. Your scope is **unit tests for the miekg-based DNS probe (with a mocked DNS server), integration tests against a real resolver, e2e Phase L-DNS in `scripts/e2e-test-backends.sh`, terraform-via-docker tests, k8s test-name + comment polish (Sprint 4 tech-writer Issue 14 carry-over), the SSH wrapper-script + bootstrap-failure matrix tests (Sprint 4 validator Issue 3 carry-over, once the staff agent lands the seam), CI workflow updates, and CONTRIBUTING.md additions**.

Sprint 5 is the **v0.9 release gate sprint** — at sprint-end the integrator tags `v0.9` and cuts a GitHub release. Your tests are the gate's primary signal.

Project location: `/mnt/d/project/roksbnkctl/`. Min Go: 1.25.

## Read first

- `docs/prd/03-EXECUTION-BACKENDS.md` §"DNS probe (GSLB-aware)" — design spec the unit tests assert against. Pay attention to the JSON output schema (`roksbnkctl.dns.v1`) — your tests should validate that the binary emits valid schema-conformant output, including for negative cases (NXDOMAIN, SERVFAIL, timeout).
- `docs/prd/05-E2E-TEST-PLAN.md` §"Phase L-DNS" — the e2e step list LD0-LD10 you're implementing in `scripts/e2e-test-backends.sh`. LD9 (SSH vantage) requires SSH-backend e2e infra not landing until Sprint 6 — defer the LD9 step with a `yellow ⊘` skip marker.
- `docs/PLAN.md` Sprint 5 "Test deliverables" — your acceptance criteria. Note the "Manual: real GSLB validation against the F5 BIG-IP Next deployment from Phase D" line — that's the integrator's manual checklist for v0.9 sign-off, not an automated test; you don't need to wire it.
- `prompts/sprint4/validator.md` for prompt-structure reference; `issues/resolved_sprint4_validator.md` for the two explicit Sprint 5 carry-overs (Issue 3 wrapper-script + Issue 7 entrypoint).
- `issues/resolved_sprint4_tech-writer.md` Issue 14 — the k8s test-name + comment polish you own this sprint.
- `scripts/e2e-test-backends.sh` — Sprint 4's e2e driver. Phase L-DNS is a new section appended after Phase M.
- `.github/workflows/ci.yml` — existing matrix + Sprint 1 integration + Sprint 3 docker-backend + Sprint 4 k8s-backend jobs. No new CI matrix job this sprint (the DNS probe runs in the existing `test` job's unit tier; integration tier reuses Sprint 4's setup).
- `.github/workflows/tools-images.yml` — Sprint 3's tools-image build workflow. Sprint 4 staff Issue 2 flagged an optional follow-up: also publish `:dev` on `main` pushes. Decide whether to land it (PRD 03 §"Image versioning" closed this as "tied to release version", so `:dev` on main is just a dev-UX nicety; landing it is fine, deferring it is also fine).
- `cspell.json` — add Sprint 5 vocabulary as you encounter it.

## Coordinate with parallel agents

An architect agent is replacing 3 chapter stubs (20 connectivity, 21 DNS-GSLB flagship, 22 throughput) and appending a terraform docker-backend subsection to chapter 17. A staff-engineer agent is implementing the miekg-based DNS probe (`internal/test/dns.go` rewrite, `internal/cli/test.go::dnsCmd` flag extensions, `internal/exec/k8s.go` dns-probe Job mode, workspace config additions), the terraform docker backend (`internal/exec/docker.go` + `internal/cli/lifecycle.go`), doctor extensions, and three Sprint 4 polish carry-overs (k8s entrypoint fix, SSH wrapper-script test seam, optional `:dev`-on-main). **Do not touch their files.** You own all `*_test.go`, `.github/workflows/*.yml`, `scripts/e2e-test-backends.sh`, `cspell.json`, CONTRIBUTING.md additions, and `docs/E2E_TEST.md` updates.

## Tasks

### 1. DNS probe unit tests (`internal/test/dns_test.go`)

The new `Probe` struct + miekg-based implementation. Use `miekg/dns`'s built-in `dns.Server` library for a deterministic in-process resolver — no external network needed for unit tests.

Cases:

- **Record types**: A, AAAA, CNAME, MX, NS, TXT, SRV, SOA, PTR, CAA, DS, DNSKEY each return parsed answers with the right `Type` string in `DNSAnswer.Type`
- **Server resolution**: `Probe.Server = "<ip>:<port>"` uses the literal; `Probe.Server = "system"` reads `/etc/resolv.conf` (mock the file or skip if unavailable); `Probe.Server = "cluster"` errors with "cluster-only server, run with --backend k8s" at the local-backend boundary
- **RTT distribution**: iterations=1 → `p50 == p95 == p99 == singleRTT`; iterations=10 → distinct percentile values (assert ordering p50 ≤ p95 ≤ p99)
- **Error paths**: NXDOMAIN → `Rcode == "NXDOMAIN"`, no error returned; SERVFAIL → `Rcode == "SERVFAIL"`; timeout (mock a server that hangs) → `Rcode == "TIMEOUT"`, error returned with deadline-exceeded; refused → `Rcode == "REFUSED"`
- **JSON schema validation**: marshal a `ProbeResult` and assert it deserialises into the schema documented in PRD 03 (use a JSON-schema validator like `github.com/xeipuuv/gojsonschema` or a hand-written field-presence check)
- **Truncated + authoritative flags**: mock a server that returns TC=1 → `Truncated == true`; mock one with AA=1 → `Authoritative == true`
- **Concurrent iterations**: iterations=10 against a single mock server — ensure all queries finish, ordering is preserved, RTTs are recorded

### 2. DNS probe integration tests (`internal/test/dns_integration_test.go`)

Build-tag `integration`. Two cases:

- **Against `8.8.8.8`**: query `www.cloudflare.com` for A; assert RTT > 0, at least one answer parsed, `Rcode == "NOERROR"`. Skip cleanly if the test host has no network.
- **Against a local stub**: spin up the same `dns.Server` shape from the unit test but in a goroutine; query it in parallel from the same test process. Assert both probes complete, both RTTs are measured, no cross-talk between concurrent probes.

### 3. `--gslb-compare` multi-vantage tests (`internal/cli/test_dns_compare_test.go`)

CLI-level integration test: `roksbnkctl test dns --target <…> --server <…> --gslb-compare -o json` produces:

- A `roksbnkctl.dns.v1`-schema JSON with multiple `vantages[]` entries (one per available backend)
- `gslb_divergence: true` when the vantage answers diverge; `false` when they all agree
- `--require-divergence` flips the exit code when `gslb_divergence == false`

Mock backends if needed — the unit-tier version can substitute mocked Probe implementations per vantage. Reserve the live multi-backend test for the e2e tier (Phase L-DNS).

### 4. Terraform-via-docker tests (`internal/exec/docker_terraform_test.go` + integration)

Unit tier (no Docker required):

- `buildTerraformMounts(workspaceState, vars)` returns the correct bind-mount + env shape (state dir mounted read-write, `--user` flag set, `TF_VAR_*` env vars passed through)
- The image name resolves to `hashicorp/terraform:<v>` (literal pin, not version-resolved per the chapter 17 `:dev` tag section)
- `--backend k8s` and `--backend ssh:<target>` for terraform error with the documented "deferred to v1.x; see PRD 03 §State concerns" message

Integration tier (build-tag `integration`, runs in CI's `docker-backend` job): actually `docker run hashicorp/terraform:<v> --version` succeeds; bind-mount path round-trips a file (write file from container, host can read it back); UID matches `id -u` of the running user.

### 5. Phase L-DNS in `scripts/e2e-test-backends.sh`

PRD 05 §"Phase L-DNS" steps LD0-LD10. Add a new phase block after Phase M (the cred audit) in the existing driver:

```bash
phase_l_dns() {
    local cluster_ready="$1"   # set by caller; whether Phase D's cluster is up

    # LD0: confirm `dig` not on PATH (or, if installed, test runs without invoking it)
    # LD1: --backend local --target www.cloudflare.com --type A --server 8.8.8.8
    # LD2: --backend local --type AAAA
    # LD3: --backend local --target nonexistent-zzz.example.invalid → exit 1, NXDOMAIN
    # LD4: --backend local --iterations 10 → rtt_ms p50/p95/p99 populated
    # LD5: --backend k8s (requires cluster + ops pod from Phase L's L0)
    # LD6: --backend k8s --server cluster (uses pod's /etc/resolv.conf)
    # LD7: --gslb-compare -o json (local + k8s) → 2 vantages, gslb_divergence boolean
    # LD8: GSLB divergence happy path (test against a name known to differ)
    # LD9: SSH vantage — defer per validator's Sprint 4 Issue 6 pattern; yellow ⊘
    # LD10: --backend docker → exit non-zero with "DNS probe doesn't benefit from docker"
}
```

Follow the same `capture`/`assert_contains`/`assert_exit_code` helper pattern as Phases K + L + M. Skip cleanly when the cluster is unreachable (LD5-LD8 need it).

### 6. K8s test-name + comment polish (Sprint 4 tech-writer Issue 14 carry-over)

In `internal/exec/k8s_test.go`:

- Split `TestK8sBackend_Run_Job_CreatesJobAndSecret_TTL` into three single-invariant tests (CreatesJob, CreatesFilesSecret, SetsTTL). Same fake-clientset wiring; one test per pinned behaviour.
- Add docstrings to security-relevant tests citing the PRD invariant they pin. Example:

```go
// TestK8sBackend_NoCredValueInArgv pins the PRD 04 §"In-cluster pod"
// security invariant: the IBM Cloud API key value MUST appear only in
// the Secret's data field (base64), never in argv, never in container
// env values, never in pod metadata annotations or labels. A regression
// here is a cred-leak vulnerability — see audit_test.go for the
// cross-surface audit.
func TestK8sBackend_NoCredValueInArgv(t *testing.T) { … }
```

Cover similar test files in `internal/exec/audit_test.go` and `internal/exec/ssh_test.go` where the same pattern would help.

### 7. SSH wrapper-script + bootstrap-failure tests (Sprint 4 validator Issue 3 carry-over)

Staff is landing the `SetSSHClientFactory` seam this sprint. Once it's in place, write the unit tests this seam unblocks:

- Wrapper script content excludes the cred value (only the env-file path is in the script body; `set +x` confirmed)
- File materialization writes Files entries to `/tmp/roksbnkctl.<rand>/<basename>` (capture the mock client's `Run` invocations and verify the wrapper script's content)
- `sudo -n` fails → exit `126` with the documented "passwordless sudo required" message
- Non-Ubuntu OS (`lsb_release -is` returns `RHEL`) → exit `126` with the documented "auto-install only supports Ubuntu" message
- Package-repo unreachable → exit `127` with the documented "target can't reach the package repo" message
- `--bootstrap` opt-in: tool missing without flag → exit `127`; with flag → apt-get spawn observed

Add these to `internal/exec/ssh_test.go`.

### 8. cspell.json — Sprint 5 vocabulary

Add as you encounter, but at minimum:

- `miekg`, `coredns`, `CoreDNS`, `NXDOMAIN`, `SERVFAIL`, `NOERROR`, `SOA`, `DNSKEY`, `dnskey`, `rdata`, `rcode`, `AcceptEnv`, `Anycast`, `anycast`
- `apparmor` if it shows up; `bindfs` for any future bind-mount workaround mention
- Sprint 5 chapter 20/21/22 will surface more — sweep `book/src/20-*.md` / `21-*.md` / `22-*.md` after the architect's commits land

### 9. CONTRIBUTING.md additions

Append:

- "Running DNS probe unit tests" — `go test ./internal/test/dns_test.go` runs the in-process miekg-based mock server; integration tier is `-tags integration` and needs network for the `8.8.8.8` assertion.
- "Testing GSLB scenarios manually" — how to point `roksbnkctl test dns --gslb-compare` at a real GSLB endpoint to validate the divergence detection (for the v0.9 release manual checklist).

### 10. Optional: `:dev`-on-main push in `tools-images.yml` (Sprint 4 staff Issue 2)

If you have spare scope, add a `:dev` push step to `.github/workflows/tools-images.yml` so `go install ./cmd/roksbnkctl@main` works on a fresh host without a local docker build. The current workflow only fires on tag pushes; add a `push: branches: [main]` trigger that tags the published image as `:dev`. Defer if seam-work or DNS coverage runs over budget; it's a UX nicety, not a v0.9 blocker.

### 11. v0.9 release-gate documentation in `docs/E2E_TEST.md`

Sprint 5 is the v0.9 release gate. Add a "v0.9 release checklist" section to `docs/E2E_TEST.md` documenting the manual gate items the integrator runs before tagging:

- `scripts/e2e-test-backends.sh` (all phases, including new Phase L-DNS) passes against a real BNK-deployed cluster
- Manual GSLB validation: `roksbnkctl test dns --gslb-compare` produces `gslb_divergence: true` against a real F5 BIG-IP Next GSLB record
- `roksbnkctl up --backend docker` runs a full plan→apply→destroy cycle against the embedded HCL
- All Phase M cred-audit assertions pass (no API key leaks across all four backends)
- `roksbnkctl doctor` green on a stock dev box with only `terraform` installed

This is the integrator's checklist for the `v0.9` tag — your scope is to write it down clearly.

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean (incl. new DNS unit tests + the split + docstring'd k8s tests)
- `go test -tags integration -timeout 10m ./internal/exec/... ./internal/cli/... ./internal/test/...` runs (skip-or-pass on missing prereqs)
- `bash -n scripts/e2e-test-backends.sh` clean
- `DRY_RUN=1 ./scripts/e2e-test-backends.sh` shows all phases (K, L, M, L-DNS) cleanly
- `gofmt -d -l .` clean for any Go file you touched

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint5_validator.md`. `Severity: roadmap` for forward-looking items.

## Final report (under 200 words)

- Files created
- Files edited
- Test results (unit + integration if available)
- Issues filed (counts by severity)
- Whether `DRY_RUN=1 ./scripts/e2e-test-backends.sh` shows all phases cleanly (incl. new Phase L-DNS)
- Anything the integrator should know (especially regarding the GSLB-divergence test target choice for LD8 — if `www.google.com` returns identical answers via anycast, document an alternative target the integrator should use during manual v0.9 sign-off)

Do NOT commit. The integrator commits the aggregated work.
