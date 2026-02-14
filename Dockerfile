FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /consul-unifi-watcher .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /consul-unifi-watcher /usr/local/bin/consul-unifi-watcher
USER nobody
ENTRYPOINT ["consul-unifi-watcher"]
