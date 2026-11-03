package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fitglue/server/src/go/internal/health"
	"github.com/fitglue/server/src/go/pkg/integrations/googlehealth"
)

// HealthReader serves the owner's Google Health data (internal/health).
type HealthReader interface {
	ReadRange(ctx context.Context, kind health.Kind, days int, end googlehealth.Date) (interface{}, error)
}

// SetHealth installs the Google Health reader behind /users/me/sleep, /body
// /movement and /nutrition. Without one those routes answer 404, as an unconnected
// grant does.
func (s *APIServer) SetHealth(h HealthReader) { s.health = h }

func (s *APIServer) registerHealthRoutes(r chi.Router) {
	r.Get("/users/me/sleep", s.handleHealth(health.KindSleep))
	r.Get("/users/me/body", s.handleHealth(health.KindBody))
	r.Get("/users/me/movement", s.handleHealth(health.KindMovement))
	r.Get("/users/me/nutrition", s.handleHealth(health.KindNutrition))
}

// handleHealth answers 200 with the kind's JSON, 400 for a bad ?days or ?end, 404
// when Google Health is not connected for that kind (no grant, or no scope —
// Jarvis reads 404 as "not connected"), and 502 when Google fails.
func (s *APIServer) handleHealth(kind health.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := kind.DefaultDays()
		if v := r.URL.Query().Get("days"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > health.MaxDays {
				writeHealthError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "days must be an integer 1..90")
				return
			}
			days = n
		}
		var end googlehealth.Date // zero: today
		if v := r.URL.Query().Get("end"); v != "" {
			t, err := time.Parse("2006-01-02", v)
			if err != nil {
				writeHealthError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "end must be a date, YYYY-MM-DD")
				return
			}
			end = googlehealth.DateOf(t)
		}
		if s.health == nil {
			writeHealthError(w, http.StatusNotFound, "NOT_CONNECTED", "google health is not configured")
			return
		}
		v, err := s.health.ReadRange(r.Context(), kind, days, end)
		if err != nil {
			if errors.Is(err, health.ErrNotConnected) {
				writeHealthError(w, http.StatusNotFound, "NOT_CONNECTED", err.Error())
				return
			}
			s.logger.Warn(r.Context(), "google health read failed", "kind", string(kind), "error", err)
			writeHealthError(w, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error())
			return
		}
		_ = WriteJSON(w, v)
	}
}

func writeHealthError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = WriteJSON(w, APIError{Code: code, Message: msg})
}
