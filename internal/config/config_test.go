package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rishenco/reddit-mcp/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Transport != config.TransportStdio {
		t.Errorf("transport = %q, want stdio", cfg.Transport)
	}

	// The http transport must not be reachable from outside the host unless
	// somebody opts in: it has no authentication by default.
	if !strings.HasPrefix(cfg.HTTPAddr, "127.0.0.1") {
		t.Errorf("default HTTP_ADDR = %q, want a loopback bind", cfg.HTTPAddr)
	}

	if cfg.CacheTTL != 5*time.Minute {
		t.Errorf("cache ttl = %v, want 5m", cfg.CacheTTL)
	}

	if cfg.ListingTextChars <= 0 {
		t.Errorf("listing text chars = %d, want a positive cap", cfg.ListingTextChars)
	}

	if cfg.Authenticated() {
		t.Error("no credentials in the environment means anonymous")
	}
}

func TestLoadRejectsHalfCredentials(t *testing.T) {
	t.Setenv("REDDIT_CLIENT_ID", "id-only")

	_, err := config.Load()
	if err == nil {
		t.Fatal("a client id without a secret cannot authenticate and should fail loudly")
	}

	if !strings.Contains(err.Error(), "REDDIT_CLIENT_SECRET") {
		t.Errorf("error should name the missing variable: %v", err)
	}
}

func TestNormalizeTransport(t *testing.T) {
	for input, want := range map[string]string{
		"stdio": config.TransportStdio,
		"STDIO": config.TransportStdio,
		" http": config.TransportHTTP,
	} {
		got, err := config.NormalizeTransport(input)
		if err != nil {
			t.Fatalf("NormalizeTransport(%q): %v", input, err)
		}

		if got != want {
			t.Errorf("NormalizeTransport(%q) = %q, want %q", input, got, want)
		}
	}

	if _, err := config.NormalizeTransport("carrier-pigeon"); err == nil {
		t.Error("unknown transports should be rejected")
	}
}

func TestCacheMaxBytes(t *testing.T) {
	cfg := config.Config{CacheMaxMB: 2}
	if got := cfg.CacheMaxBytes(); got != 2*1024*1024 {
		t.Errorf("CacheMaxBytes = %d", got)
	}

	if got := (config.Config{CacheMaxMB: 0}).CacheMaxBytes(); got != 0 {
		t.Errorf("CacheMaxBytes = %d, want 0 to disable the cache", got)
	}
}

func TestUserAgentDefaultsToTheRunningVersion(t *testing.T) {
	cfg := config.Config{}

	got := cfg.UserAgentOrDefault("v1.2.3")
	if !strings.Contains(got, "v1.2.3") {
		t.Errorf("default User-Agent %q should carry the build version", got)
	}

	cfg.UserAgent = "custom (by /u/me)"
	if got := cfg.UserAgentOrDefault("v1.2.3"); got != "custom (by /u/me)" {
		t.Errorf("configured User-Agent should win, got %q", got)
	}
}
