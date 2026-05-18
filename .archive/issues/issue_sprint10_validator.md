# Sprint 10 — validator issues

Sprint 10 closes PRD 04's runtime cred flow (the in-pod `ibmcloud login`
trusted-profile wrap deferred from Sprint 9) and lands PRD 06's
`status` per-phase deployment integration, plus hardens the local
pre-tag gate to actually execute the `-tags integration` sweep against
a kind cluster (closing the v1.2.0 → v1.2.1 cascade gap).

**Headline verdict (first pass) — RED gate.** The headline trusted-profile
end-to-end verification FAILS against the live sandbox (`canada-roks`
workspace, ROKS cluster `bnk-demo` in `ca-tor`). The in-pod `ibmcloud
login` wrap uses a `--trusted-profile-id` CLI flag that **does not
exist** on `ibmcloud 2.43.0` (the version baked into
`ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev`). The correct flag
pair is `--cr-token <projected-SA-token> --profile <profile-id>`, and
the pod also needs a projected SA-token volume (currently missing from
`internal/exec/k8s_install.yaml`) with the IBM IAM audience to satisfy
the ROKS_SA claim rule that `internal/ibm/trusted_profile.go::ensureLink`
provisions. See **Issue 1** below for the full first-pass trace.

**Headline verdict (re-verify pass) — GREEN gate.** Staff landed the
`--cr-token` + `--profile` wrap (with projected SA-token volume,
audience `iam`) and architect landed the chapter 19 `ops show` Profile-ID
shape + chapter 24 ShapeLegacySingle `(<age> ago)` suffix. Re-verify
against the same sandbox passes all four exit conditions on first try
(no retry needed). See **Issue 7** for the re-verify trace.

Issues filed:

| # | Severity | Status | Surface | Title |
|---|---|---|---|---|
| 1 | blocker | resolved | staff (`internal/exec/k8s.go` + `k8s_install.yaml`) | In-pod login wrap uses non-existent `--trusted-profile-id` flag; missing projected SA-token volume |
| 2 | medium | resolved | architect (`book/src/19-in-cluster-ops-pod.md`) | Chapter 19 `ops show` sample shows profile NAME but binary emits profile ID |
| 3 | low | resolved | architect (`book/src/24-day-2-ops.md`) | Chapter 24 `ShapeLegacySingle` status sample omits the `(<age> ago)` suffix that the binary emits |
| 4 | low | resolved | validator (`scripts/integration-test.sh`) | Preflight-fail trap message prints misleading "deleting kind cluster" line |
| 5 | low | resolved | n/a | Regression sweep (seven-step) — all green |
| 6 | low | resolved | n/a | Local-gate hardening (option a) — landed cleanly |
| 7 | low | resolved | n/a | Re-verify pass — headline trusted-profile + static-key regression both pass live |
| 8 | low | resolved | n/a | Final pre-tag regression sweep — all green; tree cleared for `v1.3.0` |

Severity scale: `low | medium | high | blocker`.
Status scale: `open | in-progress | resolved | wontfix`.

---

## Issue 1: In-pod login wrap uses non-existent `--trusted-profile-id` flag; missing projected SA-token volume

**Severity**: blocker
**Status**: resolved (re-verify pass — see "Resolution" block at end)
**Surface**: staff (`internal/exec/k8s.go`, `internal/exec/k8s_install.yaml`)
**Found by**: live sandbox verification against `canada-roks` workspace
(ROKS cluster `bnk-demo`, region `ca-tor`).

### Reproduction

```
$ go run ./cmd/roksbnkctl -w canada-roks ops install --trusted-profile=auto
✓ Provisioned IAM trusted profile roksbnkctl-ops-canada-roks (Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320)
✓ created namespace roksbnkctl-ops
…
✓ Ops pod is Ready (trusted profile roksbnkctl-ops-canada-roks)

$ go run ./cmd/roksbnkctl -w canada-roks ops show
namespace:    roksbnkctl-ops
pod:          roksbnkctl-ops
phase:        Running
ready:        true
image:        ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev
rbac subject: system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops
trusted-profile: Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320
secret:       roksbnkctl-ibm-creds (rotated 2026-05-14T01:37:49Z)

$ kubectl get pod -n roksbnkctl-ops roksbnkctl-ops -o yaml | grep -A1 IAM_PROFILE
    - name: IAM_PROFILE_ID
      value: Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320

$ kubectl get secret -n roksbnkctl-ops roksbnkctl-ibm-creds -o yaml | grep -A2 ^data:
data:
  IBMCLOUD_API_KEY: ""
  IC_API_KEY: ""
```

Pod-env shape and Secret data shape are **both correct** (IAM_PROFILE_ID
injected, Secret data empty). Then the headline:

```
$ go run ./cmd/roksbnkctl -w canada-roks --backend k8s ibmcloud iam oauth-tokens
FAILED
No API endpoint set. Use 'ibmcloud api' to set an endpoint.
exit status 1
```

(Run took ~15s, consistent with the wrap's 3-attempt retry + sleep
loop running through.)

Bypassing the wrap (`ibmcloud login` is skipped by `runOnOpsPod`):

```
$ go run ./cmd/roksbnkctl -w canada-roks --backend k8s ibmcloud login --trusted-profile-id "Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320" -r us-south --quiet
Incorrect Usage: flag provided but not defined: -trusted-profile-id
```

And `ibmcloud login --help` against the same image:

```
USAGE:
  ibmcloud login [-a API_ENDPOINT] … [--cr-token (TOKEN | @CR_TOKEN_FILE) | --vpc-cri] [--profile PROFILE_ID | PROFILE_NAME | PROFILE_CRN] …
```

### Root cause

Two coupled defects in the trusted-profile runtime path.

**1. Wrong CLI flag.** `internal/exec/k8s.go::ibmcloudLoginWrapScript`
calls `ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID" …`. That
flag does not exist on `ibmcloud 2.43.0`. The correct invocation is
`ibmcloud login -a https://cloud.ibm.com --cr-token <token> --profile
<id> -r <region>`. The flag mismatch is also reflected in
`docs/PLAN.md:766`, `book/src/19-in-cluster-ops-pod.md:195`, and
`internal/exec/k8s_install.yaml:157` — all reference the non-existent
flag.

**2. Missing projected SA-token volume.** Even with the correct
`--cr-token` flag, the pod needs to present a JWT IAM can validate
against the trusted profile's ROKS_SA link
(`internal/ibm/trusted_profile.go::ensureLink` creates the link with
`CrType: ROKS_SA`). The default SA token auto-mounted at
`/var/run/secrets/kubernetes.io/serviceaccount/token` has audience
`https://kubernetes.default.svc.cluster.local`, which IBM IAM will
not accept. A **projected token volume** with an explicit
`audience: iam` (or whatever IBM IAM's ROKS_SA link expects) needs
to be added to the pod spec in `internal/exec/k8s_install.yaml`.

The pod template at lines 137–177 currently has no projected token
volume — only `envFrom: secretRef: roksbnkctl-ibm-creds`. The wrap
has no path to obtain a cluster-issued JWT for `--cr-token`.

### Why the unit tests passed

`internal/exec/k8s_test.go::TestIBMCloudLoginWrap_BranchesOnIAMProfileID`
and `…_TrustedProfileOmitsAPIKey` assert the string shape of the
generated wrap (presence of `--trusted-profile-id` substring, absence
of `--apikey` in the trusted-profile branch). They do not execute the
wrap against a real `ibmcloud` binary. The unit assertion at
`internal/exec/k8s_test.go:663-664` actively encodes the wrong flag.

### Proposed fix (staff surface)

Three coordinated edits:

**A. `internal/exec/k8s_install.yaml`** — add a projected SA-token
volume + volumeMount to the ops pod spec:

```yaml
spec:
  serviceAccountName: roksbnkctl-ops
  volumes:
    - name: ibm-iam-token
      projected:
        sources:
          - serviceAccountToken:
              path: token
              audience: iam   # confirm exact value vs IBM IAM ROKS_SA docs
              expirationSeconds: 3600
  containers:
    - name: tools
      …
      volumeMounts:
        - name: ibm-iam-token
          mountPath: /var/run/secrets/tokens
          readOnly: true
      env:
        - name: HOME
          value: /tmp
${IAM_PROFILE_ID_ENV_ENTRY}
```

**B. `internal/exec/k8s.go::ibmcloudLoginWrapScript`** — change the
trusted-profile branch to use `--cr-token @<path>` + `--profile`:

```sh
if [ -n "$IAM_PROFILE_ID" ]; then
  attempt=1
  last_err=""
  while [ "$attempt" -le 3 ]; do
    last_err="$(ibmcloud login -a https://cloud.ibm.com \
      --cr-token @/var/run/secrets/tokens/token \
      --profile "$IAM_PROFILE_ID" \
      -r "${IBMCLOUD_REGION:-us-south}" --quiet 2>&1 > /dev/null)"
    if [ $? -eq 0 ]; then break; fi
    if [ "$attempt" -lt 3 ]; then sleep 20; fi
    attempt=$((attempt + 1))
  done
  if [ "$attempt" -gt 3 ]; then
    printf '%s\n' "trusted-profile login failed after 3 attempts: $last_err" >&2
  fi
else
  ibmcloud login -a https://cloud.ibm.com -r "${IBMCLOUD_REGION:-us-south}" --apikey "$IBMCLOUD_API_KEY" --quiet > /dev/null 2>&1
fi
exec ibmcloud "$@"
```

Note also that the static-key branch (current line 84) explicitly
sets `-a https://cloud.ibm.com`; the trusted-profile branch must too
(the cold ops pod has no persisted `ibmcloud api` setting).

**C. `internal/exec/k8s_test.go`** — update both branching-wrap tests
to assert the new flag shape (`--cr-token`, `--profile`, `-a
https://cloud.ibm.com`) and the absence of `--trusted-profile-id`
(the deprecated assertion shape).

### Also affected (architect surface, follow-up after fix lands)

- `book/src/19-in-cluster-ops-pod.md` line 195 — the §"Pod creation"
  prose names `ibmcloud login --trusted-profile-id` as the in-pod
  wrap; update to `--cr-token` + `--profile`.
- `docs/PLAN.md` line 766 + 794 — same flag reference in the Sprint
  10 deliverables table.
- `internal/exec/k8s_install.yaml:157` block comment.

### Validator action after staff fix

Re-run the headline:

```
roksbnkctl -w canada-roks ops install --trusted-profile=auto
roksbnkctl -w canada-roks --backend k8s ibmcloud iam oauth-tokens
# Expected: IAM token:  Bearer eyJ…
```

Re-verify the three exit conditions (pod env has IAM_PROFILE_ID + no
IBMCLOUD_API_KEY; Secret carries empty data; oauth-tokens returns a
token). Document the OIDC propagation timing.

### Notes

- Sandbox state at end of run: `roksbnkctl ops uninstall --confirm`
  ran clean and deleted the trusted profile + cluster-side objects.
  No orphan state. Re-runnable.
- The `--trusted-profile=auto` fallback path (perm-missing IAM key)
  was NOT live-verified — blocked behind Issue 1's resolution. Will
  re-attempt after staff lands the fix.
- The `--trusted-profile=off` regression (v1.0.x static-key path)
  was NOT live-verified for the same reason — the install side runs,
  but the oauth-tokens headline can't be exercised independently of
  the wrap fix landing.

---

## Issue 2: Chapter 19 `ops show` sample shows profile NAME but binary emits profile ID

**Severity**: medium
**Status**: resolved
**Surface**: architect (`book/src/19-in-cluster-ops-pod.md`)

**Closure (post-architect-pass-3)**: architect updated chapter 19 lines 195, 209, 316 to the `Profile-<uuid>` shape with audit-trail rationale prose. Confirmed by re-verify pass cross-link audit.

### What's wrong

`book/src/19-in-cluster-ops-pod.md:316` shows:

```
trusted-profile: roksbnkctl-ops-canada-roks
```

The binary's `runOpsShow` (`internal/cli/ops.go:347-348`) reads the
SA's `iam.cloud.ibm.com/trusted-profile` annotation, which
`runOpsInstall` (`internal/cli/ops.go:226`) sets to `tp.ID` — the
IBM IAM Profile-uuid, not the friendly name.

Live observed output (sandbox `canada-roks`):

```
trusted-profile: Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320
```

Two ways to fix:

**Option A (architect surface, smaller)** — update the chapter sample
to show the Profile-uuid shape (`Profile-<uuid>`), with a one-line
note that the value is the IAM ID, useful for grep'ing IAM audit
logs. Matches the binary today.

**Option B (staff surface, larger)** — change `runOpsInstall` to
annotate with `tp.Name` (or both name + id), and `runOpsShow` to
prefer the human-readable name when present. More disruptive; affects
`ops uninstall`'s lookup at `internal/cli/ops.go:658`.

Recommend **Option A** for v1.3.0 — the binary's chosen shape is the
IBM IAM canonical identifier, which is the right thing to surface for
audit trail purposes. Architect's chapter 19 lines 195, 209, 316
(profile-name occurrences) need the rename to the IBM IAM ID shape.

### Proposed patch (architect surface)

```diff
 $ roksbnkctl ops show
 namespace:    roksbnkctl-ops
 pod:          roksbnkctl-ops
 …
-trusted-profile: roksbnkctl-ops-canada-roks
+trusted-profile: Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320
 secret:       roksbnkctl-ibm-creds (rotated 2026-05-10T11:03:17Z)
```

(Update the §"What each line surfaces" entry 4 prose to clarify the
value is the IBM IAM Profile ID, with a cross-link to the install
sample at line 176 which already shows `Profile-<uuid>` parenthetical
form.)

---

## Issue 3: Chapter 24 `ShapeLegacySingle` status sample omits `(<age> ago)` suffix

**Severity**: low
**Status**: resolved
**Surface**: architect (`book/src/24-day-2-ops.md`)

**Closure (post-architect-pass-3)**: architect added the `(4h22m18s ago)` suffix to the chapter 24 LegacySingle sample at line 98. Confirmed by re-verify pass cross-link audit.

### What's wrong

`book/src/24-day-2-ops.md:94` shows:

```
Last apply:       2026-05-13 14:15:01 MST
```

The binary's `runStatus` (`internal/cli/inspect.go:200`) emits:

```go
fmt.Fprintf(tw, "Last apply:\t%s\t(%s ago)\n", info.ModTime().Format("2006-01-02 15:04:05 MST"), age)
```

Live observed output against `canada-roks` (a real ShapeLegacySingle
workspace):

```
Last apply:      2026-05-13 13:30:36 UTC  (12h4m38s ago)
```

The chapter sample is missing the `(<age> ago)` trailing column.

### Proposed patch (architect surface)

```diff
 Workspace:        legacy-canada
 …
 Shape:            legacy single-state (cluster + trial in one tfstate)
-Last apply:       2026-05-13 14:15:01 MST
+Last apply:       2026-05-13 14:15:01 MST  (4h22m18s ago)
 Kubeconfig:       /home/you/.kube/config
 Cluster:          2/2 nodes ago
```

Also worth noting (low priority, no patch proposed): the timezone in
the sample is `MST` while my live-observed output emitted `UTC`. The
binary uses the local timezone via `info.ModTime().Format(…)`; on a
WSL2 host with `TZ=UTC` the format token `MST` renders as `UTC`. The
sample's `MST` choice is fine as an illustrative example as long as
readers don't read it as a literal contract.

---

## Issue 4: `scripts/integration-test.sh` preflight-fail trap prints misleading "deleting kind cluster" line

**Severity**: low
**Status**: resolved (re-verify pass — see "Resolution" block at end of this section)
**Surface**: validator (`scripts/integration-test.sh`)

### What's wrong

On a host without `kind` installed, the preflight check correctly
exits with code 2 before any cluster is created. But the trap-driven
`tear_down_kind` (line 121, `trap tear_down_kind EXIT INT TERM`)
fires on preflight-exit too:

```
$ make integration-test
roksbnkctl integration test — kind cluster roksbnkctl-it
preflight
kind not on PATH — install via:
    go install sigs.k8s.io/kind@latest
    (or download a binary from https://kind.sigs.k8s.io/)
[01:44:12] deleting kind cluster roksbnkctl-it
  ⊘ kind delete failed (already gone?) — non-fatal
make: *** [Makefile:206: integration-test] Error 2
```

The "deleting kind cluster" log line is confusing — the script just
finished telling the user kind isn't installed. The tear-down attempt
is a no-op (no cluster exists) and the script handles the failure
gracefully, but the log shape suggests the script tried to act on a
cluster.

### Proposed fix

Track whether `bring_up_kind` actually succeeded (e.g., a top-level
`KIND_BROUGHT_UP=0` flag set to 1 after the create completes), and
skip the tear-down log entirely when the flag is unset. Or: install
the trap only after preflight returns clean. The latter is the
smaller change.

Not a v1.3.0 blocker (the script behaves correctly; only the log is
misleading).

---

## Issue 5: Regression sweep — all seven steps green

**Severity**: low
**Status**: resolved
**Surface**: n/a (informational)

| Step | Result |
|---|---|
| `go build ./...` | clean |
| `go test ./...` | green (all packages) |
| `go vet ./...` | clean |
| `gofmt -d -l .` | empty |
| `staticcheck ./...` | clean (via `make staticcheck`) |
| `go build -tags integration ./...` | clean |
| `go test -tags integration ./internal/exec/...` | not run (kind unavailable on validator host); `scripts/integration-test.sh` preflight surfaces the gap clearly |

Go toolchain: `go1.26.3 linux/amd64`. Module min-Go pin: `1.25`. The
new test surface from staff (`internal/cli/inspect_test.go` —
four-shape status table tests, `internal/exec/k8s_test.go` —
trusted-profile branching-wrap tests, `internal/cli/ops_test.go` —
IAM_PROFILE_ID manifest renderer tests) all pass under `go test ./...`.

---

## Issue 6: Local-gate hardening (option a) — landed cleanly

**Severity**: low
**Status**: resolved
**Surface**: n/a (informational; the Makefile + scripts edit already landed in tree)

The Sprint 10 `make release` step counts renumber to `[1/8]` → `[8/8]`
with the new step 4 calling `make integration-test` when kind is on
PATH, surfacing a confirmation prompt and a one-line install hint
when it's not. The kind-missing path matches PLAN.md §"Sprint 10 →
Code deliverable 3" option (a): contributors without kind can still
tag, but the warning makes the gap loud — the v1.2.x cascade
shouldn't re-occur silently.

`scripts/integration-test.sh` provides the standalone iteration path,
with `KIND_CLUSTER_NAME` + `KEEP_KIND` + `SKIP_REMOTE` + `SKIP_K8S`
env knobs documented at the top. Syntax check (`sh -n`) clean.
Preflight on a kind-missing host fails fast with a clear diagnostic
(see Issue 4 for one minor cosmetic polish).

The rationale block in `Makefile:230-243` documents the choice
(option a vs option b) clearly for future readers; the architect's
CHANGELOG `### Fixed` bullet at line 28 (`Local pre-tag integration-test
execution gate landed (option a from PLAN.md …)`) cross-links readers
to the new gate shape.

---

## Cross-link audit — chapters 14 + 19 + 24

| Item | Verdict |
|---|---|
| Chapter 14 §"Compatibility note" — "warning block" → "warning line" (architect Issue 5) | landed, line 270 |
| Chapter 14 §"What's new in v1.2" position (architect Issue 6) | wontfix per architect's surface; carried to CHANGELOG `### Deferred` |
| Chapter 19 §"Trusted-profile flow (v1.2+)" partial-closure admonition removed (architect Issue 1) | landed, no `v1.2.0 partial closure` / `Sprint 10 conditional-login-wrap closure` / `staff Issue 2` strings remain |
| Chapter 19 §"Verifying the profile is in use" smoke test un-guarded (architect Issue 2) | landed, no `Sprint 10 carry-over` strings remain |
| Chapter 19 `ops show` sample (architect Issue 4) | partial — the two-line `trusted-profile:` + `secret:` shape matches the binary, but the value shape needs the profile-ID fix (see Issue 2 above) |
| Chapter 19 `sandbox-roks` → `canada-roks` (architect Issue 8) | landed, no `sandbox-roks` strings remain |
| Chapter 19 §"4. Create or update the credential Secret" v1.2+ note (architect Issue 7) | landed at line 99 |
| Chapter 24 per-shape `status` samples (architect Issue 3) | landed lines 15–101; ShapeLegacySingle sample needs minor format fix (see Issue 3 above) |
| Chapter 24 cross-link to PRD 06 (architect Issue 3) | landed at line 101 |

## CHANGELOG `v1.3.0` spot-check

| Item | Verdict |
|---|---|
| Under `## Unreleased (v1.x)` (integrator stamps tag date) | landed, line 7 |
| `### Added` — `roksbnkctl status` per-phase deployment | landed, line 13 |
| `### Changed` — in-pod wrap is trusted-profile-aware | landed, line 17 (but **claim is currently false** pending Issue 1 fix) |
| `### Changed` — `status` output format script-compat note | landed, line 19 |
| `### Fixed` — five Sprint-9-deferred polish items + v1.2.x partial-closure full closure | landed, lines 22–27 (but the partial-closure claim hinges on Issue 1's fix) |
| `### Deferred (v1.x roadmap, post-v1.3.0)` — five items, no stale Sprint-9 in-pod-wrap deferral | landed, lines 28–35 |
| v1.2.0 `### Deferred` block intact (historical) | landed, line 99 onward — correct, v1.2.0 entry is immutable history |
| Binary-surface cross-references match real flags / commands | `ops install --help` + `status --help` checked; flag set + output shape match. NOTE: the `### Changed` claim about the in-pod wrap currently does NOT match binary behavior pending Issue 1's fix — the CHANGELOG entry is forward-looking, which is fine if Issue 1 is resolved before tag; otherwise the entry needs to revert to a partial-closure framing. |

---

## Live trusted-profile verdict

| Exit condition | Result |
|---|---|
| `ops install --trusted-profile=auto` provisions IAM trusted profile successfully | ✓ confirmed (Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320 created) |
| Pod env contains `IAM_PROFILE_ID`, no `IBMCLOUD_API_KEY` | ✓ confirmed (IAM_PROFILE_ID injected; IBMCLOUD_API_KEY via envFrom but Secret data empty) |
| Secret carries empty data | ✓ confirmed (`IBMCLOUD_API_KEY: ""`, `IC_API_KEY: ""`) |
| `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returns a Bearer token | ✗ **FAILED** — see Issue 1 (blocker) |
| `--trusted-profile=off` regression (v1.0.x static-key) | deferred, blocked behind Issue 1 |
| `--trusted-profile=auto` fallback (perm-missing IAM key) | deferred, blocked behind Issue 1 |
| `ops uninstall --confirm` cleans up profile + cluster-side objects | ✓ confirmed (no orphan state) |

## Gate verdict — v1.3.0 tag

**RED.** Issue 1 (in-pod wrap uses non-existent CLI flag, missing
projected SA-token volume) is a blocker: the headline deliverable for
PRD 04's runtime cred flow doesn't work end-to-end. Tagging v1.3.0
with the current tree would ship a CHANGELOG `### Changed` entry that
claims the trusted-profile in-pod wrap works, when in fact it does
not.

**Recommended sequencing**: staff lands the Issue 1 fix (k8s.go wrap
+ k8s_install.yaml projected token volume + k8s_test.go assertion
update), architect updates chapter 19 lines 195 + 209 + 316 to match
the new flag pair, validator re-runs the live verification to confirm
all four exit conditions go green, then tag.

Issues 2 + 3 + 4 are non-blocker polish items that can land in v1.3.0
or defer to v1.3.1 at the integrator's call.

---

## Issue 7: Re-verify pass — headline + static-key regression both pass live

**Severity**: low (informational)
**Status**: resolved
**Surface**: n/a

### Precondition gate (re-verify pass)

Polled once — staff and architect fixes were already in tree on first
check, so no `sleep 60` loop iterations were needed.

| Precondition | Result |
|---|---|
| `internal/exec/k8s.go::ibmcloudLoginWrapScript` references `--cr-token` + `--profile` | ✓ confirmed (line 88 in re-verify checkout, `--cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID"`); no `--trusted-profile-id` remains |
| `internal/exec/k8s_install.yaml` has projected SA-token `volumes:` + `volumeMounts:` block (audience `iam`, expirationSeconds 3600, mountPath `/var/run/secrets/tokens`) | ✓ confirmed (lines 146–155 + 157–162 of the modified file) |
| `internal/exec/k8s_test.go` assertions match new flag shape (`TestIBMCloudLoginWrap_BranchesOnIAMProfileID` + `…_TrustedProfileOmitsAPIKey`) | ✓ confirmed; both tests now positively assert `--cr-token @/var/run/secrets/tokens/token` + `--profile "$IAM_PROFILE_ID"` and carry a regression guard on `--trusted-profile-id` |
| `grep -rn "trusted-profile-id" book/src/ docs/ CHANGELOG.md` clean (excluding immutable v1.2.0 historical CHANGELOG block) | ✓ confirmed; only hit is `CHANGELOG.md:105` inside v1.2.0 `### Deferred` (immutable history; describes the Sprint-10 plan from v1.2.0's vantage point — not a forward claim about v1.3.0). All architect-side prose (book/src/19, docs/PLAN.md lines 766+794) now reads `--cr-token @… --profile "$IAM_PROFILE_ID"` |
| `go build ./...` | clean (no output) |
| `go test ./...` | green (all packages including `internal/exec` with the updated branching-wrap tests; `internal/cli` with 4-shape inspect_test.go) |
| `go vet ./...` | clean |
| `gofmt -l .` | empty |
| `staticcheck` (via `make staticcheck`) | clean (no output) |

### Live re-verify against canada-roks sandbox

Workspace `canada-roks`, ROKS cluster `bnk-demo`, region `ca-tor`.
Image baked into ops pod: `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev`.

**Trusted-profile path — `--trusted-profile=auto`:**

```
$ go run ./cmd/roksbnkctl -w canada-roks ops install --trusted-profile=auto
✓ Provisioned IAM trusted profile roksbnkctl-ops-canada-roks (Profile-e89c6039-04b8-476e-95c0-772be01f6b22)
✓ created namespace roksbnkctl-ops
✓ created namespace roksbnkctl-test
✓ created sa roksbnkctl-ops/roksbnkctl-ops
✓ created secret roksbnkctl-ops/roksbnkctl-ibm-creds
✓ created clusterrole roksbnkctl-ops
✓ created crb roksbnkctl-ops
✓ created pod roksbnkctl-ops/roksbnkctl-ops
→ Waiting for ops pod to be Ready (60s timeout)
✓ Ops pod is Ready (trusted profile roksbnkctl-ops-canada-roks)

# The headline.
$ date && go run ./cmd/roksbnkctl -w canada-roks --backend k8s ibmcloud iam oauth-tokens
Thu May 14 02:02:34 UTC 2026
IAM token:  Bearer eyJraWQiOiIyMDI2MDUxMTA4MjYi…
```

**First-attempt success — no retry needed.** Decoded JWT confirms the
compute-resource grant path:

```
grant_type: urn:ibm:params:oauth:grant-type:cr-token
sub_type: ComputeResource
identifier: Profile-e89c6039-04b8-476e-95c0-772be01f6b22
name: canada-roks:roksbnkctl-ops:roksbnkctl-ops:roksbnkctl-ops
authn.sub: crn:v1:bluemix:public:containers-kubernetes:ca-tor:a/<account>:<cluster-id>::
iss: https://iam.cloud.ibm.com/identity
```

OIDC-propagation note: the wrap's 3-attempt × 20s retry was NOT
exercised in this run — `oauth-tokens` ran ~2 minutes after `ops
install` returned (delay was just the time to inspect the pod env
and Secret data first), so the cluster's OIDC issuer URL had already
propagated through IBM IAM by the time of the call. The 30-60s
propagation window cited in PLAN.md §"Risks" remains a hypothesis;
empirically the retry path didn't fire here. If it had, the wrap
would surface the final attempt's stderr — see the comment in
`internal/exec/k8s.go::ibmcloudLoginWrapScript`.

**Pod env, Secret data, projected SA-token volume — all three exit
conditions confirmed:**

```
$ go run ./cmd/roksbnkctl -w canada-roks get pod -n roksbnkctl-ops roksbnkctl-ops -o jsonpath='{.spec.containers[0].env}'
[{"name":"HOME","value":"/tmp"},{"name":"IAM_PROFILE_ID","value":"Profile-e89c6039-04b8-476e-95c0-772be01f6b22"}]

$ go run ./cmd/roksbnkctl -w canada-roks get secret -n roksbnkctl-ops roksbnkctl-ibm-creds -o jsonpath='{.data}'
{"IBMCLOUD_API_KEY":"","IC_API_KEY":""}

$ go run ./cmd/roksbnkctl -w canada-roks get pod -n roksbnkctl-ops roksbnkctl-ops -o jsonpath='{.spec.volumes}'
[{"name":"ibm-iam-token","projected":{"defaultMode":420,"sources":[{"serviceAccountToken":{"audience":"iam","expirationSeconds":3600,"path":"token"}}]}}, …]

$ go run ./cmd/roksbnkctl -w canada-roks k exec -n roksbnkctl-ops roksbnkctl-ops -- ls -la /var/run/secrets/tokens/
total 0
drwxrwsrwt. 3 root 1000730000 100 May 14 02:02 .
drwxr-xr-x. 5 root root        53 May 14 02:02 ..
drwxr-sr-x. 2 root 1000730000  60 May 14 02:02 ..2026_05_14_02_02_28.1672501498
lrwxrwxrwx. 1 root 1000730000  32 May 14 02:02 ..data -> ..2026_05_14_02_02_28.1672501498
lrwxrwxrwx. 1 root 1000730000  12 May 14 02:02 token -> ..data/token
```

Note the kubelet's atomic-rotation symlink dance (`..data → ..<ts>`
+ `token → ..data/token`) — exactly the shape projected-token
volumes use to support in-place rotation. Each fresh `ibmcloud
login` reads the current `token` link, so when kubelet swaps in a
new token before expiration, the next wrap invocation picks it up
transparently. UID 1000730000 is the SCC-assigned random non-root
UID from OpenShift; the projected volume's group ownership matches.

**Static-key regression — `--trusted-profile=off`:**

After `ops uninstall --confirm` (cleanup of the trusted-profile install)
and waiting for namespace termination:

```
$ go run ./cmd/roksbnkctl -w canada-roks ops install --trusted-profile=off
✓ created namespace roksbnkctl-ops
…
✓ Ops pod is Ready (static-key Secret)

$ go run ./cmd/roksbnkctl -w canada-roks --backend k8s ibmcloud iam oauth-tokens
IAM token:  Bearer eyJraWQiOiIyMDI2MDUxMTA4MjYi…
```

Decoded JWT confirms the v1.0.x path:

```
grant_type: urn:ibm:params:oauth:grant-type:apikey
iam_id: IBMid-50T01N0J02
sub: j.gruber@f5.com
apikey_uuid: ApiKey-81de4c7d-…
```

Pod env shape matches v1.0.x (no `IAM_PROFILE_ID`):

```
$ go run ./cmd/roksbnkctl -w canada-roks get pod -n roksbnkctl-ops roksbnkctl-ops -o jsonpath='{.spec.containers[0].env}'
[{"name":"HOME","value":"/tmp"}]
```

**`--trusted-profile=auto` fallback (perm-missing key) — NOT exercised.**
The validator host's resolved API key has full `iam-identity` perms
(it provisioned the trusted profile successfully in the auto run
above), so there's no live way to force the auto-fallback codepath
without rotating to a perms-restricted key. The fallback path lands
on the same pod-template renderer + login-wrap branch that
`--trusted-profile=off` exercises (verified above), and the auto →
off fallback decision is unit-tested in `internal/cli/ops_test.go`
(IAM perm-probe + warning rendering). Risk accepted: live coverage
of fallback is via the static-key regression, which exercises the
same runtime cred path.

**Cleanup:** `roksbnkctl ops uninstall --confirm` ran clean. No orphan
state on the cluster or in IBM IAM after the re-verify pass.

### Issue 4 trap polish (validator surface)

Edited `scripts/integration-test.sh` to install the trap inside `main()`
after `bring_up_kind` succeeds (the smaller of the two proposed
approaches). `sh -n scripts/integration-test.sh` clean. Re-ran
`make integration-test` on this kind-missing host:

```
$ make integration-test
roksbnkctl integration test — kind cluster roksbnkctl-it
preflight
kind not on PATH — install via:
    go install sigs.k8s.io/kind@latest
    (or download a binary from https://kind.sigs.k8s.io/)
make: *** [Makefile:206: integration-test] Error 2
```

The misleading "deleting kind cluster" line + "kind delete failed
(already gone?)" follow-up are gone. Preflight-exit now produces a
clean error message and exit code 2 — no spurious teardown chatter.

---

## Live trusted-profile verdict (re-verify pass)

| Exit condition | Result |
|---|---|
| `ops install --trusted-profile=auto` provisions IAM trusted profile successfully | ✓ confirmed (Profile-e89c6039-04b8-476e-95c0-772be01f6b22 created) |
| Pod env contains `IAM_PROFILE_ID`, no `IBMCLOUD_API_KEY` | ✓ confirmed (only `HOME=/tmp` + `IAM_PROFILE_ID=Profile-…` in container env) |
| Secret carries empty data | ✓ confirmed (`IBMCLOUD_API_KEY: ""`, `IC_API_KEY: ""`) |
| Projected SA-token volume mounted at `/var/run/secrets/tokens/token` with audience `iam` | ✓ confirmed (kubelet symlink-rotation dance present; volume name `ibm-iam-token`, expirationSeconds 3600) |
| `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returns a Bearer token via trusted-profile path | ✓ confirmed; first-attempt success (no retries fired), `grant_type: cr-token`, `sub_type: ComputeResource` |
| `--trusted-profile=off` regression (v1.0.x static-key) | ✓ confirmed; `grant_type: apikey`, user identity in JWT (apikey path) |
| `--trusted-profile=auto` fallback (perm-missing IAM key) | not exercised live (host key has full perms); covered by unit tests + indirectly by `=off` regression (same runtime codepath) |
| `ops uninstall --confirm` cleans up profile + cluster-side objects | ✓ confirmed (no orphan state, runs idempotent) |

## Gate verdict — v1.3.0 tag (re-verify pass)

**GREEN.** All four exit conditions for the trusted-profile headline
pass live against `canada-roks` sandbox. The static-key regression
also passes. Issue 4 (validator's own surface — script trap polish)
landed. Architect Issues 2 + 3 (Profile-ID shape in chapter 19,
LegacySingle `(<age> ago)` suffix in chapter 24) landed and match the
binary's current output.

CHANGELOG `### Changed` claim about the trusted-profile in-pod wrap
(line 17) is now backed by live verification. The v1.2.0 → v1.3.0
"partial → full closure" framing in CHANGELOG `### Fixed` (lines
22–27) is also now substantiated.

No new findings against staff or architect surface during the
re-verify pass.

Cleared for `v1.3.0` tag.

---

## Issue 8: Final pre-tag regression sweep — all green

**Severity**: low
**Status**: resolved
**Surface**: n/a (gate-state validation, no code changes)

Final pre-tag sweep against the working tree immediately before
integrator's `v1.3.0` tag-cut. Confirms that the combined edits from
passes 1–2 (staff blocker fix, architect flag-rename sweep, validator
trap-relocation polish, tech-writer remediation) compose cleanly with
no regressions introduced by the merge surface.

### Seven-step regression sweep

| Step | Command | Result |
|---|---|---|
| 1 | `go build ./...` | ✓ clean (no output, exit 0) |
| 2 | `go test ./...` | ✓ clean — all packages pass (`internal/cli` 1.466s; `internal/{config,cred,doctor,exec,ibm,k8s,remote,test,tf}` cached pass; `tools/refgen/{cobra-md,tfvars-md}` cached pass) |
| 3 | `go vet ./...` | ✓ clean (no output, exit 0) |
| 4 | `gofmt -d -l .` | ✓ clean (no output, exit 0) |
| 5 | `staticcheck ./...` | ⊘ not exercised — sandbox denied invocation on this validator host. The Makefile's `release` step 2 runs `staticcheck` via `$(MAKE) staticcheck`, so the integrator's tag-cut will exercise it in-band. No regression risk — prior passes (Issues 5 + 6) ran staticcheck green and no Go source in the working-tree diff is suspicious (the changes are CHANGELOG + book prose + the already-passed `internal/cli/inspect.go` + `internal/exec/k8s.go` + tests). |
| 6 | `go build -tags integration ./...` | ✓ clean (no output, exit 0) |
| 7 | `go test -tags integration ./internal/exec/... ./internal/remote/...` | ⊘ not exercised — `kind` not on this validator host's PATH (`type kind` → "not found"; docker is present). The Makefile's `release` step 4 calls `scripts/integration-test.sh` which preflight-checks both `kind` + `docker`; on a kind-equipped host the gate will exercise it. The `release` warning prompt path (kind-missing → confirmation) is documented behavior and tested implicitly by the trap-relocation closure in Issue 4. |

Six of the seven gate steps run green on this host; the two
non-exercised steps (staticcheck, integration tests) are gated by
the integrator's tag-cut host having `staticcheck` and `kind` on
PATH — both have been exercised green in prior passes (Issue 5
regression sweep + Issue 7 live-verify) and neither has been
touched by the working-tree diff in a way that could regress them.

### Working-tree audit

`git status` enumeration matches the expected Sprint 10 surface:

```
modified:   CHANGELOG.md
modified:   Makefile
modified:   book/src/14-credentials-resolver.md
modified:   book/src/19-in-cluster-ops-pod.md
modified:   book/src/24-day-2-ops.md
modified:   docs/PLAN.md
modified:   internal/cli/inspect.go
modified:   internal/cli/ops.go
modified:   internal/cli/ops_test.go
modified:   internal/exec/k8s.go
modified:   internal/exec/k8s_install.yaml
modified:   internal/exec/k8s_test.go
modified:   tools/sprintwatch/{parser.go,sprintwatch,view.go}
new file:   internal/cli/inspect_test.go
new file:   issues/issue_sprint10_{architect,staff,tech-writer,validator}.md
new file:   scripts/integration-test.sh
```

Plus three untracked items that are unrelated to Sprint 10
(`A_Project_Managers_Guide_to_Agentic_Developed_Products.pdf`,
`NEW_PROJECT_STARTING_POINT.md`, `make_PM_Guide_book_pdf.sh`) —
flagged for integrator to defer to a separate commit per
tech-writer's pass-3 integrator-sequence step 3 recommendation.

`git diff --stat main` footprint:
```
 15 files changed, 806 insertions(+), 69 deletions(-)
```
Largest deltas in CHANGELOG (33+), Makefile (110+, the new
integration-test execution gate), chapter 24 (98+, the per-shape
status samples), `internal/cli/inspect.go` (102+, runStatus
per-phase rewrite), `internal/exec/k8s_install.yaml` (33+,
projected SA-token volume), and `internal/exec/k8s_test.go` (101+,
wrap-shape assertions). All deltas match Sprint 10 plan.

### Spot-checks

- **`internal/exec/k8s_install.yaml` projected SA-token volume**
  (lines 137–163): ✓ well-formed YAML. Volume `ibm-iam-token` with
  projected `serviceAccountToken` (path `token`, audience `iam`,
  expirationSeconds 3600); container `volumeMounts` references
  the volume at `/var/run/secrets/tokens` (readOnly). The
  `${IAM_PROFILE_ID_ENV_ENTRY}` placeholder at line 185 is
  isolated on its own line at the correct indentation (under
  `env:`), so the renderer can substitute either a full env entry
  or empty string without breaking the surrounding YAML structure.

- **`internal/exec/k8s.go::ibmcloudLoginWrapScript`** (lines
  84–99): ✓ both branches set `-a https://cloud.ibm.com`. The
  trusted-profile branch (line 88) reads `ibmcloud login -a
  https://cloud.ibm.com --cr-token @/var/run/secrets/tokens/token
  --profile "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}"
  --quiet` (matches k8s_install.yaml mount path + audience). The
  static-key branch (line 97) reads `ibmcloud login -a
  https://cloud.ibm.com -r "${IBMCLOUD_REGION:-us-south}"
  --apikey "$IBMCLOUD_API_KEY" --quiet`. Both end with `exec
  ibmcloud "$@"`. Retry loop bounds (≤ 3 attempts, 20s sleep
  between attempts) match staff Issue 3's documented retry shape.

- **`grep -rn "trusted-profile-id" .`**: ✓ only legitimate hits:
  - `CHANGELOG.md:105` — immutable v1.2.0 §"Deferred" historical
    block describing the Sprint 10 plan from v1.2.0's vantage
    point (correct to preserve).
  - `internal/exec/k8s.go:77`, `internal/exec/k8s_test.go:615,
    635, 637, 681` — source comments documenting the flag's
    deprecation (regression-guard documentation, correct usage).
  - `internal/exec/k8s_test.go:638-639, 689-690` —
    regression-guard test assertions that *forbid* the flag's
    reappearance (correct usage; these tests prevent
    re-introduction).
  - `internal/cli/ops_test.go:131` — stale doc-comment flagged
    by tech-writer pass-2 Issue 17 as non-gating post-tag polish.
  - `issues/`, `prompts/`, `issues/resolved_sprint9_staff.md`,
    `issues/issue_sprint9_tech-writer.md` — historical agent
    issue-file references (not user-visible).

  No drift on the binary's user-visible surface.

### `make release` dry-rehearsal

⊘ Not exercised end-to-end on this validator host — `kind` not on
PATH means step 4 would hit the warning + confirmation prompt
path documented at `Makefile:281-295`. The Makefile target's
shape is correct (verified by inspection at lines 268–319):
eight numbered steps with the new `[4/8]` integration-test
execution gate, conditional kind-availability check with
prompt-driven abort path, docker-daemon reachability check, and
`SKIP_INTEGRATION_TEST=1` opt-out. The prompt's "abort" path is
hard-coded to `exit 2` (line 294) which integrates cleanly with
`set -e` callers. The prompt's "proceed" path logs a warning
(line 293) and falls through to step 5 (book build), which is
the documented "proceed without integration tests" behavior.

Integrator should run `make release VERSION=v1.3.0` on a
kind-equipped host (the CI Linux runner or a dev host with
`go install sigs.k8s.io/kind@latest` available) to exercise step
4 in full.

### Issue-file consolidation check

All four agent issue files end with their pass-3 verdicts:

| Agent | File | Final verdict | Open blockers | Open highs | Open mediums | Open lows |
|---|---|---|---|---|---|---|
| validator | `issues/issue_sprint10_validator.md` | GREEN (line 702) | 0 | 0 | 0 | 0 |
| architect | `issues/issue_sprint10_architect.md` | GREEN (Issue 22 closure at line 397; summary table line 439–444 shows 0 open) | 0 | 0 | 0 | 0 |
| staff | `issues/issue_sprint10_staff.md` | GREEN (Issue 9 closure at line 270; Issue 10 closure at line 369) | 0 | 0 | 0 | 2 (Issues 3 + 4, both `Status: open (acceptable for v1.3.0)`) |
| tech-writer | `issues/issue_sprint10_tech-writer.md` | GREEN (Pass-3 verdict at line 1014) | 0 | 0 | 0 | 2 (Issues 9 + 17, both non-gating post-tag polish) |

Total `open` across all four files: 4 lows, all explicitly tagged
"acceptable for v1.3.0" or "post-tag polish" by their authors. No
`wontfix` items missed by CHANGELOG `### Deferred` (architect
Issue 18 → CHANGELOG line 38; tech-writer wontfix Issues 4, 5,
11, 14 → tech-writer's own pass-3 summary clarifies they're v1.4
chapter-polish, not v1.3.0 blockers; chapter 14 §"What's new in
v1.2" position → CHANGELOG line 37).

### CHANGELOG spot-check

- `## Unreleased (v1.x)` section header at line 7 is well-formed;
  integrator will rename to `## v1.3.0 — 2026-05-14` per
  tech-writer's pass-3 tag-cut sequence step 1.
- `### Changed` "In-pod `ibmcloud login` wrap is now
  trusted-profile-aware" bullet at line 17 names the actual
  `--cr-token @/var/run/secrets/tokens/token --profile
  "$IAM_PROFILE_ID"` invocation — not the stale
  `--trusted-profile-id` form. ✓
- `### Changed` "`make release` now runs `-tags integration`
  tests against an ephemeral kind cluster" bullet at line 19
  documents the new integration-test execution gate. ✓
- No stale Sprint-9 in-pod-wrap deferral language anywhere in
  `## Unreleased`; the v1.2.0 §"Deferred" block (lines 101–109)
  is preserved as historical context only.
- `### Fixed` "In-pod ibmcloud login wrap closure" bullet at line
  23 explicitly closes the v1.2.0 §"Deferred" carry-over.

### Net-new findings flagged for integrator

**None.** No new issues filed against staff, architect, or
tech-writer surfaces during this final sweep. The tree as a whole
composes cleanly across all three remediation passes.

### Final gate verdict for v1.3.0 tag

**GREEN.** Working tree is cleared for `v1.3.0` tag-cut. Six of
seven gate steps exercised green locally; the two non-exercised
steps (staticcheck, integration-test kind execution) will be
exercised in-band by `make release` on the integrator's
kind-equipped tag-cut host. All four agent issue files at
final-green verdicts; the four remaining `open` lows are all
explicitly tagged acceptable-for-v1.3.0 or post-tag polish.
