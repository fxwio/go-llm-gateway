package audit

import (
	"time"

	"github.com/fxwio/go-llm-gateway/internal/metrics"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

// AuditRecord 定义审计/计费记录。
// 注意：这里绝不保存原始 token，只保存稳定指纹用于排查与聚合。
type AuditRecord struct {
	Timestamp        time.Time
	RequestID        string
	TraceID          string
	ClientIP         string
	TokenFingerprint string
	Provider         string
	Model            string
	PromptTokens     int
	CompletionToken  int
	TotalTokens      int
	CostStatus       string // hit_cache / api_call / stream_api_call / coalesced_shared
}

var (
	auditQueue = make(chan AuditRecord, 10000)
)

func InitAuditor() {
	metrics.AuditQueueDepth.Set(0)

	for i := 0; i < 3; i++ {
		go func(workerID int) {
			logger.Log.Info("Audit worker started", zap.Int("worker_id", workerID))

			for record := range auditQueue {
				logger.Log.Info(
					"[BILLING AUDIT]",
					zap.String("request_id", record.RequestID),
					zap.String("trace_id", record.TraceID),
					zap.String("client_ip", record.ClientIP),
					zap.String("gateway_token_fingerprint", record.TokenFingerprint),
					zap.String("provider", record.Provider),
					zap.String("model", record.Model),
					zap.Int("prompt_tokens", record.PromptTokens),
					zap.Int("completion_tokens", record.CompletionToken),
					zap.Int("total_tokens", record.TotalTokens),
					zap.String("cost_status", record.CostStatus),
				)

				metrics.TokenUsageTotal.WithLabelValues(record.Provider, record.Model, "prompt").
					Add(float64(record.PromptTokens))
				metrics.TokenUsageTotal.WithLabelValues(record.Provider, record.Model, "completion").
					Add(float64(record.CompletionToken))
				metrics.TokenUsageTotal.WithLabelValues(record.Provider, record.Model, "total").
					Add(float64(record.TotalTokens))
				metrics.AuditQueueDepth.Set(float64(len(auditQueue)))
			}
		}(i)
	}
}

func PushRecord(record AuditRecord) {
	select {
	case auditQueue <- record:
		metrics.AuditQueueDepth.Set(float64(len(auditQueue)))
	default:
		metrics.AuditDroppedTotal.Inc()
		logger.Log.Warn(
			"Audit queue is full, dropping billing record",
			zap.String("request_id", record.RequestID),
			zap.String("trace_id", record.TraceID),
		)
	}
}
