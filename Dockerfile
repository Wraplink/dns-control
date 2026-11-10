FROM golang:1.26 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN mkdir -p /out/dns-query-spool && chmod 0700 /out/dns-query-spool && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -tags timetzdata -trimpath -ldflags="-s -w" -o /out/dns-control \
    ./cmd/dns-control

# Static Go runtime: no Alpine package index or apk network access required.
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/dns-control /dns-control
COPY config.yaml /etc/dns-control/config.yaml
# Docker named volumes inherit this writable directory on first creation.
COPY --from=builder --chown=10001:10001 /out/dns-query-spool /var/lib/wraplink/dns-query-spool

USER 10001:10001

EXPOSE 8053/udp
EXPOSE 8053/tcp
EXPOSE 8080/tcp

ENTRYPOINT ["/dns-control", "-config", "/etc/dns-control/config.yaml"]
