//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/router"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
)

func TestOpsEndpointsAreProtected(t *testing.T) {
	logger.InitLogger()
	t.Cleanup(logger.Sync)

	configPath := filepath.Join("testdata", "config.integration.yaml")
	if err := config.LoadConfig(configPath); err != nil {
		t.Fatalf("load config: %v", err)
	}
	cache.InitRedis()

	ts := httptest.NewServer(router.NewRouter())
	t.Cleanup(ts.Close)

	assertStatus(t, ts.URL+"/health/live", "", http.StatusOK)
	assertStatus(t, ts.URL+"/version", "", http.StatusOK)
	assertStatus(t, ts.URL+"/metrics", "", http.StatusForbidden)
	assertStatus(t, ts.URL+"/admin/runtime", "", http.StatusUnauthorized)
	assertStatus(t, ts.URL+"/debug/pprof/", "", http.StatusUnauthorized)

	assertStatus(t, ts.URL+"/metrics", "Bearer metrics-token", http.StatusOK)
	assertStatus(t, ts.URL+"/admin/runtime", "Bearer admin-token", http.StatusOK)
}

func assertStatus(t *testing.T, target string, authHeader string, expected int) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expected {
		t.Fatalf("%s expected status %d, got %d", target, expected, resp.StatusCode)
	}
}
