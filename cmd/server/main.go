package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rishenco/reddit-mcp/internal/config"
	loggerpkg "github.com/rishenco/reddit-mcp/internal/logger"
	"github.com/rishenco/reddit-mcp/internal/reddit"
	"github.com/rishenco/reddit-mcp/internal/tools"
	transporthttp "github.com/rishenco/reddit-mcp/internal/transport/http"
)

const (
	serverName      = "reddit-mcp"
	serverVersion   = "0.1.0"
	shutdownTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := loggerpkg.New(cfg.VerboseLog)

	if cfg.Authenticated() {
		logger.Info("starting reddit-mcp", "auth", "app-only", "client_id", maskedID(cfg.ClientID))
	} else {
		logger.Info("starting reddit-mcp", "auth", "anonymous")
	}

	redditClient := reddit.New(
		cfg.ClientID,
		cfg.ClientSecret,
		cfg.UserAgent,
		cfg.RateLimitRPM,
		logger,
	)

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)
	tools.Register(mcpServer, redditClient)

	httpServer := transporthttp.NewMCPServer(mcpServer, cfg.HTTPAddr, logger)

	go shutdownOnSignal(ctx, httpServer, logger)

	logger.Info("listening", "addr", cfg.HTTPAddr, "endpoint", "/mcp")

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	logger.Info("stopped")

	return nil
}

func shutdownOnSignal(ctx context.Context, httpServer *http.Server, logger *slog.Logger) {
	<-ctx.Done()
	logger.Info("shutting down")

	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer scancel()

	if err := httpServer.Shutdown(sctx); err != nil {
		logger.Error("shutdown", "err", err)
	}
}

func maskedID(value string) string {
	const minVisible = 4

	if len(value) <= minVisible {
		return "****"
	}

	return value[:2] + "****" + value[len(value)-2:]
}
