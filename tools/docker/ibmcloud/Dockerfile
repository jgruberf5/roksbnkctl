# syntax=docker/dockerfile:1
# roksbnkctl-tools-ibmcloud — bundled ibmcloud-cli + ks plugin. Used by
# the docker execution backend (PRD 03, docs/prd/03-EXECUTION-BACKENDS.md).
#
# Build via tools/docker/Makefile:
#
#	cd tools/docker && make build-ibmcloud
#
# The CI workflow `.github/workflows/tools-images.yml` publishes
# `:latest` and `:<git-tag>` (e.g. `:v0.9.0`) on tag push events.
# Local development builds via tools/docker/Makefile use the `:dev`
# tag, which matches what `internal/exec/docker.go::toolImages`
# resolves at runtime in this build.
#
# Install method: IBM's own install.sh hosted at
# clis.cloud.ibm.com/install/linux. The script picks the right tarball
# for the runtime arch (linux64 / ppc64le / s390x / arm64), drops
# `ibmcloud` on PATH, and runs the post-install steps the manual
# tarball install requires.
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

# Default to `ibmcloud` as the entrypoint so callers can `docker run
# IMAGE ks cluster ls` without repeating the binary name. Backends that
# want a different shape can override the entrypoint per-invocation.
ENTRYPOINT ["ibmcloud"]
