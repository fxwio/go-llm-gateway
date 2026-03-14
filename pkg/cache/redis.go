package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var (
	RedisClient *redis.Client
	RateLimiter *redis_rate.Limiter

	redisHealthy atomic.Bool

	redisStatusMu sync.RWMutex
	redisLastErr  string

	redisMonitorOnce sync.Once
)

type RedisDependencyStatus struct {
	Enabled   bool      `json:"enabled"`
	Healthy   bool      `json:"healthy"`
	LastError string    `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

var redisUpdatedAt atomic.Int64

// InitRedis 初始化 Redis 客户端。
// 与旧实现不同：即使 Redis 当前不可用，也不会直接终止整个网关进程。
func InitRedis() {
	RedisClient = redis.NewClient(&redis.Options{
		Addr:         config.GlobalConfig.Redis.Addr,
		Password:     config.GlobalConfig.Redis.Password,
		DB:           config.GlobalConfig.Redis.DB,
		PoolSize:     100,
		MinIdleConns: 10,
	})

	RateLimiter = redis_rate.NewLimiter(RedisClient)

	if err := pingRedis(); err != nil {
		markRedisUnhealthy(err)
		logger.Log.Warn("Redis unavailable at startup, gateway will run in degraded mode",
			zap.Error(err),
			zap.String("degraded_features", "cache,distributed_rate_limit"),
		)
	} else {
		markRedisHealthy()
		logger.Log.Info("Redis connected successfully",
			zap.String("mode", "distributed_cache_and_rate_limit"),
		)
	}

	startRedisMonitor()
}

func startRedisMonitor() {
	redisMonitorOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				if RedisClient == nil {
					continue
				}
				if err := pingRedis(); err != nil {
					markRedisUnhealthy(err)
					continue
				}
				markRedisHealthy()
			}
		}()
	})
}

func pingRedis() error {
	if RedisClient == nil {
		return redis.ErrClosed
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	return RedisClient.Ping(ctx).Err()
}

func markRedisHealthy() {
	redisHealthy.Store(true)
	redisUpdatedAt.Store(time.Now().Unix())

	redisStatusMu.Lock()
	redisLastErr = ""
	redisStatusMu.Unlock()
}

func markRedisUnhealthy(err error) {
	redisHealthy.Store(false)
	redisUpdatedAt.Store(time.Now().Unix())

	redisStatusMu.Lock()
	if err != nil {
		redisLastErr = err.Error()
	} else {
		redisLastErr = "unknown redis error"
	}
	redisStatusMu.Unlock()
}

func ReportRedisFailure(err error) {
	markRedisUnhealthy(err)
}

func ReportRedisSuccess() {
	markRedisHealthy()
}

func RedisAvailable() bool {
	return RedisClient != nil && redisHealthy.Load()
}

func RedisRateLimiterAvailable() bool {
	return RateLimiter != nil && redisHealthy.Load()
}

func GetRedisStatus() RedisDependencyStatus {
	redisStatusMu.RLock()
	lastErr := redisLastErr
	redisStatusMu.RUnlock()

	updatedAtUnix := redisUpdatedAt.Load()
	var updatedAt time.Time
	if updatedAtUnix > 0 {
		updatedAt = time.Unix(updatedAtUnix, 0)
	}

	return RedisDependencyStatus{
		Enabled:   RedisClient != nil,
		Healthy:   redisHealthy.Load(),
		LastError: lastErr,
		UpdatedAt: updatedAt,
	}
}
