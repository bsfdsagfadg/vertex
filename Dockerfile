FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY go.mod go.sum* ./

ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 go build -tags "with_utls with_quic with_wireguard" -buildvcs=false -trimpath -ldflags="-s -w" -o vproxy ./cmd/vproxy \
    && go clean -cache -modcache -testcache

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata bash

WORKDIR /app

COPY --from=builder /build/vproxy /app/vproxy
COPY config/config.example.json /app/config.example.json
COPY config/api_keys.example.txt /app/api_keys.example.txt
COPY config/models.json /app/models.json
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

ENV VPROXY_CONFIG=/app/config/config.json
ENV VPROXY_API_KEYS=/app/config/api_keys.txt
ENV VPROXY_MODELS=/app/config/models.json

EXPOSE 2156

ENTRYPOINT ["/app/entrypoint.sh"]