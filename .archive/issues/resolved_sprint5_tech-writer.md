# Sprint 5 — tech writer issues, resolution notes

15 issues filed: 2 high (v0.9 release blockers), 1 high doc-correctness,
3 medium, 9 low. Both v0.9 blockers resolved; the high doc-correctness
chapter-21 schema fix landed; 4 of 6 mediums/lows resolved with prose
or YAML fixes; 2 lows self-resolved (chapter 17 iperf3 server section
already correct against the implementation; the parallel/serial fix
covered in Issue 5 also subsumed Issue 7's k8s-vantage prereq edit);
1 low deferred to v0.9.1 polish (chapter 22 flow-order improvement).

## Issue 1 (HIGH BLOCKER — README missing v0.9 / Sprint 5 highlight bullet) — resolved by integrator

Added a Sprint 5 / v0.9 highlight bullet to `README.md` §"Highlights"
matching the Sprint 1-4 cadence: covers `roksbnkctl test dns
--gslb-compare` (with `--require-divergence` mentioned alongside the
`jq -e` form), `roksbnkctl up/plan/apply/destroy --backend docker`
with the `hashicorp/terraform:1.5.7` pin and host-user UID/GID
alignment. Cross-links chapters 21 (DNS) + 22 (throughput) + 17
(terraform via docker). The "Status" line at the top flipped from
"Pre-release. Source compiles, unit tests pass…" to "v0.9 release
candidate. Four execution backends, GSLB-aware DNS probe, and
terraform-via-docker all wired and tested." plus a forward-link to
the new `CHANGELOG.md`.

**Status**: ✅ resolved (release-blocker cleared)

## Issue 2 (HIGH BLOCKER — no CHANGELOG.md) — resolved by integrator

Created `CHANGELOG.md` at the repo root using the Keep a Changelog
format with semantic versioning starting at `v0.9.0`. The v0.9.0
section covers the cumulative Sprint 3 + 4 + 5 surface (cred
abstraction; four backends; in-cluster ops pod; GSLB-aware DNS probe;
terraform via docker; doctor extensions; book chapters 12-22). An
"Unreleased (v1.x)" section seeds the v1.0/v1.x roadmap from PLAN.md
§"What's deliberately deferred to post-v1.0".

**Status**: ✅ resolved (release-blocker cleared)

## Issue 3 (HIGH doc-correctness — chapter 21 wrong schema string + wrong shape for single-vantage) — resolved by integrator

Rewrote chapter 21's `## JSON output schema` section to document the
**two distinct schemas** (`roksbnkctl.dns.v1.vantage` for single-vantage,
`roksbnkctl.dns.v1` for `--gslb-compare`) explicitly. Replaced the
single-vantage JSON example with the actual flat shape from
`internal/test/dns.go::DNSProbeResult` — no `vantages[]` wrap, no
top-level `target`/`type`/`gslb_divergence`, schema string
`roksbnkctl.dns.v1.vantage`. Multi-vantage example updated to embed
the per-vantage `schema` field on each `vantages[]` entry (since both
shapes share the per-vantage struct). The `### Schema field reference`
section split into two sub-tables (per-vantage shape vs comparison
shape). The `-o json` row in the flag table was rewritten to name
both schemas.

**Status**: ✅ resolved (chapter 21 ↔ `internal/test/dns.go` consistent;
JSON examples are now copy-pasteable into a CI assertion)

## Issue 4 (MEDIUM — chapter 21 documents `edns_client_subnet` field that the binary doesn't emit) — resolved by integrator

Dropped `vantages[].edns_client_subnet` from the chapter 21 schema
field reference and from the JSON examples. Added a one-line callout
that PRD 03 reserves the field for v1.x; v0.9 doesn't emit it. (The
Sprint 5 staff agent didn't land the EDNS Client Subnet field; PRD 03
will be refreshed in a future polish pass to acknowledge the v1.x
reservation rather than implying a v0.9 commitment.)

**Status**: ✅ resolved (chapter 21 ↔ `DNSProbeResult` struct
consistent)

## Issue 5 (MEDIUM — chapter 21 says vantages run in parallel; impl is serial) — resolved by integrator

Rewrote chapter 21 §"The `--gslb-compare` workflow" line "Each vantage
runs the probe in parallel" → "Each vantage runs the probe in sequence
(one at a time; the run completes when the slowest vantage returns).
… Worst-case wall time with three vantages and the default 2-second
per-query timeout is ~6 seconds." Matches the actual `runDNSGSLBCompare`
serial loop. (A parallel implementation via `errgroup` is a reasonable
v0.9.1 polish; the chapter now reflects what the binary does today.)

**Status**: ✅ resolved (chapter 21 ↔ `runDNSGSLBCompare` consistent)

## Issue 6 (MEDIUM — chapter 17 `/work` mount path; HCL not bind-mounted claim is wrong) — resolved by integrator

Replaced `/work` with `/state` everywhere in chapter 17 §"terraform
via docker" (matches `internal/cli/lifecycle.go::runTerraformLifecycleDocker`
+ `docker_terraform_integration_test.go` which both bind-mount at
`/state`). Replaced `--workdir /work` with `--workdir
/state/tf-source/embedded-terraform` (matches the staff impl's
`containerSrcDir := filepath.ToSlash(filepath.Join("/state", srcRel))`).
Rewrote the "HCL is not bind-mounted" prose to acknowledge that the
embedded HCL **is** materialised under `<stateDir>/tf-source/<source>/`
and so does land inside the container via the same bind-mount; cross-link
to chapter 31 §"embedded-terraform layout" stub.

**Status**: ✅ resolved (chapter 17 ↔ `lifecycle.go::dockerTerraformExec`
consistent; integration test docstring at
`docker_terraform_integration_test.go:99-117` confirms the path)

## Issue 7 (LOW — chapter 21 §"`--gslb-compare` workflow" misframes k8s vantage prereq) — resolved by integrator

The "Each vantage runs the probe in parallel" rewrite (Issue 5) also
subsumed the line above it about vantage enumeration. Final wording:
"`local` always; `k8s` when a kubeconfig is reachable on the host
(the probe runs as a one-shot Job in `roksbnkctl-test` — the long-lived
ops pod isn't required); plus every entry in the workspace's
`targets:` block, each as `ssh:<name>`." Matches `internal/cli/test.go`
lines 303 + 307-325.

**Status**: ✅ resolved (chapter 21 ↔ `runDNSGSLBCompare` enumerator
consistent)

## Issue 8 (LOW — chapter 21 overstates k8s Job image equivalence) — resolved by integrator

Rewrote chapter 21 §"Backend selection" `--backend k8s` row to name
the actual image (`ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>`,
"the same image the in-cluster ops pod uses; carries both `ibmcloud`
and `roksbnkctl` on PATH"). Added an inline parenthetical: "If a Job
fails to pull, `kubectl describe pod` will name the
`roksbnkctl-tools-ibmcloud` image — there is no separate
`roksbnkctl-cli` image to look for." This points debuggers at the
right artefact when a fresh cluster lacks the image.

**Status**: ✅ resolved (chapter 21 ↔ `internal/exec/docker.go::toolImages`
consistent)

## Issue 9 (LOW — chapter 17 iperf3 server-shape — Sprint 4 carry-over) — self-resolved (chapter is already correct)

Checked the chapter 17 iperf3 server section after Sprint 5's
terraform-docker addition; the section now lives at lines 401-410
(shifted from Sprint 4's lines 322-330 by the new terraform-docker
prose) and reads:

> | Server | `roksbnkctl-iperf3` bare Pod + Service (`LoadBalancer`
> for `--mode north-south`; `ClusterIP` for `--mode east-west`) | torn
> down after the client Job completes |

…with a follow-on paragraph explaining the bare-Pod-vs-Deployment
choice. The Sprint 4 carry-over claim was based on stale line numbers;
the actual prose is consistent with chapter 22 and with
`internal/k8s/iperf3.go`. No edit needed.

**Status**: ✅ self-resolved (chapter 17 already correct against the
implementation; the Sprint 4 issue's open-status was a stale
line-number reference)

## Issue 10 (LOW — PRD 03 default-backends drift) — resolved by integrator

Updated PRD 03 §"DNS probe" §"Default backends" row from "default
both vantages always" to "single-vantage default `local`; multi-vantage
opt-in via `--gslb-compare`, which fans out across `local` + `k8s`
(when a kubeconfig is reachable) + every registered SSH target. v0.9
chose opt-in over 'run both always' to keep the default fast and
side-effect-free; revisit per user feedback in v1.x." The PRD now
matches the implementation + chapter 17 + chapter 21 framing.

**Status**: ✅ resolved (PRD 03 ↔ implementation ↔ chapters 17 + 21
consistent)

## Issue 11 (LOW — PLAN.md still lists `--require-divergence` as v1.1 deferred) — resolved by integrator

Removed the `DNS probe '--require-divergence' CI assertion mode (v1.1)`
bullet from PLAN.md §"What's deliberately deferred to post-v1.0".
Staff landed the flag this sprint at `internal/cli/test.go:129`;
chapter 21 documents both the flag and the `jq -e` form per the
integrator's prose update earlier this pass.

**Status**: ✅ resolved (PLAN.md deferred-list ↔ implementation
consistent)

## Issue 12 (LOW — stale `sshseam` build-tag comment in `ssh_wrapper_test.go`) — resolved by integrator

Trimmed the file's docstring to drop the "Build tag: gated behind
`-tags sshseam`…" prose and the now-incorrect `go test -tags sshseam`
invocation. The "Run with: `go test -run SSHWrapper
./internal/exec/...`" line stays, matching the file's actual
no-tag setup (the tag was dropped at validator-run time once staff
landed the seam).

**Status**: ✅ resolved

## Issue 13 (LOW — `cspell.json` duplicate `miekg`) — resolved by integrator

Removed the duplicate `miekg` entry at `cspell.json:74` (the original
entry at line 34 stays). One occurrence remains; cspell is happy.

**Status**: ✅ resolved

## Issue 14 (LOW — chapter 22 bundled-image / SCC flow ordering) — accepted (deferred to v0.9.1 polish)

Tech-writer's recommendation was to move chapter 22's bundled-image /
SCC explanation earlier in the chapter (before §"Reading the output")
so a user reading top-to-bottom hits the SCC gotcha before being shown
sample output. The current chapter prose is correct (and explains the
override path); the recommendation is a chapter-flow improvement, not
a correctness fix. Rolled into the v0.9.1 polish basket alongside
similar "good chapter, could be better-ordered" items.

The higher-effort fix (flip `Iperf3DefaultImage` to the bundled image
so the out-of-box experience is OpenShift-clean) is a behaviour change
and isn't appropriate at the v0.9-cut moment. Tracked for v1.x as a
default-image revision once we have user-feedback confirming the
bundled image is safe across all supported environments.

**Status**: ⏸ accepted (chapter 22 is correct; flow-order improvement
queued for v0.9.1 polish)

## Issue 15 (LOW — chapter 21 record-type list is incomplete) — resolved by integrator

Updated chapter 21's `--type` row in the flag table from "any record
type the underlying [`miekg/dns`] library exposes via its `dns.Type`
enum: `A`, `AAAA`, …, `ANY`, etc." to "any record type the underlying
[`miekg/dns`] library accepts via [`dns.StringToType`]. Common picks:
A, AAAA, CNAME, MX, NS, TXT, SRV, SOA, PTR, CAA, DS, DNSKEY, ANY. The
full table also includes HTTPS, SVCB, TLSA, SSHFP, URI, NAPTR, RRSIG,
NSEC/NSEC3, LOC, etc." With a deep-link to `dns.StringToType` on
pkg.go.dev as the canonical source-of-truth.

**Status**: ✅ resolved

## Integrator additions

- Verified `go build ./...`, `go vet ./...`, `gofmt -d -l .`,
  `go test ./...` all green post-fixes.
- Verified `DRY_RUN=1 IBMCLOUD_API_KEY=dummy ROKSBNKCTL=true
  ./scripts/e2e-test-backends.sh` emits all four phases (K, L, L-DNS,
  M) cleanly with proper yellow-skip markers for Sprint-6-deferred
  steps (LD9, M5+M6) — confirmed at integration time per
  validator's Issue 2.

## v0.9 release-gate verdict (PLAN.md §"Sprint 5 — Gate to Sprint 6")

| Gate item | Status |
|---|---|
| M3 merged + tagged `v0.9` | **ready** (Issues 1 + 2 cleared this pass) |
| Phase L-DNS passes including the GSLB divergence detection | ✓ (LD0-LD8 + LD10 wired; LD9 yellow-skipped per Sprint 6 SSH-e2e deferral; DRY_RUN walkthrough green) |
| terraform `--backend docker` runs a real `up` cycle end-to-end | ⚠ deferred to integrator's manual sign-off per `docs/E2E_TEST.md` §"v0.9 release checklist" item 3 (no IBM Cloud account on the sprint VM) |
| Three chapters published; testing section of book complete; ~22 chapters live | ✓ (chapters 20 / 21 / 22 + chapter 17 expansion; SUMMARY.md TOC updated) |

The v0.9 tag is unblocked from the doc / release-readiness side. The
"terraform `--backend docker` end-to-end against a real workspace"
gate item is the integrator's manual sign-off step per the v0.9
release checklist in `docs/E2E_TEST.md` — not a code or doc
deliverable.

## Summary

15 issues filed; 13 resolved in this pass (2 release blockers cleared,
1 high doc-correctness fix, 4 medium/low chapter corrections, 4 low
PRD/PLAN/test-file/cspell cleanups, 2 self-resolved against actual
implementation); 1 (Issue 14) accepted with deferral to v0.9.1 polish
(chapter-flow improvement, not a correctness fix). Build, vet, gofmt,
full test suite, and DRY_RUN walkthrough of the e2e driver all green.

**The v0.9 tag can ship.**
