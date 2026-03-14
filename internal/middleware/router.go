package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/model"
)

// 定义 context key 类型，避免与其他包的 Context Key 冲突
type contextKey string

const GatewayContextKey contextKey = "gateway_ctx"

// ModelRouterMiddleware 负责解析请求中的 model，并注入 GatewayContext。
// 这里不再重复读取 r.Body，而是直接复用 BodyContextMiddleware 已经缓存好的 body。
func ModelRouterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			http.Error(w, "Request body context missing", http.StatusInternalServerError)
			return
		}

		var req model.ChatCompletionRequest
		if err := json.Unmarshal(bodyCtx.RawBody, &req); err != nil {
			http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
			return
		}

		if req.Model == "" {
			http.Error(w, "Missing 'model' field in request", http.StatusBadRequest)
			return
		}

		var targetProvider *config.ProviderConfig
		for i := range config.GlobalConfig.Providers {
			p := &config.GlobalConfig.Providers[i]
			for _, m := range p.Models {
				if m == req.Model {
					targetProvider = p
					break
				}
			}
			if targetProvider != nil {
				break
			}
		}

		if targetProvider == nil {
			http.Error(w, "Model not supported or routed", http.StatusNotFound)
			return
		}

		ctxVal := &model.GatewayContext{
			TargetProvider: targetProvider.Name,
			TargetModel:    req.Model,
			APIKey:         targetProvider.APIKey,
			BaseURL:        targetProvider.BaseURL,
		}

		ctx := context.WithValue(r.Context(), GatewayContextKey, ctxVal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
