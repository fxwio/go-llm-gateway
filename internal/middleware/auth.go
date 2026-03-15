package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/fxwio/go-llm-gateway/internal/response"
	"github.com/fxwio/go-llm-gateway/internal/tenant"
)

type clientAuthContextKey string

const ClientAuthContextKey clientAuthContextKey = "client_auth_ctx"

type ClientAuthContext struct {
	Token           string
	Fingerprint     string
	TokenName       string
	Tenant          string
	App             string
	RateLimitQPS    float64
	RateLimitBurst  int
	DailyTokenLimit int64
	Legacy          bool
}

// AuthMiddleware 校验客户端发来的 Bearer Token 是否合法。
// 鉴权通过后，把 token 身份信息放入 context，供限流、配额和审计复用。
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if authHeader == "" {
			response.WriteOpenAIError(w, http.StatusUnauthorized, "Missing Authorization header.", "authentication_error", nil, response.Ptr("missing_authorization_header"))
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") {
			response.WriteOpenAIError(w, http.StatusUnauthorized, "Invalid Authorization header. Expected 'Bearer '.", "authentication_error", nil, response.Ptr("invalid_authorization_header"))
			return
		}

		clientToken := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if clientToken == "" {
			response.WriteOpenAIError(w, http.StatusUnauthorized, "Missing bearer token.", "authentication_error", nil, response.Ptr("missing_bearer_token"))
			return
		}

		identity, ok := tenant.ResolveToken(clientToken)
		if !ok {
			response.WriteOpenAIError(w, http.StatusForbidden, "Invalid API key provided.", "authentication_error", nil, response.Ptr("invalid_api_key"))
			return
		}

		authCtx := &ClientAuthContext{
			Token:           clientToken,
			Fingerprint:     identity.Fingerprint,
			TokenName:       identity.Name,
			Tenant:          identity.Tenant,
			App:             identity.App,
			RateLimitQPS:    identity.RateLimitQPS,
			RateLimitBurst:  identity.RateLimitBurst,
			DailyTokenLimit: identity.DailyTokenLimit,
			Legacy:          identity.Legacy,
		}
		ctx := context.WithValue(r.Context(), ClientAuthContextKey, authCtx)
		tenant.RecordRequestResult(identity, "accepted")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetClientAuthContext(r *http.Request) (*ClientAuthContext, bool) {
	ctxVal := r.Context().Value(ClientAuthContextKey)
	if ctxVal == nil {
		return nil, false
	}
	authCtx, ok := ctxVal.(*ClientAuthContext)
	if !ok || authCtx == nil {
		return nil, false
	}
	return authCtx, true
}
