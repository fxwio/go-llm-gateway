package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/audit"
	"github.com/fxwio/go-llm-gateway/internal/buildinfo"
	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/router"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	logger.InitLogger()
	defer logger.Sync()

	configPath := config.ResolveConfigPath("")
	if err := config.LoadConfig(configPath); err != nil {
		logger.Log.Fatal("failed to load configuration",
			zap.String("config_path", configPath),
			zap.Error(err),
		)
	}

	audit.InitAuditor()
	cache.InitRedis()

	r := router.NewRouter()

	readTimeout := mustParseDuration(config.GlobalConfig.Server.ReadTimeout, 300*time.Second)
	readHeaderTimeout := mustParseDuration(config.GlobalConfig.Server.ReadHeaderTimeout, 10*time.Second)
	writeTimeout := mustParseDuration(config.GlobalConfig.Server.WriteTimeout, 300*time.Second)
	idleTimeout := mustParseDuration(config.GlobalConfig.Server.IdleTimeout, 120*time.Second)
	shutdownTimeout := mustParseDuration(config.GlobalConfig.Server.ShutdownTimeout, 10*time.Second)

	addr := net.JoinHostPort(config.GlobalConfig.Server.Host, fmt.Sprintf("%d", config.GlobalConfig.Server.Port))

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	info := buildinfo.Current()
	logger.Log.Info("starting Go-LLM-Gateway",
		zap.String("addr", addr),
		zap.String("config_path", configPath),
		zap.String("metrics_path", config.GlobalConfig.Metrics.Path),
		zap.String("version", info.Version),
		zap.String("commit", info.Commit),
		zap.String("build_date", info.BuildDate),
		zap.Int("provider_count", len(config.GlobalConfig.Providers)),
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Fatal("ListenAndServe failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Log.Info("shutting down server gracefully", zap.Duration("timeout", shutdownTimeout))

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Log.Fatal("server forced to shutdown", zap.Error(err))
	}

	logger.Log.Info("server exited")
}

func mustParseDuration(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
