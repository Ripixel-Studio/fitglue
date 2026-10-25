package infra

import (
	"context"
	"log/slog"
	"os"

	sentryPkg "github.com/fitglue/server/src/go/pkg/infrastructure/sentry"
)

// InitSentry initialises the Sentry SDK from the environment. Call it first
// thing in main(). Sentry is optional: SENTRY_DSN is the single switch — unset
// or empty means every capture in the process is a no-op (one Info line at
// boot says so). There is one DSN for all services; the service is told apart
// by server_name.
//
// Environment tag: ENVIRONMENT (terraform sets it; the box env file may), else
// "local". Release: SENTRY_RELEASE, else the Cloud Run revision, else
// "unknown". Server name: SERVICE_NAME, else the Cloud Run service.
func InitSentry() {
	environment := os.Getenv("ENVIRONMENT")
	if environment == "" {
		environment = "local"
	}

	release := os.Getenv("SENTRY_RELEASE")
	if release == "" {
		release = os.Getenv("K_REVISION")
		if release == "" {
			release = "unknown"
		}
	}

	serverName := os.Getenv("SERVICE_NAME")
	if serverName == "" {
		serverName = os.Getenv("K_SERVICE")
	}

	logger := NewLoggerWithComponent("sentry")

	if err := sentryPkg.Init(sentryPkg.Config{
		DSN:                os.Getenv("SENTRY_DSN"),
		Environment:        environment,
		Release:            release,
		ServerName:         serverName,
		TracesSampleRate:   0.1,
		ProfilesSampleRate: 0.1,
	}, slog.Default()); err != nil {
		logger.Warn(context.Background(), "Sentry initialization failed", "error", err)
	}
}
