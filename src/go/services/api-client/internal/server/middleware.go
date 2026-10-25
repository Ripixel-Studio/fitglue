package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/go-chi/chi/v5/middleware"
)

type contextKey string

const userContextKey = contextKey("principal")

// Principal is the authenticated caller. FitGlue is single-user: every request
// that presents the static API token acts as the owner, so the only field is
// the owner's user id (the Firestore `users/{uid}` document the data is keyed
// by).
type Principal struct {
	UID string
}

// AuthConfig is the static bearer credential api-client checks on every
// authenticated route, and the user it resolves to. Both come from the
// environment (FITGLUE_API_TOKEN, FITGLUE_OWNER_UID) and are required.
type AuthConfig struct {
	Token    string
	OwnerUID string
}

// AuthMiddleware authenticates requests with a static bearer token.
//
// `Authorization: Bearer <token>` is compared against cfg.Token in constant
// time; a missing, malformed or mismatched header is a 401. On success the
// request context carries a Principal for cfg.OwnerUID, which handlers read
// through getUserToken. An empty cfg.Token never matches, so a misconfigured
// server rejects everything rather than accepting anything.
func AuthMiddleware(cfg AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "missing or malformed Authorization header", http.StatusUnauthorized)
				return
			}

			presented := strings.TrimPrefix(authHeader, "Bearer ")
			if cfg.Token == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(cfg.Token)) != 1 {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, &Principal{UID: cfg.OwnerUID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// LoggerMiddleware sets up structured logging on endpoints
func LoggerMiddleware(logger infra.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			reqID := middleware.GetReqID(r.Context())

			defer func() {
				logger.Info(r.Context(), "API Request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"duration_ms", time.Since(start).Milliseconds(),
					"req_id", reqID,
					"bytes_written", ww.BytesWritten(),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// JSONResponseMiddleware adds content-type application/json to responses
func JSONResponseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// SentryRecoveryMiddleware catches panics, logs them (which routes through SentryHandler
// to capture the exception in Sentry), and returns HTTP 500.
// Use in place of chi's middleware.Recoverer.
func SentryRecoveryMiddleware(logger infra.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					err, ok := rec.(error)
					if !ok {
						err = fmt.Errorf("panic: %v", rec)
					}
					logger.Error(r.Context(), "Panic recovered in HTTP handler", "error", err, "path", r.URL.Path)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
