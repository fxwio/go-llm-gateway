package middleware

import (
	"fmt"
	"net/http"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/go-redis/redis_rate/v10"
)

// RateLimitMiddleware 基于 Redis Lua 脚本的分布式限流中间件
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 获取客户端 IP (生产环境应使用 r.Header.Get("X-Forwarded-For"))
		clientIP := r.RemoteAddr

		// 2. 构建 Redis 限流 Key (例如: "rate_limit:ip:192.168.1.1")
		limitKey := fmt.Sprintf("rate_limit:ip:%s", clientIP)

		// 3. 从配置中读取 QPS 阈值 (此处使用 PerSecond 规则)
		qps := int(config.GlobalConfig.Auth.RateLimitQPS)
		
		// 4. 执行 Redis 分布式限流判定
		res, err := cache.RateLimiter.Allow(r.Context(), limitKey, redis_rate.PerSecond(qps))
		if err != nil {
			// 如果 Redis 挂了，为了高可用，你可以选择放行 (降级) 或是拦截。这里选择记录错误并拦截
			http.Error(w, "Internal Server Error: Rate limiter unavailable", http.StatusInternalServerError)
			return
		}

		// 5. 将限流状态写入 HTTP 响应头
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", qps))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", res.Remaining))

		if res.Allowed == 0 {
			// 触发限流，返回 429
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(res.RetryAfter.Seconds())))
			http.Error(w, "Too Many Requests - Global Rate Limit Exceeded", http.StatusTooManyRequests)
			return
		}

		// 6. 放行
		next.ServeHTTP(w, r)
	})
}