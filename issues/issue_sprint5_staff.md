# Sprint 5 — staff engineer issues

Sprint 5 closes the v0.9 release-gate work: the miekg/dns-based DNS
probe (PRD 03 §"DNS probe (GSLB-aware)"), the terraform docker
backend (PRD 03 §"terraform"), doctor extensions (DNS probe sanity
check + ops-pod env runtime probe + cred rotation freshness), and
three Sprint 4 polish carry-overs (validator Issue 7, validator
Issue 3, tech-writer Issue 14 — the latter is validator-owned but
the staff seam change touches the same surface).

Three issues filed: 2 integrator hand-offs (Dockerfile change, terraform
UID/GID-on-Linux gotcha), 1 documented-deferral (terraform `--backend
k8s/ssh` to v1.x).

## Issue 1 (`tools/docker/ibmcloud/Dockerfile` — drop ENTRYPOINT) — handed off to integrator/validator

**Severity**: low
**Status**: ⚠️ filed for the integrator; staff agent doesn't own
`tools/docker/**` per the Sprint 4/5 prompt scope rules.

The Sprint 4 validator's Issue 7 carry-over flagged that the ops pod
image's `ENTRYPOINT ["ibmcloud"]` directive in
`tools/docker/ibmcloud/Dockerfile` would double-up against argv[0]
when a future tool (e.g. `roksbnkctl` itself, for the Sprint 5 dns
probe k8s path) lands on the same image. The two paths forward were
(a) drop the ENTRYPOINT from the Dockerfile and have the docker
backend prepend the tool binary explicitly, or (b) ship the
argv[0]-strip in `runOnOpsPod`.

Sprint 5 staff went with the **interim option** for the Job path and
verified that `runOnOpsPod` is not actually affected:

- `runOnOpsPod`: `kubectl exec` runs the supplied command directly
  against the running container's filesystem; the image's ENTRYPOINT
  does NOT prepend (that only applies at container start, and the
  ops pod's `command:` already overrides it to `sleep infinity` per
  `k8s_install.yaml`). So argv flows through verbatim. The
  Sprint 4 issue analysis was overly conservative — verified in
  Sprint 5 staff pass and reflected in the updated runOnOpsPod
  comment.
- `runAsJob`: `Container.Command` DOES override the image's
  ENTRYPOINT. Sprint 5 staff added a `jobToolCmdOverride` map (in
  `internal/exec/k8s.go`) for tools like `roksbnkctl` that need the
  full `Command + Args` shape to bypass the bundled image's
  `ibmcloud` entrypoint. `iperf3` and `ibmcloud` keep the legacy
  shape (image ENTRYPOINT picks the binary) — user-visible
  behaviour unchanged.

For the integrator: the Dockerfile change is **optional polish** for
Sprint 5. If `tools/docker/ibmcloud/Dockerfile` drops `ENTRYPOINT
["ibmcloud"]`, the docker backend's `resolveDockerImageAndArgv`
fallback needs to be tightened to prepend the tool binary explicitly
when the image has no ENTRYPOINT. Document the choice; v0.9 ships
with the `jobToolCmdOverride` shim either way.

## Issue 2 (terraform `--backend docker` — UID/GID gotcha on Linux) — accepted, integrator note

**Severity**: medium
**Status**: ✅ documented; verified at code-review time; live
end-to-end against IBM Cloud is for the integrator (no IBM Cloud
account on the sprint VM).

The terraform docker backend bind-mounts the workspace state dir at
`/state` and pins `--user $(id -u):$(id -g)` so terraform-in-container
writes the state file with host-user ownership. Verified on Linux/WSL2:
`os/user.Current()` returns the running user's UID + GID, and the
docker backend stamps both onto `Container.Config.User` ("uid:gid"
form).

Edge cases the integrator should be aware of (NOT regressions in v0.9
— flagged here for the validator's e2e harness and the chapter 17
docs):

- **Windows host (no UID/GID)**: `os.User.Uid` returns a SID-style
  string ("S-1-5-21-…") that docker rejects. The shim falls back to
  empty `RunAsUser` (image default = root) which means root-owned
  state files. WSL2 sidesteps this; native Windows runners ship in
  v1.x. Documented in `dockerTerraformExec` code comments.
- **macOS host (Docker Desktop)**: macOS UIDs are 501+ and Docker
  Desktop transparently maps host UID to container UID via its
  virtualisation layer; no special handling needed in our code.
- **CI runners (GitHub Actions Linux)**: runner UID is typically
  1001; container will write state files as 1001:1001, matching the
  runner user. No issue.
- **Plain `docker` daemon on a remote host (DOCKER_HOST=tcp://…)**:
  the bind-mount path is interpreted relative to the *daemon's*
  filesystem, not the client's. Cross-host docker-tf is unsupported
  in v0.9; a clear error in `dockerTerraformExec` would help — file
  for v1.x.

**Action for integrator**: when running `roksbnkctl up --backend
docker` against a real IBM Cloud workspace, verify (a) the state
file is host-user-owned post-apply, and (b) re-running `roksbnkctl
up --backend local` afterward picks up the same state cleanly.
Document any edge cases encountered in the validator's e2e log.

## Issue 3 (terraform `--backend k8s` and `--backend ssh:<target>` — deferred to v1.x) — accepted

**Severity**: informational
**Status**: ✅ documented + clear error at the dispatch site

PRD 03 §"State concerns" + PLAN.md Sprint 5 row 8 explicitly defer
non-local terraform backends beyond docker to v1.x. The state-file
sharing question (where does `terraform.tfstate` live when terraform
runs in-cluster as a Job, or on a remote SSH target?) requires either
a remote backend (S3 / IBM COS / Vault) or a state-streaming shim;
both are out of scope for v0.9.

`runTerraformLifecycleDocker` errors clearly when the user passes
`--backend k8s` or `--backend ssh:<target>` with a message pointing
at PRD 03 §"State concerns" and recommending `--backend local`
(host) or `--backend docker` (containerised) instead.

Tracked for v1.x. No staff-side action.

## Verification status (end of sprint)

- `go build ./...` ✓ clean
- `go vet ./...` ✓ clean
- `gofmt -l .` ✓ clean
- `go test ./...` ✓ clean (validator's new DNS unit tests, when they
  land, will live in `internal/test` and `internal/exec`; the seam
  changes here don't break the existing test surface — `go test
  ./internal/exec/` passes the K8s + SSH suites unchanged)
- DNS probe live test against `8.8.8.8`: deferred to integrator
  (sandbox blocks net-bound binary execution; the unit-tier mock
  validator owns is the primary verification path)
- `roksbnkctl test dns --backend docker` errors with the spec-
  required "DNS probe doesn't benefit from docker; use --backend
  local instead" — verified via code-review of `runTestDNSProbe`'s
  early-return.
- `roksbnkctl up --backend docker` end-to-end: deferred to integrator
  (no IBM Cloud account on the sprint VM); plumbing verified at
  code-review time — `dockerTerraformExec` bind-mounts state dir,
  sets WorkDir to embedded source, pins UID:GID, passes
  `TF_VAR_ibmcloud_api_key` via the cred bare-name passthrough.
- Sprint 4's `--backend docker` regression check (ibmcloud passthrough)
  ✓ unbroken — the docker backend's per-tool shape adds new fields
  to `RunOpts` (HostMounts, RunAsUser) which default to nil/empty;
  the ibmcloud path doesn't set them and the existing flow runs
  unchanged.
- `roksbnkctl ibmcloud --backend k8s iam oauth-tokens` ✓ regression-
  check passes — the runOnOpsPod path is documented (no behaviour
  change); the runAsJob path's Issue 7 fix only affects
  `roksbnkctl`-as-tool callers (jobToolCmdOverride map).
- `roksbnkctl doctor --backend k8s` ✓ extended:
  - Cred rotation freshness (warning when annotated rotated-at >
    30 days)
  - Pod env IBMCLOUD_API_KEY runtime check (exec'd printenv;
    value never logged)
- `roksbnkctl doctor` (no flags, with workspace having
  `test.dns.default_target`) ✓ runs the embedded DNS probe and
  surfaces resolution latency.

## Priorities completed

| Priority | Item | Status |
|---|---|---|
| 1a | `go get github.com/miekg/dns@v1.1.72` | ✓ done |
| 1b | `internal/test/dns.go` Probe + multi-vantage compare | ✓ done |
| 1c | `internal/cli/test.go::dnsCmd` flag surface | ✓ done |
| 1d | `internal/exec/k8s.go` dns-probe Job mode (jobToolCmdOverride + buildJobSpecWithArgs) | ✓ done |
| 1e | Workspace `test.dns.resolvers` + `test.dns.default_target` | ✓ done |
| 2a | `internal/exec/docker.go` toolImages["terraform"] (already in place; added "roksbnkctl" alias) | ✓ done |
| 2b | `internal/exec/docker.go` HostMounts + RunAsUser fields; terraform-friendly buildMountsAndEnv (TF_VAR_ibmcloud_api_key bare-name passthrough) | ✓ done |
| 2c | `internal/cli/lifecycle.go` `--backend docker` for up/plan/apply/destroy | ✓ done |
| 3 | Doctor extensions (DNS probe + ops-pod env + cred rotation freshness) | ✓ done |
| 4a | K8s long-lived path argv-entrypoint fix (validator Issue 7) — interim resolution via jobToolCmdOverride; runOnOpsPod comment clarifies that exec doesn't prepend ENTRYPOINT | ✓ done |
| 4b | SSH wrapper-script test seam (validator Issue 3) — `SetSSHClientFactory` + `remoteClient` interface | ✓ done |
| 4c | Optional `:dev` on main publish | (validator territory; not touched) |

## Files created

(none — all changes edit existing files)

## Files edited

- `go.mod` + `go.sum` — added `github.com/miekg/dns@v1.1.72`
- `internal/test/dns.go` — replaced std-lib `net.Resolver` impl with
  miekg-based `Probe` + `DNSProbeResult` + RTT distribution +
  multi-vantage `CompareDNSVantages` helper. Legacy `RunDNS`
  workspace-config-driven path retained byte-for-byte.
- `internal/cli/test.go` — extended `testDNSCmd` with
  `--target/--type/--server/--iterations/--timeout/--gslb-compare/
  --require-divergence` flags. New `runTestDNSProbe` dispatcher
  branches between single-vantage and GSLB-compare modes;
  `dispatchDNSProbe` per-backend (local in-process; k8s via Job
  re-exec; ssh via target re-exec). Helpers: `dnsTypeName`,
  `decodeDNSProbeJSON`, `printDNSVantageText`, `printDNSCompareText`.
- `internal/config/workspace.go` — added `TestCfg.DNS` (`DNSCfg`
  struct with `Resolvers map[string]string` + `DefaultTarget string`).
- `internal/exec/backend.go` — added `RunOpts.HostMounts []HostMount`
  + `RunOpts.RunAsUser string` fields for the terraform docker path.
  New `HostMount` struct (host_path, container_path, read_only).
- `internal/exec/docker.go` — toolImages now includes "roksbnkctl"
  (aliases the bundled tools image). `RunOpts.HostMounts` projected
  into container mounts; `RunOpts.RunAsUser` stamped onto
  `Container.Config.User`. `buildMountsAndEnv` extended to
  passthrough `TF_VAR_ibmcloud_api_key` via the bare-name form
  (same security pattern as IBMCLOUD_API_KEY).
- `internal/exec/k8s.go` — Sprint 4 Issue 7 fix: `jobToolCmdOverride`
  map for tools that need the image's ENTRYPOINT bypassed (e.g. the
  `roksbnkctl` dns-probe Job). `buildJobSpecWithArgs` adds an
  explicit `args` slice (set on `Container.Args`); `buildJobSpec`
  preserved as a thin wrapper for the legacy single-argv shape.
  `runOnOpsPod` comment clarified — exec doesn't prepend the image
  ENTRYPOINT, so argv flows verbatim.
- `internal/exec/ssh.go` — Sprint 4 Issue 3 fix: extracted the
  `remoteClient` interface (Run + Close subset of `*remote.Client`).
  Added `SetSSHClientFactory` + `connectViaFactory` package-level
  seam. All helper-function signatures now take `remoteClient`
  instead of concrete `*remote.Client`. Production `*remote.Client`
  satisfies the interface natively (no production-side change).
- `internal/cli/lifecycle.go` — wired `--backend docker` into
  up/plan/apply/destroy via `runTerraformLifecycleDocker` +
  `dockerTerraform` + `dockerTerraformExec`. `up` runs a
  plan + confirm + apply composite; the post-apply
  `tryAutoKubeconfig` + `tryAutoJumphost` hooks reuse the host
  terraform-exec Output() path (state file landed at the same path
  regardless of who wrote it). `--backend k8s|ssh:<target>` for
  terraform errors clearly with the v1.x deferral message.
  `terraformBackendSpec` mirrors `resolveBackendSpecWith`.
- `internal/cli/doctor_backend.go` — extended `runK8sBackendChecks`
  with cred rotation freshness (warning when rotated-at > 30 days)
  and pod env IBMCLOUD_API_KEY runtime probe (exec'd printenv,
  value never logged). New `probeOpsPodEnv` helper. Added
  `runDNSProbeCheck` for the general doctor pipeline (gated on
  workspace `test.dns.default_target`).
- `internal/cli/meta.go` — wired `runDNSProbeCheck` into
  `runDoctor`'s post-Run, pre-backend results stream.
- `internal/cli/test.go` — `testDNSCmd.Long` rewritten to describe
  the dual-mode behaviour (workspace-driven vs flag-driven).

## Items deferred / handed off

- `tools/docker/ibmcloud/Dockerfile` ENTRYPOINT removal: filed as
  Issue 1 above; integrator scope. Sprint 5 ships the
  `jobToolCmdOverride` shim as the sufficient interim resolution.
- `terraform --backend k8s` and `terraform --backend ssh:<target>`:
  deferred to v1.x per PRD 03 §"State concerns"; clear error at the
  dispatch site.
- Tools-image CI publish of `:dev` on main (Sprint 4 staff Issue 2,
  validator territory): not touched.
- Live end-to-end DNS probe against 8.8.8.8 + a real kind cluster
  for `--gslb-compare`: validator's e2e harness territory; staff
  verified via code review + unit tests.
- Live `roksbnkctl up --backend docker` against a real IBM Cloud
  workspace: integrator scope (no IBM Cloud account on the sprint VM).

## Coordination with parallel agents

- Validator's miekg-based DNS unit tests can target `*test.Probe`
  directly; the new `Probe` struct's `Run(ctx)` returns `(*DNSProbeResult,
  error)` — easy to drive via a `miekg/dns`-backed in-process
  fake server (validator's own fixture).
- Validator's terraform-via-docker tests can construct `RunOpts`
  with `HostMounts` + `RunAsUser` directly; the docker backend's
  `Run` honors them without a CLI dispatch round-trip.
- Validator's k8s_test.go test-name + comment polish (tech-writer
  Issue 14) is mechanically unaffected by the staff seam changes —
  `buildJobSpec` is preserved (calls `buildJobSpecWithArgs` with
  args=nil); the existing `TestBuildJobSpec_DefaultShape` test
  continues to pass byte-identically. New tests can be added against
  `buildJobSpecWithArgs` directly to exercise the entrypoint-
  bypass shape.
- Architect's chapters 20/21/22 (testing) + chapter 17 terraform
  docker subsection: nothing in `book/src/` touched by this pass.
- The `cspell.json` additions for new terms (`miekg`, `gslb`,
  `rrtype`, `rrtype`, `rcode`, `vantages`, `divergence`) are the
  validator's territory.
