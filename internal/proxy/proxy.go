package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/adapter"
	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/model"
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
					http.Error(
						w,
						"Service Unavailable: Downstream AI provider is temporarily blocked (Circuit Breaker Open)",
						http.StatusServiceUnavailable,
					)
					return
				}

				http.Error(w, "Gateway Proxy Error: "+err.Error(), http.StatusBadGateway)
			},
		}

		gatewayProxy = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gatewayCtx, err := getGatewayContext(r)
			if err != nil {
				http.Error(w, "Gateway context missing", http.StatusInternalServerError)
				return
			}

			if _, err := parseAndCacheBaseURL(gatewayCtx.BaseURL); err != nil {
				http.Error(w, "Invalid target URL", http.StatusInternalServerError)
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
		logger.Log.Error("Gateway context missing in proxy director")
		return
	}

	targetURL, err := parseAndCacheBaseURL(gatewayCtx.BaseURL)
	if err != nil {
		logger.Log.Error("Invalid target URL", zap.String("base_url", gatewayCtx.BaseURL), zap.Error(err))
		return
	}

	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.Host = targetURL.Host

	enrichStreamOptions(req)

	if gatewayCtx.TargetProvider == "anthropic" {
		req.URL.Path = "/v1/messages"
		req.URL.RawPath = req.URL.Path

		req.Header.Set("x-api-key", gatewayCtx.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Del("Authorization")

		if err := adapter.TranslateOpenAIToAnthropic(req); err != nil {
			logger.Log.Error("Failed to translate protocol", zap.Error(err))
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

func enrichStreamOptions(req *http.Request) {
	if req.Body == nil {
		return
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		req.Body = io.NopCloser(bytes.NewBuffer(nil))
		req.ContentLength = 0
		return
	}

	req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	req.ContentLength = int64(len(bodyBytes))

	if !isStreamRequest(bodyBytes) {
		return
	}

	var jsonBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &jsonBody); err != nil {
		return
	}

	streamOptions, ok := jsonBody["stream_options"].(map[string]interface{})
	if !ok || streamOptions == nil {
		streamOptions = make(map[string]interface{})
	}
	if _, exists := streamOptions["include_usage"]; !exists {
		streamOptions["include_usage"] = true
	}
	jsonBody["stream_options"] = streamOptions

	newBodyBytes, err := json.Marshal(jsonBody)
	if err != nil {
		return
	}

	req.Body = io.NopCloser(bytes.NewBuffer(newBodyBytes))
	req.ContentLength = int64(len(newBodyBytes))
	req.Header.Set("Content-Length", strconv.Itoa(len(newBodyBytes)))
}

func isStreamRequest(body []byte) bool {
	return bytes.Contains(body, []byte(`"stream":true`)) ||
		bytes.Contains(body, []byte(`"stream": true`))
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
