---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: OpenShift typed-client + scheme registration so `roksbnkctl k get projects`/`routes`/`imagestreams` resolve against ROKS clusters (PRD 02 Phase 2.1 / PLAN.md post-v1.0 carry)'
labels: []
assignees: ''
---

## Motivation

`internal/k8s/openshift.go::BuildOpenShiftClient` has been a reserved
stub since Sprint 2 (`returns errors.New("BuildOpenShiftClient not
implemented (Phase 2.1; see issue_sprint2_staff.md)")`). The Phase 2.0
dynamic-client + RESTMapper path in `internal/cli/k_get.go` happens to
work against the OpenShift CRDs the ROKS API discovery doc advertises
late-bound — but the typed-client + scheme-registration path the stub
promises has never landed.

PLAN.md §"What's deliberately deferred to post-v1.0" Code list names
this: "OpenShift CRDs in `roksbnkctl k get` (`Project`, `Route`, etc.)
— v1.1 — tracked in PRD 02 § Phase 2.1." The carry has been
unchanged since v1.0.0.

The user-visible payoff is two-fold: (1) **better error messages**
when an OpenShift-specific verb-resource combination isn't allowed
(typed client surfaces a real `RouteList: cannot list resource
"routes" in API group "route.openshift.io"` instead of the dynamic
client's late-bound `the server could not find the requested
resource`); (2) **completion + help text** — the typed client lets
`tools/refgen/cobra-md` surface OpenShift resource names in the
auto-generated chapter 27 (Command reference) so users see `roksbnkctl
k get projects` is a real verb-resource combo before they type it.

## Proposed surface

No new top-level verb. Two additions:

1. **`internal/k8s/openshift.go::BuildOpenShiftClient(kubeconfig
   string) (*openshiftclientset.Clientset, error)`** replaces the
   stub, returning a typed client built from the same kubeconfig
   discovery chain `BuildClientset` uses.

2. **Scheme registration into the existing `resource.Builder`** in
   `internal/cli/k_get.go` so `roksbnkctl k get projects` / `routes` /
   `imagestreams` / `deploymentconfigs` / `buildconfigs` / `templates`
   resolve by typed-client first, falling back to the
   dynamic-client path on a non-OpenShift cluster (so plain k8s
   clusters are unaffected). User-facing CLI shape is unchanged:

```
roksbnkctl k get projects                  # was: works via dynamic client late-bind
roksbnkctl k get routes                    # was: works via dynamic client late-bind
roksbnkctl k get routes -n my-ns           # new: typed-client preferred path
roksbnkctl k get -o yaml route my-route    # new: typed-client preferred path
```

No new flags. The typed-vs-dynamic dispatch is internal; the user
never picks.

## Behavior

- ROKS / OpenShift cluster detection: probe the API discovery doc
  once on first `k get` invocation; cache for the lifetime of the
  process. Cluster advertises `route.openshift.io/v1` → use typed
  client. Plain k8s cluster (no openshift.io group) → fall back to
  the dynamic-client path unchanged.
- Resource-name resolution: a known set of OpenShift resources
  (`projects`, `routes`, `imagestreams`, `deploymentconfigs`,
  `buildconfigs`, `templates`, and their singular / abbreviated
  forms) routes through the typed client. Anything else still uses
  the existing dynamic-client path (so user CRDs that don't ship
  with `openshift/client-go` still work).
- Error messages: typed-client returns the real "cannot list
  resource X in API group Y" / "forbidden" / "not found" shapes.
  Dynamic-client late-bind retains its current behaviour.
- Output format: `-o yaml`, `-o json`, default tabular — all match
  kubectl's current rendering. The typed-client return values are
  serialised via the same `resource.Builder` printer the dynamic
  path uses.
- `--namespace` / `-n` flag: unchanged. Cluster-scoped resources
  (`projects`) ignore it with the same warning kubectl emits.
- Non-OpenShift cluster: `roksbnkctl k get projects` falls back to
  the dynamic-client path and produces the same
  `<resource> not found` error the user sees today. NO change in
  behaviour on plain k8s.
- Vendored dependency: pull `github.com/openshift/client-go`
  in the version matching the ROKS API server's OpenShift minor (4.x
  series in production). Pin in `go.mod`; pin a single version (no
  matrix).

## Acceptance criteria

1. `internal/k8s/openshift.go::BuildOpenShiftClient(kubeconfig
   string)` returns a typed `*openshiftclientset.Clientset` and a
   nil error against a kubeconfig pointing at a ROKS cluster, AND
   wires the OpenShift API group schemes into the same
   `resource.Builder` `internal/cli/k_get.go` already uses.
2. `roksbnkctl k get projects` against a ROKS cluster returns the
   project list via the typed client (verifiable by setting a
   debug log line + asserting in a hermetic test against a fake
   `restclient` per the existing `internal/cli/k_get_test.go`
   pattern, additive — not editing a pre-existing test).
3. `roksbnkctl k get routes`, `imagestreams`, `deploymentconfigs`,
   `buildconfigs`, `templates` all route through the typed client
   when the cluster advertises the openshift.io API groups; route
   through the dynamic-client fallback when it doesn't.
4. Cluster-detection probe (API discovery doc) caches per-process
   so it's a one-shot cost. Hermetic test asserts the second call
   doesn't re-probe.
5. `roksbnkctl k get` against a plain k8s cluster (no openshift.io
   advertised) behaves byte-identically to v1.6.2 for every
   resource — typed-client path is silently skipped.
6. Error messages on `--namespace`-required misuse against a
   cluster-scoped resource match kubectl's shape verbatim.
7. PLAN.md §"What's deliberately deferred to post-v1.0" Code list:
   the OpenShift CRD bullet flips to "Resolved in <release>";
   PRD 02 §"Phase 2.1" gains a closure entry.
8. Vendored `github.com/openshift/client-go` pin appears in `go.mod`
   + `go.sum` at a deliberate version (CR for the integrator);
   `make release` / `go vet ./...` / `go build ./...` all clean.

## Out of scope (deliberately)

- Bash/zsh completion for OpenShift resource names — separate
  follow-up (PRD 02 §"Open questions" item 1 — bash completion is
  its own deferred work).
- OpenShift-specific verbs not in `k get` (e.g. `oc rsh`, `oc rollout`)
  — the v1.0.x `--backend ssh:<jumphost> oc rsh` passthrough path
  handles those already.
- `roksbnkctl k apply`/`create`/`delete` for OpenShift resources —
  scope here is strictly the `get` verb the PRD names.
- A typed-client cache keyed on kubeconfig file path — the
  per-process cache is sufficient; `k get` invocations are
  single-shot.
- Surfacing the OpenShift verbs in `tools/refgen/cobra-md`'s
  generated chapter 27 (Command reference) — once the typed client
  is wired, the existing generator will pick up resource names from
  the discovery doc; no generator change needed.

## Files likely touched

- `internal/k8s/openshift.go` — replace the stub with the real
  `BuildOpenShiftClient`.
- `internal/k8s/openshift_test.go` (new, additive) — hermetic test
  using a `restclient` fake.
- `internal/cli/k_get.go` — wire typed-client probe + dispatch into
  the existing `resource.Builder` chain.
- `internal/cli/k_get_test.go` (additive new test functions) —
  cover the typed-vs-dynamic dispatch decision.
- `go.mod`, `go.sum` — pin `github.com/openshift/client-go` +
  transitive deps.
- `docs/prd/02-KUBECTL-INTERNAL.md` §"Phase 2.1" — flip to "Landed
  in <release>".
- `docs/PLAN.md` §"What's deliberately deferred to post-v1.0" Code
  list — drop the OpenShift CRDs bullet.
- `CHANGELOG.md` — new `### Added` entry; drop the post-v1.0 carry.
- `book/src/02-overview.md` / `book/src/17-execution-backends.md`
  (or wherever `k get` is documented) — surface the typed-client
  payoff (better error messages, project/route/imagestream resolved
  without late-bind warnings).

## Notes

The Sprint 2 staff issue (`.archive/issues/issue_sprint2_staff.md`,
referenced in the stub's doc comment) names this as Phase 2.1 deferred
explicitly; the dynamic-client path was shipped as the v1.0.x
acceptable workaround. With the cli decomposition (Sprint 15/16) and
applied-tfvars / phase-handoff plumbing (Sprint 16) now stable, the
remaining post-v1.0 PRD 02 work is small enough to pick up cleanly.

This is one of the named post-v1.0 carries in PLAN.md that has been
deferred unchanged across every release tag from v1.0.0 to v1.6.2.
