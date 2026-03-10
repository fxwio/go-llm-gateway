package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/model"
)

// NewGatewayProxy 创建一个动态的反向代理引擎
func NewGatewayProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 从 Context 中获取之前路由中间件注入的厂商信息
		ctxVal := r.Context().Value(middleware.GatewayContextKey)
		if ctxVal == nil {
			http.Error(w, "Gateway context missing", http.StatusInternalServerError)
			return
		}
		gatewayCtx := ctxVal.(*model.GatewayContext)

		// 2. 解析目标厂商的 BaseURL
		targetURL, err := url.Parse(gatewayCtx.BaseURL)
		if err != nil {
			http.Error(w, "Invalid target URL", http.StatusInternalServerError)
			return
		}

		// 3. 构建 ReverseProxy 实例
		proxy := &httputil.ReverseProxy{
			// Director 负责在请求发送到下游之前，对 HTTP 请求进行篡改/重写
			Director: func(req *http.Request) {
				// 重写协议和域名 (例如把 http://localhost 变成 https://api.openai.com)
				req.URL.Scheme = targetURL.Scheme
				req.URL.Host = targetURL.Host

				// [资深细节] 必须重写 Host 头，否则下游的 WAF 或 CDN（如 Cloudflare）会直接拦截请求
				req.Host = targetURL.Host

				// 拼接路径，保留客户端请求的 Path (如 /v1/chat/completions)
				req.URL.Path = targetURL.Path + req.URL.Path

				// 注入目标厂商的 API Key
				if gatewayCtx.APIKey != "" {
					req.Header.Set("Authorization", "Bearer "+gatewayCtx.APIKey)
				}

				// [极其关键的流式处理细节]
				// 删除客户端可能发送的 Accept-Encoding 头。
				// 防止下游服务器返回 Gzip 压缩数据，导致 Go 的反代引擎尝试缓冲整个响应体而破坏 SSE 流式输出。
				req.Header.Del("Accept-Encoding")
			},
			// [资深细节] 生产级 Transport 连接池配置，避免高并发下端口耗尽 (TIME_WAIT)
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,              // 最大空闲连接数
				MaxIdleConnsPerHost:   100,              // 每个 Host 的最大空闲连接数
				IdleConnTimeout:       90 * time.Second, // 空闲连接超时时间
				TLSHandshakeTimeout:   10 * time.Second, // TLS 握手超时
				ExpectContinueTimeout: 1 * time.Second,
			},
			// ModifyResponse 允许我们在拿到大模型响应后、发回给客户端前做点什么
			ModifyResponse: func(resp *http.Response) error {
				// 比如：隐藏下游真实的 Server 信息，提升安全性
				resp.Header.Del("Server")
				// 比如：注入我们自己的跨域 CORS 头
				resp.Header.Set("Access-Control-Allow-Origin", "*")
				return nil
			},
			// 错误处理兜底
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, "Gateway Proxy Error: "+err.Error(), http.StatusBadGateway)
			},
		}

		// 4. 执行真正的转发 (ServeHTTP 会自动处理 HTTP 连通和分块传输分发)
		proxy.ServeHTTP(w, r)
	})
}
