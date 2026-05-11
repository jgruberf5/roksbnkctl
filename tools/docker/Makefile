.PHONY: build-ibmcloud build-iperf3 build-mdbook build-all clean

IMG_PREFIX ?= ghcr.io/jgruberf5/roksbnkctl-tools
TAG ?= dev

build-ibmcloud:
	docker build -t $(IMG_PREFIX)-ibmcloud:$(TAG) ibmcloud/

build-iperf3:
	docker build -t $(IMG_PREFIX)-iperf3:$(TAG) iperf3/

build-mdbook:
	docker build -t $(IMG_PREFIX)-mdbook:$(TAG) mdbook/

build-all: build-ibmcloud build-iperf3 build-mdbook

clean:
	docker image rm -f $(IMG_PREFIX)-ibmcloud:$(TAG) $(IMG_PREFIX)-iperf3:$(TAG) $(IMG_PREFIX)-mdbook:$(TAG) || true
