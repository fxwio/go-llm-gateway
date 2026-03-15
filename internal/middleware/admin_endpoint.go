package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/fxwio/go-llm-gateway/internal/config"
)

var (
	adminCIDROnce    sync.Once
	adminCIDRs       []*net.IPNet
	adminCIDRErr     error
	adminLimiterOnce sync.Once
	adminLimiter     *localTokenBucket
)

func getAdminLimiter() *localTokenBucket {
	adminLimiterOnce.Do(func() {
		rps := config.GlobalConfig.Auth.Admin.RateLimitRPS
		burst := config.GlobalConfig.Auth.Admin.RateLimitBurst
		if rps > 0 && burst > 0 {
			adminLimiter = newLocalTokenBucket(rps, burst)
		}
	})
	return adminLimiter
}

func loadAdminAllowedCIDRs() ([]*net.IPNet, error) {
	adminCIDROnce.Do(func() {
		adminCIDRs, adminCIDRErr = parseCIDRs(config.GlobalConfig.Auth.Admin.AllowedCIDRs, "admin")
	})
	return adminCIDRs, adminCIDRErr
}

func isAdminIPAllowed(r *http.Request) bool {
	allowed, err := loadAdminAllowedCIDRs()
	if err != nil || len(allowed) == 0 {
		return len(allowed) == 0 && err == nil
	}
	return ipInCIDRs(extractClientIP(r), allowed)
}

func hasValidAdminBearerToken(r *http.Request) bool {
	expected := strings.TrimSpace(config.GlobalConfig.Auth.Admin.BearerToken)
	if expected == "" {
		return true
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	return token != "" && token == expected
}

func adminProtectionConfigured() bool {
	adminCfg := config.GlobalConfig.Auth.Admin
	return strings.TrimSpace(adminCfg.BearerToken) != "" || len(adminCfg.AllowedCIDRs) > 0
}

// AdminEndpointMiddleware 比 /metrics 更严格：
// 1. 配了 CIDR 就必须命中 CIDR。
// 2. 配了 Bearer Token 就必须携带合法 Token。
// 3. 二者都配置时必须同时满足。
// 4. 未配置任何保护策略时直接返回 404，避免误暴露。
func AdminEndpointMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !adminProtectionConfigured() {
			http.NotFound(w, r)
			return
		}

		if len(config.GlobalConfig.Auth.Admin.AllowedCIDRs) > 0 && !isAdminIPAllowed(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if !hasValidAdminBearerToken(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		limiter := getAdminLimiter()
		if limiter != nil && !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
