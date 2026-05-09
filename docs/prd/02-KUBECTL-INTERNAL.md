# PRD 02 — Phase 2: kubectl internalization (drop the host kubectl/oc requirement)

> Prerequisites: Phase 1 not strictly required (this is independent), but landing Phase 1 first makes Phase 2's `--on` interaction simpler.
>
> Estimated effort: medium (~1500 LOC); 2 weeks of focused work.

## Goal

Eliminate the `kubectl` and `oc` host-binary requirement for everyday `roksbnkctl` use by implementing the BNK-relevant subset of their command surface natively in Go via `client-go` and `openshift/client-go`. Preserve the existing passthrough commands as opt-in escape hatches for power users.

## Why

- `client-go` is already a transitive dependency (used in `internal/test/throughput.go` to deploy the iperf3 fixture pod).
- `roksbnkctl doctor` flags missing `kubectl` as an optional warning today, but when users skip the install, they hit it later — `roksbnkctl logs`, `roksbnkctl status` reachability checks, and any post-apply diagnostic break.
- The "single binary" promise: install one Go binary and get cluster ops + deployment + tests, no further toolchain.
- Output format compatibility (`-o yaml`, `-o json`, `-o jsonpath=...`) matches user muscle memory; `cli-runtime` gives us this for free.

## Scope

### Core verbs (Phase 2.0)

The BNK-relevant subset that roksbnkctl uses internally + the high-frequency user-facing operations:

| Verb | Surface | Replaces |
|---|---|---|
| `roksbnkctl get <resource> [name] [-n <ns>] [-o <fmt>]` | typed resources + unstructured fallback | `kubectl get` / `oc get` |
| `roksbnkctl describe <resource> <name>` | `k8s.io/kubectl/pkg/describe` | `kubectl describe` |
| `roksbnkctl apply -f <file-or-dir>` | server-side apply (SSA) with kustomize base resolution | `kubectl apply` |
| `roksbnkctl delete <resource> <name>` | with `--force`, `--grace-period`, `--cascade` | `kubectl delete` |
| `roksbnkctl logs <pod-or-component> [-n <ns>] [-c <container>] [-f]` | extends existing component-aware version | `kubectl logs` |
| `roksbnkctl exec <pod> -- <cmd>` | SPDY executor | `kubectl exec` |
| `roksbnkctl port-forward <pod> <local>:<remote>` | SPDY port-forwarder | `kubectl port-forward` |

### OpenShift extensions (Phase 2.1)

Adds OpenShift-specific resource types via `github.com/openshift/client-go`. Same verb shape; just expands the resource list `get/describe/delete` recognize:

- `Project`, `ProjectRequest`
- `Route`
- `ImageStream`, `ImageStreamTag`
- `BuildConfig`, `Build`
- `DeploymentConfig`
- `SecurityContextConstraints` (SCC)

### Out of scope

- `kubectl edit`, `kubectl patch` (low frequency for BNK ops; users with a need can use the passthrough)
- `kubectl rollout`, `kubectl scale` (not part of the BNK happy path)
- `kubectl drain`, `kubectl cordon`, `kubectl taint` (cluster admin operations; not roksbnkctl's role)
- `kubectl explain`, `kubectl api-resources`, `kubectl api-versions` (informational; passthrough fine)
- `kubectl auth can-i`, `kubectl certificate` (security ops; passthrough fine)
- `kubectl run`, `kubectl create` (imperative resource creation; SSA / `apply -f` is the recommended path)

### Preservation

The existing `roksbnkctl kubectl <args...>` and `roksbnkctl oc <args...>` passthroughs **stay** as escape hatches. After Phase 2:

- If host `kubectl` exists: passthrough works as today
- If host `kubectl` is missing: passthrough errors with "kubectl not on PATH; use `roksbnkctl get` etc. for the in-process path, or install kubectl"
- Doctor downgrades `kubectl`/`oc` from "needed" to "informational"

## Design

### Library

| Library | Purpose |
|---|---|
| `k8s.io/client-go` | core API client, REST mappers, dynamic client for unstructured |
| `k8s.io/cli-runtime` | `genericclioptions.PrintFlags`, `resource.Builder`, kubectl-compatible printer logic |
| `k8s.io/kubectl/pkg/describe` | reuse the describe library kubectl uses internally — 1:1 output |
| `sigs.k8s.io/kustomize/api` | `apply -f <dir>` resolves a kustomize base before applying |
| `github.com/openshift/client-go` | OpenShift API typed clients (Phase 2.1) |
| `github.com/openshift/api` | OpenShift CRD types |

### Code organization

```
internal/k8s/
  client.go        # existing — extended with builder helpers
  get.go           # generic resource fetcher (typed + unstructured)
  describe.go      # delegates to kubectl/pkg/describe
  apply.go         # SSA, kustomize-aware
  delete.go        # cascade-aware deletion
  logs.go          # existing — extended with raw pod-name path
  exec.go          # SPDY exec
  port_forward.go  # SPDY port-forward
internal/cli/
  k_get.go         # cobra wiring for roksbnkctl get
  k_describe.go    # ...
  k_apply.go       # ...
  k_delete.go      # ...
  k_logs.go        # extends existing
  k_exec.go        # disambiguate from existing exec (host) verb
  k_port_forward.go
```

### Disambiguating `roksbnkctl exec`

Today `roksbnkctl exec <cmd>` runs on the host. After Phase 2, `roksbnkctl exec <pod> -- <cmd>` runs in a pod. Three options:

**Option A** — name collision detection: if first arg matches an existing pod name in the current namespace, treat as cluster-exec; else host-exec. **Magic; surprising.**

**Option B** — separate verb: `roksbnkctl k exec <pod> -- <cmd>` for cluster, `roksbnkctl exec <cmd>` for host. **Clean; one extra char.**

**Option C** — explicit flag: `roksbnkctl exec --pod <name> -- <cmd>` for cluster, bare for host. **Verbose but unambiguous.**

**Recommendation: Option B**. Introduce `roksbnkctl k` as a parent for k8s-internal verbs:
- `roksbnkctl k get ...`
- `roksbnkctl k apply ...`
- `roksbnkctl k logs ...`
- `roksbnkctl k exec ...`
- `roksbnkctl k port-forward ...`
- `roksbnkctl k describe ...`
- `roksbnkctl k delete ...`

Top-level shortcuts for the most common verbs (`get`, `apply`, `logs`) so users typing `roksbnkctl get pods` don't have to learn the `k` prefix. The `roksbnkctl exec` (host), `roksbnkctl kubectl` (passthrough), and `roksbnkctl k exec` (cluster) all coexist without ambiguity.

### Output formatting compatibility

Use `cli-runtime`'s `genericclioptions.PrintFlags` directly. This makes:

- `-o yaml` produce kubectl-byte-identical YAML
- `-o json` produce kubectl-byte-identical JSON
- `-o wide` produce the same column set
- `-o name` produce `<resource>/<name>` lines
- `-o jsonpath=...` and `-o go-template=...` work via the same library

A regression test compares `roksbnkctl get nodes -o yaml` to `kubectl get nodes -o yaml` and asserts byte equality (modulo timestamps).

### Apply implementation

Server-side apply (SSA) with field-manager `roksbnkctl`. For directory inputs:

1. Detect `kustomization.yaml` → build via `sigs.k8s.io/kustomize/api`
2. Else recurse `*.yaml`, parse each
3. Apply each resource via SSA with `force-conflicts=true` only if `--force` flag is set

This matches `kubectl apply -k` semantics for kustomize bases and `kubectl apply -f` for plain directories.

### Logs implementation

Extends the existing component-aware `roksbnkctl logs flo` (etc.) with raw pod-name fallback:

- `roksbnkctl logs flo` — looks up component (existing label-selector path)
- `roksbnkctl logs <pod-name>` — direct pod logs (matches kubectl)
- `-c <container>`, `-f`, `--previous`, `--since`, `--tail` flags all supported

## Implementation tasks

1. **Extract shared client builder** in `internal/k8s/client.go`:
   - `Client(kubeconfig path) (*kubernetes.Clientset, error)`
   - `DynamicClient(...)` for unstructured
   - `OpenShiftClient(...)` for Phase 2.1
   - In-cluster fallback (for use by Phase 3's k8s backend pod)

2. **Wire `cli-runtime` builder** for resource lookup by `<type>/<name>` or `-f <file>`

3. **`roksbnkctl k get`** — accepts plural resource names, label selectors, `-A`, `-o`, etc. Use `cli-runtime`'s `resource.Builder` to do the heavy lifting.

4. **`roksbnkctl k describe`** — call `describe.GenericDescriberFor(...)` from `kubectl/pkg/describe` and write its output

5. **`roksbnkctl k apply -f`** — SSA via dynamic client, with kustomize base support

6. **`roksbnkctl k delete`** — cascade options, grace period, finalizer waiting

7. **`roksbnkctl k logs`** — extends existing `internal/cli/inspect.go logsCmd` with the raw pod-name path

8. **`roksbnkctl k exec`** — uses `remotecommand.NewSPDYExecutor` for the bidirectional stream

9. **`roksbnkctl k port-forward`** — uses `portforward.New(...)`; signal handling on Ctrl+C for graceful close

10. **Top-level shortcuts**: `roksbnkctl get`/`apply`/`logs` aliased to `k get`/`k apply`/`k logs`

11. **OpenShift register** (Phase 2.1): scheme registration for OpenShift types so `roksbnkctl k get projects` works

12. **Doctor update**: drop kubectl/oc from required; add an "internalized" green check showing client-go version

13. **Test fixtures**: golden-file tests comparing `roksbnkctl k get -o yaml` byte-for-byte against `kubectl get -o yaml` for representative resources (Node, Pod, Service, ConfigMap)

## Acceptance criteria

- `roksbnkctl k get nodes` works against a live ROKS cluster without `kubectl` on PATH; output matches `kubectl get nodes` byte-for-byte (with `-o yaml` strict comparison; with default tabular, column set matches)
- `roksbnkctl k apply -f <kustomize-dir>` is functionally equivalent to `kubectl apply -k <dir>`
- `roksbnkctl k logs <pod> -f` streams logs identically to `kubectl logs -f <pod>`
- `roksbnkctl k exec <pod> -- bash -c 'echo hi'` returns "hi" + exit 0
- `roksbnkctl k port-forward <pod> 8080:80` makes localhost:8080 reach pod's :80
- `roksbnkctl k get projects` (after Phase 2.1) lists OpenShift projects against a ROKS cluster
- Doctor on a host without kubectl shows green for the internalized k8s path
- E2E tests pass with `kubectl` removed from `$PATH` (Phase 5 covers this in Phase J)

## Open questions

- **Bash completion**: kubectl has rich completion (`kubectl completion bash`). Worth replicating for `roksbnkctl k`? Cobra completion gives us the verb list for free; resource-name completion needs a live API call. Defer to Phase 2.x.
- **`kubeconfig` discovery order**: kubectl uses `--kubeconfig` flag → `KUBECONFIG` env → `~/.kube/config`. roksbnkctl currently uses workspace-local kubeconfig at `~/.roksbnkctl/<ws>/state/kubeconfig`. Reconcile by: workspace-local first, then `KUBECONFIG`, then `~/.kube/config`. Document clearly.
- **Cluster-scoped vs namespace-scoped**: `roksbnkctl k get pods` defaults to namespace `default`; `roksbnkctl k get nodes` is cluster-scoped. Auto-detect from the resource type.
- **CRDs**: BNK ships several CRDs (`BIGIPNextController`, `CNEInstance`, etc.). They register with the API server post-deploy. `roksbnkctl k get cneinstances` should work without a hardcoded list — discovery via the dynamic client. Confirm this works against late-registered CRDs.

## Related work

- The k8s backend in [PRD 03](./03-EXECUTION-BACKENDS.md) reuses the in-cluster client builder from this PRD's `internal/k8s/client.go`
- Phase 5's [E2E plan](./05-E2E-TEST-PLAN.md), Phase J, validates the kubectl-not-on-PATH scenario
- Doctor command updates ripple into [PRD 00](./00-OVERVIEW.md)'s success criteria
