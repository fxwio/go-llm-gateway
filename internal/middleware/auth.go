package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/response"
)

const ClientAuthContextKey contextKey = "client_auth_ctx"

type ClientAuthContext struct {
	Token string
}

// AuthMiddleware 校验客户端发来的 Bearer Token 是否合法。
// 鉴权通过后，把“已验证通过的 token”放入 context，供后续限流等中间件复用。
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if authHeader == "" {
			response.WriteOpenAIError(
				w,
				http.StatusUnauthorized,
				"Missing Authorization header.",
				"authentication_error",
				nil,
				response.Ptr("missing_authorization_header"),
			)
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			response.WriteOpenAIError(
				w,
				http.StatusUnauthorized,
				"Invalid Authorization header. Expected 'Bearer <token>'.",
				"authentication_error",
				nil,
				response.Ptr("invalid_authorization_header"),
			)
			return
		}

		clientToken := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if clientToken == "" {
			response.WriteOpenAIError(
				w,
				http.StatusUnauthorized,
				"Missing bearer token.",
				"authentication_error",
				nil,
				response.Ptr("missing_bearer_token"),
			)
			return
		}

		isValid := false
		for _, validToken := range config.GlobalConfig.Auth.ValidTokens {
			if clientToken == validToken {
				isValid = true
				break
			}
		}
		if !isValid {
			response.WriteOpenAIError(
				w,
				http.StatusForbidden,
				"Invalid API key provided.",
				"authentication_error",
				nil,
				response.Ptr("invalid_api_key"),
			)
			return
		}

		authCtx := &ClientAuthContext{
			Token: clientToken,
		}
		ctx := context.WithValue(r.Context(), ClientAuthContextKey, authCtx)

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
