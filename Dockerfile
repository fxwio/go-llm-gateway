# syntax=docker/dockerfile:1.7

FROM golang:1.23-alpine AS builder
WORKDIR /src

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN apk --no-cache add ca-certificates git tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
      -ldflags="-s -w \
        -X github.com/fxwio/go-llm-gateway/internal/buildinfo.Version=${VERSION} \
        -X github.com/fxwio/go-llm-gateway/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/fxwio/go-llm-gateway/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/llm-gateway ./cmd/gateway/main.go

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="go-llm-gateway" \
      org.opencontainers.image.description="A production-grade LLM gateway built in Go" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

WORKDIR /app
COPY --from=builder --chown=65532:65532 /out/llm-gateway /llm-gateway

ENV GATEWAY_CONFIG_PATH=/etc/go-llm-gateway/config.yaml

EXPOSE 8080
USER 65532:65532

ENTRYPOINT ["/llm-gateway"]
