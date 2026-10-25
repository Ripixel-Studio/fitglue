// Package oauthflow is the one implementation of FitGlue's OAuth connect
// flow: the per-provider authorise/token endpoints and scopes, the HMAC-signed
// state parameter, and the callback URL the provider redirects back to.
//
// Two callers share it. `api-client` serves the callback
// (`GET /api/v2/oauth/{provider}/callback`) and used to also build the
// authorise URL for the web app; `fitgluectl connect <provider>` now prints
// that same URL from a terminal on the box. Both must agree on the state
// signature and on redirect_uri (the provider rejects a token exchange whose
// redirect_uri differs from the one in the authorise request), so neither may
// build either on its own.
package oauthflow

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Env vars read by this package.
const (
	// StateSecretEnv signs the state parameter; every caller (CLI and
	// callback) must see the same value or the callback rejects the state.
	StateSecretEnv = "OAUTH_STATE_SECRET"
	// PublicBaseURLEnv is the externally reachable origin of api-client
	// (after F3.3 the Tailscale Funnel URL). It takes precedence over
	// LegacyBaseURLEnv when both are set.
	PublicBaseURLEnv = "FITGLUE_PUBLIC_BASE_URL"
	// LegacyBaseURLEnv is the name api-client has always read for the same
	// value on Cloud Run (terraform sets it to var.base_url).
	LegacyBaseURLEnv = "API_URL"
)

// StateTTL is how long a signed state stays valid.
const StateTTL = 10 * time.Minute

// CallbackPathPrefix is where api-client serves the callback; the provider
// name is appended, then "/callback".
const CallbackPathPrefix = "/api/v2/oauth/"

// ProviderConfig is one provider's OAuth 2.0 endpoints and app credentials.
type ProviderConfig struct {
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// Provider returns the configuration for an OAuth provider, or nil when the
// name is not an OAuth provider FitGlue knows how to connect. Client id and
// secret come from the environment (`STRAVA_CLIENT_ID`, ...); a missing env var
// yields an empty field, not an error, so callers can still print a URL for
// inspection — check ClientID before relying on it.
func Provider(name string) *ProviderConfig {
	switch name {
	case "strava":
		return &ProviderConfig{
			AuthURL:      "https://www.strava.com/oauth/authorize",
			TokenURL:     "https://www.strava.com/oauth/token",
			ClientID:     os.Getenv("STRAVA_CLIENT_ID"),
			ClientSecret: os.Getenv("STRAVA_CLIENT_SECRET"),
			Scopes:       []string{"read_all", "activity:read_all", "activity:write"},
		}
	case "fitbit":
		return &ProviderConfig{
			AuthURL:      "https://www.fitbit.com/oauth2/authorize",
			TokenURL:     "https://api.fitbit.com/oauth2/token",
			ClientID:     os.Getenv("FITBIT_CLIENT_ID"),
			ClientSecret: os.Getenv("FITBIT_CLIENT_SECRET"),
			Scopes:       []string{"activity", "profile", "heartrate"},
		}
	case "spotify":
		return &ProviderConfig{
			AuthURL:      "https://accounts.spotify.com/authorize",
			TokenURL:     "https://accounts.spotify.com/api/token",
			ClientID:     os.Getenv("SPOTIFY_CLIENT_ID"),
			ClientSecret: os.Getenv("SPOTIFY_CLIENT_SECRET"),
			Scopes:       []string{"user-read-recently-played", "user-read-playback-state"},
		}
	case "github":
		return &ProviderConfig{
			AuthURL:      "https://github.com/login/oauth/authorize",
			TokenURL:     "https://github.com/login/oauth/access_token",
			ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
			ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
			Scopes:       []string{"read:user"},
		}
	}
	return nil
}

// Providers lists the OAuth provider names Provider accepts, sorted.
func Providers() []string {
	names := []string{"fitbit", "github", "spotify", "strava"}
	sort.Strings(names)
	return names
}

// ScopeSeparator returns the separator a provider expects between scopes.
// Strava requires commas; everyone else follows RFC 6749 (space).
func ScopeSeparator(provider string) string {
	if provider == "strava" {
		return ","
	}
	return " "
}

// PublicBaseURL returns the origin the provider must redirect back to, with
// no trailing slash. FITGLUE_PUBLIC_BASE_URL wins; API_URL is the fallback
// api-client has always read. Neither set is an error: a redirect_uri derived
// from the request's Host header would be open to Host-header injection.
func PublicBaseURL() (string, error) {
	for _, env := range []string{PublicBaseURLEnv, LegacyBaseURLEnv} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return strings.TrimRight(v, "/"), nil
		}
	}
	return "", errors.New(PublicBaseURLEnv + " (or " + LegacyBaseURLEnv + ") must be set")
}

// CallbackURL is the redirect_uri for a provider: <base>/api/v2/oauth/<provider>/callback.
func CallbackURL(baseURL, provider string) string {
	return strings.TrimRight(baseURL, "/") + CallbackPathPrefix + provider + "/callback"
}

// SignState builds the state parameter: base64url(JSON{uid,ts}) "." hex(HMAC-SHA256).
// The timestamp is what the callback uses to expire the state (StateTTL).
func SignState(secret []byte, uid string, now time.Time) string {
	payload, _ := json.Marshal(map[string]string{
		"uid": uid,
		"ts":  strconv.FormatInt(now.Unix(), 10),
	})
	return base64.URLEncoding.EncodeToString(payload) + "." + sign(secret, payload)
}

func sign(secret, payload []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// State verification failures. The callback maps each to the error code it
// has always redirected with, so the mapping must stay one-to-one.
var (
	ErrStateFormat     = errors.New("invalid_state")
	ErrStateEncoding   = errors.New("invalid_state_encoding")
	ErrStateSignature  = errors.New("invalid_state_signature")
	ErrStatePayload    = errors.New("invalid_state_payload")
	ErrStateExpired    = errors.New("state_token_expired")
	ErrStateMissingUID = errors.New("missing_uid")
)

// VerifyState checks a state parameter produced by SignState and returns the
// uid it carries. Checks run in the same order as the original handler:
// shape, base64, signature, JSON, age, uid.
func VerifyState(secret []byte, state string, now time.Time) (string, error) {
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return "", ErrStateFormat
	}
	payload, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrStateEncoding
	}
	if !hmac.Equal([]byte(parts[1]), []byte(sign(secret, payload))) {
		return "", ErrStateSignature
	}
	var data map[string]string
	if err := json.Unmarshal(payload, &data); err != nil {
		return "", ErrStatePayload
	}
	ts, _ := strconv.ParseInt(data["ts"], 10, 64)
	if now.Unix()-ts > int64(StateTTL/time.Second) {
		return "", ErrStateExpired
	}
	if data["uid"] == "" {
		return "", ErrStateMissingUID
	}
	return data["uid"], nil
}

// AuthorizeURL builds the provider's authorise URL for a signed state and a
// redirect_uri. Query keys are emitted sorted (url.Values.Encode), which is
// what api-client has always sent.
func AuthorizeURL(provider string, cfg *ProviderConfig, redirectURI, state string) (string, error) {
	if cfg == nil {
		return "", errors.New("unsupported provider: " + provider)
	}
	u, err := url.Parse(cfg.AuthURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	if len(cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(cfg.Scopes, ScopeSeparator(provider)))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Connect is the whole start of the flow for one user: sign a state for uid
// now, derive the callback from the public base URL, and build the authorise
// URL. It is what both the api-client handler and fitgluectl call.
func Connect(provider, uid string, secret []byte, now time.Time) (string, error) {
	cfg := Provider(provider)
	if cfg == nil {
		return "", errors.New("unsupported provider: " + provider)
	}
	base, err := PublicBaseURL()
	if err != nil {
		return "", err
	}
	return AuthorizeURL(provider, cfg, CallbackURL(base, provider), SignState(secret, uid, now))
}
