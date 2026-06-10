# syntax=docker/dockerfile:1
# awsbnkctl-tools-aws — Alpine-based tools image carrying:
#   - awscli v2 (official AWS bundle — NOT `pip install awscli` which is v1)
#   - kubectl  (matched to cluster_version default 1.30)
#   - helm     (v3.x — used by the S3 supply-chain manifests)
#   - iperf3 / dig / jq / curl / ca-certificates (runtime deps)
#
# Consumed by the docker execution backend's AWS code paths —
# `awsbnkctl up cluster --backend docker` shells into this image to run
# `aws eks describe-cluster`, `kubectl apply`, `helm install`, etc.
# without requiring host installs.
#
# The inherited tools-image was IBM-shaped (`tools/docker/ibmcloud/Dockerfile`).
# This is its AWS-flavoured replacement. The uid 1000 + writable
# /home/runner + ENV HOME=/home/runner posture is carried over from
# roksbnkctl v1.2.0's post-mortem fix for `mkdir /.bluemix: permission
# denied`-style failures (CLI tools that expect to write $HOME at first
# invocation will explode if HOME is unset or unwritable).
#
# The awscli/kubectl/helm download URLs branch on $TARGETARCH so this
# image builds cleanly on linux/amd64 AND linux/arm64. Both arches get
# their own pinned sha256 checksums — supply-chain protection survives
# the multi-arch split because each architecture's binary is verified
# independently against an architecture-specific pin.
#
# Multi-stage:
#   Stage 1 (downloader) — fetches awscli v2 zip, kubectl + helm
#   binaries from official URLs; verifies sha256 checksums. Build fails
#   fast if any checksum mismatches (supply-chain protection — a swap
#   at the upstream CDN can't pass through the build).
#
#   Stage 2 (runtime)    — minimal alpine; installs runtime deps, copies
#   binaries from stage 1, drops privileges to uid 1000.

# ── Stage 1: downloader ─────────────────────────────────────────────
FROM alpine:3.19 AS downloader

# TARGETARCH is auto-set by buildx (docker buildx build --platform
# linux/amd64,linux/arm64 ...). Values seen: `amd64`, `arm64`. The case
# statement below maps to the per-tool naming conventions:
#   - awscli:  `x86_64` / `aarch64`
#   - kubectl: `amd64`  / `arm64`
#   - helm:    `amd64`  / `arm64`
ARG TARGETARCH

# Pinned versions. Bumps land in a deliberate commit — supply-chain
# rotations need a code review pass, never a silent CI auto-bump.
ARG AWSCLI_VERSION=2.17.52
ARG KUBECTL_VERSION=v1.30.5
ARG HELM_VERSION=v3.15.4

# Pinned sha256 checksums per architecture. Refreshed when
# AWSCLI_VERSION / KUBECTL_VERSION / HELM_VERSION are bumped; mismatch
# fails the build.
#
# Sources (AWS publishes the zip but no .sha256 file — checksum was
# computed against the official zip at version-pin time; kubectl + helm
# publish .sha256 / .sha256sum sidecar files that were fetched verbatim):
#   awscli (amd64):  shasum -a 256 awscli-exe-linux-x86_64-${AWSCLI_VERSION}.zip
#   awscli (arm64):  shasum -a 256 awscli-exe-linux-aarch64-${AWSCLI_VERSION}.zip
#   kubectl:         https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/<arch>/kubectl.sha256
#   helm:            https://get.helm.sh/helm-${HELM_VERSION}-linux-<arch>.tar.gz.sha256sum
ARG AWSCLI_SHA256_AMD64=11dd0016d93a2e9b5b0f3f13a5d4ee42a09581ef0371461c1c1c1d40c511d81e
ARG AWSCLI_SHA256_ARM64=f9b2b8486dfa45470142129db8e1fa44aa6434bf7a9cd6927e815f6b8b3609aa
ARG KUBECTL_SHA256_AMD64=b8aa921a580c3d8ba473236815de5ce5173d6fbfa2ccff453fa5eef46cc5ee7a
ARG KUBECTL_SHA256_ARM64=efc594857f9255fc33bcda9409b8862a3b47ce5f4e09d51c3427b85dd769b9b9
ARG HELM_SHA256_AMD64=11400fecfc07fd6f034863e4e0c4c4445594673fd2a129e701fe41f31170cfa9
ARG HELM_SHA256_ARM64=fa419ecb139442e8a594c242343fafb7a46af3af34041c4eac1efcc49d74e626

RUN apk add --no-cache curl unzip tar

WORKDIR /staging

# awscli v2 — vendored python runtime, no host pip needed. The
# `aws-cli` zip extracts to ./aws/ with an installer script; we run
# `./install` into /opt/aws-cli so stage 2 can copy a single tree.
#
# Per-arch URL: AWS publishes `awscli-exe-linux-x86_64-<v>.zip` and
# `awscli-exe-linux-aarch64-<v>.zip` from the same CDN base URL.
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) AWSCLI_ARCH=x86_64;  AWSCLI_SHA256="${AWSCLI_SHA256_AMD64}" ;; \
        arm64) AWSCLI_ARCH=aarch64; AWSCLI_SHA256="${AWSCLI_SHA256_ARM64}" ;; \
        *) echo "FATAL: unsupported TARGETARCH=${TARGETARCH}"; exit 1 ;; \
    esac; \
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${AWSCLI_ARCH}-${AWSCLI_VERSION}.zip" \
        -o awscli.zip; \
    echo "${AWSCLI_SHA256}  awscli.zip" > awscli.zip.sha256; \
    sha256sum -c awscli.zip.sha256 || { \
        echo "FATAL: awscli (${AWSCLI_ARCH}) checksum mismatch — supply-chain swap suspected"; \
        echo "expected: ${AWSCLI_SHA256}"; \
        echo "got:      $(sha256sum awscli.zip)"; \
        exit 1; }; \
    unzip -q awscli.zip; \
    ./aws/install -i /opt/aws-cli -b /opt/aws-cli/bin; \
    rm -rf awscli.zip aws/

# kubectl — single binary, no install step. Upstream publishes a
# `linux/amd64/kubectl` and `linux/arm64/kubectl` under the same release
# tree; both URLs return the same sha256 sidecar pattern.
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) KUBECTL_SHA256="${KUBECTL_SHA256_AMD64}" ;; \
        arm64) KUBECTL_SHA256="${KUBECTL_SHA256_ARM64}" ;; \
        *) echo "FATAL: unsupported TARGETARCH=${TARGETARCH}"; exit 1 ;; \
    esac; \
    curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
        -o /staging/kubectl; \
    echo "${KUBECTL_SHA256}  /staging/kubectl" > kubectl.sha256; \
    sha256sum -c kubectl.sha256 || { \
        echo "FATAL: kubectl (${TARGETARCH}) checksum mismatch"; \
        exit 1; }; \
    chmod +x /staging/kubectl

# helm — tarball, extract the linux-<arch>/helm binary. Upstream tarball
# layout: `helm-<v>-linux-amd64.tar.gz` extracts to `linux-amd64/helm`;
# `helm-<v>-linux-arm64.tar.gz` extracts to `linux-arm64/helm`. Same
# binary name post-extract; the parent directory carries the arch.
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) HELM_SHA256="${HELM_SHA256_AMD64}" ;; \
        arm64) HELM_SHA256="${HELM_SHA256_ARM64}" ;; \
        *) echo "FATAL: unsupported TARGETARCH=${TARGETARCH}"; exit 1 ;; \
    esac; \
    curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" \
        -o helm.tar.gz; \
    echo "${HELM_SHA256}  helm.tar.gz" > helm.tar.gz.sha256; \
    sha256sum -c helm.tar.gz.sha256 || { \
        echo "FATAL: helm (${TARGETARCH}) checksum mismatch"; \
        exit 1; }; \
    tar -xzf helm.tar.gz; \
    mv linux-${TARGETARCH}/helm /staging/helm; \
    chmod +x /staging/helm; \
    rm -rf helm.tar.gz linux-${TARGETARCH}/


# ── Stage 2: runtime ────────────────────────────────────────────────
FROM alpine:3.19

LABEL org.opencontainers.image.source="https://github.com/JLCode-tech/awsbnkctl"
LABEL org.opencontainers.image.description="awsbnkctl AWS tools image — awscli v2 + kubectl + helm + iperf3 + dig + jq + curl"
LABEL org.opencontainers.image.licenses="Apache-2.0"

# Runtime deps:
#   bash         — awscli v2's wrapper expects it
#   ca-certificates — TLS to AWS endpoints
#   curl, jq     — common HTTP + JSON-munging tools
#   iperf3       — throughput probe surface
#   bind-tools   — provides `dig` for the DNS probe surface
#   groff, less  — awscli v2 help pager (awscli v2's vendored python
#                  shells out to `less`; without it `aws help` emits a
#                  confusing error)
#   libstdc++    — awscli v2's vendored python runtime needs it for
#                  some bundled C extensions
RUN apk add --no-cache \
        bash \
        ca-certificates \
        curl \
        jq \
        iperf3 \
        bind-tools \
        groff \
        less \
        libstdc++

# Copy binaries from the downloader stage.
COPY --from=downloader /opt/aws-cli /opt/aws-cli
COPY --from=downloader /staging/kubectl /usr/local/bin/kubectl
COPY --from=downloader /staging/helm /usr/local/bin/helm

# awscli v2's `install -b` flag created /opt/aws-cli/bin/aws as a
# wrapper. Symlink into /usr/local/bin so it's on the default PATH.
RUN ln -s /opt/aws-cli/bin/aws /usr/local/bin/aws \
 && ln -s /opt/aws-cli/bin/aws_completer /usr/local/bin/aws_completer

# Create a non-root runner user with a writable home — uid 1000 matches
# the iperf3 sibling image and the `restricted-v2`-style SCC / k8s
# `runAsNonRoot: true` policy the docker backend hands the image. The
# writable $HOME and `ENV HOME=/home/runner` are the load-bearing piece
# of the v1.2.0 post-mortem (see comment at top of file): tools like
# `aws configure`, `kubectl config set-context`, `helm repo add` all
# expect to write under $HOME at first invocation; if HOME is unset or
# unwritable they explode with `permission denied`.
RUN addgroup -g 1000 runner \
 && adduser -D -u 1000 -G runner -h /home/runner runner \
 && mkdir -p /home/runner/.aws /home/runner/.kube /home/runner/.config/helm \
 && chown -R 1000:1000 /home/runner

USER 1000
WORKDIR /home/runner
ENV HOME=/home/runner
ENV PATH=/home/runner/.local/bin:/usr/local/bin:/usr/bin:/bin

# Default entrypoint left empty — the docker exec backend always
# invokes with an explicit command (`aws ...` / `kubectl ...` /
# `helm ...` / `iperf3 ...`). Setting ENTRYPOINT to any one of them
# would block the multi-tool use case.
CMD ["/bin/bash"]
