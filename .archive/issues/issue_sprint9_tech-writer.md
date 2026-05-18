# Sprint 9 — tech-writer issues

Read-only review of Sprint 9 deliverables (PRD 04 closure: cred tmpfile-bind-mount for docker, trusted-profile auto-provisioning for k8s, `v1.2.0` prep). Chapters reviewed: 14 (Credentials and the resolver chain) and 19 (The in-cluster ops pod). PRD 04, CHANGELOG, PLAN.md cross-referenced.

**Headline verdict on the `v1.2.0` tag: BLOCKED.** Two `high` severity issues (Issues 1 + 2) document Sprint 10's partial closure of the trusted-profile path in chapter 19 + CHANGELOG against what the prompt asserts they call out, and one `high` severity drift (Issue 3) on chapter 19's `--trusted-profile=auto` success-path sample output diverging from `ops.go`'s actual emissions. The fall-back warning text fix from integrator (validator Issue 7 + architect Issue 1) verified clean — verbatim match on three rows against `ops.go:272/293/305`.

Sprint 9 implementation is solid; the open issues are documentation-only and patchable in a doc-only follow-up. None alters the binary surface. Integrator's call whether to land the doc fixes pre-tag (the chapter 19 §"Trusted-profile flow" sample-output drift is the most user-visible) or post-tag.

Severity scale: `low | medium | high | blocker`.
Status scale: `open | resolved | wontfix`.

---

## Issue 1: chapter 19 §"Trusted-profile flow (v1.2+)" claims the success path needs no static-key Secret + uses the projected SA token directly, but `runOnOpsPod`'s ibmcloud login wrap still uses `--apikey "$IBMCLOUD_API_KEY"` (Sprint 10 carry-over invisible to readers)

**Severity**: high
**Status**: open
**Files affected**: `book/src/19-in-cluster-ops-pod.md` (specifically lines 187-193 §"What just happened, in order" step 5; lines 217-221 §"Verifying the profile is in use" describing `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returning a fresh token; line 226 `ops show` "secret: `<none — trusted profile … in use>`")
**Cross-file evidence**: `internal/exec/k8s.go:219` (the v1.0.x apikey login wrap, unchanged by Sprint 9); `internal/cli/ops.go:205-208` (manifest renderer passes empty `manifestKey` when trusted-profile path is in play, so `Secret.data.IBMCLOUD_API_KEY` is empty); `issues/issue_sprint9_staff.md` Issue 2 (the deferred fix).

### What chapter 19 currently asserts

Step 5 of the auto-success walkthrough (line 193):

> **Pod creation.** No `envFrom: secretRef` — instead, the pod gets a projected SA token volume that the IBM IAM SDK inside the ops image trades for a short-lived IAM token on each call. The `IBMCLOUD_API_KEY_FILE` env var (when set) is also honored as an alternative authn path for tools that don't speak the trusted-profile flow; under `--trusted-profile=auto` it's unset and the SDK uses the projected token directly.

And the operational verification (lines 216-221):

> End-to-end smoke test — if the profile is wired correctly, a fresh OAuth token returns:
>
> ```
> $ roksbnkctl --backend k8s ibmcloud iam oauth-tokens
> IAM token:  Bearer eyJ…
> ```

### What actually ships

Staff Issue 2 (deferred) is explicit: the `runOnOpsPod` ibmcloud login wrap is **unchanged** from v1.0.x and still does:

```
ibmcloud login -a https://cloud.ibm.com -r "${IBMCLOUD_REGION:-us-south}" --apikey "$IBMCLOUD_API_KEY" --quiet
```

Under `--trusted-profile=auto` success, the manifest renderer passes `manifestKey=""` (`ops.go:205-208`), so the Secret's `IBMCLOUD_API_KEY` key is the empty string. The pod's container still has `envFrom: secretRef: roksbnkctl-ibm-creds` (the manifest has no branching on this) and `$IBMCLOUD_API_KEY` is empty in the pod. The wrap above fails with `missing API key`. The "fresh OAuth token returns" smoke test in chapter 19 line 217-219 will **not** pass under the v1.2.0 trusted-profile-success path — it will return `failed to authenticate: missing API key` instead.

Chapter 19 also describes the manifest as having no `envFrom: secretRef` when trusted-profile is in play ("No `envFrom: secretRef`"). The actual manifest at `internal/exec/k8s_install.yaml:153-155` always emits `envFrom: secretRef: roksbnkctl-ibm-creds` regardless of trusted-profile mode (the Secret is rendered with empty data fields rather than omitted).

### Why this is high (not blocker)

The CHANGELOG `### Added` line on "Trusted-profile auto-provisioning" promises the user-facing behaviour the chapter 19 walkthrough describes. A v1.2.0 user who runs `roksbnkctl ops install --trusted-profile=auto` (the default) against a workspace with `iam-identity` perms will observe the install completing successfully, the SA carrying the annotation, `ops show` reporting `trusted-profile: <id>` — and then `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returning the "missing API key" error from the login wrap rather than the documented fresh token. The promised v1.2.0 behaviour is partial — the **provisioning side** is shipped but the **runtime side** is staff Issue 2's deferred-to-Sprint 10 work.

Not blocker because the auto-fallback path (which the prompt's task 6 acknowledges as the "most security-minded reader" surface) is the safe-by-default behaviour: any workspace whose API key lacks `iam-identity` perms gets the static-key path with a warning and the wrap works as in v1.0.x. The drift only bites the **auto-success** path, which is gated on `iam-identity` perms being present + the live IBM IAM API providing the cluster CRN successfully.

Integrator-facing prompt context explicitly named this as a Sprint 10 carry-over to surface: "the runtime login wrap still uses static-key, so the security benefit is partial. CHANGELOG `### Deferred` for v1.2.0 calls this out; chapter 19 cross-links it." Both claims are **false** as of `bc4ef8b`:

- CHANGELOG `### Deferred` does NOT mention the runtime login wrap deferral (only customisable policies, SSH backend, `up`/`cluster up` flag). See Issue 2 below.
- Chapter 19 does NOT cross-link it; the §"Trusted-profile flow (v1.2+)" walkthrough is written as if the closure is complete.

### Proposed fix (route to architect)

Chapter 19 §"Trusted-profile flow (v1.2+)" needs a Sprint 10 carry-over admonition near the section's intro. Suggested wording for architect to drop in around line 164:

> **v1.2.0 partial closure.** The `--trusted-profile` flag provisions the trusted profile and annotates the ops pod's SA at `ops install` time, but the in-pod `ibmcloud login` wrap that wraps stateful `ibmcloud` subcommands still uses `--apikey "$IBMCLOUD_API_KEY"`. Under `--trusted-profile=auto` (success), the Secret carries empty data, so the in-pod wrap fails with "missing API key" until the user re-runs with `--trusted-profile=off` or sets `IBMCLOUD_API_KEY` in the pod env by some out-of-band means. Full closure (in-pod wrap switches to `ibmcloud login --trusted-profile-id` when the SA carries the `iam.cloud.ibm.com/trusted-profile` annotation) is Sprint 10 work; see [staff Issue 2](https://github.com/jgruberf5/roksbnkctl/blob/main/issues/issue_sprint9_staff.md#issue-2-ops-pod-runtime-trusted-profile-login-wrap-deferred). The provisioning-side closure (no static key in any Secret under auto-success) is real and verifiable via `oc get secret roksbnkctl-ibm-creds -o yaml`.

Additionally:
- Line 193 "under `--trusted-profile=auto` it's unset and the SDK uses the projected token directly" should be reframed as the Sprint 10 target rather than the v1.2.0 shipped behaviour.
- Lines 217-219's smoke test (`roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returns a fresh token) should be removed or guarded with "(under `--trusted-profile=off` or once Sprint 10's login-wrap closure ships)".
- Line 226 (`ops show` "secret: `<none — trusted profile … in use>`") needs to match what `ops show` actually emits (Issue 4 below — `ops show` does not have that conditional output path).

---

## Issue 2: CHANGELOG `### Deferred (v1.x roadmap, post-v1.2.0)` does NOT mention the in-pod login-wrap Sprint 10 deferral (prompt context asserts it does)

**Severity**: high
**Status**: open
**Files affected**: `CHANGELOG.md` lines 57-63 (`### Deferred` subsection in `## Unreleased (v1.x)`).
**Cross-file evidence**: `issues/issue_sprint9_staff.md` Issue 2 (deferred); `issues/resolved_sprint9_staff.md` Issue 2 (deferral confirmed).

### What's missing

The integrator's prompt context says:

> **Sprint 10 carry-over to be aware of**: staff Issue 2 (deferred). `runOnOpsPod`'s ibmcloud login wrap still does `ibmcloud login --apikey "$IBMCLOUD_API_KEY"` even when the trusted profile is provisioned and annotated. … CHANGELOG `### Deferred` for v1.2.0 calls this out; chapter 19 cross-links it.

The current CHANGELOG `### Deferred` block has three bullets:

1. Workspace-config customisation of trusted-profile policies.
2. Trusted-profile path for the SSH backend.
3. `--trusted-profile` flag on `roksbnkctl up` / `cluster up`.

None of these is the runtime-login-wrap deferral. The most user-impacting Sprint 10 carry-over — the one that makes `--trusted-profile=auto` success a partial closure — is absent from the user-facing release-notes surface.

### Proposed fix (route to architect)

Add a new bullet to `### Deferred (v1.x roadmap, post-v1.2.0)` in `CHANGELOG.md`:

> - **In-pod `ibmcloud login` wrap for the trusted-profile path** — Sprint 9 lands the provisioning side (profile creation, SA annotation, manifest rendering with empty Secret data), but the existing `runOnOpsPod` login wrap at `internal/exec/k8s.go:219` still does `ibmcloud login --apikey "$IBMCLOUD_API_KEY"`. Under `--trusted-profile=auto` success the in-pod wrap will fail with "missing API key" because the Secret data is empty by design. Sprint 10 ships the conditional wrap (`ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"` when the SA carries the trusted-profile annotation, with `IAM_PROFILE_ID` injected into the pod spec at install time). Tracked in [staff Issue 2](https://github.com/jgruberf5/roksbnkctl/blob/main/issues/issue_sprint9_staff.md#issue-2-ops-pod-runtime-trusted-profile-login-wrap-deferred).

This wording closes the "what's the security benefit of the v1.2.0 partial closure" question: the provisioning-side cleanup (no static API key in any Secret at rest in etcd under `--trusted-profile=auto` success) is real and is the v1.2.0 deliverable; the runtime-cred-flow cleanup is Sprint 10.

---

## Issue 3: chapter 19 `--trusted-profile=auto` success sample output diverges from `ops.go`'s actual stderr emissions (apply-line shapes, success summary, idempotent-vs-create vocabulary)

**Severity**: high
**Status**: open
**Files affected**: `book/src/19-in-cluster-ops-pod.md` lines 171-185 (`--trusted-profile=auto` success sample), lines 240-245 (auto fallback sample preamble + footer), lines 266-270 (`--trusted-profile=off` sample), lines 284-293 (`ops uninstall --confirm` sample).
**Cross-file evidence**: `internal/cli/ops.go:402-422` (`tryDel` labels for uninstall); `internal/cli/ops.go:482-587` (apply-side emissions); `internal/cli/ops.go:241,243` (the success summary lines).

### What chapter 19 currently shows vs what the binary emits

Chapter 19's `--trusted-profile=auto` success sample (lines 171-185):

```
$ roksbnkctl ops install --trusted-profile=auto
✓ applied namespace roksbnkctl-ops
✓ applied serviceaccount roksbnkctl-ops/roksbnkctl-ops
✓ applied clusterrole roksbnkctl-ops
✓ applied clusterrolebinding roksbnkctl-ops
checking IAM permissions for trusted-profile provisioning ...
  ✓ iam-identity perms present (key c3b7…b4a2)
provisioning trusted profile roksbnkctl-ops-sandbox-roks ...
  ✓ created profile crn:v1:bluemix:public:iam-identity:::profile/iam-Profile-9f2…
  ✓ linked compute resource: cluster a1b2c3d4 / ns roksbnkctl-ops / sa roksbnkctl-ops
  ✓ attached policies: viewer on container-registry, operator on cloud-object-storage
annotated serviceaccount roksbnkctl-ops with iam.cloud.ibm.com/trusted-profile=roksbnkctl-ops-sandbox-roks
✓ created pod roksbnkctl-ops/roksbnkctl-ops (no static-key Secret needed)
✓ pod Ready (3.4s)
```

`ops.go`'s actual emissions in the success path:

| Stage | Chapter 19 says | Binary actually emits | Source |
|---|---|---|---|
| Namespace | `✓ applied namespace roksbnkctl-ops` | `✓ created namespace roksbnkctl-ops` OR `✓ namespace roksbnkctl-ops exists` | `ops.go:482, 485` |
| ServiceAccount | `✓ applied serviceaccount roksbnkctl-ops/roksbnkctl-ops` | `✓ created sa roksbnkctl-ops/roksbnkctl-ops` OR `✓ sa roksbnkctl-ops/roksbnkctl-ops exists` | `ops.go:495, 498` |
| Secret | (not in success sample) | `✓ created secret roksbnkctl-ops/roksbnkctl-ibm-creds` OR `✓ updated secret …` | `ops.go:508, 525` |
| ClusterRole | `✓ applied clusterrole roksbnkctl-ops` | `✓ created clusterrole roksbnkctl-ops` OR `✓ updated clusterrole …` | `ops.go:535, 546` |
| ClusterRoleBinding | `✓ applied clusterrolebinding roksbnkctl-ops` | `✓ created crb roksbnkctl-ops` OR `✓ updated crb …` | `ops.go:556, 568` |
| Pod | `✓ created pod roksbnkctl-ops/roksbnkctl-ops (no static-key Secret needed)` | `✓ created pod roksbnkctl-ops/roksbnkctl-ops` | `ops.go:587` |
| Pre-trusted-profile preamble | `checking IAM permissions for trusted-profile provisioning ...` | (nothing — there is no preamble line) | n/a |
| TP-perm-check success | `  ✓ iam-identity perms present (key c3b7…b4a2)` | (nothing) | n/a |
| TP-create preamble | `provisioning trusted profile roksbnkctl-ops-sandbox-roks ...` | (nothing) | n/a |
| TP-create success | `  ✓ created profile crn:v1:bluemix:public:iam-identity:::profile/iam-Profile-9f2…` | (single line, different shape) — `✓ Provisioned IAM trusted profile <name> (<ID>)` | `ops.go:312` |
| TP compute-resource link | `  ✓ linked compute resource: cluster a1b2c3d4 / ns roksbnkctl-ops / sa roksbnkctl-ops` | (nothing) | n/a |
| TP policies attached | `  ✓ attached policies: viewer on container-registry, operator on cloud-object-storage` | (nothing — policies are part of profile create, not separately announced) | n/a |
| SA annotation | `annotated serviceaccount roksbnkctl-ops with iam.cloud.ibm.com/trusted-profile=roksbnkctl-ops-sandbox-roks` | (nothing — happens silently during apply) | n/a |
| Pre-ready wait | (not in sample) | `→ Waiting for ops pod to be Ready (60s timeout)` | `ops.go:236` |
| Ready completion | `✓ pod Ready (3.4s)` | `✓ Ops pod is Ready (trusted profile <name>)` | `ops.go:241` |

The vocabulary mismatch is consistent: `applied` (chapter 19) vs `created` / `updated` / `exists` (binary). And the chapter 19 sample has six lines under the trusted-profile-provisioning step that the binary doesn't emit at all. A user running the documented command will see ~5 lines, half with different wording, none of the breakdown the chapter implies.

### `--trusted-profile=auto` fallback sample (lines 240-245)

```
$ roksbnkctl ops install
✓ applied namespace roksbnkctl-ops
warning: IAM perm 'iam-identity' missing; using static-key Secret. Pass `--trusted-profile=off` to silence.
✓ applied secret roksbnkctl-ops/roksbnkctl-ibm-creds (static-key fallback)
✓ pod Ready (3.1s)
```

The `(static-key fallback)` parenthetical on the secret line is **not** emitted by `ops.go:508/525`. Same `applied` vs `created/updated/exists` vocabulary drift as the success sample. The "✓ pod Ready (3.1s)" line should be `✓ Ops pod is Ready (static-key Secret)` per `ops.go:243`. The order is also potentially wrong — the warning fires BEFORE any cluster-side resource is created (it's the return value of `resolveTrustedProfileForInstall` invoked at `ops.go:188` before `applyOpsObject` runs), so the warning prints first, then all the create lines.

The actual fallback session would look approximately:

```
warning: IAM perm 'iam-identity' missing; using static-key Secret. Pass `--trusted-profile=off` to silence.
✓ created namespace roksbnkctl-ops
✓ created sa roksbnkctl-ops/roksbnkctl-ops
✓ created secret roksbnkctl-ops/roksbnkctl-ibm-creds
✓ created clusterrole roksbnkctl-ops
✓ created crb roksbnkctl-ops
✓ created pod roksbnkctl-ops/roksbnkctl-ops
→ Waiting for ops pod to be Ready (60s timeout)
✓ Ops pod is Ready (static-key Secret)
```

### `--trusted-profile=off` sample (lines 266-270)

Same drift: `✓ applied namespace roksbnkctl-ops` vs `✓ created namespace …`. Secret line `✓ applied secret roksbnkctl-ops/roksbnkctl-ibm-creds (static-key, --trusted-profile=off)` — the binary just emits `✓ created secret roksbnkctl-ops/roksbnkctl-ibm-creds` with no `(static-key, --trusted-profile=off)` annotation. Final line `✓ pod Ready (3.0s)` should be `✓ Ops pod is Ready (static-key Secret)`.

### `ops uninstall --confirm` sample (lines 284-293)

Chapter 19 shows:

```
✓ deleted pod roksbnkctl-ops
✓ deleted serviceaccount roksbnkctl-ops
✓ deleted clusterrolebinding roksbnkctl-ops
✓ deleted clusterrole roksbnkctl-ops
✓ deleted namespace roksbnkctl-ops
✓ deleted namespace roksbnkctl-test
✓ deleted IAM trusted profile roksbnkctl-ops-sandbox-roks
```

`ops.go:402-422` orders the deletes as: pod → secret → serviceaccount → clusterrolebinding → clusterrole → namespace ops → namespace test, with the trusted-profile delete happening **first** (via `deleteTrustedProfileIfManaged` at `ops.go:389`, before the `tryDel` calls start). And the trusted-profile success line is `✓ deleted trusted profile %s` (`ops.go:673`), not `✓ deleted IAM trusted profile %s`. Actual order:

```
✓ deleted trusted profile <name>
✓ deleted pod roksbnkctl-ops
✓ deleted secret roksbnkctl-ibm-creds
✓ deleted serviceaccount roksbnkctl-ops
✓ deleted clusterrolebinding roksbnkctl-ops
✓ deleted clusterrole roksbnkctl-ops
✓ deleted namespace roksbnkctl-ops
✓ deleted namespace roksbnkctl-test
```

(Chapter 19 omits the `secret` delete line entirely, which the binary always emits even under `--trusted-profile=auto` success — the Secret manifest is always applied, just with empty data.)

### Why this is high (not blocker)

The success-sample is the **first thing** a v1.2.0 reader following the chapter sees when they run the documented command. Six of the eight prose lines describe operations the binary never logs; the lines that do match the binary use different verb vocabulary. Confidence-eroding on the most-prominent v1.2 surface. Not blocker because the install still works; the docs follow-up is a doc-only chapter rewrite that doesn't re-cut the binary.

### Proposed fix (route to architect)

Replace the three `--trusted-profile=*` sample blocks in chapter 19 with the actual `ops.go` emissions verbatim. The "applied" → "created"/"updated" vocabulary change is mechanical (the manifest's existing-resource path emits `... exists` or `updated ...` — a fresh-cluster install hits the `created` path). The six prescriptive trusted-profile-provisioning lines (perm check preamble, profile-create breakdown, policies, link, annotation) should be replaced with `ops.go:312`'s single line: `✓ Provisioned IAM trusted profile <name> (<id>)`. The `✓ pod Ready (3.4s)` summary should be `✓ Ops pod is Ready (trusted profile <name>)` or `(static-key Secret)` per which branch fired.

This is the same surface validator Issue 7 (resolved on the fallback-warning-table) caught for the fallback case. The fix already landed three rows of fallback-warning verbatim text; the success-path and uninstall-path sample blocks need the same treatment.

---

## Issue 4: chapter 19 `ops show` shape under `--trusted-profile=auto` (line 226) diverges from `ops.go`'s actual output

**Severity**: medium
**Status**: open
**Files affected**: `book/src/19-in-cluster-ops-pod.md` lines 223-227.
**Cross-file evidence**: `internal/cli/ops.go:340-360`.

### What chapter 19 shows

Lines 223-227:

> `roksbnkctl ops show` surfaces the cred posture in one line:
>
> ```
> secret:       <none — trusted profile roksbnkctl-ops-sandbox-roks in use>
> ```

### What actually ships

`ops.go:340-360` prints two independent lines (one for the trusted-profile annotation, one for the secret):

```
trusted-profile: <profile-id>                              # ops.go:346
secret:       roksbnkctl-ibm-creds (rotated <timestamp>)   # ops.go:357
```

There is no `<none — trusted profile X in use>` conditional emit on the `secret:` line. The Secret is always rendered (even with empty data) and `ops show` always emits the `secret:` line. The trusted-profile annotation surfaces on its own `trusted-profile:` line.

### Proposed fix (route to architect)

Replace lines 223-235 of chapter 19 (the `ops show` excerpt + the v1.0.x comparison shape) with the actual two-line emission:

```
trusted-profile: iam-Profile-9f2…
secret:       roksbnkctl-ibm-creds (rotated 2026-05-13T14:08:33Z)
```

And note that the v1.0.x shape (no `trusted-profile:` line) differs only by absence of the first line — both static-key Secret + trusted-profile success render the `secret:` line; the trusted-profile annotation just adds one more line above it.

Also affects the chapter 14 cross-link prose at line 266 which directs readers to "chapter 19 walks through ... how `ops uninstall` cleans up a provisioned profile" — verify that chapter 19's `ops uninstall` sample (Issue 3 above) reflects the actual cleanup order before finalising chapter 14's cross-link wording.

---

## Issue 5: chapter 19 §"Trusted-profile flow (v1.2+)" step 3 "Policy attachment" claims minimal default policies are attached (viewer on container-registry, operator on cloud-object-storage), but `CreateForOpsPod` does not attach any policies

**Severity**: medium
**Status**: open
**Files affected**: `book/src/19-in-cluster-ops-pod.md` lines 191-192.
**Cross-file evidence**: `internal/ibm/trusted_profile.go:105-153` (`CreateForOpsPod` calls only `iam.CreateProfileWithContext` + `ensureLink`, no policy creation).

### What chapter 19 currently asserts

Lines 191-192 (step 3 of the auto-success walkthrough):

> 3. **Policy attachment.** Minimal default policies: viewer on Container Registry (for image pulls if you ever run private-registry tools), operator on Cloud Object Storage (for the BNK supply chain). Adjust via the workspace config's `ibmcloud.trusted_profile.policies` block (post-v1.2 enhancement; v1.2 ships with the defaults).

### What actually ships

`CreateForOpsPod` (`internal/ibm/trusted_profile.go:105-153`) does two things: `iam.CreateProfileWithContext` (creates the profile) and `ensureLink` (binds the cluster CRN + SA as a compute resource). There is **no** call to `iam.CreatePolicyWithContext` or any equivalent policy-attachment API.

The CHANGELOG `### Deferred` bullet "Workspace-config customisation of trusted-profile policies" confirms this is post-v1.2 work — but the bullet's framing ("v1.2 ships with minimal defaults") asserts there ARE defaults shipped. Both chapter 19 and the CHANGELOG bullet describe a behaviour that doesn't ship.

A user reading chapter 19's step 3 will expect `oc get clusterroles` / `ibmcloud iam access-policies` lookups to show the documented defaults attached. They won't.

### Proposed fix (route to architect)

Two routes; architect picks:

**Route A** — update chapter 19 + CHANGELOG to reflect "no default policies in v1.2; user adds via the IBM Cloud console or `ibmcloud iam` CLI". Drop the "viewer on Container Registry, operator on Cloud Object Storage" specifics. Pair with the existing post-v1.2 deferral bullet.

**Route B** — file as a staff follow-up for Sprint 10 to actually attach the documented defaults inside `CreateForOpsPod`. Higher implementation cost; ships the documented behaviour. Lower-confidence approach since "viewer on Container Registry" and "operator on Cloud Object Storage" don't map obviously to specific IAM policy templates and might need product input.

Route A is the lower-risk doc-only fix and matches the "Sprint 9 deliverable is the provisioning + link, policies are a future cycle" framing the CHANGELOG bullet already adopts.

---

## Issue 6: chapter 19 stderr warning text drift on the three fallback shapes (validator Issue 7 + architect Issue 1 — verified resolved by integrator)

**Severity**: high (originally)
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` lines 247-253 — verified against `internal/cli/ops.go:272/293/305`.
**Resolution**: integrator's three-row warning table at chapter 19 lines 249-253 matches `ops.go` verbatim. Confirmed by direct byte-comparison:

- Row 1 (cluster-not-registered) lines 251 ↔ `ops.go:272`: `warning: trusted-profile mode 'auto' needs a registered cluster (<err>); falling back to static-key Secret. Pass `--trusted-profile=off` to silence.` — match.
- Row 2 (cluster-lookup-failed) lines 252 ↔ `ops.go:293`: `warning: trusted-profile mode 'auto' couldn't look up cluster (<err>); falling back to static-key Secret. Pass `--trusted-profile=off` to silence.` — match.
- Row 3 (iam-perm-missing) lines 253 ↔ `ops.go:305`: `warning: IAM perm 'iam-identity' missing; using static-key Secret. Pass `--trusted-profile=off` to silence.` — match (the `<err>` placeholder absent on this row matches the binary's literal-string format-call).

The "actionable detail belongs in this chapter, not in every stderr line" prose framing (lines 255-259 of chapter 19) is good: it correctly characterises the three terse warnings and moves the "ask your IAM admin" remediation to chapter prose rather than the stderr line. Closes validator Issue 7 + architect Issue 1 cleanly.

Logged here only for the audit trail — no further action needed.

---

## Issue 7: chapter 14 §"What's new in v1.2" claims `--trusted-profile=auto` warns with "stderr warning that names the missing perm and how to silence it" — wording correct, but the §"Compatibility note" follow-up paragraph (line 268-270) describes "one extra stderr warning block on `ops install`" — slight wording drift between the singular line and "block"

**Severity**: low
**Status**: open
**Files affected**: `book/src/14-credentials-resolver.md` lines 262 (table row) ↔ 270 (compatibility note).

### What chapter 14 currently says

Line 262 (table row for `auto`):

> Try to provision … If your workspace API key doesn't have IAM `iam-identity` permissions, automatically fall back to the v1.0.x static-key Secret with a stderr warning that names the missing perm and how to silence it (`--trusted-profile=off`).

Line 270 (Compatibility note):

> The k8s `--trusted-profile=auto` default with auto-fallback means existing workspaces against an IAM-restricted key keep getting the static-key Secret as before, with one extra stderr warning block on `ops install` naming the fallback and how to silence it.

The drift: line 262 says "stderr warning" (singular line). Line 270 says "stderr warning block" (suggests multiple lines). The actual binary emits a single line (one of the three shapes in Issue 6 above). Architect Issue 2 already notes this wording was reworded from "one-line stderr warning" to "block" — but the binary actually does emit a single line, not a block. So the "block" wording in chapter 14 line 270 is incorrect by one line.

### Proposed fix

Reword line 270 from "one extra stderr warning block" to "one extra stderr warning line". Mechanical fix.

### Why this is low

Most readers will pattern-match "block" or "line" to the same mental model ("there's a warning at install time"). The drift doesn't change reader behaviour — they'd still know to look at stderr — and the table row at line 262 already correctly says "warning" (singular).

---

## Issue 8: chapter 14 §"What's new in v1.2: the cred-tmpfile and trusted-profile paths" position in the chapter

**Severity**: low
**Status**: open
**Files affected**: `book/src/14-credentials-resolver.md` chapter structure.

### Position assessment

The "What's new in v1.2" section currently sits between §"How `roksbnkctl init` writes the API key" (which is v1.0.x baseline) and §"Backend-specific cred propagation" (which is the existing backend-matrix table). A reader walking the chapter top-to-bottom hits four secrets → resolver chain → init walkthrough → v1.2 callout → backend matrix → redactor → cross-references. This is logical but the v1.2 callout is the **only** version-specific section in an otherwise version-agnostic chapter, which makes it stand out as documentation-of-a-feature rather than reference material.

### Proposed fix

Low priority. Two routes; architect picks:

**Route A** — leave as-is. The "What's new in v1.2" framing tells the reader they're reading delta material; that's useful for v1.0.x → v1.2.x upgraders.

**Route B** — fold the v1.2 material directly into §"Backend-specific cred propagation": the docker paragraph becomes part of the docker row in the backend-matrix table; the `--trusted-profile=auto|on|off` table becomes a sub-table under the k8s row. Cross-link chapter 19 from the table footer rather than from a standalone section. This produces a more "this is how the tool works (with what changed in v1.2 documented inline)" chapter shape that scales better as v1.3+ landings come.

Either route is fine; flagging here in case the integrator wants a more polished chapter-14 structure as part of the v1.2.0 ship. Not blocking.

---

## Issue 9: dogfooding loop — "where does a reader first learn that v1.2 changed cred handling?" — chapter discoverability is good (CHANGELOG + chapter 14 callout both surface it), but chapter 19 §"Credential propagation" (line 387-409) doesn't lead with a v1.2-callout, only a note tucked between sections

**Severity**: medium
**Status**: open
**Files affected**: `book/src/19-in-cluster-ops-pod.md` line 387-389.

### What works

- CHANGELOG `### Added` "Sprint 9 — PRD 04 closure" leads with the two changes, with one-line samples. Discoverable.
- Chapter 14 §"What's new in v1.2" is a top-level section with the docker-tmpfile + `--trusted-profile` flag table. Discoverable.
- Chapter 19 §"Trusted-profile flow (v1.2+)" is a top-level section after `ops install`. Discoverable.

### What stuck

A reader who lands on chapter 19 expecting v1.0.x credential propagation (because they're upgrading from v1.0.x and only know to look at the §"Credential propagation" section for cred questions) will hit the §"Credential propagation" v1.2+ note at line 389:

> **v1.2+ note.** What follows is the **static-key** propagation path. As of v1.2 it's the fallback rather than the default — `--trusted-profile=auto` installs assume an IBM Cloud trusted profile via the pod's projected SA token and the static API key never lands in a Kubernetes Secret. See [§"Trusted-profile flow (v1.2+)"](#trusted-profile-flow-v12) above for that path. The hop-by-hop description below still describes what happens under `--trusted-profile=off` (and under the `auto`-fallback when IAM perms don't allow the trusted-profile path).

This is **good** — it correctly redirects the reader. But the reader scrolling past §"Wait for readiness" (the last `ops install` step) and into §"Trusted-profile flow (v1.2+)" might not hit §"Credential propagation" at all if they're skimming. The reverse-discoverability — a reader who lands at §"Credential propagation" expecting v1.0.x behaviour — works fine; the reader who lands at §"Trusted-profile flow (v1.2+)" first and never reaches §"Credential propagation" is also fine.

The dogfooding stuck-point is the **boundary case**: a reader who skims §"`roksbnkctl ops install`" (the v1.0.x section), expects to see the static-key Secret as documented, and is confused when the chapter then transitions into the trusted-profile path. The transition between §"6. Wait for readiness" and §"Trusted-profile flow (v1.2+)" is abrupt — there's no "but if you're on v1.2 the default is different, see below" callout in §"4. Create or update the credential Secret".

### Proposed fix (route to architect)

Add a one-line `v1.2+ note` to §"4. Create or update the credential Secret" (around line 96-114) mirroring the existing note in §"Credential propagation":

> **v1.2+ note.** This step describes the **static-key** Secret applied under `--trusted-profile=off` and under the `auto`-fallback when IAM perms don't allow the trusted-profile path. Under `--trusted-profile=auto` success the Secret is still applied with **empty data fields** (placeholder for the v1.0.x rollback path); the cred propagation happens via the trusted-profile annotation on the SA instead. See [§"Trusted-profile flow (v1.2+)"](#trusted-profile-flow-v12) below.

Symmetric with the existing note in §"Credential propagation". Low-cost, improves discoverability for skimmers.

---

## Issue 10: dogfooding loop — auto-success path "how to verify it worked" is described in chapter 19 §"Verifying the profile is in use" (lines 196-221), but the verification command at line 217-219 is broken by Issue 1 (the in-pod `ibmcloud login` wrap still uses static-key)

**Severity**: high
**Status**: open (depends on Issue 1 closure)
**Files affected**: `book/src/19-in-cluster-ops-pod.md` lines 213-221.

### What's wrong

Chapter 19 line 213-221:

> End-to-end smoke test — if the profile is wired correctly, a fresh OAuth token returns:
>
> ```
> $ roksbnkctl --backend k8s ibmcloud iam oauth-tokens
> IAM token:  Bearer eyJ…
> ```

A v1.2.0 user running this command after `ops install --trusted-profile=auto` succeeds will hit the staff Issue 2 deferred path — the in-pod login wrap uses `--apikey "$IBMCLOUD_API_KEY"` against an empty env var and fails with "missing API key" rather than returning a token.

### Proposed fix

Either:
- Remove the smoke-test from chapter 19 §"Verifying the profile is in use" entirely, leaving only the `oc get serviceaccount` SA-annotation check as the v1.2.0-verifiable proof.
- Replace the smoke-test with a comment that it works under `--trusted-profile=off` (since the static-key path is functional under that flag) and add a Sprint 10 cross-link for the trusted-profile-success smoke test.

This is **the** stuck-point for a security-minded reader (task 6 of the prompt's dogfooding loop): a reader who follows the chapter's recommended verification will see the install succeed (provisioning side is real), the SA annotation present (also real), and then the smoke test fails. They will reasonably conclude "the install is broken" rather than "the implementation is partial, the smoke test that the docs prescribe doesn't yet work."

Coupled with Issue 1 (the Sprint 10 carry-over callout) and Issue 2 (CHANGELOG `### Deferred` mention of the runtime-login wrap), this issue closes once those two upstream fixes land. Track here as a dogfooding-loop high-severity stuck-point that's a symptom of Issues 1+2.

---

## Issue 11: dogfooding loop — `--trusted-profile=off` opt-out path is documented as a first-class choice (chapter 14 table row + chapter 19 §"`--trusted-profile=off`")

**Severity**: low (positive finding — informational)
**Status**: resolved

The opt-out path is well-documented:

- Chapter 14 line 264 names it explicitly as a first-class choice with three use cases (compatibility, debugging, restricted-IAM clusters).
- Chapter 19 lines 261-278 has a dedicated `### --trusted-profile=off` subsection with three documented use cases (v1.0.x byte-for-byte parity, air-gapped clusters, cred-rotation runbook compatibility) and a sample.

A reader who wants v1.0.x behaviour will find it without buried-as-footnote framing. The third value `--trusted-profile=on` is described in chapter 19 line 278 as "the inverse" — also well-positioned.

No fix needed. Logging as the positive dogfooding-loop finding for completeness.

---

## Issue 12: dogfooding loop — docker `docker inspect` no-leak path discoverability

**Severity**: low (positive finding — informational)
**Status**: resolved

A reader who hit the v1.1.x `NoLeakInInspect` failure and is upgrading to v1.2 will:

- See the CHANGELOG `### Added` "Cred tmpfile-bind-mount pattern" bullet → links to PRD 04 §"Resolved in Sprint 9" → "Cred tmpfile-bind-mount pattern (docker backend)".
- See the CHANGELOG `### Fixed` "TestIntegration_DockerBackend_NoLeakInInspect re-enabled" bullet → confirms the v1.1.x regression is closed.
- See the CHANGELOG `### Changed` "`--backend docker` cred propagation" bullet → describes the env-shape change.
- Find chapter 14 §"What's new in v1.2 → The tmpfile-bind-mount pattern" → one-paragraph user-facing explainer + the "takeaway is one line" framing.
- PRD 04 §"Resolved in Sprint 9 → Cred tmpfile-bind-mount pattern (docker backend)" → full design rationale.

Three independent entry points (CHANGELOG bullets, chapter 14, PRD 04) all converge on the same closure. The chapter 14 framing — "for most users the takeaway is one line: in `v1.2`, `--backend docker` is `docker inspect`-clean" — is the right user-facing summary.

No fix needed. Logging as the positive dogfooding-loop finding.

---

## Issue 13: drift between PRD 04 §"Resolved in Sprint 9" §"Trusted-profile auto-provisioning" §"Workspace namespacing" and the chapter 19 sample (`roksbnkctl-ops-sandbox-roks` vs `roksbnkctl-ops-<workspace>`)

**Severity**: low
**Status**: open
**Files affected**: `docs/prd/04-CREDENTIALS.md` line 43 (`roksbnkctl-ops-<workspace>` — abstract); `book/src/19-in-cluster-ops-pod.md` line 178 (`roksbnkctl-ops-sandbox-roks` — concrete); `internal/cli/ops.go:299` (`"roksbnkctl-ops-" + cctx.WorkspaceName`).

### Drift

The PRD's abstract framing `roksbnkctl-ops-<workspace>` matches the code's literal name-construction. The chapter 19 sample uses the concrete name `roksbnkctl-ops-sandbox-roks` — but the workspace name pattern in canonical examples elsewhere in the book is `dev` / `prod` / `canada-roks`. `sandbox-roks` doesn't appear as a documented workspace anywhere else in the book; it's a one-off in this sample.

### Proposed fix

Mechanical wording change: replace `sandbox-roks` with one of the canonical workspace names used elsewhere (`dev` or `canada-roks` from the v1.1 release notes). Affects:

- Chapter 19 line 178: `roksbnkctl-ops-sandbox-roks`
- Chapter 19 line 182: same
- Chapter 19 line 207: `iam.cloud.ibm.com/trusted-profile: roksbnkctl-ops-sandbox-roks`
- Chapter 19 line 226: `<none — trusted profile roksbnkctl-ops-sandbox-roks in use>`
- Chapter 19 line 292: `✓ deleted IAM trusted profile roksbnkctl-ops-sandbox-roks`

Low priority; doesn't change correctness, just cross-chapter consistency.

---

## Launch-readiness verdict for `v1.2.0`

**BLOCKED** on Issues 1 + 2 + 3 + 10 before a clean v1.2.0 ship.

### Specific blockers

1. **Issue 1** (high, open) — chapter 19 §"Trusted-profile flow (v1.2+)" describes auto-success as a complete closure; staff Issue 2's deferred runtime-login wrap means auto-success is a partial closure. Chapter 19 prose needs a Sprint 10 carry-over admonition + the step 5 "the SDK uses the projected token directly" line needs reframing.
2. **Issue 2** (high, open) — CHANGELOG `### Deferred` block omits the runtime-login wrap deferral that the integrator's prompt context asserts is documented there.
3. **Issue 3** (high, open) — chapter 19's three `--trusted-profile=*` sample blocks (auto-success, auto-fallback, off) diverge from `ops.go`'s actual stderr emissions. The fallback-warning text fix from integrator (validator Issue 7) closed the three-row table; the surrounding sample-output blocks were not similarly aligned.
4. **Issue 10** (high, open, depends on Issue 1) — chapter 19 §"Verifying the profile is in use" smoke test (`roksbnkctl --backend k8s ibmcloud iam oauth-tokens`) will fail under `--trusted-profile=auto` success because of Issue 1's underlying gap.

### Doc-only blockers — alternative path

All four blockers are documentation-only. The binary, tests, CI, Makefile, and PRD surfaces are clean and shippable. The integrator has two routes:

**Route A — fix-the-docs-then-tag.** Route the four high-severity issues to architect, land doc-only fixes against `book/src/19-in-cluster-ops-pod.md` + `CHANGELOG.md`, then proceed with the gate-checklist in the prompt. Estimated cost: a single architect doc-only commit; no re-test cycle.

**Route B — tag-then-patch-docs.** Cut `v1.2.0` against `bc4ef8b`, ship the doc-only fix as a `v1.2.1` patch release a day or two later. The binary works correctly for `--trusted-profile=off` (the v1.0.x byte-for-byte parity path) and for `--trusted-profile=auto` fallback (the IAM-perm-missing case). The auto-success path's "missing API key" symptom from Issue 1 will hit any v1.2.0 user whose workspace API key has `iam-identity` perms — the prompt's audience characterisation. Not recommended.

**Recommendation: Route A.** The doc-only fixes are mechanical, the test surface is unchanged, and shipping `v1.2.0` with the four high-severity blockers in the user-facing docs will create user-support load that's avoidable with a single pre-tag doc commit.

---

## Re-verification pass — 2026-05-13

**Issue 1: CLOSED.** Chapter 19 line 166 now opens §"Trusted-profile flow (v1.2+)" with a "v1.2.0 partial closure — read this first" callout. The callout distinguishes the **provisioning side** (profile creation, compute-resource binding, SA annotation, empty-data Secret manifest — all shipping in v1.2.0) from the **runtime side** (in-pod `ibmcloud login` wrap still uses `--apikey "$IBMCLOUD_API_KEY"` unchanged from v1.0.x — Sprint 10 work). Names the actual failure mode (`missing API key`) under auto-success. Cross-links to [staff Issue 2](`issues/issue_sprint9_staff.md`). Names the `--trusted-profile=off` workaround for users who need the runtime wrap to actually work in v1.2.0. Step 5 (line 193) reframes "the SDK uses the projected token directly" as the Sprint 10 target, not v1.2.0 shipped behaviour. All four sub-criteria satisfied.

**Issue 2: CLOSED.** CHANGELOG.md line 61 adds a new top-of-list bullet to `### Deferred (v1.x roadmap, post-v1.2.0)` titled "In-pod `ibmcloud login` wrap for the trusted-profile path (Sprint 10)". The bullet:
- Names the file: `internal/exec/k8s.go`.
- Names the failure mode: "fails with `missing API key` when stateful `ibmcloud` subcommands actually run inside the pod (the Secret data is empty by design)".
- Names the Sprint 10 target: "the conditional wrap (`ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"` when the SA carries `iam.cloud.ibm.com/trusted-profile`)".
- Links to staff Issue 2.
- Names the `--trusted-profile=off` workaround.

All five sub-criteria satisfied.

**Issue 3: CLOSED.** All four sample blocks rewritten and verified verbatim against `internal/cli/ops.go`:

- **Success sample (chapter 19 lines 172-183)** — uses `created namespace`, `created sa`, `created secret`, `created clusterrole`, `created crb`, `created pod` (matches `ops.go:482/495/508/535/556/587`); the single line `✓ Provisioned IAM trusted profile roksbnkctl-ops-sandbox-roks (iam-Profile-9f2…)` matches `ops.go:312`; pre-ready `→ Waiting for ops pod to be Ready (60s timeout)` matches `ops.go:236`; success summary `✓ Ops pod is Ready (trusted profile roksbnkctl-ops-sandbox-roks)` matches `ops.go:241`. The six prescriptive trusted-profile-provisioning lines are gone. Confirmed `applied` vocabulary replaced with `created`/`updated`/`exists` (line 185 explains the re-run shape).
- **Auto-fallback sample (lines 234-245)** — the warning prints FIRST (line 236), then the `created` lines follow. Final summary is `✓ Ops pod is Ready (static-key Secret)` (matches `ops.go:243`). The `(static-key fallback)` parenthetical on the Secret line is gone.
- **`--trusted-profile=off` sample (lines 265-275)** — same `created`/`✓ Ops pod is Ready (static-key Secret)` shape, no `(static-key, --trusted-profile=off)` annotation.
- **Uninstall sample (lines 290-299)** — `✓ deleted trusted profile roksbnkctl-ops-sandbox-roks` is FIRST (matches `ops.go:673`), then pod, secret, serviceaccount, clusterrolebinding, clusterrole, namespaces (matches `tryDel` order at `ops.go:402-422`). Secret-delete line included. Trailing prose at line 301 correctly notes the secret is always deleted and the trusted-profile delete is best-effort.

Step-3 "Policy attachment" prose (line 191) corrected inline — now states "v1.2 ships with no default policies attached" rather than asserting the old "viewer on container-registry, operator on cloud-object-storage" defaults. Folds in Issue 5's medium fix as a side benefit.

**Issue 10: CLOSED.** Chapter 19 §"Verifying the profile is in use" (lines 195-228) now frames the `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` smoke test with a `> Heads up — Sprint 10 carry-over` admonition (line 220). The admonition explicitly names the `failed to authenticate: missing API key` failure mode under v1.2.0 auto-success, shows the `--trusted-profile=off` workaround as a working v1.2.0 alternative (with sample output `IAM token: Bearer eyJ…`), and names the Sprint 10 closure timing ("Once Sprint 10 lands, the `--trusted-profile=auto` smoke test returns the token directly"). All three sub-criteria satisfied.

**Cross-cutting consistency: OK.** Chapter 19's partial-closure admonition (line 166) and CHANGELOG.md's `### Deferred` bullet (line 61) reference the same Sprint 10 carry-over with the same conceptual shape: provisioning-side ships, runtime-side defers, `missing API key` is the failure mode, `--trusted-profile-id` is the Sprint 10 target, `--trusted-profile=off` is the workaround, staff Issue 2 is the canonical tracker. The Issue 10 smoke-test guard (line 220) routes to the same `--trusted-profile=off` workaround that §"`--trusted-profile=off`" (line 261) documents as a first-class subsection with three named use cases. No divergence between the four fixes.

**Final verdict: clear-to-commit-and-tag.** All four HIGH-severity issues (1, 2, 3, 10) are closed against `book/src/19-in-cluster-ops-pod.md` + `CHANGELOG.md` with verbatim verification against `internal/cli/ops.go`. The 9 deferred medium/low issues per the integrator's resolved file are acknowledged and out of scope for this pass. Integrator may proceed with the pre-tag gate (rename `## Unreleased (v1.x)` → `## v1.2.0 — <date>`) and the `make release VERSION=v1.2.0` + `git tag -a v1.2.0` sequence.

### After Route A

Once Issues 1+2+3+10 land:

1. The integrator's pre-tag gate (rename `## Unreleased (v1.x)` → `## v1.2.0 — <date>`) runs cleanly.
2. The `make release VERSION=v1.2.0` driver (now with `staticcheck` + `-tags integration` build steps per validator's commit) exercises all 7 gate steps. Validator confirmed all 7 are green on `bc4ef8b`.
3. `git tag -a v1.2.0` against `main`.
4. `goreleaser release` + `make release-publish VERSION=v1.2.0`.

### Non-blockers (filed but doc-only follow-up acceptable)

- Issue 4 (medium): `ops show` sample drift — patchable post-tag.
- Issue 5 (medium): Policy-attachment promise vs reality — patchable post-tag OR Route B (staff implements). Architect should pick.
- Issue 7 (low): Chapter 14 "block" vs "line" wording — patchable post-tag.
- Issue 8 (low): Chapter 14 structure — optional polish.
- Issue 9 (medium): Chapter 19 cred-propagation v1.2+ note placement — improves discoverability but not a blocker.
- Issue 13 (low): `sandbox-roks` workspace-name consistency — patchable post-tag.

### Single most-important thing the integrator should know

**The v1.2.0 trusted-profile auto-success path is a partial closure** (provisioning side is real, runtime side defers to Sprint 10). The current chapter 19 + CHANGELOG read as if it's a complete closure. Either the docs need to honestly frame the partial closure (recommended — adopt Issues 1+2 fixes) or the runtime-login wrap needs to land before tag (high implementation cost, drops Sprint 10 carry-over). The provisioning side is genuinely useful (no static API key in any Secret at rest under auto-success); the runtime side gap is symmetrically genuinely a gap (the in-pod `ibmcloud iam oauth-tokens` smoke test will fail). Don't ship a release-notes claim that the smoke test works.
