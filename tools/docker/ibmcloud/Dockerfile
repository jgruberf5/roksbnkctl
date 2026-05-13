# syntax=docker/dockerfile:1
# roksbnkctl-tools-ibmcloud — bundled ibmcloud-cli + ks plugin + the
# roksbnkctl binary itself (so the k8s backend's DNS-probe Job can
# re-exec /usr/local/bin/roksbnkctl in-cluster per the Sprint 5 design
# at internal/cli/test.go::runDNSProbeK8s).
#
# Used by the docker execution backend (PRD 03,
# docs/prd/03-EXECUTION-BACKENDS.md) and by the k8s backend's one-shot
# Job dispatch path.
#
# Build via tools/docker/Makefile:
#
#	cd tools/docker && make build-ibmcloud
#
# The Makefile passes the REPO ROOT as the build context (`-f
# tools/docker/ibmcloud/Dockerfile .`) so the Go build stage below can
# see ./cmd, ./internal, go.mod, etc.
#
# The CI workflow `.github/workflows/tools-images.yml` publishes
# `:latest` and `:<git-tag>` (e.g. `:v0.9.0`) on tag push events.
# Local development builds via tools/docker/Makefile use the `:dev`
# tag (or whatever TAG= is passed), which matches what
# `internal/exec/docker.go::toolImageTag()` resolves at runtime from
# the binary's `internal/cli.Version` ldflag.
#
# Install method (ibmcloud): IBM's own install.sh hosted at
# clis.cloud.ibm.com/install/linux. The script picks the right tarball
# for the runtime arch (linux64 / ppc64le / s390x / arm64), drops
# `ibmcloud` on PATH, and runs the post-install steps.

# Stage 1: build the roksbnkctl binary from the repo source.
#
# This stage exists so the k8s-backend dns-probe Job's container can
# re-exec roksbnkctl inside the pod. Sprint 5's design assumed the
# tools image bundled the binary; the Dockerfile until this fix didn't
# actually do that. e2e step LD5 (roksbnkctl test dns --backend k8s)
# fail-stopped with "executable file /usr/local/bin/roksbnkctl not
# found" — closed by this stage + the COPY below.
FROM golang:1.25-bookworm AS roksbnkctl-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# embedded.go is the root-package source declaring `EmbeddedTerraform`
# via `//go:embed all:terraform`. terraform/ is the HCL tree it embeds.
# Both are required for the build; internal/tf/fetch.go imports the
# root package to access EmbeddedTerraform at FetchSource time.
COPY embedded.go ./
COPY terraform/ ./terraform/
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG ROKSBNKCTL_VERSION=dev
ARG ROKSBNKCTL_COMMIT=unknown
ARG ROKSBNKCTL_BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
        -ldflags "-s -w \
            -X github.com/jgruberf5/roksbnkctl/internal/cli.Version=${ROKSBNKCTL_VERSION} \
            -X github.com/jgruberf5/roksbnkctl/internal/cli.Commit=${ROKSBNKCTL_COMMIT} \
            -X github.com/jgruberf5/roksbnkctl/internal/cli.BuildDate=${ROKSBNKCTL_BUILD_DATE}" \
        -o /out/roksbnkctl ./cmd/roksbnkctl

# Stage 2: ibmcloud runtime.
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    IBMCLOUD_VERSION_CHECK=false

# Install ibmcloud CLI + the ks (container-service) and observe-service
# plugins. The CLI installer pulls the latest stable build; pin via tag
# tooling at release time (the GH Actions workflow publishes :dev / :v*
# tags so users get reproducible image versions per roksbnkctl release).
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        jq \
 && curl -fsSL https://clis.cloud.ibm.com/install/linux | sh \
 && ibmcloud --version \
 && ibmcloud config --check-version=false \
 && ibmcloud plugin install -f container-service \
 && apt-get purge -y --auto-remove curl \
 && rm -rf /var/lib/apt/lists/*

# Bundle the roksbnkctl binary built in stage 1. The k8s backend's
# dispatch path (internal/exec/k8s.go::jobToolCmdOverride at
# `"roksbnkctl": {"/usr/local/bin/roksbnkctl"}`) points here; the
# docker backend's `resolveDockerImageAndArgv` for the "roksbnkctl"
# tool name resolves to this same path.
COPY --from=roksbnkctl-build /out/roksbnkctl /usr/local/bin/roksbnkctl

# Drop to uid 1000 — matches the other tools-* images
# (tools-iperf3, tools-mdbook) and lets the k8s backend's `runAsJob`
# spec (`RunAsNonRoot: true`, no `RunAsUser` pin) pass admission on
# clusters without SCC mutating webhooks (kind, vanilla k8s) without
# colliding with OpenShift's per-namespace UID range. Sprint 9 /
# PRD 04 §"Resolved in Sprint 9" — required for
# TestIntegration_K8sBackend_JobMode_Echo to start the pod.
#
# Provision a writable HOME for uid 1000 BEFORE the USER drop. The
# ibmcloud CLI writes its config dir (`$HOME/.bluemix/`) on first
# invocation regardless of subcommand — even `ibmcloud --version`
# fails with `Configuration error: mkdir /.bluemix: permission
# denied` if HOME isn't set or points to an unwritable location.
# Sprint 9 / v1.2.1 fix: create /home/runner owned by uid 1000 and
# set HOME so ibmcloud's first-run config write lands there. The
# directory survives image rebuilds because it's part of the image
# layer; per-Pod state still works because $HOME is read-write for
# the running user inside the container.
RUN install -d -m 0755 -o 1000 -g 0 /home/runner
ENV HOME=/home/runner
USER 1000
WORKDIR /home/runner

# No ENTRYPOINT — Sprint 6 (PLAN.md row §"Code/config deliverables" +
# `issues/resolved_sprint5_staff.md` Issue 1). The image carries
# BOTH `ibmcloud` and `roksbnkctl` (self-exec via the docker and k8s
# backends), so a single ENTRYPOINT can't service both callers
# without per-tool argv juggling. The docker backend's
# `resolveDockerImageAndArgv` (`internal/exec/docker.go`) prepends
# the tool binary explicitly in the `Cmd` slice; the k8s backend's
# `jobToolCmdOverride` map does the same.
