package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const (
	// TransportStdio serves MCP over stdin/stdout, the way MCP clients launch
	// servers declared in .mcp.json.
	TransportStdio = "stdio"
	// TransportHTTP serves MCP over streamable HTTP on HTTPAddr.
	TransportHTTP = "http"

	bytesPerMB = 1024 * 1024
)

type Config struct {
	ClientID     string `envconfig:"REDDIT_CLIENT_ID"`
	ClientSecret string `envconfig:"REDDIT_CLIENT_SECRET"`
	UserAgent    string `envconfig:"REDDIT_USER_AGENT"`
	Transport    string `default:"stdio"                  envconfig:"MCP_TRANSPORT"`
	HTTPAddr     string `default:"127.0.0.1:8080"         envconfig:"HTTP_ADDR"`
	VerboseLog   bool   `default:"false"                  envconfig:"VERBOSE_LOG"`
	RateLimitRPM int    `default:"0"                      envconfig:"RATE_LIMIT_RPM"`

	// HTTPAuthToken, when set, is required as a bearer token on the http
	// transport. Without it the endpoint is open to whoever can reach the port.
	HTTPAuthToken string `envconfig:"MCP_AUTH_TOKEN"`

	// CacheTTL is the base freshness window for cached Reddit responses.
	// Listings use it directly; threads and profiles keep results longer.
	// Zero disables caching.
	CacheTTL time.Duration `default:"5m" envconfig:"CACHE_TTL"`
	// CacheMaxMB bounds the response cache. Zero disables caching.
	CacheMaxMB int `default:"50" envconfig:"CACHE_MAX_MB"`
	// ListingTextChars caps self-text and comment bodies inside listings, where
	// full bodies are wasted context. Zero disables the cap.
	ListingTextChars int `default:"500" envconfig:"LISTING_TEXT_CHARS"`
	// CommentTextChars caps comment bodies inside a thread, which are read
	// rather than skimmed. Zero, the default, keeps them whole.
	CommentTextChars int `default:"0" envconfig:"COMMENT_TEXT_CHARS"`
}

func Load() (Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, fmt.Errorf("process env: %w", err)
	}

	if (cfg.ClientID == "") != (cfg.ClientSecret == "") {
		return Config{}, errors.New(
			"REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET must be set together (or both empty for anonymous mode)",
		)
	}

	transport, err := NormalizeTransport(cfg.Transport)
	if err != nil {
		return Config{}, err
	}

	cfg.Transport = transport

	return cfg, nil
}

// NormalizeTransport lowercases and validates a transport name, so that both
// MCP_TRANSPORT and the command-line flag that overrides it are checked the same way.
func NormalizeTransport(transport string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(transport))

	switch normalized {
	case TransportStdio, TransportHTTP:
		return normalized, nil
	default:
		return "", fmt.Errorf("unknown transport %q: want %q or %q", transport, TransportStdio, TransportHTTP)
	}
}

func (cfg Config) Authenticated() bool {
	return cfg.ClientID != "" && cfg.ClientSecret != ""
}

// CacheMaxBytes converts the configured cache budget into bytes.
func (cfg Config) CacheMaxBytes() int64 {
	if cfg.CacheMaxMB <= 0 {
		return 0
	}

	return int64(cfg.CacheMaxMB) * bytesPerMB
}

// DefaultUserAgent follows the format Reddit asks for,
// <platform>:<app id>:<version>, stamped with the running build.
func DefaultUserAgent(version string) string {
	return "go:github.com/rishenco/reddit-mcp:" + version
}

// UserAgentOrDefault returns the configured User-Agent, falling back to a
// version-stamped default rather than a hardcoded one that drifts from the build.
func (cfg Config) UserAgentOrDefault(version string) string {
	if strings.TrimSpace(cfg.UserAgent) != "" {
		return cfg.UserAgent
	}

	return DefaultUserAgent(version)
}
