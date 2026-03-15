# syntax=docker/dockerfile:1

# 第一阶段：编译构建
FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-w -s" -o /out/llm-gateway ./cmd/gateway/main.go

# 第二阶段：极简运行环境
FROM alpine:3.22
RUN apk --no-cache add ca-certificates tzdata \
    && addgroup -S gateway \
    && adduser -S -D -H -h /app -s /sbin/nologin -G gateway gateway

WORKDIR /app
COPY --from=builder /out/llm-gateway /usr/local/bin/llm-gateway

ENV GATEWAY_CONFIG_PATH=/etc/go-llm-gateway/config.yaml
EXPOSE 8080
USER gateway

# 配置文件不再内置进镜像，必须通过 volume / Secret / ConfigMap 注入。
CMD ["/usr/local/bin/llm-gateway"]
