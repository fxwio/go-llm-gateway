package cache

import (
	"context"
	"log"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
)

var (
	RedisClient *redis.Client
	RateLimiter *redis_rate.Limiter
)

// InitRedis 初始化 Redis 连接池和全局分布式限流器
func InitRedis() {
	RedisClient = redis.NewClient(&redis.Options{
		Addr:         config.GlobalConfig.Redis.Addr,
		Password:     config.GlobalConfig.Redis.Password,
		DB:           config.GlobalConfig.Redis.DB,
		PoolSize:     100, // 生产级连接池大小
		MinIdleConns: 10,
	})

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := RedisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	// 挂载基于 Redis 的分布式限流器
	RateLimiter = redis_rate.NewLimiter(RedisClient)

	log.Println("Redis connected successfully & Distributed Rate Limiter initialized.")
}