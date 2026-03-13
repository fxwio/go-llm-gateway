package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/audit"
	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// responseRecorder 包装 ResponseWriter，用于拦截反向代理返回的数据
type responseRecorder struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
	isStream   bool
	gatewayCtx *model.GatewayContext
	clientIP   string
	authToken  string

	usageMu    sync.Mutex
	finalUsage *model.Usage
}

func (rw *responseRecorder) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	if !rw.isStream {
		// 非流式：全量存入 buffer 用于做 Redis 缓存和老计费
		rw.body.Write(b)
	} else {
		// 流式请求：不存全量 body，而是实时嗅探每一块数据
		if bytes.Contains(b, []byte(`"usage":`)) {
			// 同步解析提取 usage，不断覆盖暂存最新值
			rw.extractUsageFromChunk(b)
		}
	}
	// 毫无延迟地原样写回给客户端
	return rw.ResponseWriter.Write(b)
}

// extractUsageFromChunk 解析 SSE 数据块并更新最新账单
func (rw *responseRecorder) extractUsageFromChunk(chunk []byte) {
	lines := bytes.Split(chunk, []byte("\n"))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("data: ")) {
			jsonStr := bytes.TrimPrefix(line, []byte("data: "))

			if string(jsonStr) == "[DONE]" {
				continue
			}

			var llmResp model.OpenAIResponse
			// 忽略解析错误，因为有些 chunk 可能不是完整的 JSON
			if err := json.Unmarshal(jsonStr, &llmResp); err == nil {
				if llmResp.Usage.TotalTokens > 0 {
					rw.usageMu.Lock()
					// 🚀 核心逻辑：不断覆盖，永远只保留最后一次（最大的）账单！
					rw.finalUsage = &llmResp.Usage
					rw.usageMu.Unlock()
				}
			}
		}
	}
}

// CacheMiddleware 核心缓存与审计计费中间件
func CacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 读取请求体
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request", http.StatusBadRequest)
			return
		}
		// 恢复请求体，防止下游读不到
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 2. 检查是否是流式请求
		isStream := bytes.Contains(bodyBytes, []byte(`"stream": true`)) || bytes.Contains(bodyBytes, []byte(`"stream":true`))

		// 提前提取 Context (此时已被 Router 解析并注入)
		ctxVal := r.Context().Value(GatewayContextKey)
		var gCtx *model.GatewayContext
		if ctxVal != nil {
			gCtx = ctxVal.(*model.GatewayContext)
		}

		var cacheKey string

		// 3. 【只有非流式】才去计算 Hash 并查 Redis 缓存
		if !isStream {
			hash := sha256.Sum256(bodyBytes)
			cacheKey = "llm_cache:" + hex.EncodeToString(hash[:])

			ctx := context.Background()
			cachedResp, err := cache.RedisClient.Get(ctx, cacheKey).Result()

			if err == nil && cachedResp != "" {
				// 🎯 缓存命中，直接返回，触发 0 元计费审计
				logger.Log.Info("Cache Hit", zap.String("key", cacheKey))
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Cache", "HIT")
				w.Write([]byte(cachedResp))

				if gCtx != nil {
					audit.PushRecord(audit.AuditRecord{
						Timestamp:       time.Now(),
						ClientIP:        r.RemoteAddr,
						Token:           r.Header.Get("Authorization"),
						Provider:        gCtx.TargetProvider,
						Model:           gCtx.TargetModel,
						PromptTokens:    0, // 缓存命中不耗费 API Token
						CompletionToken: 0,
						TotalTokens:     0,
						CostStatus:      "hit_cache",
					})
				}
				return
			} else if err != redis.Nil {
				logger.Log.Warn("Redis get error", zap.Error(err))
			}
			logger.Log.Info("Cache Miss, forwarding request...", zap.String("key", cacheKey))
		}

		// 4. 【核心重构】：无论流式还是非流式，都要包装 recorder
		recorder := &responseRecorder{
			ResponseWriter: w,
			body:           &bytes.Buffer{},
			statusCode:     http.StatusOK,
			isStream:       isStream, // 告诉 recorder 它的真实身份
			gatewayCtx:     gCtx,
			clientIP:       r.RemoteAddr,
			authToken:      r.Header.Get("Authorization"),
		}

		if !isStream {
			w.Header().Set("X-Cache", "MISS")
		}

		// 5. 放行给后续路由和核心代理
		// 【重要】：这里会一直阻塞，直到请求彻底结束（流式打字结束，或非流式全部返回）！
		next.ServeHTTP(recorder, r)

		// 6. 后置处理与结算 (此时请求已经彻底结束)
		if isStream {
			// 🚀 流式结算：把暂存的最后一条（最大的）账单发出去，全过程只发这一次！
			if recorder.finalUsage != nil && recorder.gatewayCtx != nil {
				audit.PushRecord(audit.AuditRecord{
					Timestamp:       time.Now(),
					ClientIP:        recorder.clientIP,
					Token:           recorder.authToken,
					Provider:        recorder.gatewayCtx.TargetProvider,
					Model:           recorder.gatewayCtx.TargetModel,
					PromptTokens:    recorder.finalUsage.PromptTokens,
					CompletionToken: recorder.finalUsage.CompletionTokens,
					TotalTokens:     recorder.finalUsage.TotalTokens,
					CostStatus:      "stream_api_call",
				})
			}
		} else if recorder.statusCode == http.StatusOK {
			// 🚀 非流式结算：写 Redis 缓存 + 解析完整 JSON 算账
			respBytes := recorder.body.Bytes()

			// 异步写缓存
			go func() {
				err := cache.RedisClient.Set(context.Background(), cacheKey, respBytes, 24*time.Hour).Err()
				if err != nil {
					logger.Log.Error("Failed to save cache", zap.Error(err))
				}
			}()

			// 异步计费
			go func() {
				if gCtx == nil {
					return
				}
				var llmResp model.OpenAIResponse
				if err := json.Unmarshal(respBytes, &llmResp); err == nil {
					audit.PushRecord(audit.AuditRecord{
						Timestamp:       time.Now(),
						ClientIP:        r.RemoteAddr,
						Token:           r.Header.Get("Authorization"),
						Provider:        gCtx.TargetProvider,
						Model:           gCtx.TargetModel,
						PromptTokens:    llmResp.Usage.PromptTokens,
						CompletionToken: llmResp.Usage.CompletionTokens,
						TotalTokens:     llmResp.Usage.TotalTokens,
						CostStatus:      "api_call",
					})
				} else {
					logger.Log.Error("Audit JSON Unmarshal Failed", zap.Error(err))
				}
			}()
		}
	})
}
