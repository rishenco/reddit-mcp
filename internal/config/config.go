package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

const (
	// TransportStdio serves MCP over stdin/stdout, the way MCP clients launch
	// servers declared in .mcp.json.
	TransportStdio = "stdio"
	// TransportHTTP serves MCP over streamable HTTP on HTTPAddr.
	TransportHTTP = "http"
)

type Config struct {
	ClientID     string `envconfig:"REDDIT_CLIENT_ID"`
	ClientSecret string `envconfig:"REDDIT_CLIENT_SECRET"`
	UserAgent    string `default:"reddit-mcp/0.1 (by /u/anonymous)" envconfig:"REDDIT_USER_AGENT"`
	Transport    string `default:"stdio"                            envconfig:"MCP_TRANSPORT"`
	HTTPAddr     string `default:"0.0.0.0:8080"                     envconfig:"HTTP_ADDR"`
	VerboseLog   bool   `default:"false"                            envconfig:"VERBOSE_LOG"`
	RateLimitRPM int    `default:"0"                                envconfig:"RATE_LIMIT_RPM"`
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
