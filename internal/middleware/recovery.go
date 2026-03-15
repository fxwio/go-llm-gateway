package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/fxwio/go-llm-gateway/internal/response"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

// RecoveryMiddleware 兜底捕获 handler panic，避免单个请求把整个网关进程打崩。
// 这里返回 OpenAI 风格错误，保证客户端拿到稳定的协议响应。
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			requestID := ""
			traceID := ""
			if meta, ok := GetRequestMeta(r); ok {
				requestID = meta.RequestID
				traceID = meta.TraceID
				if requestID != "" && w.Header().Get("X-Request-ID") == "" {
					w.Header().Set("X-Request-ID", requestID)
				}
			}

			logger.Log.Error("request panic recovered",
				zap.String("request_id", requestID),
				zap.String("trace_id", traceID),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("panic", fmt.Sprint(recovered)),
				zap.ByteString("stack", debug.Stack()),
			)

			response.WriteOpenAIError(
				w,
				http.StatusInternalServerError,
				"Internal server error.",
				"server_error",
				nil,
				response.Ptr("internal_server_error"),
			)
		}()

		next.ServeHTTP(w, r)
	})
}
