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

# No ENTRYPOINT — Sprint 6 (PLAN.md row §"Code/config deliverables" +
# `issues/resolved_sprint5_staff.md` Issue 1). The image carries
# BOTH `ibmcloud` and `roksbnkctl` (self-exec via the docker backend),
# so a single ENTRYPOINT can't service both callers without per-tool
# argv juggling. The docker backend's `resolveDockerImageAndArgv`
# (`internal/exec/docker.go`) now prepends the tool binary explicitly
# in the `Cmd` slice; the k8s backend's `jobToolCmdOverride` map is
# correspondingly trimmed (the `roksbnkctl` entry is removed; ibmcloud
# never needed an override there because `runOnOpsPod`'s `kubectl exec`
# doesn't see the image's ENTRYPOINT at exec time).
