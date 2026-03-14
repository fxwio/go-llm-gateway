package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/response"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/redact"
	"github.com/go-redis/redis_rate/v10"
)

var (
	trustedProxyOnce sync.Once
	trustedProxyNets []*net.IPNet
	trustedProxyErr  error

	localFallbackLimiter = newKeyedLocalLimiter()
)

func resetRateLimitRuntimeForTest() {
	trustedProxyOnce = sync.Once{}
	trustedProxyNets = nil
	trustedProxyErr = nil
	localFallbackLimiter = newKeyedLocalLimiter()
}

func loadTrustedProxyCIDRs() ([]*net.IPNet, error) {
	trustedProxyOnce.Do(func() {
		cidrs := config.GlobalConfig.Server.TrustedProxyCIDRs
		nets := make([]*net.IPNet, 0, len(cidrs))

		for _, cidr := range cidrs {
			cidr = strings.TrimSpace(cidr)
			if cidr == "" {
				continue
			}
			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				trustedProxyErr = fmt.Errorf("invalid trusted proxy cidr %q: %w", cidr, err)
				return
			}
			nets = append(nets, ipNet)
		}

		trustedProxyNets = nets
	})

	return trustedProxyNets, trustedProxyErr
}

func remoteIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return ip
}

func isTrustedProxyIP(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}

	nets, err := loadTrustedProxyCIDRs()
	if err != nil {
		return false
	}

	for _, ipNet := range nets {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

func extractClientIP(r *http.Request) string {
	remote := remoteIP(r)

	if !isTrustedProxyIP(remote) {
		return remote
	}

	xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if xff != "" {
		ips := strings.Split(xff, ",")
		for _, candidate := range ips {
			candidate = strings.TrimSpace(candidate)
			if candidate != "" {
				return candidate
			}
		}
	}

	xrip := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if xrip != "" {
		return xrip
	}

	return remote
}

func buildRateLimitIdentity(r *http.Request) (scope string, key string) {
	if authCtx, ok := GetClientAuthContext(r); ok && strings.TrimSpace(authCtx.Token) != "" {
		fp := redact.TokenFingerprint(authCtx.Token)
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

		scope, limitKey := buildRateLimitIdentity(r)

		mode := "local"
		allowed := true
		remaining := -1
		resetAfter := ""
		retryAfter := ""

		if cache.RedisRateLimiterAvailable() {
			limit := redis_rate.Limit{
				Rate:   qps,
				Burst:  burst,
				Period: time.Second,
			}

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
					retryAfter = fmt.Sprintf("%d", int(res.RetryAfter.Seconds()))
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

			response.WriteOpenAIError(
				w,
				http.StatusTooManyRequests,
				"Rate limit exceeded.",
				"rate_limit_error",
				nil,
				response.Ptr("rate_limit_exceeded"),
			)
			return
		}

		next.ServeHTTP(w, r)
	})
}
