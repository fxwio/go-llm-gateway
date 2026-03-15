package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// LLMProxy 封装了对下游 AI 厂商的代理逻辑
type LLMProxy struct {
	targetURL *url.URL
	proxy     *httputil.ReverseProxy
}

func NewLLMProxy(target string) (*LLMProxy, error) {
	url, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)

	// 自定义 Transport 以优化连接池
	proxy.Transport = &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}

	// 处理代理过程中的错误，防止网关崩溃
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Proxy error: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		if _, writeErr := w.Write([]byte("Bad Gateway: Downstream AI provider is unreachable")); writeErr != nil {
			log.Printf("Proxy error response write failed: %v", writeErr)
		}
	}

	return &LLMProxy{
		targetURL: url,
		proxy:     proxy,
	}, nil
}

// ServeHTTP 实现了 http.Handler 接口
func (p *LLMProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 在此处可以拦截和修改 Request Body (例如：改写 model 字段)
	// r.Host 必须修改，否则某些厂商的 WAF 会拦截
	r.Host = p.targetURL.Host

	// 执行代理，ReverseProxy 会自动处理 SSE 规范中的 Flush 逻辑
	p.proxy.ServeHTTP(w, r)
}
