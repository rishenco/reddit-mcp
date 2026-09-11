package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	transporthttp "github.com/rishenco/reddit-mcp/internal/transport/http"
)

func newHandler(t *testing.T, token string) http.Handler {
	t.Helper()

	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "reddit-mcp", Version: "test"}, nil)

	return transporthttp.NewMCPServer(mcpSrv, transporthttp.Options{
		Addr:      "127.0.0.1:0",
		AuthToken: token,
		Logger:    discardLogger(),
	}).Handler
}

func TestHealthzIsOpen(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(t, "secret").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want 200 even behind a token (probes cannot authenticate)", rec.Code)
	}
}

func TestMCPRequiresTokenWhenConfigured(t *testing.T) {
	handler := newHandler(t, "secret")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /mcp = %d, want 401", rec.Code)
	}

	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("a 401 should say how to authenticate")
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d, want 401", rec.Code)
	}
}

func TestNoTokenMeansNoGate(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(t, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))

	if rec.Code == http.StatusUnauthorized {
		t.Error("with no token configured the endpoint should not demand one")
	}
}
