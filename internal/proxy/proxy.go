package proxy

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/adapter"
	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/internal/response"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
)

var (
	gatewayProxyOnce sync.Once
	gatewayProxy     http.Handler
	baseURLCache     sync.Map // map[string]*url.URL
)

func NewGatewayProxy() http.Handler {
	gatewayProxyOnce.Do(func() {
		sharedTransport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          512,
			MaxIdleConnsPerHost:   128,
			MaxConnsPerHost:       256,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}

		reverseProxy := &httputil.ReverseProxy{
			Director: proxyDirector,
			Transport: &CircuitBreakerTransport{
				Transport: sharedTransport,
			},
			ModifyResponse: func(resp *http.Response) error {
				resp.Header.Del("Server")
				resp.Header.Set("Access-Control-Allow-Origin", "*")
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				if err == gobreaker.ErrOpenState {
					response.WriteOpenAIError(
						w,
						http.StatusServiceUnavailable,
						"Downstream AI provider is temporarily unavailable.",
						"server_error",
						nil,
						response.Ptr("circuit_breaker_open"),
					)
					return
				}

				response.WriteOpenAIError(
					w,
					http.StatusBadGateway,
					"Gateway proxy error: "+err.Error(),
					"server_error",
					nil,
					response.Ptr("bad_gateway"),
				)
			},
		}

		gatewayProxy = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gatewayCtx, err := getGatewayContext(r)
			if err != nil {
				response.WriteOpenAIError(
					w,
					http.StatusInternalServerError,
					"Gateway context missing.",
					"server_error",
					nil,
					response.Ptr("missing_gateway_context"),
				)
				return
			}

			if _, err := parseAndCacheBaseURL(gatewayCtx.BaseURL); err != nil {
				response.WriteOpenAIError(
					w,
					http.StatusInternalServerError,
					"Invalid target URL.",
					"server_error",
					nil,
					response.Ptr("invalid_target_url"),
				)
				return
			}

			reverseProxy.ServeHTTP(w, r)
		})
	})

	return gatewayProxy
}

func proxyDirector(req *http.Request) {
	gatewayCtx, err := getGatewayContext(req)
	if err != nil {
		logger.Log.Error("Gateway context missing in proxy director", requestMetaFields(req)...)
		return
	}

	targetURL, err := parseAndCacheBaseURL(gatewayCtx.BaseURL)
	if err != nil {
		fields := append([]zap.Field{
			zap.String("base_url", gatewayCtx.BaseURL),
			zap.Error(err),
		}, requestMetaFields(req)...)
		logger.Log.Error("Invalid target URL", fields...)
		return
	}

	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.Host = targetURL.Host

	if meta, ok := middleware.GetRequestMeta(req); ok {
		req.Header.Set("X-Request-ID", meta.RequestID)
		req.Header.Set("Traceparent", meta.TraceParent)
		if meta.TraceState != "" {
			req.Header.Set("Tracestate", meta.TraceState)
		}
	}

	// 不再在 proxy 层二次读取和改写 body。
	// stream_options.include_usage 的注入已经前移到 BodyContextMiddleware。
	if gatewayCtx.TargetProvider == "anthropic" {
		req.URL.Path = "/v1/messages"
		req.URL.RawPath = req.URL.Path

		req.Header.Set("x-api-key", gatewayCtx.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Del("Authorization")

		if err := adapter.TranslateOpenAIToAnthropic(req); err != nil {
			fields := append([]zap.Field{
				zap.Error(err),
			}, requestMetaFields(req)...)
			logger.Log.Error("Failed to translate protocol", fields...)
		}
	} else {
		req.URL.Path = joinURLPath(targetURL.Path, req.URL.Path)
		req.URL.RawPath = req.URL.Path

		if gatewayCtx.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+gatewayCtx.APIKey)
		}
	}

	req.Header.Del("Accept-Encoding")
}

func getGatewayContext(r *http.Request) (*model.GatewayContext, error) {
	ctxVal := r.Context().Value(middleware.GatewayContextKey)
	if ctxVal == nil {
		return nil, http.ErrNoCookie
	}

	gatewayCtx, ok := ctxVal.(*model.GatewayContext)
	if !ok || gatewayCtx == nil {
		return nil, http.ErrNoCookie
	}

	return gatewayCtx, nil
}

func parseAndCacheBaseURL(raw string) (*url.URL, error) {
	if v, ok := baseURLCache.Load(raw); ok {
		return v.(*url.URL), nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	baseURLCache.Store(raw, parsed)
	return parsed, nil
}

func joinURLPath(basePath, reqPath string) string {
	switch {
	case basePath == "":
		if reqPath == "" {
			return "/"
		}
		return reqPath
	case reqPath == "":
		return basePath
	default:
		return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(reqPath, "/")
	}
}

func requestMetaFields(r *http.Request) []zap.Field {
	if meta, ok := middleware.GetRequestMeta(r); ok {
		return []zap.Field{
			zap.String("request_id", meta.RequestID),
			zap.String("trace_id", meta.TraceID),
		}
	}

	return nil
}
