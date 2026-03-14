package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/fxwio/go-llm-gateway/internal/config"
)

func resetMetricsEndpointState() {
	resetMetricsEndpointRuntimeForTest()
}

func TestMetricsEndpoint_AllowsTrustedIP(t *testing.T) {
	resetMetricsEndpointState()

	config.GlobalConfig = &config.Config{
		Metrics: config.MetricsConfig{
			AllowedCIDRs:   []string{"127.0.0.1/32"},
			RateLimitRPS:   100,
			RateLimitBurst: 100,
		},
	}

	called := false
	handler := MetricsEndpointMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("expected handler to be called")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
}

func TestMetricsEndpoint_DeniesUntrustedIPWithoutToken(t *testing.T) {
	resetMetricsEndpointState()

	config.GlobalConfig = &config.Config{
		Metrics: config.MetricsConfig{
			AllowedCIDRs:   []string{"127.0.0.1/32"},
			RateLimitRPS:   100,
			RateLimitBurst: 100,
		},
	}

	handler := MetricsEndpointMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", rr.Code)
	}
}

func TestMetricsEndpoint_AllowsValidBearerToken(t *testing.T) {
	resetMetricsEndpointState()

	config.GlobalConfig = &config.Config{
		Metrics: config.MetricsConfig{
			BearerToken:    "metrics-secret",
			AllowedCIDRs:   []string{"127.0.0.1/32"},
			RateLimitRPS:   100,
			RateLimitBurst: 100,
		},
	}

	called := false
	handler := MetricsEndpointMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req.Header.Set("Authorization", "Bearer metrics-secret")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("expected handler to be called")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
}

func TestMetricsEndpoint_RateLimit(t *testing.T) {
	resetMetricsEndpointState()

	config.GlobalConfig = &config.Config{
		Metrics: config.MetricsConfig{
			AllowedCIDRs:   []string{"127.0.0.1/32"},
			RateLimitRPS:   1,
			RateLimitBurst: 1,
		},
	}

	handler := MetricsEndpointMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req1 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req1.RemoteAddr = "127.0.0.1:12345"
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req2.RemoteAddr = "127.0.0.1:12346"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr1.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", rr1.Code)
	}
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request 429, got %d", rr2.Code)
	}
}

// 防止 go test -race 下误报未引用 sync
var _ = sync.Once{}
