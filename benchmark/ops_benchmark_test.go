package benchmark

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/router"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
)

var (
	benchmarkSetupOnce sync.Once
	benchmarkRouter    http.Handler
	benchmarkSetupErr  error
)

func BenchmarkHealthLive(b *testing.B) {
	benchmarkSetupOnce.Do(func() {
		if err := config.LoadConfig(filepath.Join("..", "integration", "testdata", "config.integration.yaml")); err != nil {
			benchmarkSetupErr = err
			return
		}
		cache.InitRedis()
		benchmarkRouter = router.NewRouter()
	})
	if benchmarkSetupErr != nil {
		b.Fatalf("benchmark setup: %v", benchmarkSetupErr)
	}

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		benchmarkRouter.ServeHTTP(rr, req)
	}
}
