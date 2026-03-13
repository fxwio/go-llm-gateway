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

	// 顺序：日志 -> 限流 -> 鉴权 -> 缓存 -> 路由拦截 -> 核心代理引擎
	coreEngine := proxy.NewGatewayProxy()
	cachedHandler := middleware.CacheMiddleware(coreEngine)
	routedHandler := middleware.ModelRouterMiddleware(cachedHandler)
	authedHandler := middleware.AuthMiddleware(routedHandler)
	limitedHandler := middleware.RateLimitMiddleware(authedHandler)
	finalChatHandler := middleware.AccessLogMiddleware(limitedHandler)

	// 注册符合 OpenAI 标准的路由路径
	mux.Handle("POST /v1/chat/completions", finalChatHandler)

	// metrics
	mux.Handle("/metrics", promhttp.Handler())

	// 健康检查接口 (不需要走模型路由中间件)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	return mux
}
