package oauthflow

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

var secret = []byte("test-secret")

func TestSignVerifyStateRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := SignState(secret, "owner-uid", now)
	if strings.Count(state, ".") != 1 {
		t.Fatalf("state should be payload.signature, got %q", state)
	}
	uid, err := VerifyState(secret, state, now.Add(StateTTL))
	if err != nil || uid != "owner-uid" {
		t.Fatalf("VerifyState at TTL edge = %q, %v", uid, err)
	}
	if _, err := VerifyState(secret, state, now.Add(StateTTL+time.Second)); !errors.Is(err, ErrStateExpired) {
		t.Errorf("one second past TTL: err = %v, want ErrStateExpired", err)
	}
}

func TestSignStatePayloadShape(t *testing.T) {
	// The callback has always decoded {"ts":"<unix>","uid":"<uid>"} signed with
	// hex(HMAC-SHA256(secret, payload)); pin that so an old api-client and a
	// new fitgluectl interoperate.
	now := time.Unix(1234, 0)
	state := SignState(secret, "u1", now)
	parts := strings.Split(state, ".")
	payload, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"ts":"1234","uid":"u1"}` {
		t.Errorf("payload = %s", payload)
	}
	h := hmac.New(sha256.New, secret)
	h.Write(payload)
	if parts[1] != hex.EncodeToString(h.Sum(nil)) {
		t.Errorf("signature mismatch: %s", parts[1])
	}
}

func TestVerifyStateFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	good := SignState(secret, "u1", now)
	payloadB64 := strings.Split(good, ".")[0]
	sig := strings.Split(good, ".")[1]

	signed := func(payload string) string {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(payload))
		return base64.URLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(h.Sum(nil))
	}

	cases := []struct {
		name  string
		state string
		want  error
	}{
		{"no dot", "nodot", ErrStateFormat},
		{"two dots", "a.b.c", ErrStateFormat},
		{"bad base64", "!!!." + sig, ErrStateEncoding},
		{"wrong secret", payloadB64 + "." + strings.Repeat("0", 64), ErrStateSignature},
		{"not json", signed("not json"), ErrStatePayload},
		{"missing uid", signed(`{"ts":"1700000000"}`), ErrStateMissingUID},
		{"missing ts", signed(`{"uid":"u1"}`), ErrStateExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := VerifyState(secret, tc.state, now); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// The sentinel text is the redirect error code the callback emits.
	for _, e := range []error{ErrStateFormat, ErrStateEncoding, ErrStateSignature, ErrStatePayload, ErrStateExpired, ErrStateMissingUID} {
		if strings.ContainsAny(e.Error(), " /?&=") {
			t.Errorf("%q is not a bare error code", e.Error())
		}
	}
}

func TestProviderScopesAndEndpoints(t *testing.T) {
	t.Setenv("STRAVA_CLIENT_ID", "strava-id")
	t.Setenv("STRAVA_CLIENT_SECRET", "strava-secret")
	t.Setenv("FITBIT_CLIENT_ID", "fitbit-id")
	t.Setenv("GITHUB_CLIENT_ID", "github-id")
	t.Setenv("SPOTIFY_CLIENT_ID", "spotify-id")
	t.Setenv("GOOGLEHEALTH_CLIENT_ID", "gh-id")
	t.Setenv("GOOGLEHEALTH_CLIENT_SECRET", "gh-secret")

	want := map[string]struct {
		auth, token, clientID string
		scope                 string
	}{
		"strava":  {"https://www.strava.com/oauth/authorize", "https://www.strava.com/oauth/token", "strava-id", "read_all,activity:read_all,activity:write"},
		"fitbit":  {"https://www.fitbit.com/oauth2/authorize", "https://api.fitbit.com/oauth2/token", "fitbit-id", "activity profile heartrate"},
		"github":  {"https://github.com/login/oauth/authorize", "https://github.com/login/oauth/access_token", "github-id", "read:user"},
		"spotify": {"https://accounts.spotify.com/authorize", "https://accounts.spotify.com/api/token", "spotify-id", "user-read-recently-played user-read-playback-state"},
		"googlehealth": {"https://accounts.google.com/o/oauth2/v2/auth?access_type=offline&prompt=consent", "https://oauth2.googleapis.com/token", "gh-id",
			"https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly https://www.googleapis.com/auth/googlehealth.health_metrics_and_measurements.readonly https://www.googleapis.com/auth/googlehealth.sleep.readonly https://www.googleapis.com/auth/googlehealth.nutrition.readonly"},
	}
	if got := Providers(); strings.Join(got, ",") != "fitbit,github,googlehealth,spotify,strava" {
		t.Errorf("Providers() = %v", got)
	}
	for name, w := range want {
		cfg := Provider(name)
		if cfg == nil {
			t.Fatalf("%s: nil config", name)
		}
		if cfg.AuthURL != w.auth || cfg.TokenURL != w.token || cfg.ClientID != w.clientID {
			t.Errorf("%s: config = %+v", name, cfg)
		}
		if got := strings.Join(cfg.Scopes, ScopeSeparator(name)); got != w.scope {
			t.Errorf("%s: scope = %q, want %q", name, got, w.scope)
		}
	}
	if Provider("googlehealth").ClientSecret != "gh-secret" {
		t.Error("googlehealth client secret not read from env")
	}
	if Provider("strava").ClientSecret != "strava-secret" {
		t.Error("strava client secret not read from env")
	}
	for _, name := range []string{"hevy", "intervals", "trainingpeaks", "parkrun", ""} {
		if Provider(name) != nil {
			t.Errorf("%q should not be an OAuth provider", name)
		}
	}
}

func TestAuthorizeURL(t *testing.T) {
	cfg := &ProviderConfig{AuthURL: "https://www.strava.com/oauth/authorize", ClientID: "123", Scopes: []string{"read_all", "activity:read_all", "activity:write"}}
	got, err := AuthorizeURL("strava", cfg, "https://fitglue.tech/api/v2/oauth/strava/callback", "abc.def")
	if err != nil {
		t.Fatal(err)
	}
	// Sorted query keys, comma-joined Strava scopes, encoded redirect_uri —
	// byte-identical to what api-client has always returned.
	want := "https://www.strava.com/oauth/authorize?client_id=123&redirect_uri=https%3A%2F%2Ffitglue.tech%2Fapi%2Fv2%2Foauth%2Fstrava%2Fcallback&response_type=code&scope=read_all%2Cactivity%3Aread_all%2Cactivity%3Awrite&state=abc.def"
	if got != want {
		t.Errorf("AuthorizeURL =\n%s\nwant\n%s", got, want)
	}

	// Existing query parameters on the auth URL survive; no scopes → no scope key.
	got, err = AuthorizeURL("x", &ProviderConfig{AuthURL: "https://example.com/auth?prompt=consent", ClientID: "c"}, "https://cb", "s")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	if u.Query().Get("prompt") != "consent" || u.Query().Has("scope") {
		t.Errorf("query = %v", u.Query())
	}

	if _, err := AuthorizeURL("nope", nil, "", ""); err == nil {
		t.Error("nil config should error")
	}
	if _, err := AuthorizeURL("x", &ProviderConfig{AuthURL: "://bad"}, "", ""); err == nil {
		t.Error("unparseable auth url should error")
	}
}

func TestPublicBaseURLAndCallback(t *testing.T) {
	t.Setenv(PublicBaseURLEnv, "")
	t.Setenv(LegacyBaseURLEnv, "")
	if _, err := PublicBaseURL(); err == nil {
		t.Error("expected error with neither env set")
	}
	t.Setenv(LegacyBaseURLEnv, "https://fitglue.tech/")
	if b, _ := PublicBaseURL(); b != "https://fitglue.tech" {
		t.Errorf("API_URL fallback = %q", b)
	}
	t.Setenv(PublicBaseURLEnv, "https://jarvis.tail3065f7.ts.net/fg/")
	b, _ := PublicBaseURL()
	if b != "https://jarvis.tail3065f7.ts.net/fg" {
		t.Errorf("FITGLUE_PUBLIC_BASE_URL = %q", b)
	}
	if got := CallbackURL(b, "strava"); got != "https://jarvis.tail3065f7.ts.net/fg/api/v2/oauth/strava/callback" {
		t.Errorf("CallbackURL = %q", got)
	}
}

func TestConnectEndToEnd(t *testing.T) {
	t.Setenv(PublicBaseURLEnv, "https://fitglue.tech")
	t.Setenv("FITBIT_CLIENT_ID", "fb")
	now := time.Unix(1_700_000_000, 0)
	got, err := Connect("fitbit", "owner", secret, now)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "www.fitbit.com" || q.Get("client_id") != "fb" || q.Get("response_type") != "code" ||
		q.Get("redirect_uri") != "https://fitglue.tech/api/v2/oauth/fitbit/callback" || q.Get("scope") != "activity profile heartrate" {
		t.Errorf("url = %s", got)
	}
	// The state in the URL verifies with the same secret and carries the uid:
	// this is the contract between fitgluectl connect and the callback.
	uid, err := VerifyState(secret, q.Get("state"), now.Add(time.Minute))
	if err != nil || uid != "owner" {
		t.Errorf("state round trip: %q, %v", uid, err)
	}
	if _, err := Connect("hevy", "owner", secret, now); err == nil {
		t.Error("hevy is not an OAuth provider")
	}
	t.Setenv(PublicBaseURLEnv, "")
	t.Setenv(LegacyBaseURLEnv, "")
	if _, err := Connect("fitbit", "owner", secret, now); err == nil {
		t.Error("expected error without a base URL")
	}
}

// TestConnectGoogleHealth pins the authorise URL `fitgluectl connect
// googlehealth` prints on the box: Google's endpoint, offline access, a forced
// consent screen (so a re-connect returns a refresh token), the four read-only
// scopes, and the redirect_uri that must be registered on the Google OAuth
// client byte for byte.
func TestConnectGoogleHealth(t *testing.T) {
	t.Setenv(PublicBaseURLEnv, "https://jarvis.tail3065f7.ts.net/fg")
	t.Setenv("GOOGLEHEALTH_CLIENT_ID", "gh.apps.googleusercontent.com")
	now := time.Unix(1_700_000_000, 0)
	got, err := Connect("googlehealth", "owner", secret, now)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme+"://"+u.Host+u.Path != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Errorf("auth endpoint = %s", got)
	}
	checks := map[string]string{
		"client_id":     "gh.apps.googleusercontent.com",
		"response_type": "code",
		"access_type":   "offline",
		"prompt":        "consent",
		"redirect_uri":  "https://jarvis.tail3065f7.ts.net/fg/api/v2/oauth/googlehealth/callback",
		"scope":         strings.Join(GoogleHealthScopes, " "),
	}
	for k, want := range checks {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	for _, s := range GoogleHealthScopes {
		if !strings.HasPrefix(s, "https://www.googleapis.com/auth/googlehealth.") || !strings.HasSuffix(s, ".readonly") {
			t.Errorf("scope %q is not a read-only Google Health scope", s)
		}
	}
	if uid, err := VerifyState(secret, q.Get("state"), now.Add(time.Minute)); err != nil || uid != "owner" {
		t.Errorf("state round trip: %q, %v", uid, err)
	}
}
