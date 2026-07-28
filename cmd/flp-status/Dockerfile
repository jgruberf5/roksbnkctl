# flp-status — the F5 License Proxy status web service. One image, two runtimes:
#   • VSI:     a container in the FLP podman pod; talks to the pod's other
#              containers over the mounted podman socket (CONTAINER_HOST +
#              podman-remote), publishes :80.
#   • Cluster: a Deployment; talks to the Kubernetes API via its ServiceAccount
#              (kubectl), exposed by a NodePort Service.
#
# Build (from the repo root):
#   docker build -f cmd/flp-status/Dockerfile -t <registry>/flp-status:<tag> .
# For an air-gapped deploy, push it to your registry and mirror it into Harbor
# (registry replicate handles the FAR set; a custom image is a one-liner crane
# copy / `docker push <harbor>/bnk-mirror/flp-status:<tag>`).

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/flp-status ./cmd/flp-status

# Runtime: the two adapters shell out to `curl`, `podman` (remote), and `kubectl`.
FROM alpine:3.20
RUN apk add --no-cache curl podman-remote kubectl ca-certificates
COPY --from=build /out/flp-status /usr/local/bin/flp-status
# VSI: set CONTAINER_HOST=unix:///run/podman/podman.sock and mount the socket.
# Cluster: the ServiceAccount token is auto-mounted; kubectl uses in-cluster config.
EXPOSE 80
USER 0
ENTRYPOINT ["/usr/local/bin/flp-status"]
