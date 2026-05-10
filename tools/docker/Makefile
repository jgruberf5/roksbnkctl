.PHONY: build-ibmcloud build-iperf3 build-all clean

IMG_PREFIX ?= ghcr.io/jgruberf5/roksbnkctl-tools
TAG ?= dev

build-ibmcloud:
	docker build -t $(IMG_PREFIX)-ibmcloud:$(TAG) ibmcloud/

build-iperf3:
	docker build -t $(IMG_PREFIX)-iperf3:$(TAG) iperf3/

build-all: build-ibmcloud build-iperf3

clean:
	docker image rm -f $(IMG_PREFIX)-ibmcloud:$(TAG) $(IMG_PREFIX)-iperf3:$(TAG) || true
