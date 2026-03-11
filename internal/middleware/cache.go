package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// responseRecorder 包装 ResponseWriter，用于捕获反向代理返回的 Body
type responseRecorder struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (rw *responseRecorder) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	// 把数据复制一份到我们的 buffer 里
	rw.body.Write(b)
	// 原样写回给客户端
	return rw.ResponseWriter.Write(b)
}

// CacheMiddleware 核心缓存逻辑
func CacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 读取请求体用于计算 Hash
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request", http.StatusBadRequest)
			return
		}

		// [资深细节] 恢复请求体，否则后续的路由和反向代理就读不到数据了
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 2. 粗略检查是否是流式请求 (如果包含 "stream": true 则跳过缓存)
		// 生产环境中建议解析 JSON，这里为了极致性能直接做字符串/字节匹配
		if bytes.Contains(bodyBytes, []byte(`"stream": true`)) || bytes.Contains(bodyBytes, []byte(`"stream":true`)) {
			next.ServeHTTP(w, r)
			return
		}

		// 3. 计算请求体的 SHA-256 指纹，作为 Redis Key
		hash := sha256.Sum256(bodyBytes)
		cacheKey := "llm_cache:" + hex.EncodeToString(hash[:])

		// 4. 尝试从 Redis 中获取缓存
		ctx := context.Background()
		cachedResp, err := cache.RedisClient.Get(ctx, cacheKey).Result()

		if err == nil && cachedResp != "" {
			// 🎯 缓存命中 (Cache Hit)！
			logger.Log.Info("Cache Hit", zap.String("key", cacheKey))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT") // 添加高大上的缓存标识
			w.Write([]byte(cachedResp))
			return
		} else if err != redis.Nil {
			logger.Log.Warn("Redis get error", zap.Error(err))
		}

		// 5. 缓存未命中 (Cache Miss)，准备拦截响应
		logger.Log.Info("Cache Miss, forwarding request...", zap.String("key", cacheKey))

		recorder := &responseRecorder{
			ResponseWriter: w,
			body:           &bytes.Buffer{},
			statusCode:     http.StatusOK, // 默认 200
		}

		w.Header().Set("X-Cache", "MISS")

		// 放行给后续的代理引擎
		next.ServeHTTP(recorder, r)

		// 6. 只有当大模型正常返回 200 OK 时，我们才缓存结果，防止缓存报错信息
		if recorder.statusCode == http.StatusOK {
			// 设置 24 小时过期时间 (生产环境可配置)
			err := cache.RedisClient.Set(ctx, cacheKey, recorder.body.Bytes(), 24*time.Hour).Err()
			if err != nil {
				logger.Log.Error("Failed to save cache to Redis", zap.Error(err))
			} else {
				logger.Log.Info("Response cached successfully", zap.String("key", cacheKey))
			}
		}
	})
}
