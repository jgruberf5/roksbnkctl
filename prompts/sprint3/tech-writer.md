You are the tech writer agent for Sprint 3 of the roksbnkctl project. Read-only review of all documentation produced this sprint, plus example correctness and PRD/PLAN drift.

Project location: `/mnt/d/project/roksbnkctl/`. Your scope is **review + issue filing only** — do not edit any files except `issues/issue_sprint3_tech-writer.md`.

## Context — what the other agents produced this sprint

- **Architect** replaced 5 chapter stubs with real prose under `book/src/`: 12 (Workspace config), 13 (Terraform variables), 14 (Credentials and the resolver chain), 15 (SSH targets), 17 intro (Execution backends — intro only; Sprint 4 expands).
- **Staff engineer** implemented PRD 04 (cred resolver + redactor + Credentials struct) and PRD 03 first half (Backend interface + local + docker backends), refactored `internal/cli/cluster.go` passthroughs through the new Backend, added `--backend` CLI flag at root, added the `exec:` block to workspace config, filled in `tools/docker/{ibmcloud,iperf3}/Dockerfile`.
- **Validator** added unit tests under `internal/cred/` + `internal/exec/`, integration tests for the docker backend, a cred-audit unit test, the new `.github/workflows/tools-images.yml` workflow (build + push tools images on tag), CI updates with a docker-backend integration job, e2e Phase B10 (docker backend prelim), and CONTRIBUTING.md additions for cred-audit tests + local image builds.

Their issue files are at `issues/issue_sprint3_<role>.md` with corresponding `resolved_sprint3_<role>.md`. Read them — your job is to find what they missed.

## Tasks

### 1. New chapter quality — chapters 12, 13, 14, 15, 17 intro

For each chapter:
- **Tone consistency** with each other and Sprint 1+2 chapters (clipped technical voice, lower-case prose, code-block-heavy)
- **Audience alignment**: chapter 12 is a reference, chapter 13 is operational, chapter 14 is security-aware, chapter 15 is technical companion to chapter 16, chapter 17 intro is a forward-look
- **Code examples runnable**: every `roksbnkctl ...` snippet should be a real command. Verify against `cmd/roksbnkctl --help` for the new `--backend` flag.
- **Cross-references resolve**: relative links work; PRD links use GitHub-canonical URLs (per Sprint 1 Issue 9 fix)
- **No unfilled placeholders**: zero "Coming in Sprint 3" should remain (chapter 17's `*Coming in Sprint 4.*` markers for the deep-dive sections ARE intentional — leave them)

### 2. Chapter 14 example correctness — the new cred resolver

Chapter 14 documents the resolver chain (env → keychain → config-b64 → prompt). Verify:
- The order matches the staff agent's actual implementation in `internal/cred/resolver.go`
- The "what's safe to commit" examples reflect what the binary actually writes (read `internal/config/secrets.go` for the existing `api_key_b64` write path; chapter must match)
- The redactor description matches `internal/exec/redact.go` — what gets masked, what doesn't

### 3. Chapter 12 example correctness — the new `exec:` block

The Sprint 3 addition to workspace config. Verify:
- The schema in chapter 12 matches `internal/config/workspace.go`'s `Workspace.Exec` field
- The `--backend` flag override semantics match what the staff agent implemented (flag wins over config; config wins over default)

### 4. Chapter 17 intro — backend coverage matrix accuracy

Chapter 17's intro should accurately describe what's available **today** (Sprint 3) vs **coming** (Sprint 4). Verify:
- "Available today" lists `local` + `docker` only
- "Coming in Sprint 4" lists `k8s` + `ssh`
- The forward-link to PRD 03 uses GitHub-canonical URL
- Doesn't promise functionality that hasn't shipped (e.g. don't claim apt-bootstrap works yet — that's Sprint 4's SSH backend territory)

### 5. PRD-to-chapter coverage check

PRD 04 specifies the design for cred handling. Chapter 14 is the user-facing version. Verify:
- Every user-visible cred handling decision in PRD 04 appears in chapter 14
- Anything PRD 04 lists as backend-specific (k8s Secret, SSH wrapper-script fallback) is correctly marked as Sprint 4 territory in chapter 14, not as currently-shipped
- The chapter doesn't claim functionality the staff agent didn't build

### 6. Docker backend example correctness

Chapter 17 intro mentions `--backend docker`. Verify the example commands work against the staff agent's actual implementation:
- `roksbnkctl ibmcloud --backend docker iam oauth-tokens` matches the wired flag
- The image name `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud` matches what `internal/exec/docker.go` resolves to
- The chapter doesn't promise anything about `iperf3 --backend docker` or `terraform --backend docker` (those are Sprint 4/5 territory)

### 7. Tool image Dockerfiles — correctness as documentation

Read `tools/docker/ibmcloud/Dockerfile` and `tools/docker/iperf3/Dockerfile`. They're not user-facing prose, but they're project documentation in a sense. Flag if:
- Comments are unclear or stale ("TODO Sprint 3" still present, etc.)
- The build steps are obviously wrong (would fail on `docker build`)
- Version pins are missing where they should be present

### 8. README + CONTRIBUTING updates

Sprint 3 should add:
- A `--backend docker` highlight bullet to README (analogous to Sprint 1's `--on jumphost` and Sprint 2's k-commands bullets) OR a deferral until Sprint 4 lands the full four-backend matrix. Either is defensible; flag whichever the team didn't do as a candidate addition.
- CONTRIBUTING.md "Running cred-audit tests" section (validator owns this)
- CONTRIBUTING.md "Building tool images locally" section (validator owns this)

If those updates are missing or look thin, file as **medium** severity (analogous to Sprint 1 Issue 10).

### 9. Cross-document drift check

Spot-check:
- `docs/PLAN.md` (does PLAN.md still accurately describe Sprint 3's outcomes?)
- `docs/prd/04-CREDENTIALS.md` and `docs/prd/03-EXECUTION-BACKENDS.md` (any details now obsolete?)
- `book/src/SUMMARY.md` (chapter titles match h1?)
- The Go version in chapter 4 + README — Sprint 3's `docker/docker/client` dep may have bumped go.mod again; if so, chapter 4 + README should follow

### 10. Test code readability

Read `internal/cred/*_test.go` and `internal/exec/*_test.go`. Flag if:
- A test name is unclear
- A test lacks a comment explaining the behaviour it pins down
- Magic constants without explanation

Don't be picky for stylistic preferences.

## Issue file format

`/mnt/d/project/roksbnkctl/issues/issue_sprint3_tech-writer.md`. Same format as Sprints 0/1/2. If genuinely clean, file with `*No issues filed.*`. Don't manufacture issues.

## Verification before reporting done

- All 5 chapter files contain real prose (no "Coming in Sprint 3"; chapter 17's intentional `*Coming in Sprint 4.*` markers remain)
- All cross-references in the new chapters resolve
- All `roksbnkctl ...` commands appear in the actual binary's help output

## Final report (under 200 words)

- Files reviewed (counts)
- Issues filed (counts by severity)
- Top 3 noteworthy observations not filed as issues
- Whether you spotted any drift between PRD 04 / PRD 03 / PLAN.md and delivered surface

Do NOT edit any files (except your issue file). Do NOT commit anything.
