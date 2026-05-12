.PHONY: build-ibmcloud build-iperf3 build-mdbook build-all clean

IMG_PREFIX ?= ghcr.io/jgruberf5/roksbnkctl-tools
TAG ?= dev

# Repo root, derived relative to this Makefile (tools/docker/Makefile).
# The ibmcloud image's Go build stage needs the repo root as build
# context so it can see ./cmd, ./internal, go.mod (which live above
# tools/docker/ibmcloud/).
REPO_ROOT ?= $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)

# Build args propagated into the ibmcloud image's Stage 1 Go build —
# match the host `make build` ldflags so the bundled roksbnkctl
# reports the same version / commit / date.
ROKSBNKCTL_VERSION ?= $(TAG)
ROKSBNKCTL_COMMIT  ?= $(shell git -C $(REPO_ROOT) rev-parse --short HEAD 2>/dev/null || echo none)
ROKSBNKCTL_BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

build-ibmcloud:
	docker build \
	    -f $(REPO_ROOT)/tools/docker/ibmcloud/Dockerfile \
	    --build-arg ROKSBNKCTL_VERSION=$(ROKSBNKCTL_VERSION) \
	    --build-arg ROKSBNKCTL_COMMIT=$(ROKSBNKCTL_COMMIT) \
	    --build-arg ROKSBNKCTL_BUILD_DATE=$(ROKSBNKCTL_BUILD_DATE) \
	    -t $(IMG_PREFIX)-ibmcloud:$(TAG) \
	    $(REPO_ROOT)

build-iperf3:
	docker build -t $(IMG_PREFIX)-iperf3:$(TAG) iperf3/

build-mdbook:
	docker build -t $(IMG_PREFIX)-mdbook:$(TAG) mdbook/

build-all: build-ibmcloud build-iperf3 build-mdbook

clean:
	docker image rm -f $(IMG_PREFIX)-ibmcloud:$(TAG) $(IMG_PREFIX)-iperf3:$(TAG) $(IMG_PREFIX)-mdbook:$(TAG) || true
