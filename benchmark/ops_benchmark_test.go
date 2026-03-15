package benchmark

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/router"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
)

func BenchmarkHealthLive(b *testing.B) {
	if err := config.LoadConfig(filepath.Join("..", "integration", "testdata", "config.integration.yaml")); err != nil {
		b.Fatalf("load config: %v", err)
	}
	cache.InitRedis()

	h := router.NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}
}
