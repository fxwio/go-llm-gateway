package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/fxwio/go-llm-gateway/internal/config"
)

var (
	metricsCIDROnce sync.Once
	metricsCIDRs    []*net.IPNet

	metricsLimiterOnce sync.Once
	metricsLimiter     *localTokenBucket
)

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
