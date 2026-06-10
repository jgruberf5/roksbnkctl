.PHONY: build-aws build-iperf3 build-all clean

# GHCR normalises org names to lowercase server-side; the
# `JLCode-tech` casing in the .github/workflows/tools-images.yml
# `tags:` strings is fine for the GitHub Actions docker/build-push
# action (it lowercases before push). Local `docker build`, by
# contrast, rejects mixed-case repository names outright. Keep the
# Makefile lowercased so `make build-aws` / `make build-iperf3`
# succeed on a contributor's laptop; the published image name on GHCR
# is identical either way.
IMG_PREFIX ?= ghcr.io/jlcode-tech/awsbnkctl-tools
TAG ?= dev

# Repo root, derived relative to this Makefile (tools/docker/Makefile).
# Used by image build stages (e.g. the AWS image's optional Go build
# step) that need the repo root as build context so they can see
# ./cmd, ./internal, go.mod.
REPO_ROOT ?= $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)

# Build args propagated into image Go build stages — match the host
# `make build` ldflags so any bundled awsbnkctl reports the same
# version / commit / date.
AWSBNKCTL_VERSION ?= $(TAG)
AWSBNKCTL_COMMIT  ?= $(shell git -C $(REPO_ROOT) rev-parse --short HEAD 2>/dev/null || echo none)
AWSBNKCTL_BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# `build-aws` builds the AWS-flavoured tools image. Bumping awscli/kubectl/helm
# versions is a `--build-arg` override:
#
#   make build-aws AWSCLI_VERSION=2.18.0 KUBECTL_VERSION=v1.31.0
#
# The Dockerfile pins SHA256 checksums alongside each ARG; bumping a
# version without refreshing the corresponding checksum fails the build
# (intentional supply-chain protection — see tools/docker/aws/Dockerfile
# stage 1 commentary).
AWSCLI_VERSION ?=
KUBECTL_VERSION ?=
HELM_VERSION ?=

# linux/amd64 is the only platform the AWS image needs to target —
# awscli v2's vendored Python interpreter has historically been
# amd64-only (and reliably-on-amd64); EKS worker AMIs are amd64-default.
# Honor a caller override (DOCKER_PLATFORM=linux/arm64,linux/amd64) if
# someone needs multi-arch.
DOCKER_PLATFORM ?= linux/amd64

build-aws:
	docker build \
		--platform $(DOCKER_PLATFORM) \
		$(if $(AWSCLI_VERSION),--build-arg AWSCLI_VERSION=$(AWSCLI_VERSION)) \
		$(if $(KUBECTL_VERSION),--build-arg KUBECTL_VERSION=$(KUBECTL_VERSION)) \
		$(if $(HELM_VERSION),--build-arg HELM_VERSION=$(HELM_VERSION)) \
		-t $(IMG_PREFIX)-aws:$(TAG) aws/

build-iperf3:
	docker build -t $(IMG_PREFIX)-iperf3:$(TAG) iperf3/

build-all: build-aws build-iperf3

clean:
	docker image rm -f \
		$(IMG_PREFIX)-aws:$(TAG) \
		$(IMG_PREFIX)-iperf3:$(TAG) || true
