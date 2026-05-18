# Sprint 2 — validator issues

Format matches Sprint 1. `Severity: roadmap` is reserved for non-blocking
forward-looking observations; `low/medium/high/blocker` for actionable
findings.

## Issue 1: fake-clientset coverage narrower than the brief envisaged

**Severity**: low
**Status**: open (informational; explained in test file headers)
**Description**: The validator brief asks for fake-clientset unit tests
covering `Get`, `Describe`, `Apply`, `Delete`, `Logs`, `Exec`,
`PortForward` happy + edge paths. Staff's PRD 02 implementation drives
each of those through `cli-runtime`'s `resource.Builder` (Get,
Describe, Delete) or `BuildRESTConfig`-backed real client builders
(Apply, Logs, Exec, PortForward). `resource.Builder` requires a real
`RESTClientGetter` and reaches a real apiserver — it is **not**
substitutable by `kubernetes/fake` or `dynamic/fake`.

This is the right design choice for the production code: cli-runtime
is the only library that produces kubectl-byte-identical output (the
PRD 02 acceptance criterion), and it tightly couples to a real REST
client because that's how kubectl itself works. The trade-off is
reduced unit-test surface — fake-clientset tests would be testing a
parallel code path that doesn't reflect production behaviour.

What the validator's `_test.go` files cover instead:

- **Pure helpers**: `parseYAMLStream`, `splitYAML`, `applyPatchOptions`,
  `propagationFor`, `ParseSinceDuration`, `IsNotFound`,
  `DefaultKubeconfigPath` — full table-driven coverage.
- **Option validation**: every `*Options.Run(ctx)` entry point has a
  test that confirms it errors clearly on missing required fields
  (PodName, Filename, Args, Command, Ports). These are the regression
  guards against future API drift.
- **Constants drift guard**: `FieldManager == "roksbnkctl"`,
  `InClusterKubeconfigSentinel == "in-cluster"`, `CascadeBackground`
  / `Foreground` / `Orphan` strings match kubectl's spelling.
- **Client builders**: `BuildRESTConfig`, `BuildClientset`,
  `BuildDynamicClient` round-trip a stub kubeconfig file fixture.

End-to-end byte equivalence is the responsibility of the live golden
tests in `internal/k8s/golden_test.go` (build tag `live`), which compare
`roksbnkctl k get -o yaml` to `kubectl get -o yaml` byte-for-byte
(modulo `managedFields/resourceVersion/creationTimestamp`).

**Files affected**: `internal/k8s/{get,describe,apply,delete,logs,exec,
port_forward,client}_test.go`
**Proposed fix**: none — the live golden tests close the coverage gap.
A future Sprint 4 task could spin up a kind cluster in CI to run a
subset of the golden tests on every PR; PLAN.md sequences kind-based
integration into Sprint 4 already (PRD 03's K8s backend tests).

## Issue 2: kind-based CI integration deferred to Sprint 4

**Severity**: roadmap
**Status**: open (forward-looking)
**Description**: The validator brief notes a kind-based integration
job as "lower priority — skip if you run out of time. PLAN.md
sequences this as Sprint 4 territory." We did not add a kind job to
`.github/workflows/ci.yml` this sprint. Justification:

1. The K8s execution backend (PRD 03 §K8s backend) lands in Sprint 4
   and will require the same kind setup — adding it now would mean two
   migrations (Sprint 2: kind + golden subset; Sprint 4: kind + K8s
   backend). One migration, in Sprint 4, is cleaner.
2. The live golden tests `t.Skip` cleanly when no cluster is reachable,
   so they're CI-safe — they just turn into no-ops. Once Sprint 4 lands
   the kind cluster, the golden-test invocation can flip from `make
   test-live` (manual) to a CI step automatically.

**Files affected**: forward-looking
**Proposed fix**: when Sprint 4's staff agent provisions a kind cluster
for the K8s backend integration tier, extend the same job to run a
subset of the golden tests (Node, Pod). Document in
`prompts/sprint4/validator.md` when that prompt is drafted.

## Issue 3: validator unit tests use staff's API but cannot fake `cli-runtime` — relies on golden tests

**Severity**: low
**Status**: open (informational; same root cause as Issue 1)
**Description**: A reasonable reader of the validator brief might
expect the `_test.go` files to import `kubernetes/fake` and
`dynamic/fake`. They don't, because (per Issue 1) staff's chosen API
isn't fake-friendly — the `Run()` methods call `BuildRESTConfig` →
`clientcmd.BuildConfigFromFlags` → real network. Substituting a fake
clientset would require an interface seam that staff's API doesn't
expose.

The pragmatic alternative — what the validator's tests do — is:

- Test pure helpers (parsing, mapping) directly. These were extracted
  out of the `Run()` methods specifically to make them testable.
- Test option validation by calling `Run()` with deliberately invalid
  inputs (missing PodName, empty Args). These return errors before
  reaching the network, so they exercise the `Options` struct surface
  without needing a cluster.
- Test client builders against a stub kubeconfig file (fakes the
  on-disk side, not the network side).

If a future sprint wants higher unit-test coverage of the network-
adjacent paths, the right move is a kind-based integration tier
(see Issue 2), not a pretend interface seam. **This isn't a bug**;
it's the design's natural unit-test shape.

**Files affected**: same as Issue 1
**Proposed fix**: tracked via Issue 2.

## Issue 4: golden test "byte-equivalence" relies on simple line-level diff

**Severity**: low
**Status**: open
**Description**: `internal/k8s/golden_test.go::stripVolatileFields`
does line-level filtering of `managedFields/resourceVersion/
creationTimestamp/generation/uid/selfLink` before comparing the two
YAML outputs. This catches the headline cases (PRD 02 explicitly
mentions "modulo `managedFields/resourceVersion/creationTimestamp`")
but not subtle variations:

- Different YAML key ordering (Go's `yaml.Marshal` and kubectl's may
  differ on map ordering for sub-objects, though both use stable
  ordering in practice).
- Whitespace at end of lines.
- BOM bytes.
- Different `time.RFC3339` precision (kubectl: seconds; client-go: same
  but worth confirming).

If a flake surfaces in any of these, the fix is a more sophisticated
diff (parse both sides as YAML and compare structurally). For Sprint 2's
pre-tag check, the line-level approach is faster to debug — failures
print both outputs to the test log.

**Files affected**: `internal/k8s/golden_test.go`
**Proposed fix**: defer until a real flake surfaces. If the integrator
hits one before tagging v0.8, swap `stripVolatileFields` for a YAML-
parse + cmp.Diff approach (`github.com/google/go-cmp` is already in
the transitive deps via testcontainers-go).

## Issue 5: e2e D3b PATH-strip helper logic — busybox sort/paste portability

**Severity**: low
**Status**: open
**Description**: `scripts/e2e-test.sh` Phase D's new D3b uses a `while
read` loop that filters PATH entries containing `kubectl` or `oc`
executables, then `paste -sd:` to recombine. Tested on Linux (bash 5.x
+ GNU coreutils). Behaviour verified via `DRY_RUN=1` → "env
PATH=<stripped>" line renders. Recent Sprint 1 fixes addressed busybox
`sort` portability in `internal/cli/flo`; the same care applies here:

- `paste -sd:` is busybox-friendly (busybox paste accepts `-sd`).
- The `while read d; do … done` loop avoids `mapfile` (bash 4+ only).
- `[[ -x ... ]]` is bash-specific but the script's shebang is
  `/usr/bin/env bash` so that's fine.

Tested on the validator's WSL2 host (bash 5.2). If we later run e2e on
a busybox-only minimal image (Alpine), retest. Not blocking.

**Files affected**: `scripts/e2e-test.sh phase_D` D3b block
**Proposed fix**: re-test on Alpine/busybox before any sprint that
runs e2e from a slim runner. None needed for Sprint 2.

## Issue 6: Doctor regression check — verified against staff's commit

**Severity**: low
**Status**: resolved (verified)
**Description**: Built `roksbnkctl` against staff's
`internal/doctor/doctor.go` and ran `PATH="/usr/bin:/bin" doctor` (no
kubectl, no oc on the stripped PATH). Output:

```
✓  kubectl  not on PATH (internalised; passthrough still works if installed)
✓  oc       not on PATH (internalised; passthrough still works if installed)
```

Both rows are `✓` (StatusOK), not `⚠` (StatusWarning). PRD 02 §Doctor
update goal met: "a fresh dev box without kubectl/oc should not
produce warnings post-Sprint 2 for everyday roksbnkctl use." Staff's
new helper `checkBinaryInformational` cleanly separates the
"informational, no fix needed" path from the legacy required/optional
path.

**Files affected**: none (verification only)
**Proposed fix**: none.

## Issue 7 (roadmap): kustomize `loadKustomization` integration test

**Severity**: roadmap
**Status**: informational
**Description**: `internal/k8s/apply.go::loadKustomization` calls into
`sigs.k8s.io/kustomize/api/krusty` to build a kustomize base. The
validator unit tests cover `parseYAMLStream` (the post-build YAML
parser) but don't construct a kustomize base on disk — that requires a
multi-file fixture (`kustomization.yaml` + at least one resource), which
is more setup than other tests. A real-world bug in the kustomize
wiring would show up as "wrong number of objects after build", and the
live golden test indirectly catches it via SSA round-trip.

A future Sprint 3 / 4 polish pass could add a `testdata/kustomize-base/`
fixture and a unit test that asserts loadKustomization returns the
expected resources. Not Sprint 2's job per the validator brief.

**Files affected**: forward-looking
**Proposed fix**: track for a future polish sprint; not actionable now.

## Issue 8 (roadmap): kind-based smoke test alongside PRD 03 K8s backend tests

**Severity**: roadmap
**Status**: informational
**Description**: When Sprint 4 lands the K8s execution backend (PRD 03)
and provisions a kind cluster in CI, the same kind cluster makes a
natural home for:

- A subset of `internal/k8s/golden_test.go` (Node, Pod) running on
  every PR, not just pre-tag.
- A smoke test of the new internalised verbs against a real but
  ephemeral cluster.
- The `roksbnkctl k get nodes` PATH-strip check, automated.

Ties into Issue 2's deferral rationale; flagging here so Sprint 4's
validator picks it up.

**Files affected**: forward-looking
**Proposed fix**: track for Sprint 4.

---

*Total filed: 8 issues — 0 blocker, 4 low (test surface, e2e
portability, doctor verified, golden diff resolution), 1 informational
(doctor verified resolved), 3 roadmap (kind-CI in Sprint 4, kustomize
fixture, kind smoke).*

## Verification summary

- `go build ./...` clean
- `go test ./...` clean — 30+ new unit tests in `internal/k8s/`
  pass alongside the existing `internal/{config,ibm,remote,tf}` suites
- `go test -tags live -timeout 5m ./internal/k8s/...` compiles cleanly;
  not run against a live cluster (no KUBECONFIG pointed at one in the
  validator's environment)
- `bash -n scripts/e2e-test.sh` clean
- `DRY_RUN=1 PHASE_FROM=D ./scripts/e2e-test.sh` shows D3 (`k get pods
  -n f5-bnk`) and D3b (`k get nodes (PATH-stripped)`) cleanly
- `gofmt -d -l .` clean for all touched Go files
- Doctor on a kubectl-less PATH: no warnings for kubectl/oc rows
  (verified against staff's commit)
