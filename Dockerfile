FROM golang:1.26-alpine AS builder

ARG VERSION=2.0.0-dev+local
ARG COMMIT=
ARG BUILD_TIME=

WORKDIR /build

COPY go.mod go.sum* ./

RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags="-s -w -X github.com/bsfdsagfadg/vertex/internal/buildinfo.ReleaseVersion=${VERSION} -X github.com/bsfdsagfadg/vertex/internal/buildinfo.ReleaseCommit=${COMMIT} -X github.com/bsfdsagfadg/vertex/internal/buildinfo.ReleaseBuildTime=${BUILD_TIME} -X github.com/bsfdsagfadg/vertex/internal/buildinfo.ReleaseSource=docker" \
    -o vproxy ./cmd/vproxy \
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
