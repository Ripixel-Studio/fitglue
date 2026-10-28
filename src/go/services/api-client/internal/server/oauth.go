package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/oauthflow"
	"github.com/fitglue/server/src/go/pkg/integrations/googlehealth"
	userpb "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/structpb"
)

// webURL returns the WEB_URL env var, falling back to local dev and appending the /app basename.
func webURL() string {
	url := os.Getenv("WEB_URL")
	if url == "" {
		url = "http://localhost:5173"
	}
	return strings.TrimRight(url, "/") + "/app"
}

// apiURL returns the base API URL for OAuth callbacks (FITGLUE_PUBLIC_BASE_URL,
// else API_URL). One of them must be set; falling back to the request Host
// header is unsafe (Host header injection).
func apiURL() string {
	base, err := oauthflow.PublicBaseURL()
	if err != nil {
		panic(err.Error())
	}
	return base
}

// stateSecret is the HMAC key shared with fitgluectl connect.
func stateSecret() []byte {
	return []byte(os.Getenv(oauthflow.StateSecretEnv))
}

func (s *APIServer) registerOAuthRoutes(r chi.Router) {
	// OAuth endpoints require user authentication because we need to know WHICH user is connecting the integration
	r.Post("/users/me/integrations/{provider}/connect", s.handleOAuthConnect)
}

func (s *APIServer) handleOAuthConnect(w http.ResponseWriter, r *http.Request) {
	token := getUserToken(r)
	if token == nil {
		WriteError(w, statusError(http.StatusUnauthorized, "missing user context"))
		return
	}

	provider := chi.URLParam(r, "provider")
	config := oauthflow.Provider(provider)
	if config == nil {
		WriteError(w, statusError(http.StatusBadRequest, "unsupported provider"))
		return
	}

	state := oauthflow.SignState(stateSecret(), token.UID, time.Now())
	authURL, err := oauthflow.AuthorizeURL(provider, config, oauthflow.CallbackURL(apiURL(), provider), state)
	if err != nil {
		WriteError(w, statusError(http.StatusInternalServerError, "failed to build authorise url"))
		return
	}

	WriteJSON(w, map[string]string{"url": authURL})
}

func (s *APIServer) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	config := oauthflow.Provider(provider)
	if config == nil {
		http.Redirect(w, r, webURL()+"/connections?error=unsupported_provider", http.StatusFound)
		return
	}

	code := r.URL.Query().Get("code")
	stateArg := r.URL.Query().Get("state")
	errDesc := r.URL.Query().Get("error_description")

	if errDesc != "" {
		http.Redirect(w, r, webURL()+"/connections?error="+url.QueryEscape(errDesc), http.StatusFound)
		return
	}

	if code == "" || stateArg == "" {
		http.Redirect(w, r, webURL()+"/connections?error=missing_code_or_state", http.StatusFound)
		return
	}

	// Verify state. VerifyState only ever returns its Err* sentinels, whose
	// text is the redirect error code this handler has always emitted
	// (invalid_state, invalid_state_encoding, invalid_state_signature,
	// invalid_state_payload, state_token_expired, missing_uid).
	userID, err := oauthflow.VerifyState(stateSecret(), stateArg, time.Now())
	if err != nil {
		http.Redirect(w, r, webURL()+"/connections?error="+err.Error(), http.StatusFound)
		return
	}

	// Exchange code for tokens
	redirectURI := oauthflow.CallbackURL(apiURL(), provider)
	data := url.Values{}
	data.Set("client_id", config.ClientID)
	data.Set("client_secret", config.ClientSecret)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", redirectURI)

	req, _ := http.NewRequest("POST", config.TokenURL, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	if provider == "fitbit" || provider == "spotify" {
		req.SetBasicAuth(config.ClientID, config.ClientSecret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Redirect(w, r, webURL()+"/connections?error=token_exchange_failed", http.StatusFound)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Redirect(w, r, webURL()+"/connections?error=token_exchange_error", http.StatusFound)
		return
	}

	var tokenResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		http.Redirect(w, r, webURL()+"/connections?error=invalid_token_response", http.StatusFound)
		return
	}

	// Enrich with standard connection metadata
	tokenResp["enabled"] = true
	tokenResp["consent_given"] = true
	tokenResp["created_at"] = time.Now().UTC().Format(time.RFC3339)

	// Normalize expires_at or expires_in
	if exp, ok := tokenResp["expires_at"]; ok {
		switch v := exp.(type) {
		case float64:
			tokenResp["expires_at"] = time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
		case int64:
			tokenResp["expires_at"] = time.Unix(v, 0).UTC().Format(time.RFC3339)
		}
	} else if expIn, ok := tokenResp["expires_in"]; ok {
		switch v := expIn.(type) {
		case float64:
			tokenResp["expires_at"] = time.Now().Add(time.Duration(v) * time.Second).UTC().Format(time.RFC3339)
		case int64:
			tokenResp["expires_at"] = time.Now().Add(time.Duration(v) * time.Second).UTC().Format(time.RFC3339)
		}
	}

	// Provider specific mapping for user IDs
	identityErr := ""
	if provider == "strava" {
		if athlete, ok := tokenResp["athlete"].(map[string]interface{}); ok {
			if idFloat, ok := athlete["id"].(float64); ok {
				tokenResp["athlete_id"] = int64(idFloat)
			}
		}
	} else if provider == "fitbit" {
		if uid, ok := tokenResp["user_id"].(string); ok {
			tokenResp["fitbit_user_id"] = uid
		}
	} else if provider == "googlehealth" {
		// Google's token response has no user id. GET /v4/users/me/identity both
		// supplies one and proves the account is linked to the Google Health app;
		// store the tokens either way (they are valid) but send the browser to an
		// error naming the reason, so a missing link is seen at connect time and
		// not on the next activity.
		if errCode := googleHealthIdentity(r.Context(), tokenResp); errCode != "" {
			identityErr = errCode
		}
	}

	// Create protobuf Struct containing the tokens
	pbStruct, _ := structpb.NewStruct(tokenResp)
	_, err = s.userService.SetIntegration(r.Context(), &userpb.SetIntegrationRequest{
		UserId:          userID,
		Provider:        provider,
		IntegrationData: pbStruct,
	})

	if err != nil {
		s.logger.Error(r.Context(), "failed to save integration", "error", err)
		http.Redirect(w, r, webURL()+"/connections?error=failed_to_save_integration", http.StatusFound)
		return
	}

	if identityErr != "" {
		s.logger.Warn(r.Context(), "integration saved but identity check failed", "provider", provider, "error", identityErr)
		http.Redirect(w, r, webURL()+"/connections?error="+url.QueryEscape(identityErr), http.StatusFound)
		return
	}

	http.Redirect(w, r, webURL()+"/connections/"+provider+"/success", http.StatusFound)
}

// googleHealthBaseURL is the Google Health API origin the callback checks the
// new grant against (overridden in tests).
var googleHealthBaseURL = googlehealth.BaseURL

// googleHealthIdentity calls GET /v4/users/me/identity with the freshly
// issued access token and records healthUserId on tokenResp. It returns ""
// on success, else the error code for the callback redirect:
// googlehealth_account_not_linked (sign in to the Google Health app with this
// Google account, then connect again) or googlehealth_identity_<status|error>.
func googleHealthIdentity(ctx context.Context, tokenResp map[string]interface{}) string {
	token, _ := tokenResp["access_token"].(string)
	if token == "" {
		return "googlehealth_no_access_token"
	}
	httpClient := &http.Client{Timeout: 15 * time.Second, Transport: bearerTransport{token: token}}
	id, err := googlehealth.NewWithBaseURL(googleHealthBaseURL, httpClient).Identity(ctx)
	if err != nil {
		var apiErr *googlehealth.APIError
		if errors.As(err, &apiErr) {
			if apiErr.Reason == "ACCOUNT_NOT_LINKED" {
				return "googlehealth_account_not_linked"
			}
			return "googlehealth_identity_" + strconv.Itoa(apiErr.StatusCode)
		}
		return "googlehealth_identity_error"
	}
	tokenResp["health_user_id"] = id.HealthUserID
	return ""
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}
