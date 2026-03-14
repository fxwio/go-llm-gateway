package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/internal/response"
)

// 定义 context key 类型，避免与其他包的 Context Key 冲突
type contextKey string

const GatewayContextKey contextKey = "gateway_ctx"

// ModelRouterMiddleware 负责解析请求中的 model，并注入 GatewayContext。
func ModelRouterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			response.WriteOpenAIError(
				w,
				http.StatusInternalServerError,
				"Request body context missing.",
				"server_error",
				nil,
				response.Ptr("missing_body_context"),
			)
			return
		}

		var req model.ChatCompletionRequest
		if err := json.Unmarshal(bodyCtx.RawBody, &req); err != nil {
			response.WriteOpenAIError(
				w,
				http.StatusBadRequest,
				"Invalid JSON payload.",
				"invalid_request_error",
				nil,
				response.Ptr("invalid_json"),
			)
			return
		}

		if req.Model == "" {
			response.WriteOpenAIError(
				w,
				http.StatusBadRequest,
				"Missing required field: model.",
				"invalid_request_error",
				response.Ptr("model"),
				response.Ptr("missing_required_field"),
			)
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
			response.WriteOpenAIError(
				w,
				http.StatusNotFound,
				"The model '"+req.Model+"' does not exist or is not available on this gateway.",
				"invalid_request_error",
				response.Ptr("model"),
				response.Ptr("model_not_found"),
			)
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
