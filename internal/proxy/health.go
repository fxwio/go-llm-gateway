package proxy

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
	gatewaymetrics "github.com/fxwio/go-llm-gateway/internal/metrics"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

type ProviderDependencyStatus struct {
	Name       string    `json:"name"`
	BaseURL    string    `json:"base_url"`
	Healthy    bool      `json:"healthy"`
	StatusCode int       `json:"status_code,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	Source     string    `json:"source,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

var (
	providerHealthMu    sync.RWMutex
	providerHealthMap   = make(map[string]ProviderDependencyStatus)
	providerMonitorOnce sync.Once
)

func initUpstreamHealthMonitor(transport http.RoundTripper) {
	providerMonitorOnce.Do(func() {
		seedProviderStatuses()

		interval, _ := time.ParseDuration(config.GlobalConfig.Upstream.HealthCheckInterval)
		if interval <= 0 {
			interval = 15 * time.Second
		}

		client := &http.Client{Transport: transport}

		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				for _, provider := range config.GlobalConfig.Providers {
					probeProvider(client, provider)
				}
				<-ticker.C
			}
		}()
	})
}

func seedProviderStatuses() {
	providerHealthMu.Lock()
	defer providerHealthMu.Unlock()

	for _, provider := range config.GlobalConfig.Providers {
		status := ProviderDependencyStatus{
			Name:      provider.Name,
			BaseURL:   provider.BaseURL,
			Healthy:   true,
			Source:    "bootstrap",
			UpdatedAt: time.Now(),
		}
		providerHealthMap[provider.Name] = status
		setProviderHealthMetric(provider.Name, true)
	}
}

func probeProvider(client *http.Client, provider config.ProviderConfig) {
	timeout, _ := time.ParseDuration(config.GlobalConfig.Upstream.HealthCheckTimeout)
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	probeURL := strings.TrimRight(provider.BaseURL, "/")
	if path := strings.TrimSpace(provider.HealthCheckPath); path != "" {
		probeURL += "/" + strings.TrimLeft(path, "/")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		updateProviderHealth(provider.Name, provider.BaseURL, false, 0, err, "active")
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		updateProviderHealth(provider.Name, provider.BaseURL, false, 0, err, "active")
		return
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Log.Debug(
				"Failed to close upstream health probe body",
				zap.String("provider", provider.Name),
				zap.Error(closeErr),
			)
		}
	}()

	healthy := resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests
	var healthErr error
	if !healthy {
		healthErr = errStatusUnhealthy(resp.StatusCode)
	}
	updateProviderHealth(provider.Name, provider.BaseURL, healthy, resp.StatusCode, healthErr, "active")
}

func markPassiveProbeResult(providerName, baseURL string, resp *http.Response, err error) {
	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}

	healthy := err == nil && resp != nil && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests
	updateProviderHealth(providerName, baseURL, healthy, statusCode, err, "passive")
}

func updateProviderHealth(providerName, baseURL string, healthy bool, statusCode int, err error, source string) {
	status := ProviderDependencyStatus{
		Name:       providerName,
		BaseURL:    baseURL,
		Healthy:    healthy,
		StatusCode: statusCode,
		Source:     source,
		UpdatedAt:  time.Now(),
	}
	if err != nil {
		status.LastError = err.Error()
	}

	providerHealthMu.Lock()
	providerHealthMap[providerName] = status
	providerHealthMu.Unlock()
	setProviderHealthMetric(providerName, healthy)

	if healthy {
		logger.Log.Debug(
			"Upstream provider healthy",
			zap.String("provider", providerName),
			zap.String("source", source),
			zap.Int("status_code", statusCode),
		)
		return
	}

	logger.Log.Warn(
		"Upstream provider unhealthy",
		zap.String("provider", providerName),
		zap.String("source", source),
		zap.Int("status_code", statusCode),
		zap.String("last_error", status.LastError),
	)
}

func GetUpstreamStatuses() map[string]ProviderDependencyStatus {
	providerHealthMu.RLock()
	defer providerHealthMu.RUnlock()

	result := make(map[string]ProviderDependencyStatus, len(providerHealthMap))
	for name, status := range providerHealthMap {
		result[name] = status
	}
	return result
}

func setProviderHealthMetric(provider string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1
	}
	gatewaymetrics.UpstreamProviderHealth.WithLabelValues(provider).Set(value)
}

func errStatusUnhealthy(statusCode int) error {
	return statusError{statusCode: statusCode}
}

type statusError struct {
	statusCode int
}

func (e statusError) Error() string {
	return http.StatusText(e.statusCode)
}
