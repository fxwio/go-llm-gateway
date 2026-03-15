package tenant

import (
	"sort"
	"sync"
	"time"

	gatewaymetrics "github.com/fxwio/go-llm-gateway/internal/metrics"
)

type UsageSnapshot struct {
	Date             string    `json:"date"`
	Fingerprint      string    `json:"fingerprint"`
	TokenName        string    `json:"token_name"`
	Tenant           string    `json:"tenant"`
	App              string    `json:"app"`
	Requests         int64     `json:"requests"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	CacheHits        int64     `json:"cache_hits"`
	DailyTokenLimit  int64     `json:"daily_token_limit,omitempty"`
	RemainingTokens  int64     `json:"remaining_tokens,omitempty"`
	LastSeen         time.Time `json:"last_seen"`
}

var (
	usageMu    sync.RWMutex
	usageByDay = make(map[string]map[string]*UsageSnapshot)
)

func TodayUsage(fp string) (int64, bool) {
	usageMu.RLock()
	defer usageMu.RUnlock()
	day := dayKey(time.Now())
	perDay, ok := usageByDay[day]
	if !ok {
		return 0, false
	}
	snapshot, ok := perDay[fp]
	if !ok {
		return 0, false
	}
	return snapshot.TotalTokens, true
}

func IsDailyQuotaExceeded(identity ClientIdentity) (bool, int64) {
	if identity.DailyTokenLimit <= 0 {
		return false, 0
	}
	consumed, _ := TodayUsage(identity.Fingerprint)
	return consumed >= identity.DailyTokenLimit, consumed
}

func RecordUsage(identity ClientIdentity, promptTokens, completionTokens, totalTokens int, costStatus string) {
	if identity.Fingerprint == "" {
		return
	}

	usageMu.Lock()
	defer usageMu.Unlock()

	day := dayKey(time.Now())
	perDay, ok := usageByDay[day]
	if !ok {
		perDay = make(map[string]*UsageSnapshot)
		usageByDay[day] = perDay
	}
	snapshot, ok := perDay[identity.Fingerprint]
	if !ok {
		snapshot = &UsageSnapshot{
			Date:            day,
			Fingerprint:     identity.Fingerprint,
			TokenName:       identity.Name,
			Tenant:          identity.Tenant,
			App:             identity.App,
			DailyTokenLimit: identity.DailyTokenLimit,
		}
		perDay[identity.Fingerprint] = snapshot
	}

	snapshot.Requests++
	snapshot.PromptTokens += int64(promptTokens)
	snapshot.CompletionTokens += int64(completionTokens)
	snapshot.TotalTokens += int64(totalTokens)
	if costStatus == "hit_cache" {
		snapshot.CacheHits++
	}
	snapshot.LastSeen = time.Now().UTC()
	if snapshot.DailyTokenLimit > 0 {
		remaining := snapshot.DailyTokenLimit - snapshot.TotalTokens
		if remaining < 0 {
			remaining = 0
		}
		snapshot.RemainingTokens = remaining
	}

	gatewaymetrics.TenantTokenUsageTotal.WithLabelValues(identity.Tenant, identity.App, identity.Name, "prompt").Add(float64(promptTokens))
	gatewaymetrics.TenantTokenUsageTotal.WithLabelValues(identity.Tenant, identity.App, identity.Name, "completion").Add(float64(completionTokens))
}

func RecordRequestResult(identity ClientIdentity, result string) {
	gatewaymetrics.TenantRequestsTotal.WithLabelValues(identity.Tenant, identity.App, identity.Name, result).Inc()
}

func RecordQuotaRejection(identity ClientIdentity, reason string) {
	gatewaymetrics.QuotaRejectionsTotal.WithLabelValues(identity.Tenant, identity.App, identity.Name, reason).Inc()
	gatewaymetrics.TenantRequestsTotal.WithLabelValues(identity.Tenant, identity.App, identity.Name, "rejected").Inc()
}

func ListTodayUsage() []UsageSnapshot {
	usageMu.RLock()
	defer usageMu.RUnlock()

	day := dayKey(time.Now())
	perDay := usageByDay[day]
	out := make([]UsageSnapshot, 0, len(perDay))
	for _, snapshot := range perDay {
		out = append(out, *snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant == out[j].Tenant {
			if out[i].App == out[j].App {
				return out[i].TokenName < out[j].TokenName
			}
			return out[i].App < out[j].App
		}
		return out[i].Tenant < out[j].Tenant
	})
	return out
}

func dayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}
