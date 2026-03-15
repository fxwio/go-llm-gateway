package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	TenantRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_tenant_requests_total",
			Help: "Total gateway requests grouped by tenant, app, token and result.",
		},
		[]string{"tenant", "app", "token", "result"},
	)

	TenantTokenUsageTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_tenant_token_usage_total",
			Help: "Accumulated token usage grouped by tenant, app, token and token type.",
		},
		[]string{"tenant", "app", "token", "token_type"},
	)

	QuotaRejectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_quota_rejections_total",
			Help: "Quota rejection count grouped by tenant, app, token and reason.",
		},
		[]string{"tenant", "app", "token", "reason"},
	)

	ConfiguredGatewayTokens = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_configured_tokens",
			Help: "Configured structured gateway tokens by tenant, app and token name.",
		},
		[]string{"tenant", "app", "token"},
	)
)
