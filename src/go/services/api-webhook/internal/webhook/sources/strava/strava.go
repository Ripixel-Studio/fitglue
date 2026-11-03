// nolint:proto-json
package strava

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	shared "github.com/fitglue/server/src/go/pkg"
	stravamap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/strava"
	"github.com/fitglue/server/src/go/pkg/infrastructure/oauth"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	userpb "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
	"github.com/fitglue/server/src/go/services/api-webhook/internal/webhook"
)

// stravaAPIBase is the Strava REST API base URL.
// Changing to https://www.api-v3.strava.com is required before June 1, 2027.
const stravaAPIBase = "https://www.strava.com/api/v3"

type Provider struct {
	verifyToken string
	db          shared.Database
}

func NewProvider(verifyToken string, db shared.Database) *Provider {
	if verifyToken == "" {
		panic("STRAVA_WEBHOOK_VERIFY_TOKEN must be set")
	}
	return &Provider{verifyToken: verifyToken, db: db}
}

func (p *Provider) ID() string {
	return "strava"
}

// VerifySubscription handles Strava's `hub.challenge` loop
func (p *Provider) VerifySubscription(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == p.verifyToken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"hub.challenge": challenge})
		return
	}

	http.Error(w, "Forbidden", http.StatusForbidden)
}

type stravaWebhookPayload struct {
	ObjectType string                 `json:"object_type"`
	ObjectID   int64                  `json:"object_id"`
	AspectType string                 `json:"aspect_type"`
	OwnerID    int64                  `json:"owner_id"`
	Updates    map[string]interface{} `json:"updates"`
}

// ParseEvent extracts events from a Strava webhook
func (p *Provider) ParseEvent(r *http.Request) ([]*webhook.WebhookEvent, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}

	var payload stravaWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}

	// We only process activity events for now.
	if payload.ObjectType != "activity" {
		// Return empty list so processor ignores it but returns 200 OK
		return nil, nil
	}

	evt := &webhook.WebhookEvent{
		Provider:    p.ID(),
		ProviderUID: fmt.Sprintf("%d", payload.OwnerID),
		ActivityID:  fmt.Sprintf("%d", payload.ObjectID),
		Event:       payload.AspectType, // "create", "update", "delete"
		RawPayload:  body,
	}

	return []*webhook.WebhookEvent{evt}, nil
}

func (p *Provider) FetchActivity(ctx context.Context, _ userpb.UserServiceClient, internalUserID string, evt *webhook.WebhookEvent) (*pbevents.ActivityPayload, error) {
	// 1. Get a valid (auto-refreshed) token via Firestore
	token, err := oauth.NewFirestoreTokenSourceFromDB(p.db, internalUserID, "strava").Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get strava token: %w", err)
	}

	// 2. Fetch the activity + streams and map it (shared with `fitgluectl reconcile`)
	return stravamap.FetchPayload(ctx, stravaAPIBase, token.AccessToken, evt.ActivityID, internalUserID)
}
