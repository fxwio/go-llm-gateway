package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/fxwio/go-llm-gateway/internal/config"
)

func resetAdminEndpointState() {
	adminCIDROnce = sync.Once{}
	adminCIDRs = nil
	adminCIDRErr = nil
	adminLimiterOnce = sync.Once{}
	adminLimiter = nil
}

func TestAdminEndpoint_ReturnsNotFoundWithoutProtection(t *testing.T) {
	resetAdminEndpointState()

	config.GlobalConfig = &config.Config{
		Auth: config.AuthConfig{
			Admin: config.AdminConfig{},
		},
	}

	called := false
	handler := AdminEndpointMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/runtime", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if called {
		t.Fatal("expected handler to stay hidden when admin protection is not configured")
	}
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}
