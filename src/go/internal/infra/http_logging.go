package infra

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

// responseWriter is a minimal wrapper to capture the HTTP status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// LoggingMiddleware logs HTTP requests and CloudEvents.
func LoggingMiddleware(logger Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()
		reqID := uuid.New().String()

		ceID := r.Header.Get("Ce-Id")
		ceType := r.Header.Get("Ce-Type")
		ceSource := r.Header.Get("Ce-Source")

		if ceID != "" {
			logger.Info(ctx, "Event Received",
				"req_id", reqID,
				"ce_id", ceID,
				"ce_type", ceType,
				"ce_source", ceSource,
			)
		} else {
			// user_agent + remote_addr name the caller, so a hang-up can be
			// traced to the process that made it (the Jarvis gateway's aiohttp
			// poller, a script, curl) instead of guessed at from timings.
			logger.Info(ctx, "HTTP Request Received",
				"req_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"user_agent", r.UserAgent(),
				"remote_addr", r.RemoteAddr,
			)
		}

		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		attrs := []any{
			"req_id", reqID,
			"status_code", rw.status,
			"duration_ms", duration.Milliseconds(),
		}
		if ctx.Err() != nil {
			// The client went away before we finished (closed connection).
			attrs = append(attrs, "client_gone", true)
		}
		logger.Info(ctx, "Request Finished", attrs...)
	})
}
