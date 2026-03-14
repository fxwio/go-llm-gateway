package middleware

import (
	"net/http"
	"time"

	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

type accessLogRecorder struct {
	http.ResponseWriter
	statusCode int
	bytes      int
}

func (rw *accessLogRecorder) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *accessLogRecorder) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	return n, err
}

func AccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &accessLogRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		if recorder.statusCode == 0 {
			recorder.statusCode = http.StatusOK
		}

		clientIP := extractClientIP(r)
		cacheStatus := recorder.Header().Get("X-Cache")
		rateLimitScope := recorder.Header().Get("X-RateLimit-Scope")

		fields := []zap.Field{
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", recorder.statusCode),
			zap.Int("bytes", recorder.bytes),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
			zap.String("client_ip", clientIP),
			zap.String("user_agent", r.UserAgent()),
		}

		if cacheStatus != "" {
			fields = append(fields, zap.String("cache_status", cacheStatus))
		}
		if rateLimitScope != "" {
			fields = append(fields, zap.String("rate_limit_scope", rateLimitScope))
		}

		if meta, ok := GetRequestMeta(r); ok {
			fields = append(fields,
				zap.String("request_id", meta.RequestID),
				zap.String("trace_id", meta.TraceID),
			)
		}

		logger.Log.Info("HTTP request completed", fields...)
	})
}
