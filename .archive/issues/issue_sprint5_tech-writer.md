# Sprint 5 — tech writer issues

Findings cover the three new chapters (20 connectivity, 21 DNS/GSLB,
22 throughput), chapter 17's terraform-via-docker subsection, the
`internal/test/dns.go` + `internal/cli/test.go` + `internal/cli/lifecycle.go`
+ `internal/exec/docker.go` surfaces the prose claims against, the
v0.9 release-readiness surface (README highlight, CHANGELOG, PLAN.md
milestone), and PRD/PLAN drift. All findings are doc-only; no code
changes proposed.

## Issue 1 (BLOCKER for v0.9 release): README has no Sprint 5 / DNS-probe / terraform-docker highlight bullet

**Severity**: high (v0.9 release surface)
**Status**: open
**Description**: Sprint 1 (`--on jumphost`), Sprint 2 (k-verbs), Sprint 3
(`--backend docker`), and Sprint 4 (`--backend k8s` + `--backend ssh`)
each landed a top-level bullet in `README.md` §"Highlights". Sprint 5 is
the v0.9 release-gate sprint and ships two user-visible features
(GSLB-aware DNS probe; terraform-via-docker) plus the testing-suite
chapters that anchor the v0.9 docs. README has nothing reflecting any
of this — the last highlight bullet is the Sprint 4 `--backend k8s` +
`--backend ssh` line at `README.md:38`.

A reader landing on the README on tag-v0.9 day learns nothing about
the DNS probe, terraform-via-docker, or the new testing chapters. The
"Status" line at `README.md:7` still reads "Pre-release. Source
compiles, unit tests pass, every PRD verb is implemented." — accurate
but uninformative for v0.9.

**Files affected**: `README.md` §"Highlights" (lines 26-38)

**Proposed fix**: add a Sprint 5 / v0.9 highlight bullet matching the
Sprint 1-4 cadence. Suggested wording:

> - **GSLB-aware DNS probe + `terraform --backend docker` (v0.9)** —
>   `roksbnkctl test dns --gslb-compare` fans the probe across local +
>   in-cluster + SSH-target vantages and emits a `gslb_divergence`
>   boolean for CI assertions; `--require-divergence` is the
>   exit-code-keyed form. `roksbnkctl up/plan/apply/destroy --backend
>   docker` runs terraform inside `hashicorp/terraform:1.5.7` with the
>   workspace's state directory bind-mounted, host-user-owned. See
>   [chapter 21] for DNS probe + GSLB scenarios, [chapter 22] for
>   throughput, and [chapter 17] §"terraform via docker" for the
>   terraform-docker mechanics.

The "Status" line should also flip from "Pre-release" to "v0.9 release
candidate" (or similar) once the integrator tags. No content change to
the rest of the README.

## Issue 2 (BLOCKER for v0.9 release): no CHANGELOG / RELEASE-NOTES file

**Severity**: high (v0.9 release surface)
**Status**: open
**Description**: There is no `CHANGELOG.md`, `CHANGES.md`, or
`docs/RELEASE-NOTES.md` in the repo (verified via `find -name 'CHANGELOG*'`
+ `find -name 'RELEASE*'` — both empty). The v0.9 release will be the
first "real" tag (per `README.md:7` "No tagged release yet"), and a
release without release notes is the kind of thing that makes
downstream consumers ask "what changed in v0.9 vs v0.8?".

PLAN.md §"v0.9 (M3)" (lines 627-633) lists the cumulative v0.9 surface
across Sprints 3-5 (cred abstraction, four backends, DNS probe,
terraform-docker, in-cluster ops pod, ~24 book chapters). That bullet
list is the natural seed for `CHANGELOG.md`'s v0.9 section.

The release engineer can land this in 30 minutes by copying PLAN.md
§"v0.9 (M3)" into a new `CHANGELOG.md` plus splitting Sprint 3 vs
Sprint 4 vs Sprint 5 contributions per the existing PLAN.md sprint
sections.

**Files affected**: repo root (new file `CHANGELOG.md`) — or
`docs/CHANGELOG.md` if the team prefers to keep it under docs/.

**Proposed fix**: create `CHANGELOG.md` with at minimum a v0.9 section
covering:

- Sprint 3: cred abstraction (`Backend.Run` interface; redactor;
  `IBMCloudAPIKey`-by-reference plumbing; `--backend local` +
  `--backend docker`).
- Sprint 4: `--backend k8s` (long-lived ops pod + one-shot Job split);
  `--backend ssh:<target>` (file materialisation, env propagation,
  `--bootstrap`); ops-pod lifecycle (`roksbnkctl ops install/show/uninstall`);
  iperf3 SCC compliance (`runAsNonRoot`, `runAsUser: 1000`).
- Sprint 5: GSLB-aware DNS probe (`miekg/dns`-based; `--target` /
  `--type` / `--server` / `--iterations` / `--gslb-compare` /
  `--require-divergence`; `roksbnkctl.dns.v1` + `roksbnkctl.dns.v1.vantage`
  schemas); terraform via docker (`up` / `plan` / `apply` / `destroy`
  `--backend docker`; `hashicorp/terraform:1.5.7` pin; UID/GID host-user
  alignment); doctor extensions (DNS-probe sanity, ops-pod env runtime
  probe, cred rotation freshness); book chapters 20 / 21 / 22.

The "What's deliberately deferred to post-v1.0" list at PLAN.md:650-672
is the second half — those bullets become a "v1.x" / "Unreleased"
section in CHANGELOG.md so future contributors don't re-propose them
as v0.9 work.

## Issue 3: chapter 21 single-vantage JSON example shows the wrong schema string AND wrong shape

**Severity**: high (load-bearing — this is the v0.9 schema spec from
the user's perspective)
**Status**: open
**Description**: Chapter 21 has two JSON output examples — single-vantage
(lines 187-215) and multi-vantage (lines 219-256). Both use schema
string `roksbnkctl.dns.v1` and both wrap the result in a `vantages[]`
array with `target`, `type`, `gslb_divergence` at the top level.

The actual implementation has **two distinct schemas**:

- `internal/test/dns.go:19` — `DNSSchemaVersion = "roksbnkctl.dns.v1"`
  (the multi-vantage `DNSCompareResult` shape only)
- `internal/test/dns.go:20` — `DNSVantageSchemaVersion = "roksbnkctl.dns.v1.vantage"`
  (the single-vantage `DNSProbeResult` shape only)

`runDNSSingleVantage` (`internal/cli/test.go:268-289`) emits a
`*test.DNSProbeResult` directly to stdout via `test.WriteJSON(os.Stdout, res)`.
The result document is **not** wrapped in `vantages[]`, has **no**
top-level `target` / `type` / `gslb_divergence` fields, and the schema
string is `roksbnkctl.dns.v1.vantage` not `roksbnkctl.dns.v1`. Looking
at the struct (`internal/test/dns.go:57-68`), the actual single-vantage
JSON is:

```json
{
  "schema": "roksbnkctl.dns.v1.vantage",
  "backend": "local",
  "server": "8.8.8.8:53",
  "iterations": 10,
  "rtt_ms": { "p50": 12.4, "p95": 18.1, "p99": 22.7 },
  "answers": [
    { "name": "www.cloudflare.com.", "type": "A", "ttl": 60, "rdata": "104.16.132.229" }
  ],
  "rcode": "NOERROR",
  "authoritative": false,
  "truncated": false
}
```

Chapter 21's claim at line 183-184:

> The schema is `roksbnkctl.dns.v1`, defined verbatim in PRD 03 §"DNS
> probe". Single-vantage and multi-vantage runs share the schema —
> multi-vantage adds entries to `vantages[]` and populates
> `gslb_divergence`.

…is wrong on three counts: (1) single-vantage's schema string is
`.vantage`, not just `.v1`; (2) the two shapes don't share the schema —
they are two distinct JSON documents; (3) single-vantage doesn't have
`vantages[]` at all (it IS the vantage entry).

The Sprint 5 architect's resolved\_architect.md Issue 1 spot-check
("`internal/test/dns.go:14-19` — `DNSSchemaVersion = roksbnkctl.dns.v1`
+ `DNSVantageSchemaVersion = roksbnkctl.dns.v1.vantage`. Chapter 21
references both verbatim.") reads the constants correctly but missed
that the chapter's example uses only one of them and uses the wrong
shape.

**Files affected**: `book/src/21-dns-testing-gslb.md` §"Single-vantage
output" (lines 185-215); §"JSON output schema" prose (lines 181-184);
§"Schema field reference" table (lines 258-278); §"`-o json`" row of
the flag table (line 73).

**Proposed fix**:
1. Replace the single-vantage JSON example at lines 192-214 with the
   actual `DNSProbeResult` shape (no `vantages[]` wrap; schema
   `roksbnkctl.dns.v1.vantage`; top-level `backend`, `server`,
   `iterations`, `rtt_ms`, `answers`, `rcode`, `authoritative`,
   `truncated`).
2. Update the prose at lines 183-184 to acknowledge **two** schemas:
   single-vantage (`roksbnkctl.dns.v1.vantage`) and comparison
   (`roksbnkctl.dns.v1`). The flag-table row at line 73 should read
   "Switch from human-readable text on stderr to JSON on stdout
   (`roksbnkctl.dns.v1.vantage` for single-vantage, `roksbnkctl.dns.v1`
   for `--gslb-compare`)".
3. Split the Schema field reference table (lines 258-278) into two
   sub-tables — "Per-vantage shape" (the `DNSProbeResult` fields) and
   "Comparison shape" (the `DNSCompareResult` fields, which embeds
   `vantages[]` + `gslb_divergence` + `gslb_divergence_summary`). This
   matches what a JSON consumer needs to write a parser for.

## Issue 4: chapter 21 documents `edns_client_subnet` field that the implementation doesn't emit

**Severity**: medium
**Status**: open
**Description**: Chapter 21's "Schema field reference" table line 274
documents:

> `vantages[].edns_client_subnet` | object \| null | If the response
> carries an EDNS Client Subnet option, the subnet the resolver
> claimed; otherwise `null`.

The actual `DNSProbeResult` struct at `internal/test/dns.go:57-68` has
no `EDNSClientSubnet` field. `grep -n edns_client_subnet
internal/test/dns.go` returns 0 hits. The probe never emits this field
in JSON output.

The field is in PRD 03's spec
(`docs/prd/03-EXECUTION-BACKENDS.md:335`) — that's the source the
chapter copied from. But staff didn't implement it.

A CI script that pins `jq -e '.vantages[0].edns_client_subnet | not'`
expecting the documented shape will get a `null` from the missing
field (jq returns `null` for missing keys); that happens to work for
the negative assertion. But a CI script that asserts the field's
*presence* (e.g., to prove the schema's stable for v0.9) will fail.

**Files affected**: `book/src/21-dns-testing-gslb.md` line 274;
secondarily, the JSON example at line 211 (which emits
`"edns_client_subnet": null` — accurate as a JSON shape but the actual
binary doesn't emit it at all, so the example over-specifies).

**Proposed fix**: either (a) drop the `edns_client_subnet` row from the
table + drop the `"edns_client_subnet": null` line from the
single-vantage JSON example and call out the field as
"reserved for v1.x — see PRD 03 §"DNS probe" §"Future"", or (b) file a
follow-up staff issue to land the field in the `DNSProbeResult` struct
(easy: add `EDNSClientSubnet *EDNSClientSubnet json:"edns_client_subnet"`
to the struct; populate from `dns.Msg.IsEdns0()` if present). PRD 03
already specs it; if staff lands it as a v0.9 polish, the chapter
becomes accurate again. Otherwise option (a) is the
true-against-the-binary fix.

## Issue 5: chapter 21 says vantages run in parallel; the implementation runs them serially

**Severity**: medium
**Status**: open
**Description**: Chapter 21 §"The `--gslb-compare` workflow" line 174:

> 2. Each vantage runs the probe in parallel. The query (target, type,
>    server) is identical; only the backend differs.

The actual `runDNSGSLBCompare` (`internal/cli/test.go:298-366`) iterates
over the spec list **serially** in a for loop:

```go
for _, spec := range specs {
    res, err := dispatchDNSProbe(ctx, cctx, spec, ...)
    ...
    vantages = append(vantages, *res)
}
```

There's no goroutine, no `sync.WaitGroup`, no `errgroup.Group`. Each
vantage waits for the previous one to complete. With three vantages
(local + k8s + ssh:target) and the per-query timeout of 2s, a
worst-case all-timeout run takes 6s, not 2s.

This is a correctness issue for the chapter (the parallel claim is
wrong) and a UX implication users should know about — a slow ssh:target
vantage stretches the whole run.

**Files affected**: `book/src/21-dns-testing-gslb.md` line 174

**Proposed fix**: change "in parallel" to "in sequence (one vantage at
a time; the run completes when the slowest vantage returns)". If the
team prefers parallel, that's a small staff follow-up using
`golang.org/x/sync/errgroup` — but the chapter must reflect what the
binary does today.

## Issue 6: chapter 17 §"terraform via docker" §"State persistence via bind-mount" hard-codes the wrong container path

**Severity**: medium
**Status**: open
**Description**: Chapter 17 §"State persistence via bind-mount" (lines
233-252) shows the bind-mount as:

```
docker run --rm \
  -v ~/.roksbnkctl/<workspace>/state:/work \
  --workdir /work \
  --user $(id -u):$(id -g) \
  hashicorp/terraform:1.5.7 \
  apply -auto-approve
```

…and prose at lines 248-249:

> - **Container target**: `/work` — set as `WorkingDir` so terraform
>   commands run from there without an explicit `cd`.

The actual implementation in `internal/cli/lifecycle.go:667-705`
mounts the state dir at **`/state`** (not `/work`):

```go
hostMounts := []execbackend.HostMount{{
    HostPath:      stateDir,
    ContainerPath: "/state",        // ← /state, not /work
    ReadOnly:      false,
}}
```

…and sets `WorkDir` to `/state/<srcRel>` (e.g.,
`/state/tf-source/embedded-terraform`), not `/work`:

```go
containerSrcDir := filepath.ToSlash(filepath.Join("/state", srcRel))
...
WorkDir:     containerSrcDir,
```

The integration test at `internal/exec/docker_terraform_integration_test.go:99-117`
also explicitly mirrors `ContainerPath: "/state"` with the comment "Mirror
what internal/cli/lifecycle.go's terraform docker dispatch builds".

The follow-on §"The UID/GID alignment gotcha" code block (lines 264-271)
reuses the same `/work` path and so is also wrong.

Additionally, chapter 17 line 250 claims:

> The HCL itself is **not** bind-mounted from the workspace's state
> dir. The HCL ships embedded in the `roksbnkctl` binary…

The actual HCL **is** in the bind-mount — the embedded source is
materialised into `<stateDir>/tf-source/embedded-terraform/` (which
becomes `/state/tf-source/embedded-terraform/` in the container, hence
the `WorkDir` value). The chapter's "HCL is not bind-mounted" claim is
the opposite of what the code does.

**Files affected**: `book/src/17-execution-backends.md` §"terraform via
docker" §"State persistence via bind-mount" (lines 233-252) +
§"The UID/GID alignment gotcha" §" example block" (lines 264-272)

**Proposed fix**: replace `/work` with `/state` everywhere in the
chapter's terraform-via-docker section; replace `--workdir /work` with
`--workdir /state/tf-source/embedded-terraform` (or note that WorkDir
points at the materialised source dir under the bind-mount). Rewrite
the "HCL is not bind-mounted" prose to acknowledge that the embedded
HCL is materialised under `<stateDir>/tf-source/<source-name>/` per
chapter 31 §"embedded-terraform layout" (cross-link), and the
bind-mount carries both state and HCL.

## Issue 7: chapter 21 §"`--gslb-compare` workflow" misframes the k8s vantage prereq

**Severity**: low
**Status**: open
**Description**: Chapter 21 line 173:

> 1. The runner enumerates configured vantages. By default that's
>    `local` plus `k8s` (when an ops-installed cluster is reachable)
>    plus any `ssh:<target>` entries the workspace config marks as a
>    probe vantage.

Two inaccuracies:

1. **k8s vantage prereq**: the actual check at
   `internal/cli/test.go:303` is `if k8s.DefaultKubeconfigPath() != ""
   { specs = append(specs, "k8s") }` — i.e., presence of a kubeconfig
   on disk, NOT whether the ops pod is installed. The DNS probe runs
   as a one-shot Job, not via the ops pod (Job mode reuses the bundled
   tools image with `jobToolCmdOverride` to bypass the ENTRYPOINT).
   The chapter's "ops-installed cluster" framing implies a wider
   prereq than the binary actually checks.
2. **ssh-vantage selection**: the actual loop at
   `internal/cli/test.go:307-325` includes **every** `cctx.Workspace.Targets`
   entry, not just those marked as a probe vantage. There is no
   "marked as a probe vantage" config field on `Targets` — every
   registered target gets fanned out.

**Files affected**: `book/src/21-dns-testing-gslb.md` §"The
`--gslb-compare` workflow" lines 172-174

**Proposed fix**: rewrite line 173 to: "1. The runner enumerates
configured vantages: `local` always; `k8s` when a kubeconfig is
reachable on the host (the probe runs as a one-shot Job — the ops pod
isn't required); plus every entry in the workspace's `targets:` block,
each as `ssh:<name>`." This matches the implementation and is a tighter
contract for the user.

## Issue 8: chapter 21 §"Backend selection" overstates the k8s Job's image equivalence

**Severity**: low
**Status**: open
**Description**: Chapter 21 line 150:

> **`--backend k8s`**: a one-shot Job in `roksbnkctl-test`. The pod's
> image is the `roksbnkctl` binary itself (the same `:dev` /
> `:v0.9.x` tag the Docker backend would pull); the Job's command is
> `roksbnkctl test dns --target ... --type ... --server ... --backend
> local -o json`, and the Job's stdout is collected via the k8s
> backend's log-stream path. The vantage is the cluster's egress IP.

The "Job's image is the `roksbnkctl` binary itself" framing is correct
in spirit but slightly misleading. Looking at
`internal/exec/docker.go:67-68`:

```go
"roksbnkctl": "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:" + tag,
```

The `roksbnkctl` tool name aliases the **bundled `ibmcloud` tools
image**. That image must carry both `ibmcloud` AND `roksbnkctl`
binaries on PATH — that's a tools/docker/ibmcloud/Dockerfile contract
(the Sprint 5 staff's notes about `jobToolCmdOverride` confirm this).
The chapter says "the same `:dev` / `:v0.9.x` tag the Docker backend
would pull" — accurate — but doesn't tell the reader that the image
they need on the cluster is the **ibmcloud tools image** (not a
separate `roksbnkctl` image). For a reader debugging a Job that fails
to pull, the ImagePullBackOff diagnostic will name the
`roksbnkctl-tools-ibmcloud:<tag>` image, and the chapter's framing
sends them looking for a `roksbnkctl-cli:<tag>` image that doesn't
exist.

**Files affected**: `book/src/21-dns-testing-gslb.md` §"Backend
selection for the probe" line 150

**Proposed fix**: change to: "**`--backend k8s`**: a one-shot Job in
`roksbnkctl-test`. The Job's pod runs the bundled tools image
(`ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>` — the same image
the in-cluster ops pod uses; it carries both `ibmcloud` and
`roksbnkctl` on PATH). The Job's command is `roksbnkctl test dns
--target ... --backend local -o json`; the stdout is collected via the
k8s backend's log-stream path. Vantage is the cluster's egress IP."

## Issue 9: chapter 22 says iperf3 server is in `roksbnkctl-test` namespace; cross-reference to chapter 17 says LoadBalancer always — chapter 17 contradicts chapter 22 on Service type

**Severity**: low
**Status**: open
**Description**: Chapter 22 §"What the suite measures" line 11 says:

> The **server** runs in the cluster — a single bare Pod plus a
> Service, deployed in the `roksbnkctl-test` namespace. Service type
> is `ClusterIP` for east-west, `LoadBalancer` for north-south.

Chapter 17 (per Sprint 4 tech-writer Issue 3 — still unfixed in the
file as of Sprint 5 review) says at lines 322-330:

> The `iperf3` test deploys a **server** Deployment + LoadBalancer
> Service into `roksbnkctl-test`…

The Sprint 4 issue 3 was filed and accepted but the chapter 17 prose
wasn't updated in Sprint 4 or Sprint 5. Chapter 22 has the right shape
(bare Pod + dynamic Service type); chapter 17 still claims Deployment
+ LoadBalancer always. The two chapters disagree, and chapter 22's
cross-link to chapter 17's "iperf3 server side" section sends the
reader to a stale section.

This is a Sprint 4 carry-over rather than new Sprint 5 drift, but
chapter 22 is new this sprint and now actively contradicts chapter
17, so the lower-effort fix is to update chapter 17 to match chapter
22's accurate description.

**Files affected**: `book/src/17-execution-backends.md` §"iperf3 server
side" (lines 322-330) — this is the same section flagged as Sprint 4
tech-writer Issue 3 (status: still open).

**Proposed fix**: same as Sprint 4 issue 3 — replace "Deployment +
LoadBalancer Service" with "bare Pod + Service (ClusterIP for
east-west, LoadBalancer for north-south)" and rename the resource to
`roksbnkctl-iperf3` (not `roksbnkctl-iperf3-server`).

## Issue 10: PRD 03 §"DNS probe" §"Default backends" says "default both vantages always"; the implementation defaults to local single-vantage

**Severity**: low
**Status**: open
**Description**: PRD 03 line 255:

> **Default backends** | `local` **and** `k8s` (run both, surface both
> answers — see GSLB note)

The actual implementation defaults to `local` only when `--backend`
isn't passed (`internal/cli/cluster.go:338-342` — `perToolDefaultBackend`
has no `dns` row, so it falls through to the `"local"` default at
`resolveBackendSpecWith` line 367). Multi-vantage fan-out is opt-in
via `--gslb-compare`.

Chapter 17's per-tool defaults table (`book/src/17-execution-backends.md:87`)
documents this correctly:

> | `dns` | `local` | `local`, `k8s`, `ssh:<target>` | Single-vantage
> by default; `--gslb-compare` fans out across configured vantages for
> GSLB validation. |

Chapter 21 §"Backend selection" line 154 also documents this correctly:

> The default backend per `roksbnkctl` invocation (when `--backend` is
> omitted and there's no `exec.dns.backend` in workspace config) is
> `local`. To run GSLB cross-vantage you generally pass
> `--gslb-compare`, which fans out instead of picking a single
> vantage.

So the chapters are internally consistent and accurate against the
binary. The drift is in PRD 03 — its "Default backends" row is
aspirational and doesn't match what shipped. The Sprint 5 architect's
resolved\_architect.md Issue 7 already calls this out:

> PRD 03 §"DNS probe" §"Default backends" said "default both vantages
> always", but PLAN.md and the staff implementation chose the more
> conservative "opt-in fan-out" — flagging the PRD as slightly
> aspirational; we'll refresh PRD 03 to match in a follow-up.

**Files affected**: `docs/prd/03-EXECUTION-BACKENDS.md` line 255

**Proposed fix**: update PRD 03's "Default backends" row to
`local` (single-vantage; multi-vantage opt-in via `--gslb-compare`)
to match what shipped. Or add a note "v0.9 ships single-vantage default
+ opt-in `--gslb-compare`; multi-vantage default is a v1.x
consideration if user feedback shows it's wanted."

## Issue 11: PLAN.md §"What's deliberately deferred to post-v1.0" still lists `--require-divergence` as deferred to v1.1

**Severity**: low
**Status**: open
**Description**: PLAN.md line 663:

> - DNS probe `--require-divergence` CI assertion mode (v1.1)

Staff actually landed `--require-divergence` this sprint at
`internal/cli/test.go:129`:

```go
testDNSCmd.Flags().BoolVar(&flagDNSRequireDivergence, "require-divergence", false, "with --gslb-compare: exit non-zero if NO divergence is observed (CI assertion that GSLB is doing something)")
```

The behaviour is wired in `runDNSGSLBCompare` lines 356-359 (non-zero
exit when `flagDNSRequireDivergence && !cmp.GSLBDivergence`). Chapter
21 §"Asserting divergence in CI" (lines 326-336) documents both the
flag and the `jq -e` form correctly per the integrator's prose update
this pass.

PLAN.md's deferred-list bullet is now stale.

**Files affected**: `docs/PLAN.md` line 663

**Proposed fix**: delete the bullet from "What's deliberately deferred
to post-v1.0" and add a one-line acknowledgement under PLAN.md §"v0.9
(M3)" (around line 631-633) that `--require-divergence` landed —
something like "DNS probe `--require-divergence` CI assertion flag (in
addition to the `jq -e` user-side equivalent)".

## Issue 12: stale `sshseam` build-tag comment in `internal/exec/ssh_wrapper_test.go`

**Severity**: low (test-file readability)
**Status**: open
**Description**: `internal/exec/ssh_wrapper_test.go` opens with a
docstring comment block (lines 30-36):

```go
// Build tag: gated behind `sshseam` until staff lands the
// SetSSHClientFactory seam (Sprint 4 validator Issue 3 carry-over).
// The integrator drops the tag once the seam lands.
//
// Run with:
//
//	go test -tags sshseam -run SSHWrapper ./internal/exec/...
```

…but the build tag was dropped during Sprint 5 (per validator's Issue
5 self-resolved note + verified — the file has no `//go:build` or
`// +build` directive on lines 1-3). The test runs in the default `go
test ./...` suite now.

A future contributor reading the file's docstring will be confused
about why the documented build tag isn't there. The "Run with" line
also points at a `-tags sshseam` invocation that no longer matches the
test setup.

**Files affected**: `internal/exec/ssh_wrapper_test.go` lines 30-36

**Proposed fix**: drop the "Build tag: gated behind `sshseam`…" prose;
update the "Run with" line to plain `go test -run SSHWrapper
./internal/exec/...`. Mention in passing that the test was previously
behind the seam tag, in case `git blame` or the carry-over note is
cited elsewhere.

## Issue 13: `cspell.json` has duplicate `miekg` entry

**Severity**: low
**Status**: open
**Description**: `cspell.json` words list has `miekg` listed twice
(verified via `grep -n miekg cspell.json` — two distinct lines, both
in the user's word list array). Duplicates don't break cspell but
they make the diff harder to read on future word-list changes.

**Files affected**: `cspell.json`

**Proposed fix**: remove one of the two `miekg` entries.

## Issue 14: chapter 22's iperf3 server pod default image conflict — bundled image required by SCC, but config default is `networkstatic/iperf3:latest`

**Severity**: low (already mostly addressed in chapter 22 prose; one
sharper one-line fix possible)
**Status**: open
**Description**: Chapter 22 §"The bundled image and the `runAsNonRoot`
constraint" (lines 82-117) correctly documents the conflict:
`Iperf3DefaultImage = "networkstatic/iperf3:latest"` (per
`internal/k8s/iperf3.go:22`) runs as root and fails OpenShift
`restricted-v2` admission; the bundled
`ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>` image runs as UID 1000
and passes admission.

The chapter prose at line 105-114 walks the user through setting
`test.throughput.image` in workspace config to switch to the bundled
image. That's the right guidance.

What's missing: the `roksbnkctl init --auto` flow doesn't seed the
workspace config with the bundled image as the default —
`internal/config/workspace.go:124-129` defines `ThroughputCfg.Image`
as a string with no init-time default, and `iperf3.DeployIperf3` falls
back to `networkstatic/iperf3:latest` when the field is empty (per
`iperf3.go:42-46`). So the **out-of-the-box** behaviour on OpenShift
is "the iperf3 fixture fails admission" until the user reads chapter
22 and edits the config.

For v0.9 release polish, either (a) staff lands a one-line change to
flip `Iperf3DefaultImage` to the bundled image (which is what every
real user wants on OpenShift, and is what the chapter recommends), or
(b) chapter 22 surfaces the workaround more prominently in the
"Troubleshooting" / first-encounter prose so a user who runs
`roksbnkctl test throughput` against a fresh ROKS cluster doesn't
hit the SCC error before reading the bundled-image section.

**Files affected**: `book/src/22-throughput-testing.md` §"The bundled
image and the `runAsNonRoot` constraint" (the lead-in could move
earlier in the chapter, before "Reading the output", so it's
encountered before the user runs the command)

**Proposed fix**: low-effort doc fix — move the bundled-image
explanation earlier (after §"The two modes", before §"Reading the
output") so a user reading the chapter top-to-bottom hits the SCC
gotcha before being shown sample output. Higher-effort: file with
staff to flip `Iperf3DefaultImage` so the out-of-box behaviour is
admission-clean. Both are reasonable; the doc move is the v0.9
non-blocker.

## Issue 15: chapter 21's record-type list at line 68 + line 124 doesn't reflect what `dns.StringToType` actually accepts

**Severity**: low
**Status**: open
**Description**: Chapter 21 has two places that enumerate the
supported record types:

1. Line 68 (flag table `--type` row): "`A`, `AAAA`, `CNAME`, `MX`,
   `NS`, `TXT`, `SRV`, `SOA`, `PTR`, `CAA`, `DS`, `DNSKEY`, `ANY`, etc."
2. Line 124 (table prose row): same list with "etc."

The actual `ParseDNSType` (`internal/test/dns.go:323-332`) accepts
**any name in `dns.StringToType`** — that's the full miekg/dns table,
which includes (beyond what the chapter lists): `URI`, `SVCB`, `HTTPS`,
`SSHFP`, `TLSA`, `OPENPGPKEY`, `LOC`, `NAPTR`, `RP`, `AFSDB`, `HINFO`,
`MINFO`, `RRSIG`, `NSEC`, `NSEC3`, `NSEC3PARAM`, etc. The "etc." in
the chapter's list is intentional but vague — a user wondering "does
`--type HTTPS` work?" has no source-of-truth answer.

The unit test at `internal/test/dns_test.go:141-187` covers 10 of the
chapter's 13 listed types (no `DS`, `DNSKEY`, `ANY` test cases). The
absence of `DS`/`DNSKEY` in the test isn't a documentation issue but
worth noting for the validator follow-up; the underlying parser
accepts them.

**Files affected**: `book/src/21-dns-testing-gslb.md` lines 68 and 124

**Proposed fix**: replace the explicit list with the canonical source:
"any record type miekg/dns's `dns.StringToType` table accepts — the
PRD 03 §"DNS probe" calls out the common subset (A, AAAA, CNAME, MX,
NS, TXT, SRV, SOA, PTR, CAA, DS, DNSKEY, ANY); see [the miekg/dns
types.go upstream] for the full list including HTTPS, SVCB, TLSA, etc."
With a stable upstream link that names the file. This is more honest
than "etc." and makes the contract precise.

## Verification gates

- `go build ./...` ✓ (verified compiles)
- Chapter 20/21/22 `*Coming in Sprint 5*` markers: ✓ none (grep
  returns zero hits in chapters 20/21/22; chapters 23, 25, 26, 27, 28,
  29, 30, 31, 32 still have `*Coming in Sprint 6*` per the explicit
  forward-reference convention)
- Chapter 17 has terraform-via-docker subsection at line 229 ✓
- Chapter 21 documents both `--require-divergence` and `jq -e` form
  per Sprint 5 integrator update ✓ (lines 326-336)
- All `roksbnkctl test dns` flag names match `internal/cli/test.go`
  lines 123-129 ✓ (`--target` `--type` `--server` `--iterations`
  `--timeout` `--gslb-compare` `--require-divergence`)
- All `roksbnkctl up/plan/apply/destroy --backend docker` plumb
  through `internal/cli/lifecycle.go::runTerraformLifecycleDocker` ✓
- `book/src/SUMMARY.md` chapter titles match h1 of each chapter file
  ✓ (chapters 20 / 21 / 22 — exact match)
- Cross-references resolve (sampled): chapters 12 / 17 / 18 / 20 / 21
  / 22 / 26 anchors checked; PRD 03 + PRD 05 GitHub URLs use canonical
  form per Sprint 1 Issue 9 fix
- `cmd/roksbnkctl --version`: ldflags-driven via
  `internal/version.Version` (per `internal/exec/docker.go:75-93`'s
  `toolImageTag` resolver); a release-built binary will report `v0.9`
  via `-ldflags="-X internal/version.Version=v0.9"` per the standard
  Go tag-build pattern. The build setup is documented in
  `Makefile` + `chapter 31 — Building from source` (currently a
  Sprint 6 stub — not blocking v0.9 since the integrator can do the
  ldflags pass at tag-time).
- README has no v0.9 / Sprint 5 highlight bullet — filed as **Issue 1
  (BLOCKER)**
- No `CHANGELOG.md` exists — filed as **Issue 2 (BLOCKER)**
- v0.9 release checklist in `docs/E2E_TEST.md` lines 166-274 is
  comprehensive and actionable for the integrator at tag time ✓

## v0.9 release-gate assessment (PLAN.md §"Sprint 5 — Gate to Sprint 6")

PLAN.md gate items at lines 426-431:

| Gate item | Status |
|---|---|
| M3 merged + tagged `v0.9` | **blocked on Issue 1 + Issue 2** |
| Phase L-DNS passes including the GSLB divergence detection | ✓ (LD0-LD8 + LD10 wired in `scripts/e2e-test-backends.sh`; LD9 yellow-skipped per Sprint 6 SSH-e2e deferral; integrator-validated DRY_RUN walkthrough) |
| terraform `--backend docker` runs a real `up` cycle end-to-end | ⚠ deferred to integrator's manual sign-off per `docs/E2E_TEST.md` §"v0.9 release checklist" item 3 (no IBM Cloud account on the sprint VM) |
| Three chapters published; testing section of book complete; total ~22 chapters live | ✓ (chapters 20 / 21 / 22 + chapter 17 expansion all in `main`; SUMMARY.md TOC updated; book builds cleanly per CI; Issues 3-9 are quality fixes, not blockers) |

The two **release blockers** (Issues 1 + 2) are doc-only, low-effort,
and can be landed by the integrator in a single follow-up commit. The
other 13 issues are quality polish that doesn't gate the v0.9 tag —
the chapters as written are usable and the implementation is sound.

Recommendation: integrator lands Issue 1 (README highlight bullet) +
Issue 2 (CHANGELOG.md) before tagging `v0.9`. Issues 3, 4, 5, 6 are the
high-value chapter-correctness fixes for a v0.9.1 polish patch (or a
single follow-up commit before tag if convenient).

## Summary

15 issues filed for Sprint 5: 2 high (v0.9 release blockers — README
highlight + CHANGELOG), 1 high doc-correctness (chapter 21 single-
vantage schema string + JSON shape), 3 medium (chapter 21
`edns_client_subnet` field that doesn't exist, parallel-vs-serial
vantage execution, chapter 17 `/work` vs `/state` mount path), 9 low.

Top three noteworthy observations not filed as issues:

1. **Chapter 21 is excellent flagship-chapter writing.** The GSLB
   problem statement, per-vantage probing rationale, sample F5 BIG-IP
   Next scenarios, and CI-assertion section are all on-tone and
   technically rich. The schema-shape error (Issue 3) is a single
   load-bearing fix that brings the rest of the chapter from "great
   draft" to "release-ready spec".
2. **Chapter 22's bundled-image / SCC story is well-explained.** Even
   though the out-of-box default still pulls a root-running image,
   the chapter walks the user through the workspace-config override
   correctly. Issue 14 is a chapter-flow improvement, not a
   correctness fix.
3. **The 23 new validator tests have clear, intent-documenting
   names.** `TestProbe_RecordTypes_AllParseAndProjectType`,
   `TestProbe_Server_ClusterFromLocalBackendErrors`,
   `TestProbe_Rcode_NXDOMAIN/SERVFAIL/REFUSED/Timeout` follow the
   Sprint 4 tech-writer Issue 14 bar. Top-of-file docstring for
   `dns_test.go` explains the in-process miekg/dns server fixture
   shape clearly. The `TestK8sBackend_Run_Job_*` split (Sprint 4
   Issue 14 carry-over) landed cleanly.

Drift between PRD 03 / PRD 05 / PLAN.md and delivered surface:

- **PRD 03 §"DNS probe" §"Default backends"** still says "default both
  vantages always"; implementation defaults to `local` single-vantage
  with `--gslb-compare` opt-in (Issue 10 — Sprint 5 architect's
  resolved log already flagged this for a future PRD refresh).
- **PRD 03 §"DNS probe" §"JSON output schema"** specifies the
  `edns_client_subnet` field; implementation doesn't emit it (Issue 4).
- **PLAN.md "deferred to post-v1.0"** still lists `--require-divergence`
  as v1.1; staff landed it this sprint (Issue 11).
- **PRD 05 §"Phase L-DNS"** matches `scripts/e2e-test-backends.sh`'s
  Phase L-DNS implementation; LD9 + M5/M6 yellow-skipped per the
  Sprint 6 SSH-e2e deferral; `docs/E2E_TEST.md` §"v0.9 release
  checklist" calls this out clearly. No drift.

**v0.9 release gate criteria assessment**: M3 merged + tagged `v0.9`
is **blocked on Issues 1 + 2** (README highlight + CHANGELOG);
otherwise the gate items are met. The integrator can land both fixes
in a single follow-up commit (~30 minutes) before tagging.
