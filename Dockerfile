FROM golang:1.26 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -tags timetzdata -trimpath -ldflags="-s -w" -o /out/dns-control \
    ./cmd/dns-control

# Static Go runtime: no Alpine package index or apk network access required.
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/dns-control /dns-control
COPY config.yaml /etc/dns-control/config.yaml

USER 10001:10001

EXPOSE 8053/udp
EXPOSE 8053/tcp
EXPOSE 8080/tcp

ENTRYPOINT ["/dns-control", "-config", "/etc/dns-control/config.yaml"]
