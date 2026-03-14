package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
)

var (
	metricsCIDROnce sync.Once
	metricsCIDRs    []*net.IPNet

	metricsLimiterOnce sync.Once
	metricsLimiter     *localTokenBucket
)

type localTokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newLocalTokenBucket(rate float64, burst int) *localTokenBucket {
	now := time.Now()
	return &localTokenBucket{
		rate:   rate,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   now,
	}
}

func (b *localTokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now

	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}

	if b.tokens < 1 {
		return false
	}

	b.tokens -= 1
	return true
}

func resetMetricsEndpointRuntimeForTest() {
	metricsCIDROnce = sync.Once{}
	metricsCIDRs = nil

	metricsLimiterOnce = sync.Once{}
	metricsLimiter = nil
}

func getMetricsLimiter() *localTokenBucket {
	metricsLimiterOnce.Do(func() {
		rps := config.GlobalConfig.Metrics.RateLimitRPS
		burst := config.GlobalConfig.Metrics.RateLimitBurst
		if rps > 0 && burst > 0 {
			metricsLimiter = newLocalTokenBucket(rps, burst)
		}
	})
	return metricsLimiter
}

func loadMetricsAllowedCIDRs() []*net.IPNet {
	metricsCIDROnce.Do(func() {
		cidrs := config.GlobalConfig.Metrics.AllowedCIDRs
		nets := make([]*net.IPNet, 0, len(cidrs))
		for _, cidr := range cidrs {
			cidr = strings.TrimSpace(cidr)
			if cidr == "" {
				continue
			}
			_, ipNet, err := net.ParseCIDR(cidr)
			if err == nil {
				nets = append(nets, ipNet)
			}
		}
		metricsCIDRs = nets
	})

	return metricsCIDRs
}

func isMetricsIPAllowed(r *http.Request) bool {
	ipStr := remoteIP(r)
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}

	for _, ipNet := range loadMetricsAllowedCIDRs() {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

func hasValidMetricsBearerToken(r *http.Request) bool {
	expected := strings.TrimSpace(config.GlobalConfig.Metrics.BearerToken)
	if expected == "" {
		return false
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		return false
	}

	return token == expected
}

// MetricsEndpointMiddleware 保护 /metrics：
// 1. 允许 metrics bearer token
// 2. 或者允许来自受信 CIDR 的抓取请求
// 3. 对 /metrics 自身做轻量本地限流
func MetricsEndpointMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasValidMetricsBearerToken(r) && !isMetricsIPAllowed(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		limiter := getMetricsLimiter()
		if limiter != nil && !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
