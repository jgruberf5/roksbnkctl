You are the **staff engineer** agent, **round 6**, for Sprint 18
Issue 2 (cos perf) of the roksbnkctl project. Repo root:
`/mnt/c/project/roksbnkctl`. You run with no memory of prior
conversation.

## Context — round 5 found the real bug, in a different file

Rounds 2/3/4 churned on `internal/cos/client.go`. Round-5
instrumented the actual code path and the data is unambiguous:

| Phase | Duration (live) |
|---|---|
| Process start → `cos.NewWithResolver` entry | **76.4s** ← the bug |
| All `internal/cos/client.go` work (region resolver + ListObjects) | 1.47s ✓ |

The 76.4s lives in `internal/ibm/cos_instance.go::GetCOSInstanceByName` →
`ListCOSInstances`, which paginates the IBM Cloud Resource Controller
v2 over **every resource in the account** to find the CRN matching the
`--instance <name>` flag.

**Empirical proof**: `roksbnkctl cos object list bnk-schematics-resources
--instance <CRN-directly>` runs in **1.27 seconds**, under the 1.4s
`ibmcloud cos` baseline. The `internal/cos/` code already meets Issue 2
AC #1 — the upstream instance-name → CRN resolution is the entire gap.

## Task — replace the unfiltered pagination with a server-side filter

The IBM Cloud Resource Controller v2 API (`/v2/resource_instances`)
accepts query parameters that restrict the listing server-side. At
minimum the SDK exposes one of: `resource_id` (the plan/service id),
`name`, `type`, `resource_group_id`, … . Find the right one for
"only COS instances" — the goal is to make `ListCOSInstances` return
just the account's COS service instances (typically O(1)–O(10)),
not every resource in the account (typically O(thousands) hence the
76.4s).

**Investigation sources** (read these before deciding):

1. **`internal/ibm/cos_instance.go`** — current
   `ListCOSInstances` + `GetCOSInstanceByName` implementations. You
   are replacing the unfiltered pagination with a server-side
   filtered one.
2. **The IBM Resource Controller v2 SDK** — the package the file
   currently imports (likely
   `github.com/IBM/platform-services-go-sdk/resourcecontrollerv2`).
   Inspect its `ListResourceInstancesOptions` struct; find the
   COS-narrowing field. Hint: COS's
   service-class identifier is `cloud-object-storage`; the SDK's
   field is something like `Set<ServiceName|ResourceID|Type>(…)`.
3. **The official `ibmcloud resource service-instances --service-name
   cloud-object-storage` CLI** — if you have access to its source
   (Go, `github.com/IBM-Cloud/ibm-cloud-cli`), it will show the
   exact field name + value the CLI uses.
4. **Round-5's data**, in
   `issues/issue_sprint18_staff.md` (Round-5 closure section the
   integrator just merged). You don't need to re-instrument — round-5
   already proved where the 76.4s goes.

## Implementation

Smallest change that closes the gap:

- Modify `ListCOSInstances` (or whichever func paginates today) to
  pass the COS service filter to the SDK call so the response only
  contains COS instances.
- Keep the public function signatures stable —
  `GetCOSInstanceByName(ctx, name)` stays
  `(*COSInstance, error)`, etc. The behavioural change is invisible
  to every caller; only the wire traffic narrows.
- If the filter happens to enumerate the COS service catalog by a
  plan-id rather than a service-class name, hardcode the value
  with a constant + clear comment naming what it is and where you
  found it. Avoid magic strings.

## Optional second win (only if it's trivially small)

Per-workspace memoization of name → CRN inside `internal/cli/cos.go`'s
`openCOSClient` would make the **second-and-subsequent** cos calls in
the same workspace skip the lookup entirely. The integrator's notes
mention this — round-1 staff already memoized `*cos.Client` in
`cachedCOSClient`/`cachedCOSInstance`/`cachedCOSWS`, so the structure
is in place. If swapping the body of `resolveCOSInstance` for a
name-keyed cache lookup is a 5-line change, do it; if it grows past
that, defer to v1.7.

## Constraints

- **Touch only `internal/ibm/cos_instance.go`** (and `internal/cli/cos.go`
  if you take the optional cache win — keep that diff small).
- **No edits to any pre-existing `_test.go`.** Additive new tests
  welcome (e.g.
  `internal/ibm/cos_instance_filter_test.go` pinning that the SDK
  call gets the COS-narrowing filter set).
- Do **not** commit. Integrator commits.
- Do **not** run `gh issue create`.
- Do **not** add a new SDK dependency to `go.mod` unless the IBM
  Resource Controller v2 SDK genuinely doesn't expose the filter
  (it does — verify in vendor / module cache).
- Do **not** touch `internal/cos/client.go` — that work is done.

## Verify before reporting done

- `go build ./...` clean. `go vet ./...` clean.
  `gofmt -l internal/` empty.
- `go test -race ./internal/ibm/ ./internal/cos/ ./internal/cli/` all
  green (cos + cli unchanged; ibm gains your new test).
- `git diff --stat -- '*_test.go'` shows ONLY your additive test
  file(s) (parity discipline).
- Authority to make ONE live IBM Cloud call to confirm the fix
  works (key load snippet in round-5's prompt; never echo). Capture
  the new wall-clock for
  `time roksbnkctl cos object list bnk-schematics-resources --instance bnk-orchestration`.

## Final report

≤200 words: the SDK field you wired (with where you found it), files
touched (exactly the ones in scope), hermetic-test pass list, and the
**measured** live wall-clock (target ≤2.8s). State explicitly: did
not commit, did not touch `internal/cos/client.go`, did not run
`gh issue create`.

If the IBM SDK genuinely lacks a server-side COS filter, fall back
honestly: explain what the SDK does expose, why you couldn't narrow
the call, and propose the next-best fix (e.g. break out of the
pagination loop early once an instance matches the name — won't help
when the matching instance is alphabetically late, but cuts the
common case). That's also an acceptable round-6 outcome.
