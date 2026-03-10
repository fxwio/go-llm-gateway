package middleware

import (
	"net/http"
	"sync"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"golang.org/x/time/rate"
)

var (
	// 使用 sync.Map 或带有 RWMutex 的 map 来存储不同 IP 的限流器，保证并发安全
	limiters = make(map[string]*rate.Limiter)
	mu       sync.RWMutex
)

// getVisitorLimiter 动态获取或创建对应 IP 的限流器
func getVisitorLimiter(ip string) *rate.Limiter {
	mu.RLock()
	limiter, exists := limiters[ip]
	mu.RUnlock()

	if !exists {
		mu.Lock()
		defer mu.Unlock()
		// Double-check 避免并发写入
		limiter, exists = limiters[ip]
		if !exists {
			qps := rate.Limit(config.GlobalConfig.Auth.RateLimitQPS)
			burst := config.GlobalConfig.Auth.RateLimitBurst
			limiter = rate.NewLimiter(qps, burst)
			limiters[ip] = limiter
		}
	}
	return limiter
}

// RateLimitMiddleware IP 维度的限流中间件
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 简单提取 IP (生产环境需考虑 X-Forwarded-For)
		ip := r.RemoteAddr

		limiter := getVisitorLimiter(ip)

		if !limiter.Allow() {
			http.Error(w, "Too Many Requests - Rate Limit Exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
