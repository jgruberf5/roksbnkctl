# syntax=docker/dockerfile:1
# roksbnkctl-tools-iperf3 — alpine + iperf3 binary. Used by the k8s
# execution backend's iperf3 server pod. Skeleton image; client/server
# invocation is wired up by the iperf3 backend selection logic.
FROM alpine:3.19
RUN apk add --no-cache iperf3
# Run as a fixed non-root uid so the pod's restricted-v2 SCC + plain-k8s
# `runAsNonRoot: true` policies (per internal/k8s/iperf3.go) accept the
# image. iperf3 needs no privileged ports (5201 is unprivileged).
USER 1000
ENTRYPOINT ["iperf3"]
