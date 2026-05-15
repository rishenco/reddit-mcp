package http

import (
	"log/slog"
	nethttp "net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	readHeaderTimeout = 10 * time.Second
)

func NewMCPServer(mcpSrv *mcp.Server, addr string, logger *slog.Logger) *nethttp.Server {
	handler := mcp.NewStreamableHTTPHandler(func(*nethttp.Request) *mcp.Server {
		return mcpSrv
	}, nil)

	mux := nethttp.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)
	mux.HandleFunc("/healthz", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return &nethttp.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}
