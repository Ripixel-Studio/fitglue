package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/stretchr/testify/assert"
)

func TestJSONResponseMiddleware_ClientServer(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := JSONResponseMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestLoggerMiddleware_PassesThrough(t *testing.T) {
	logger := infra.NewLogger()
	called := false

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})

	handler := LoggerMiddleware(logger)(next)
	req := httptest.NewRequest(http.MethodGet, "/test-path", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.True(t, called, "next handler should have been called")
	assert.Equal(t, http.StatusAccepted, w.Code)
}

// authProbe is a terminal handler that records the Principal the middleware
// placed in the context.
func authProbe(t *testing.T, got **Principal) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = getUserToken(r)
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthMiddleware_MissingAuthHeader(t *testing.T) {
	var got *Principal
	handler := AuthMiddleware(AuthConfig{Token: "secret", OwnerUID: "owner"})(authProbe(t, &got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Nil(t, got, "handler must not run without a token")
}

func TestAuthMiddleware_MalformedAuthHeader(t *testing.T) {
	var got *Principal
	handler := AuthMiddleware(AuthConfig{Token: "secret", OwnerUID: "owner"})(authProbe(t, &got))

	for _, header := range []string{"NotBearer secret", "secret", "Bearer", "bearer secret"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", header)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code, "header %q", header)
		assert.Nil(t, got, "header %q", header)
	}
}

func TestAuthMiddleware_WrongToken(t *testing.T) {
	var got *Principal
	handler := AuthMiddleware(AuthConfig{Token: "secret", OwnerUID: "owner"})(authProbe(t, &got))

	for _, token := range []string{"wrong", "secre", "secret ", "Secret", ""} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code, "token %q", token)
		assert.Nil(t, got, "token %q", token)
	}
}

func TestAuthMiddleware_RightTokenSetsOwnerPrincipal(t *testing.T) {
	var got *Principal
	handler := AuthMiddleware(AuthConfig{Token: "secret", OwnerUID: "owner-uid"})(authProbe(t, &got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	if assert.NotNil(t, got) {
		assert.Equal(t, "owner-uid", got.UID)
	}
}

// An unconfigured server (empty token) must reject every request, including
// one presenting an empty bearer — never fail open.
func TestAuthMiddleware_EmptyConfiguredTokenRejectsAll(t *testing.T) {
	var got *Principal
	handler := AuthMiddleware(AuthConfig{OwnerUID: "owner"})(authProbe(t, &got))

	for _, header := range []string{"Bearer ", "Bearer x"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", header)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code, "header %q", header)
		assert.Nil(t, got, "header %q", header)
	}
}

func TestGetUserToken_NoPrincipal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Nil(t, getUserToken(req))
}

// The routed server: the OAuth callbacks stay reachable without the bearer
// (Strava/Fitbit/Hevy redirect there), everything under the owner's API does not.
func TestAPIServer_StaticBearerOnRoutes(t *testing.T) {
	s := NewAPIServer(
		infra.NewLogger(),
		AuthConfig{Token: "secret", OwnerUID: "owner-uid"},
		&mockPublisher{},
		&mockApiKeyStore{},
		nil,
		&mockUserServiceClient{},
		&mockPipelineServiceClient{},
		&mockActivityServiceClient{},
	)

	do := func(path, bearer string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		return w.Code
	}

	assert.Equal(t, http.StatusUnauthorized, do("/api/v2/users/me", ""))
	assert.Equal(t, http.StatusUnauthorized, do("/api/v2/users/me", "wrong"))
	assert.Equal(t, http.StatusOK, do("/api/v2/users/me", "secret"))
	assert.Equal(t, http.StatusOK, do("/api/v2/health", ""))
	// Callback without state/code is a client error from the handler, not a 401
	// from the middleware: the route is outside the authenticated group.
	assert.NotEqual(t, http.StatusUnauthorized, do("/api/v2/oauth/strava/callback", ""))
	assert.NotEqual(t, http.StatusUnauthorized, do("/auth/strava/callback", ""))
}
