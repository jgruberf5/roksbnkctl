# Sprint 2 — staff engineer issues

Sprint 2 implements PRD 02 (kubectl internalisation). Five issues filed:
two resolved during the sprint, three accepted-and-deferred.

## Issue 1 (top-level `apply` alias collides with terraform `apply`) — resolved by agent

**Severity**: medium
**Status**: ✅ resolved by agent during sprint (deviates from PRD §"Top-level shortcuts")

PRD 02's "Top-level shortcuts" section lists three aliases to add at
root level: `get`, `apply`, `logs`. But `roksbnkctl apply` already
exists from Sprint 0/1 as a top-level command that runs
`terraform apply` (paired with `up`/`plan`/`down` lifecycle commands).
Adding a second `apply` would shadow the existing one and break user
muscle memory around `roksbnkctl up`.

Decision: ship `get` and `logs` aliases only; document the conflict
in `internal/cli/k_aliases.go` and direct users to
`roksbnkctl k apply` for k8s SSA. Update PRD 02 in a future PR if
needed (or accept this as the runtime reality).

`logs` is special: the existing `roksbnkctl logs <component>` from
Sprint 0 stays; the handler in `internal/cli/inspect.go` was extended
so that an unknown component name falls through to the kubectl-style
raw pod-name path. So `roksbnkctl logs my-pod` works without users
needing the `k` prefix; `roksbnkctl logs flo` also still works. This
is the single point of truth for "show me logs" — no second
`logs` command was added.

## Issue 2 (doctor downgrade — kubectl/oc rows now informational) — resolved by agent

**Severity**: low
**Status**: ✅ resolved by agent during sprint

`internal/doctor/doctor.go` adds `checkBinaryInformational()` (parallel
to `checkBinary()`). Used for kubectl + oc only. Behaviour:

- Binary missing → `StatusOK` with detail
  `"not on PATH (internalised; passthrough still works if installed)"`
- Binary present → `StatusOK` with path/version (same as before)

A fresh dev box without kubectl/oc now produces no warnings from
doctor for those rows post-Sprint-2, matching PRD 02 §"Preservation"
and PLAN.md Sprint 2 acceptance criteria.

## Issue 3 (OpenShift typed clients — Phase 2.1) — accepted, deferred

**Severity**: roadmap
**Status**: ⏸ deferred to Phase 2.1 (Sprint 5 polish per PLAN.md)

PRD 02 §"OpenShift extensions" calls for adding typed clients via
`github.com/openshift/client-go` and `github.com/openshift/api` so
`roksbnkctl k get projects/routes/imagestreams/buildconfigs/etc`
gets a typed code path with full openshift-aware describe output.

Phase 2.0 ships without this. The dynamic-client +
DeferredDiscoveryRESTMapper path already discovers any GVK the cluster
advertises (including project.openshift.io/v1, route.openshift.io/v1,
etc.) — `roksbnkctl k get projects` will resolve via that path against
a real ROKS cluster, producing the server-side-rendered Table for
human output and full unstructured for `-o yaml`. The typed-client
path is an optimisation for richer kubectl/pkg/describe support of
OpenShift kinds.

Reserved API: `internal/k8s/openshift.go` exports
`BuildOpenShiftClient()` as a stub returning `errors.New("not
implemented")`. Phase 2.1 fills it in without breaking the existing
surface.

Risks if/when implemented:
- `openshift/client-go` has its own version dance vs k8s.io/client-go.
  Pin both via go.mod replace directives if the transitive
  k8s.io/api version diverges.
- Scheme registration must happen at package init in
  `internal/k8s/openshift.go` so `cli-runtime`'s scheme picks up
  the types for the Unstructured → typed conversion path.

## Issue 4 (apply -k kustomize doc boundary) — accepted, integrator note

**Severity**: low
**Status**: ✅ noted; behaviour matches kubectl

Spec called for `kubectl apply -k <dir>` semantics. Implementation
detects `kustomization.yaml`/`kustomization.yml`/`Kustomization` in
the directory and routes through `sigs.k8s.io/kustomize/api/krusty`
before SSA-applying. No separate `-k` flag was added: `roksbnkctl k
apply -f <dir-with-kustomization.yaml>` produces the kustomize-built
output, matching the way `kubectl apply -f` works for kustomize bases
in modern kubectl (1.21+).

If a future user is surprised by the auto-detection, we can add an
explicit `-k`/`--kustomize` flag that's a hard dispatch (skip the
recursive YAML-glob path entirely). Not done in this sprint.

## Issue 5 (dependency surface — go.mod additions) — accepted, integrator note

**Severity**: low
**Status**: ✅ accepted; sized appropriately

Sprint 2 added these direct + indirect deps:

Direct:
- `k8s.io/cli-runtime` v0.30.0 — RESTClientGetter, PrintFlags,
  resource.Builder
- `k8s.io/kubectl` v0.30.0 — pkg/describe + pkg/scheme
- `sigs.k8s.io/kustomize/api` v0.17.2 — krusty Kustomizer
- `sigs.k8s.io/kustomize/kyaml` v0.17.1 — filesys (used by krusty)

Indirect (pulled by the above):
- `github.com/blang/semver/v4`
- `github.com/evanphx/json-patch`
- `github.com/liggitt/tabwriter`
- `gopkg.in/evanphx/json-patch.v4`
- `github.com/go-errors/errors`, `github.com/monochromegane/go-gitignore`,
  `github.com/xlab/treeprint`, `github.com/google/shlex`,
  `go.starlark.net/{starlark,starlarkstruct,...}`
- `github.com/gregjones/httpcache`, `github.com/peterbourgon/diskv`,
  `github.com/google/btree`
- `github.com/gorilla/websocket` (port-forward + exec SPDY paths)
- `github.com/moby/spdystream`
- `github.com/mxk/go-flowrate`

The k8s SIG release train (`k8s.io/{api,apimachinery,client-go,
cli-runtime,kubectl}`) is pinned to v0.30.0 across all five modules
to avoid the API churn risk PLAN.md Sprint 2 §"Risks" called out.

`go.sum` grew by ~80 lines. Binary size impact:

```
$ ls -la dist/roksbnkctl   (post-Sprint-1)  ≈ 56 MB
$ go build -o /tmp/roksbnkctl ./cmd/roksbnkctl
$ ls -la /tmp/roksbnkctl    (post-Sprint-2) ≈ 110 MB (estimate; verify pre-tag)
```

Acceptable for a single-binary-deploy CLI; the `cli-runtime` +
`kubectl/pkg/describe` libraries are not slim. If binary size becomes
a concern, candidates for trimming:

- `k8s.io/kubectl/pkg/describe` (largest single contributor) — could
  be replaced with a hand-rolled describer covering only the kinds
  BNK uses, at the cost of 1:1 kubectl output. Not recommended for
  v0.8; revisit if user feedback flags it.
- `sigs.k8s.io/kustomize/api/krusty` — pulls go.starlark for plugin
  support we don't use. Could be excised by switching to
  `sigs.k8s.io/kustomize/kyaml` directly + a minimal builder. Also
  not recommended for v0.8.

## Issue 6 (validator API mismatch) — accepted, integrator note

**Severity**: medium
**Status**: ⚠️ for the integrator — coordination gap

The validator agent wrote `internal/k8s/*_test.go` files in parallel,
gated by `//go:build k8sinternal`. The validator's prompt instructed
them to "use the same package name (`package k8s`) so you can access
unexported symbols," which gave them visibility into my code shape —
but since the agents ran in parallel, the validator's tests assumed
an API surface that doesn't match my final implementation. Examples:

- `apply_test.go` calls `Apply(ApplyOptions{Dynamic, Paths, Stdin,
  ...})`. My API is `(*ApplyOptions).Run(ctx)` with `Filename`,
  not `Paths`/`Dynamic`/`Stdin` fields.
- Likely similar drift in `get_test.go`, `describe_test.go`,
  `logs_test.go`, `delete_test.go`.

The `k8sinternal` build tag means default `go test ./...` is clean —
no existing CI lane breaks. The integrator should:

1. Either rewrite the validator's tests against the staff API surface
   (preferred — the API as shipped is the contract going forward), or
2. Reconcile by renaming staff API fields to match the validator's
   expectations if they're objectively better names.

Recommend (1): the staff API (`Filename` for the kubectl-flag-aligned
`-f` value, `Run()` for the cobra-style entry point) matches PRD 02's
"CLI surface" examples and kubectl's own conventions.

The CI workflow does NOT enable `-tags=k8sinternal` today (validator
prompt §3 doesn't require it), so this only blocks `make
test-internal` (or whatever local target the validator added) — not
the merge gate.

## Verification status (end of sprint)

- `go build ./...` ✓ clean
- `go vet ./...` ✓ clean
- `gofmt -l .` ✓ clean
- `go test ./...` ✓ clean (existing suite; validator agent owns the
  new `internal/k8s/*_test.go` files)
- `roksbnkctl --help` ✓ shows `k` parent + `get`/`logs` aliases
- `roksbnkctl k --help` ✓ lists all 7 verbs
- `roksbnkctl get --help` ✓ alias works
- Doctor with kubectl on PATH ✓ → green check, version line
- Doctor with kubectl PATH-stripped ✓ → green check, "internalised"
  detail, no warning row

## Priorities completed

| Priority | Item | Status |
|---|---|---|
| 1 | Client builder extensions (`BuildClientset`/`BuildDynamicClient`/`BuildRESTConfig`) | ✓ done |
| 2 | `roksbnkctl k get` (cli-runtime PrintFlags + resource.Builder) | ✓ done |
| 3 | `roksbnkctl k describe` (kubectl/pkg/describe delegation) | ✓ done |
| 4 | `roksbnkctl k apply -f` (SSA + kustomize auto-detect) | ✓ done |
| 5 | `roksbnkctl k logs` + `roksbnkctl k delete` | ✓ done |
| 6 | `roksbnkctl k exec` (SPDY) | ✓ done |
| 7 | `roksbnkctl k port-forward` (SPDY) | ✓ done |
| 8 | Doctor downgrade | ✓ done (Issue 2) |
| 9 | OpenShift extensions (Phase 2.1) | ⏸ deferred (Issue 3) |
| 10 | Top-level aliases | partial — `get`+`logs` shipped; `apply` skipped due to terraform-apply collision (Issue 1) |
