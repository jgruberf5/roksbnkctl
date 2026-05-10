# Sprint 5 — architect issues

## Issue 1: Chapter 21 flag surface and JSON schema are forward-statements; staff may diverge
**Severity**: medium
**Status**: open
**Description**: Chapter 21 documents the `roksbnkctl test dns` flag surface (`--target`, `--type`, `--server`, `--iterations`, `--backend`, `--gslb-compare`, `-o json`) and the `roksbnkctl.dns.v1` JSON schema verbatim from PRD 03 §"DNS probe (GSLB-aware)". At drafting time `internal/cli/test.go::dnsCmd` flag definitions and `internal/test/dns.go`'s miekg-based `Probe` struct were not yet committed (the staff agent has them in flight this sprint). The chapter is faithful to the PRD, but if staff lands a flag with a slightly different name (e.g., `--records` instead of `--type`, or `--source` instead of `--server`) or a JSON field with different casing, the chapter will be inaccurate.

Specific points to spot-check after staff lands:

- Flag names exactly: `--target`, `--type`, `--server`, `--iterations`, `--gslb-compare` (chapter uses these per PRD 03's CLI surface examples).
- JSON schema string literal: `roksbnkctl.dns.v1` (chapter pins against this for CI assertions).
- JSON field names verbatim: `vantages[]`, `vantages[].backend`, `vantages[].server`, `vantages[].iterations`, `vantages[].rtt_ms.{p50,p95,p99}`, `vantages[].answers[].{name,type,ttl,rdata}`, `vantages[].rcode`, `vantages[].authoritative`, `vantages[].truncated`, `vantages[].edns_client_subnet`, `gslb_divergence`, `gslb_divergence_summary`. PRD 03 §"DNS probe" §"JSON output schema" is the source.
- Docker rejection error text — chapter quotes a verbatim message ("DNS probe doesn't benefit from --backend docker (same network identity as --backend local, no GSLB-relevant vantage difference). Use --backend local, --backend k8s, or --backend ssh:<target> instead."). Staff's actual error text may phrase this differently.
- Server form `--server cluster` — chapter says it errors at parse time when used with `--backend local` or `--backend ssh:<target>`. Confirm staff implements that validation.

**Files affected**: `book/src/21-dns-testing-gslb.md`
**Proposed fix**: integrator runs `roksbnkctl test dns --help` against staff's landed binary and diffs against the chapter's flag table; integrator runs a representative invocation with `-o json` and diffs the output against the chapter's schema reference. Small-prose tweaks land as a follow-up commit on the integration branch. If staff renamed any flag or schema field, the chapter needs the rename.

## Issue 2: Chapter 21 documents `--require-divergence` ambiguously vs PLAN.md
**Severity**: low
**Status**: open
**Description**: PLAN.md "What's deliberately deferred to post-v1.0" lists `DNS probe '--require-divergence' CI assertion mode (v1.1)` as deferred — i.e., not in v0.9. PRD 03 §"DNS probe" mentions the flag inline ("flip to `--require-divergence` to fail when GSLB silently returns identical answers everywhere"). The Sprint 5 architect prompt instructs the chapter to cover "the `--require-divergence` flag for CI assertions".

To avoid documenting a flag the binary doesn't ship, chapter 21 instead describes the user-side equivalent (a `jq -e '.gslb_divergence == true'` exit-code-keyed assertion in CI). That preserves the CI-assertion workflow without claiming a flag that may not exist on the v0.9 binary.

**Files affected**: `book/src/21-dns-testing-gslb.md` §"The `--gslb-compare` workflow"
**Proposed fix**: if staff lands `--require-divergence` this sprint after all, add a short subsection "Asserting divergence in CI" that names both the flag and the `jq -e` form. If staff defers per PLAN.md, the chapter is correct as written; close this issue resolved.

## Issue 3: Chapter 20 documents `extra_hosts` as a bare URL list; prompt described a richer schema
**Severity**: low
**Status**: open
**Description**: The Sprint 5 architect prompt's chapter-20 spec describes the `extra_hosts:` config block as supporting "URL, optional method, optional expected status, optional `insecure_tls: true` for self-signed". The actual `internal/config/workspace.go::ConnectivityCfg.ExtraHosts` field is a bare `[]string` of URLs (per `grep -n ExtraHosts internal/config/workspace.go`: `ExtraHosts []string yaml:"extra_hosts,omitempty"`). The richer schema doesn't exist in the v0.9 codebase.

Chapter 20 is written against the actual v0.9 schema (a `[]string` of URLs) and explicitly notes "There's no per-host method, no per-host expected-status, and no per-host TLS-trust override today." It also calls out that the `--insecure` flag is session-wide rather than per-host, and that the workaround for mixed TLS-trust posture is two workspaces.

**Files affected**: `book/src/20-connectivity-testing.md` §"Configuring `extra_hosts`"
**Proposed fix**: confirm with staff/integrator that the v0.9 binary keeps `ExtraHosts` as `[]string`. If a richer schema is intended for v0.9 (the prompt suggests so), staff needs to land the schema change and chapter 20 needs a follow-up edit. If the schema stays minimal (which matches the codebase), chapter 20 is correct.

## Issue 4: Chapter 20 documents `--insecure` not `--insecure-tls`
**Severity**: low
**Status**: open
**Description**: The Sprint 5 architect prompt for chapter 20 names the flag `--insecure-tls`. The actual v0.9 binary defines the flag as `--insecure` in `internal/cli/test.go` (`testCmd.Flags().BoolVar(&flagInsecureTLS, "insecure", false, "skip TLS certificate validation (connectivity only)")`). Chapter 20 documents the actual flag (`--insecure`) per the "true of the v0.9 binary, not aspirational" directive.

**Files affected**: `book/src/20-connectivity-testing.md` §"The `--insecure` flag"
**Proposed fix**: if staff intends to rename the flag to `--insecure-tls` for v0.9 (clearer intent; the variable is already `flagInsecureTLS`), staff lands the rename and chapter 20 needs a one-word edit. If the flag stays `--insecure`, chapter 20 is correct.

## Issue 5: Chapter 17 §"terraform via docker" pin (`hashicorp/terraform:1.5.7`) read from existing code; staff may bump
**Severity**: low
**Status**: open
**Description**: Chapter 17's new §"terraform via docker" subsection cites the terraform image pin as `hashicorp/terraform:1.5.7`, read from `internal/exec/docker.go::toolImages`'s existing `terraform` entry. Staff may bump the pin this sprint as part of "wiring up the docker terraform backend" — a bumped pin would make chapter 17's literal version stale.

**Files affected**: `book/src/17-execution-backends.md` §"terraform via docker" §"Image"
**Proposed fix**: integrator greps `internal/exec/docker.go` for `hashicorp/terraform:` and updates the chapter's literal version reference if staff bumped the pin.

## Issue 6: Chapter 17 §"terraform via docker" claims `roksbnkctl up/plan/apply/destroy --backend docker` work; staff may not land all four
**Severity**: medium
**Status**: open
**Description**: Chapter 17 documents `roksbnkctl up --backend docker`, `plan --backend docker`, `apply --backend docker`, `destroy --backend docker` as supported in v0.9. PLAN.md Sprint 5 Week 2 row 7 says "`--backend docker` for `roksbnkctl up`/`plan`/`apply`/`destroy`" — all four. The Sprint 5 architect prompt says "The supported commands: `roksbnkctl up --backend docker` (apply + auto-approve), `roksbnkctl plan --backend docker`, `roksbnkctl apply --backend docker`, `roksbnkctl destroy --backend docker`."

Risk: staff may land a subset (e.g., only `up` + `destroy`) due to scope-cutting. Chapter 17's "Supported commands" subsection would then over-promise.

**Files affected**: `book/src/17-execution-backends.md` §"terraform via docker" §"Supported commands"
**Proposed fix**: integrator confirms each of the four `roksbnkctl <verb> --backend docker` actually plumbs through to the docker terraform backend in staff's landed code. If any are deferred, chapter 17's list narrows correspondingly.

## Issue 7: Chapter 17 per-tool defaults table now has 4 rows including a `dns` row that didn't exist before
**Severity**: low
**Status**: open
**Description**: The Sprint 5 update to chapter 17's per-tool defaults table widened it to include `Supported backends` and added a `dns` row. The `dns` row claims default `local` and supported `local`, `k8s`, `ssh:<target>` per PRD 03 §"DNS probe". This is a forward-statement against the staff agent's `internal/cli/test.go` per-tool default map — confirm the entry exists and matches.

PRD 03 §"DNS probe" §"Default backends" actually says **`local` and `k8s`** (run both, surface both answers — see GSLB note). The chapter simplifies to "default `local`, `--gslb-compare` fans out". If staff implements the PRD's literal "default both vantages always" semantics, the table row needs a tweak.

**Files affected**: `book/src/17-execution-backends.md` §"Per-tool defaults from `exec:`" table; `book/src/22-throughput-testing.md` cites this table.
**Proposed fix**: integrator confirms whether staff implements `dns` as "single-vantage default `local`, opt-in fan-out via `--gslb-compare`" (chapter's framing) or "default both vantages always" (PRD's literal text). One-line table edit if the latter.
