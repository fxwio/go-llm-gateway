package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/audit"
	"github.com/fxwio/go-llm-gateway/internal/config"
	gatewaymetrics "github.com/fxwio/go-llm-gateway/internal/metrics"
	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/internal/response"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"github.com/fxwio/go-llm-gateway/pkg/redact"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type responseRecorder struct {
	http.ResponseWriter
	body             *bytes.Buffer
	statusCode       int
	isStream         bool
	gatewayCtx       *model.GatewayContext
	clientIP         string
	requestID        string
	traceID          string
	tokenFingerprint string

	usageMu    sync.Mutex
	finalUsage *model.Usage
}

func (rw *responseRecorder) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	if !rw.isStream {
		rw.body.Write(b)
	} else if bytes.Contains(b, []byte(`"usage":`)) {
		rw.extractUsageFromChunk(b)
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseRecorder) extractUsageFromChunk(chunk []byte) {
	lines := bytes.Split(chunk, []byte("\n"))
	for _, line := range lines {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		jsonStr := bytes.TrimPrefix(line, []byte("data: "))
		if string(jsonStr) == "[DONE]" {
			continue
		}

		var llmResp model.OpenAIResponse
		if err := json.Unmarshal(jsonStr, &llmResp); err != nil {
			continue
		}

		if llmResp.Usage.TotalTokens > 0 {
			rw.usageMu.Lock()
			rw.finalUsage = &llmResp.Usage
			rw.usageMu.Unlock()
		}
	}
}

func (rw *responseRecorder) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type recordedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type bufferedResponseWriter struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{
		header: make(http.Header),
	}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) WriteHeader(code int) {
	w.statusCode = code
}

func (w *bufferedResponseWriter) Write(b []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.body.Write(b)
}

type inflightResponse struct {
	done chan struct{}
	resp *recordedResponse
	err  error
}

type responseCoalescer struct {
	mu    sync.Mutex
	calls map[string]*inflightResponse
}

func newResponseCoalescer() *responseCoalescer {
	return &responseCoalescer{
		calls: make(map[string]*inflightResponse),
	}
}

func (c *responseCoalescer) Do(
	ctx context.Context,
	key string,
	fn func() (*recordedResponse, error),
) (*recordedResponse, bool, error) {
	c.mu.Lock()
	if call, ok := c.calls[key]; ok {
		c.mu.Unlock()

		select {
		case <-call.done:
			return cloneRecordedResponse(call.resp), true, call.err
		case <-ctx.Done():
			return nil, true, ctx.Err()
		}
	}

	call := &inflightResponse{done: make(chan struct{})}
	c.calls[key] = call
	c.mu.Unlock()

	resp, err := fn()

	c.mu.Lock()
	call.resp = cloneRecordedResponse(resp)
	call.err = err
	close(call.done)
	delete(c.calls, key)
	c.mu.Unlock()

	return cloneRecordedResponse(resp), false, err
}

var nonStreamMissCoalescer = newResponseCoalescer()

func CacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
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

		gatewayCtx := getGatewayContextFromRequest(r)

		providerLabel := "unknown"
		modelLabel := "unknown"
		if gatewayCtx != nil {
			if gatewayCtx.TargetProvider != "" {
				providerLabel = gatewayCtx.TargetProvider
			}
			if gatewayCtx.TargetModel != "" {
				modelLabel = gatewayCtx.TargetModel
			}
		}

		clientIP := extractClientIP(r)
		tokenFingerprint := redact.TokenFingerprint(r.Header.Get("Authorization"))

		var requestID, traceID string
		if meta, ok := GetRequestMeta(r); ok {
			requestID = meta.RequestID
			traceID = meta.TraceID
		}

		logFields := []zap.Field{
			zap.String("request_id", requestID),
			zap.String("trace_id", traceID),
		}

		if bodyCtx.IsStream || !config.GlobalConfig.Cache.Enabled {
			w.Header().Set("X-Cache", "BYPASS")
			gatewaymetrics.CacheRequestsTotal.WithLabelValues(providerLabel, modelLabel, "bypass").Inc()

			servePassThrough(
				w,
				r,
				next,
				gatewayCtx,
				clientIP,
				requestID,
				traceID,
				tokenFingerprint,
				false,
				false,
				"",
			)
			return
		}

		cacheKey := buildLLMCacheKey(gatewayCtx, bodyCtx.UpstreamBody)
		cacheReadable := cache.RedisAvailable()

		if cacheReadable {
			cachedResp, err := cache.RedisClient.Get(context.Background(), cacheKey).Bytes()
			switch {
			case err == nil && len(cachedResp) > 0:
				cache.ReportRedisSuccess()
				gatewaymetrics.CacheRequestsTotal.WithLabelValues(providerLabel, modelLabel, "hit").Inc()

				logger.Log.Info("Cache Hit",
					append([]zap.Field{
						zap.String("key", cacheKey),
					}, logFields...)...,
				)

				writeCachedHitResponse(w, cachedResp)
				emitHitCacheAudit(gatewayCtx, clientIP, requestID, traceID, tokenFingerprint)
				return

			case err == redis.Nil:
				cache.ReportRedisSuccess()
				logger.Log.Info("Cache Miss, forwarding request...",
					append([]zap.Field{
						zap.String("key", cacheKey),
					}, logFields...)...,
				)

			default:
				cache.ReportRedisFailure(err)
				cacheReadable = false

				logger.Log.Warn("Redis get error, switching to in-process coalescing only",
					append([]zap.Field{
						zap.Error(err),
						zap.String("key", cacheKey),
					}, logFields...)...,
				)
			}
		} else {
			logger.Log.Warn("Redis unavailable, switching to in-process coalescing only", logFields...)
		}

		resp, shared, err := executeNonStreamRequest(
			r,
			bodyCtx,
			next,
			cacheKey,
			config.GlobalConfig.Cache.CoalesceEnabled,
		)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}

			logger.Log.Error("Non-stream shared execution failed",
				append([]zap.Field{zap.Error(err)}, logFields...)...,
			)

			response.WriteOpenAIError(
				w,
				http.StatusBadGateway,
				"Gateway proxy error: "+err.Error(),
				"server_error",
				nil,
				response.Ptr("bad_gateway"),
			)
			return
		}

		cacheHeader := "MISS"
		cacheResult := "miss"
		if shared {
			cacheHeader = "MISS-SHARED"
			cacheResult = "shared"
		}

		gatewaymetrics.CacheRequestsTotal.WithLabelValues(providerLabel, modelLabel, cacheResult).Inc()
		writeRecordedResponse(w, resp, cacheHeader)

		if shared {
			emitSharedAudit(gatewayCtx, clientIP, requestID, traceID, tokenFingerprint)
			return
		}

		emitNonStreamAudit(resp.Body, gatewayCtx, clientIP, requestID, traceID, tokenFingerprint, "api_call")

		if resp.StatusCode != http.StatusOK || !cacheReadable {
			return
		}

		if len(resp.Body) > config.GlobalConfig.Cache.MaxPayloadBytes {
			logger.Log.Info("Skip cache store because payload is larger than cache.max_payload_bytes",
				append([]zap.Field{
					zap.String("key", cacheKey),
					zap.Int("payload_bytes", len(resp.Body)),
					zap.Int("cache_max_payload_bytes", config.GlobalConfig.Cache.MaxPayloadBytes),
				}, logFields...)...,
			)
			return
		}

		storeCacheAsync(cacheKey, resp.Body, requestID, traceID)
	})
}

func servePassThrough(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	gatewayCtx *model.GatewayContext,
	clientIP string,
	requestID string,
	traceID string,
	tokenFingerprint string,
	allowStore bool,
	cacheReadable bool,
	cacheKey string,
) {
	recorder := &responseRecorder{
		ResponseWriter:   w,
		body:             &bytes.Buffer{},
		statusCode:       http.StatusOK,
		isStream:         false,
		gatewayCtx:       gatewayCtx,
		clientIP:         clientIP,
		requestID:        requestID,
		traceID:          traceID,
		tokenFingerprint: tokenFingerprint,
	}

	if bodyCtx, ok := GetRequestBodyContext(r); ok && bodyCtx.IsStream {
		recorder.isStream = true
	}

	next.ServeHTTP(recorder, r)

	if recorder.isStream {
		if recorder.finalUsage != nil && recorder.gatewayCtx != nil {
			audit.PushRecord(audit.AuditRecord{
				Timestamp:        time.Now(),
				RequestID:        recorder.requestID,
				TraceID:          recorder.traceID,
				ClientIP:         recorder.clientIP,
				TokenFingerprint: recorder.tokenFingerprint,
				Provider:         recorder.gatewayCtx.TargetProvider,
				Model:            recorder.gatewayCtx.TargetModel,
				PromptTokens:     recorder.finalUsage.PromptTokens,
				CompletionToken:  recorder.finalUsage.CompletionTokens,
				TotalTokens:      recorder.finalUsage.TotalTokens,
				CostStatus:       "stream_api_call",
			})
		}
		return
	}

	if recorder.statusCode != http.StatusOK {
		return
	}

	respBytes := append([]byte(nil), recorder.body.Bytes()...)
	emitNonStreamAudit(
		respBytes,
		gatewayCtx,
		clientIP,
		requestID,
		traceID,
		tokenFingerprint,
		"api_call",
	)

	if allowStore && cacheReadable && cacheKey != "" && len(respBytes) <= config.GlobalConfig.Cache.MaxPayloadBytes {
		storeCacheAsync(cacheKey, respBytes, requestID, traceID)
	}
}

func executeNonStreamRequest(
	r *http.Request,
	bodyCtx *RequestBodyContext,
	next http.Handler,
	cacheKey string,
	coalesceEnabled bool,
) (*recordedResponse, bool, error) {
	if !coalesceEnabled || cacheKey == "" {
		resp, err := executeCapturedResponse(r, bodyCtx, next)
		return resp, false, err
	}

	return nonStreamMissCoalescer.Do(r.Context(), cacheKey, func() (*recordedResponse, error) {
		return executeCapturedResponse(r, bodyCtx, next)
	})
}

func executeCapturedResponse(
	r *http.Request,
	bodyCtx *RequestBodyContext,
	next http.Handler,
) (*recordedResponse, error) {
	bufferedWriter := newBufferedResponseWriter()

	clonedReq := r.Clone(context.WithoutCancel(r.Context()))
	applyRequestBody(clonedReq, bodyCtx.UpstreamBody)

	next.ServeHTTP(bufferedWriter, clonedReq)

	if bufferedWriter.statusCode == 0 {
		bufferedWriter.statusCode = http.StatusOK
	}

	return &recordedResponse{
		StatusCode: bufferedWriter.statusCode,
		Header:     cloneHeader(bufferedWriter.header),
		Body:       append([]byte(nil), bufferedWriter.body.Bytes()...),
	}, nil
}

func buildLLMCacheKey(gatewayCtx *model.GatewayContext, upstreamBody []byte) string {
	hasher := sha256.New()

	if gatewayCtx != nil {
		_, _ = hasher.Write([]byte(gatewayCtx.TargetProvider))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(gatewayCtx.TargetModel))
		_, _ = hasher.Write([]byte{0})
	}

	_, _ = hasher.Write(upstreamBody)

	return "llm_cache:" + hex.EncodeToString(hasher.Sum(nil))
}

func writeCachedHitResponse(w http.ResponseWriter, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "HIT")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func writeRecordedResponse(w http.ResponseWriter, resp *recordedResponse, cacheHeader string) {
	if resp == nil {
		response.WriteOpenAIError(
			w,
			http.StatusBadGateway,
			"Gateway proxy error: empty upstream response.",
			"server_error",
			nil,
			response.Ptr("bad_gateway"),
		)
		return
	}

	copyHeaders(w.Header(), resp.Header)
	if cacheHeader != "" {
		w.Header().Set("X-Cache", cacheHeader)
	}

	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	w.WriteHeader(statusCode)
	if len(resp.Body) > 0 {
		// #nosec G705 -- cached upstream payload is replayed verbatim for proxy semantics; upstream content type headers are restored before writing.
		if _, err := w.Write(resp.Body); err != nil {
			logger.Log.Warn("Failed to write cached response body", zap.Error(err))
		}
	}
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		copiedValues := make([]string, len(values))
		copy(copiedValues, values)
		dst[key] = copiedValues
	}
}

func cloneHeader(src http.Header) http.Header {
	if src == nil {
		return make(http.Header)
	}
	dst := make(http.Header, len(src))
	copyHeaders(dst, src)
	return dst
}

func cloneRecordedResponse(resp *recordedResponse) *recordedResponse {
	if resp == nil {
		return nil
	}
	return &recordedResponse{
		StatusCode: resp.StatusCode,
		Header:     cloneHeader(resp.Header),
		Body:       append([]byte(nil), resp.Body...),
	}
}

func storeCacheAsync(cacheKey string, payload []byte, requestID string, traceID string) {
	cacheTTL, err := time.ParseDuration(config.GlobalConfig.Cache.TTL)
	if err != nil {
		logger.Log.Error(
			"Invalid cache ttl in runtime, skip cache store",
			zap.Error(err),
			zap.String("cache_ttl", config.GlobalConfig.Cache.TTL),
			zap.String("request_id", requestID),
			zap.String("trace_id", traceID),
		)
		return
	}

	payloadCopy := append([]byte(nil), payload...)
	go func() {
		err := cache.RedisClient.Set(context.Background(), cacheKey, payloadCopy, cacheTTL).Err()
		if err != nil {
			cache.ReportRedisFailure(err)
			logger.Log.Error(
				"Failed to save cache",
				zap.Error(err),
				zap.String("request_id", requestID),
				zap.String("trace_id", traceID),
			)
			return
		}

		cache.ReportRedisSuccess()
	}()
}

func getGatewayContextFromRequest(r *http.Request) *model.GatewayContext {
	if ctxVal := r.Context().Value(GatewayContextKey); ctxVal != nil {
		if gatewayCtx, ok := ctxVal.(*model.GatewayContext); ok {
			return gatewayCtx
		}
	}
	return nil
}

func emitHitCacheAudit(
	gatewayCtx *model.GatewayContext,
	clientIP string,
	requestID string,
	traceID string,
	tokenFingerprint string,
) {
	if gatewayCtx == nil {
		return
	}

	audit.PushRecord(audit.AuditRecord{
		Timestamp:        time.Now(),
		RequestID:        requestID,
		TraceID:          traceID,
		ClientIP:         clientIP,
		TokenFingerprint: tokenFingerprint,
		Provider:         gatewayCtx.TargetProvider,
		Model:            gatewayCtx.TargetModel,
		PromptTokens:     0,
		CompletionToken:  0,
		TotalTokens:      0,
		CostStatus:       "hit_cache",
	})
}

func emitSharedAudit(
	gatewayCtx *model.GatewayContext,
	clientIP string,
	requestID string,
	traceID string,
	tokenFingerprint string,
) {
	if gatewayCtx == nil {
		return
	}

	audit.PushRecord(audit.AuditRecord{
		Timestamp:        time.Now(),
		RequestID:        requestID,
		TraceID:          traceID,
		ClientIP:         clientIP,
		TokenFingerprint: tokenFingerprint,
		Provider:         gatewayCtx.TargetProvider,
		Model:            gatewayCtx.TargetModel,
		PromptTokens:     0,
		CompletionToken:  0,
		TotalTokens:      0,
		CostStatus:       "coalesced_shared",
	})
}

func emitNonStreamAudit(
	payload []byte,
	gatewayCtx *model.GatewayContext,
	clientIP string,
	requestID string,
	traceID string,
	tokenFingerprint string,
	costStatus string,
) {
	if gatewayCtx == nil {
		return
	}

	payloadCopy := append([]byte(nil), payload...)
	go func() {
		var llmResp model.OpenAIResponse
		if err := json.Unmarshal(payloadCopy, &llmResp); err != nil {
			logger.Log.Error(
				"Audit JSON Unmarshal Failed",
				zap.Error(err),
				zap.String("request_id", requestID),
				zap.String("trace_id", traceID),
			)
			return
		}

		audit.PushRecord(audit.AuditRecord{
			Timestamp:        time.Now(),
			RequestID:        requestID,
			TraceID:          traceID,
			ClientIP:         clientIP,
			TokenFingerprint: tokenFingerprint,
			Provider:         gatewayCtx.TargetProvider,
			Model:            gatewayCtx.TargetModel,
			PromptTokens:     llmResp.Usage.PromptTokens,
			CompletionToken:  llmResp.Usage.CompletionTokens,
			TotalTokens:      llmResp.Usage.TotalTokens,
			CostStatus:       costStatus,
		})
	}()
}
