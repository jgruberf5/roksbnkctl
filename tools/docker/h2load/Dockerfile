# syntax=docker/dockerfile:1
# roksbnkctl-tools-h2load — alpine + h2load (nghttp2). The L7 load
# generator for `roksbnkctl test matrix` (HTTPRoute, http/https). Used by
# the docker / k8s execution backends; the ssh backend uses the jumphost's
# apt-installed nghttp2-client instead (preinstalled via the testing
# module user_data — no --bootstrap needed).
#
# Alpine's `nghttp2` package ships the command-line tools (h2load, nghttp,
# nghttpd) built against OpenSSL, so https targets (TLS terminate at TMM)
# work out of the box.
FROM alpine:3.19
RUN apk add --no-cache nghttp2 ca-certificates
# Run as a fixed non-root uid so the restricted-v2 SCC + plain-k8s
# runAsNonRoot policies accept the image (mirrors the iperf3 image).
# h2load is a pure client — it binds no privileged ports.
USER 1000
ENTRYPOINT ["h2load"]
