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
)

type fakeHealth struct {
	kind health.Kind
	days int
	v    interface{}
	err  error
}

func (f *fakeHealth) Read(ctx context.Context, kind health.Kind, days int) (interface{}, error) {
	f.kind, f.days = kind, days
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
