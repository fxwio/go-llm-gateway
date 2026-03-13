package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// 1. 请求总量计数器 (按厂商、模型、HTTP状态码进行多维度统计)
	RequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of HTTP requests processed by the AI gateway",
		},
		[]string{"provider", "model", "status_code", "cache_status"},
	)

	// 2. 请求延迟分布直方图 (用于计算 P99, P95 延迟，这是架构师最看重的指标)
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Histogram of request latencies",
			Buckets: []float64{0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 40, 50, 60, 70, 80},
		},
		[]string{"provider", "model", "cache_status"},
	)

	// 3. 计费 Token 消耗总量计数器 (直观展示网关烧了多少钱)
	TokenUsageTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_tokens_total",
			Help: "Total number of AI tokens consumed",
		},
		[]string{"provider", "model", "token_type"}, // token_type: prompt 或 completion
	)
)
