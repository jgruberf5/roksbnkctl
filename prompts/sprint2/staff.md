You are the staff engineer agent for Sprint 2 of the roksbnkctl project. Your scope is **PRD 02** — internalising kubectl/oc into native Go via `client-go`, dropping the kubectl host-binary requirement.

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25 (per Sprint 1's bump).

## Read first

- `docs/prd/02-KUBECTL-INTERNAL.md` — your authoritative spec. The "Implementation tasks" section lists the 13 numbered deliverables in priority order.
- `docs/PLAN.md` Sprint 2 section — confirms ordering and verification gates.
- `internal/k8s/client.go` and existing files in `internal/k8s/` — `client-go` is already a transitive dep used by `internal/test/throughput.go`. Build on this rather than introducing a parallel client.
- `internal/cli/cluster.go` (passthrough commands `kubectl`/`oc`/`ibmcloud`/`exec`/`shell`/`shell` — Sprint 1 added the `--on` dispatch path; preserve it) and `internal/cli/inspect.go` (the existing `roksbnkctl logs <component>` command — extend it).
- `internal/doctor/doctor.go` + `internal/doctor/check.go` (the Sprint 0 Check struct refactor) — you'll downgrade kubectl + oc from "needed" to "informational".
- `prompts/sprint1/staff.md` and the resolved files at `issues/resolved_sprint1_*.md` — patterns from Sprint 1 (priority ordering, verification block, append-only-shared-files coordination notes).

## Coordinate with parallel agents

An architect agent is replacing 7 chapter stubs with real prose under `book/src/` (chapters 5, 6, 8, 9, 10, 11, 24). A validator agent is adding fake-clientset unit tests at `internal/k8s/*_test.go`, golden-file byte-equivalence tests, editing `.github/workflows/ci.yml`, patching `scripts/e2e-test.sh` Phase D (replacing D3 with `roksbnkctl k get pods -n f5-bnk` + a PATH-strip substep), and updating `docs/E2E_TEST.md`.

**Do not touch their files.** Specifically: don't write `*_test.go` files in `internal/k8s/` (validator owns those); don't edit `scripts/e2e-test.sh` or `.github/workflows/ci.yml` or `docs/E2E_TEST.md`. You own everything else.

## Tasks (priority order — finish from the top down)

If you run out of token budget, stop at the priority boundary you reached and file an issue describing what's deferred. Don't half-finish a task.

### Priority 1 — Client builder extensions (`internal/k8s/client.go`)

Add these constructors. Keep the existing surface intact; this is additive.

```go
// BuildClientset returns a typed client for core + apps + batch + etc.
// kubeconfigPath: empty string → workspace default at
// ~/.roksbnkctl/<ws>/state/kubeconfig; "in-cluster" sentinel → use
// rest.InClusterConfig() (used by the K8s execution backend in Phase 3,
// PRD 03).
func BuildClientset(kubeconfigPath string) (kubernetes.Interface, error)

// BuildDynamicClient returns a dynamic.Interface for unstructured access
// (necessary for kubectl get <type-not-in-typed-scheme>, CRDs, etc.).
func BuildDynamicClient(kubeconfigPath string) (dynamic.Interface, error)

// BuildRESTConfig is the lower-level helper both of the above use; expose
// it so callers that need a custom rest.Config (e.g. SPDY upgrades for
// exec/port-forward) can build off it.
func BuildRESTConfig(kubeconfigPath string) (*rest.Config, error)
```

Phase 2.1 adds OpenShift typed client + scheme registration; defer if you run out of budget but reserve `BuildOpenShiftClient` as the function name for that follow-up.

### Priority 2 — `roksbnkctl k get` (`internal/k8s/get.go` + `internal/cli/k_get.go`)

The flagship internalised verb. Use `k8s.io/cli-runtime`'s `genericclioptions.PrintFlags` so `-o yaml/json/wide/jsonpath/go-template/name` matches kubectl byte-for-byte. Use `cli-runtime`'s `resource.Builder` for type/name parsing.

CLI surface:
```bash
roksbnkctl k get <resource> [name] [-n <ns>] [-A] [-l <selector>] [-o <fmt>]
roksbnkctl get ...                  # top-level alias for the bare 'get'
```

Plural / singular / shortname (pods/pod/po) handling comes from `RESTMapper`; use the discovery client.

### Priority 3 — `roksbnkctl k describe` (`internal/k8s/describe.go` + `internal/cli/k_describe.go`)

Delegate to `k8s.io/kubectl/pkg/describe`. The library does the heavy lifting; this is mostly cobra + flag wiring.

### Priority 4 — `roksbnkctl k apply -f` (`internal/k8s/apply.go` + `internal/cli/k_apply.go`)

Server-side apply via dynamic client with field-manager `roksbnkctl`. Inputs:
- `-f <file>` — single YAML file
- `-f <dir>` — recurse `*.yaml` (or detect kustomization.yaml and use kustomize/api)
- `-f -` — stdin

Use `sigs.k8s.io/kustomize/api/krusty` for kustomize base resolution. `--force` flag maps to SSA's `force-conflicts=true`.

CLI surface:
```bash
roksbnkctl k apply -f <file-or-dir> [-n <ns>] [--force]
roksbnkctl apply -f ...           # top-level alias
```

### Priority 5 — `roksbnkctl k logs` (extends existing) + `roksbnkctl k delete`

`logs`: extend the existing component-aware `internal/cli/inspect.go logsCmd` with raw pod-name path:
- `roksbnkctl logs flo` (existing — by component label)
- `roksbnkctl k logs <pod-name>` (new — direct pod)
- Both honour `-n <ns>`, `-c <container>`, `-f`, `--previous`, `--since`, `--tail`

`delete`: cobra wiring for the dynamic-client delete + cascade options. CLI:
```bash
roksbnkctl k delete <resource> <name> [-n <ns>] [--force] [--grace-period N] [--cascade <orphan|background|foreground>]
```

### Priority 6 — `roksbnkctl k exec` (SPDY) (`internal/k8s/exec.go` + `internal/cli/k_exec.go`)

Use `client-go/tools/remotecommand.NewSPDYExecutor`. CLI surface:

```bash
roksbnkctl k exec <pod> [-n <ns>] [-c <container>] [-i] [-t] -- <cmd> [args...]
```

`-i` opens stdin to the remote process; `-t` allocates a PTY (chapter 24 will tell users to use this for `top` / `bash`-style interactive work).

### Priority 7 — `roksbnkctl k port-forward` (SPDY)

`client-go/tools/portforward`. CLI:

```bash
roksbnkctl k port-forward <pod> [-n <ns>] <local-port>:<remote-port>
```

Signal handling: graceful close on Ctrl+C (no orphaned tunnel).

### Priority 8 — Doctor downgrade

In `internal/doctor/doctor.go`: change the `kubectl` and `oc` checks to:
- `Optional: true` (already?) — verify
- Status now downgraded one notch: missing → `StatusOK` with detail `(internalised; passthrough still works if installed)` rather than `StatusWarning`. Or keep as `StatusWarning` but change the message to mention the internalisation.

Whichever you pick, document the choice in the issue file. The intent is: a fresh dev box without kubectl/oc should not produce warnings post-Sprint 2 for everyday roksbnkctl use.

### Priority 9 — OpenShift extensions (Phase 2.1) — DEFER if budget tight

`github.com/openshift/client-go` + `github.com/openshift/api`. Register OpenShift API types in the scheme so `roksbnkctl k get projects` (and routes, imagestreams) works against ROKS clusters. PLAN.md and PRD 02 explicitly mark this as Phase 2.1; if you don't have time, file an issue and move on. The 7 architect chapters can mention the deferral as a future enhancement.

### Priority 10 — Top-level aliases

After all `roksbnkctl k <verb>` commands work, add cobra command aliases at the root level so the most common verbs work without the `k` prefix:

- `roksbnkctl get` ↔ `roksbnkctl k get`
- `roksbnkctl apply` ↔ `roksbnkctl k apply`
- `roksbnkctl logs` ↔ `roksbnkctl k logs`

The disambiguation pattern (host vs cluster `exec`): `exec` stays host-side; cluster exec is `roksbnkctl k exec` only — no top-level alias.

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean (unit tests added by validator pass; your code shouldn't break them)
- `go vet ./...` clean
- `gofmt -d -l .` clean
- `roksbnkctl --help` shows the new `k` parent + at least the top-level aliases
- `roksbnkctl k --help` shows the verb list
- `roksbnkctl get --help` works (alias)
- Doctor on a host with kubectl on PATH: kubectl row still ✓
- Doctor on a host without kubectl: row downgrades to informational, not a warning (`StatusOK` with explanatory detail)

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint2_staff.md`. Same format as Sprint 1. If a priority item is deferred (most likely candidates: priority 7 port-forward, priority 9 OpenShift extensions), file an issue documenting what's missing and why. Don't half-finish.

## Final report (under 200 words)

- Files created (count + key paths)
- Files edited
- Build / test / vet / gofmt status
- Which priority items completed; which (if any) deferred
- Issues filed
- Anything the integrator should know (especially regarding cli-runtime / kubectl/pkg/describe / kustomize/api dep additions to go.mod)

Do NOT commit. The integrator commits the aggregated work.
