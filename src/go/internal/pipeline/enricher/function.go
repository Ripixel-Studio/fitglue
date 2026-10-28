// nolint:proto-json
package enricher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	"github.com/google/uuid"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"
	"google.golang.org/protobuf/encoding/protojson"

	shared "github.com/fitglue/server/src/go/pkg"
	"github.com/fitglue/server/src/go/pkg/bootstrap"

	activityPkg "github.com/fitglue/server/src/go/pkg/domain/activity"
	"github.com/fitglue/server/src/go/pkg/framework"

	infrapubsub "github.com/fitglue/server/src/go/pkg/infrastructure/pubsub"

	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"

	// Register providers
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/activity_filter"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_activity_type"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_banner"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_companion"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/auto_increment"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/best_efforts"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/cadence_summary"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/calories_burned"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/condition_matcher"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/distance_milestones"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/effort_score"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/elevation_summary"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/fit_file_heart_rate"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/fitbit_heart_rate"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/goal_tracker"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/hdrop"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/heart_rate_summary"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/heart_rate_zones"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/hybrid_race_tagger"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ical_title"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/intervals"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/location_naming"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/location_pinner"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/logic_gate"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/manual_workout_entry"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/mock"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/muscle_heatmap"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/muscle_heatmap_image"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/pace_summary"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/parkrun"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/personal_records"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/photo_upload"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/power_summary"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/recovery_advisor"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/route_thumbnail"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/running_dynamics"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/source_link"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/speed_summary"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/spotify_tracks"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/streak_tracker"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/temperature_summary"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/training_load"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/type_mapper"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/user_input"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/virtual_gps"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/weather"
	_ "github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/workout_summary"
)

var (
	svc     *bootstrap.Service
	svcOnce sync.Once
	svcErr  error
)

func init() {
	// CloudEvent handler for EventArc triggers (raw-activity topic)
	functions.CloudEvent("EnrichActivity", EnrichActivity)

	// HTTP handler for push subscriptions (lag topic) - properly returns HTTP 500 on error
	functions.HTTP("EnrichActivityHTTP", EnrichActivityHTTP)
}

func initService(ctx context.Context) (*bootstrap.Service, error) {
	if svc != nil {
		return svc, nil
	}
	svcOnce.Do(func() {
		svc, svcErr = bootstrap.NewService(ctx)
	})
	return svc, svcErr
}

// EnrichActivity is the entry point for EventArc triggers
func EnrichActivity(ctx context.Context, e cloudevents.Event) error {
	svc, err := initService(ctx)
	if err != nil {
		return fmt.Errorf("service init failed: %v", err)
	}
	return framework.WrapCloudEvent("enricher", svc, enrichHandler)(ctx, e)
}

// EnrichActivityHTTP is the HTTP handler for push subscriptions (lag topic).
// This handler properly returns HTTP 500 on errors, allowing Pub/Sub to NACK and retry.
func EnrichActivityHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	svc, err := initService(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("service init failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse CloudEvent from request
	// Try CloudEvents format first (structured or binary)
	event, err := cehttp.NewEventFromHTTPRequest(r)
	if err != nil {
		// Fall back to Pub/Sub push message format
		event, err = parseCloudEventFromPubSubPush(r)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to parse event: %v", err), http.StatusBadRequest)
			return
		}
	}

	// Call the existing CloudEvent handler
	handlerErr := framework.WrapCloudEvent("enricher", svc, enrichHandler)(ctx, *event)

	if handlerErr != nil {
		// Return HTTP 500 to trigger Pub/Sub NACK and retry
		http.Error(w, handlerErr.Error(), http.StatusInternalServerError)
		return
	}

	// Success - Pub/Sub will ACK
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// parseCloudEventFromPubSubPush parses a CloudEvent from a Pub/Sub push message.
// Pub/Sub push sends messages in a wrapper format with message.data containing the actual event.
func parseCloudEventFromPubSubPush(r *http.Request) (*cloudevents.Event, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	defer r.Body.Close()

	// Pub/Sub push format: {"message": {"data": "base64...", "messageId": "...", ...}, "subscription": "..."}
	var pushMsg struct {
		Message struct {
			Data        []byte            `json:"data"`
			Attributes  map[string]string `json:"attributes"`
			MessageID   string            `json:"messageId"`
			PublishTime string            `json:"publishTime"`
		} `json:"message"`
		Subscription string `json:"subscription"`
	}

	if err := json.Unmarshal(body, &pushMsg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal push message: %w", err)
	}

	if len(pushMsg.Message.Data) == 0 {
		return nil, fmt.Errorf("no data in push message")
	}

	// The data might be a CloudEvent JSON or just the payload
	// Try to parse as CloudEvent first
	var event cloudevents.Event
	if err := json.Unmarshal(pushMsg.Message.Data, &event); err == nil && event.Type() != "" {
		return &event, nil
	}

	// If not a CloudEvent, create one from the raw data
	event = cloudevents.NewEvent()
	event.SetID(pushMsg.Message.MessageID)
	event.SetSource(infrapubsub.GetCloudEventSource(pbevents.CloudEventSource_CLOUD_EVENT_SOURCE_ENRICHER))
	event.SetType(infrapubsub.GetCloudEventType(pbevents.CloudEventType_CLOUD_EVENT_TYPE_ENRICHMENT_LAG))
	event.SetData(cloudevents.ApplicationJSON, pushMsg.Message.Data)

	// Copy attributes as extensions
	for k, v := range pushMsg.Message.Attributes {
		event.SetExtension(k, v)
	}

	return &event, nil
}

// enrichHandler contains the business logic
func enrichHandler(ctx context.Context, e cloudevents.Event, fwCtx *framework.FrameworkContext) (interface{}, error) {
	// Extract payload and attributes
	// We assume strict CloudEvent input (legacy Pub/Sub messages are no longer supported)
	rawData := e.Data()
	isLagRetry := false
	if val, ok := e.Extensions()["origin"].(string); ok && val == "lag-queue" {
		isLagRetry = true
	}

	var rawEvent pbevents.ActivityPayload
	// Use protojson to unmarshal, which supports both camelCase (canonical) and snake_case field names
	unmarshalOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true, // Be resilient to future schema changes
	}
	if err := unmarshalOpts.Unmarshal(rawData, &rawEvent); err != nil {
		return nil, fmt.Errorf("protojson unmarshal: %v", err)
	}

	if rawEvent.UserId == "" {
		return nil, fmt.Errorf("missing userId in payload")
	}

	fwCtx.Logger.Info("Starting enrichment", "timestamp", rawEvent.Timestamp, "source", rawEvent.Source)

	// Extract pipeline_execution_id from payload; generate a fresh UUID if absent.
	// Falling back to fwCtx.ExecutionID is unsafe: it is a timestamp-based ID shared
	// across concurrent enricher invocations, causing GCS blob path collisions that
	// corrupt the showcase enrichment data of an unrelated activity.
	pipelineExecID := rawEvent.PipelineExecutionId
	if pipelineExecID == nil || *pipelineExecID == "" {
		newID := uuid.NewString()
		pipelineExecID = &newID
		fwCtx.Logger.Warn("pipeline_execution_id missing from payload, generated fallback", "fallback_id", newID)
	}

	// Initialize Orchestrator
	bucketName := fwCtx.Service.Config.GCSArtifactBucket
	if bucketName == "" {
		bucketName = "fitglue-server-dev-artifacts" // Fallback for local development
	}

	orchestrator := NewOrchestrator(fwCtx.Service.DB, fwCtx.Service.Store, bucketName)
	// Enable reverse geocoding for the always-on implicit location step so GPS-tracked
	// activities get a real place name (showcase page + roundup "where it happened"), not just
	// coordinates. Unit tests leave this nil to stay offline.
	orchestrator.geocode = location_naming.ReverseGeocode
	// Re-read a Strava source activity's description before building the description, so
	// text a third-party app (ROXFIT) added after ingest is kept, not overwritten.
	orchestrator.fetchSourceDescription = NewStravaSourceDescriptionFetcher(fwCtx.Service)

	// Register Providers from registry
	for _, provider := range providers.GetAll() {
		// Set service if the provider supports it
		if sp, ok := provider.(interface{ SetService(*bootstrap.Service) }); ok {
			sp.SetService(fwCtx.Service)
		}
		orchestrator.Register(provider)
	}

	// Honour a provider-requested retry window (e.g. a Retry-After from a 429):
	// if the lag message came back before its window elapsed, NACK without touching
	// the rate-limited upstream so Pub/Sub redelivers later with backoff.
	if isLagRetry {
		if notBefore, ok := parseTimeExtension(e, extRetryNotBefore); ok && time.Now().Before(notBefore) {
			wait := time.Until(notBefore).Round(time.Second)
			fwCtx.Logger.Info("Retry window not yet elapsed, deferring", "not_before", notBefore, "wait", wait)
			return map[string]interface{}{
				"status": "WAITING_RETRY_WINDOW",
				"reason": fmt.Sprintf("retry window elapses at %s", notBefore.Format(time.RFC3339)),
			}, framework.NewRetryableError("retry window not yet elapsed", fmt.Errorf("waiting %s until %s", wait, notBefore.Format(time.RFC3339)))
		}
	}

	// Calculate lag exhaustion (Force mode / Do Not Retry).
	// Primary signal: the lagdeadline extension stamped when the message was
	// offloaded to the lag queue (retry window + lagExhaustionWindow of attempts).
	// Fallback: event age vs lagExhaustionWindow, for messages without the extension.
	doNotRetry := false
	if deadline, ok := parseTimeExtension(e, extLagDeadline); ok {
		if time.Now().After(deadline) {
			fwCtx.Logger.Warn("Activity lag deadline passed, forcing partial enrichment", "deadline", deadline)
			doNotRetry = true
		}
	} else if !e.Time().IsZero() {
		// For Pub/Sub events, e.Time() is the publish time.
		// Note: For unwrapped events, e.Time() is the original event time, which is what we want.
		lagDuration := time.Since(e.Time())
		if lagDuration > lagExhaustionWindow {
			fwCtx.Logger.Warn("Activity lag exhausted, forcing partial enrichment", "age", lagDuration)
			doNotRetry = true
		}
	}

	// Process
	processResult, err := orchestrator.Process(ctx, fwCtx.Logger, &rawEvent, fwCtx.ExecutionID, *pipelineExecID, doNotRetry)

	if err != nil {
		// Check if the error is retryable (e.g. data lag)
		if retryErr := asRetryable(err); retryErr != nil {

			if isLagRetry {
				fwCtx.Logger.Warn("Lag Retry failed (will retry with backoff)", "error", err)
				// Return error to trigger Pub/Sub retry with backoff (keep status for execution tracking)
				fwCtx.Logger.Info("Returning error to trigger retry", "status", "STATUS_LAGGED_RETRY")
				// Return a RetryableError (not a plain error) so the framework NACKs for
				// Pub/Sub backoff without reporting this expected lag retry to Sentry.
				return map[string]interface{}{
					"status": "STATUS_LAGGED_RETRY",
					"error":  err.Error(),
				}, framework.NewRetryableError("lagged retry failed (status=STATUS_LAGGED_RETRY)", err)
			} else {
				// Preserve the original error before it gets shadowed
				originalErr := err
				fwCtx.Logger.Info("Activity data lagging, offloading to lag queue", "error", originalErr)

				// Publish to Lag Topic with "origin=lag-queue" to break infinite loop on next consumption
				// Create CloudEvent
				lagEvent, err := infrapubsub.NewCloudEvent("/enricher", "com.fitglue.enrichment.lag", rawData)
				if err != nil {
					fwCtx.Logger.Error("Failed to create lag event", "error", err)
					return nil, err
				}
				lagEvent.SetExtension("origin", "lag-queue")

				// Honour the provider's requested backoff (e.g. Retry-After on a 429):
				// stamp when the lag message may next be processed, and how long past
				// that window we keep retrying before forcing partial enrichment.
				now := time.Now()
				notBefore := now
				if retryErr.RetryAfter > 0 {
					notBefore = now.Add(retryErr.RetryAfter)
				}
				lagEvent.SetExtension(extRetryNotBefore, notBefore.Format(time.RFC3339))
				lagEvent.SetExtension(extLagDeadline, notBefore.Add(lagExhaustionWindow).Format(time.RFC3339))

				_, pubErr := fwCtx.Service.Pub.PublishCloudEvent(ctx, shared.TopicEnrichmentLag, lagEvent)
				if pubErr != nil {
					fwCtx.Logger.Error("Failed to publish to lag topic", "error", pubErr)
					return nil, pubErr // Fail execution to trigger retry of this offload attempt
				}

				return map[string]interface{}{
					"status": "LAGGED_RETRY",
					"reason": originalErr.Error(),
				}, nil // ACK original message since we've successfully moved it to the delay queue
			}
		}

		// Non-retryable failure: the pipeline run is already marked FAILED in Firestore.
		// ACK the message so Pub/Sub doesn't retry endlessly — returning a plain error
		// would NACK and cause infinite re-delivery for permanent failures (bad data,
		// bad user input, etc.). Use TerminalError so the framework wrapper logs it to
		// Sentry but still ACKs.
		fwCtx.Logger.Error("Orchestrator failed (terminal)", "error", err)
		return nil, framework.NewTerminalError(err.Error())
	}

	if len(processResult.Events) == 0 {
		fwCtx.Logger.Info("No pipelines matched, skipping enrichment")
		return map[string]interface{}{
			"status":              "SKIPPED",
			"reason":              "No enriched event created - possibly halted by a provider",
			"published_events":    []interface{}{},
			"provider_executions": processResult.ProviderExecutions,
		}, nil
	}

	// Publish Results to Router
	var publishedCount int

	// Track published events for rich output
	type PublishedEvent struct {
		ActivityID         string   `json:"activity_id"`
		PipelineID         string   `json:"pipeline_id"`
		Destinations       []string `json:"destinations"`
		AppliedEnrichments []string `json:"applied_enrichments"`
		FitFileURI         string   `json:"fit_file_uri,omitempty"`
		PubSubMessageID    string   `json:"pubsub_message_id"`
	}
	publishedEvents := []PublishedEvent{}

	for _, event := range processResult.Events {
		// Propagate pipeline execution ID
		event.PipelineExecutionId = pipelineExecID

		// Always offload activity data to GCS for consistent behavior
		// This ensures all destinations (especially Showcase) have access to the data
		eventToPublish := event
		bucketName := fwCtx.Service.Config.GCSArtifactBucket
		if bucketName != "" {
			preparedEvent, uploadedSize, err := activityPkg.PrepareForPublish(ctx, event, fwCtx.Service.Store, bucketName)
			if err != nil {
				fwCtx.Logger.Warn("Failed to offload activity data to GCS, publishing inline", "error", err)
			} else if uploadedSize > 0 {
				eventToPublish = preparedEvent
				fwCtx.Logger.Info("Offloaded activity data to GCS",
					"uri", preparedEvent.ActivityDataUri,
					"size_bytes", uploadedSize)
			}
		}

		resultEvent, err := infrapubsub.NewCloudEvent("/enricher", "com.fitglue.activity.enriched", eventToPublish)
		if err != nil {
			fwCtx.Logger.Error("Failed to create result event", "error", err)
			continue
		}

		// Add as CloudEvent extension for framework to extract
		resultEvent.SetExtension("pipeline_execution_id", *pipelineExecID)

		msgID, err := fwCtx.Service.Pub.PublishCloudEvent(ctx, shared.TopicEnrichedActivity, resultEvent)
		if err != nil {
			fwCtx.Logger.Error("Failed to publish result", "error", err, "pipeline_id", event.PipelineId)
		} else {
			publishedCount++
			fwCtx.Logger.Info("Published enriched event",
				"activity_id", event.ActivityId,
				"pipeline_id", event.PipelineId,
				"destinations", event.Destinations,
				"message_id", msgID)

			publishedEvents = append(publishedEvents, PublishedEvent{
				ActivityID:         event.ActivityId,
				PipelineID:         event.PipelineId,
				Destinations:       destinationsToStrings(event.Destinations),
				AppliedEnrichments: event.AppliedEnrichments,
				FitFileURI:         event.FitFileUri,
				PubSubMessageID:    msgID,
			})
		}
	}

	fwCtx.Logger.Info("Enrichment complete", "published_count", publishedCount)

	finalStatus := "SUCCESS"
	if processResult.Status == pbpipeline.ExecutionStatus_STATUS_WAITING {
		finalStatus = "WAITING"
	}

	return map[string]interface{}{
		"status":              finalStatus,
		"published_count":     publishedCount,
		"total_events":        len(processResult.Events),
		"published_events":    publishedEvents,
		"provider_executions": processResult.ProviderExecutions,
	}, nil
}

// lagExhaustionWindow is how long we keep retrying past the (possibly
// provider-requested) retry window before forcing partial enrichment.
const lagExhaustionWindow = 30 * time.Minute

// CloudEvent extension attribute names (lowercase alphanumeric per spec).
const (
	// extRetryNotBefore marks the earliest time a lag message may be processed
	// (honours Retry-After from rate-limited upstreams).
	extRetryNotBefore = "retrynotbefore"
	// extLagDeadline marks when to stop retrying and force partial enrichment.
	extLagDeadline = "lagdeadline"
)

// asRetryable unwraps err to a *providers.RetryableError, or nil. Uses
// errors.As (not a type assertion) so wrapped retryable errors still count.
func asRetryable(err error) *providers.RetryableError {
	var re *providers.RetryableError
	if errors.As(err, &re) {
		return re
	}
	return nil
}

// parseTimeExtension reads an RFC3339 timestamp CloudEvent extension.
func parseTimeExtension(e cloudevents.Event, name string) (time.Time, bool) {
	raw, ok := e.Extensions()[name].(string)
	if !ok || raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func destinationsToStrings(dests []pbplugin.DestinationType) []string {
	strs := make([]string, len(dests))
	for i, d := range dests {
		strs[i] = d.String()
	}
	return strs
}
