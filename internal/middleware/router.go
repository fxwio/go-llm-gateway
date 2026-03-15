package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/internal/response"
)

// 定义 context key 类型，避免与其他包的 Context Key 冲突。
type contextKey string

const GatewayContextKey contextKey = "gateway_ctx"

// ModelRouterMiddleware 负责解析请求中的 model，并注入 GatewayContext。
// 一个模型可以命中多个 provider，最终由 proxy 层做故障切换。
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

		if strings.TrimSpace(req.Model) == "" {
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

		candidates := matchProviders(req.Model)
		if len(candidates) == 0 {
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

		gatewayCtx := &model.GatewayContext{
			TargetModel:        req.Model,
			CandidateProviders: candidates,
		}
		gatewayCtx.SetActiveProvider(candidates[0])

		ctx := context.WithValue(r.Context(), GatewayContextKey, gatewayCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func matchProviders(targetModel string) []model.ProviderRoute {
	providers := make([]model.ProviderRoute, 0, len(config.GlobalConfig.Providers))
	for i := range config.GlobalConfig.Providers {
		provider := config.GlobalConfig.Providers[i]
		for _, supportedModel := range provider.Models {
			if supportedModel != targetModel {
				continue
			}
			providers = append(providers, model.ProviderRoute{
				Name:            provider.Name,
				BaseURL:         provider.BaseURL,
				APIKey:          provider.APIKey,
				Priority:        provider.Priority,
				MaxRetries:      provider.MaxRetries,
				HealthCheckPath: provider.HealthCheckPath,
			})
			break
		}
	}

	sort.SliceStable(providers, func(i, j int) bool {
		if providers[i].Priority == providers[j].Priority {
			return providers[i].Name < providers[j].Name
		}
		return providers[i].Priority < providers[j].Priority
	})
	return providers
}
