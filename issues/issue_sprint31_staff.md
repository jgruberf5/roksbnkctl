# Sprint 31 — staff issues (build the runner image + the S3 state backend)

> Build the all-in-one runner image (binary + all dispatched CLIs, NO mdbook/
> pandoc) and the optional COS/S3 terraform state backend. Specs:
> [PRD 15](../docs/prd/15-RUNNER-IMAGE.md), [PRD 16](../docs/prd/16-REMOTE-STATE-BACKEND.md).
> Design decisions (BLOCKING): `issue_sprint31_architect.md`.

`Status`: in progress — **Issue 1 (PRD 15 runner image) implemented 2026-06-11**; Issues 2–3 (PRD 16 S3 backend) pending

> **Issue 1 done (2026-06-11).** `tools/docker/runner/Dockerfile` (multi-stage:
> the reused Go-build stage + ibmcloud/terraform 1.10.5/helm/kubectl/oc/iperf3/
> h2load on ubuntu:22.04, no mdbook/pandoc; uid 1000 + `/work` `ROKSBNKCTL_HOME`
> volume; `ENTRYPOINT ["roksbnkctl"]`); `tools/docker/Makefile` `build-runner` +
> `build-all`/`clean`; `runner` added to the `tools-images` CI matrix. Workflow
> YAML + Makefile validated; the image build + smoke is the validator's Issue 1
> (no docker available in the authoring env).

### Locked decisions (integrator; confirm before dispatch)
- The runner reuses the `tools-ibmcloud` Dockerfile's Go-build stage verbatim
  (same ldflags / `ROKSBNKCTL_VERSION` build-args) — do not fork the build.
- The S3 backend sits behind a `config.yaml` `state:` block; `backend: local`
  (absent block) renders byte-identically to today (no regression).
- COS credentials are HMAC keys resolved via the existing `cred.Resolver`
  `*_source` pattern — never written to tfvars or committed.

---

## Issue 1 — `tools/docker/runner` image [PRD 15]

- **tools/docker/runner/Dockerfile**: multi-stage. Stage 1 = the
  `roksbnkctl-build` stage copied from `tools/docker/ibmcloud/Dockerfile`. Stage 2
  installs `ibmcloud` (+ container-service plugin), `terraform` (pinned to the
  `toolImages` version, 1.5.7), `helm`, `kubectl`, `oc`, `iperf3`, `nghttp2-client`
  (h2load); copies the binary to `/usr/local/bin/roksbnkctl`; runs as uid 1000
  with a writable `$HOME` (mirror the tools-ibmcloud HOME/runner setup). **Do not**
  install mdbook/pandoc/texlive.
- **`/work` volume contract**: create `/work` owned by uid 1000, set
  `ENV ROKSBNKCTL_HOME=/work/.roksbnkctl`, `WORKDIR /work`. `ENTRYPOINT`
  `["roksbnkctl"]` (the runner IS the tool, unlike the per-tool images).
- **tools/docker/Makefile**: `build-runner` target + add to `build-all` + `clean`.
- **.github/workflows/tools-images.yml**: add `runner` to the image matrix (the
  Go-build stage needs the repo-root build context — match the `ibmcloud` job's
  `file:`/context, not the simple iperf3/mdbook form).

## Issue 2 — `state:` config block + S3 backend rendering [PRD 16]

- **internal/config/workspace.go**: `StateCfg{ Backend string; S3 *StateS3Cfg }`
  where `StateS3Cfg{ Endpoint, Bucket, Region, KeyPrefix, AccessKeySource,
  SecretKeySource string }`. Additive + omitempty; absent block → local.
- **internal/tf/terraform.go**: where the local backend is pinned today, branch on
  `ws.State.Backend`. For `s3`, render a `backend "s3"` block — endpoint
  (`endpoints { s3 = … }`), bucket, key = `<prefix>/<workspace>/<phase>/terraform.tfstate`,
  region, `skip_*` flags COS needs, and the chosen lock setting (per architect:
  native `use_lockfile` if the pinned TF supports it against COS). The per-phase
  `key` is the critical bit — the four phases share the bucket.
- **internal/cred** (or reuse): resolve the HMAC access/secret keys from
  `*_source`, inject as `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` env for the
  terraform process (never in the rendered HCL).

## Issue 3 — local → s3 migration [PRD 16]

- A `roksbnkctl state migrate` path (or documented `init -migrate-state` per
  phase) that flips a workspace from local to s3 and moves each existing
  `terraform.tfstate` into the bucket. Idempotent; refuses if the bucket key
  already holds state (no silent overwrite).

### Scope guards
- `backend: local` MUST render identically to pre-Sprint-31 (golden-test the
  tfvars/backend output).
- The runner image MUST NOT contain mdbook/pandoc (validator asserts this).
- HMAC keys never appear in `terraform.tfstate`, tfvars, or logs (redact).

### Acceptance criteria
1. `make -C tools/docker build-runner` produces an image that runs
   `roksbnkctl version` and each bundled CLI (`terraform version`, `oc version
   --client`, `iperf3 -v`, `h2load --version`, `ibmcloud --version`).
2. A workspace with `state.backend: s3` plans/applies with state in COS, keyed
   per phase; `state.backend: local` (or absent) is unchanged.
3. Migration moves an existing local state into the bucket without drift.

### Files affected
- **New**: `tools/docker/runner/Dockerfile`; `internal/tf/backend*.go` (or
  extend `terraform.go`); migration command.
- **Modified**: `tools/docker/Makefile`, `.github/workflows/tools-images.yml`,
  `internal/config/workspace.go`, `internal/tf/terraform.go`, `internal/cred/`.

### Related
- [PRD 15](../docs/prd/15-RUNNER-IMAGE.md) · [PRD 16](../docs/prd/16-REMOTE-STATE-BACKEND.md)
  · `issue_sprint31_architect.md` (blocking decisions).
