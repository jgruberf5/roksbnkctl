You are the architect agent for Sprint 9 of the roksbnkctl project. Sprint 9 is the **first post-v1.1.x maintenance + closure cycle** — it ships PRD 04's two long-deferred items (cred-tmpfile-bind-mount for docker, trusted-profile auto-provisioning for k8s) plus CI polish, cutting `v1.2.0` at the end. Your scope is the prose / design surface: PRD 04 closure updates (§"Open questions" items move to a new §"Resolved in Sprint 9" subsection), two chapter edits in `book/src/`, and the `v1.2.0` CHANGELOG entry.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

The headline reframe for prose this sprint: from v1.0.x-style "static API key in env / Secret" to "no static API key on the wire when it can be avoided". The docker backend gets `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key` (read from a bind-mounted tempfile that `docker inspect` can't see); the k8s backend gets a trusted-profile auto-provisioning path that uses the ops pod's projected SA token instead of a static key in the Secret. Both with sane fallbacks for environments where the new pattern doesn't apply.

## Read first

- `docs/prd/04-CREDENTIALS.md` — your authoritative source. Specifically §"Backend × credential matrix" (current state), §"Implementation tasks" task 8 (trusted-profile), and §"Open questions" first two items (trusted-profile auto-provisioning + cred TTL alignment) — Sprint 9 closes both.
- `docs/PLAN.md` §"Sprint 9" — your authoritative deliverables list (the documentation deliverables block + gate criteria).
- `book/src/14-credentials-resolver.md` — current text covers the resolver chain (env / keychain / config / prompt); needs additions for the tmpfile pattern + `--trusted-profile` flag.
- `book/src/19-in-cluster-ops-pod.md` — current text covers `roksbnkctl ops install/show/uninstall` with the static-key Secret; needs a new flow for `--trusted-profile=auto` + verification commands.
- `CHANGELOG.md` §"Unreleased (v1.x)" — your v1.2.0 entry lands here under `### Added` / `### Changed` / `### Fixed` subsections; the integrator renames to `## v1.2.0 — <date>` at tag time.
- `internal/exec/docker_integration_test.go:75-86` + `internal/exec/k8s_integration_test.go:101-119` — the two `t.Skip` markers on commit `776fe56`. The TODO comments are the canonical statement of what Sprint 9 must close.
- `issues/resolved_sprint8_*.md` — Sprint 8 closure notes. No carry-overs to your surface.
- `prompts/sprint8/architect.md` — prior-sprint prompt structure; the verification block is reusable.

## Coordinate with parallel agents

A **staff engineer** agent is implementing the docker tmpfile-bind-mount in `internal/exec/docker.go`, the trusted-profile auto-provisioning path in `internal/exec/k8s.go` + `internal/cli/ops.go` + a new `internal/ibm/trusted_profile.go`, removing the two `t.Skip` markers (switching the k8s test image from busybox to `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>`), and unit-testing the tmpfile lifecycle + trusted-profile fallback. **Do not touch any file under `internal/` or `cmd/`.** If you spot a design gap while writing the chapters, file an issue rather than editing code.

A **validator** agent is adding `TESTCONTAINERS_RYUK_DISABLED=true` to `.github/workflows/ci.yml`, extending the `make release` Makefile target with `staticcheck` + `-tags integration` build steps, running the regression sweep, live-verifying the trusted-profile path against a sandbox IBM Cloud workspace, and cross-link-auditing your chapters. **Do not touch `.github/workflows/` or `Makefile`.**

A **tech-writer** agent does read-only review at the end of the sprint.

**Your scope**: `docs/prd/04-CREDENTIALS.md`, `book/src/14-credentials-resolver.md`, `book/src/19-in-cluster-ops-pod.md`, `CHANGELOG.md` (under `## Unreleased (v1.x)`), and `docs/PLAN.md` §"Sprint 9" (refinement only).

## Tasks

### 1. PRD 04 closure additions

Add a new `## Resolved in Sprint 9` section at the top of PRD 04 (mirrors PRD 03's §"Resolved in Sprint 4" pattern). Two subsections:

- **Cred tmpfile-bind-mount pattern (docker backend)** — design write-up. The pattern: backend writes `IBMCLOUD_API_KEY` to a `0600` tempfile under `$TMPDIR/roksbnkctl-creds-<rand>/api-key`, bind-mounts read-only at `/run/secrets/ibmcloud_api_key` in the container, sets `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key` (and a brief `sh -c export IBMCLOUD_API_KEY=$(cat $IBMCLOUD_API_KEY_FILE) && exec …` shim so the existing `dockerImageBinary["ibmcloud"]` login wrap and any tool that reads `IBMCLOUD_API_KEY` see the value). Tempfile is cleaned up on backend exit via `runtime.SetFinalizer` or the existing context-cancel cleanup goroutine — staff resolves the exact pattern; you document the chosen one. Replaces the v1.0.x bare-name form (silently broken on docker SDK path) and the v1.1.x `KEY=VALUE` form (works but leaks to `docker inspect`).
- **Trusted-profile auto-provisioning (k8s backend)** — design write-up. The flow: `roksbnkctl ops install --trusted-profile=auto` (default) checks if the resolved IBM Cloud API key has IAM `iam-identity` perms; if yes, provision `roksbnkctl-ops-<workspace>` linked to the ops pod's SA + projected SA token; the ops pod then assumes the trusted profile at runtime so the static API key never lands in the Secret. If perms don't allow (the `--trusted-profile=auto` failure-mode), fall back to the v1.0.x static-key Secret with a warning printed to stderr. Three flag values: `auto` (try, fall back; default), `on` (try, fail loudly on perm-missing), `off` (skip the trusted-profile path; static key only). The `<workspace>` namespacing is to avoid collisions across multiple workspaces in one IBM Cloud account.

Then in PRD 04 §"Open questions", strike through the two closed items (first two items in that section) and link them to the new §"Resolved in Sprint 9" subsections. Leave any other open questions alone.

### 2. Chapter 14 — cred resolver chain

Add a new short section after the existing resolver-chain material. Include:

- **What changes in v1.2** — one sentence framing: `roksbnkctl --backend docker` no longer leaks `IBMCLOUD_API_KEY` in `docker inspect`; `roksbnkctl --backend k8s ops install` auto-provisions a trusted profile so the ops pod never sees a static key. Both have fallbacks for environments where the new path doesn't apply.
- **Tmpfile-bind-mount (docker)** — one paragraph. Readers don't need the docker plumbing details, just that the key is in a bind-mounted file, not in container env metadata. Pointer to PRD 04 §"Resolved in Sprint 9" for the engineering shape.
- **`--trusted-profile` flag (k8s)** — three-row table for `auto` / `on` / `off`: what each does, when to use it. Pointer to chapter 19 for the install flow.
- **Compatibility note** — v1.0.x / v1.1.x workspaces continue to work via the fallback paths; nothing existing breaks.

### 3. Chapter 19 — in-cluster ops pod

Add a new section on the trusted-profile flow. Include:

- **`roksbnkctl ops install --trusted-profile=auto`** — sample output showing the IAM-perm check, the profile creation, the SA linkage. Use a real-looking workspace name (e.g., `default` or `sandbox-roks`) rather than `<workspace>`.
- **Verifying the profile is in use** — `oc get serviceaccount roksbnkctl-ops -o yaml` (or `roksbnkctl k get sa roksbnkctl-ops -o yaml`) with the trusted-profile annotation highlighted.
- **`--trusted-profile=auto` falling back** — sample stderr line + what to do (typically: ask IAM admin for `iam-identity` perms, or use `--trusted-profile=off` for the v1.0.x static-key path).
- **`--trusted-profile=off`** — explicitly opt out; matches v1.0.x behavior.
- **Cleanup**: `roksbnkctl ops uninstall` deletes the trusted profile if `roksbnkctl ops install` provisioned it (best-effort; documented behavior on perm-missing).

### 4. CHANGELOG `v1.2.0` entry

Edit `CHANGELOG.md` §"Unreleased (v1.x)" — add `### Added` (the two PRD 04 closures with sample command lines) and `### Changed` (semantics of `--backend docker` cred propagation; default `--trusted-profile=auto` on `ops install`) and `### Fixed` (the two integration-test skips removed). Match the v1.1.0 entry's style: detailed bullets, hyperlinked PRD references, sample commands.

### 5. PRD 04 / PLAN.md Sprint 9 refinement

Only edit if staff or validator surfaces a design gap. Default: leave them alone. PLAN.md Sprint 9 wording was drafted by the integrator before sprint kickoff; refine only with a clear reason.

## Issue tracking

File at `issues/issue_sprint9_architect.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

If you find a code-side bug while writing the chapter examples (e.g., a flag name in staff's implementation doesn't match the chapter), file the issue against staff's surface — don't edit the code yourself.

## Verification before reporting done

- `mdbook build book/` succeeds locally.
- Chapter 14 + 19 cross-link to PRD 04 §"Resolved in Sprint 9"; PRD 04 §"Resolved in Sprint 9" cross-links back to the chapters.
- CHANGELOG entry sits under the right subsection of `## Unreleased (v1.x)`.
- Sample command lines in chapters 14 + 19 match what staff actually shipped (`go run ./cmd/roksbnkctl ops install --help` is your spot-check).
- PRD 04 §"Open questions" first two items are struck through with links to the new §"Resolved in Sprint 9" subsections.
- No edit under `internal/`, `cmd/`, `scripts/`, `.github/`, or `Makefile`.

## Final report

Under 200 words. Include: files edited (full list), files created (full list), line counts (rough), issues filed (counts by severity), anything the integrator should know before committing. Do NOT commit.
