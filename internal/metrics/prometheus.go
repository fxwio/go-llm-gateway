package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// 请求总量：按 provider / model / status_code / cache_status 统计。
	// cache_status 目前包含 hit / miss / shared / bypass。
	RequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of HTTP requests completed by the AI gateway.",
		},
		[]string{"provider", "model", "status_code", "cache_status"},
	)

	// 请求延迟：单位必须是 seconds，bucket 手工选择，覆盖毫秒级命中到长耗时推理。
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "gateway_request_duration_seconds",
			Help: "Histogram of completed HTTP request latencies in seconds.",
			Buckets: []float64{
				0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300,
			},
		},
		[]string{"provider", "model", "status_code", "cache_status"},
	)

	// 当前正在处理中的请求数。
	// 为了控制标签基数，只保留 route 维度，不带 request_id / client_ip 之类高基数标签。
	RequestsInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_requests_in_flight",
			Help: "Current number of in-flight HTTP requests being processed by the AI gateway.",
		},
		[]string{"route"},
	)

	// 缓存请求总量：hit / miss / shared / bypass。
	CacheRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_cache_requests_total",
			Help: "Total number of cache lookups by result.",
		},
		[]string{"provider", "model", "result"},
	)

	// Token 消耗总量。
	TokenUsageTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_tokens_total",
			Help: "Total number of AI tokens consumed.",
		},
		[]string{"provider", "model", "token_type"},
	)
)
