You are the staff engineer agent for Sprint 5 of the roksbnkctl project. Your scope is **the DNS probe (`miekg/dns` integration + multi-vantage `--gslb-compare`)**, **the terraform docker backend**, **doctor extensions for the DNS probe + k8s ops-pod health**, and **three Sprint 4 polish carry-overs**.

Sprint 5 is the **v0.9 release gate sprint** — at sprint-end the integrator tags `v0.9` and cuts a GitHub release. Your code must be release-quality.

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25.

## Read first

- `docs/prd/03-EXECUTION-BACKENDS.md` §"DNS probe (GSLB-aware)" — the authoritative design spec for the DNS probe. Covers the flag surface (`--target`/`--type`/`--server`/`--iterations`/`--gslb-compare`/`--require-divergence`), the `roksbnkctl.dns.v1` JSON output schema, server resolution semantics (`system`/`cluster`/named-from-config/literal), GSLB-divergence detection, RTT measurement via `miekg/dns`'s `Exchange()`.
- `docs/prd/05-E2E-TEST-PLAN.md` §"Phase L-DNS" — the e2e step list LD0-LD10 (validator implements; you only need to know what shape they exercise).
- `docs/PLAN.md` Sprint 5 section — confirms the two-week structure (week 1: DNS probe; week 2: terraform-docker + polish + doctor + docs) and the v0.9 gate criteria.
- Existing files:
  - `internal/test/dns.go` — current implementation uses `net.Resolver`; you replace this with a miekg-based `Probe` struct.
  - `internal/cli/test.go` — has the `dns` subcommand wired (`Use: "dns"`); extend with new flags.
  - `internal/exec/k8s.go` — Sprint 4's k8s backend with the long-lived ops-pod + one-shot Job paths. Sprint 5 adds a `dns-probe` Job mode that self-execs `roksbnkctl` in-cluster (no separate image).
  - `internal/exec/docker.go` — Sprint 3's docker backend; you extend the per-tool path so terraform routes through it.
  - `internal/cli/lifecycle.go` — `up/plan/apply/destroy` subcommands. You wire `--backend docker` here.
  - `internal/config/workspace.go` — workspace-config schema. Add `Test.DNS.Resolvers` map and `Test.DNS.DefaultTarget`.
  - `internal/cli/doctor.go` + `internal/cli/doctor_backend.go` — extend with DNS-probe + ops-pod-health checks.
  - `internal/version/` — the binary's version package; `toolImageTag()` in `internal/exec/docker.go` reads from here.
  - `internal/exec/k8s.go::runOnOpsPod` — Sprint 4 validator filed Issue 7 about argv[0] entrypoint double-up; the fix lands this sprint.
  - `internal/exec/ssh.go` — Sprint 4 validator filed Issue 3 about needing a mock surface for wrapper-script tests; the fix lands this sprint (extract interface from `*remote.Client` or add `SetSSHClientFactory` seam).
  - `internal/exec/k8s_test.go` — Sprint 4 tech-writer filed Issue 14 about test-name verbosity + missing PRD-04 docstrings; validator owns this carry-over, but you'll want to coordinate on whether seam changes affect the test surface.
- `prompts/sprint4/staff.md` for prompt-structure reference.
- `issues/resolved_sprint4_validator.md` + `resolved_sprint4_tech-writer.md` for the explicit Sprint 5 polish carry-overs.

## Coordinate with parallel agents

An architect agent is replacing/extending 3 testing chapters under `book/src/` (20 connectivity, 21 DNS-GSLB flagship, 22 throughput) and appending a terraform docker-backend subsection to chapter 17. A validator agent is adding miekg-based DNS probe unit tests (with a mocked DNS server via `miekg/dns`'s server library), integration tier against `8.8.8.8` + local stub, Phase L-DNS in `scripts/e2e-test-backends.sh`, terraform-via-docker tests, k8s_test.go test-name + comment polish (tech-writer Issue 14), and any CI workflow + cspell + CONTRIBUTING updates. **Do not touch their files.** You own production code only.

## Tasks (priority order — finish from the top down)

If you run out of token budget, stop at a priority boundary and file an issue describing what's deferred.

### Priority 1 — DNS probe (`miekg/dns`-based)

#### 1a. Add `github.com/miekg/dns` dep

Add to `go.mod` via `go get github.com/miekg/dns@<stable-tag>`. Pin to a stable release tag (latest as of 2026 is `v1.1.59`-ish; check). Update `go.sum` via the build.

#### 1b. `internal/test/dns.go` — replace `net.Resolver` impl with `Probe`

Replace the current implementation with a miekg-based `Probe` struct exposing the PRD 03 surface:

```go
package test

import (
    "context"
    "time"

    "github.com/miekg/dns"
)

// Probe is a single-vantage DNS probe.
type Probe struct {
    Target     string        // DNS name to query
    Type       uint16        // dns.Type* constants (A=1, AAAA=28, …)
    Server     string        // "<ip>:<port>" or "system" (resolve from /etc/resolv.conf)
    Iterations int           // number of repeated queries (default 1; RTT distribution if >1)
    Timeout    time.Duration // per-query timeout
}

type ProbeResult struct {
    Schema     string         `json:"schema"`     // "roksbnkctl.dns.v1.vantage"
    Backend    string         `json:"backend"`    // "local" | "k8s" | "ssh:<target>"
    Server     string         `json:"server"`
    Iterations int            `json:"iterations"`
    RTTMs      RTTDistribution `json:"rtt_ms"`
    Answers    []DNSAnswer    `json:"answers"`
    Rcode      string         `json:"rcode"`      // "NOERROR", "NXDOMAIN", "SERVFAIL", "TIMEOUT", ...
    Authoritative bool        `json:"authoritative"`
    Truncated     bool        `json:"truncated"`
    Err           string      `json:"error,omitempty"`
}

type RTTDistribution struct { P50, P95, P99 float64 `json:",inline"` }
type DNSAnswer       struct { Name, Type, RData string; TTL uint32 }

func (p *Probe) Run(ctx context.Context) (*ProbeResult, error) { … }
```

Server-resolution: `"system"` resolves to the host's `/etc/resolv.conf` (use `dns.ClientConfigFromFile("/etc/resolv.conf")` → first nameserver), `"cluster"` is k8s-backend-only (resolves to the pod's resolv.conf at run time; the local-backend implementation errors clearly), `"<ip>"` is used verbatim (default port 53 if no port), named-from-config is resolved by the CLI layer before calling Run.

RTT capture: each `dns.Client.ExchangeContext()` returns a `time.Duration` — accumulate across iterations and compute p50/p95/p99. For iterations=1, `p50 == p95 == p99 == the single RTT`.

#### 1c. `internal/cli/test.go::dnsCmd` — extend with new flags

Current `dnsCmd` (line 50-ish) is a simple wrapper over the current workspace-driven probe. Sprint 5 adds:

- `--target <name>` — the DNS name (overrides workspace config's `test.dns.default_target`)
- `--type <type>` — `A` (default), `AAAA`, `CNAME`, `MX`, `NS`, `TXT`, `SRV`, `SOA`, `PTR`, `CAA`, `DS`, `DNSKEY`, `ANY`. Map name → `miekg/dns` constant via `dns.StringToType`.
- `--server <value>` — literal `<ip>` / `<ip>:<port>` / `system` / `cluster` / named-resolver-from-workspace-config (`google` / `cloudflare` / etc.)
- `--iterations <N>` — default 1; >1 enables RTT distribution
- `--timeout <duration>` — per-query timeout (default 2s)
- `--gslb-compare` — multi-vantage mode; fans out to every configured backend (local + k8s if `roksbnkctl ops install` has run; + each `ssh:<target>` in workspace targets). Emits a single comparison JSON with `roksbnkctl.dns.v1` schema and `gslb_divergence` boolean.
- `--require-divergence` — flips the exit code when `--gslb-compare` finds **no** divergence (useful for CI assertions that GSLB is actually doing something).

The flag surface lives behind both a backwards-compatible path (no flags = today's extra_hosts probe) and the new explicit path. PRD 03 §"CLI surface" is the authoritative spec.

For the workspace-`extra_hosts` path (today's default `roksbnkctl test dns` with no flags), keep the existing behavior intact — it probes each `extra_hosts` entry. The new flag surface activates when **any** of `--target`/`--type`/`--server`/`--gslb-compare` is set.

#### 1d. `internal/exec/k8s.go` — add `dns-probe` Job mode

The dns-probe runs the `roksbnkctl` binary itself in-cluster (single binary; no separate image needed). The Job's pod runs `/usr/local/bin/roksbnkctl test dns --target <…> --type <…> --server <…> --iterations <…> -o json` and the Job's log streams the JSON output back to the caller.

The binary needs to be present in the ops pod's image (it is — the bundled tools image is `roksbnkctl-tools-ibmcloud` and we ship the binary alongside via the ops Pod's image). Alternative: use a `kubectl cp`-equivalent to project the local binary into the pod at exec time (more flexible; one less image to keep in sync).

Pick whichever approach is simpler given Sprint 4's k8s.go shape. Document the choice in code comments referencing PRD 03 §"K8s shape".

#### 1e. Workspace config — `test.dns.resolvers` + `test.dns.default_target`

Extend `internal/config/workspace.go::Workspace.Test.DNS`:

```go
type DNSConfig struct {
    Resolvers     map[string]string `yaml:"resolvers,omitempty"`     // name → "<ip>:<port>"
    DefaultTarget string            `yaml:"default_target,omitempty"`
}
```

PRD 03 §"Server resolution" §"workspace config" shape:

```yaml
test:
  dns:
    resolvers:
      google:     "8.8.8.8:53"
      cloudflare: "1.1.1.1:53"
      gslb-vip:   "169.45.91.5:53"
    default_target: "www.example.com"
```

### Priority 2 — Terraform docker backend

#### 2a. `internal/exec/docker.go::toolImages` — add terraform

Already has `terraform` entry? Verify; if not, add:

```go
"terraform": "hashicorp/terraform:1.5.7",   // or whichever upstream version we pin
```

The terraform image is the **exception** to the per-tool tag-version resolution (chapter 17 §`:dev` tag resolution documents this); the version is a literal pin against upstream. PRD 03 §"terraform" recommends this.

#### 2b. `internal/exec/docker.go::DockerBackend.Run` — terraform-specific bind-mounts

Generic `DockerBackend.Run` already handles the common case (kubeconfig, IBMCLOUD_API_KEY). For terraform, the additional requirement is bind-mounting the workspace's terraform state directory:

- Mount: `~/.roksbnkctl/<workspace>/state/` → `/state/` in the container, **read-write**
- Set the container's working directory to `/state/tf-source/embedded-terraform/` (where the embedded HCL lives — see chapter 13 + chapter 31)
- Pass `--user $(id -u):$(id -g)` so terraform writes the state file with the host user's UID/GID (Linux container runs as root by default → bind-mount permission collision)
- Honor `TF_VAR_*` env vars passed through `RunOpts.Env` (terraform reads them directly)

The shape can live as a per-tool branch inside `DockerBackend.Run`, or as a dedicated `buildTerraformMounts` helper that mirrors the existing `buildMountsAndEnv` shape for the ibmcloud + iperf3 paths. PRD 03 §"Docker container" specs the cred-pass-by-reference rules; same rules apply (no `--env IBMCLOUD_API_KEY=<value>`).

#### 2c. `internal/cli/lifecycle.go` — `--backend docker` for `up/plan/apply/destroy`

The existing `up/plan/apply/destroy` commands use `internal/tf` (terraform-exec on the host). Sprint 5 adds a `--backend` flag override that, when set to `docker`, dispatches through `internal/exec/docker.DockerBackend` with the terraform image instead of the host-local terraform-exec path.

The flag is the existing persistent `--backend` at root; the lifecycle commands honor it just like cluster/test do. When `--backend docker`, the wrapped command is:

```bash
docker run --rm -it \
  --user <uid>:<gid> \
  -v <host-workspace-state>:/state \
  -w /state/tf-source/embedded-terraform \
  -e TF_VAR_<...> \
  hashicorp/terraform:1.5.7 \
  init / plan / apply / destroy <flags...>
```

Defer `--backend k8s` and `--backend ssh` for terraform — state-handling design is open per PRD 03 §"State concerns"; PLAN.md Sprint 5 row 8 explicitly defers to v1.x. Error clearly if the user passes them ("terraform --backend k8s deferred to v1.x; see PRD 03 §State concerns").

### Priority 3 — Doctor extensions

Extend `internal/cli/doctor.go` + `doctor_backend.go`:

- **DNS probe check**: confirm `roksbnkctl test dns --target <workspace-default-target>` returns at least one answer; report the resolution latency. Mostly a no-op since `miekg/dns` is built into the binary (no external probe install required), but worth surfacing in `roksbnkctl doctor`'s output for completeness.
- **K8s ops-pod health**: when `--backend k8s` is in effect, deepen the existing Sprint 4 check to also verify the pod's env actually carries `IBMCLOUD_API_KEY` (via `kubectl exec -- printenv` against the pod, with the value redacted in output) and that the Secret's `roksbnkctl.io/rotated-at` annotation isn't more than 30 days old (best-practice rotation reminder, not a hard fail).

Don't add a check that would surprise users (e.g., one that opens an external network connection from `roksbnkctl doctor`'s no-args path); these checks live behind `--backend k8s` (already opt-in).

### Priority 4 — Sprint 4 polish carry-overs

#### 4a. K8s long-lived path argv-entrypoint fix (validator Issue 7)

Per `resolved_sprint4_validator.md` Issue 7: `K8sBackend.runOnOpsPod` passes argv to `PodExecOptions.Command` verbatim. The current ops pod image has `ibmcloud` as ENTRYPOINT; for ibmcloud passthrough `argv = ["ibmcloud", "iam", "oauth-tokens"]` becomes `ibmcloud ibmcloud iam oauth-tokens` (exec doesn't strip entrypoint). Today this works for ibmcloud (the duplicate `ibmcloud` is a no-op the parser ignores) but breaks for future tools landing on the ops pod.

Two options per the issue:
- (preferred) Switch to a no-entrypoint ops image (chapter 19 covers the multi-tool ops-pod approach; the image's `ENTRYPOINT` becomes `["sleep", "infinity"]` already set at the Pod level via `command:`, leaving the container without an exec-time entrypoint)
- (alternative) Strip argv[0] in `runOnOpsPod` when it matches a known per-image entrypoint (brittle; tied to image internals)

Implement option (a) — the ops image's `ENTRYPOINT` directive in `tools/docker/ibmcloud/Dockerfile` becomes a no-op for the ops pod path (the pod overrides `command:` anyway), so future tool-name argv flows through verbatim. The docker backend's own path (which uses the same image but runs it directly without `command:` override) keeps the ENTRYPOINT semantics for backward compatibility.

Actually the cleanest fix is to **drop the ENTRYPOINT from `tools/docker/ibmcloud/Dockerfile`** and have the docker backend's per-tool path prepend the tool binary name explicitly. The ops pod path benefits because argv is now flowing through verbatim. The docker backend path is unchanged user-visibly (still runs `docker run ... <tool> <args>`).

(If you find the entrypoint-drop too invasive at the v0.9-cut moment, document the deferral and ship the argv[0]-strip in `runOnOpsPod` as the interim fix. Either is acceptable; file an issue documenting the choice.)

The `tools/docker/ibmcloud/Dockerfile` edit is outside your normal scope per Sprint 4 prompt rules; coordinate with the validator (they own `tools/docker/`) or file the Dockerfile change as an issue for the integrator.

#### 4b. SSH wrapper-script test seam (validator Issue 3)

Per `resolved_sprint4_validator.md` Issue 3: `internal/exec/ssh.go` calls a concrete `*remote.Client`; mocking wrapper-script content + bootstrap-failure tests requires either:
- (a) Extracting an interface from `*remote.Client`'s public methods (Run + Close + Shell + the SetEnv-via-RunOpts shape), OR
- (b) Adding a `SetSSHClientFactory` package-level seam analogous to `SetSSHTargetResolver` (Sprint 4's pattern)

Pick option (b) for symmetry with the existing Sprint 4 seam:

```go
// SetSSHClientFactory swaps the remote.Client constructor used by
// SSHBackend.Run. Tests set this to inject a mock client surface
// that captures wrapper-script content + simulates bootstrap-failure
// modes. Defaults to remote.Connect at production.
func SetSSHClientFactory(fn func(ctx context.Context, target *remote.Target) (remoteClient, error))
```

Define the `remoteClient` interface as the minimum subset SSHBackend uses (Run + Close + SetEnv-via-RunOpts; verify against the actual call sites). Validator's tests then exercise the wrapper-script content + sudo failure / non-Ubuntu / repo-unreachable matrix that's currently deferred at the unit tier.

#### 4c. Optional: publish `:dev` on `main` (validator-territory polish from staff Issue 2)

Staff Issue 2 from Sprint 4 noted the optional follow-up — having `.github/workflows/tools-images.yml` also push `:dev` on `main` pushes so `go install ./cmd/roksbnkctl@main` works without a local docker build. The change is one block in the workflow file (validator territory; coordinate). Don't touch the workflow yourself; mention it to the validator agent if needed.

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean (validator's new DNS unit tests pass; your code shouldn't break them)
- `go vet ./...` clean
- `gofmt -d -l .` clean
- `roksbnkctl test dns --target www.cloudflare.com --type A --server 8.8.8.8` works against a real DNS server (note in issue file what you ran against)
- `roksbnkctl test dns --target www.cloudflare.com --type A --server 8.8.8.8 --iterations 10 -o json` produces JSON matching the `roksbnkctl.dns.v1.vantage` schema with populated `rtt_ms.p50/p95/p99`
- `roksbnkctl test dns --gslb-compare ...` runs multi-vantage (against a kind cluster if available); emits `gslb_divergence` boolean
- `roksbnkctl test dns --backend docker` errors with the spec-required "DNS probe doesn't benefit from docker; use local instead"
- `roksbnkctl up --backend docker` runs `terraform init/plan/apply` against the bundled HCL (against a real workspace if locally available; otherwise document in the issue file what the integrator should validate)
- `roksbnkctl ibmcloud --backend k8s iam oauth-tokens` still works post-entrypoint-fix (regression check from Sprint 4)
- `roksbnkctl doctor --backend k8s` reports the cred rotation freshness
- Sprint 4's `--backend docker` regression check still passes (ibmcloud docker path didn't break)

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint5_staff.md`. Same format as Sprint 4.

## Final report (under 200 words)

- Files created
- Files edited
- Build / test / vet / gofmt status
- Which priority items completed; which deferred
- Issues filed
- Anything the integrator should know (especially regarding the terraform UID/GID gotcha — what env you tested on, what didn't work — and the entrypoint-drop choice in 4a)

Do NOT commit. The integrator commits the aggregated work.
