# Sprint 31 — validator issues (CI for the runner image + S3 state backend)

> Verify the all-in-one runner image is a real, working install target (and that
> mdbook/pandoc are NOT in it), and that the COS/S3 state backend round-trips.
> Specs: [PRD 15](../docs/prd/15-RUNNER-IMAGE.md), [PRD 16](../docs/prd/16-REMOTE-STATE-BACKEND.md).
> Validator owns the CI workflow files.

`Status`: **resolved — all validator issues done 2026-06-11** (PRD 15 runner smoke; PRD 16 S3 backend CI)

> **Issues 3–4 done (2026-06-11).** `.github/workflows/state-backend-it.yml`
> stands up MinIO + terraform 1.10.5 and runs the build-tagged
> `internal/tf/backend_integration_test.go` (`IT_S3`): the real render path +
> a round-trip (state lands at the per-phase key, no local tfstate, re-plan
> reads remote) and a local→s3 migration. The **local-unchanged golden** and
> **secret-hygiene** (no creds in the rendered HCL) guards are plain unit
> tests in the normal CI run. Lock is config-asserted (`use_lockfile` in the
> unit test) + terraform-native; a concurrency-contention test is deferred to
> avoid CI flakiness. YAML + tagged build validated (no docker in the
> authoring env).

> **PRD 15 CI done (2026-06-11).** New `.github/workflows/runner-smoke.yml`
> (path-filtered PR + dispatch): builds the runner image (load, no push) and
> asserts roksbnkctl runs, every bundled CLI responds, terraform ≥ 1.10,
> **mdbook/pandoc absent**, image size reported, and workspace state persists
> across two container runs on a named volume (`ws new` → `ws list`). YAML
> validated; the job itself runs in CI (no docker in the authoring env).

---

## Issue 1 — runner image build + smoke in CI

**Severity**: high
**Status**: open

- **.github/workflows/tools-images.yml**: confirm the `runner` matrix entry
  builds and publishes on tag/main (root build context, like `ibmcloud`).
- **Smoke job** (CI): `docker run` the freshly-built image and assert it works as
  an install target —
  `roksbnkctl version` exits 0, and each bundled CLI responds:
  `terraform version`, `oc version --client`, `kubectl version --client`,
  `helm version`, `ibmcloud --version`, `iperf3 -v`, `h2load --version`.
- **No-dev-tooling assertion**: assert mdbook/pandoc/texlive are **absent**
  (`! command -v mdbook && ! command -v pandoc`) and record the image size so the
  docs toolchain can't creep back in.

## Issue 2 — state-volume persistence

**Severity**: medium
**Status**: open

A test that runs roksbnkctl from the container with `-v <tmp>:/work
-e ROKSBNKCTL_HOME=/work/.roksbnkctl`, creates a workspace, and confirms the
state/config survive a second container run against the same volume (the
"container holds no state; the volume does" contract).

## Issue 3 — S3 backend round-trip

**Severity**: high
**Status**: open

- A backend round-trip against an S3-compatible endpoint (MinIO in CI, or a real
  COS bucket gated behind a secret): `state.backend: s3`, run a trivial
  plan/apply, assert the state object lands at the per-phase key, and a second
  invocation reads it (no local `terraform.tfstate`).
- **Lock test**: two concurrent applies against the same key — one must block/
  fail on the lock, not corrupt state.
- **Regression**: `state.backend: local` (or absent) produces byte-identical
  backend/tfvars output to pre-Sprint-31 (golden test).
- **Secret hygiene**: assert HMAC keys never appear in the rendered HCL, the
  state object, or CI logs.

## Issue 4 — migration + doc-coupling

**Severity**: low
**Status**: open

- Migration test: a local-state workspace flipped to s3 moves cleanly (no drift
  on the next plan), and refuses to clobber an occupied key.
- Doc-coupling: the install-chapter `docker run` examples reference the actual
  published image name/tags; the `state:` config-reference fields match the
  shipped schema.

### Acceptance criteria
1. CI builds + smoke-tests the runner image; mdbook/pandoc absence asserted.
2. State-volume persistence verified across container runs.
3. S3 round-trip + lock + local-unchanged regression + secret-hygiene all green.
4. Migration verified; install/config docs match the shipped surface.

### Files affected
- **Modified**: `.github/workflows/tools-images.yml` (+ a smoke/round-trip job or
  a new workflow), test fixtures under `internal/tf/` for the backend golden test.

### Related
- [PRD 15](../docs/prd/15-RUNNER-IMAGE.md) · [PRD 16](../docs/prd/16-REMOTE-STATE-BACKEND.md)
  · `issue_sprint31_staff.md` (the implementation under test).
