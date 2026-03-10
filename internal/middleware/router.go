package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/model"
)

// 定义 context key 类型，避免与其他包的 Context Key 冲突 (这是非常重要的最佳实践)
type contextKey string

const GatewayContextKey contextKey = "gateway_ctx"

// ModelRouterMiddleware 负责拦截请求、解析模型、匹配规则并注入 Context
func ModelRouterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 读取请求体 (小心：读完之后 r.Body 就空了)
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		// [资深细节] 2. 把 Body 重新塞回去，让后续的 Handler (比如反向代理) 还能继续读
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 3. 解析 JSON 获取客户端想要调用的 model
		var req model.ChatCompletionRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
			return
		}

		if req.Model == "" {
			http.Error(w, "Missing 'model' field in request", http.StatusBadRequest)
			return
		}

		// 4. 匹配 config.yaml 中的 Provider 路由规则
		var targetProvider *config.ProviderConfig
		for _, p := range config.GlobalConfig.Providers {
			for _, m := range p.Models {
				if m == req.Model {
					targetProvider = &p
					break
				}
			}
			if targetProvider != nil {
				break
			}
		}

		// 如果没有匹配到支持的模型，直接拒绝
		if targetProvider == nil {
			http.Error(w, "Model not supported or routed", http.StatusNotFound)
			return
		}

		// 5. 将核心路由信息打包进 GatewayContext
		ctxVal := &model.GatewayContext{
			TargetProvider: targetProvider.Name,
			TargetModel:    req.Model,
			APIKey:         targetProvider.APIKey,
			BaseURL:        targetProvider.BaseURL,
		}

		// 6. 将自定义信息注入到标准的 HTTP Context 中
		ctx := context.WithValue(r.Context(), GatewayContextKey, ctxVal)

		// 7. 带着全新的 Context，放行给下一个中间件或最终的代理引擎
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
