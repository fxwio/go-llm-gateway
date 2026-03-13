package audit

import (
	"time"

	"github.com/fxwio/go-llm-gateway/internal/metrics"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

// AuditRecord 定义了我们需要审计的账单数据
type AuditRecord struct {
	Timestamp       time.Time
	ClientIP        string
	Token           string // 客户端使用的鉴权 Token
	Provider        string // 实际调用的底层厂商 (openai, anthropic)
	Model           string // 实际调用的模型
	PromptTokens    int    // 提问消耗的 Token
	CompletionToken int    // 回答消耗的 Token
	TotalTokens     int    // 总耗费
	CostStatus      string // "hit_cache" 或 "api_call"
}

var (
	// 定义一个带缓冲的 Channel，作为异步队列，容量 10000 应对高并发
	auditQueue = make(chan AuditRecord, 10000)
)

// InitAuditor 启动后台异步审计工作池 (Worker Pool)
func InitAuditor() {
	// 启动 3 个后台 Goroutine 专门负责消费审计日志
	for i := 0; i < 3; i++ {
		go func(workerID int) {
			logger.Log.Info("Audit worker started", zap.Int("worker_id", workerID))
			for record := range auditQueue {
				// 在这里，你可以将 record 写入 PostgreSQL、ClickHouse 或者专门的计费系统
				// 目前我们先用高性能结构化日志将其独立打印出来，方便后续通过 ELK 收集
				logger.Log.Info("[BILLING AUDIT]",
					zap.String("client_ip", record.ClientIP),
					zap.String("gateway_token", record.Token),
					zap.String("provider", record.Provider),
					zap.String("model", record.Model),
					zap.Int("prompt_tokens", record.PromptTokens),
					zap.Int("completion_tokens", record.CompletionToken),
					zap.Int("total_tokens", record.TotalTokens),
					zap.String("status", record.CostStatus),
				)

				metrics.TokenUsageTotal.WithLabelValues(record.Provider, record.Model, "prompt").Add(float64(record.PromptTokens))
				metrics.TokenUsageTotal.WithLabelValues(record.Provider, record.Model, "completion").Add(float64(record.CompletionToken))
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
		// 如果突发流量把 10000 的队列打满了，直接丢弃或走降级日志，绝对不能阻塞主业务逻辑！
		logger.Log.Warn("Audit queue is full, dropping billing record (CRITICAL)")
	}
}
