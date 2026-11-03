// nolint:proto-json
package strava

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	stravaapi "github.com/fitglue/server/src/go/pkg/api/strava"
	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
)

// APIBase is the Strava REST API base URL.
// Changing to https://www.api-v3.strava.com is required before June 1, 2027.
const APIBase = "https://www.strava.com/api/v3"

// FetchPayload reads one activity (summary plus best-effort streams) with an
// already-valid access token and returns the ActivityPayload the Strava webhook
// publishes to topic-raw-activity (the caller adds the pipeline execution id).
// Shared by the webhook and `fitgluectl reconcile`.
func FetchPayload(ctx context.Context, baseURL, accessToken, activityID, userID string) (*pbevents.ActivityPayload, error) {
	client, err := stravaapi.NewClientWithResponses(
		baseURL,
		stravaapi.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+accessToken)
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create strava client: %w", err)
	}

	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity id %q: %w", activityID, err)
	}

	actResp, err := client.GetActivityByIdWithResponse(ctx, id, &stravaapi.GetActivityByIdParams{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch strava activity: %w", err)
	}
	if actResp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("strava api error: status=%d body=%s", actResp.StatusCode(), string(actResp.Body))
	}

	// Time-series streams — best-effort, nil if unavailable or not permitted
	var streams *stravaapi.StreamSet
	streamResp, streamErr := client.GetActivityStreamsWithResponse(ctx, id, &stravaapi.GetActivityStreamsParams{
		Keys:      StreamKeys,
		KeyByType: true,
	})
	if streamErr == nil && streamResp.StatusCode() == http.StatusOK && streamResp.JSON200 != nil {
		streams = streamResp.JSON200
	}

	stdActivity, err := MapToStandardizedActivity(actResp.Body, userID, streams)
	if err != nil {
		return nil, fmt.Errorf("failed to parse strava activity: %w", err)
	}
	aid := activityID
	return &pbevents.ActivityPayload{
		Source:               activitypb.ActivitySource_SOURCE_STRAVA,
		UserId:               userID,
		OriginalPayloadJson:  string(actResp.Body),
		ActivityId:           &aid,
		StandardizedActivity: stdActivity,
	}, nil
}
