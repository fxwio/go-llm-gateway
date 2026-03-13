package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/adapter"
	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
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

				if req.Body != nil {
					bodyBytes, _ := io.ReadAll(req.Body)
					if bytes.Contains(bodyBytes, []byte(`"stream": true`)) || bytes.Contains(bodyBytes, []byte(`"stream":true`)) {
						var jsonBody map[string]interface{}
						if err := json.Unmarshal(bodyBytes, &jsonBody); err == nil {
							// 注入 stream_options
							jsonBody["stream_options"] = map[string]interface{}{"include_usage": true}
							newBodyBytes, _ := json.Marshal(jsonBody)
							// 重新塞回 Request
							req.Body = io.NopCloser(bytes.NewBuffer(newBodyBytes))
							req.ContentLength = int64(len(newBodyBytes))
						} else {
							// 解析失败兜底，原样塞回
							req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
						}
					} else {
						// 非流式请求，原样塞回
						req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
					}
				}

				// Anthropic 的请求路径和 OpenAI 不一样，需要重写！
				if gatewayCtx.TargetProvider == "anthropic" {
					req.URL.Path = "/v1/messages"                     // Claude 的标准 API 路径
					req.Header.Set("x-api-key", gatewayCtx.APIKey)    // Claude 的鉴权头不一样
					req.Header.Set("anthropic-version", "2023-06-01") // Claude 必传的头

					// 动态协议翻译
					if err := adapter.TranslateOpenAIToAnthropic(req); err != nil {
						logger.Log.Error("Failed to translate protocol", zap.Error(err))
					}
					// 注意：删掉之前可能设置的 Bearer token，因为 Claude 不用这个
					req.Header.Del("Authorization")
				} else {
					// 其他兼容 OpenAI 标准的厂商保持不变
					req.URL.Path = targetURL.Path + req.URL.Path
					if gatewayCtx.APIKey != "" {
						req.Header.Set("Authorization", "Bearer "+gatewayCtx.APIKey)
					}
				}

				// 删除客户端可能发送的 Accept-Encoding 头。
				// 防止下游服务器返回 Gzip 压缩数据，导致 Go 的反代引擎尝试缓冲整个响应体而破坏 SSE 流式输出。
				req.Header.Del("Accept-Encoding")
			},
			// [资深细节] 生产级 Transport 连接池配置，避免高并发下端口耗尽 (TIME_WAIT)
			Transport: &CircuitBreakerTransport{
				Transport: &http.Transport{
					Proxy:                 http.ProxyFromEnvironment,
					MaxIdleConns:          100,
					MaxIdleConnsPerHost:   100,
					IdleConnTimeout:       90 * time.Second,
					TLSHandshakeTimeout:   10 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
				},
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
				// 针对熔断器直接拦截的情况，给客户端返回友好的 503 Service Unavailable
				if err == gobreaker.ErrOpenState {
					http.Error(w, "Service Unavailable: Downstream AI provider is temporarily blocked (Circuit Breaker Open)", http.StatusServiceUnavailable)
					return
				}
				http.Error(w, "Gateway Proxy Error: "+err.Error(), http.StatusBadGateway)
			},
		}

		// 4. 执行真正的转发 (ServeHTTP 会自动处理 HTTP 连通和分块传输分发)
		proxy.ServeHTTP(w, r)
	})
}
