package infra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/idtoken"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
}

func pushReq(authz string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/pubsub/raw", nil)
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	return r
}

func TestPubSubPushAuth_NoAudienceIsPassthrough(t *testing.T) {
	t.Setenv("PUBSUB_PUSH_AUDIENCE", "")
	t.Setenv("PUBSUB_PUSH_SERVICE_ACCOUNT", "")
	a := PubSubPushAuthFromEnv(NewLogger())
	w := httptest.NewRecorder()
	a.Wrap(okHandler()).ServeHTTP(w, pushReq(""))
	if w.Code != http.StatusTeapot {
		t.Fatalf("expected passthrough 418, got %d", w.Code)
	}
}

func TestPubSubPushAuth_RejectsMissingAndBadTokens(t *testing.T) {
	a := &PubSubPushAuth{
		Audience: "https://box.example/fg/pubsub",
		Logger:   NewLogger(),
		Verify: func(ctx context.Context, token, audience string) (*idtoken.Payload, error) {
			if audience != "https://box.example/fg/pubsub" {
				t.Errorf("verifier got audience %q", audience)
			}
			if token == "good" {
				return &idtoken.Payload{Claims: map[string]any{"email": "sa@x.iam.gserviceaccount.com", "email_verified": true}}, nil
			}
			return nil, errors.New("bad token")
		},
	}
	h := a.Wrap(okHandler())

	cases := []struct {
		name  string
		authz string
		want  int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"not bearer", "Basic abc", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"bad token", "Bearer nope", http.StatusForbidden},
		{"good token", "Bearer good", http.StatusTeapot},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, pushReq(c.authz))
		if w.Code != c.want {
			t.Errorf("%s: expected %d, got %d", c.name, c.want, w.Code)
		}
	}
}

func TestPubSubPushAuth_ServiceAccountPin(t *testing.T) {
	verify := func(email string, verified bool) IDTokenVerifier {
		return func(ctx context.Context, token, audience string) (*idtoken.Payload, error) {
			return &idtoken.Payload{Claims: map[string]any{"email": email, "email_verified": verified}}, nil
		}
	}
	const want = "fitglue-box@fitglue-server-prod.iam.gserviceaccount.com"
	cases := []struct {
		name     string
		email    string
		verified bool
		code     int
	}{
		{"pinned account", want, true, http.StatusTeapot},
		{"case-insensitive", "FitGlue-Box@fitglue-server-prod.iam.gserviceaccount.com", true, http.StatusTeapot},
		{"other account", "someone@else.iam.gserviceaccount.com", true, http.StatusForbidden},
		{"unverified email", want, false, http.StatusForbidden},
		{"no email claim", "", true, http.StatusForbidden},
	}
	for _, c := range cases {
		a := &PubSubPushAuth{Audience: "aud", ServiceAccount: want, Verify: verify(c.email, c.verified)}
		w := httptest.NewRecorder()
		a.WrapFunc(okHandler().ServeHTTP).ServeHTTP(w, pushReq("Bearer t"))
		if w.Code != c.code {
			t.Errorf("%s: expected %d, got %d", c.name, c.code, w.Code)
		}
	}
}

func TestPubSubPushAuth_NilReceiverPassthrough(t *testing.T) {
	var a *PubSubPushAuth
	w := httptest.NewRecorder()
	a.Wrap(okHandler()).ServeHTTP(w, pushReq(""))
	if w.Code != http.StatusTeapot {
		t.Fatalf("expected passthrough 418, got %d", w.Code)
	}
}
