package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/fxwio/go-llm-gateway/internal/config"
)

func AdminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adminToken := strings.TrimSpace(config.GlobalConfig.Auth.Admin.BearerToken)
		if adminToken == "" {
			http.NotFound(w, r)
			return
		}
		if !isAllowedAdminCIDR(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		provided := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if provided == "" || provided != adminToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAllowedAdminCIDR(r *http.Request) bool {
	cidrs := config.GlobalConfig.Auth.Admin.AllowedCIDRs
	if len(cidrs) == 0 {
		return true
	}
	ip := extractClientIP(r)
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, raw := range cidrs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}
