package router

import (
	"net/http"
	"net/http/pprof"
	"runtime"
	"strings"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/buildinfo"
	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/middleware"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
)

type runtimeResponse struct {
	Now          time.Time        `json:"now"`
	Build        buildinfo.Info   `json:"build"`
	NumGoroutine int              `json:"num_goroutine"`
	NumCPU       int              `json:"num_cpu"`
	GoMaxProcs   int              `json:"gomaxprocs"`
	Memory       runtime.MemStats `json:"memory"`
	Redis        interface{}      `json:"redis"`
}

func registerAdminRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}

	protected := func(handler http.Handler) http.Handler {
		return middleware.AdminEndpointMiddleware(handler)
	}

	mux.Handle("/admin/runtime", protected(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		writeJSON(w, http.StatusOK, runtimeResponse{
			Now:          time.Now(),
			Build:        buildinfo.Current(),
			NumGoroutine: runtime.NumGoroutine(),
			NumCPU:       runtime.NumCPU(),
			GoMaxProcs:   runtime.GOMAXPROCS(0),
			Memory:       mem,
			Redis:        cache.GetRedisStatus(),
		})
	})))

	mux.Handle("/admin/configz", protected(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sanitizedConfig())
	})))

	if config.GlobalConfig.Debug.PprofEnabled {
		registerPprofRoutes(mux, protected)
	}
}

func registerPprofRoutes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	prefix := strings.TrimRight(config.GlobalConfig.Debug.PprofPathPrefix, "/")
	if prefix == "" {
		prefix = "/debug/pprof"
	}

	mux.Handle(prefix+"/", wrap(http.HandlerFunc(pprof.Index)))
	mux.Handle(prefix+"/cmdline", wrap(http.HandlerFunc(pprof.Cmdline)))
	mux.Handle(prefix+"/profile", wrap(http.HandlerFunc(pprof.Profile)))
	mux.Handle(prefix+"/symbol", wrap(http.HandlerFunc(pprof.Symbol)))
	mux.Handle(prefix+"/trace", wrap(http.HandlerFunc(pprof.Trace)))
	mux.Handle(prefix+"/allocs", wrap(pprof.Handler("allocs")))
	mux.Handle(prefix+"/block", wrap(pprof.Handler("block")))
	mux.Handle(prefix+"/goroutine", wrap(pprof.Handler("goroutine")))
	mux.Handle(prefix+"/heap", wrap(pprof.Handler("heap")))
	mux.Handle(prefix+"/mutex", wrap(pprof.Handler("mutex")))
	mux.Handle(prefix+"/threadcreate", wrap(pprof.Handler("threadcreate")))
}

func sanitizedConfig() interface{} {
	if config.GlobalConfig == nil {
		return map[string]string{"status": "config_not_loaded"}
	}

	cfg := *config.GlobalConfig
	cfg.Auth.ValidTokens = nil
	cfg.Auth.Tokens = append([]config.ClientTokenConfig(nil), cfg.Auth.Tokens...)
	for i := range cfg.Auth.Tokens {
		cfg.Auth.Tokens[i].Value = ""
	}
	cfg.Auth.Admin.BearerToken = ""
	cfg.Metrics.BearerToken = ""
	cfg.Providers = append([]config.ProviderConfig(nil), cfg.Providers...)
	for i := range cfg.Providers {
		cfg.Providers[i].APIKey = ""
	}

	return struct {
		GeneratedAt time.Time     `json:"generated_at"`
		Config      config.Config `json:"config"`
	}{
		GeneratedAt: time.Now(),
		Config:      cfg,
	}
}
