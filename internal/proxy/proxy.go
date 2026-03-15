package proxy

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/adapter"
	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/internal/response"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

var (
	gatewayProxyOnce sync.Once
	gatewayProxy     http.Handler
	baseURLCache     sync.Map // map[string]*url.URL
)

type gatewayProxyHandler struct {
	client *http.Client
}

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

		transport := &CircuitBreakerTransport{Transport: sharedTransport}
		initUpstreamHealthMonitor(sharedTransport)

		gatewayProxy = &gatewayProxyHandler{
			client: &http.Client{Transport: transport},
		}
	})
	return gatewayProxy
}

func (h *gatewayProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	bodyCtx, ok := middleware.GetRequestBodyContext(r)
	if !ok {
		response.WriteOpenAIError(
			w,
			http.StatusInternalServerError,
			"Request body context missing.",
			"server_error",
			nil,
			response.Ptr("missing_body_context"),
		)
		return
	}

	if len(gatewayCtx.CandidateProviders) == 0 {
		response.WriteOpenAIError(
			w,
			http.StatusServiceUnavailable,
			"No available upstream providers were resolved for this request.",
			"server_error",
			nil,
			response.Ptr("no_route_candidates"),
		)
		return
	}

	var finalErr error
	for providerIndex, provider := range gatewayCtx.CandidateProviders {
		gatewayCtx.SetActiveProvider(provider)
		attemptBudget := effectiveRetryCount(provider) + 1
		for attempt := 1; attempt <= attemptBudget; attempt++ {
			upstreamReq, err := buildUpstreamRequest(r, bodyCtx.UpstreamBody, gatewayCtx)
			if err != nil {
				response.WriteOpenAIError(
					w,
					http.StatusInternalServerError,
					"Failed to build upstream request.",
					"server_error",
					nil,
					response.Ptr("upstream_request_build_failed"),
				)
				return
			}

			resp, err := h.client.Do(upstreamReq)
			markPassiveProbeResult(provider.Name, provider.BaseURL, resp, err)
			if shouldRetryResponse(resp, err) {
				finalErr = err
				if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
				logRetryAttempt(r, gatewayCtx, provider.Name, providerIndex, attemptBudget, attempt, resp, err)
				if attempt < attemptBudget {
					time.Sleep(retryBackoff())
					continue
				}
				break
			}
			if err != nil {
				finalErr = err
				break
			}
			if err := writeUpstreamResponse(w, resp); err != nil {
				finalErr = err
				logger.Log.Error("Failed while streaming upstream response",
					append([]zap.Field{
						zap.String("provider", provider.Name),
						zap.Error(err),
					}, requestMetaFields(r)...)...,
				)
			}
			return
		}

		gatewayCtx.FailoverCount++
		gatewayCtx.AttemptedProviders = append(gatewayCtx.AttemptedProviders, provider.Name)
		logFailover(r, gatewayCtx, provider.Name, providerIndex)
	}

	message := "All configured upstream providers are temporarily unavailable."
	if finalErr != nil {
		message = message + " Last error: " + finalErr.Error()
	}
	response.WriteOpenAIError(
		w,
		http.StatusServiceUnavailable,
		message,
		"server_error",
		nil,
		response.Ptr("all_upstreams_unavailable"),
	)
}

func buildUpstreamRequest(orig *http.Request, upstreamBody []byte, gatewayCtx *model.GatewayContext) (*http.Request, error) {
	targetURL, err := parseAndCacheBaseURL(gatewayCtx.BaseURL)
	if err != nil {
		return nil, err
	}

	requestURL := *orig.URL
	requestURL.Scheme = targetURL.Scheme
	requestURL.Host = targetURL.Host
	requestURL.Path = joinURLPath(targetURL.Path, orig.URL.Path)
	requestURL.RawPath = requestURL.Path

	req, err := http.NewRequestWithContext(orig.Context(), orig.Method, requestURL.String(), bytes.NewReader(upstreamBody))
	if err != nil {
		return nil, err
	}
	req.Header = cloneHeader(orig.Header)
	req.Host = targetURL.Host
	req.ContentLength = int64(len(upstreamBody))

	if meta, ok := middleware.GetRequestMeta(orig); ok {
		req.Header.Set("X-Request-ID", meta.RequestID)
		req.Header.Set("Traceparent", meta.TraceParent)
		if meta.TraceState != "" {
			req.Header.Set("Tracestate", meta.TraceState)
		}
	}

	if gatewayCtx.TargetProvider == "anthropic" {
		req.URL.Path = "/v1/messages"
		req.URL.RawPath = req.URL.Path
		req.Header.Set("x-api-key", gatewayCtx.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Del("Authorization")
		if err := adapter.TranslateOpenAIToAnthropic(req); err != nil {
			return nil, err
		}
	} else if gatewayCtx.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+gatewayCtx.APIKey)
	}

	req.Header.Del("Accept-Encoding")
	return req, nil
}

func writeUpstreamResponse(w http.ResponseWriter, resp *http.Response) error {
	defer resp.Body.Close()
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Del("Server")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(resp.StatusCode)

	writer := &flushWriter{ResponseWriter: w}
	_, err := io.Copy(writer, resp.Body)
	return err
}

func shouldRetryResponse(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	if resp == nil {
		return true
	}
	for _, statusCode := range config.GlobalConfig.Upstream.RetryableStatusCodes {
		if resp.StatusCode == statusCode {
			return true
		}
	}
	return false
}

func effectiveRetryCount(provider model.ProviderRoute) int {
	if provider.MaxRetries > 0 {
		return provider.MaxRetries
	}
	return config.GlobalConfig.Upstream.DefaultMaxRetries
}

func retryBackoff() time.Duration {
	backoff, err := time.ParseDuration(config.GlobalConfig.Upstream.RetryBackoff)
	if err != nil || backoff <= 0 {
		return 200 * time.Millisecond
	}
	return backoff
}

func logRetryAttempt(r *http.Request, gatewayCtx *model.GatewayContext, provider string, providerIndex int, attemptBudget int, attempt int, resp *http.Response, err error) {
	fields := []zap.Field{
		zap.String("provider", provider),
		zap.String("model", gatewayCtx.TargetModel),
		zap.Int("provider_index", providerIndex),
		zap.Int("attempt", attempt),
		zap.Int("attempt_budget", attemptBudget),
		zap.Int("failover_count", gatewayCtx.FailoverCount),
	}
	if resp != nil {
		fields = append(fields, zap.Int("status_code", resp.StatusCode))
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	fields = append(fields, requestMetaFields(r)...)
	logger.Log.Warn("Retrying upstream provider request", fields...)
}

func logFailover(r *http.Request, gatewayCtx *model.GatewayContext, provider string, providerIndex int) {
	logger.Log.Warn("Failing over to next upstream provider",
		append([]zap.Field{
			zap.String("provider", provider),
			zap.String("model", gatewayCtx.TargetModel),
			zap.Int("provider_index", providerIndex),
			zap.Int("failover_count", gatewayCtx.FailoverCount),
			zap.Strings("attempted_providers", gatewayCtx.AttemptedProviders),
		}, requestMetaFields(r)...)...,
	)
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

func cloneHeader(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for key, values := range src {
		copiedValues := make([]string, len(values))
		copy(copiedValues, values)
		dst[key] = copiedValues
	}
	return dst
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if shouldSkipResponseHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func shouldSkipResponseHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

type flushWriter struct {
	http.ResponseWriter
}

func (w *flushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}
