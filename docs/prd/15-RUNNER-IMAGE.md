# PRD 15 — the all-in-one roksbnkctl runner image

> A single container that carries the `roksbnkctl` binary **plus every backend dependency it dispatches** — so "run roksbnkctl from a container" becomes a first-class install target for CI, fleet provisioning, and air-gapped sites. Distinct from the per-tool images (PRD 03), which stay for the docker/k8s *backend-dispatch* path. The image is **use-time only**: the docs/dev toolchain (mdbook, pandoc, texlive) is deliberately excluded. Pairs with [PRD 16](./16-REMOTE-STATE-BACKEND.md), which lets the container keep state without a host volume. Estimated effort: small (~1 Dockerfile + Makefile/CI wiring + a book chapter); the binary build stage is reused verbatim.

## Why

roksbnkctl ships as a binary (`go install` / release tarball) and assumes host tools for some paths (terraform, helm, kubectl, oc). The per-tool images from PRD 03 (`roksbnkctl-tools-{ibmcloud,iperf3,h2load}`) exist so the **docker/k8s execution backends** can dispatch a single tool to a container — they are not a way to *run roksbnkctl itself*.

There is no single artifact an operator can `docker run` to get a complete, self-contained roksbnkctl. That artifact is exactly what CI runners, fleet provisioners, and air-gapped installs want: one image to pull (or mirror behind a firewall) instead of installing a binary + six CLIs on every host. This PRD builds it.

## Goal

Publish `ghcr.io/jgruberf5/roksbnkctl-tools-runner:<tag>` — the binary + the tools the backends dispatch — with `ENTRYPOINT ["roksbnkctl"]`, so:

```
docker run --rm \
  -v bnk-state:/work \
  -e ROKSBNKCTL_HOME=/work/.roksbnkctl \
  ghcr.io/jgruberf5/roksbnkctl-tools-runner:v1.11.0 up
```

is a complete roksbnkctl invocation with no host dependencies.

## Design

### Contents — use-time only

Multi-stage build:

- **Stage 1 — binary.** Reuse the `roksbnkctl-build` stage from `tools/docker/ibmcloud/Dockerfile` **verbatim** (same `golang:1.25-bookworm`, the same `embedded.go` + `terraform/` + `cmd/` + `internal/` copies, the same `ROKSBNKCTL_VERSION`/`COMMIT`/`BUILD_DATE` ldflags). Do not fork the build — a drift between the runner's binary and the ibmcloud image's binary would be a footgun. The Makefile passes the repo root as the build context (as it already does for `ibmcloud`).
- **Stage 2 — runtime (`ubuntu:22.04`, matching tools-ibmcloud).** Install the dependencies the backends dispatch:
  - `ibmcloud` (+ `container-service` plugin) — same installer as tools-ibmcloud
  - `terraform` — **pinned ≥ 1.10** (this is load-bearing: PRD 16's COS state locking needs the native S3 lockfile, added in TF 1.10; the host `terraform` is what roksbnkctl execs via `exec.LookPath`, so the runner's pin is what's in effect)
  - `helm`, `kubectl`, `oc`
  - `iperf3`, `h2load` (`nghttp2-client`)
  - copy `/usr/local/bin/roksbnkctl` from stage 1

  **Excluded:** mdbook, pandoc, texlive, mermaid-cli. These are the book *build* toolchain (`tools/docker/mdbook`) — dev-time only, never needed to *use* roksbnkctl, and they dominate image size. Keeping them out is a hard requirement the validator asserts.

### `ENTRYPOINT` + the `--backend local` story

Unlike the per-tool images (which have no ENTRYPOINT, because the backends prepend the tool binary explicitly), the runner **is** the tool: `ENTRYPOINT ["roksbnkctl"]`.

Because every dispatched tool is already in the image, the docker/k8s backends — whose whole purpose is to fetch a tool from another network location — are redundant *inside* the runner. The runner runs tools **locally** (no docker-in-docker, no in-cluster Job needed for a tool that's on `PATH`). `--backend local` is the in-container default story; the docker/k8s backends remain for the binary-on-host case. (Whether to *auto-default* `--backend local` on an "I'm in the runner" signal vs just document it is an open question below.)

### State contract

The image holds **no state**. roksbnkctl's state is local files under `$ROKSBNKCTL_HOME` (default `~/.roksbnkctl`); terraform uses the local backend pinned per phase. So the runner must point `ROKSBNKCTL_HOME` at durable storage:

- `RUN install -d -m 0755 -o 1000 /work` (owned by the non-root uid), `ENV ROKSBNKCTL_HOME=/work/.roksbnkctl`, `WORKDIR /work`.
- Operators mount a named volume or host bind at `/work`. This composes with the existing `/work` convention the `kubeconfig_dir` / `scratch_dir` defaults already assume.
- Non-root **uid 1000** with a writable `$HOME` (mirror the tools-ibmcloud `/home/runner` setup so `ibmcloud`'s first-run config write succeeds), matching the other tools images and OpenShift/k8s `runAsNonRoot` admission.

This is the volume answer. [PRD 16](./16-REMOTE-STATE-BACKEND.md) is the no-volume / fleet answer — state in COS, nothing durable in the container.

### Build + publish

- `tools/docker/Makefile`: `build-runner` target (root build context, like `build-ibmcloud`); add to `build-all` + `clean`.
- `.github/workflows/tools-images.yml`: add `runner` to the image matrix. It needs the **root build context** (the Go stage reads `./cmd`, `./internal`, `go.mod`), so it uses the `ibmcloud` job's `file:`/context form, not the simple `iperf3`/`mdbook` directory-context form.

### Relationship to the per-tool images

The runner **supersedes** `tools-ibmcloud` for human/CI *use* of roksbnkctl, but the per-tool images (`tools-ibmcloud`, `tools-iperf3`, `tools-h2load`) **stay** — the docker/k8s backends still dispatch individual tools to them, and the k8s backend's in-cluster self-exec Job still uses the binary embedded in `tools-ibmcloud`. The two coexist: one image to *run* roksbnkctl; per-tool images for *backend dispatch*.

## Scope

### In scope
- `tools/docker/runner/Dockerfile` (multi-stage: reused build stage + the bundled CLIs).
- Makefile `build-runner` + CI matrix entry.
- A book chapter documenting the container as an install target (tech-writer, [issue](../../issues/issue_sprint31_tech-writer.md) Issue 1).

### Out of scope
- A `--backend local` *auto-default* (open question; ship the documented default first).
- Slimming the base below `ubuntu:22.04` (a follow-on; correctness before size).
- Bundling mdbook/pandoc (explicitly never — the docs image owns those).

## Acceptance

1. `make -C tools/docker build-runner` produces an image whose `ENTRYPOINT` runs `roksbnkctl`, and `docker run … runner version` exits 0.
2. Each bundled CLI responds in-image: `terraform version` (≥ 1.10), `oc version --client`, `kubectl version --client`, `helm version`, `ibmcloud --version`, `iperf3 -v`, `h2load --version`.
3. `mdbook` and `pandoc` are **absent** (`! command -v mdbook && ! command -v pandoc`); image size is recorded so the docs toolchain can't creep in.
4. Running roksbnkctl from the container with `-v <vol>:/work -e ROKSBNKCTL_HOME=/work/.roksbnkctl` creates a workspace whose config/state survive a second container run against the same volume.
5. The `tools-images` workflow builds + publishes `:latest` and `:<tag>` on tag push.

## Open questions
- **Auto-`--backend local` in-container.** Detect "running in the runner" (e.g. a baked `ROKSBNKCTL_RUNNER=1` env) and default the backend to `local`, or leave it explicit/documented? Auto is friendlier; explicit is less magic.
- **Base-image slimming.** A `debian-slim`/`distroless`-ish base would cut size, but `ibmcloud`'s installer + the CLIs assume a glibc userland; defer until the contents are proven.

## Cross-references
- [PRD 03 — execution backends](./03-EXECUTION-BACKENDS.md) — the per-tool image model the runner sits beside.
- [PRD 16 — COS/S3 remote state backend](./16-REMOTE-STATE-BACKEND.md) — the no-volume state path that makes the runner truly stateless.
- `tools/docker/ibmcloud/Dockerfile` — the Go-build stage reused verbatim; `tools/docker/Makefile`; `.github/workflows/tools-images.yml`.
- `internal/config/paths.go` — `ROKSBNKCTL_HOME`; `internal/tf/terraform.go` — host `terraform` via `exec.LookPath` (why the runner's terraform pin is what's in effect).
- Sprint 31 issues: [staff](../../issues/issue_sprint31_staff.md) Issue 1, [validator](../../issues/issue_sprint31_validator.md) Issues 1–2, [tech-writer](../../issues/issue_sprint31_tech-writer.md) Issue 1.
