package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	gatewaymetrics "github.com/fxwio/go-llm-gateway/internal/metrics"
	"github.com/fxwio/go-llm-gateway/internal/model"
)

type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
	bytes      int
}

func (rw *metricsResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *metricsResponseWriter) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	return n, err
}

// MetricsMiddleware 负责上报 Prometheus 指标。
// 计数和时延都在请求结束后统一记录，in-flight 则在请求生命周期内增减。
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		route := metricsRouteLabel(r)

		gatewaymetrics.RequestsInFlight.WithLabelValues(route).Inc()
		defer gatewaymetrics.RequestsInFlight.WithLabelValues(route).Dec()

		wrappedWriter := &metricsResponseWriter{
			ResponseWriter: w,
			statusCode:     0,
		}

		next.ServeHTTP(wrappedWriter, r)

		if wrappedWriter.statusCode == 0 {
			wrappedWriter.statusCode = http.StatusOK
		}

		durationSeconds := time.Since(start).Seconds()

		provider := "unknown"
		targetModel := "unknown"
		if ctxVal := r.Context().Value(GatewayContextKey); ctxVal != nil {
			if gCtx, ok := ctxVal.(*model.GatewayContext); ok && gCtx != nil {
				if gCtx.TargetProvider != "" {
					provider = gCtx.TargetProvider
				}
				if gCtx.TargetModel != "" {
					targetModel = gCtx.TargetModel
				}
			}
		}

		statusCode := strconv.Itoa(wrappedWriter.statusCode)
		cacheStatus := normalizeCacheStatus(wrappedWriter.Header().Get("X-Cache"))

		gatewaymetrics.RequestTotal.WithLabelValues(
			provider,
			targetModel,
			statusCode,
			cacheStatus,
		).Inc()

		gatewaymetrics.RequestDuration.WithLabelValues(
			provider,
			targetModel,
			statusCode,
			cacheStatus,
		).Observe(durationSeconds)
	})
}

func metricsRouteLabel(r *http.Request) string {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		return "chat_completions"
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		return "health"
	case r.URL.Path == "/metrics":
		return "metrics"
	default:
		return "unknown"
	}
}

func normalizeCacheStatus(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "HIT":
		return "hit"
	case "MISS":
		return "miss"
	case "BYPASS":
		return "bypass"
	default:
		return "unknown"
	}
}
