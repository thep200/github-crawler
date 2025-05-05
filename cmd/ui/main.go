package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/internal/ui"
	"github.com/thep200/github-crawler/pkg/db"
	applog "github.com/thep200/github-crawler/pkg/log"
)

func main() {
	// Parse command line flags
	port := flag.Int("port", 8080, "Port for the UI server to listen on")
	flag.Parse()

	// Setup dependencies
	ctx := context.Background()
	loader, _ := cfg.NewViperLoader()
	config, _ := loader.Load()
	mysql, _ := db.NewMysql(config)
	logger, _ := applog.NewCslLogger()

	// Create and run the server
	server, err := ui.NewServer(logger, config, mysql, *port)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Run server in a goroutine
	go func() {
		logger.Info(ctx, "Starting UI server on port %d", *port)
		if err := server.Start(); err != nil {
			logger.Error(ctx, "Server failed to start: %v", err)
			os.Exit(1)
		}
	}()

	// Setup signal handling for graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Wait for termination signal
	<-stop

	// Create a context with timeout for shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Gracefully shutdown the server
	if err := server.Stop(shutdownCtx); err != nil {
		logger.Error(ctx, "Error during server shutdown: %v", err)
	}

	logger.Info(ctx, "Server shut down gracefully")
}
