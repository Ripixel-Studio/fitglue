package main

import (
	"fmt"
	"os"
)

// Config holds the environment-derived settings for the MCP server.
//
// FitGlue is single-user and api-client authenticates every request with one
// static bearer token, so the only credential is FITGLUE_API_TOKEN — the same
// value the gateway was started with.
type Config struct {
	// BaseURL is the FitGlue deployment to talk to, e.g. https://fitglue.tech.
	// The api-client gateway is expected at {BaseURL}/api/v2.
	BaseURL string

	// Token is sent as `Authorization: Bearer <token>` on every request.
	Token string
}

const defaultBaseURL = "https://fitglue.tech"

// ConfigFromEnv builds a Config from FITGLUE_* environment variables.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		BaseURL: os.Getenv("FITGLUE_API_URL"),
		Token:   os.Getenv("FITGLUE_API_TOKEN"),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks that the token is present.
func (c Config) Validate() error {
	if c.Token == "" {
		return fmt.Errorf("set FITGLUE_API_TOKEN (the api-client static bearer token)")
	}
	return nil
}
