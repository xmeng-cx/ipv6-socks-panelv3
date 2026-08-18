ARG XRAY_IMAGE=ghcr.io/xtls/xray-core:26.5.9
ARG GO_IMAGE=golang:1.25-alpine
ARG ALPINE_IMAGE=alpine:3.22

FROM ${XRAY_IMAGE} AS xray

FROM ${GO_IMAGE} AS builder
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ipv6-socks-panel ./cmd/panel

FROM ${ALPINE_IMAGE}
RUN apk add --no-cache ca-certificates tzdata
COPY --from=xray /usr/local/bin/xray /usr/local/bin/xray
COPY --from=xray /usr/local/share/xray /usr/local/share/xray
COPY --from=builder /out/ipv6-socks-panel /usr/local/bin/ipv6-socks-panel
ENV DATA_DIR=/data \
    XRAY_BINARY=/usr/local/bin/xray \
    TZ=Asia/Shanghai
VOLUME ["/data"]
HEALTHCHECK --interval=20s --timeout=3s --start-period=60s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:8080/healthz" || exit 1
ENTRYPOINT ["/usr/local/bin/ipv6-socks-panel"]
