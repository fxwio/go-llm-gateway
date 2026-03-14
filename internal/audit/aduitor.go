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
	ClientIP         string
	TokenFingerprint string // 原始 token 的稳定指纹，不落明文
	Provider         string // 实际调用的底层厂商 (openai, anthropic, siliconflow...)
	Model            string // 实际调用的模型
	PromptTokens     int    // 提问消耗的 Token
	CompletionToken  int    // 回答消耗的 Token
	TotalTokens      int    // 总耗费
	CostStatus       string // "hit_cache" / "api_call" / "stream_api_call"
}

var (
	// 定义一个带缓冲的 Channel，作为异步队列，容量 10000 应对高并发
	auditQueue = make(chan AuditRecord, 10000)
)

// InitAuditor 启动后台异步审计工作池
func InitAuditor() {
	for i := 0; i < 3; i++ {
		go func(workerID int) {
			logger.Log.Info("Audit worker started", zap.Int("worker_id", workerID))

			for record := range auditQueue {
				// 后续可以接 PostgreSQL / ClickHouse / billing service
				// 当前先用结构化日志输出，但不允许输出原始 token
				logger.Log.Info(
					"[BILLING AUDIT]",
					zap.String("client_ip", record.ClientIP),
					zap.String("gateway_token_fingerprint", record.TokenFingerprint),
					zap.String("provider", record.Provider),
					zap.String("model", record.Model),
					zap.Int("prompt_tokens", record.PromptTokens),
					zap.Int("completion_tokens", record.CompletionToken),
					zap.Int("total_tokens", record.TotalTokens),
					zap.String("status", record.CostStatus),
				)

				metrics.TokenUsageTotal.WithLabelValues(record.Provider, record.Model, "prompt").
					Add(float64(record.PromptTokens))
				metrics.TokenUsageTotal.WithLabelValues(record.Provider, record.Model, "completion").
					Add(float64(record.CompletionToken))
			}
		}(i)
	}
}

// PushRecord 提供给中间件的非阻塞投递接口
func PushRecord(record AuditRecord) {
	select {
	case auditQueue <- record:
		// 成功放入队列
	default:
		// 队列满时只丢记录，绝不阻塞主业务链路
		logger.Log.Warn("Audit queue is full, dropping billing record")
	}
}
