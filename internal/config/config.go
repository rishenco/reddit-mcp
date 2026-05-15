package config

import (
	"errors"
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	ClientID     string `envconfig:"REDDIT_CLIENT_ID"`
	ClientSecret string `envconfig:"REDDIT_CLIENT_SECRET"`
	UserAgent    string `default:"reddit-mcp/0.1 (by /u/anonymous)" envconfig:"REDDIT_USER_AGENT"`
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

	return cfg, nil
}

func (cfg Config) Authenticated() bool {
	return cfg.ClientID != "" && cfg.ClientSecret != ""
}
