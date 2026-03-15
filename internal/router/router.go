package router

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/internal/proxy"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type healthResponse struct {
	Status           string                 `json:"status"`
	Time             time.Time              `json:"time"`
	DegradedFeatures []string               `json:"degraded_features,omitempty"`
	Dependencies     map[string]interface{} `json:"dependencies,omitempty"`
}

func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()

	coreEngine := proxy.NewGatewayProxy()
	cachedHandler := middleware.CacheMiddleware(coreEngine)
	metricsHandler := middleware.MetricsMiddleware(cachedHandler)
	routedHandler := middleware.ModelRouterMiddleware(metricsHandler)
	bodyHandler := middleware.BodyContextMiddleware(middleware.DefaultMaxRequestBodyBytes, routedHandler)
	limitedHandler := middleware.RateLimitMiddleware(bodyHandler)
	authedHandler := middleware.AuthMiddleware(limitedHandler)
	recoveryHandler := middleware.RecoveryMiddleware(authedHandler)
	accessLogHandler := middleware.AccessLogMiddleware(recoveryHandler)
	finalChatHandler := middleware.RequestMetaMiddleware(accessLogHandler)

	mux.Handle("POST /v1/chat/completions", finalChatHandler)

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

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{
			Status: "alive",
			Time:   time.Now(),
		})
	})

	readyHandler := func(w http.ResponseWriter, r *http.Request) {
		redisStatus := cache.GetRedisStatus()
		resp := healthResponse{
			Status:       "ok",
			Time:         time.Now(),
			Dependencies: map[string]interface{}{"redis": redisStatus},
		}

		if !redisStatus.Healthy {
			resp.Status = "degraded"
			resp.DegradedFeatures = []string{"cache", "distributed_rate_limit"}
		}

		writeJSON(w, http.StatusOK, resp)
	}

	mux.HandleFunc("GET /health/ready", readyHandler)
	mux.HandleFunc("GET /health", readyHandler)

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
