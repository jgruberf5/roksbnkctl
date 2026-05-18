# Sprint 6 — staff engineer issues, resolution notes

Three issues filed: 1 low (chapter 21 EDNS doc drift — architect/tech-writer territory), 2 informational (Phase N live verification; terraform internalisation v1.x roadmap). The low-severity doc drift was resolved by the integrator in this pass; the two informationals are accepted as v1.0 sign-off / v1.x roadmap.

## Issue 1 (LOW — chapter 21 + PRD 03 `edns_client_subnet` doc drift) — resolved by integrator

Staff landed `DNSProbeResult.EDNSClientSubnet` + `extractEDNSClientSubnet` in `internal/test/dns.go`; chapter 21 + PRD 03 still described the field as "reserved for v1.x". Integrator updated:

- `book/src/21-dns-testing-gslb.md` §"Both schemas are stable…" paragraph (line 289) — rewrote the v1.x-reserved framing to acknowledge v0.10 added the field; documented the sub-fields (`family`, `source_netmask`, `scope_netmask`, `address`); kept the `omitempty` framing so readers know the field is silent on non-ECS resolvers.
- `CHANGELOG.md` — moved the EDNS line out of §"Deferred (post-v1.0)" and added a "Sprint 6 (v1.0 prep)" section to capture the v0.10 surface additions including EDNS Client Subnet.
- PRD 03's JSON example already shows `edns_client_subnet: null` and never explicitly said "reserved for v1.x" in its prose — no PRD edit required.

**Status**: ✅ resolved (chapter 21 ↔ `internal/test/dns.go::extractEDNSClientSubnet` consistent)

## Issue 2 (INFORMATIONAL — Phase N live mixed-mode verification) — accepted (Go-side contract pinned; live e2e is integrator scope)

Staff's `internal/cred/resolver_invariance_test.go` pins the cred-resolver contract across all four backends — the Go-side guarantee that PRD 04 §3 promises. Backend-independent state path + kubeconfig path verified at code-review time.

The live Phase N verification (running `up --backend local; test throughput --backend k8s; down --backend docker` against a real cluster) is the integrator's v1.0 manual sign-off scope per `docs/E2E_TEST.md` §"Per-release checklist". Validator's `scripts/e2e-test-backends.sh::phase_N` (N1-N6) wires the e2e assertions; the integrator's manual run lights it up against a real workspace.

**Status**: ✅ accepted (unit-tier contract pinned; live verification deferred to integrator sign-off run)

## Issue 3 (INFORMATIONAL — terraform-as-host-tool v1.x exploration) — accepted

`terraform` is the one remaining required host install after Sprint 6's doctor refresh. A v1.x effort could internalise the IBM provider via `terraform-plugin-go` and run apply/destroy directly from Go — eliminating the host binary requirement entirely. The `--backend docker` for terraform (Sprint 5) already provides a "no host terraform required" path that produces the same state file.

PLAN.md §"What's deliberately deferred to post-v1.0" tracks this direction. No Sprint 6 action.

**Status**: ✅ accepted (v1.x roadmap)

## Verification of Sprint 5 polish carry-overs (staff Priority 5)

- **5a. Dockerfile ENTRYPOINT drop**: verified `tools/docker/ibmcloud/Dockerfile` no longer has `ENTRYPOINT ["ibmcloud"]`. Verified `internal/exec/docker.go::resolveDockerImageAndArgv` now prepends the tool binary name explicitly via `dockerImageBinary` map. Verified `internal/exec/k8s.go::jobToolCmdOverride` extended to include `ibmcloud` (mirrors the docker path). Cross-backend invariant pinned by `TestDockerImageBinary_MirrorsK8sOverrides`. Regression checks (ibmcloud --backend docker, ibmcloud --backend k8s) are unit-test-pinned; live verification deferred to Phase K + L of integrator's v1.0 e2e run.
- **5b. EDNS Client Subnet**: verified `DNSProbeResult.EDNSClientSubnet` is populated when `dns.Msg.IsEdns0()` returns a `dns.EDNS0_SUBNET` option. Two new unit tests (`TestProbe_EDNSClientSubnet_Echoed` + `TestProbe_EDNSClientSubnet_AbsentWhenNoOPT`) pin the behaviour. Chapter 21 + CHANGELOG.md updated by the integrator this pass.

## Verification of staff's Priority 1-4 deliverables

- **Priority 1 (generators)**: integrator ran both generators successfully. `go run ./tools/refgen/cobra-md` emits 1151 lines covering all 25 top-level commands + global flags + per-subcommand sections. `go run ./tools/refgen/tfvars-md` emits 205 lines covering root module + 6 submodules with sensitive-flag honoring. Both generators have passing smoke tests (`tools/refgen/cobra-md/main_test.go` + `tools/refgen/tfvars-md/main_test.go`).
- **Priority 2 (doctor green-by-default)**: pinned by `internal/doctor/doctor_test.go::TestHasFailures_StockDevBoxGreen`. Live host smoke deferred to integrator's `docs/E2E_TEST.md` §5 sign-off item.
- **Priority 3 (MIGRATING.md)**: top-level file lands; covers v0.6.x bnkctl migration, manual BNK deployment migration, v0.7/v0.8/v0.9 within-roksbnkctl notes, workspace migration. Sized ~250 lines per the architect's prompt.
- **Priority 4 (Phase N Go-side stubs)**: cred-resolver invariance test landed; backend-independent state path + kubeconfig path verified at code-review time. Live verification is integrator scope.

## Summary

3 issues filed; 1 resolved by integrator (chapter 21 + CHANGELOG.md EDNS doc update), 2 accepted (1 integrator sign-off scope, 1 v1.x roadmap). All seven Priority items (1a, 1b, 1c, 2, 3, 4, 5a, 5b) landed cleanly. Build, vet, gofmt, full test suite, generator smoke tests all green.
