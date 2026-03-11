package router

import (
	"net/http"

	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/proxy"
)

// NewRouter 初始化全局路由
func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()

	// 【重点】组装中间件链条 (洋葱模型：从外到内执行)
	// 顺序：日志 -> 限流 -> 鉴权 -> 缓存 -> 路由拦截 -> 核心代理引擎
	coreEngine := proxy.NewGatewayProxy()
	routedHandler := middleware.ModelRouterMiddleware(coreEngine)
	cachedHandler := middleware.CacheMiddleware(routedHandler)
	authedHandler := middleware.AuthMiddleware(cachedHandler)
	rateLimitHandler := middleware.RateLimitMiddleware(authedHandler)
	finalChatHandler := middleware.AccessLogMiddleware(rateLimitHandler)

	// 注册符合 OpenAI 标准的路由路径
	mux.Handle("POST /v1/chat/completions", finalChatHandler)

	// 健康检查接口 (不需要走模型路由中间件)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	return mux
}
