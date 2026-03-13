# 第一阶段：编译构建
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 禁用 CGO，构建无依赖的纯静态二进制文件
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o llm-gateway ./cmd/gateway/main.go

# 第二阶段：极简运行环境
FROM alpine:latest
# 安装 SSL 根证书，网关请求 HTTPS 接口（如 OpenAI）必须要有它
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app/
COPY --from=builder /app/llm-gateway .
# 记得把配置文件也拷进去
COPY config.yaml . 

EXPOSE 8080
CMD ["./llm-gateway"]