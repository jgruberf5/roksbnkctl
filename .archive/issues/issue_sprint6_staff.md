# Sprint 6 — staff engineer issues

Sprint 6 closes Sprint 5 polish carry-overs (Dockerfile ENTRYPOINT drop, the corresponding shim trim, optional EDNS Client Subnet surfacing), lands the auto-generators for chapters 27 + 29, refreshes `roksbnkctl doctor` to green-by-default on a stock dev box, ships a top-level `MIGRATING.md`, and pins the Phase N mixed-mode lifecycle Go-side contract (cred-resolver invariance) in a unit test.

Three issues filed: 1 low (chapter-21 documentation drift now that EDNS is emitted; architect/tech-writer territory), 1 informational (Phase N live verification deferred to integrator), 1 informational (terraform local-backend internalisation deferred to v1.x).

## Issue 1 (LOW — chapter 21 documents `edns_client_subnet` as v1.x reserved; v0.10 now emits it) — filed for architect / tech-writer

**Severity**: low (doc drift, not a code bug)
**Status**: filed for the next architect/tech-writer pass; staff scope per the Sprint 6 prompt is "production Go code, the auto-generators, MIGRATING.md, and the tools/docker/ibmcloud/Dockerfile carry-over" — chapter 21 is outside that lane.

Sprint 5 tech-writer Issue 4 resolved chapter 21's `## JSON output schema` section by dropping the `edns_client_subnet` field reference and adding a callout that PRD 03 reserves the field for v1.x. Sprint 6 staff landed Priority 5b — the field now surfaces from any response carrying an EDNS Client Subnet OPT option (RFC 7871), implemented in `internal/test/dns.go::extractEDNSClientSubnet` with two new unit tests in `internal/test/dns_test.go`.

The drift to fix:

- Chapter 21 line 289: "PRD 03 calls out an `edns_client_subnet` field reserved for v1.x; v0.9 doesn't emit it." Should now read "Sprint 6 (v0.10) added `edns_client_subnet` surfacing when the resolver echoes an EDNS Client Subnet option (RFC 7871) — most GSLB-aware resolvers do; vanilla recursive resolvers don't, in which case the field is omitted from the JSON via `omitempty`."
- Chapter 21 "Schema field reference" per-vantage table (around line 265): add a row for `edns_client_subnet` documenting the four sub-fields (`family`, `source_netmask`, `scope_netmask`, `address`).
- PRD 03 §"DNS probe": flip the field from "reserved for v1.x" to "implemented in v0.10".

Cross-check: `CHANGELOG.md`'s `## Unreleased (v1.x)` § "deferred" line "DNS probe `edns_client_subnet` field surfacing (PRD 03 specs it; not emitted in v0.9)" should also move under a new v0.10 section once the integrator tags v0.10.

## Issue 2 (INFORMATIONAL — Phase N mixed-mode lifecycle live verification deferred to integrator) — filed for integrator

**Severity**: informational
**Status**: ✅ accepted (the Go-side contract is pinned in unit tests; live e2e is integrator scope)

Sprint 6 Priority 4 (Phase N Go-side stubs) landed:

- Cred-resolver invariance test (`internal/cred/resolver_invariance_test.go`) — pins that `Resolver.IBMCloudAPIKey(ctx)` returns the same value across all four backends (`local`, `docker`, `k8s`, `ssh:<target>`) for the same workspace + Source. Verifies PRD 04 §3 "single-source-of-truth cred resolution" contract.
- Backend-independent state path verified at code-review time: `tf.Workspace` writes terraform state at `~/.roksbnkctl/<ws>/state/terraform.tfstate` regardless of which backend invokes `terraform apply`. The local backend writes via `terraform-exec`; the docker backend bind-mounts the state dir at `/state` and pins `--user $(id -u):$(id -g)`. Both land at the same path with the same ownership (Linux/WSL2/macOS Docker Desktop).
- Backend-independent kubeconfig: `~/.roksbnkctl/<ws>/state/kubeconfig` is written by `runUp`'s post-apply hook (`tryAutoKubeconfig`) and consumed by the k8s backend's `defaultK8sInit` → `internal/k8s/BuildClientset("")` chain. Path discovery is host-local and identical across backend invocations.
- Backend-switch idempotency reviewed: the local backend's `terraform.tfstate` writes go through terraform-exec; the docker backend's writes go through `hashicorp/terraform:1.5.7` bind-mounting the same path. Both honor terraform's state-locking + state-consistency guarantees. Switching backends mid-lifecycle (`up --backend local`; `up --backend docker`) on the same workspace is safe in theory; integrator's Phase N e2e step is the live verification.

What the integrator should verify in the live Phase N run (PRD 05 §"Phase N — mixed-mode lifecycle"):

- N3: `roksbnkctl up --auto -w e2e-mixed --var-file …` with `exec.terraform.backend=local` writes state at the expected path.
- N6: `roksbnkctl test throughput -w e2e-mixed` with `exec.iperf3.backend=k8s` runs entirely in cluster.
- N7: `roksbnkctl ibmcloud account show -w e2e-mixed` with `exec.ibmcloud.backend=ssh:jumphost` routes via SSH.
- N9: `roksbnkctl down` works against the same state.

If any of these surface a state-corruption or cred-divergence bug, file a Sprint 7 issue with the e2e log attached; the unit-tier pinning here means the bug is environmental, not a Go-side regression.

## Issue 3 (INFORMATIONAL — terraform local-backend host-tool requirement remains) — accepted

**Severity**: informational
**Status**: ✅ accepted (PLAN.md row §"Gate to Sprint 7" + this sprint's PRIORITY 2 doctor refresh acknowledge `terraform` is the ONE remaining required host install)

Post-Sprint-6 doctor green-by-default refresh: `terraform` is the only required host install. Every other tool roksbnkctl was historically requiring (kubectl, oc, ibmcloud, iperf3, dig) is now internalised:

- kubectl + oc — client-go (`roksbnkctl k *`)
- ibmcloud — bundled tools-ibmcloud image (`--backend docker` / `--backend ssh:<target>`)
- iperf3 — bundled tools-iperf3 image (`--backend k8s`)
- dig — miekg/dns probe library

A future v1.x effort could internalise terraform itself (the `terraform-plugin-go` ecosystem makes it possible to run the IBM provider directly from Go; the upstream Terraform binary would become the docker-backend-only path). PRD 03 §"State concerns" + PLAN.md §"What's deliberately deferred to post-v1.0" already lists this direction as "explore in v1.x"; staff side files nothing further this sprint.

The `--backend docker` for terraform (Sprint 5 staff Issue 1) already provides a "no host terraform required" path that lands the same state file. Users who want the binary-only experience today can run `roksbnkctl up --backend docker` against a workspace; the doctor `terraform` check is the only nag (warning, not error, on a fully docker-backend-driven workflow).

## Verification status

- `go build ./...` ✓ clean
- `go vet ./...` ✓ clean
- `gofmt -d -l .` ✓ clean
- `go test ./...` ✓ all green (added `tools/refgen/cobra-md`, `tools/refgen/tfvars-md`, `internal/cred/resolver_invariance_test.go`, `internal/doctor/doctor_test.go`, `internal/exec/docker_test.go` extensions, `internal/test/dns_test.go` ECS tests)
- `go run ./tools/refgen/cobra-md` ✓ emits valid markdown starting with `# Command reference`; covers all top-level commands (init / up / down / k / ws / cluster / cos / doctor / test / etc.); flag tables sorted alphabetically; parent backlinks emit for subcommands at depth ≥ 3
- `go run ./tools/refgen/tfvars-md` ✓ emits valid markdown starting with `# Terraform variable reference`; covers root module + every submodule's `variables.tf`; sensitive flag honored for `ibmcloud_api_key` + `bigip_password`
- `roksbnkctl doctor` on a stock dev box → couldn't run binary (sandbox blocks execution); unit test `TestHasFailures_StockDevBoxGreen` pins the contract: a host with `terraform` present and nothing else produces zero StatusError rows from `runWithWhy`. Integrator sign-off via `docs/E2E_TEST.md` §"5. Doctor green on a stock dev box".
- `roksbnkctl ibmcloud --backend docker iam oauth-tokens` regression check ✓ unit-test pinned via `TestResolveDockerImageAndArgv` ("ibmcloud prepends binary" case); live verification at integrator e2e (Phase K1-K3 in `scripts/e2e-test-backends.sh`).
- `roksbnkctl test dns --backend k8s --target <name>` regression check ✓ unit-test pinned via the same TestResolveDockerImageAndArgv + `TestDockerImageBinary_MirrorsK8sOverrides` (k8s `jobToolCmdOverride` mirrors docker `dockerImageBinary`); live verification at validator's Phase L-DNS.

## Priorities completed

| Priority | Item | Status |
|---|---|---|
| 1a | `tools/refgen/cobra-md/main.go` + main_test.go | ✓ done |
| 1b | `tools/refgen/tfvars-md/main.go` + main_test.go | ✓ done |
| 1c | Generator smoke tests (output starts with expected H1; known commands/variables present; tables balanced) | ✓ done |
| 2  | Doctor green-by-default refresh (`internal/doctor/doctor.go` + `internal/cli/meta.go` `Long` text + `docs/E2E_TEST.md` item 5) | ✓ done |
| 3  | `MIGRATING.md` top-level file | ✓ done |
| 4  | Phase N Go-side stubs (cred-resolver invariance test; backend-independent state/kubeconfig path code-review verified) | ✓ done |
| 5a | Drop `ENTRYPOINT ["ibmcloud"]` from `tools/docker/ibmcloud/Dockerfile` + update `dockerImageBinary` in `internal/exec/docker.go::resolveDockerImageAndArgv` + extend `jobToolCmdOverride` in `internal/exec/k8s.go` to keep parity + unit test `TestDockerImageBinary_MirrorsK8sOverrides` pinning the invariant | ✓ done |
| 5b | `edns_client_subnet` field in `DNSProbeResult` + extraction from `dns.Msg.IsEdns0()` + unit tests | ✓ done (chapter 21 doc update filed as Issue 1 above) |

## Files created

- `tools/refgen/cobra-md/main.go` + `main_test.go`
- `tools/refgen/tfvars-md/main.go` + `main_test.go`
- `MIGRATING.md` (top-level)
- `internal/cred/resolver_invariance_test.go`
- `internal/doctor/doctor_test.go`

## Files edited

- `internal/cli/root.go` — added exported `RootCommand()` accessor so `tools/refgen/cobra-md` can walk the wired command tree
- `internal/cli/meta.go` — updated `doctorCmd.Long` to reflect the green-by-default behavior + the informational-tool list
- `internal/doctor/doctor.go` — `runWithWhy` now requires only `terraform`; kubectl/oc/ibmcloud/iperf3/dig render as `checkBinaryInformational`; `checkKubeconfigInformational` replaces `checkKubeconfig` for the general path (pre-`up` runs no longer warn); `versionLine` extended to recognize `dig`
- `internal/exec/docker.go` — added `dockerImageBinary` map; `resolveDockerImageAndArgv` now prepends the binary name for tools whose images have no ENTRYPOINT (`ibmcloud`, `roksbnkctl`)
- `internal/exec/docker_test.go` — added `TestResolveDockerImageAndArgv` + `TestDockerImageBinary_MirrorsK8sOverrides` to pin the new contract
- `internal/exec/k8s.go` — `jobToolCmdOverride` now lists both `ibmcloud` and `roksbnkctl` (instead of just `roksbnkctl`) so the k8s Job path mirrors the docker container path post-Dockerfile-ENTRYPOINT-drop; comment block rewritten to explain the cross-backend contract
- `internal/test/dns.go` — new `EDNSClientSubnet` struct + `DNSProbeResult.EDNSClientSubnet` field (`omitempty`); `extractEDNSClientSubnet` helper walks `dns.Msg.IsEdns0()` for an ECS option (RFC 7871); `Probe.Run` sets the field after the Exchange returns
- `internal/test/dns_test.go` — added `TestProbe_EDNSClientSubnet_Echoed` + `TestProbe_EDNSClientSubnet_AbsentWhenNoOPT`
- `tools/docker/ibmcloud/Dockerfile` — dropped `ENTRYPOINT ["ibmcloud"]`; replaced with a comment block referencing the Sprint 6 docker-backend dispatch change
- `docs/E2E_TEST.md` §"5. Doctor green on a stock dev box" — added a Sprint 6 note explaining the refactor

## Items deferred / handed off

- **Chapter 21 + PRD 03 documentation drift now that EDNS Client Subnet is emitted**: filed as Issue 1 above; architect/tech-writer scope. The implementation landed; the doc reference still says "v1.x reserved".
- **Live Phase N mixed-mode lifecycle verification against a real IBM Cloud account**: filed as Issue 2 above; integrator scope (no IBM Cloud account on the sprint VM). The Go-side contract is pinned in unit tests.
- **`go.work`-style internalising of terraform itself (run the IBM provider from Go without a host terraform binary)**: filed as Issue 3 above; v1.x exploration per PLAN.md §"What's deliberately deferred to post-v1.0".
- **Chapter 27 + Chapter 29 output commits**: the generators emit the markdown; the architect commits the generated files into `book/src/27-command-reference.md` and `book/src/29-terraform-variable-reference.md`. Staff scope is the generators themselves, not the rendered output.

## Coordination with parallel agents

- **Architect** is writing chapters 23 / 25 / 26 / 27 / 28 / 29 / 30 / 31 / 32. The cobra-md + tfvars-md generators emit the body of 27 + 29 respectively; the architect runs them at integration time. No file conflicts — staff doesn't touch `book/src/*`.
- **Validator** is wiring Phase I/M/N in `scripts/e2e-test-backends.sh` + `scripts/e2e-test-full.sh`. Staff's cred-resolver invariance test pins the Go-side Phase N contract; validator's e2e steps assert the wire-level behaviour. Staff doesn't touch `scripts/*`.
- **Tech-writer**'s next pass will see Issue 1 (chapter 21 + PRD 03 EDNS doc update) and the EDNS implementation in `internal/test/dns.go::extractEDNSClientSubnet`; small chapter update.

## Summary

7 priorities all green; 3 issues filed (1 doc drift for architect, 1 integrator-scope live verification, 1 v1.x deferral). Build / vet / gofmt / test all clean. The Sprint 5 carry-overs (Dockerfile ENTRYPOINT + EDNS) landed cleanly; the auto-generators are ready for the architect's chapter integration step; the doctor green-by-default contract is pinned in unit tests; `MIGRATING.md` is at the repo root.
