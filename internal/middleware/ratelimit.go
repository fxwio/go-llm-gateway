package middleware

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/response"
	"github.com/fxwio/go-llm-gateway/internal/tenant"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/redact"
	"github.com/go-redis/redis_rate/v10"
)

var (
	trustedProxyOnce     sync.Once
	trustedProxyNets     []*net.IPNet
	trustedProxyErr      error
	localFallbackLimiter = newKeyedLocalLimiter()
)

func loadTrustedProxyCIDRs() ([]*net.IPNet, error) {
	trustedProxyOnce.Do(func() {
		trustedProxyNets, trustedProxyErr = parseCIDRs(config.GlobalConfig.Server.TrustedProxyCIDRs, "trusted proxy")
	})
	return trustedProxyNets, trustedProxyErr
}

func extractClientIP(r *http.Request) string {
	trustedCIDRs, err := loadTrustedProxyCIDRs()
	if err != nil {
		return remoteIP(r)
	}
	return extractClientIPFromTrustedProxy(r, trustedCIDRs)
}

func buildRateLimitIdentity(r *http.Request) (scope string, key string) {
	if authCtx, ok := GetClientAuthContext(r); ok && strings.TrimSpace(authCtx.Token) != "" {
		fp := authCtx.Fingerprint
		if fp == "" {
			fp = redact.TokenFingerprint(authCtx.Token)
		}
		if fp != "" {
			return "token", "rate_limit:token:" + fp
		}
	}
	clientIP := extractClientIP(r)
	return "ip", "rate_limit:ip:" + clientIP
}

func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qps := int(config.GlobalConfig.Auth.RateLimitQPS)
		if qps <= 0 {
			qps = 1
		}
		burst := config.GlobalConfig.Auth.RateLimitBurst
		if burst <= 0 {
			burst = qps
		}

		if authCtx, ok := GetClientAuthContext(r); ok {
			if authCtx.RateLimitQPS > 0 {
				qps = int(authCtx.RateLimitQPS)
			}
			if authCtx.RateLimitBurst > 0 {
				burst = authCtx.RateLimitBurst
			}
			identity := tenant.ClientIdentity{
				Fingerprint:     authCtx.Fingerprint,
				Name:            authCtx.TokenName,
				Tenant:          authCtx.Tenant,
				App:             authCtx.App,
				DailyTokenLimit: authCtx.DailyTokenLimit,
			}
			if exceeded, consumed := tenant.IsDailyQuotaExceeded(identity); exceeded {
				w.Header().Set("X-Usage-Daily-Limit", strconv.FormatInt(authCtx.DailyTokenLimit, 10))
				w.Header().Set("X-Usage-Daily-Consumed", strconv.FormatInt(consumed, 10))
				tenant.RecordQuotaRejection(identity, "daily_token_limit")
				response.WriteOpenAIError(w, http.StatusTooManyRequests, "Daily token quota exceeded.", "insufficient_quota", nil, response.Ptr("daily_token_quota_exceeded"))
				return
			}
		}

		scope, limitKey := buildRateLimitIdentity(r)
		mode := "local"
		var allowed bool
		remaining := -1
		resetAfter := ""
		retryAfter := ""

		if cache.RedisRateLimiterAvailable() {
			limit := redis_rate.Limit{Rate: qps, Burst: burst, Period: time.Second}
			res, err := cache.RateLimiter.Allow(r.Context(), limitKey, limit)
			if err == nil {
				cache.ReportRedisSuccess()
				mode = "redis"
				allowed = res.Allowed > 0
				remaining = res.Remaining
				if res.ResetAfter > 0 {
					resetAfter = res.ResetAfter.String()
				}
				if res.RetryAfter > 0 {
					retryAfter = strconv.Itoa(int(res.RetryAfter.Seconds()))
				}
			} else {
				cache.ReportRedisFailure(err)
				allowed = localFallbackLimiter.Allow(limitKey, float64(qps), burst)
			}
		} else {
			allowed = localFallbackLimiter.Allow(limitKey, float64(qps), burst)
		}

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(qps))
		w.Header().Set("X-RateLimit-Burst", strconv.Itoa(burst))
		w.Header().Set("X-RateLimit-Scope", scope)
		w.Header().Set("X-RateLimit-Mode", mode)
		if authCtx, ok := GetClientAuthContext(r); ok {
			if authCtx.TokenName != "" {
				w.Header().Set("X-Gateway-Tenant", authCtx.Tenant)
				w.Header().Set("X-Gateway-App", authCtx.App)
				w.Header().Set("X-Gateway-Token", authCtx.TokenName)
				if authCtx.DailyTokenLimit > 0 {
					w.Header().Set("X-Usage-Daily-Limit", strconv.FormatInt(authCtx.DailyTokenLimit, 10))
					if consumed, ok := tenant.TodayUsage(authCtx.Fingerprint); ok {
						w.Header().Set("X-Usage-Daily-Consumed", strconv.FormatInt(consumed, 10))
					}
				}
			}
		}
		if scope == "ip" {
			w.Header().Set("X-RateLimit-Client-IP", extractClientIP(r))
		}
		if remaining >= 0 {
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		}
		if resetAfter != "" {
			w.Header().Set("X-RateLimit-Reset-After", resetAfter)
		}
		if !allowed {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			} else {
				w.Header().Set("Retry-After", "1")
			}
			if authCtx, ok := GetClientAuthContext(r); ok {
				tenant.RecordQuotaRejection(tenant.ClientIdentity{Fingerprint: authCtx.Fingerprint, Name: authCtx.TokenName, Tenant: authCtx.Tenant, App: authCtx.App}, "rate_limit")
			}
			response.WriteOpenAIError(w, http.StatusTooManyRequests, "Rate limit exceeded.", "rate_limit_error", nil, response.Ptr("rate_limit_exceeded"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
