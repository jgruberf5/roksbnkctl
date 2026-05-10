# Sprint 0 — validator issues

## Issue 1: stale `bnkctl` reference in docs/E2E_TEST.md
**Severity**: low
**Status**: open
**Description**: Post-rename sweep missed one occurrence of the old name
in the E2E test plan doc. Line 46 reads "Validates that `cluster
register` correctly discovers and persists the identity of a cluster
bnkctl didn't itself create." — the bare `bnkctl` here should read
`roksbnkctl`. Cosmetic; doesn't affect any code or test runs. The
script `scripts/e2e-test.sh` itself is clean (verified — all references
say `roksbnkctl`, log dir is `/tmp/roksbnkctl-e2e`, env var is
`ROKSBNKCTL`).
**Files affected**: `docs/E2E_TEST.md` (line 46)
**Proposed fix**: One-character edit — replace `bnkctl` with
`roksbnkctl` on that line. Trivial; can ride along with any future doc
PR.

## Issue 2: `BNKCTL_HOME` env var — N/A (no such variable referenced)
**Severity**: informational
**Status**: resolved
**Description**: The validator brief asked to verify
`BNKCTL_HOME` → `ROKSBNKCTL_HOME` was renamed in `scripts/e2e-test.sh`.
Search shows the script never references any `*_HOME` env variable —
state location is hardcoded as `~/.roksbnkctl/...` and there is no
overrideable home env. No action required.
**Files affected**: none
**Proposed fix**: none

## Issue: future testing improvements (roadmap, not bugs)

**Severity**: roadmap
**Status**: informational
**Description**: Survey of the current `internal/` test footprint to
inform Sprint 1+ planning. Not bugs — these are forward-looking notes
about which packages most need test scaffolding additions as new
sprints land. The PLAN.md per-sprint testing additions table already
covers most of this; this entry confirms the current baseline that
those additions build on.

### Current internal/ package test coverage

| Package | Has tests | Test files | Approx LOC ratio (test/source) | Recommended addition |
|---|---|---|---|---|
| `internal/cli` | no | — | 0 / ~2200 | Cobra subcommand wiring is mostly thin glue; defer dedicated unit tests until Sprint 1's `--on` flag adds branching that's worth covering. Integration via `scripts/e2e-test.sh`. |
| `internal/config` | yes | `context_test.go` (13 tests) | ~268 / ~880 | Decent coverage of context resolution. **Sprint 3** cred-resolver work (PRD 04) will add `cred/resolver_test.go` next door — keep `config` unit tests close to the schema. |
| `internal/cos` | no | — | 0 / ~265 | Add table-driven tests for object key/path parsing. **testcontainers-go** with [MinIO image](https://hub.docker.com/r/minio/minio) would let us exercise put/get/delete against a real S3 endpoint — perfect Sprint 3 add when the docker backend lands. |
| `internal/doctor` | no | — | 0 / ~258 | Sprint 0's doctor refactor (staff-engineer) introduces `Check{Name, Status, Detail}` — unit tests can be table-driven over check structs. Add alongside that refactor. |
| `internal/ibm` | yes (one) | `client_test.go` (3 tests) | ~89 / ~860 | Coverage thin. The cluster_config / cluster_discover / cos_instance / identity paths are mostly IBM SDK pass-through, but argument-mapping tests would catch regressions cheaply. **httptest.Server** stubbing IBM endpoints (PLAN.md Sprint 3 mentions this) is the right tool — no testcontainers needed. |
| `internal/k8s` | no | — | 0 / ~340 | Sprint 2 (PRD 02 — kubectl internalization) explicitly adds `client-go/kubernetes/fake` clientset tests. **kind**-based integration tests come in Sprint 4 per the PLAN.md testing pyramid; could also use **testcontainers-go**'s [k3s module](https://golang.testcontainers.org/modules/k3s/) for lighter-weight kube-API in-process testing. |
| `internal/test` | no | — | 0 / ~457 | DNS probe gets miekg-with-stub-server unit tests in Sprint 5. Connectivity probe could be tested against a `httptest.NewTLSServer`. Throughput probe is the one most likely to need testcontainers — an iperf3 server container as the SUT in CI. |
| `internal/tf` | yes | `fetch_test.go` (6), `source_test.go` (4), `vars_test.go` (5) | ~271 / ~700 | Strong coverage of vars + fetch + source resolution. The `terraform.go` runner itself (`Apply`, `Destroy`) is untested and probably out-of-reach for unit testing — terraform-exec needs a real TF binary. **testcontainers-go** with `hashicorp/terraform` image becomes relevant when Sprint 5 lands the docker backend for terraform. |
| `internal/ui` | n/a | — | 0 / ~4 | Stub package (just `doc.go`). Skip. |

### testcontainers-go ranking — most-to-least valuable

1. **`internal/remote` (Sprint 1)** — primary target. `sshd` container is the only sensible way to integration-test the SSH client without a real jumphost. PLAN.md Sprint 1 already calls this out.
2. **`internal/exec` (Sprint 3+)** — docker backend tests use a local Docker daemon directly (PLAN.md mentions this); k8s backend tests use `kind` per PLAN.md, which testcontainers-go's k3s module could replace for faster CI.
3. **`internal/cos`** — MinIO container would give us a self-contained S3 endpoint. Worthwhile when the cos package grows beyond simple pass-through; for now httptest is enough.
4. **`internal/test/throughput`** — iperf3 container as both client and server. Lower priority since the k8s backend test path naturally exercises this.

**Files affected**: forward-looking — no current files
**Proposed fix**: Track per-sprint additions against this table; revisit at Sprint 3 boundary to confirm the cred-resolver/redactor unit tests land alongside the docker-backend integration tests.

---

*No actionable bugs filed beyond Issue 1 (cosmetic doc nit). Issue 2 is
informational confirmation. Everything else above is roadmap.*
