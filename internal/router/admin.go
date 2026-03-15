package router

import (
	"net/http"
	"strings"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/tenant"
)

type adminTokensResponse struct {
	Time   time.Time              `json:"time"`
	Tokens []adminTokenDescriptor `json:"tokens"`
}

type adminTokenDescriptor struct {
	Name            string  `json:"name"`
	Tenant          string  `json:"tenant"`
	App             string  `json:"app"`
	Fingerprint     string  `json:"fingerprint"`
	RateLimitQPS    float64 `json:"rate_limit_qps,omitempty"`
	RateLimitBurst  int     `json:"rate_limit_burst,omitempty"`
	DailyTokenLimit int64   `json:"daily_token_limit,omitempty"`
	Legacy          bool    `json:"legacy"`
}

type adminUsageResponse struct {
	Time  time.Time              `json:"time"`
	Date  string                 `json:"date"`
	Usage []tenant.UsageSnapshot `json:"usage"`
}

func registerAdminRoutes(mux *http.ServeMux) {
	if config.GlobalConfig == nil || strings.TrimSpace(config.GlobalConfig.Auth.Admin.BearerToken) == "" {
		return
	}

	tokensHandler := middleware.AdminAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identities := tenant.ListTokens()
		tokens := make([]adminTokenDescriptor, 0, len(identities))
		for _, identity := range identities {
			tokens = append(tokens, adminTokenDescriptor{
				Name:            identity.Name,
				Tenant:          identity.Tenant,
				App:             identity.App,
				Fingerprint:     identity.Fingerprint,
				RateLimitQPS:    identity.RateLimitQPS,
				RateLimitBurst:  identity.RateLimitBurst,
				DailyTokenLimit: identity.DailyTokenLimit,
				Legacy:          identity.Legacy,
			})
		}
		writeJSON(w, http.StatusOK, adminTokensResponse{Time: time.Now().UTC(), Tokens: tokens})
	}))

	usageHandler := middleware.AdminAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		writeJSON(w, http.StatusOK, adminUsageResponse{Time: now, Date: now.Format("2006-01-02"), Usage: tenant.ListTodayUsage()})
	}))

	mux.Handle("GET /admin/v1/tokens", tokensHandler)
	mux.Handle("GET /admin/v1/usage", usageHandler)
}
