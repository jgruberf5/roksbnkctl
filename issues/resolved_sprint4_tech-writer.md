# Sprint 4 — tech writer issues, resolution notes

Fourteen issues filed: 2 high, 4 medium, 8 low. Twelve resolved in this
integration pass; two (Issues 9 + 14) accepted with minor scope notes;
one (Issue 5's `roksbnkctl-test` namespace deletion) resolved doc-side
rather than code-side after weighing the two paths.

All fixes are doc-and-RBAC only — one YAML tightening
(`internal/exec/k8s_install.yaml`'s Secret rule gained `resourceNames`
to match Chapter 19's "named secrets" framing); the rest are book
prose, README, and PRD text.

## Issue 1 (HIGH — chapters 17 + 19 document pod name `ops`; actual is `roksbnkctl-ops`) — resolved by integrator

This was the root-cause drift behind Issues 1, 5, 11 (partial) and the
incorrect spot-check in `resolved_sprint4_architect.md` Issue 1.

Replaced every reference across the two chapters:

- Chapter 17 §"Long-lived ops pod pattern" — pod name `ops` →
  `roksbnkctl-ops`, container name `ops` → `tools`, code-block's
  `Name("ops")` + `Container: "ops"` → `Name("roksbnkctl-ops")` +
  `Container: "tools"`
- Chapter 19 §5 "Create the Pod" — manifest rewritten to byte-match
  `internal/exec/k8s_install.yaml` (correct labels, correct container
  name `tools`, no fictional `workingDir`/`runAsUser`/`label` fields)
- Chapter 19 §"Cred propagation" — `kubectl describe pod ops` →
  `kubectl describe pod roksbnkctl-ops` (two instances)
- Chapter 19 §"Rotation" + §"Operability" — every `pod/ops` / `ops` →
  `pod/roksbnkctl-ops` / `roksbnkctl-ops` (six instances)
- Chapter 19 partial-install sample — `roksbnkctl-ops/ops` →
  `roksbnkctl-ops/roksbnkctl-ops`

**Status**: ✅ resolved (`grep -nE '"ops"|name: ops|pod ops'` returns zero
hits across chapters 17 + 19)

## Issue 2 (HIGH — chapter 19 ClusterRole table doesn't match `k8s_install.yaml`) — resolved by integrator (YAML tightened + table rewritten)

Two-sided fix:

1. **YAML tightened**: added `resourceNames: ["roksbnkctl-ibm-creds"]`
   to the `secrets: get` rule in `internal/exec/k8s_install.yaml`. The
   chapter's "named secrets, least-privilege per PRD 04" framing is now
   true byte-for-byte against the embedded manifest. No other Secret in
   the `roksbnkctl-ops` namespace is readable by the SA.
2. **Table rewritten**: removed the three fictional rows (services,
   deployments, namespaces — the SA legitimately doesn't have these
   verbs because nothing inside the pod needs them); corrected the
   pod-related rows to match the actual `pods` (get/list/watch only),
   `pods/log` (get/list), `pods/exec` (create/get) split.

The §"Notably **not** granted" block also got an explicit
`services / deployments / namespaces — the SA can't touch these at
all` callout to reinforce the actual least-privilege surface, since
the iperf3-server provisioning happens on the caller side (with the
user's kubeconfig), not from inside the ops pod.

**Status**: ✅ resolved (chapter 19 RBAC table ↔
`internal/exec/k8s_install.yaml:64-90` byte-for-byte consistent)

## Issue 3 (MEDIUM — chapter 17 says iperf3 server is "Deployment + LoadBalancer Service") — resolved by integrator

Rewrote chapter 17 §"iperf3 server side" to reflect the actual
implementation: bare Pod (not Deployment) named `roksbnkctl-iperf3`,
Service type driven by `--mode` (LoadBalancer for north-south,
ClusterIP for east-west). Added a paragraph explaining the bare-Pod
choice — single-shot, scoped, torn down on completion; controller
machinery would only confuse the cleanup story.

**Status**: ✅ resolved (chapter 17 ↔ `internal/k8s/iperf3.go`
consistent)

## Issue 4 (MEDIUM — chapter 19's `ops show` sample doesn't match the binary) — resolved by integrator

Rewrote the sample-output block to match the actual six-line print
format from `internal/cli/ops.go::runOpsShow` (lines 167-183):

```
namespace:    roksbnkctl-ops
pod:          roksbnkctl-ops
phase:        Running
ready:        true
image:        ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v0.9.0
rbac subject: system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops
secret:       roksbnkctl-ibm-creds (rotated 2026-05-10T11:03:17Z)
```

Dropped the fictional `service-account:`, `clusterrole:`, `started=`,
`image-id=`, `in-use-by-pod=` rows. Downgraded `-o json` to "Sprint
5+ roadmap" with explicit "once `ops show` grows additional fields"
framing — that mode is genuinely worth landing but doesn't exist yet.

**Status**: ✅ resolved (chapter 19 ↔
`internal/cli/ops.go::runOpsShow` consistent)

## Issue 5 (MEDIUM — chapter 19 omits `--confirm` gate; says "does not touch roksbnkctl-test") — resolved by integrator (doc-side)

Rewrote chapter 19 §"`roksbnkctl ops uninstall`" to:

1. Show the default no-flag invocation producing the "Would delete
   (re-run with --confirm…)" preview output verbatim from
   `internal/cli/ops.go::runOpsUninstall` (lines 189-198).
2. Show a second `roksbnkctl ops uninstall --confirm` invocation
   producing the actual deletion output (the `✓ deleted <label>`
   format the `tryDel` helper emits to stderr).
3. Rewrote the prose about `roksbnkctl-test` namespace handling:
   chose **doc-side** resolution (acknowledge that uninstall does
   delete the test namespace) over **code-side** resolution
   (removing the namespace deletion). Rationale: by the time a user
   runs `ops uninstall`, test workloads in `roksbnkctl-test` should
   already be done (their TTL Jobs auto-clean); leaving the namespace
   behind would orphan a roksbnkctl-managed surface and complicate
   `ops install` idempotency. The two-namespace deletion matches
   `internal/cli/ops.go::runOpsUninstall` lines 234-236.

**Status**: ✅ resolved (chapter 19 ↔
`internal/cli/ops.go::runOpsUninstall` consistent; `--confirm` flag
documented; `roksbnkctl-test` deletion documented)

## Issue 6 (MEDIUM — chapter 17 SSH bootstrap exit codes claim "all surface as exit 126") — resolved by integrator

Rewrote chapter 17 §"Bootstrap failure modes" lead-in + table:

- Acknowledged the 126/127 split explicitly with a one-paragraph
  framing cross-linked to §"Backend-failure semantics"
- Added an exit-code column to the table so each failure mode's
  actual code is visible at a glance
- Added the `--bootstrap` not set / tool missing row (was implicit
  in chapter prose elsewhere but missing from the failure-modes
  table)
- Added the "no apt mapping for tool" row (the `toolPackages` map
  in `internal/exec/ssh.go` covers only `ibmcloud` + `iperf3` today;
  other tool names error 126)
- Corrected the non-Ubuntu detection mechanism from `/etc/os-release`
  to `lsb_release -is` (matches `ssh.go:260-264`)

**Status**: ✅ resolved (chapter 17 §SSH backend ↔
`internal/exec/ssh.go::ensureTool` exit codes consistent)

## Issue 7 (LOW — chapter 17 `--backend` flag format documentation) — resolved by integrator

Collapsed the two-row format display to a single row
`--backend local|docker|k8s|ssh:<target>`, with a follow-on paragraph
explaining the `ssh:<target>` form's relationship to `roksbnkctl
targets list`. Matches `internal/cli/root.go:123` flag help.

**Status**: ✅ resolved

## Issue 8 (LOW — chapter 18 DNS-probe example reads as runnable today) — resolved by integrator

Added an explicit "Sprint 5 deliverable" lead-in paragraph to the
GSLB DNS validation section, with "the flags below don't exist on
`roksbnkctl test dns` today" framing. The code block carries an
in-comment `# Sprint 5+` marker. Readers running the example get
the forward-looking framing before the unknown-flag error.

**Status**: ✅ resolved

## Issue 9 (LOW — PRD 03 open questions are now answered) — resolved by integrator

Converted PRD 03 §"Open questions" (lines 398-404) into a
§"Resolved in Sprint 4" subsection that records the decision for
each of the four open questions, citing the file path that
implements it. Keeps PRD 03 a living document.

**Status**: ✅ resolved

## Issue 10 (LOW — README highlight bullet links only to chapter 17) — resolved by integrator

Extended the Sprint 4 highlight bullet at `README.md:38` to add
follow-on links to chapters 18 (decision tree) and 19 (ops pod
lifecycle), so readers landing on the README from the highlight
bullet have the discovery path to both companion chapters.

**Status**: ✅ resolved

## Issue 11 (LOW — chapter 19 rotation section) — resolved by integrator

Three sub-fixes:

1. Added the `IC_API_KEY` alias note to §"Credential propagation"
   (the YAML at `k8s_install.yaml:51-56` populates both
   `IBMCLOUD_API_KEY` and `IC_API_KEY` from the same resolved
   value; chapter 19 now reflects this).
2. Replaced `pod/ops` with `pod/roksbnkctl-ops` in the rotation
   code example (covered by Issue 1's blanket pod-name fix).
3. Removed the `kubectl rollout restart pod/...` example —
   `rollout restart` doesn't operate on bare pods. Replaced with
   `kubectl delete pod roksbnkctl-ops -n roksbnkctl-ops` plus an
   inline note that the bare pod has no controller, so re-running
   `roksbnkctl ops install` is the canonical recreate path.

**Status**: ✅ resolved

## Issue 12 (LOW — chapter 19's "kubectl apply --server-side" prose is wrong) — resolved by integrator

Rewrote the "If the Secret already exists" paragraph in chapter 19
§4 to describe the actual `internal/cli/ops.go::applyOpsObject`
behavior: client-side Get + Update that overwrites `data` and
refreshes the `roksbnkctl.io/rotated-at` annotation, leaving the
rest of the metadata alone.

**Status**: ✅ resolved (chapter 19 ↔
`internal/cli/ops.go::applyOpsObject` consistent)

## Issue 13 (LOW — chapter 17 `:dev` tag section says "landed unchanged this sprint") — resolved by integrator

Rewrote chapter 17 §"`:dev` tag resolution" to acknowledge the
Sprint 4 polish carry-over: `toolImageTag()` resolves the per-tool
image tag from the binary's `internal/version.Version`, with `:dev`
as the explicit fallback for dev builds. Added a sentence noting
that `terraform` is the exception to the version-resolved pattern
(pinned to a literal `1.5.7`).

**Status**: ✅ resolved (chapter 17 ↔
`internal/exec/docker.go::toolImageTag` consistent)

## Issue 14 (LOW — `internal/exec/k8s_test.go` test naming + comments) — accepted (deferred to Sprint 5 polish)

Tech-writer's two concrete suggestions (split
`TestK8sBackend_Run_Job_CreatesJobAndSecret_TTL` into three single-
invariant tests; add a PRD-04-SECURITY docstring to
`TestK8sBackend_NoCredValueInArgv`) are reasonable code-hygiene
improvements but don't change the verification surface. Rolled into
Sprint 5's polish basket alongside the other "tests work as written;
could be split" entries the validator filed at integration time.

**Status**: ⏸ accepted (Sprint 5 polish)

## Integrator additions

- Tightened `internal/exec/k8s_install.yaml`'s ClusterRole's
  `secrets: get` rule with `resourceNames: ["roksbnkctl-ibm-creds"]`
  (paired with Issue 2's table rewrite; the chapter's "named
  secrets" framing is now load-bearing against the manifest).
- Verified `go build ./...`, `go vet ./...`, `gofmt -d -l .`,
  `go test ./...` all green post-fixes.

## Summary

14 issues filed (2 high, 4 medium, 8 low); 12 fully resolved in this
pass; Issue 5 resolved doc-side (the `roksbnkctl-test` deletion stays;
chapter prose now matches); Issue 14 deferred to Sprint 5 polish. Both
high-severity items (pod-name drift, RBAC-table drift) are closed.
M3-prelim gate criteria from PLAN.md §"Sprint 4 — Gate to Sprint 5"
remain met; the doc-blocking issues the tech-writer flagged are now
unblocked.
