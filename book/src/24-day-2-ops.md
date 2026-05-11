# Day-2 ops: status, logs, k get/apply/exec

This is the chapter to read after the cluster is up and BNK is deployed and you're now living with the result. Most day-2 work is the small stuff: read pod state, tail logs, apply a manifest, port-forward to a service, exec into a pod. Sprint 2 internalises all of those into native Go via [`client-go`](https://pkg.go.dev/k8s.io/client-go) so you no longer need `kubectl` on `PATH` for the everyday workflow.

The full design rationale lives in [PRD 02](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/02-KUBECTL-INTERNAL.md). This chapter is the user-facing surface — the canonical "what's the kubectl-equivalent in `roksbnkctl`?" reference.

## Why internalise

Three reasons, in order of weight:

1. **Single binary.** `roksbnkctl` is meant to be the one thing you install. After Sprint 2, the only required external prerequisite for the happy path is `terraform`. Everything else — `kubectl`, `oc`, `iperf3`, `dig` — is either built-in or an optional escape hatch.
2. **No version skew.** The vendored `client-go` matches the kube API the bundled HCL targets. You can't accidentally use `kubectl` 1.20 against a 1.28 cluster and have its print column heuristics go sideways.
3. **First-class output formats.** `cli-runtime` gives byte-identical `-o yaml`/`-o json`/`-o jsonpath` output to `kubectl`. The validator agent's golden-file tests in [`internal/k8s/golden_test.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/k8s/golden_test.go) assert this for representative resources.

## The `k` command tree

All internalised verbs live under `roksbnkctl k`:

```
roksbnkctl k get          # fetch resources
roksbnkctl k describe     # human-readable detail
roksbnkctl k apply        # server-side apply from file/dir
roksbnkctl k delete       # delete with cascade options
roksbnkctl k logs         # pod or component logs
roksbnkctl k exec         # exec into a pod (SPDY)
roksbnkctl k port-forward # forward local ports to a pod (SPDY)
```

Two of those have **top-level shortcuts** for muscle-memory convenience — the verbs you'd type a hundred times a day:

```
roksbnkctl get  ↔  roksbnkctl k get
roksbnkctl logs ↔  roksbnkctl k logs
```

`apply`, `exec`, `delete`, `describe`, and `port-forward` only work under the `k` prefix.

Two verbs are **deliberately not aliased** to avoid shadowing existing top-level commands:

- **`roksbnkctl apply`** is the existing top-level lifecycle verb that runs `terraform apply` against the workspace (Sprint 0/1 surface). Adding a second `apply` would shadow it and break `roksbnkctl up` / `roksbnkctl apply` muscle memory. Use `roksbnkctl k apply -f ...` explicitly for the Kubernetes-side server-side apply.
- **`roksbnkctl exec`** runs a command on the **host** with the workspace's env loaded (Sprint 1's host-exec verb — see [Chapter 16](./16-on-flag-ssh-jumphosts.md), specifically the "Working examples" section). `roksbnkctl k exec` runs in a pod. The split keeps both meanings unambiguous without surprising name-collision behaviour.

## kubectl/oc passthroughs stay as escape hatches

The existing `roksbnkctl kubectl <args...>` and `roksbnkctl oc <args...>` passthroughs are **preserved** post-Sprint 2. They still shell out to the host binary (with the workspace's `KUBECONFIG` and credentials loaded) for anything outside the internalised subset.

When to reach for the passthrough:

| Use case | Why passthrough |
|---|---|
| `kubectl rollout` (status/history/undo/restart) | Out of scope for v1.0; PRD 02 explicitly defers |
| `kubectl scale` / `kubectl autoscale` | Out of scope; passthrough is fine |
| `kubectl edit` / `kubectl patch` | Low frequency for BNK ops; out of scope for v1.0 |
| `kubectl auth can-i` / RBAC introspection | Out of scope; passthrough is fine |
| `kubectl drain` / `cordon` / `taint` | Cluster admin operations; not roksbnkctl's role |
| `kubectl run` / `kubectl create` | Imperative resource creation; use `k apply -f` instead |
| `oc adm` / `oc image` / OpenShift admin verbs | Niche enough to defer; passthrough |
| Niche flag combos | Anything not in the internalised verb's flag set |

If `kubectl` is missing from `PATH`, the passthrough errors with:

```
Error: kubectl not on PATH; use `roksbnkctl k get/apply/...` for the in-process path,
       or install kubectl
```

Same for `oc`. The doctor check (post-Sprint 2) treats both as **informational** rather than warnings — see [Chapter 5 — Doctor](./05-doctor.md).

## Worked examples

The verbs in everyday order. Every example below assumes `roksbnkctl k` and accepts the top-level alias where one exists.

### `roksbnkctl k get`

The most-used verb. Resource type + optional name + optional flags:

```bash
# All pods in the default namespace
roksbnkctl k get pods

# Pods in a specific namespace
roksbnkctl k get pods -n f5-bnk

# Pods across all namespaces
roksbnkctl k get pods -A

# A specific pod by name
roksbnkctl k get pod flo-controller-abc123 -n f5-bnk

# Label selector
roksbnkctl k get pods -A -l app.kubernetes.io/name=f5-lifecycle-operator

# Cluster-scoped resources (no namespace)
roksbnkctl k get nodes
roksbnkctl k get storageclasses
```

Output formats — these match `kubectl` byte-for-byte:

```bash
roksbnkctl k get pods -n f5-bnk -o yaml
roksbnkctl k get pods -n f5-bnk -o json
roksbnkctl k get pods -n f5-bnk -o wide
roksbnkctl k get pods -n f5-bnk -o name
roksbnkctl k get pods -n f5-bnk -o jsonpath='{.items[*].metadata.name}'
roksbnkctl k get pods -n f5-bnk -o go-template='{{range .items}}{{.metadata.name}}{{"\n"}}{{end}}'
```

Plural / singular / shortname handling comes from the cluster's `RESTMapper` via the discovery client, so `pod`, `pods`, `po` all work and pick up CRDs without a hardcoded list. `roksbnkctl k get cneinstances` (a BNK CRD) works as soon as the CRD is registered with the API server — no rebuild required.

Using the top-level alias:

```bash
roksbnkctl get pods -A
```

### `roksbnkctl k describe`

Delegates to `k8s.io/kubectl/pkg/describe` — the same library `kubectl` uses internally. Output is identical to `kubectl describe`:

```bash
roksbnkctl k describe pod flo-controller-abc123 -n f5-bnk
roksbnkctl k describe node 10.243.0.4
roksbnkctl k describe service flo-webhook -n f5-bnk
roksbnkctl k describe cneinstance my-instance -n f5-bnk
```

The describe output's "Events" section is especially useful for debugging stuck resources — pod scheduling failures, image pull errors, finaliser hangs all surface here.

### `roksbnkctl k apply`

Server-side apply (SSA) with field-manager `roksbnkctl`. Inputs:

```bash
# Single file
roksbnkctl k apply -f pod.yaml

# Directory of YAMLs (recurses *.yaml)
roksbnkctl k apply -f manifests/

# Kustomize base (auto-detected if kustomization.yaml is present)
roksbnkctl k apply -f my-kustomize-base/

# stdin
cat pod.yaml | roksbnkctl k apply -f -

# Apply into a specific namespace (overrides metadata.namespace if absent)
roksbnkctl k apply -f manifests/ -n f5-bnk

# Force conflicts (SSA force-conflicts=true)
roksbnkctl k apply -f manifests/ --force
```

There is **no** top-level `roksbnkctl apply` alias for this verb — `roksbnkctl apply` is the lifecycle command that runs `terraform apply`. Always use `roksbnkctl k apply` for the Kubernetes-side apply.

Differences from `kubectl apply`:

- **Always SSA.** Field manager is `roksbnkctl`. Client-side apply is not supported.
- **Kustomize auto-detect.** A directory containing `kustomization.yaml` is built via `sigs.k8s.io/kustomize/api` before applying — no `-k` flag needed.
- **`--force` maps to SSA's `force-conflicts=true`.** Without it, conflicts with another field manager produce a clean error rather than silently winning.

For a vanilla `kubectl apply -f` workflow, the behaviour is functionally identical. For workflows that depend on client-side three-way merge or specific `--server-side` flag combinations, fall back to the passthrough.

### `roksbnkctl k delete`

Cascade-aware deletion via the dynamic client:

```bash
# Delete by name
roksbnkctl k delete pod flo-controller-abc123 -n f5-bnk

# Cascade: orphan, background (default), foreground
roksbnkctl k delete deployment flo -n f5-bnk --cascade=foreground

# Force (bypass graceful deletion; immediate)
roksbnkctl k delete pod stuck-pod -n f5-bnk --force

# Custom grace period (seconds)
roksbnkctl k delete pod my-pod -n f5-bnk --grace-period=5
```

Use `--cascade=foreground` when you want to wait for owned resources (Pods owned by a Deployment, etc.) to be deleted before the parent disappears — useful for tearing down BNK trial CRs cleanly so finalisers run in order.

### `roksbnkctl k logs` and `roksbnkctl logs`

Two paths, one verb. The component-aware path was introduced in Sprint 1 for BNK-specific workflows; the raw pod-name path is new in Sprint 2.

**Component-aware** (existing — by label selector):

```bash
roksbnkctl logs flo                # F5 Lifecycle Operator (label selector under the hood)
roksbnkctl logs cis                # F5 BNK CIS controller
roksbnkctl logs cert-manager       # cert-manager
roksbnkctl logs cneinstance        # BIG-IP TMM data plane pods
```

**Raw pod-name** (new in Sprint 2):

```bash
roksbnkctl k logs flo-controller-abc123 -n f5-bnk
```

Common flags (both paths):

```bash
-f, --follow              # stream live (kubectl logs -f)
-c, --container <name>    # specific container in a multi-container pod
--previous                # logs from the previous instance (after a crash)
--since=10m               # only logs in the last 10 minutes
--tail=100                # last N lines only
```

Top-level alias:

```bash
roksbnkctl logs flo -f --since=5m
```

If the named first arg matches one of the well-known BNK components (`flo`, `cis`, `cert-manager`, `cneinstance`), the component-aware path is used; otherwise it's treated as a pod name. The component map lives in [`internal/cli/inspect.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/cli/inspect.go) and is keyed off the upstream chart's default labels.

### `roksbnkctl k exec`

SPDY exec into a pod. Same semantics as `kubectl exec`:

```bash
# One-shot command
roksbnkctl k exec flo-controller-abc123 -n f5-bnk -- ls -la /

# stdin attached
roksbnkctl k exec flo-controller-abc123 -n f5-bnk -i -- cat /etc/hostname

# Interactive PTY (the bash-style use)
roksbnkctl k exec flo-controller-abc123 -n f5-bnk -i -t -- bash

# Specific container in a multi-container pod
roksbnkctl k exec flo-controller-abc123 -n f5-bnk -c sidecar -- env
```

The `-i` and `-t` flags map directly to `kubectl exec`'s `-i` (stdin) and `-t` (PTY). For `top` / `bash` / interactive Python sessions, pass both.

There is **no** `roksbnkctl exec` (top-level) alias — `roksbnkctl exec` runs on the host. See ["Disambiguating `roksbnkctl exec`" in PRD 02](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/02-KUBECTL-INTERNAL.md#disambiguating-roksbnkctl-exec).

### `roksbnkctl k port-forward`

SPDY port-forward to a pod:

```bash
# Forward localhost:8080 → pod's :80
roksbnkctl k port-forward flo-controller-abc123 -n f5-bnk 8080:80

# Multiple ports
roksbnkctl k port-forward flo-controller-abc123 -n f5-bnk 8080:80 8443:443

# Random local port (let the kernel pick)
roksbnkctl k port-forward flo-controller-abc123 -n f5-bnk :80
```

`Ctrl+C` closes the tunnel cleanly — no orphaned local listeners. The forward survives idle (reads/writes are bidirectional); it's torn down only on signal or pod restart.

For a Service rather than a Pod, port-forward via the Service's underlying pod or use `kubectl port-forward svc/<name>` through the passthrough — Service-targeted port-forwarding is currently passthrough-only.

## Output format compatibility

The biggest user-visible promise: **`-o yaml` / `-o json` / `-o wide` / `-o jsonpath` produce the same bytes as `kubectl`**, modulo a small set of timestamp-and-resourceVersion fields that change between calls.

Concretely, the validator agent's golden-file tests at [`internal/k8s/golden_test.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/k8s/golden_test.go) capture `kubectl get <resource> -o yaml` and `roksbnkctl k get <resource> -o yaml` against a live ROKS cluster and `diff` them, ignoring:

- `metadata.managedFields` (ordering varies between callers; not user-visible)
- `metadata.resourceVersion` (monotonic counter; changes on every read)
- `metadata.creationTimestamp` (set server-side; not under our control)

Anything else differing is a test failure. The covered resources at v1.0 are Node, Pod, Service, ConfigMap — representative both of cluster-scoped (Node) and namespace-scoped (Pod, Service, ConfigMap), and of the typed-client (Node, Pod, Service) and dynamic-client (anything via `cli-runtime`'s `resource.Builder`) paths.

Run them locally with:

```bash
make test-live
```

…against a `KUBECONFIG` that points at a real ROKS cluster. They're **not** part of the unit-test CI run because they need a live cluster; the integrator runs them before tagging a release. Documented in CONTRIBUTING.md.

## OpenShift extensions

Beyond the core kubectl-equivalent verbs, ROKS clusters surface OpenShift-specific resource types — `Project`, `Route`, `ImageStream`, `BuildConfig`. **`roksbnkctl k get` discovers these natively today** via the dynamic client + RESTMapper path (the cluster advertises them through the API discovery doc; the deferred-discovery mapper picks them up):

```bash
roksbnkctl k get projects                    # OpenShift projects (vs Kubernetes namespaces)
roksbnkctl k get routes -n f5-bnk            # OpenShift Routes (vs Ingress)
roksbnkctl k get imagestreams -n f5-bnk      # OpenShift ImageStreams
roksbnkctl k get buildconfigs                # BuildConfigs (mostly empty in BNK trials)
```

Same verb shape (`get` / `describe` / `delete`); the dynamic-client + RESTMapper combination handles type discovery without needing a per-type Go-side scheme registration.

Phase 2.1 of PRD 02 adds **typed clients** via `github.com/openshift/client-go` for nicer printing and `describe` integration of these resources. This is on the v1.x roadmap (see [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) §"What's deliberately deferred to post-v1.0"). Until typed clients land, `roksbnkctl k get/describe` still works against OpenShift CRDs — just with the generic unstructured printer. If you want richer per-type output today, fall back to the `oc` passthrough:

```bash
roksbnkctl oc get projects                   # typed-client output today
roksbnkctl oc describe route f5-bnk-svc      # typed Route fields
```

## Doctor change recap

A reminder of what changed in Sprint 2's doctor (covered in [Chapter 5](./05-doctor.md)):

- **`kubectl`** — was "needed (warning when missing)"; now **informational** (no warning when missing).
- **`oc`** — same downgrade.

A fresh dev box without `kubectl` / `oc` installed should run `roksbnkctl doctor` and see green-or-informational across the board for the everyday workflow. The host-binary requirement is gone; the binaries are nice-to-have for the passthroughs.

## kubectl muscle-memory cheat sheet

A reader migrating from `kubectl` should be able to use this section as a Rosetta Stone:

| `kubectl ...` | `roksbnkctl ...` |
|---|---|
| `kubectl get pods` | `roksbnkctl get pods` (or `roksbnkctl k get pods`) |
| `kubectl get pods -A` | `roksbnkctl get pods -A` |
| `kubectl get pods -o yaml` | `roksbnkctl get pods -o yaml` |
| `kubectl describe pod <name>` | `roksbnkctl k describe pod <name>` |
| `kubectl apply -f manifests/` | `roksbnkctl k apply -f manifests/` |
| `kubectl apply -k overlay/` | `roksbnkctl k apply -f overlay/` (auto-detects kustomize) |
| `kubectl delete pod <name>` | `roksbnkctl k delete pod <name>` |
| `kubectl logs <pod> -f` | `roksbnkctl logs <pod> -f` (or `roksbnkctl k logs <pod> -f`) |
| `kubectl exec -it <pod> -- bash` | `roksbnkctl k exec <pod> -i -t -- bash` |
| `kubectl port-forward <pod> 8080:80` | `roksbnkctl k port-forward <pod> 8080:80` |
| `kubectl rollout status deploy/foo` | `roksbnkctl kubectl rollout status deploy/foo` (passthrough) |
| `kubectl edit deployment foo` | `roksbnkctl kubectl edit deployment foo` (passthrough) |
| `kubectl scale deployment foo --replicas=3` | `roksbnkctl kubectl scale deployment foo --replicas=3` (passthrough) |
| `oc projects` | `roksbnkctl k get projects` (works today via dynamic-client) or `roksbnkctl oc projects` for typed-client output |

The general pattern: if it's `get` / `describe` / `apply` / `delete` / `logs` / `exec` / `port-forward` against a typed or unstructured Kubernetes resource, the internalised verb is the right answer. Anything else, fall back to the passthrough.

## Cross-references

- [Chapter 5 — Doctor](./05-doctor.md) — the kubectl/oc downgrade in context.
- [Chapter 6 — Workspaces](./06-workspaces.md) — the `KUBECONFIG` resolution chain that powers every `k <verb>`.
- [Chapter 16 — The `--on` flag](./16-on-flag-ssh-jumphosts.md) — `--on` plus the passthroughs for customer-firewalled scenarios.
- [PRD 02](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/02-KUBECTL-INTERNAL.md) — the design rationale and acceptance criteria for the work in this chapter.
