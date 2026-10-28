// nolint:proto-json
package enricher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/infrastructure/oauth"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// SourceDescriptionFetcher returns the current description of a source activity as the
// source platform holds it now. Production wiring (function.go) sets a Strava fetcher;
// NewOrchestrator leaves it nil so unit tests never make external calls.
type SourceDescriptionFetcher func(ctx context.Context, userID string, source pbactivity.ActivitySource, externalID string) (string, error)

// sourceRefreshTimeout bounds the refresh GET: it is best-effort and must not stall the run.
const sourceRefreshTimeout = 10 * time.Second

// refreshSourceDescription re-reads a Strava-sourced activity's description before the
// payload is persisted and the description slots are built, and adopts it as the
// original description when it differs.
//
// Why: an activity created on Strava by a third-party app (ROXFIT writes official Hyrox
// splits) may gain its description seconds after the create webhook, after FitGlue's
// ingest read it as "". The same-source Strava update then overwrites the description
// with FitGlue's own text, built on an empty slot 0, and the official splits are lost.
//
// Guard: only while FitGlue has not written back to that Strava activity. Once it has,
// Strava's description is (partly) FitGlue's own output, and adopting it would stack
// sections on every repost (Rule E22, Reset-on-Repost). "Written" is any of:
//   - a SUCCESS Strava outcome on this pipeline run (run.Destinations or the
//     destination-outcomes subcollection),
//   - an UploadedActivity (loop-prevention) record for strava:<id> — the destination
//     executor writes it before every Strava Create/Update from any pipeline or run, so
//     it also covers other pipelines and earlier runs of the same activity,
//   - a repost (Magic Action) — those replay the stored clean payload by design.
//
// Fails open: a fetch error is logged and the run continues with what it had.
//
// Returns whether FitGlue had already written to the source activity, and whether the
// question applied at all (false for non-Strava sources).
func (o *Orchestrator) refreshSourceDescription(ctx context.Context, logger *slog.Logger, payload *pbevents.ActivityPayload, pipelineExecutionID string, existingRun *pbpipeline.PipelineRun) (written bool, applies bool) {
	if payload.Source != pbactivity.ActivitySource_SOURCE_STRAVA || payload.StandardizedActivity == nil {
		return false, false
	}
	externalID := payload.StandardizedActivity.GetExternalId()
	if externalID == "" {
		return false, false
	}

	written = payload.IsRepost || o.fitglueWroteStrava(ctx, logger, payload.UserId, pipelineExecutionID, externalID, existingRun)
	if written {
		logger.Info("Source description refresh skipped: FitGlue has already written to this Strava activity",
			"strava_id", externalID, "is_repost", payload.IsRepost)
		return true, true
	}
	if o.fetchSourceDescription == nil || payload.GetMetadata()["backfill_verbatim_description"] == "true" {
		return false, true
	}

	fetchCtx, cancel := context.WithTimeout(ctx, sourceRefreshTimeout)
	defer cancel()
	current, err := o.fetchSourceDescription(fetchCtx, payload.UserId, payload.Source, externalID)
	if err != nil {
		logger.Warn("Source description refresh failed, continuing with the ingested description",
			"strava_id", externalID, "error", err)
		return false, true
	}
	if strings.TrimSpace(current) == "" || current == payload.StandardizedActivity.Description {
		return false, true
	}
	logger.Info("Source description refreshed from Strava",
		"strava_id", externalID,
		"ingested_len", len(payload.StandardizedActivity.Description),
		"current_len", len(current))
	payload.StandardizedActivity.Description = current
	return false, true
}

// fitglueWroteStrava reports whether FitGlue has written (or attempted to write) to the
// Strava activity: see refreshSourceDescription for the signals. Lookup errors count as
// "not written" — the destination-side guard in the Strava uploader still applies.
func (o *Orchestrator) fitglueWroteStrava(ctx context.Context, logger *slog.Logger, userID, pipelineExecutionID, stravaID string, run *pbpipeline.PipelineRun) bool {
	isStravaSuccess := func(d *pbpipeline.DestinationOutcome) bool {
		return d.GetDestination() == pbplugin.DestinationType_DESTINATION_STRAVA &&
			d.GetStatus() == pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS
	}
	for _, d := range run.GetDestinations() {
		if isStravaSuccess(d) {
			return true
		}
	}
	if pipelineExecutionID != "" {
		outcomes, err := o.database.GetDestinationOutcomes(ctx, userID, pipelineExecutionID)
		if err != nil {
			logger.Warn("Source description refresh: destination outcome lookup failed", "error", err)
		}
		for _, d := range outcomes {
			if isStravaSuccess(d) {
				return true
			}
		}
	}
	rec, err := o.database.GetUploadedActivity(ctx, userID, pbplugin.DestinationType_DESTINATION_STRAVA, stravaID)
	if err != nil {
		logger.Warn("Source description refresh: uploaded-activity lookup failed", "error", err)
		return false
	}
	return rec != nil
}

// NewStravaSourceDescriptionFetcher reads an activity's description from the Strava API
// with the user's stored OAuth token (same token source as the Strava uploader).
func NewStravaSourceDescriptionFetcher(svc *bootstrap.Service) SourceDescriptionFetcher {
	return func(ctx context.Context, userID string, source pbactivity.ActivitySource, externalID string) (string, error) {
		if source != pbactivity.ActivitySource_SOURCE_STRAVA {
			return "", fmt.Errorf("unsupported source %s", source)
		}
		tokenSource := oauth.NewFirestoreTokenSource(svc, userID, "strava")
		client := oauth.NewClientWithUsageTracking(tokenSource, svc, userID, "strava", infra.NewLogger())
		return fetchStravaDescription(ctx, client, stravaAPIBase, externalID)
	}
}

// stravaAPIBase is the Strava REST API base URL (as in the Strava uploader).
const stravaAPIBase = "https://www.strava.com/api/v3"

func fetchStravaDescription(ctx context.Context, client *http.Client, base, activityID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/activities/%s", base, activityID), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GET activity: status %d: %s", resp.StatusCode, string(body))
	}
	var act struct {
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&act); err != nil {
		return "", fmt.Errorf("decode activity: %w", err)
	}
	if act.Description == nil {
		return "", nil
	}
	return *act.Description, nil
}
