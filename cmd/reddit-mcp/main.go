package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
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

	// jsonrpcCodeServerClosing is jsonrpc2's non-standard "server is closing"
	// code, reported when a request arrives while the connection is shutting down.
	jsonrpcCodeServerClosing = -32004
)

func main() {
	transport := flag.String("transport", "", "MCP transport: stdio (default) or http; overrides MCP_TRANSPORT")

	flag.Parse()

	if err := run(*transport); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(transportFlag string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if transportFlag != "" {
		transport, terr := config.NormalizeTransport(transportFlag)
		if terr != nil {
			return fmt.Errorf("transport flag: %w", terr)
		}

		cfg.Transport = transport
	}

	// Logs always go to stderr: on stdio transport stdout carries the MCP protocol.
	logger := loggerpkg.New(cfg.VerboseLog)

	if cfg.Authenticated() {
		logger.Info("starting reddit-mcp",
			"transport", cfg.Transport, "auth", "app-only", "client_id", maskedID(cfg.ClientID))
	} else {
		logger.Info("starting reddit-mcp", "transport", cfg.Transport, "auth", "anonymous")
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

	if cfg.Transport == config.TransportStdio {
		return runStdio(ctx, mcpServer, logger)
	}

	return runHTTP(ctx, mcpServer, cfg.HTTPAddr, logger)
}

func runStdio(ctx context.Context, mcpServer *mcp.Server, logger *slog.Logger) error {
	logger.Info("serving on stdio")

	// The client closing stdin (or a signal) ends the session normally, not with a failure.
	if err := mcpServer.Run(ctx, &mcp.StdioTransport{}); err != nil && !isClientDisconnect(err) {
		return fmt.Errorf("stdio server: %w", err)
	}

	logger.Info("stopped")

	return nil
}

// isClientDisconnect reports whether err just means the MCP client went away:
// stdin closed, the session was cancelled, or a request was in flight while the
// connection was tearing down.
func isClientDisconnect(err error) bool {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}

	var wireErr *jsonrpc.Error

	return errors.As(err, &wireErr) && wireErr.Code == jsonrpcCodeServerClosing
}

func runHTTP(ctx context.Context, mcpServer *mcp.Server, addr string, logger *slog.Logger) error {
	httpServer := transporthttp.NewMCPServer(mcpServer, addr, logger)

	go shutdownOnSignal(ctx, httpServer, logger)

	logger.Info("listening", "addr", addr, "endpoint", "/mcp")

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
