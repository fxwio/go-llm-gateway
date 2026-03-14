package router

import (
	"net/http"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/proxy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()

	// 业务主链路
	coreEngine := proxy.NewGatewayProxy()
	cachedHandler := middleware.CacheMiddleware(coreEngine)
	metricsHandler := middleware.MetricsMiddleware(cachedHandler)
	routedHandler := middleware.ModelRouterMiddleware(metricsHandler)
	bodyHandler := middleware.BodyContextMiddleware(middleware.DefaultMaxRequestBodyBytes, routedHandler)
	limitedHandler := middleware.RateLimitMiddleware(bodyHandler)
	authedHandler := middleware.AuthMiddleware(limitedHandler)
	accessLogHandler := middleware.AccessLogMiddleware(authedHandler)
	finalChatHandler := middleware.RequestMetaMiddleware(accessLogHandler)

	mux.Handle("POST /v1/chat/completions", finalChatHandler)

	// /metrics 自监控 + 保护
	metricsBaseHandler := promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{
			EnableOpenMetrics: config.GlobalConfig.Metrics.EnableOpenMetrics,
		},
	)
	metricsInstrumentedHandler := promhttp.InstrumentMetricHandler(
		prometheus.DefaultRegisterer,
		metricsBaseHandler,
	)
	protectedMetricsHandler := middleware.MetricsEndpointMiddleware(metricsInstrumentedHandler)

	mux.Handle(config.GlobalConfig.Metrics.Path, protectedMetricsHandler)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	return mux
}
