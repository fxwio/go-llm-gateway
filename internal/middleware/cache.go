package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/audit"
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
	} else {
		if bytes.Contains(b, []byte(`"usage":`)) {
			rw.extractUsageFromChunk(b)
		}
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
		if err := json.Unmarshal(jsonStr, &llmResp); err == nil {
			if llmResp.Usage.TotalTokens > 0 {
				rw.usageMu.Lock()
				rw.finalUsage = &llmResp.Usage
				rw.usageMu.Unlock()
			}
		}
	}
}

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

		bodyBytes := bodyCtx.RawBody
		isStream := bodyCtx.IsStream

		var gCtx *model.GatewayContext
		if ctxVal := r.Context().Value(GatewayContextKey); ctxVal != nil {
			if gatewayCtx, ok := ctxVal.(*model.GatewayContext); ok {
				gCtx = gatewayCtx
			}
		}

		providerLabel := "unknown"
		modelLabel := "unknown"
		if gCtx != nil {
			if gCtx.TargetProvider != "" {
				providerLabel = gCtx.TargetProvider
			}
			if gCtx.TargetModel != "" {
				modelLabel = gCtx.TargetModel
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

		if isStream {
			w.Header().Set("X-Cache", "BYPASS")
			gatewaymetrics.CacheRequestsTotal.WithLabelValues(providerLabel, modelLabel, "bypass").Inc()
		}

		var cacheKey string

		if !isStream {
			hash := sha256.Sum256(bodyBytes)
			cacheKey = "llm_cache:" + hex.EncodeToString(hash[:])

			ctx := context.Background()
			cachedResp, err := cache.RedisClient.Get(ctx, cacheKey).Result()
			if err == nil && cachedResp != "" {
				gatewaymetrics.CacheRequestsTotal.WithLabelValues(providerLabel, modelLabel, "hit").Inc()

				logger.Log.Info("Cache Hit", append([]zap.Field{
					zap.String("key", cacheKey),
				}, logFields...)...)

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Cache", "HIT")
				_, _ = w.Write([]byte(cachedResp))

				if gCtx != nil {
					audit.PushRecord(audit.AuditRecord{
						Timestamp:        time.Now(),
						RequestID:        requestID,
						TraceID:          traceID,
						ClientIP:         clientIP,
						TokenFingerprint: tokenFingerprint,
						Provider:         gCtx.TargetProvider,
						Model:            gCtx.TargetModel,
						PromptTokens:     0,
						CompletionToken:  0,
						TotalTokens:      0,
						CostStatus:       "hit_cache",
					})
				}
				return
			}

			gatewaymetrics.CacheRequestsTotal.WithLabelValues(providerLabel, modelLabel, "miss").Inc()

			if err != nil && err != redis.Nil {
				logger.Log.Warn("Redis get error", append([]zap.Field{
					zap.Error(err),
				}, logFields...)...)
			}

			logger.Log.Info("Cache Miss, forwarding request...", append([]zap.Field{
				zap.String("key", cacheKey),
			}, logFields...)...)

			w.Header().Set("X-Cache", "MISS")
		}

		recorder := &responseRecorder{
			ResponseWriter:   w,
			body:             &bytes.Buffer{},
			statusCode:       http.StatusOK,
			isStream:         isStream,
			gatewayCtx:       gCtx,
			clientIP:         clientIP,
			requestID:        requestID,
			traceID:          traceID,
			tokenFingerprint: tokenFingerprint,
		}

		next.ServeHTTP(recorder, r)

		if isStream {
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

		if recorder.statusCode == http.StatusOK {
			respBytes := append([]byte(nil), recorder.body.Bytes()...)

			go func(cacheKey string, payload []byte, requestID string, traceID string) {
				err := cache.RedisClient.Set(context.Background(), cacheKey, payload, 24*time.Hour).Err()
				if err != nil {
					logger.Log.Error("Failed to save cache",
						zap.Error(err),
						zap.String("request_id", requestID),
						zap.String("trace_id", traceID),
					)
				}
			}(cacheKey, respBytes, requestID, traceID)

			go func(payload []byte, gatewayCtx *model.GatewayContext, clientIP string, requestID string, traceID string, tokenFingerprint string) {
				if gatewayCtx == nil {
					return
				}

				var llmResp model.OpenAIResponse
				if err := json.Unmarshal(payload, &llmResp); err == nil {
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
						CostStatus:       "api_call",
					})
				} else {
					logger.Log.Error("Audit JSON Unmarshal Failed",
						zap.Error(err),
						zap.String("request_id", requestID),
						zap.String("trace_id", traceID),
					)
				}
			}(respBytes, gCtx, clientIP, requestID, traceID, tokenFingerprint)
		}
	})
}

func (rw *responseRecorder) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
