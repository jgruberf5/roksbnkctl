# Sprint 5 — architect issues, resolution notes

Seven issues filed: 2 medium, 5 low. Six resolved by integrator
spot-check (chapters were already correct against staff's landed code);
1 resolved by integrator-side prose update (staff landed
`--require-divergence` after all, so chapter 21 now documents both the
flag and the `jq -e` form).

## Issue 1 (MEDIUM — chapter 21 flag surface + JSON schema spot-check) — resolved by integrator (spot-check passed)

Verified chapter 21's flag list and JSON schema string against staff's
landed code:

- `internal/cli/test.go:123-129` — flags `--target`, `--type`,
  `--server`, `--iterations`, `--timeout`, `--gslb-compare`,
  `--require-divergence` all present and named exactly as chapter 21
  documents.
- `internal/test/dns.go:14-19` — `DNSSchemaVersion = "roksbnkctl.dns.v1"`
  + `DNSVantageSchemaVersion = "roksbnkctl.dns.v1.vantage"`. Chapter 21
  references both verbatim.
- `internal/test/dns.go:55-99` — JSON struct fields (`schema`,
  `backend`, `server`, `iterations`, `rtt_ms.{p50,p95,p99}`, `answers`,
  `rcode`, `authoritative`, `truncated`, `gslb_divergence`,
  `gslb_divergence_summary`) match chapter 21's schema reference.
- Docker rejection error text — verified at `runTestDNSProbe`'s
  early-return; matches the chapter quote modulo trivial wording.

**Status**: ✅ resolved (chapter 21 ↔ staff's landed code consistent)

## Issue 2 (LOW — chapter 21 `--require-divergence` framing vs PLAN.md) — resolved by integrator (chapter updated)

Staff landed `--require-divergence` after all
(`internal/cli/test.go:129`), contrary to PLAN.md's "deferred to v1.1"
note. Updated chapter 21 §"Asserting divergence in CI" to document both
the `--require-divergence` flag (Option B, no `jq` dep) and the
`jq -e` form (Option A, parse-it-yourself). Both produce the same
non-zero exit when divergence is absent.

**Status**: ✅ resolved (chapter 21 ↔ staff's landed flag consistent;
PLAN.md note will be updated in the resolved\_tech-writer log)

## Issue 3 (LOW — chapter 20 `extra_hosts` schema is `[]string`) — accepted (chapter is correct)

Verified `internal/config/workspace.go:132` — `ExtraHosts []string`,
matching chapter 20's framing. The architect's prompt described a
richer schema (per-host method/status/insecure_tls) that doesn't
exist in v0.9; chapter 20 correctly notes this as the v1.x roadmap
shape.

**Status**: ✅ accepted (chapter 20 ↔ workspace.go consistent)

## Issue 4 (LOW — chapter 20 `--insecure` flag name) — accepted (chapter is correct)

Verified `internal/cli/test.go:113` —
`testCmd.Flags().BoolVar(&flagInsecureTLS, "insecure", false, …)`. The
flag is named `--insecure` (not `--insecure-tls`) per the v0.9 binary;
chapter 20 documents the actual name. The variable's `flagInsecureTLS`
internal name is a Go identifier convention; the user-visible flag is
`--insecure`.

**Status**: ✅ accepted (chapter 20 ↔ test.go consistent)

## Issue 5 (LOW — chapter 17 terraform image pin `:1.5.7`) — accepted (chapter is correct)

Verified `internal/exec/docker.go:66, 109` — both `toolImages` entries
say `hashicorp/terraform:1.5.7`. Chapter 17's literal version reference
matches. Staff didn't bump the pin this sprint.

**Status**: ✅ accepted (chapter 17 ↔ docker.go consistent)

## Issue 6 (MEDIUM — all four `up/plan/apply/destroy --backend docker` commands) — accepted (all four landed)

Verified `internal/cli/lifecycle.go:115, 160, 181, 205` — `up`, `plan`,
`apply`, and `destroy` all dispatch through `runTerraformLifecycleDocker`
when `--backend docker` is set. No scope-cut; all four are wired per
chapter 17's "Supported commands" list.

**Status**: ✅ accepted (chapter 17 ↔ lifecycle.go consistent)

## Issue 7 (LOW — chapter 17 per-tool defaults table `dns` row framing) — accepted (chapter framing is correct)

Verified `internal/cli/cluster.go:338-342` —
`perToolDefaultBackend` has three entries (iperf3=k8s, ibmcloud=local,
terraform=local). No `dns` row. Without an entry, `roksbnkctl test dns`
without flags falls through to `local` (single-vantage,
workspace-`extra_hosts`-driven default). Multi-vantage is opt-in via
`--gslb-compare`.

This matches chapter 17's framing ("Single-vantage by default;
`--gslb-compare` fans out across configured vantages") and PLAN.md's
v0.9 scope. PRD 03 §"DNS probe" §"Default backends" said "default
both vantages always", but PLAN.md and the staff implementation chose
the more conservative "opt-in fan-out" — flagging the PRD as
slightly aspirational; we'll refresh PRD 03 to match in a follow-up.

**Status**: ✅ accepted (chapter 17 ↔ cluster.go consistent; PRD 03
will be updated in resolved\_tech-writer pass to match the
single-vantage default)

## Summary

7 issues filed, 7 resolved (6 self-resolved spot-checks; 1 prose
update for `--require-divergence`). Build, vet, gofmt, and full test
suite green.
