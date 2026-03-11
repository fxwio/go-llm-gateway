package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/go-redis/redis_rate/v10"
)

// extractClientIP 从请求中提取真实的客户端 IP
func extractClientIP(r *http.Request) string {
	// 1. 优先尝试从 X-Forwarded-For 获取 (应对经过反向代理的情况)
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		// X-Forwarded-For 的格式通常是 "client, proxy1, proxy2"
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}

	// 2. 尝试获取 X-Real-IP
	xrip := r.Header.Get("X-Real-IP")
	if xrip != "" {
		return strings.TrimSpace(xrip)
	}

	// 3. 最后使用 RemoteAddr，但必须剥离端口号！
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// 如果解析失败（通常是因为没有端口），则回退到原始字符串
		return r.RemoteAddr
	}
	return ip
}

// RateLimitMiddleware 基于 Redis Lua 脚本的分布式限流中间件
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 获取干净的客户端 IP
		clientIP := extractClientIP(r)

		// 2. 构建 Redis 限流 Key
		limitKey := fmt.Sprintf("rate_limit:ip:%s", clientIP)

		// 3. 从配置中读取 QPS 阈值
		qps := int(config.GlobalConfig.Auth.RateLimitQPS)

		// 4. 执行 Redis 分布式限流判定
		res, err := cache.RateLimiter.Allow(r.Context(), limitKey, redis_rate.PerSecond(qps))
		if err != nil {
			http.Error(w, "Internal Server Error: Rate limiter unavailable", http.StatusInternalServerError)
			return
		}

		// 5. 将限流状态写入 HTTP 响应头
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", qps))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", res.Remaining))

		if res.Allowed == 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(res.RetryAfter.Seconds())))
			http.Error(w, "Too Many Requests - Global Rate Limit Exceeded", http.StatusTooManyRequests)
			return
		}

		// 6. 放行
		next.ServeHTTP(w, r)
	})
}
