package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/metrics"
	"github.com/fxwio/go-llm-gateway/internal/model"
)

// MetricsMiddleware 专门负责拦截并上报 Prometheus QPS 与延迟指标
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 包装 ResponseWriter 以便捕获真实的 HTTP 状态码
		wrappedWriter := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		// 放行给下层中间件（此时会一直阻塞，直到请求结束）
		next.ServeHTTP(wrappedWriter, r)

		// --- 请求彻底结束后，必定会执行到这里 ---
		duration := time.Since(start).Seconds()
		statusStr := strconv.Itoa(wrappedWriter.statusCode)

		// 尝试提取路由上下文
		provider := "unknown"
		targetModel := "unknown"
		if ctxVal := r.Context().Value(GatewayContextKey); ctxVal != nil {
			if gCtx, ok := ctxVal.(*model.GatewayContext); ok {
				provider = gCtx.TargetProvider
				targetModel = gCtx.TargetModel
			}
		}

		cacheStatus := wrappedWriter.Header().Get("X-Cache")
		if cacheStatus == "" {
			cacheStatus = "MISS"
		}

		metrics.RequestTotal.WithLabelValues(provider, targetModel, statusStr, cacheStatus).Inc()
		metrics.RequestDuration.WithLabelValues(provider, targetModel, cacheStatus).Observe(duration)
	})
}
