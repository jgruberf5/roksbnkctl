You are the validator agent for Sprint 0 of the roksbnkctl project. Set up testing/validation infrastructure and write the smoke-test section of CONTRIBUTING.md.

Project location: `/mnt/d/project/roksbnkctl/`. Go module `github.com/jgruberf5/roksbnkctl`. The existing E2E test driver lives at `scripts/e2e-test.sh` (run during the e2e shake-out we recently completed against a live IBM Cloud ROKS cluster). Read `docs/PLAN.md` (Sprint 0 section + the per-sprint testing additions table) and `docs/E2E_TEST.md` for context on what's already in place.

## Coordinate with parallel agents

An architect agent is creating `book/` + adding `book` Makefile targets + a book link to README.md. A staff-engineer agent is refactoring `internal/cli/doctor.go` + `internal/doctor/` + creating `.github/workflows/ci.yml` + `scripts/pre-commit.sh` + Makefile (build/test/lint/pre-commit-install targets) + writing the "Running tests" + "Pre-commit hook" + "Code style" sections of CONTRIBUTING.md. **Do not touch their files**. Your CONTRIBUTING.md edits should be only the "Long-running smoke test" section, appended.

## Tasks

1. **Create `tools/docker/ibmcloud/Dockerfile`** — placeholder, not buildable yet. Comment header noting Sprint 3 implementation. Body:
   ```dockerfile
   # syntax=docker/dockerfile:1
   # roksbnkctl-tools-ibmcloud — bundled ibmcloud-cli + ks plugin + relevant
   # plugins. Used by the docker execution backend (see PRD 03,
   # docs/prd/03-EXECUTION-BACKENDS.md). Implemented in Sprint 3.
   FROM ubuntu:22.04
   RUN echo "TODO Sprint 3: install ibmcloud-cli + ks plugin from IBM apt repo" \
       && exit 1
   ```

2. **Create `tools/docker/iperf3/Dockerfile`** — placeholder but minimally functional (alpine + iperf3 from package manager). Body:
   ```dockerfile
   # syntax=docker/dockerfile:1
   # roksbnkctl-tools-iperf3 — alpine + iperf3 binary. Used by the k8s
   # execution backend's iperf3 server pod (see PRD 03). Skeleton image;
   # client/server invocation is wired up in Sprint 3.
   FROM alpine:3.19
   RUN apk add --no-cache iperf3
   ENTRYPOINT ["iperf3"]
   ```

3. **Create `tools/docker/Makefile`** with local-dev build targets:
   ```makefile
   .PHONY: build-ibmcloud build-iperf3 build-all clean

   IMG_PREFIX ?= ghcr.io/jgruberf5/roksbnkctl-tools
   TAG ?= dev

   build-ibmcloud:
       docker build -t $(IMG_PREFIX)-ibmcloud:$(TAG) ibmcloud/

   build-iperf3:
       docker build -t $(IMG_PREFIX)-iperf3:$(TAG) iperf3/

   build-all: build-ibmcloud build-iperf3

   clean:
       docker image rm -f $(IMG_PREFIX)-ibmcloud:$(TAG) $(IMG_PREFIX)-iperf3:$(TAG) || true
   ```

4. **Create `.github/workflows/spellcheck.yml`** — runs cspell on `book/src/**/*.md` and on Go source comments. **Warning-level only** (continue-on-error, don't gate PRs):
   ```yaml
   name: Spellcheck
   on:
     pull_request:
       paths:
         - 'book/src/**/*.md'
         - 'docs/**/*.md'
         - '**/*.go'
   jobs:
     spell:
       runs-on: ubuntu-latest
       continue-on-error: true
       steps:
         - uses: actions/checkout@v4
         - uses: streetsidesoftware/cspell-action@v6
           with:
             config: cspell.json
             files: |
               book/src/**/*.md
               docs/**/*.md
   ```

5. **Create `cspell.json`** at the repo root with project-specific terms — at minimum: roksbnkctl, kubeconfig, kubectl, terraform, terraform-exec, IBMCLOUD, ROKS, ibmcloud, OpenShift, BNK, BIG-IP, F5, GSLB, FLO, CIS, FAR, JWT, COS, SSC, sshd, miekg, openshift, jumphost, mdbook, mdBook, peaceiris, dominikh, staticcheck, goreleaser, kustomize, anycast, ingress, jgruberf5, helm, oc, AcceptEnv, SetEnv, NOPASSWD, NOOP, sudoers, scp, nmcli, kind. Format as a JSON file with `"version": "0.2"`, `"language": "en"`, `"words": [ ... ]`, `"ignorePaths": [ "book/book/**", "go.sum", "*.tfstate", "*.lock.hcl" ]`.

6. **Read `scripts/e2e-test.sh`** — verify it's still in good shape post-rename. Things to check:
   - All `bnkctl` references replaced with `roksbnkctl` (we did this earlier; double-check)
   - `BNKCTL_HOME` → `ROKSBNKCTL_HOME` env reference
   - Comments still accurate
   - Any TODO comments worth surfacing as issues
   Note any issues in your issues file (severity low if cosmetic).

7. **Read `internal/*/*_test.go`** files. For each Go package under `internal/`:
   - Note whether it has tests
   - Estimate coverage gaps
   - Identify which packages would benefit most from `testcontainers-go` (for testing in Sprint 1+'s SSH client, Sprint 3's docker backend, etc.)
   Document this as a **roadmap entry** (NOT a bug) in your issues file under a heading like `## Future testing improvements (roadmap, not bugs)`. Include a small table: package | has tests | recommended addition.

8. **Update `CONTRIBUTING.md`** — append a section ONLY (do not edit anything the staff-engineer agent is writing):
   ```markdown
   ## Long-running smoke test

   The full end-to-end test (`scripts/e2e-test.sh`) provisions a real
   ROKS cluster + BNK deployment on IBM Cloud, exercises every roksbnkctl
   verb against it, and tears down. It's the canonical "did we break
   anything" check before tagging a release.

   ### Prerequisites
   - `IBMCLOUD_API_KEY` env var (or extracted from `~/bnkfun/terraform.tfvars`)
   - `~/bnkfun/terraform.tfvars` with cluster + region + RG values
   - terraform on PATH
   - kubectl, oc, ibmcloud, iperf3 on PATH (Phase 3 plans to remove these
     prereqs — see `docs/prd/03-EXECUTION-BACKENDS.md`)

   ### Running

   ```bash
   ./scripts/e2e-test.sh                       # full pass from scratch
   PHASE_FROM=D ./scripts/e2e-test.sh           # resume from phase D
   DRY_RUN=1 ./scripts/e2e-test.sh              # show plan without execution
   ```

   ### Cost & duration

   ~3-4 hours wall time. ~$5-10 of IBM Cloud spend per full pass (cluster +
   load balancers + COS). The test is **never** run in PR CI — release
   branch nightly only, until 3 consecutive nights green, then tag.
   ```

## Issue tracking

File issues / roadmap entries to `/mnt/d/project/roksbnkctl/issues/issue_sprint0_validator.md`:

```markdown
# Sprint 0 — validator issues

## Issue 1: short title
**Severity**: low | medium | high | blocker | roadmap
**Status**: open | resolved | informational
**Description**: ...
**Files affected**: ...
**Proposed fix**: ...
```

`Severity: roadmap` is reserved for "future improvements" entries (the testcontainers-go survey above) — these are not bugs to resolve in Sprint 0; they're notes for sprint planning. Put all the testing-improvement notes under one `## Issue: future testing improvements` entry.

If no actual bugs found, the file can have just the roadmap entry plus a `*No actionable bugs filed.*` note.

## Verification before reporting done

- `go build ./...` still succeeds (you didn't break anything)
- `go test ./...` still passes
- The Dockerfiles are syntactically valid (try `docker build --check tools/docker/iperf3/` if Docker is available locally; if not, just visual review)
- `bash -n scripts/e2e-test.sh` passes (syntax check)

## Final report

Return a concise summary (under 200 words):
- Files created
- Files edited (especially what specifically you appended to CONTRIBUTING.md)
- Whether `go test ./...` is still green
- Whether you filed any actionable bugs vs roadmap notes (counts of each)
- Anything the integrator should be aware of (e.g., conflicts with what other agents are writing)

Do NOT commit anything. The integrator will commit the aggregated work.
