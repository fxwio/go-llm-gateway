package middleware

import (
	"net/http"
	"time"

	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

// responseWriterWrapper 用于拦截并记录 HTTP 状态码
type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriterWrapper) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// AccessLogMiddleware 记录每一次请求的耗时和状态
func AccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 包装 ResponseWriter，默认状态码设为 200
		wrappedWriter := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		// 放行给后续的中间件或路由
		next.ServeHTTP(wrappedWriter, r)

		duration := time.Since(start)

		// 打印结构化日志
		logger.Log.Info("Access Log",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("ip", r.RemoteAddr),
			zap.Int("status", wrappedWriter.statusCode),
			zap.Duration("latency", duration),
		)
	})
}
