package middleware

import (
	"net/http"
	"strings"

	"github.com/fxwio/go-llm-gateway/internal/config"
)

// AuthMiddleware 校验客户端发来的 Bearer Token 是否合法
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized: Missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}

		// 提取 Token (去掉 "Bearer " 前缀)
		clientToken := strings.TrimPrefix(authHeader, "Bearer ")

		// 校验 Token 是否在配置的白名单中
		isValid := false
		for _, validToken := range config.GlobalConfig.Auth.ValidTokens {
			if clientToken == validToken {
				isValid = true
				break
			}
		}

		if !isValid {
			http.Error(w, "Forbidden: Invalid API Key", http.StatusForbidden)
			return
		}

		// 鉴权通过，放行
		next.ServeHTTP(w, r)
	})
}
