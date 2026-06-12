# syntax=docker/dockerfile:1
# roksbnkctl-tools-runner — the all-in-one runner image (PRD 15). Carries the
# roksbnkctl binary PLUS every backend dependency it dispatches, so one
# `docker run` is a complete roksbnkctl with no host tools. USE-TIME ONLY: the
# docs/dev toolchain (mdbook, pandoc, texlive) is deliberately NOT here — that
# lives in tools/docker/mdbook.
#
# Build via tools/docker/Makefile (root build context — the Go stage needs
# ./cmd, ./internal, go.mod/go.sum, embedded.go, ./terraform):
#
#	cd tools/docker && make build-runner
#
# Published as ghcr.io/jgruberf5/roksbnkctl-tools-runner by
# .github/workflows/tools-images.yml (:latest + :<git-tag> on tag push, :dev on
# main / workflow_dispatch).
#
# State: the image holds NO state. roksbnkctl state lives under
# ROKSBNKCTL_HOME=/work/.roksbnkctl — mount a volume at /work to persist it
# across runs (PRD 15 §"State contract"), or configure a COS/S3 remote backend
# (PRD 16). Run --backend local in-container: every dispatched tool is already
# on PATH, so the docker/k8s backends aren't needed here.

# ── Stage 1: build the roksbnkctl binary ─────────────────────────────────────
# Reused VERBATIM from tools/docker/ibmcloud/Dockerfile's build stage — keep the
# two identical so the runner's binary never drifts from the per-tool image's.
FROM golang:1.25-bookworm AS roksbnkctl-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
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

# ── Stage 2: runtime with every dispatched CLI ───────────────────────────────
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    IBMCLOUD_VERSION_CHECK=false

# terraform pin — MUST be >= 1.10 for PRD 16's native S3 state lockfile
# (use_lockfile); IBM COS has no DynamoDB lock-table alternative. roksbnkctl
# execs the host terraform via exec.LookPath, so this pin is what's in effect.
ARG TERRAFORM_VERSION=1.10.5

# A non-root HOME (the CLIs write config there) + the /work state mount, created
# up front owned by uid 1000 / gid 0 and group-writable, so the image runs both
# as a plain uid 1000 (kind / vanilla k8s) and under OpenShift's arbitrary-uid
# SCC (which keeps gid 0). Creating them owned correctly here — and chowning
# ibmcloud's files inside the install RUN below — avoids a late recursive
# `chown -R` that would copy-on-write the whole tree into a duplicate layer.
RUN install -d -m 0775 -o 1000 -g 0 /home/runner /work
ENV HOME=/home/runner \
    IBMCLOUD_HOME=/home/runner

# Base utilities + the apt-installable perf tools (iperf3, h2load via nghttp2).
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates curl wget gnupg jq unzip tar \
        iperf3 nghttp2-client \
 && rm -rf /var/lib/apt/lists/*

# ibmcloud CLI + the container-service (ks) plugin — same installer as
# tools-ibmcloud. HOME is already /home/runner so the plugin lands there.
RUN curl -fsSL https://clis.cloud.ibm.com/install/linux | sh \
 && ibmcloud --version \
 && ibmcloud config --check-version=false \
 && ibmcloud plugin install -f container-service \
 # Fix ownership of the .bluemix config/plugins this RUN created, IN THIS LAYER
 # (not a later `chown -R`), so they're stored once at uid 1000 / gid 0 with
 # group=user perms — no duplicate layer.
 && chown -R 1000:0 /home/runner \
 && chmod -R g=u /home/runner

# terraform (pinned), kubectl + oc + helm (latest stable) — arch-aware so the
# image builds on amd64 and arm64.
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    curl -fsSL -o /tmp/tf.zip "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${arch}.zip"; \
    unzip -o -d /usr/local/bin /tmp/tf.zip; rm /tmp/tf.zip; chmod 0755 /usr/local/bin/terraform; \
    kver="$(curl -fsSL https://dl.k8s.io/release/stable.txt)"; \
    curl -fsSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/${kver}/bin/linux/${arch}/kubectl"; \
    chmod 0755 /usr/local/bin/kubectl; \
    case "$arch" in \
      amd64) octar="openshift-client-linux.tar.gz" ;; \
      arm64) octar="openshift-client-linux-arm64.tar.gz" ;; \
      *)     octar="openshift-client-linux.tar.gz" ;; \
    esac; \
    curl -fsSL -o /tmp/oc.tgz "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/${octar}"; \
    tar -xzf /tmp/oc.tgz -C /usr/local/bin oc; rm /tmp/oc.tgz; chmod 0755 /usr/local/bin/oc; \
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash; \
    terraform version; oc version --client; kubectl version --client; helm version

# The roksbnkctl binary.
COPY --from=roksbnkctl-build /out/roksbnkctl /usr/local/bin/roksbnkctl

# Ownership/permissions for /home/runner and /work are already set above (the
# install RUN created /work owned 1000:0 group-writable; the ibmcloud RUN
# chowned its config in-layer). roksbnkctl state + the terraform plugin cache
# land under ROKSBNKCTL_HOME on the mounted /work volume.
ENV ROKSBNKCTL_HOME=/work/.roksbnkctl
USER 1000
WORKDIR /work

# The runner IS the tool (unlike the per-tool images, which have no ENTRYPOINT
# so the backends can prepend a tool binary).
ENTRYPOINT ["roksbnkctl"]
