package hevy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
)

// APIBase is Hevy's public API.
const APIBase = "https://api.hevyapp.com"

// FetchPayload GETs one workout with the user's Hevy API key and returns the
// ActivityPayload the webhook publishes to topic-raw-activity (the caller adds the
// pipeline execution id). baseURL is APIBase outside tests.
func FetchPayload(ctx context.Context, client *http.Client, baseURL, apiKey, workoutID, userID string) (*pbevents.ActivityPayload, error) {
	if workoutID == "" {
		return nil, fmt.Errorf("missing workout id for hevy activity fetch")
	}
	if client == nil {
		client = http.DefaultClient
	}
	url := fmt.Sprintf("%s/v1/workouts/%s", strings.TrimRight(baseURL, "/"), workoutID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("api-key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch hevy activity: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hevy api error: status=%d body=%s", resp.StatusCode, string(body))
	}
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	stdActivity, err := MapToStandardizedActivity(rawBody, userID, activitypb.ActivitySource_SOURCE_HEVY)
	if err != nil {
		return nil, fmt.Errorf("failed to parse hevy workout to standardized activity: %w", err)
	}
	id := workoutID
	return &pbevents.ActivityPayload{
		Source:               activitypb.ActivitySource_SOURCE_HEVY,
		UserId:               userID,
		OriginalPayloadJson:  string(rawBody),
		ActivityId:           &id,
		StandardizedActivity: stdActivity,
	}, nil
}
