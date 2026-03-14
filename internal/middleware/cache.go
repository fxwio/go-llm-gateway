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
	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"github.com/fxwio/go-llm-gateway/pkg/redact"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// responseRecorder 包装 ResponseWriter，用于拦截反向代理返回的数据
type responseRecorder struct {
	http.ResponseWriter
	body             *bytes.Buffer
	statusCode       int
	isStream         bool
	gatewayCtx       *model.GatewayContext
	clientIP         string
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

// extractUsageFromChunk 解析 SSE 数据块并更新最新账单
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

// CacheMiddleware 核心缓存与审计计费中间件
func CacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			http.Error(w, "Request body context missing", http.StatusInternalServerError)
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

		clientIP := r.RemoteAddr
		tokenFingerprint := redact.TokenFingerprint(r.Header.Get("Authorization"))

		var cacheKey string

		if !isStream {
			hash := sha256.Sum256(bodyBytes)
			cacheKey = "llm_cache:" + hex.EncodeToString(hash[:])

			ctx := context.Background()
			cachedResp, err := cache.RedisClient.Get(ctx, cacheKey).Result()
			if err == nil && cachedResp != "" {
				logger.Log.Info("Cache Hit", zap.String("key", cacheKey))

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Cache", "HIT")
				_, _ = w.Write([]byte(cachedResp))

				if gCtx != nil {
					audit.PushRecord(audit.AuditRecord{
						Timestamp:        time.Now(),
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

			if err != nil && err != redis.Nil {
				logger.Log.Warn("Redis get error", zap.Error(err))
			}

			logger.Log.Info("Cache Miss, forwarding request...", zap.String("key", cacheKey))
		}

		recorder := &responseRecorder{
			ResponseWriter:   w,
			body:             &bytes.Buffer{},
			statusCode:       http.StatusOK,
			isStream:         isStream,
			gatewayCtx:       gCtx,
			clientIP:         clientIP,
			tokenFingerprint: tokenFingerprint,
		}

		if !isStream {
			w.Header().Set("X-Cache", "MISS")
		}

		next.ServeHTTP(recorder, r)

		if isStream {
			if recorder.finalUsage != nil && recorder.gatewayCtx != nil {
				audit.PushRecord(audit.AuditRecord{
					Timestamp:        time.Now(),
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

			go func(cacheKey string, payload []byte) {
				err := cache.RedisClient.Set(context.Background(), cacheKey, payload, 24*time.Hour).Err()
				if err != nil {
					logger.Log.Error("Failed to save cache", zap.Error(err))
				}
			}(cacheKey, respBytes)

			go func(payload []byte, gatewayCtx *model.GatewayContext, clientIP string, tokenFingerprint string) {
				if gatewayCtx == nil {
					return
				}

				var llmResp model.OpenAIResponse
				if err := json.Unmarshal(payload, &llmResp); err == nil {
					audit.PushRecord(audit.AuditRecord{
						Timestamp:        time.Now(),
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
					logger.Log.Error("Audit JSON Unmarshal Failed", zap.Error(err))
				}
			}(respBytes, gCtx, clientIP, tokenFingerprint)
		}
	})
}

func (rw *responseRecorder) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
