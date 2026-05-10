FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sprintwatch ./

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates
COPY --from=build /out/sprintwatch /usr/local/bin/sprintwatch
WORKDIR /repo
ENTRYPOINT ["sprintwatch", "--path", "/repo"]
