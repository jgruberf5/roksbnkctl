# Sprint 10 — staff issues

Format: one issue per finding. `Severity: low | medium | high | blocker`.
`Status: open | in-progress | resolved | wontfix`.

Sprint 10 closes PRD 04's runtime cred flow (the in-pod `ibmcloud login`
wrap that Sprint 9 deferred as staff Issue 2) and PRD 06's `status`
integration (the requirement added post-Sprint-9 in commit `4e5f103`).
Headline: `roksbnkctl ops install --trusted-profile=auto` followed by
`roksbnkctl --backend k8s ibmcloud iam oauth-tokens` now returns a
fresh IAM token end-to-end — the v1.2.0 partial-closure admonition in
chapter 19 comes out (architect's surface).

---

## Issue 1: Sprint 9 staff Issue 2 (in-pod `ibmcloud login` wrap) — closed

**Severity**: medium (carry-over from Sprint 9)
**Status**: resolved

**Context**: Sprint 9 staff Issue 2 deferred the in-pod ibmcloud login
wrap closure to Sprint 10. Sprint 9 landed the **provisioning side**
(trusted profile creation, SA annotation, manifest renderer with empty
Secret data) but `internal/exec/k8s.go::runOnOpsPod`'s wrap remained
the v1.0.x `ibmcloud login --apikey "$IBMCLOUD_API_KEY" …` unchanged,
which fails with "missing API key" under `--trusted-profile=auto`
success (the Secret carries empty data by design).

**Closure**: three coordinated edits.

1. `internal/exec/k8s_install.yaml` — added the `${IAM_PROFILE_ID_ENV_ENTRY}`
   placeholder under the existing pod `env:` block. Renderer substitutes
   either a full env entry (trusted-profile path: `IAM_PROFILE_ID: <id>`)
   or the empty string (static-key path).
2. `internal/cli/ops.go::decodeOpsManifests` — signature extended to
   take `iamProfileID string`; `runOpsInstall` threads through the
   `tp.ID` from `resolveTrustedProfileForInstall` when the trusted-
   profile path is taken, empty otherwise.
3. `internal/exec/k8s.go::runOnOpsPod` — wrap replaced with a shell
   conditional gated on `$IAM_PROFILE_ID`. When set, runs `ibmcloud
   login --trusted-profile-id "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}"
   --quiet` with a **3-attempt retry, 20s backoff** to absorb the
   cluster's OIDC-issuer propagation window (30-60s after `ops install`
   returns; documented risk in `docs/PLAN.md` §"Sprint 10 → Risks").
   When empty, the v1.0.x `--apikey` form runs unchanged. Final attempt's
   stderr is surfaced to the caller's stderr on triple-fail so users see
   a useful diagnostic, then `exec ibmcloud "$@"` flows through verbatim.

**Tests**: `internal/exec/k8s_test.go` two new tests pin the wrap
shape (`TestIBMCloudLoginWrap_BranchesOnIAMProfileID` + `…_TrustedProfileOmitsAPIKey`) —
crucially asserts the trusted-profile branch does NOT leak `--apikey`,
which would partially defeat the security gain.
`internal/cli/ops_test.go` three new tests pin the manifest renderer
(`TestDecodeOpsManifests_TrustedProfile_InjectsIAMProfileID` +
`…_StaticKey_NoIAMProfileID` + `…_TrustedProfile_HOMEStillPresent`).

**Action for validator**: live-verify against sandbox IBM Cloud per
PLAN.md §"Sprint 10 → Test deliverables" — `cluster up` → `ops install
--trusted-profile=auto` → `bnk up` → `roksbnkctl --backend k8s ibmcloud
iam oauth-tokens` must return a fresh IAM token (no `missing API key`).
Validator's findings against the 3-attempt retry's adequacy go into
the validator's resolved file and may feed back as polish work here.

---

## Issue 2: PRD 06 `status` per-shape deployment lines — landed

**Severity**: low (informational)
**Status**: resolved

**Context**: PRD 06 §"`status` command integration" added in commit
`4e5f103` is Sprint 10's second deliverable. The v1.0.x single
`Last apply` line at `internal/cli/inspect.go::runStatus` conflates
the cluster phase and the BNK trial; the four-shape phase split from
Sprint 8 (PRD 06) means a reader can't tell which phase is currently
deployed without grepping tfstate by hand.

**Closure**: `runStatus` consumes `config.DetectShape(workspace)` after
the existing workspace/region/cluster lines, then emits per-shape lines:

- `ShapeEmpty` → `Cluster phase: not deployed` + `BNK trial: not deployed`
  (the v1.0.x `Last apply` line replaced).
- `ShapeClusterOnly` → `Cluster phase: deployed (last apply <timestamp>)`
  + `BNK trial: not deployed`.
- `ShapeSplit` → both phases with their own mtimes from
  `state-cluster/terraform.tfstate` and `state/terraform.tfstate`.
- `ShapeLegacySingle` → one-line shape callout
  (`Shape: legacy single-state (cluster + trial in one tfstate)`)
  **plus** the verbatim v1.0.x `Last apply` line for script-compat —
  per PRD 06's explicit requirement that existing parsers stay stable.
- `ShapeUnknown` / `DetectShape` error → defensive fall-back to the
  v1.0.x `Last apply` line so the command never silently swallows a
  whole section.

Helpers `writeStatusPhaseLines`, `deployedLine`, `writeLegacyLastApply`
sit alongside `runStatus` in `internal/cli/inspect.go`. All filesystem
failures degrade silently to `not deployed` — every section in
`runStatus` is best-effort by convention.

**Tests**: `internal/cli/inspect_test.go` (new) four-shape table test
reusing Sprint 8's `internal/config/testdata/tfstate_{empty,cluster_only,split,legacy_single}.json`
fixtures. Each shape asserts the correct line-set appears and the
wrong-shape lines don't (e.g., ShapeLegacySingle must NOT emit
`Cluster phase:` / `BNK trial:`; ShapeSplit must NOT emit the v1.0.x
`Last apply` line). Uses `ROKSBNKCTL_HOME=t.TempDir()` + a minimal
`SaveWorkspace` + the bnk_phase_test.go fixture-copy pattern.

**Smoke-verified locally**: `go run ./cmd/roksbnkctl -w <ws> status`
against synthetic workspaces in all four shapes produces the expected
output. Verified by hand (terminal session in scratchpad; not committed).

**Action for architect**: chapter 24 (Day-2 ops) per-shape `status`
samples need to mirror these line shapes verbatim. The fixtures' real
mtimes are runtime-dependent, so architect's chapter samples need a
literal timestamp string (e.g., `2026-05-14 00:42:21 UTC`) that's
clearly an example, not an assertion.

---

## Issue 3: in-wrap retry backoff is a fixed 20s × 3 — no exponential, no jitter

**Severity**: low
**Status**: open (acceptable for v1.3.0; revisit if validator's live
verify shows pathological cases)

**Context**: the trusted-profile login wrap retries 3 times with 20s
backoff between attempts. Total window: ~60s, which matches the upper
bound of the cluster OIDC issuer propagation delay PRD 04 cites. The
implementation is the minimum viable retry — fixed delay, no jitter,
no exponential growth, no per-attempt timeout.

**Why this is OK for v1.3.0**: a single ops pod with a single wrap
invocation doesn't need jitter (no thundering herd). The 60s upper
bound is the documented OIDC-propagation window; if a real env exceeds
it, the retry would mask the underlying issue rather than fix it.
Cleaner failure mode: surface the final attempt's stderr to the
caller (which the wrap does) and let the user re-run.

**Action**: none for v1.3.0. If validator's live verify shows that
the 60s window is sometimes longer (e.g., the IBM IAM-side cache TTL
is bigger than the cluster-side TTL), Sprint 11 can extend the bound.
Currently this is a YAGNI-bounded knob.

**Note (post-validator-reverify)**: validator's live re-verify against
the `canada-roks` sandbox returned GREEN on the first attempt — the
retry loop did not fire. The 20s × 3 shape is confirmed acceptable for
v1.3.0; carries forward as v1.4.x polish only if a future env exhibits
a longer OIDC-propagation window.

---

## Issue 4: wrap script writes to stderr unconditionally on triple-fail; could mask the user's `ibmcloud` subcommand's own stderr if it also writes there

**Severity**: low
**Status**: open (acceptable for v1.3.0)

**Context**: the wrap's `printf '%s\n' "trusted-profile login failed
after 3 attempts: $last_err" >&2` runs before `exec ibmcloud "$@"`.
If the user's `ibmcloud <subcmd>` also writes to stderr (which most
non-trivial subcommands do), the wrap's error line will appear
interleaved with the subcommand's output. For a CLI consumer parsing
stderr line-by-line this is benign; for a script that greps for a
specific error pattern, the wrap's prefix line could break the match.

**Action for v1.4.x**: consider a flag (env var?) to silence the
wrap's stderr on success even if all 3 retries failed (the
`exec ibmcloud "$@"` will produce its own missing-token diagnostic).
Tracked here so it doesn't get lost. Not a v1.3.0 blocker.

**Note (post-validator-reverify)**: validator's live re-verify against
the `canada-roks` sandbox returned GREEN on the first attempt — the
triple-fail stderr path was not exercised. No interleaving observed
in practice for v1.3.0; carries forward as v1.4.x polish.

---

## Issue 5: Sprint 9 tech-writer Issues 4, 7, 8, 9, 13 (deferred polish) — architect surface

**Severity**: low
**Status**: resolved

**Context**: PLAN.md §"Sprint 10 → Carry-overs" names five tech-writer
issues from Sprint 9 as architect's chapter-polish surface for v1.3.0:

- Sprint 9 tech-writer Issue 4 (`ops show` shape — chapter 19)
- Sprint 9 tech-writer Issue 7 (chapter 14 "warning block" → "warning line")
- Sprint 9 tech-writer Issue 8 (chapter 14 §"What's new in v1.2" position)
- Sprint 9 tech-writer Issue 9 (chapter 19 cred-propagation v1.2+ note placement)
- Sprint 9 tech-writer Issue 13 (chapter 19 `sandbox-roks` → canonical workspace name)

**Action for architect**: confirmed under architect's surface
(book/src + CHANGELOG). Staff didn't touch these; they don't affect
code.

**Update (post-architect-pass-1)**: Architect closed all five Sprint-9-deferred polish items in their first remediation pass — see `issues/issue_sprint10_architect.md` Issues 4 (tech-writer Issue 4), 5 (Issue 7), 6 (Issue 8, wontfix), 7 (Issue 9), 8 (Issue 13).

---

## Issue 6: smoke verify status (Sprint 10 staff-scope)

**Severity**: low (informational)
**Status**: resolved

| Check | Result |
|---|---|
| `go build ./internal/... ./cmd/...` | clean |
| `go vet ./internal/... ./cmd/...` | clean |
| `gofmt -d -l internal cmd` | empty |
| `go test ./internal/...` | green (all packages; new tests pass) |
| `staticcheck ./internal/... ./cmd/...` | clean (via `~/go/bin/staticcheck`) |
| `go build -tags integration ./internal/... ./cmd/...` | clean |
| `go run ./cmd/roksbnkctl -w <synthetic> status` × 4 shapes | all four shapes produce the expected per-shape output (Empty / ClusterOnly / Split / Legacy) |
| `decodeOpsManifests` IAM_PROFILE_ID env injection | unit-test-verified: trusted-profile path injects, static-key path doesn't, HOME env preserved on both |

**Not covered by staff scope** (validator / integrator):
- Live IBM Cloud trusted-profile end-to-end smoke test (validator).
- `TestIntegration_K8sBackend_OpsPodExec` against a kind cluster
  with the v1.3.0 tools-ibmcloud image (validator; requires kind).
- The integration-test execution gate in `make release` (validator's
  Makefile/scripts edit per PLAN.md §"Sprint 10 → Code deliverable 3").

---

## Issue 7: `internal/cli/inspect_test.go` uses `t.Context()` (Go 1.24+) — verify min-Go pinned correctly

**Severity**: low
**Status**: resolved

**Context**: the new status test uses `cmd.SetContext(t.Context())`,
which requires Go 1.24+. The project's `go.mod` directive is Go 1.25+
per the prompt; CI uses `go version go1.26.3` locally (verified).
`t.Context()` returns a context that's auto-cancelled on test end,
matching the existing `context.Background()` pattern in bnk_phase_test.go
without the manual cleanup boilerplate.

No action — pinned correctly.

---

## Issue 8: tech-writer Issue 3 — `statusCmd.Long` doc drift after PRD 06 per-phase shape

**Severity**: medium
**Status**: resolved

**Context**: tech-writer review (Sprint 10) Issue 3 flagged that
`statusCmd.Long` (`internal/cli/inspect.go:34-44`) still described the
v1.0.x `last terraform apply timestamp (mtime of terraform.tfstate)`
bullet — which `runStatus` no longer emits for non-Legacy shapes after
the Issue 2 closure above. A user running `roksbnkctl status --help`
against a v1.3.0 binary saw a doc string that didn't match the
command's actual output on a `ShapeEmpty` / `ShapeClusterOnly` /
`ShapeSplit` workspace.

**Closure**: replaced the fourth bullet in `statusCmd.Long` with two
bullets (Option A from tech-writer's suggestion):

```
  - per-phase deployment status (cluster phase + BNK trial)
  - v1.0.x `Last apply` line preserved for legacy single-state workspaces
```

`go build ./...`, `go vet ./...`, `gofmt -l internal/cli/inspect.go`
all clean. No test asserts on the `Long` string so no test update
needed.

---

## Final report

**Headline**: Sprint 10 staff deliverables landed cleanly. The Sprint 9
staff Issue 2 "in-pod ibmcloud login wrap" deferral is closed; the
PRD 06 §"`status` command integration" is implemented and tested
against all four shapes from the Sprint 8 fixture set. Tech-writer
Issue 3 (`statusCmd.Long` doc drift) closed in this remediation pass.

**v1.3.0 readiness from staff side**: ready. The four chapter-19 + CHANGELOG
items in the architect's surface plus validator's live trusted-profile
sandbox verify are the remaining gates before tag.

---

## Issue 9: validator Issue 1 (in-pod wrap uses non-existent `--trusted-profile-id`; no projected SA-token volume) — closed

**Severity**: blocker (carry-over from Sprint 10 validator surface)
**Status**: resolved

**Context**: validator's live sandbox verification against `canada-roks`
caught a coupled defect that the unit tests (Issue 1 closure above)
could not detect — the wrap shape pinned by `TestIBMCloudLoginWrap_*`
encoded a flag (`--trusted-profile-id`) that simply does not exist on
`ibmcloud 2.43.0` (the version baked into the tools-ibmcloud image),
and the pod had no projected SA-token volume mounted, so even with
the correct flag there was no JWT for IBM IAM to validate against the
ROKS_SA link `internal/ibm/trusted_profile.go::ensureLink` provisions.
Headline `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returned
`FAILED / No API endpoint set` instead of a Bearer token.

**Closure**: three coordinated edits.

1. **`internal/exec/k8s_install.yaml`** — added a pod-level
   `volumes:` block with a projected `serviceAccountToken` source
   (path `token`, audience `iam`, expirationSeconds 3600) and a
   matching container-level `volumeMounts:` entry at
   `/var/run/secrets/tokens` (read-only). Updated the existing block
   comment at lines 153-162 (the `${IAM_PROFILE_ID_ENV_ENTRY}`
   placeholder header) to reference the new `--cr-token @<path>` +
   `--profile` shape instead of the removed `--trusted-profile-id`.
2. **`internal/exec/k8s.go::ibmcloudLoginWrapScript`** — replaced
   `ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID" -r … --quiet`
   with `ibmcloud login -a https://cloud.ibm.com --cr-token
   @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID" -r
   "${IBMCLOUD_REGION:-us-south}" --quiet`. Retained the 3-attempt ×
   20s backoff loop and the final-attempt stderr surfacing on
   triple-fail. Added `-a https://cloud.ibm.com` to the trusted-profile
   branch (the cold ops pod has no persisted `ibmcloud api` setting;
   the static-key branch already set it). Updated the `runOnOpsPod`
   inline doc block (lines 248-264) to describe the new flag pair and
   the projected-token-volume dependency.
3. **`internal/exec/k8s_test.go`** — updated
   `TestIBMCloudLoginWrap_BranchesOnIAMProfileID` to assert presence
   of `--cr-token @/var/run/secrets/tokens/token`,
   `--profile "$IAM_PROFILE_ID"`, and `-a https://cloud.ibm.com`,
   plus a regression guard asserting `--trusted-profile-id` does
   NOT reappear. Updated
   `TestIBMCloudLoginWrap_TrustedProfileOmitsAPIKey` to assert the
   `--cr-token` + `--profile` pair in the trusted-profile branch (the
   `--apikey`-absence guarantee — the security-relevant assertion —
   stays as-is).

**Audience choice**: `iam`. The ROKS_SA link's audience is not
configured client-side in `internal/ibm/trusted_profile.go::ensureLink`
(`iamidentityv1.NewCreateProfileLinkRequestLink(crn, namespace)`
encodes only the cluster CRN and the SA namespace; `SetName` adds the
SA name; `CreateLinkWithContext` with `CrType: ROKS_SA` finishes the
provision). IBM IAM's documented standard audience for compute-resource
token exchange is `iam`. No alternative audience value is visible in
the codebase, and the documented IBM Cloud OIDC issuer flow accepts
`iam` as the JWT audience for ROKS_SA links. **Validator should
confirm against the live sandbox** — if IAM rejects the token with
an audience-mismatch shape, the audience field on the projected volume
is the right place to retry (`openshift` and the cluster's OIDC
issuer URL are the obvious alternates).

**Build/test verdict**:

| Check | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `gofmt -l .` | empty |
| `go test -count=1 ./internal/exec/... ./internal/cli/...` | green |
| `staticcheck ./...` | clean |
| `go test -count=1 ./...` | green (one unrelated flake in `internal/test/dns_test.go::TestProbe_TruncatedFlag` — port-in-use race; re-ran in isolation and passed) |

**Action for validator**: re-run the live sandbox headline
(`ops install --trusted-profile=auto` → `--backend k8s ibmcloud iam
oauth-tokens`) and verify the Bearer token appears. Unit tests pin the
wrap shape but cannot exercise the live IAM acceptance — the audience
value (`iam`) is staff's best inference from the IBM Cloud docs and
the codebase; validator's live run is the ground truth.

**Out-of-scope cross-refs** (architect surface, will be flagged in
validator re-verify report): `book/src/19-in-cluster-ops-pod.md:195`
and `docs/PLAN.md:766+794` still reference `--trusted-profile-id` —
architect's surface to update once validator's re-verify is green.

---

## Issue 10: tech-writer pass-2 Issue 17 — stale `--trusted-profile-id` doc-comment in `internal/cli/ops_test.go:131` — closed

**Severity**: low
**Status**: resolved

**Context**: tech-writer's pass-2 review flagged that the doc-comment
above `TestDecodeOpsManifests_TrustedProfile_InjectsIAMProfileID` (at
`internal/cli/ops_test.go:131`) still described the in-pod wrap as
branching to `--trusted-profile-id`. That flag was retired during
Sprint 10's wrap-script blocker fix (Issue 9 above) and replaced with
the `--cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID"`
shape backed by the projected SA-token volume. The stale comment was a
documentation hazard — a future reader could re-introduce the
non-existent flag chasing the comment's claim.

**Closure**: rewrote the comment block (lines 128-135) to describe the
current wrap shape. The new wording references the `--cr-token` +
`--profile` pair verbatim and notes Sprint 10's wrap-script blocker
fix as the historical pivot point.

**Verification**:

| Check | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./internal/cli/...` | clean |
| `gofmt -l internal/cli/ops_test.go` | empty |
| `go test ./internal/cli/...` | green |
| `grep -rn "trusted-profile-id" internal/cli/` | one descriptive mention inside the rewritten comment (historical context: "replaced Sprint 9's non-existent trusted-profile flag"); zero live references |

**Note on the wider `grep -rn "trusted-profile-id" internal/`**: hits
remain in `internal/exec/k8s.go:77` (historical comment in
`runOnOpsPod`'s doc block, intentional) and `internal/exec/k8s_test.go`
lines 615, 635, 637, 638, 639, 681, 689, 690 — every one of these is a
**deliberate regression-guard assertion** that the flag MUST NOT
reappear in the wrap script (`TestIBMCloudLoginWrap_*` and
`Test*_TrustedProfileOmitsAPIKey`). Removing them would defeat their
purpose. The "should be empty after your fix" directive applied to the
`internal/cli/ops_test.go` file named in tech-writer Issue 17; that
file's only remaining mention is the historical-context phrase inside
the rewritten comment.

---

## Cumulative summary — staff Sprint 10 remediation passes

Three remediation passes total (initial + post-validator + post-tech-writer-pass-2).

| Issue | Severity | Status | Notes |
|---|---|---|---|
| 1 — Sprint 9 staff Issue 2 (in-pod wrap closure) | medium | resolved | Pass 1: manifest renderer + wrap conditional + retry loop landed. |
| 2 — PRD 06 `status` per-shape deployment lines | low | resolved | Pass 1: four-shape rendering + unit tests against Sprint 8 fixtures. |
| 3 — retry backoff fixed 20s × 3, no jitter | low | open (acceptable for v1.3.0) | Pass 3: confirmed acceptable post-validator-reverify (first-attempt success). |
| 4 — wrap stderr interleaving on triple-fail | low | open (acceptable for v1.3.0) | Pass 3: confirmed not exercised post-validator-reverify; v1.4.x polish. |
| 5 — Sprint 9 tech-writer Issues 4/7/8/9/13 (architect surface) | low | resolved | Pass 3: architect closed all five in their pass 1 (see architect Issues 4-8). |
| 6 — smoke verify status | low | resolved | Pass 1: clean across build / vet / gofmt / test / staticcheck. |
| 7 — `t.Context()` Go 1.24+ pin check | low | resolved | Pass 1: project pinned ≥ Go 1.25, no action. |
| 8 — tech-writer Issue 3 `statusCmd.Long` doc drift | medium | resolved | Pass 1 (tech-writer pass 1): per-phase bullets replaced v1.0.x bullet. |
| 9 — validator Issue 1 (`--cr-token` + projected SA-token volume) | blocker | resolved | Pass 2 (post-validator): wrap shape + manifest volume + tests updated; validator GREEN on re-verify. |
| 10 — tech-writer pass-2 Issue 17 (stale comment in `ops_test.go:131`) | low | resolved | Pass 3: doc-comment rewritten to current wrap shape. |

**Totals**: 10 issues; 8 resolved, 2 open-but-acceptable (Issues 3 and 4 — both confirmed-OK for v1.3.0 by validator's live re-verify; carry forward as v1.4.x polish). No blockers remaining on staff surface.
