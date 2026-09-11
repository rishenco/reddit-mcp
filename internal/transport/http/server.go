package http

import (
	"crypto/subtle"
	"log/slog"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	readHeaderTimeout = 10 * time.Second
	// sessionTimeout reaps sessions whose client went away without closing.
	// Left unset, the SDK never closes an idle session, so a long-running
	// server accumulates them for as long as it stays up.
	sessionTimeout = 30 * time.Minute
)

// Options configures the HTTP transport.
type Options struct {
	Addr string
	// AuthToken, when set, is required as "Authorization: Bearer <token>" on
	// every request. The endpoint otherwise lets anyone who can reach the port
	// spend the server's Reddit rate limit.
	AuthToken string
	Logger    *slog.Logger
}

func NewMCPServer(mcpSrv *mcp.Server, opts Options) *nethttp.Server {
	handler := mcp.NewStreamableHTTPHandler(func(*nethttp.Request) *mcp.Server {
		return mcpSrv
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout: sessionTimeout,
		Logger:         opts.Logger,
	})

	mux := nethttp.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)
	mux.HandleFunc("/healthz", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return &nethttp.Server{
		Addr:              opts.Addr,
		Handler:           requireToken(mux, opts.AuthToken),
		ReadHeaderTimeout: readHeaderTimeout,
		ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelError),
	}
}

// requireToken gates every path but /healthz behind a bearer token. With no
// token configured it is a pass-through, which keeps the localhost default
// frictionless.
func requireToken(next nethttp.Handler, token string) nethttp.Handler {
	if token == "" {
		return next
	}

	want := []byte("Bearer " + token)

	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)

			return
		}

		got := strings.TrimSpace(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="reddit-mcp"`)
			nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)

			return
		}

		next.ServeHTTP(w, r)
	})
}
