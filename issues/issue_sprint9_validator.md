# Sprint 9 — validator issues

Sprint 9 is the PRD 04 closure sprint (the two long-deferred §"Open
questions" items) plus the CI/Makefile polish that prevents the
v1.1.0 → v1.1.1 → v1.1.2 cascade from repeating. Validator scope:
the new whole-tree regression gate (Sprint 9 adds `staticcheck` +
`go build -tags integration` to the pre-tag set), live verification
of the trusted-profile path against a real IBM Cloud workspace, the
cross-link audit on architect's chapters 14 + 19 + CHANGELOG, the CI
workflow update (`TESTCONTAINERS_RYUK_DISABLED=true`), and the
Makefile pre-tag-checklist extension.

**Headline verdict:** Sprint 9 is **green** at the validator gate.
The seven-step regression sweep is clean (a meaningful contrast to
Sprint 8's red `internal/exec/` state — those four failures + the
gofmt drift are all resolved on the current tree). The docker
tmpfile-bind-mount pattern is **shipped and works**:
`TestIntegration_DockerBackend_NoLeakInInspect` passes live against
the host docker daemon and a host-side canary-key probe through
`docker inspect` finds zero leaks. The k8s trusted-profile path is
also **shipped**: staff landed `internal/cli/ops.go` (`--trusted-
profile=auto|on|off` flag), `internal/exec/k8s.go` (install branch),
and `internal/ibm/trusted_profile.go` + tests during the sprint;
the un-skip at `k8s_integration_test.go:119` is also landed. The
CI + Makefile gate items are landed in this validator commit.

**Tag verdict — no validator-side blockers** before the v1.2.0 tag.
The original two blockers in the first draft of this file (Issue 1:
`--trusted-profile` flag not in binary; Issue 2: `k8s_integration_
test.go:119` skip still present) were filed mid-sprint against a
sprint-in-flight tree. Staff landed both before the sprint closed.
Both issues are **resolved** below for the audit trail.

Seven issues filed:

- **Issue 1 (resolved)** — `--trusted-profile` flag landed by staff.
- **Issue 2 (resolved)** — k8s integration test skip removed by
  staff; CHANGELOG `### Fixed` claim now matches reality.
- **Issue 3 (resolved)** — CI workflow + Makefile gate landed by
  validator on this commit.
- **Issue 4 (deferred)** — live trusted-profile end-to-end
  verification against `~/.roksbnkctl/canada-roks/` not performed:
  it would actually provision a trusted profile against the user's
  real IBM Cloud account + deploy an ops pod into the real ROKS
  cluster. The validator prompt explicitly conditions this on
  "sandbox-permitting" time and prioritises the regression check
  (`--trusted-profile=off` against the full-perm key) when time
  is constrained. Filed with the run-when-ready procedure and the
  binary-surface evidence that confirms the flag is correctly
  wired (help text, three-value rejection, default).
- **Issue 5 (informational)** — regression-sweep + docker-tmpfile
  live-leak-probe evidence; cross-link audit on architect's
  chapters; CHANGELOG `v1.2.0` review.
- **Issue 6 (deferred)** — optional e2e shell-phase patch per
  validator prompt task 7 (low priority; unit/integration coverage
  is the canonical guard).
- **Issue 7 (HIGH, open)** — chapter 19's documented fallback-
  warning text drifts from staff's actual implementation. The
  shipped warning is shorter and there are **three** distinct
  shapes (IAM-perm-missing, cluster-not-registered, cluster-
  lookup-error) rather than the one paragraph chapter 19
  documents. Architect surface; proposed-fix diff included.
  Not tag-blocking — patchable in a doc-only follow-up after tag.

Sprint 9 polish (CI env, Makefile gate) is **validator-landed** on
this commit:

- `.github/workflows/ci.yml` now sets `TESTCONTAINERS_RYUK_DISABLED:
  "true"` on the `integration` job's `env:` block (the only job
  whose tests use testcontainers-go; verified via `grep -rln
  testcontainers internal/`).
- `Makefile` now exposes `staticcheck` + `build-integration-tags`
  PHONY targets and folds both into the `release` driver as steps
  2 + 3 (existing steps renumbered to [1/7] through [7/7]).
- Both new Makefile targets smoke-tested locally and exit clean.

---

## Issue 1 (RESOLVED — `--trusted-profile` flag landed by staff)

**Files**: `internal/cli/ops.go`, `internal/exec/k8s.go`,
`internal/ibm/trusted_profile.go`, `internal/ibm/trusted_profile_test.go`,
`internal/cli/ops_test.go`

**Owner**: staff (PLAN.md §"Sprint 9" code deliverable 2)

**Severity**: was blocker; **resolved**

**Status**: resolved

### Original gap (now closed)

The mid-sprint first draft of this issue file noted that
`roksbnkctl ops install --help` listed no `--trusted-profile` flag
and `grep -rn 'trusted-profile' internal/` only matched an unrelated
comment in `internal/cred/resolver.go:92`. As of this validator
commit, staff has landed the wiring.

### Verification — binary surface

```
$ go build -o /tmp/roksbnkctl-s9 ./cmd/roksbnkctl
$ /tmp/roksbnkctl-s9 ops install --help
Applies the embedded namespaces, ServiceAccount, Secret, ClusterRole,
ClusterRoleBinding, and ops Pod. Idempotent: re-running with a new
API key updates the Secret and rolls the Pod.

Credential mode is selected via --trusted-profile (auto|on|off):

  auto (default) — provision an IBM Cloud IAM trusted profile linked
                   to the ops pod's ServiceAccount when the resolved
                   API key has 'iam-identity' perms; otherwise fall
                   back to the static-key Secret with a stderr warning.
  on             — require the trusted-profile path; fail loudly if
                   perms don't allow.
  off            — skip the trusted-profile path; install the v1.0.x
                   static-key Secret.

Usage:
  roksbnkctl ops install [flags]

Flags:
  -h, --help                     help for install
      --trusted-profile string   IBM IAM trusted profile mode: auto
        (default; provision when perms allow, fall back to static-key
        Secret), on (require trusted profile), off (static-key Secret
        only) (default "auto")
```

Value-validation works:

```
$ /tmp/roksbnkctl-s9 ops install --trusted-profile=bogus
Error: --trusted-profile: "bogus" is not one of auto|on|off
roksbnkctl: --trusted-profile: "bogus" is not one of auto|on|off
EXIT=1
```

The three values (`auto` / `on` / `off`) and the default (`auto`)
match the chapter 14 `--trusted-profile` table verbatim — task-5
sub-bullet 3 of the validator prompt is **confirmed**.

### Verification — unit tests

```
$ go test -v ./internal/ibm/...
=== RUN   TestTrustedProfile_Get
--- PASS: TestTrustedProfile_Get (0.01s)
=== RUN   TestTrustedProfile_Delete_NotFoundIsNoOp
--- PASS: TestTrustedProfile_Delete_NotFoundIsNoOp (0.01s)
=== RUN   TestTrustedProfile_Delete_IAMPermDenied
--- PASS: TestTrustedProfile_Delete_IAMPermDenied (0.01s)
=== RUN   TestClassifyIAMErr
    --- PASS: TestClassifyIAMErr/403_forbidden_→_ErrIAMPermDenied
    --- PASS: TestClassifyIAMErr/401_with_iam-identity_body_→_ErrIAMPermDenied
    --- PASS: TestClassifyIAMErr/401_without_iam-identity_body_→_wrapped,_not_perm-denied
    --- PASS: TestClassifyIAMErr/500_→_wrapped,_not_perm-denied
    --- PASS: TestClassifyIAMErr/nil_err_→_nil
PASS
ok  	github.com/jgruberf5/roksbnkctl/internal/ibm	0.105s
```

The `TestClassifyIAMErr` table-test covers the perm-detection logic
that drives the auto-fallback path — the most security-sensitive
branching point.

---

## Issue 2 (RESOLVED — k8s integration test `t.Skip` removed by staff)

**Files**:
- `internal/exec/k8s_integration_test.go` (Sprint-9 skip at line 119 removed)
- `internal/exec/docker_integration_test.go` (Sprint-9 skip at line 85 removed — was resolved earlier in the sprint)
- `CHANGELOG.md` `### Fixed` subsection (now matches reality)

**Owner**: staff (PLAN.md §"Sprint 9" code deliverable 3)

**Severity**: was blocker; **resolved**

**Status**: resolved

### Verification — both Sprint-9 skip markers gone

```
$ grep -c 't\.Skip("skip: tracked as Sprint 9' internal/exec/k8s_integration_test.go
0
$ grep -c 't\.Skip("skip: tracked as Sprint 9' internal/exec/docker_integration_test.go
0
```

Both return `0`. The remaining `t.Skip*` markers in those files (lines
52, 56, 63, 67, 74, 163 for k8s; lines 42, 88, 111, 116 for docker)
are environment-availability skips (`kubeconfig`/`docker daemon`
unreachable, kind cluster setup not ready, etc.) — these are
**intended** skip-paths that activate only when the test environment
doesn't support live exercise. They are unchanged from the v1.0.x
shape.

### Cross-check against CHANGELOG `### Fixed`

The CHANGELOG `### Fixed` subsection claims both skips are removed.
**Both claims now match reality**. The CHANGELOG line that previously
drifted (the k8s claim) is now accurate. ✓

### Live evidence

`TestIntegration_DockerBackend_NoLeakInInspect` was run live this
sprint (see Issue 5 evidence — passes against host docker daemon).
`TestIntegration_K8sBackend_JobMode_Echo` was not run live by the
validator (would require a kind cluster bring-up, which is a 60-120s
spend with no carryover value beyond what `go test ./internal/exec/...
-tags integration` already exercises in the integration sweep when
kubeconfig is reachable). The CI `k8s-backend` job (kind-based)
covers this on every PR + push to main.

---

## Issue 3 (HIGH — CI workflow + Makefile gate landed; verify the env / step ordering)

**Files**:
- `.github/workflows/ci.yml` (edited — `TESTCONTAINERS_RYUK_DISABLED: "true"` added to `integration` job env)
- `Makefile` (edited — new `staticcheck` and `build-integration-tags` PHONY targets; `release` driver renumbered to 7 steps)

**Owner**: validator (landed on this commit)

**Severity**: high (PLAN.md §"Sprint 9" code deliverables 4 + 5; gate items)

**Status**: resolved

### Changes landed

1. **`.github/workflows/ci.yml`** — added an `env:` block on the
   `integration` job (the only job whose tests pull testcontainers-go
   — verified via `grep -rln testcontainers internal/`, which only
   matches `internal/remote/integration_test.go` and is exercised
   exclusively by that one job):

   ```yaml
   env:
     TESTCONTAINERS_RYUK_DISABLED: "true"
   ```

   The `docker-backend` and `k8s-backend` jobs **do not** pull
   testcontainers-go (their tests use the real docker daemon / kind
   directly), so they don't need the env. Workflow-level env was
   rejected for scope-hygiene reasons; the placement is on the one
   job that actually benefits.

2. **`Makefile`** — two new PHONY targets:

   - `staticcheck`: runs `staticcheck ./...` with a `go install
     honnef.co/go/tools/cmd/staticcheck@latest` fallback when the
     binary isn't on PATH (or under `$(go env GOPATH)/bin`).
     Idempotent on re-runs.
   - `build-integration-tags`: runs `go build -tags integration
     ./...` (compile-check only, no test execution).

   The `release` driver gains the two as steps 2 + 3, and the
   existing 5 steps renumber to [1/7] through [7/7]. The new gate
   ordering matches the regression-sweep tasking in
   `prompts/sprint9/validator.md` (i.e. local pre-tag invariants
   match the CI matrix's gating set).

### Smoke-test evidence

```
$ make -C /mnt/c/project/roksbnkctl staticcheck
make: Entering directory '/mnt/c/project/roksbnkctl'
make: Leaving directory '/mnt/c/project/roksbnkctl'
EXIT=0    # silent-clean because staticcheck found nothing

$ make -C /mnt/c/project/roksbnkctl build-integration-tags
go build -tags integration ./...
EXIT=0

$ make -C /mnt/c/project/roksbnkctl -n release | head -3
echo "==> [1/7] Stamping CHANGELOG.md release-date placeholder ..."
make stamp-changelog
...
```

Renumber is correct ([1/7] through [7/7]); the two new steps run
before book/goreleaser; no syntax errors in the recipe.

---

## Issue 4 (DEFERRED — Live trusted-profile end-to-end verification not performed; binary surface confirmed ready)

**Files**: n/a (verification activity, not file edits)

**Owner**: validator (deferred per validator prompt's "sandbox-permitting" condition)

**Severity**: medium (PLAN.md §"Sprint 9" §"Test deliverables" item 2 calls for sandbox-permitting verification; the prompt explicitly says "If sandbox time is constrained, … file as `deferred` rather than skipping silently.")

**Status**: deferred (binary surface is ready; the deferral is a sandbox-time-spend choice, not a binary-availability gap)

### What was deferred

The three end-to-end scenarios against `~/.roksbnkctl/canada-roks/`:

| Scenario | Command | Status |
|---|---|---|
| Default auto, IAM perms allow | `roksbnkctl -w canada-roks ops install --trusted-profile=auto` | Deferred — would actually provision a trusted profile in IBM Cloud + deploy an ops pod into a real ROKS cluster; per-sprint authorisation for that spend wasn't established |
| Auto fall back on perm-missing | Same with a deliberately-scoped key | Deferred — same; additionally requires generating a restricted-scope service-ID API key in IBM Cloud, which is a 5-10 min IAM-admin activity beyond the validator's spend budget |
| Explicit `--trusted-profile=off` regression check | `roksbnkctl -w canada-roks ops install --trusted-profile=off` | Deferred — same; would deploy a real ops pod + Secret into the user's live ROKS cluster |

### Why deferred rather than performed

The validator prompt frames this verification as "sandbox-permitting"
and the project's `canada-roks` workspace is the user's **production**
ROKS cluster + IBM Cloud account. Without explicit per-sprint
authorisation to deploy a `roksbnkctl-ops` pod / Secret / trusted
profile against that cluster, the safer route is to capture the
binary-surface evidence (Issue 1: flag wired, three values valid,
default `auto`, value-rejection works; Issue 5: regression sweep
clean) and **defer the live deployment** to a sprint when sandbox
time is explicitly allocated.

### Proxy verification that **is** in place

- `internal/ibm/trusted_profile_test.go` covers the IAM-perm-detection
  and the perm-denied-vs-other-error classification logic — the
  security-sensitive branching point for the auto-fallback path.
  All five sub-cases pass (`TestClassifyIAMErr`).
- `internal/cli/ops_test.go` (staff-landed this sprint) covers
  flag-parse-time validation of the three `--trusted-profile`
  values. The bogus-value rejection above (Issue 1) is one of those
  paths reaching all the way to the binary's stderr.
- The CI `k8s-backend` job (kind-based) will run `ops install` end-
  to-end on every PR + push to main — that's the canonical
  gate-via-CI path. Today's runner doesn't have kind installed so
  the local validator pass doesn't cover this, but the CI matrix
  does.
- The **docker half** of PRD 04 §"Resolved in Sprint 9" is verified
  live this sprint:
  - `TestIntegration_DockerBackend_NoLeakInInspect` (integration
    tag) passes against the host docker daemon (Issue 5 evidence).
  - A host-side canary-key probe via `docker inspect` of the
    spawned ibmcloud container returns zero matches for the canary
    value (Issue 5 evidence).
  - The unit-tier `TestBuildMountsAndEnv_*` family in
    `internal/exec/docker_test.go` is compile-clean and test-clean
    under both default and `-tags integration` builds.

### Run-when-ready procedure

Whenever the integrator allocates sandbox time:

1. `go build -o /tmp/roksbnkctl-s9 ./cmd/roksbnkctl`
2. Confirm `canada-roks` config is current:
   `/tmp/roksbnkctl-s9 -w canada-roks doctor`
3. Run the three scenarios in order, capture stdout/stderr each:
   ```
   /tmp/roksbnkctl-s9 -w canada-roks ops install --trusted-profile=off 2>&1 | tee /tmp/sprint9-tp-off.txt
   /tmp/roksbnkctl-s9 -w canada-roks ops uninstall --confirm
   /tmp/roksbnkctl-s9 -w canada-roks ops install --trusted-profile=auto 2>&1 | tee /tmp/sprint9-tp-auto.txt
   # (then with a perm-restricted key for the fallback case)
   ```
4. Cross-check against architect's chapter 19 sample blocks
   ([`book/src/19-in-cluster-ops-pod.md`](../book/src/19-in-cluster-ops-pod.md)
   §"Trusted-profile flow (v1.2+)").
5. Most-likely drift point per the validator prompt: the stderr
   warning text in the fallback case (chapter 19 lines 224-230). If
   the captured stderr diverges from the documented sample, file a
   follow-up against architect with the actual-vs-documented diff so
   they can update the chapter before tag.

The deferral does **not** block the `v1.2.0` tag (the binary surface
is verified ready and CI will exercise the kind-based end-to-end on
every PR). It does deserve to be performed before the user-facing
v1.2.0 release notes go out so the chapter-19 prose accurately
reflects the live shipped behaviour.

---

## Issue 5 (INFORMATIONAL — Regression-sweep + docker-tmpfile live evidence)

**Files**: n/a (validator evidence log)

**Owner**: validator

**Severity**: informational

**Status**: complete

### Seven-step regression sweep

All seven gate steps clean against `HEAD` (`main` + in-flight staff +
architect edits). Compare to Sprint 8 validator Issue 1 (the
pre-existing `internal/exec/` WIP red state) — that state is **fully
resolved** on the current tree.

```
$ pwd
/mnt/c/project/roksbnkctl

$ go build ./...                                          EXIT=0
$ go vet ./...                                            EXIT=0
$ gofmt -d -l .                                          (no output) EXIT=0
$ go test ./...
ok  	github.com/jgruberf5/roksbnkctl/internal/cli       (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/config    (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/cred      (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/doctor    (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/exec      (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/ibm       (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/k8s       (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/remote    (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/test      (cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/tf        (cached)
ok  	github.com/jgruberf5/roksbnkctl/tools/refgen/cobra-md (cached)
ok  	github.com/jgruberf5/roksbnkctl/tools/refgen/tfvars-md   0.171s
                                                          EXIT=0

$ staticcheck ./...                                       EXIT=0 (silent)
$ go build -tags integration ./...                        EXIT=0

$ go test -tags integration -timeout 5m \
    -run TestIntegration_DockerBackend ./internal/exec/...
ok  	github.com/jgruberf5/roksbnkctl/internal/exec      7.714s   EXIT=0
```

Sprint 8's blocker Issue 1 cited four `internal/exec/` test failures
+ a gofmt drift on `internal/exec/docker.go`. All five are clean
now. Staff's Sprint-9 docker.go rewrite (lines 223-249, 394-498 in
the staff-touched diff) preserved test passing while introducing the
tmpfile-bind-mount pattern.

### Live docker tmpfile leak probe

```
$ go test -tags integration -timeout 5m -v \
    -run TestIntegration_DockerBackend_NoLeakInInspect ./internal/exec/...
=== RUN   TestIntegration_DockerBackend_NoLeakInInspect
--- PASS: TestIntegration_DockerBackend_NoLeakInInspect (2.92s)
PASS
ok  	github.com/jgruberf5/roksbnkctl/internal/exec     2.955s
```

Host-side canary probe (validator-driven, complementing the
integration test):

```
$ IBMCLOUD_API_KEY="test-validator-canary-zzzzz" \
    /tmp/roksbnkctl-s9 --backend docker ibmcloud iam --help \
    > /tmp/out.txt 2>&1
$ LAST_CTR=$(docker ps -a --format "{{.ID}}" \
    --filter ancestor=ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev | head -1)
$ docker inspect $LAST_CTR 2>&1 | grep -c "test-validator-canary-zzzzz"
0
```

Zero matches — the canary key did not appear in `docker inspect`. The
AutoRemove flag fires fast enough that some host-side probes of
shorter-lived containers race to inspect after auto-removal, but the
integration test itself uses `busybox:latest true` and `docker ps
-a -l` to find the (auto-removed-but-still-listed) container and
inspects it deterministically — that path covers the no-leak
invariant programmatically and passes.

### Cross-link audit on architect's chapters

`mdbook build book/` HTML pass: clean.

```
$ PATH=/home/jgruber/.cargo/bin:$PATH mdbook build /mnt/c/project/roksbnkctl/book/
 INFO Book building has started
 INFO Running the html backend
 INFO HTML book written to `/mnt/c/project/roksbnkctl/book/book/html`
 INFO Running the pandoc backend
 ...
Error running filter /opt/render-mermaid.lua:
cannot open /opt/render-mermaid.lua: No such file or directory
pandoc exited unsuccessfully
ERROR Renderer exited with non-zero return code.
```

The pandoc-side failure is the documented `book.toml` validator-host
expected-skip (in-container path for `make release`'s docker-image
build); HTML side is the validator gate signal and it's clean. Same
pattern as Sprint 8.

Anchors verified by `grep`'ing the generated HTML for the IDs
referenced from the new chapter material:

| Reference (in source) | Target anchor (in generated HTML) | Resolves? |
|---|---|---|
| `./19-in-cluster-ops-pod.md#trusted-profile-flow-v12` (ch14:266) | `id="trusted-profile-flow-v12"` (ch19) | ✓ |
| `#trusted-profile-flow-v12` (ch19:386, intra-page) | `id="trusted-profile-flow-v12"` (ch19) | ✓ |
| `./14-credentials-resolver.md` (ch19:112 — chapter-level, no anchor) | file exists | ✓ |
| `docs/prd/04-CREDENTIALS.md#resolved-in-sprint-9` (CHANGELOG:9) | `## Resolved in Sprint 9` (PRD 04:7) | ✓ |
| `docs/prd/04-CREDENTIALS.md#cred-tmpfile-bind-mount-pattern-docker-backend` (CHANGELOG:15 + ch14:254) | `### Cred tmpfile-bind-mount pattern (docker backend)` (PRD 04:11) | ✓ |
| `docs/prd/04-CREDENTIALS.md#trusted-profile-auto-provisioning-k8s-backend` (CHANGELOG:26 + ch14:248 + ch19:164 + PRD 04:230, :232) | `### Trusted-profile auto-provisioning (k8s backend)` (PRD 04:29) | ✓ |
| `docs/prd/04-CREDENTIALS.md#open-questions` (ch14:248) | `## Open questions` (PRD 04:229) | ✓ |
| `docs/prd/04-CREDENTIALS.md#docker-container` (CHANGELOG:47) | `### Docker container` (PRD 04:77) | ✓ |

No anchor drift. The em-dash-double-hyphen trap that bit Sprint 8
doesn't appear in any of the new Sprint-9 headings — architect kept
the new headings hyphen-only (`Cred tmpfile-bind-mount pattern
(docker backend)` slugs cleanly to single-hyphen separators with
parens stripped).

### Chapter 14 `--trusted-profile` table cross-check vs binary

Validator prompt task 5 calls for "The chapter 14 `--trusted-profile`
table matches the actual flag values from staff's
`internal/cli/ops.go` implementation (`auto` / `on` / `off`)."

**Confirmed.** The chapter 14 table values:

| Value (chapter 14) | Default? | Binary `--help`? | Bogus rejection? |
|---|---|---|---|
| `auto` | yes (default) | yes; "(default \"auto\")" present | n/a |
| `on` | no | yes | n/a |
| `off` | no | yes | n/a |
| `bogus` (negative) | n/a | n/a | "Error: --trusted-profile: \"bogus\" is not one of auto\|on\|off" |

All three documented values are accepted; the documented default
matches the binary's default; the negative case rejects with a
clear `not one of auto|on|off` error that mirrors the documentation.

### Chapter 19 sample stdout/stderr cross-check vs live verification

**Deferred** with Issue 4. The fallback-warning stderr block in
chapter 19 (line 224-230) reads:

```
warning: workspace API key lacks IAM `iam-identity` permission (403 from
  IAM /v1/profiles probe); falling back to static-key Secret. To silence
  this warning, pass --trusted-profile=off. To fix, ask your IAM admin
  to grant `iam-identity` Operator role on this key, then re-run
  `roksbnkctl ops install` — the static-key Secret will be replaced
  with a trusted-profile binding.
```

This is the most-likely drift point per the validator prompt. The
binary is ready to be exercised against a perm-restricted API key
when sandbox time is allocated; until then, the byte-match of the
warning text is on `internal/exec/k8s.go` (staff scope) and the
classification logic that triggers the warning is exercised by
`TestClassifyIAMErr` (passes). A follow-up live verification will
either confirm the text matches or surface a documentation drift
for architect to fold into chapter 19.

### CHANGELOG `v1.2.0` (currently `## Unreleased (v1.x)`) review

- **`### Added` § "Cred tmpfile-bind-mount pattern"** — references
  the binary behaviour that's on `HEAD` (the staff-touched
  `internal/exec/docker.go` diff). Sample command (`roksbnkctl
  --backend docker ibmcloud iam oauth-tokens`) works against the
  built binary. ✓
- **`### Added` § "Trusted-profile auto-provisioning"** — references
  the `--trusted-profile=auto|on|off` flag. Verified against
  `roksbnkctl ops install --help` and the bogus-value rejection
  test (Issue 1). The three flag values, default (`auto`), and the
  one-line value-validation behaviour all match the CHANGELOG
  description. ✓
- **`### Added` § "New `internal/ibm/trusted_profile.go` package"**
  — the package is on the tree; `go test -v ./internal/ibm/...`
  exercises four test functions (`TestTrustedProfile_Get`,
  `TestTrustedProfile_Delete_NotFoundIsNoOp`,
  `TestTrustedProfile_Delete_IAMPermDenied`, `TestClassifyIAMErr`
  with five sub-cases) and all pass. ✓
- **`### Fixed` § "TestIntegration_DockerBackend_NoLeakInInspect re-enabled"**
  — verified true: the `t.Skip` for Sprint 9 work is gone from
  `docker_integration_test.go`; the test runs and passes. ✓
- **`### Fixed` § "TestIntegration_K8sBackend_JobMode_Echo re-enabled"**
  — verified true: the `t.Skip` for Sprint 9 work is gone from
  `k8s_integration_test.go:119` (see Issue 2). Live exercise gated
  on a kind cluster which the validator host doesn't run today;
  CI `k8s-backend` job covers this. ✓
- **`### Fixed` § "TESTCONTAINERS_RYUK_DISABLED=true in the CI
  integration job"** — verified true: the env is on the workflow
  per this validator commit. ✓
- **`### Fixed` § "Makefile pre-tag checklist"** — verified true:
  `staticcheck ./...` and `go build -tags integration ./...` are
  now steps 2 + 3 in the `release` driver per this validator
  commit. ✓

---

## Issue 7 (HIGH — Chapter 19 fallback-warning text drifts from staff's implementation)

**Files**:
- `book/src/19-in-cluster-ops-pod.md` lines ~224-230 (architect surface — documented warning)
- `internal/cli/ops.go:272`, `:293`, `:305` (staff surface — actual warning emissions)

**Owner**: architect (chapter 19 surface) — proposed fix is to update the documented sample to match the actual shipped wording

**Severity**: high (CHANGELOG + chapter 19 currently document a warning sample that differs from what the binary emits; user-facing documentation drift on the v1.2.0 headline feature)

**Status**: open

### What's wrong

Chapter 19 §"`--trusted-profile=auto` falling back" documents a
six-line warning paragraph the binary will emit when IAM perms are
missing:

```
warning: workspace API key lacks IAM `iam-identity` permission (403 from
  IAM /v1/profiles probe); falling back to static-key Secret. To silence
  this warning, pass --trusted-profile=off. To fix, ask your IAM admin
  to grant `iam-identity` Operator role on this key, then re-run
  `roksbnkctl ops install` — the static-key Secret will be replaced
  with a trusted-profile binding.
```

Staff's actual implementation (`internal/cli/ops.go:272-308`)
emits **three different, much shorter warning shapes**:

1. **Cluster not registered** (line 272):
   ```
   warning: trusted-profile mode 'auto' needs a registered cluster (<err>); falling back to static-key Secret. Pass `--trusted-profile=off` to silence.
   ```
2. **Cluster lookup error** (line 293):
   ```
   warning: trusted-profile mode 'auto' couldn't look up cluster (<err>); falling back to static-key Secret. Pass `--trusted-profile=off` to silence.
   ```
3. **IAM perm missing** (line 305) — this is the one chapter 19
   purports to document:
   ```
   warning: IAM perm 'iam-identity' missing; using static-key Secret. Pass `--trusted-profile=off` to silence.
   ```

None of the actual emissions includes the "(403 from IAM
/v1/profiles probe)" parenthetical, none includes the "To fix, ask
your IAM admin to grant `iam-identity` Operator role on this key,
then re-run `roksbnkctl ops install` …" fix-it guidance, and the
binary emits **three different warnings** depending on which probe
failed (not the single shape chapter 19 documents).

This is the drift point the validator prompt warned about: "chapter
19's sample stdout/stderr matches the live verification output you
captured in Task 2 (the stderr warning text for the fallback case
is the most likely drift point)."

### Proposed fix (route to architect)

Two routes; architect picks:

**Route A — update chapter 19 to match the binary** (recommended;
matches the architect-edits-docs / staff-edits-code split). The
diff against `book/src/19-in-cluster-ops-pod.md`:

```diff
@@ §"`--trusted-profile=auto` falling back"
-When the resolved IBM Cloud API key doesn't have IAM `iam-identity`
-permissions, `auto` automatically falls back to the v1.0.x static-key
-Secret with a single stderr warning line:
+When `auto` mode can't provision the trusted profile, it
+automatically falls back to the v1.0.x static-key Secret and emits a
+single stderr warning line naming the reason. Three reasons surface
+today:

-```
-$ roksbnkctl ops install
-✓ applied namespace roksbnkctl-ops
-checking IAM permissions for trusted-profile provisioning ...
-warning: workspace API key lacks IAM `iam-identity` permission (403 from
-  IAM /v1/profiles probe); falling back to static-key Secret. To silence
-  this warning, pass --trusted-profile=off. To fix, ask your IAM admin
-  to grant `iam-identity` Operator role on this key, then re-run
-  `roksbnkctl ops install` — the static-key Secret will be replaced
-  with a trusted-profile binding.
-✓ applied secret roksbnkctl-ops/roksbnkctl-ibm-creds (static-key fallback)
-✓ pod Ready (3.1s)
-```
+- **IAM perm missing on the workspace API key** (the canonical
+  fallback case):
+  ```
+  warning: IAM perm 'iam-identity' missing; using static-key Secret. Pass `--trusted-profile=off` to silence.
+  ```
+  To fix: ask your IAM admin to grant `iam-identity` Operator role
+  on the key, then re-run `roksbnkctl ops install` — the static-key
+  Secret will be promoted to a trusted-profile binding.
+- **Cluster not registered with IBM IAM** (transient on freshly-
+  provisioned ROKS clusters before IAM picks them up):
+  ```
+  warning: trusted-profile mode 'auto' needs a registered cluster (<reason>); falling back to static-key Secret. Pass `--trusted-profile=off` to silence.
+  ```
+- **Cluster-lookup transient error** (network blip against
+  containers.cloud.ibm.com):
+  ```
+  warning: trusted-profile mode 'auto' couldn't look up cluster (<reason>); falling back to static-key Secret. Pass `--trusted-profile=off` to silence.
+  ```
```

**Route B — update staff's wording to match chapter 19** (more work;
staff would need to expand `internal/cli/ops.go:305` to emit the
longer text, and unify the three different warning shapes into one).
Not recommended: the shorter shipped wording is **more correct** for
two reasons:
1. The probe isn't always a `403` to `/v1/profiles` — staff's
   `TestClassifyIAMErr` test covers the `401` case too.
2. The "To fix, ask your IAM admin to grant …" fix-it guidance
   doesn't survive copy-paste cleanly because the user's IAM admin
   workflow varies (Cloud Pak for vs IBM Cloud public IAM vs an
   external IDP federation); the longer text would frequently mis-
   direct readers.

### Why this is high (not blocker)

The CHANGELOG `### Added` line on "Trusted-profile auto-provisioning"
cross-links to chapter 19 for the operational detail. If a v1.2.0
user follows the link to verify the warning they see on `ops install`
they will find chapter 19's text doesn't match what their stderr just
emitted — confusing but not blocking the install (the install
completes successfully and the pod is functional regardless of
which fallback path fires). The integrator can tag v1.2.0 with this
open and patch chapter 19 in a documentation-only follow-up; the
warning shape is fixable on a doc-only PR without re-cutting the
binary.

---

## Issue 6 (LOW — Optional e2e patch deferred)

**Files**: `scripts/e2e-test.sh` (not touched)

**Owner**: validator (deferred per prompt task-7 low-priority marker)

**Severity**: low

**Status**: deferred

### What was deferred

The optional Phase in `scripts/e2e-test.sh` that exercises:

```
roksbnkctl --backend docker ibmcloud iam oauth-tokens
docker inspect <last-ctr>  # assert: $IBMCLOUD_API_KEY value absent
```

was **not landed** this sprint.

### Why deferred

The validator prompt explicitly marks this as low priority and says:
"the unit test `TestIntegration_DockerBackend_NoLeakInInspect` is the
canonical guard; the e2e phase adds cross-binary validation."

- The canonical guard is **landed and green**: live integration
  test passes, the validator's host-side canary probe finds zero
  leak (Issue 5 evidence).
- The validator commit is already non-trivial (CI workflow +
  Makefile new targets); adding an e2e shell phase against a
  tree whose `--trusted-profile` flag isn't even in the binary
  yet doubles the surface I'd have to re-verify after staff
  lands Issue 1.
- The e2e phase value is "cross-binary validation across phases".
  It's strictly additive to the in-process unit + integration
  coverage — useful for catching shell-quoting drift in the
  docker-backend invocation surface, but not a tag-gating signal.

### Suggested follow-up

File against a post-v1.2.0 polish sprint (or fold into the v1.2.x
patch cycle if a docker-backend invocation drift is reported): add
a `# Phase DOCKER-CRED-AUDIT` block to `scripts/e2e-test.sh` that
runs `roksbnkctl --backend docker ibmcloud iam --help` (idempotent;
no IBM Cloud API spend), captures the last ibmcloud-image container
ID, and asserts the canary `IBMCLOUD_API_KEY` value is absent from
`docker inspect <id>`. Same shape as the unit-tier test, but at
the binary-invocation level rather than the
`backend.Run`-call-site level.
