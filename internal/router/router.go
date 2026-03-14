package router

import (
	"net/http"

	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/proxy"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRouter 初始化全局路由
func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()

	// 顺序：
	// 日志 -> 限流 -> 鉴权 -> 请求体限流&复用 -> 模型路由 -> 指标 -> 缓存 -> 核心代理
	coreEngine := proxy.NewGatewayProxy()
	cachedHandler := middleware.CacheMiddleware(coreEngine)
	metricsHandler := middleware.MetricsMiddleware(cachedHandler)
	routedHandler := middleware.ModelRouterMiddleware(metricsHandler)
	bodyHandler := middleware.BodyContextMiddleware(middleware.DefaultMaxRequestBodyBytes, routedHandler)
	authedHandler := middleware.AuthMiddleware(bodyHandler)
	limitedHandler := middleware.RateLimitMiddleware(authedHandler)
	finalChatHandler := middleware.AccessLogMiddleware(limitedHandler)

	mux.Handle("POST /v1/chat/completions", finalChatHandler)

	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	return mux
}
