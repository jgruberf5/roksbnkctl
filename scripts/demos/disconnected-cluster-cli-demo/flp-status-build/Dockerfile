FROM alpine:3.20
RUN apk add --no-cache curl podman ca-certificates
COPY flp-status /usr/local/bin/flp-status
EXPOSE 80
ENTRYPOINT ["/usr/local/bin/flp-status"]
