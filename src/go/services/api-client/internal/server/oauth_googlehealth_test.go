package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoogleHealthIdentity(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		token    string
		wantCode string
		wantUID  string
	}{
		{"linked", 200, `{"name":"users/me/identity","healthUserId":"hu-1","legacyUserId":"FB1"}`, "tok", "", "hu-1"},
		{"not linked", 400, `{"error":{"code":400,"status":"FAILED_PRECONDITION","details":[{"reason":"ACCOUNT_NOT_LINKED"}]}}`, "tok", "googlehealth_account_not_linked", ""},
		{"other api error", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED"}}`, "tok", "googlehealth_identity_403", ""},
		{"no token", 200, `{}`, "", "googlehealth_no_access_token", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v4/users/me/identity" {
					t.Errorf("path = %s", r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer "+tc.token {
					t.Errorf("Authorization = %q", got)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			old := googleHealthBaseURL
			googleHealthBaseURL = srv.URL
			defer func() { googleHealthBaseURL = old }()

			tokenResp := map[string]interface{}{"access_token": tc.token}
			if tc.token == "" {
				delete(tokenResp, "access_token")
			}
			if got := googleHealthIdentity(context.Background(), tokenResp); got != tc.wantCode {
				t.Errorf("code = %q, want %q", got, tc.wantCode)
			}
			if uid, _ := tokenResp["health_user_id"].(string); uid != tc.wantUID {
				t.Errorf("health_user_id = %q, want %q", uid, tc.wantUID)
			}
		})
	}

	// Unreachable API: a transport error, not an API answer.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	old := googleHealthBaseURL
	googleHealthBaseURL = srv.URL
	defer func() { googleHealthBaseURL = old }()
	if got := googleHealthIdentity(context.Background(), map[string]interface{}{"access_token": "t"}); got != "googlehealth_identity_error" {
		t.Errorf("transport error code = %q", got)
	}
}
