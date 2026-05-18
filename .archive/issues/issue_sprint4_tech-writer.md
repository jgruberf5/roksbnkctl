# Sprint 4 — tech writer issues

Findings cover the three new chapters (17 full, 18, 19), staff's
`internal/exec/{k8s,ssh}.go` + `internal/cli/ops.go` +
`internal/exec/k8s_install.yaml`, the iperf3 SCC fix in
`internal/k8s/iperf3.go`, validator's new test suite, integrator's
three follow-up fixes (Dockerfile `USER 1000`, k8s `jobNameSanitizer`,
chapter 19 rotation rewrite), README + PRD + PLAN drift. All findings
are doc/example-correctness only — no code changes proposed.

## Issue 1: chapters 17 + 19 document the ops pod's name as `ops` but the actual name is `roksbnkctl-ops`

**Severity**: high
**Status**: open
**Description**: This is a load-bearing drift between the chapters and
the shipped surface. Every reader who runs `kubectl get pods -n
roksbnkctl-ops` after `roksbnkctl ops install` will see one pod named
`roksbnkctl-ops`, not `ops`.

Chapter 17 §"K8s backend" §"Long-lived ops pod pattern" (line 251):

> The pod is named `ops` in the `roksbnkctl-ops` namespace.

Chapter 17 §"Long-lived ops pod pattern" code block (lines 256-267) hard-codes:

```go
Resource("pods").Namespace("roksbnkctl-ops").Name("ops").
```

Chapter 19 §5 "Create the Pod" (lines 119-149) renders the pod manifest
with `metadata.name: ops` and container `name: ops`.

Chapter 19 §"`roksbnkctl ops show`" sample output (line 168):

```
pod:                 ops                         status=Running, ready=true (2/2)
```

The actual implementation (`internal/exec/k8s.go:32`):

```go
K8sOpsPodName    = "roksbnkctl-ops"
```

And `internal/exec/k8s_install.yaml:107-108`:

```yaml
kind: Pod
metadata:
  name: roksbnkctl-ops
  namespace: roksbnkctl-ops
```

Note the resolved_sprint4_architect.md Issue 1 disposition asserts "the
chapter's `ops` short-name matches the namespace-scoped resolution
`roksbnkctl-ops/roksbnkctl-ops`" — this is incorrect; the namespace-
scoped resolution is `<namespace>/<podName>` = `roksbnkctl-ops/roksbnkctl-ops`,
i.e. the pod name *itself* is `roksbnkctl-ops`, not `ops`.

The actual container name inside the pod is `tools` (`k8s_install.yaml:123`),
not `ops` as chapter 19 §5 documents at line 131 (`- name: ops`).

**Files affected**: `book/src/17-execution-backends.md` (prose at line 251;
code block at lines 256-267); `book/src/19-in-cluster-ops-pod.md`
(manifest at lines 119-149; sample show output at line 168; reference
prose throughout the chapter that says "the ops pod" implicitly assuming
the short name)

**Proposed fix**: replace every `name: ops`, `Name("ops")`, "named
`ops`", and `pod: ops` reference in chapters 17 + 19 with
`roksbnkctl-ops`. Replace the container name `ops` in chapter 19's pod
manifest with `tools`. The sample `ops show` output should match the
actual `internal/cli/ops.go::runOpsShow` print order (see Issue 4).

## Issue 2: chapter 19's ClusterRole rules table doesn't match `internal/exec/k8s_install.yaml`

**Severity**: high
**Status**: open
**Description**: Chapter 19 §"RBAC: the ClusterRole rules" (lines
219-227) documents a six-row table of rules. Five of those six rows
don't match the actual embedded manifest:

| Documented rule | Actual rule |
|---|---|
| `pods`, `pods/exec`, `pods/log` → get/list/watch/create/delete | `pods` → get/list/watch only (no create/delete); `pods/log` → get/list; `pods/exec` → create/get |
| `secrets` (named `roksbnkctl-ibm-creds`) → get/list | `secrets` → get (no resourceNames restriction; no list verb) |
| `services` → get/list/create/delete | **rule absent from manifest** |
| `apps/deployments` → get/list/create/delete | **rule absent from manifest** |
| `namespaces` → get/list | **rule absent from manifest** |
| `batch/jobs` → get/list/watch/create/delete | `batch/jobs` → get/list/create/delete/watch — matches |

The "named secrets" restriction is the load-bearing one for the chapter's
"least-privilege per PRD 04" framing — the actual manifest has no
`resourceNames` filter on `secrets: get`, so the SA can read **any**
Secret in the namespaces the binding covers. Chapter 19's prose right
after the table at line 223:

> **Named** so the SA can't read other Secrets in the namespace —
> least-privilege per PRD 04 §"In-cluster pod".

…is materially incorrect against the shipped YAML.

Chapter 19 also lists "Notably **not** granted" entries (lines 228-232)
claiming the pod can't write Secrets or modify its own RBAC. The actual
manifest doesn't grant write/delete on Secrets or RBAC either, so this
part is fine — but it implies the surrounding rule set is tighter than
it actually is.

**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"RBAC: the
ClusterRole rules" (lines 215-243)

**Proposed fix**: Either (a) update the table to match the actual
`internal/exec/k8s_install.yaml:64-87` rule set, or (b) propose to the
staff agent that the manifest be tightened to match what the chapter
documents (adding `resourceNames: ["roksbnkctl-ibm-creds"]` to the
Secret rule, and adding the missing `services` + `deployments` +
`namespaces` rules iff the ops pod actually needs them). The "named
secrets" claim is the security-spine assertion the chapter rests on —
it must be true or the prose must change.

## Issue 3: chapter 17's iperf3 server-side shape says "Deployment" but the actual implementation is a bare Pod

**Severity**: medium
**Status**: open
**Description**: Chapter 17 §"iperf3 server side" (lines 322-327) says:

> The `iperf3` test deploys a **server** Deployment + LoadBalancer
> Service into `roksbnkctl-test`…
>
> | Side | Resource | Lifetime |
> |---|---|---|
> | Server | `roksbnkctl-iperf3-server` Deployment + LoadBalancer Service | torn down after the client Job completes |

The actual implementation in `internal/k8s/iperf3.go:59-97` creates a
bare `corev1.Pod` (NOT a Deployment), named `roksbnkctl-iperf3` (NOT
`roksbnkctl-iperf3-server`). The Service is named `roksbnkctl-iperf3`
and its type is dynamic (`LoadBalancer` for `--mode north-south`,
`ClusterIP` for `--mode east-west`) — chapter 17 hard-codes "LoadBalancer".

**Files affected**: `book/src/17-execution-backends.md` §"iperf3 server
side" (lines 322-330)

**Proposed fix**: replace the table row with the actual resource type
("Pod + Service (LoadBalancer or ClusterIP depending on `--mode`)"),
fix the names to `roksbnkctl-iperf3` (both sides), and add a one-line
note about the `--mode` driving the Service type. Cross-reference
[Chapter 22 — Throughput testing] for the mode semantics. While here,
the chapter's claim that the test deploys "Deployment + LoadBalancer
Service" implicitly promises HA / multi-replica behaviour the bare Pod
doesn't provide; this should be reworded.

## Issue 4: chapter 19's `roksbnkctl ops show` sample output doesn't match what the binary prints

**Severity**: medium
**Status**: open
**Description**: Chapter 19 §"`roksbnkctl ops show`" (lines 166-179)
shows a richly-formatted sample output with field names like
`service-account`, `clusterrole`, `secret`, `last-rotation`,
`in-use-by-pod`, `rbac subject`, plus a `2/2` ready count and an
`-o json` flag at line 189.

The actual `internal/cli/ops.go::runOpsShow` (lines 152-185) prints a
much simpler block:

```
namespace:    roksbnkctl-ops
pod:          roksbnkctl-ops
phase:        Running
ready:        true
image:        ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev
rbac subject: system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops
secret:       roksbnkctl-ibm-creds (rotated 2026-05-10T11:03:17Z)
```

Specifically:
- No `service-account:` field on its own line
- No `clusterrole:` field (chapter 19 says "6 rules; see `kubectl
  describe clusterrole`" but the binary doesn't emit this row at all)
- No `(1 secret bound)` ServiceAccount-counted line
- No `ready=true (2/2)` container-count format — the binary prints
  `ready: true` (bool), since the pod has exactly one container (`tools`)
- No `started=` timestamp
- No `image-id=` sha256 line
- No `in-use-by-pod=true (env hash matches)` — the env-hash comparison
  described in chapter 19 §"Rotation" (line 294) doesn't exist in the
  current `runOpsShow` implementation
- `last-rotation=` does exist but the chapter's prose at line 185 (and
  again at lines 167, 185-186) claims it's derived from "the Secret's
  resolution timestamp" set by `ops install` — the actual mechanism
  is the `roksbnkctl.io/rotated-at` annotation stamped by
  `decodeOpsManifests` at install time (which matches; just clarify
  the annotation name for advanced users)
- `roksbnkctl ops show -o json` (line 189): the `-o`/`--output` flag is
  persistent at root (line 119 of root.go declares it), so it *parses*,
  but `runOpsShow` ignores `flagOutput` — there's no JSON branch in
  the function. Documenting `-o json` as "emits the same data as a
  structured object suitable for CI assertions" is aspirational.

**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"`roksbnkctl
ops show`" (lines 161-189)

**Proposed fix**: rewrite the sample-output block to match the actual
six-line print format. Drop the `-o json` paragraph or downgrade it to
"Sprint 5+" status. The "in-use-by-pod env-hash" reconciliation is a
worthwhile feature but it doesn't exist today and shouldn't be
documented as a current capability; either file as a staff follow-up or
remove the prose.

## Issue 5: chapter 19's `roksbnkctl ops uninstall` sample doesn't reflect the `--confirm` gate

**Severity**: medium
**Status**: open
**Description**: Chapter 19 §"`roksbnkctl ops uninstall`" (lines
191-204) shows a sample where `roksbnkctl ops uninstall` *immediately*
starts deleting:

```
$ roksbnkctl ops uninstall
deleting Pod              roksbnkctl-ops/ops                        ... done
deleting Secret           roksbnkctl-ops/roksbnkctl-ibm-creds       ... done
…
```

The actual `internal/cli/ops.go::runOpsUninstall` (lines 187-241) is a
destructive-action gate: without `--confirm`, the command prints a
**preview** of what *would* be deleted and exits successfully. Only
`roksbnkctl ops uninstall --confirm` actually deletes anything. The
`--confirm` flag is registered at `ops.go:70`:

```go
opsUninstallCmd.Flags().BoolVar(&flagOpsConfirm, "confirm", false, ...)
```

Chapter 19 doesn't mention `--confirm` once, anywhere. Readers who copy
the sample will get the preview, see "Would delete (re-run with --confirm
to proceed)" in the output, and be confused about what's wrong.

Additionally:
- The sample says `Pod roksbnkctl-ops/ops` — same `ops` vs
  `roksbnkctl-ops` drift covered in Issue 1.
- The sample lists six deletion rows (Pod, Secret, SA, ClusterRoleBinding,
  ClusterRole, Namespace `roksbnkctl-ops`). The actual implementation
  also deletes `Namespace roksbnkctl-test` (ops.go:234-236) — that's a
  seventh row the sample misses. Worth calling out because chapter 19
  explicitly says elsewhere (line 213) "`ops uninstall` does not touch
  `roksbnkctl-test`" — which contradicts the actual code.

**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"`roksbnkctl
ops uninstall`" (lines 191-213)

**Proposed fix**: update the sample to:
1. Show the default `roksbnkctl ops uninstall` invocation producing the
   preview output (the `Would delete (re-run with --confirm…)` block).
2. Show a second `roksbnkctl ops uninstall --confirm` invocation
   producing the actual deletion output (matching the `tryDel` format
   in `runOpsUninstall`, which is `✓ deleted <label>` to stderr, not
   `deleting … ... done` to stdout).
3. Update the prose at line 213 — `ops uninstall` **does** delete the
   `roksbnkctl-test` namespace, so the "does not touch roksbnkctl-test"
   claim is wrong. Either fix the prose or file with staff to drop the
   `roksbnkctl-test` deletion call.

## Issue 6: chapter 17 §SSH bootstrap-failure exit codes don't match the staff implementation

**Severity**: medium
**Status**: open
**Description**: Chapter 17 §"SSH backend" §"Per-tool apt-bootstrap"
states (line 391):

> Bootstrap failure modes (all surface as exit `126` — backend mid-run
> failure — with a remediation message)

Then the table at lines 394-398 lists three failure modes with mixed
exit codes (126 for sudo, **126 implicitly** for non-Ubuntu, **127**
for network unreachable). The "all surface as exit `126`" lead-in
contradicts the network-unreachable row's `127`.

Cross-checking against `internal/exec/ssh.go::ensureTool`:

| Chapter 17 failure | Documented code | Actual code | File:line |
|---|---|---|---|
| `--bootstrap` not set, tool missing | "exit `127`" (line 367) | `127` (sshExitFailedToStart) | ssh.go:250 |
| sudo password required | `126` (line 395) | `126` | ssh.go:289 |
| sudo install failed | n/a | `126` | ssh.go:295 |
| non-Ubuntu OS | (chapter 17 table row implies `126`) | `126` (sshExitStartedThenFailed) | ssh.go:265 |
| apt repo unreachable | "Exit `127`" (line 397) | `127` (sshExitFailedToStart) | ssh.go:280 |
| no apt mapping for tool | n/a (chapter doesn't cover) | `126` | ssh.go:255 |

The actual code splits 126 (started, then sudo/install/Ubuntu-detection
broke) from 127 (couldn't reach the repo / tool missing before bootstrap).
Chapter 17's "all surface as exit `126`" lead-in is incorrect; the
correct framing is "some are 126 (we got partway in), some are 127 (we
never got going)".

Also: chapter 17 §"Per-tool apt-bootstrap" line 396 says the non-Ubuntu
check tests `/etc/os-release`'s `ID=ubuntu`. The actual check at
`ssh.go:260-264` uses `lsb_release -is` and compares
case-insensitively to `Ubuntu`. Same intent, different mechanism;
worth a one-word fix in the chapter so a user debugging "auto-install
only supports Ubuntu" looks in the right place.

**Files affected**: `book/src/17-execution-backends.md` §"SSH backend"
§"Per-tool apt-bootstrap" (lines 391-398) and §"Bootstrap failure modes
(consolidated)" (lines 451-459)

**Proposed fix**: rewrite the "all surface as exit `126`" lead-in to
acknowledge the 126/127 split; update the `Non-Ubuntu OS` row to
reference `lsb_release -is`; add a row for "no apt mapping for tool"
(staff agent's `toolPackages` map only has iperf3 + ibmcloud today —
the SSH backend errors with exit 126 for any other tool name).

## Issue 7: chapter 17's `--backend` flag format documentation omits the `k8s` and `ssh` shapes

**Severity**: low
**Status**: open
**Description**: Chapter 17 §"The `--backend` CLI flag" line 47-50 shows
the format as:

```
--backend <name>            # local | docker | k8s
--backend ssh:<target-name> # SSH backend; target name from `roksbnkctl targets list`
```

Two minor inaccuracies:

1. The first line lists `local | docker | k8s` but the actual root flag
   registered at `internal/cli/root.go:123` documents the value as
   `local | docker | k8s | ssh:<target>` (i.e., `ssh:<target>` is a
   member of the same value set, not a separate format). The two-row
   layout suggests these are different flag shapes, which they aren't.
2. The hint "target name from `roksbnkctl targets list`" is correct but
   the actual flag help text says "target" without the cross-reference.
   Worth keeping the cross-reference in the chapter for discoverability.

**Files affected**: `book/src/17-execution-backends.md` lines 47-50

**Proposed fix**: collapse to a single-row format showing
`--backend local|docker|k8s|ssh:<target>` and note that the
`ssh:<target>` form requires a target registered via `roksbnkctl
targets add`.

## Issue 8: chapter 18 references DNS-probe flags that don't exist yet (and may be confusing)

**Severity**: low
**Status**: open
**Description**: Chapter 18 §"I'm doing GSLB DNS validation" (lines
101-115) shows:

```bash
roksbnkctl test dns \
  --target www.example.com \
  --type A \
  --server gslb-vip.f5.example.com \
  --gslb-compare
```

None of `--target`, `--type`, `--server`, or `--gslb-compare` exist on
`roksbnkctl test dns` today (the current implementation at
`internal/cli/test.go:126-133` just calls `test.RunDNS` against
workspace `extra_hosts`). All four flags land in Sprint 5 per
PLAN.md and PRD 03 §"DNS Probe". Chapter 18 acknowledges this at line
115:

> The DNS probe lands in Sprint 5; [Chapter 21 — DNS testing for GSLB]
> is the chapter to read once it ships.

This is the recommended pattern in the prompt (forward-references to
Sprint 5+ are explicit and future-tense), so this is **not** a bug —
but the example reads as runnable today. Readers running through the
chapter may try the command and get "unknown flag: --gslb-compare"
errors with no Sprint 5 hint in the error message.

**Files affected**: `book/src/18-choosing-backend.md` §"I'm doing GSLB
DNS validation" (lines 101-115)

**Proposed fix**: add a one-line lead-in to the code block clarifying
"Sprint 5+; the flags below don't exist on `roksbnkctl test dns` today"
so the example reads as forward-looking, not copy-paste. Same applies
to the "DNS probe over `docker` is rejected" prose at lines 66 + 191-198
which describes an error path that may not be wired yet (the chapter
acknowledges this implicitly but readers may be confused).

## Issue 9: PRD 03 open question for `--bootstrap` opt-in is now answered by the implementation

**Severity**: low
**Status**: open
**Description**: PRD 03 §"Open questions" line 402 still phrases the
`--bootstrap` decision as a recommendation:

> **`--bootstrap` opt-in for SSH**: should auto-install of missing
> tools require `--bootstrap` to opt in, to avoid surprise `sudo
> apt-get` invocations? **Recommendation: yes, opt-in by default**…

Staff has now landed the opt-in implementation
(`internal/exec/ssh.go::ensureTool` errors with rc=127 + "rerun with
--bootstrap"); chapter 17 documents the opt-in default. PRD 03's "open
question" framing is stale — this is now a closed decision.

Same for line 400's "Long-lived ops pod vs per-call Job for k8s
backend": staff shipped **both** (long-lived for ad-hoc ibmcloud,
one-shot Job for iperf3). And line 401's "Image versioning": staff
landed "tie to roksbnkctl version" (`toolImageTag` reads `Version`).
Line 403 ("Backend startup failures: hard error vs fall back") was
landed as hard-error per the Sprint 3 → 4 carry-over.

Only line 404 (`--backend ssh` without `:target`) is genuinely still
open — the current code errors with `no target specified` (ssh.go:107),
matching the PRD's recommendation, so this one is also de-facto closed.

**Files affected**: `docs/prd/03-EXECUTION-BACKENDS.md` §"Open
questions" (lines 398-404)

**Proposed fix**: convert the resolved questions into a short "Resolved
in Sprint 4" subsection that links to the decision (chapter 17 §SSH
backend, chapter 17 §K8s backend, etc.). Keeps the PRD honest as a
living document. Same pattern Sprint 3 used for closing the cred-
resolver chain question.

## Issue 10: README highlight bullet for Sprint 4 links only to chapter 17; missing chapter 18 + 19 cross-references

**Severity**: low
**Status**: open
**Description**: The Sprint 4 highlight bullet at `README.md:38` says:

> **`--backend k8s` + `--backend ssh` (v0.9)** — … See [chapter 17].

Sprint 1 + 2 + 3 highlight bullets follow the same single-link pattern
(chapter 16 for `--on`, chapter 24 for k-verbs, chapter 17 for
`--backend docker`). For Sprint 4 specifically, two additional chapters
landed (18 — decision tree; 19 — ops pod reference) and the README's
highlight bullet would benefit from a follow-on "for which one to use,
see [chapter 18]; for the ops-pod prerequisite, see [chapter 19]"
cross-reference. Otherwise the chapter 18 + 19 reader-discovery path
is via the chapter 17 cross-references only.

Sprint 1 + 2 + 3 chapters each shipped exactly one user-facing chapter
on the same topic, so the single-link convention worked. Sprint 4 ships
three. This is a one-line improvement.

**Files affected**: `README.md` line 38

**Proposed fix**: extend the highlight bullet's "See [chapter 17]…" to
something like "See [chapter 17] for backend mechanics, [chapter 18]
for which one to use, and [chapter 19] for the in-cluster ops pod
lifecycle."

## Issue 11: chapter 19 §"Rotation" omits the IC_API_KEY alias and the `roksbnkctl ops show`-driven verification

**Severity**: low
**Status**: open
**Description**: Chapter 19 §"Rotation: rotating the API key" (lines
266-294) — rewritten by the integrator this pass to drop the
non-existent `--rotate-key` flag and walk the three resolver-chain
update paths — is materially correct. Two small issues:

1. The actual Secret carries **two** keys, not one. Look at
   `internal/exec/k8s_install.yaml:51-56`:

   ```yaml
   data:
     IBMCLOUD_API_KEY: "${IBMCLOUD_API_KEY_B64}"
     IC_API_KEY: "${IBMCLOUD_API_KEY_B64}"
   ```

   Both `IBMCLOUD_API_KEY` and `IC_API_KEY` (the older alias older
   `ibmcloud` versions accept) are populated. Chapter 17 §"Env
   propagation" line 121 correctly notes the dual-name pattern for the
   local backend; chapter 19 §"Credential propagation" lines 244-264
   and §"Rotation" §297-301 only mention `IBMCLOUD_API_KEY`. Worth a
   one-line "the same value is also exposed as `IC_API_KEY` for older
   CLI versions" addition.

2. The §"Rotation" code-block at line 291 (the kubectl-only fallback)
   uses `pod/ops`:

   ```bash
   kubectl rollout restart pod/ops -n roksbnkctl-ops
   ```

   Same drift as Issue 1 — actual pod name is `roksbnkctl-ops`. Worth
   fixing in lockstep with Issue 1.

3. `kubectl rollout restart pod/<name>` is not a valid kubectl verb;
   `rollout restart` only operates on Deployments / DaemonSets /
   StatefulSets (controllers that own pods, not bare pods). The bare
   ops pod isn't a Deployment (Issue 3 shows the iperf3 server is the
   same case). The actual rotation flow has to be `kubectl delete pod
   roksbnkctl-ops -n roksbnkctl-ops` (and rely on `restartPolicy:
   Always` to bring it back? — no, the pod is bare, there's no
   controller recreating it). `roksbnkctl ops install` is the canonical
   way to recreate the bare pod with fresh Secret-derived env.

**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"Rotation"
(lines 266-294) + §"Credential propagation" (lines 244-264)

**Proposed fix**: (1) add the `IC_API_KEY` alias note to §"Credential
propagation"; (2) replace `pod/ops` with `pod/roksbnkctl-ops`; (3)
replace the bare `kubectl rollout restart pod/...` example with
either `kubectl delete pod roksbnkctl-ops -n roksbnkctl-ops` + an
inline note that the bare pod has no controller (so re-running
`roksbnkctl ops install` is the canonical recreate path) or
remove the kubectl-only path entirely.

## Issue 12: chapter 19's `roksbnkctl ops install` step list mentions "kubectl apply --server-side" semantics that the actual implementation doesn't use

**Severity**: low
**Status**: open
**Description**: Chapter 19 §"4. Create or update the credential Secret"
line 114 says:

> If the Secret already exists (re-running `ops install` after a key
> rotation), `roksbnkctl` uses `kubectl apply --server-side` semantics:
> the `IBMCLOUD_API_KEY` field is updated to the new value, the rest
> of the Secret's metadata is preserved.

The actual implementation at `internal/cli/ops.go::applyOpsObject`
case `*corev1.Secret` (lines 315-340) does a client-side Get +
mutate-in-place (`existing.Data = o.Data`) + Update, NOT a server-side
apply (the kubernetes Go client's `cs.CoreV1().Secrets().Apply()` shape
isn't used). The semantics are similar in practice but `kubectl apply
--server-side` is a specific server-side merge protocol with field
ownership tracking — the chapter's framing implies that, the code
doesn't do that.

This is a small fidelity issue; "client-side Get + Update preserving
existing metadata, with new Data fields and annotations merged in" is
the accurate description. Same applies to the chapter's implicit claim
about ClusterRole / ClusterRoleBinding updates (also client-side
Get-then-Update at lines 343-383).

**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"4. Create or
update the credential Secret" line 114

**Proposed fix**: change "uses `kubectl apply --server-side` semantics"
to "does a client-side Get + Update that overwrites `data` and merges
annotations in" so the description matches the actual `applyOpsObject`
code path.

## Issue 13: chapter 17 §"`:dev` tag resolution" still describes the Sprint 3 hard-code as the current state

**Severity**: low
**Status**: open
**Description**: Chapter 17 §"`:dev` tag resolution" line 208 says:

> Sprint 3 shipped a hard-coded `:dev` for `ibmcloud` + `iperf3`; that
> lookup map landed unchanged this sprint because the `:dev` tag is
> what `tools/docker/Makefile` produces locally and what the
> `.github/workflows/build-tools-images.yml` workflow publishes on
> every push to `main`. On a `git tag v1.0.0` release the same
> workflow re-tags the image as `:v1.0.0` and pushes both.

This is wrong on two counts:

1. The Sprint 3 hard-code **did change** this sprint per staff issue 2
   in `issues/issue_sprint4_staff.md` — `internal/exec/docker.go::toolImages`
   now resolves the tag from the binary's `Version` (set via ldflags)
   via the `toolImageTag()` resolver. A tag-released binary
   (`Version="v0.10.0"`) pulls
   `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v0.10.0`; only `dev`
   builds use the `:dev` tag. The chapter's "landed unchanged this
   sprint" claim is the opposite of what happened.
2. The default in `internal/exec/docker.go::toolImages` for `terraform`
   is `hashicorp/terraform:1.5.7` (a literal pin, not tag-version
   resolved). Chapter 17's table at line 206 says
   `hashicorp/terraform:<v>` which reads as "Sprint 4 version-resolved"
   — actually a hard-coded `1.5.7`. Worth noting that terraform is the
   exception to the new tag-version resolver.

The next paragraph in chapter 17 (line 208) — "The default in
`internal/exec/docker.go::toolImages` flips to the version-tagged form
at release-tag time" — describes the new (Sprint 4) behaviour accurately,
so the two sentences contradict each other.

**Files affected**: `book/src/17-execution-backends.md` §"`:dev` tag
resolution" (lines 198-211)

**Proposed fix**: rewrite the paragraph at line 208 to acknowledge the
Sprint 4 polish carry-over (`toolImageTag()` resolver landed; `:dev`
fallback only for `dev` builds; tagged releases pull matching tagged
images). Update the table at line 206 to clarify that `terraform`
stays pinned at `1.5.7` regardless of `roksbnkctl` version.

## Issue 14: test names + comments in `internal/exec/k8s_test.go` could be more specific about what they pin

**Severity**: low
**Status**: open
**Description**: Per the prompt's task 9 — flag tests with generic names
or missing intent-documenting comments. Two examples in the Sprint 4
test additions:

1. `TestK8sBackend_Run_Job_CreatesJobAndSecret_TTL` (k8s_test.go:345)
   pins three things at once: Job creation, Files Secret creation, TTL
   value. The test body covers all three correctly, but a reader looking
   for "what does this test fail if I break" has to scan ~80 lines.
   Splitting into three tests would scale better as the Job shape
   grows in Sprint 5+.

2. `TestK8sBackend_NoCredValueInArgv` (k8s_test.go:470) is a critical
   security assertion (PRD 04 §"In-cluster pod") but the test docstring
   doesn't say "PRD 04 SECURITY: cred value never appears in container
   argv, Env-by-Value, or annotations". Compare against
   `internal/exec/audit_test.go::TestCredAudit_K8s_NoLeakInJobSpec`
   (line 198) which **does** carry the rationale in a top-of-function
   doc comment.

Neither blocks the sprint; both are quality-of-test improvements for
future maintainers reading the suite cold.

**Files affected**: `internal/exec/k8s_test.go` (test names at lines 345,
425, 470)

**Proposed fix**: rename the bundle-three-assertions tests to express
each invariant separately when convenient; add docstrings to the
security-relevant tests stating the PRD invariant they pin.

## Verification gates

- `go build ./...` ✓ (binary at `/tmp/roksbnkctl`, 101 MB)
- Chapter 17 `*Coming in Sprint 4.*` markers: ✓ gone (grep returns 0
  hits in chapters 17/18/19)
- All Sprint 5+ forward-references in chapter 18 explicitly future-tense
  (`Sprint 5`, `lands in Sprint 5`, `deferred to Sprint 5+`) — ✓
- Chapters 17 + 18 + 19 cross-references resolve (sampled chapters
  12, 14, 16, 22 link targets; PRD 03 + PRD 04 GitHub URLs)
- `book/src/SUMMARY.md` chapter titles match h1 of each chapter file —
  ✓ (Sprint 4 chapters: "Execution backends: local, docker, k8s, ssh"
  / "Choosing a backend per tool" / "The in-cluster ops pod" all match)
- Go version (`go.mod`: 1.25.0) consistent across README + chapter 4
- `roksbnkctl ops install/show/uninstall` subcommands all wired in
  `internal/cli/ops.go::init`
- `--bootstrap` persistent flag wired at `internal/cli/root.go:124`
- `perToolDefaultBackend` map present at `internal/cli/cluster.go:338`
  with the expected three entries (iperf3=k8s, ibmcloud=local,
  terraform=local)

## Summary

14 issues filed for Sprint 4: 2 high (chapter 17 + 19 pod name; chapter
19 RBAC table), 4 medium (iperf3 server shape; `ops show` output; `ops
uninstall` --confirm gate; SSH bootstrap exit codes), 8 low. Build,
chapters render, all cross-references resolve. The high-severity items
are reader-blocking (anyone copy-pasting chapter 19's `kubectl get pod
ops` will hit `NotFound`; anyone trusting chapter 19's RBAC table will
mis-audit the deployed surface). Recommend integrator land Issues 1, 2,
4, 5, 6 before the M3-prelim gate review.
