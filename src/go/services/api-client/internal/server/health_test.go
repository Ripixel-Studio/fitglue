package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fitglue/server/src/go/internal/health"
	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/integrations/googlehealth"
)

type fakeHealth struct {
	kind health.Kind
	days int
	end  googlehealth.Date
	v    interface{}
	err  error
}

func (f *fakeHealth) ReadRange(ctx context.Context, kind health.Kind, days int, end googlehealth.Date) (interface{}, error) {
	f.kind, f.days, f.end = kind, days, end
	return f.v, f.err
}

func newHealthServer(h HealthReader) *APIServer {
	s := NewAPIServer(infra.NewLogger(), AuthConfig{Token: "secret", OwnerUID: "owner-uid"},
		&mockPublisher{}, &mockApiKeyStore{}, nil,
		&mockUserServiceClient{}, &mockPipelineServiceClient{}, &mockActivityServiceClient{})
	if h != nil {
		s.SetHealth(h)
	}
	return s
}

func getHealth(s *APIServer, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func TestHealthRoutesDefaultsAndBody(t *testing.T) {
	f := &fakeHealth{v: &health.SleepResponse{Nights: []health.Night{{Date: "2026-10-29", MinutesAsleep: 432, Efficiency: 90}}}}
	s := newHealthServer(f)

	w := getHealth(s, "/api/v2/users/me/sleep")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, health.KindSleep, f.kind)
	assert.Equal(t, 7, f.days)
	assert.JSONEq(t, `{"nights":[{"date":"2026-10-29","start":"","end":"","minutes_asleep":432,"efficiency":90}]}`, w.Body.String())

	getHealth(s, "/api/v2/users/me/body")
	assert.Equal(t, health.KindBody, f.kind)
	assert.Equal(t, 30, f.days)

	getHealth(s, "/api/v2/users/me/nutrition?days=3")
	assert.Equal(t, health.KindNutrition, f.kind)
	assert.Equal(t, 3, f.days)
}

func TestHealthRoutesStatuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    HealthReader
		path string
		want int
	}{
		{"no reader", nil, "/api/v2/users/me/sleep?days=7", http.StatusNotFound},
		{"not connected", &fakeHealth{err: fmt.Errorf("%w: no scope", health.ErrNotConnected)}, "/api/v2/users/me/nutrition?days=7", http.StatusNotFound},
		{"upstream", &fakeHealth{err: errors.New("google health api 500")}, "/api/v2/users/me/body?days=30", http.StatusBadGateway},
		{"bad days", &fakeHealth{}, "/api/v2/users/me/sleep?days=x", http.StatusBadRequest},
		{"too many days", &fakeHealth{}, "/api/v2/users/me/body?days=91", http.StatusBadRequest},
		{"zero days", &fakeHealth{}, "/api/v2/users/me/body?days=0", http.StatusBadRequest},
	} {
		w := getHealth(newHealthServer(tc.h), tc.path)
		assert.Equal(t, tc.want, w.Code, tc.name)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"), tc.name)
	}

	// The routes sit behind the bearer like the rest of /users/me.
	req := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/sleep", nil)
	w := httptest.NewRecorder()
	newHealthServer(&fakeHealth{}).ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRouteEndParam(t *testing.T) {
	f := &fakeHealth{v: &health.MovementResponse{}}
	s := newHealthServer(f)

	assert.Equal(t, http.StatusBadRequest, getHealth(s, "/api/v2/users/me/body?end=2025-13-01").Code)
	assert.Equal(t, http.StatusBadRequest, getHealth(s, "/api/v2/users/me/body?end=yesterday").Code)

	assert.Equal(t, http.StatusOK, getHealth(s, "/api/v2/users/me/movement").Code)
	assert.Equal(t, health.KindMovement, f.kind)
	assert.Equal(t, 7, f.days)
	assert.True(t, f.end.IsZero(), "a missing end means today")

	assert.Equal(t, http.StatusOK, getHealth(s, "/api/v2/users/me/body?end=2025-11-03&days=30").Code)
	assert.Equal(t, "2025-11-03", f.end.String())
}

func TestRouteMovementNotConnected(t *testing.T) {
	// A real reader over a grant without the activity scope: 404, never a Google call.
	r := health.NewReader(func(ctx context.Context) (*health.Grant, error) {
		return &health.Grant{Scope: "https://www.googleapis.com/auth/googlehealth.sleep.readonly", HTTP: http.DefaultClient}, nil
	})
	r.BaseURL = "http://127.0.0.1:1"
	w := getHealth(newHealthServer(r), "/api/v2/users/me/movement?days=14")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "NOT_CONNECTED")
}
