package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/audit"
	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/router"
	"github.com/fxwio/go-llm-gateway/pkg/cache"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	config.LoadConfig("config.yaml")

	logger.InitLogger()
	defer logger.Sync()

	audit.InitAuditor()
	cache.InitRedis()

	r := router.NewRouter()

	readTimeout, _ := time.ParseDuration(config.GlobalConfig.Server.ReadTimeout)
	writeTimeout, _ := time.ParseDuration(config.GlobalConfig.Server.WriteTimeout)

	if readTimeout == 0 {
		readTimeout = 300 * time.Second
	}
	if writeTimeout == 0 {
		writeTimeout = 300 * time.Second
	}

	port := config.GlobalConfig.Server.Port
	if port == 0 {
		port = 8080
	}
	addr := fmt.Sprintf(":%d", port)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Log.Info("Starting Go-LLM-Gateway", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Fatal("ListenAndServe failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Log.Info("Shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Log.Fatal("Server forced to shutdown", zap.Error(err))
	}

	logger.Log.Info("Server exited")
}
