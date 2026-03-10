package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fxwio/go-llm-gateway/internal/config"
	"github.com/fxwio/go-llm-gateway/internal/router"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
)

func main() {

	logger.InitLogger()

	config.LoadConfig("config.yaml")

	// 1. Init router & main engine
	r := router.NewRouter()

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
		// defend Slowloris
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// 2. start server
	go func() {
		log.Println("Starting Go-LLM-Gateway on :8080...")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen: %s\n", err)
		}
	}()

	// 3. Wait for the interrupt signal to shut down the server gracefully.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server gracefully...")

	// 4. Create a Context with a 5-second timeout and wait for existing connections to finish processing.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	defer logger.Sync()

	log.Println("Server exiting")

}
