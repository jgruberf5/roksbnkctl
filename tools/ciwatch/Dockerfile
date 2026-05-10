FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ciwatch ./

FROM alpine:3.20
RUN apk add --no-cache git github-cli ca-certificates
COPY --from=build /out/ciwatch /usr/local/bin/ciwatch
WORKDIR /repo
ENTRYPOINT ["ciwatch", "--path", "/repo"]
