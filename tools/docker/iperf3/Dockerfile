# syntax=docker/dockerfile:1
# roksbnkctl-tools-iperf3 — alpine + iperf3 binary. Used by the k8s
# execution backend's iperf3 server pod (see PRD 03). Skeleton image;
# client/server invocation is wired up in Sprint 4 (PLAN.md §"iperf3
# backend selection").
FROM alpine:3.19
RUN apk add --no-cache iperf3
ENTRYPOINT ["iperf3"]
