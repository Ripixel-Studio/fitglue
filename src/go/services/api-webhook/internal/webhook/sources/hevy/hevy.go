// nolint:proto-json
package hevy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	hevymap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/hevy"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	userpb "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
	"github.com/fitglue/server/src/go/services/api-webhook/internal/webhook"
)

// Provider implements webhook.SourceProvider for Hevy
type Provider struct{}

// NewProvider creates a new Hevy SourceProvider
func NewProvider() *Provider {
	return &Provider{}
}

// ID returns the provider identifier
func (p *Provider) ID() string {
	return "hevy"
}

// VerifySubscription handles Hevy webhook verification
func (p *Provider) VerifySubscription(w http.ResponseWriter, r *http.Request) {
	// Hevy does not have a challenge-response verification.
	// Users just paste the URL in their app settings.
	w.WriteHeader(http.StatusOK)
}

type hevyPayload struct {
	WorkoutID string `json:"workoutId"`
	Payload   struct {
		WorkoutID string `json:"workoutId"`
	} `json:"payload"`
}

// ParseEvent extracts events from a Hevy webhook
func (p *Provider) ParseEvent(r *http.Request) ([]*webhook.WebhookEvent, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}

	var payload hevyPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}

	workoutID := payload.WorkoutID
	if payload.Payload.WorkoutID != "" {
		workoutID = payload.Payload.WorkoutID
	}

	if workoutID == "" {
		// Possibly not a workout event, ignore
		return nil, nil
	}

	// Hevy uses Personal API Keys for webhooks, so we extract it from headers or query params
	apiKey := r.Header.Get("X-Api-Key")
	if apiKey == "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" && len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			apiKey = authHeader[7:]
		} else if authHeader != "" {
			apiKey = authHeader // raw key support
		}
	}
	if apiKey == "" {
		apiKey = r.URL.Query().Get("api_key")
	}
	if apiKey == "" {
		apiKey = r.URL.Query().Get("key")
	}

	if apiKey == "" {
		return nil, fmt.Errorf("missing api key")
	}

	evt := &webhook.WebhookEvent{
		Provider:    p.ID(),
		ProviderUID: apiKey, // Use API key to resolve the user
		ActivityID:  workoutID,
		Event:       "create_or_update", // Hevy typically sends newly logged workouts
		RawPayload:  body,
	}

	return []*webhook.WebhookEvent{evt}, nil
}

func (p *Provider) FetchActivity(ctx context.Context, userSvc userpb.UserServiceClient, internalUserID string, evt *webhook.WebhookEvent) (*pbevents.ActivityPayload, error) {
	workoutID := evt.ActivityID
	if workoutID == "" {
		return nil, fmt.Errorf("missing workout id for hevy activity fetch")
	}

	// 1. Fetch Hevy tokens for user
	integResp, err := userSvc.GetIntegration(ctx, &userpb.GetIntegrationRequest{
		UserId:   internalUserID,
		Provider: p.ID(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get integration for user: %w", err)
	}

	hevyInteg := integResp.Integrations.Hevy
	if hevyInteg == nil || hevyInteg.ApiKey == "" {
		return nil, fmt.Errorf("hevy integration not found or api key missing")
	}

	// 2. Fetch the workout and map it (shared with `fitgluectl reconcile`)
	return hevymap.FetchPayload(ctx, nil, hevymap.APIBase, hevyInteg.ApiKey, workoutID, internalUserID)
}
