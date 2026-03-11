package proxy

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
)

// CircuitBreakerTransport 包装了标准的 RoundTripper，加入了熔断逻辑
type CircuitBreakerTransport struct {
	Transport http.RoundTripper
}

var (
	// 按照不同的目标 Host (比如 api.openai.com) 维护独立的熔断器
	cbMap = make(map[string]*gobreaker.CircuitBreaker)
	cbMu  sync.RWMutex
)

// getBreaker 获取或创建针对某个目标域名的熔断器
func getBreaker(host string) *gobreaker.CircuitBreaker {
	cbMu.RLock()
	cb, exists := cbMap[host]
	cbMu.RUnlock()

	if exists {
		return cb
	}

	cbMu.Lock()
	defer cbMu.Unlock()

	// Double check
	if cb, exists = cbMap[host]; exists {
		return cb
	}

	// 初始化熔断器配置
	settings := gobreaker.Settings{
		Name:        host,
		MaxRequests: 3,                // 半开状态下允许试探的请求数
		Interval:    5 * time.Second,  // 统计周期
		Timeout:     10 * time.Second, // 熔断后，多久进入半开状态尝试恢复
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// 触发熔断的条件：错误率超过 50%，且请求总数大于 5
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.Requests >= 5 && failureRatio >= 0.5
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Log.Warn("Circuit Breaker state changed",
				zap.String("host", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	}

	cb = gobreaker.NewCircuitBreaker(settings)
	cbMap[host] = cb
	return cb
}

// RoundTrip 拦截实际的 HTTP 请求并注入熔断器
func (c *CircuitBreakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cb := getBreaker(req.URL.Host)

	// 使用熔断器执行请求
	respInterface, err := cb.Execute(func() (interface{}, error) {
		resp, err := c.Transport.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		// 如果下游厂商返回 5xx 服务器错误或 429 频控限制，也视为请求失败，计入熔断统计
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			return resp, fmt.Errorf("upstream error: status %d", resp.StatusCode)
		}
		return resp, nil
	})

	if err != nil {
		// 如果触发了熔断（gobreaker.ErrOpenState），这里会直接返回错误，不会发起真实网络请求（Fast Fail）
		logger.Log.Error("Proxy request failed or circuit breaker open",
			zap.String("host", req.URL.Host),
			zap.Error(err),
		)

		// 即使是在 Execute 内部返回的错误 resp，我们也需要尝试把它提取出来（比如为了透传 429 状态码）
		if resp, ok := respInterface.(*http.Response); ok && resp != nil {
			return resp, nil
		}

		return nil, err
	}

	return respInterface.(*http.Response), nil
}
