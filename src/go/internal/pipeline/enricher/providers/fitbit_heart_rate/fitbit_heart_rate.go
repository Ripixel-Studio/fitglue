// Package fitbit_heart_rate is the heart-rate enricher: it fills an activity's
// heart-rate stream from the wearable's own samples for the activity's
// interval.
//
// The samples come from the Google Health API (health.googleapis.com/v4,
// integration "googlehealth"), which replaced the Fitbit Web API when Google
// turned the latter off on 2 Nov 2026. The package, Name() and the
// ENRICHER_PROVIDER_FITBIT_HEART_RATE enum keep their Fitbit names on purpose:
// they are the identifiers stored in pipeline configs and run journals, and
// the watch is still a Fitbit/Google wearable.
package fitbit_heart_rate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/fitglue/server/src/go/pkg/domain/user"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/infrastructure/oauth"
	"github.com/fitglue/server/src/go/pkg/integrations/googlehealth"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// provider is the integration (Firestore key, oauthflow and token-source
// name) the samples are read with.
const provider = "googlehealth"

type FitBitHeartRate struct {
	Service *bootstrap.Service
	// baseURL overrides the Google Health origin (tests).
	baseURL string
}

func init() {
	providers.Register(NewFitBitHeartRate())
}

func NewFitBitHeartRate() *FitBitHeartRate {
	return &FitBitHeartRate{}
}

func (p *FitBitHeartRate) SetService(service *bootstrap.Service) {
	p.Service = service
}

func (p *FitBitHeartRate) Name() string {
	return "fitbit-heart-rate"
}

func (p *FitBitHeartRate) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_FITBIT_HEART_RATE
}

// IsIdempotent returns false: the fetched stream depends on the watch's own sync lag at
// query time, so re-running on resume can silently commit a less-complete stream than
// the one already applied. Marking this non-idempotent makes the orchestrator replay
// the previously-fetched stream from the journal instead of re-querying.
func (p *FitBitHeartRate) IsIdempotent() bool { return false }

func (p *FitBitHeartRate) Enrich(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, user *user.Record, inputs map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	return p.EnrichWithClient(ctx, logger, activity, user, inputs, nil, doNotRetry)
}

// EnrichWithClient allows HTTP client injection for testing
func (p *FitBitHeartRate) EnrichWithClient(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, user *user.Record, inputs map[string]string, httpClient *http.Client, doNotRetry bool) (*providers.EnrichmentResult, error) {
	// 0. Check force option - skip if activity already has heartrate data and force is not set
	forceOverwrite := inputs["force"] == "true"
	if !forceOverwrite && hasExistingHeartRateData(activity) {
		logger.Info("Skipping HR enrichment: activity already has heartrate data and force=false")
		return &providers.EnrichmentResult{
			Skipped:    true,
			SkipReason: "Activity already has heartrate data",
			Metadata: map[string]string{
				"hr_source":     "skipped",
				"status_detail": "Activity already has heartrate data",
				"force":         "false",
			},
		}, nil
	}

	// 1. Check Credentials
	if user.Integrations == nil || user.Integrations.Googlehealth == nil || !user.Integrations.Googlehealth.Enabled {
		return nil, fmt.Errorf("googlehealth integration not enabled (run `fitgluectl connect googlehealth`)")
	}

	// 2. Parse Activity Times.
	// Slice the HR query window off the FIRST SESSION's start time, not the top-level
	// activity.StartTime. The top-level field can be stale or shared across two same-day
	// activities from the same source (e.g. both left at the day's / first activity's start),
	// which would make both enrich against the identical window and inherit the SAME
	// heart-rate stream — the cross-activity duplication bug. The session start is
	// per-activity accurate and is what the rest of the pipeline (FIT generation at
	// file_generators/fit.go, stream alignment in the orchestrator) already treats as
	// canonical, so deriving the window from it keeps the enricher consistent and correct.
	durationSec := 3600 // Default
	var sessionStart time.Time
	if len(activity.Sessions) > 0 {
		durationSec = int(activity.Sessions[0].TotalElapsedTime)
		if ts := activity.Sessions[0].StartTime; ts != nil {
			sessionStart = ts.AsTime()
		}
	}

	startTime := sessionStart
	if startTime.IsZero() {
		// No usable per-session start — fall back to the top-level activity start.
		startTime = activity.StartTime.AsTime()
	}
	if startTime.IsZero() || startTime.Unix() <= 0 {
		return nil, fmt.Errorf("invalid start time: zero")
	}
	startTime = startTime.UTC()
	endTime := startTime.Add(time.Duration(durationSec) * time.Second)

	// Timezones: the Fitbit intraday API took wall-clock times in the Fitbit profile's
	// timezone, so the old enricher had to look the zone up (BST vs GMT) and split
	// queries that crossed midnight. Google Health filters on physical (UTC) time and
	// returns each sample's physical instant, so the activity's UTC start/end are used
	// as they are: no profile lookup, no local-time conversion, no midnight case.

	// 3. Initialize OAuth HTTP Client if not provided (for testing)
	if httpClient == nil {
		tokenSource := oauth.NewFirestoreTokenSource(p.Service, user.UserId, provider)
		httpClient = oauth.NewClientWithUsageTracking(tokenSource, p.Service, user.UserId, provider, infra.WrapSlogLogger(logger))
	}
	base := p.baseURL
	if base == "" {
		base = googlehealth.BaseURL
	}
	client := googlehealth.NewWithBaseURL(base, httpClient)

	// 4. Request the samples for [start, end] (end inclusive to the second).
	samples, err := client.HeartRate(ctx, startTime, endTime.Add(time.Second))
	if err != nil {
		var apiErr *googlehealth.APIError
		if errors.As(err, &apiErr) {
			// 429 (300 requests/min/user) and 5xx are transient. Client errors are
			// permanent: ACCOUNT_NOT_LINKED (the Google account is not signed in to the
			// Google Health app), MISSING_OAUTH_SCOPE (heart rate unticked at consent),
			// 401 after a refresh (grant revoked — reconnect).
			if apiErr.Transient() {
				if doNotRetry {
					logger.Warn("Google Health transient error and lag exhausted, skipping HR enrichment", "status", apiErr.StatusCode)
					return transientSkipResult(fmt.Sprintf("Google Health API returned status %d (lag exhausted)", apiErr.StatusCode)), nil
				}
				return nil, providers.NewRetryableError(err, apiErr.RetryAfter, fmt.Sprintf("google health api transient error %d", apiErr.StatusCode))
			}
			return nil, err
		}
		// Transport-level failures (DNS, TLS, OAuth refresh hiccups) are transient:
		// retry via the lag mechanism instead of failing the pipeline permanently.
		if doNotRetry {
			logger.Warn("Google Health request failed and lag exhausted, skipping HR enrichment", "error", err)
			return transientSkipResult(fmt.Sprintf("Google Health API request failed (lag exhausted): %v", err)), nil
		}
		return nil, providers.NewRetryableError(fmt.Errorf("google health api request failed: %w", err), 0, "google health api request failed (transport)")
	}

	hrSamples := make([]providers.TimedSample, 0, len(samples))
	for _, s := range samples {
		hrSamples = append(hrSamples, providers.TimedSample{Timestamp: s.Time, Value: s.BPM})
	}

	// 5. Build Stream - Check if GPS data exists for alignment
	var stream []int
	alignmentMetadata := make(map[string]string)

	if hasGPSData(activity) {
		// Align HR to GPS by absolute timestamp (see providers.AlignTimeSeries). Both
		// series are physical instants, so they share one real-time line.
		logger.Info("GPS data detected, applying absolute-timestamp HR alignment")
		gpsTimestamps := extractGPSTimestamps(activity)

		if len(gpsTimestamps) > 0 && len(hrSamples) > 0 {
			alignResult, err := providers.AlignTimeSeries(gpsTimestamps, hrSamples, providers.DefaultAlignmentConfig, logger)
			if err != nil {
				logger.Warn("HR alignment failed, falling back to index-based mapping", "error", err)
				stream = buildStreamIndexBased(hrSamples, startTime, durationSec)
			} else {
				stream = alignResult.AlignedHR
				for k, v := range alignResult.Metadata {
					alignmentMetadata[k] = v
				}
				if alignResult.WarningMessage != "" {
					alignmentMetadata["alignment_warning"] = alignResult.WarningMessage
				}
			}
		} else {
			// Fallback if no meaningful data
			stream = buildStreamIndexBased(hrSamples, startTime, durationSec)
		}
	} else {
		// No GPS data - index-based mapping (one slot per second from the start)
		stream = buildStreamIndexBased(hrSamples, startTime, durationSec)
		alignmentMetadata["alignment_status"] = "skipped_no_gps"
	}

	pointsFound := len(hrSamples)

	// Lag Detection (Start/End Coverage)
	hasStart := false
	hasEnd := false
	startThreshold := 120 // 2 minutes
	endThreshold := durationSec - 120

	if pointsFound > 0 {
		offset1 := int(hrSamples[0].Timestamp.Sub(startTime).Seconds())
		offset2 := int(hrSamples[pointsFound-1].Timestamp.Sub(startTime).Seconds())
		if offset1 <= startThreshold {
			hasStart = true
		}
		if offset2 >= endThreshold {
			hasEnd = true
		}
	}
	logger.Info(fmt.Sprintf("Retrieved Google Health HR points=%d duration=%d start=%s has_start=%v has_end=%v", pointsFound, durationSec, startTime.Format(time.RFC3339), hasStart, hasEnd))

	// Decision logic
	timeSinceEnd := time.Since(endTime)
	isRecent := timeSinceEnd < 1*time.Hour

	// On resume the activity is usually no longer "recent" (it sat paused as a pending input),
	// so the recency gate below would normally skip the lag retry and accept whatever partial
	// data has synced so far — which then gets aligned with gaps (no stretching) but still
	// misses HR the watch simply hadn't delivered yet. Re-arm the retry on resume so the lag
	// queue gives the watch time to finish syncing before we commit to partial coverage.
	isResume := inputs["is_resume"] == "true"
	requireCoverage := isRecent || isResume

	if (!hasStart || !hasEnd) && requireCoverage {
		reason := fmt.Sprintf("incomplete data (start:%v end:%v, recent:%v resume:%v) %v after end", hasStart, hasEnd, isRecent, isResume, timeSinceEnd.Round(time.Second))

		if doNotRetry {
			logger.Warn("Incomplete data detected but forced to continue: " + reason)
			// DO NOT return error, accept whatever data we have
		} else {
			logger.Warn("Incomplete data detected: " + reason)
			// RetryableError triggers the lag mechanism; this result is discarded.
			return nil, providers.NewRetryableError(fmt.Errorf("incomplete data"), 1*time.Minute, reason)
		}
	} else if pointsFound == 0 && !isRecent {
		// If old and empty, likely no data ever. Just return empty.
		logger.Warn(fmt.Sprintf("No heart rate data points found in Google Health start=%s end=%s", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339)))
	}

	utcOffset := ""
	if len(samples) > 0 {
		utcOffset = samples[0].UTCOffset.String()
	}

	return &providers.EnrichmentResult{
		Name:            "", // Don't wipe name
		HeartRateStream: stream,
		Metadata: mergeMetadata(map[string]string{
			"hr_source":     "googlehealth",
			"query_start":   startTime.Format(time.RFC3339),
			"query_end":     endTime.Format(time.RFC3339),
			"utc_offset":    utcOffset,
			"points_found":  fmt.Sprintf("%d", pointsFound),
			"status_detail": "Success",
			"do_not_retry":  fmt.Sprintf("%v", doNotRetry),
		}, alignmentMetadata),
	}, nil
}

// hasGPSData checks if any record in the activity has GPS coordinates
func hasGPSData(activity *pbactivity.StandardizedActivity) bool {
	for _, session := range activity.Sessions {
		for _, lap := range session.Laps {
			for _, record := range lap.Records {
				if record.PositionLat != 0 || record.PositionLong != 0 {
					return true
				}
			}
		}
	}
	return false
}

// extractGPSTimestamps extracts all record timestamps from the activity
func extractGPSTimestamps(activity *pbactivity.StandardizedActivity) []time.Time {
	var timestamps []time.Time
	for _, session := range activity.Sessions {
		for _, lap := range session.Laps {
			for _, record := range lap.Records {
				if record.Timestamp != nil {
					timestamps = append(timestamps, record.Timestamp.AsTime())
				}
			}
		}
	}
	return timestamps
}

// buildStreamIndexBased creates a per-second HR stream from the activity start: slot i is
// start+i seconds.
func buildStreamIndexBased(samples []providers.TimedSample, start time.Time, durationSec int) []int {
	if durationSec < 0 {
		durationSec = 0
	}
	stream := make([]int, durationSec)

	for _, s := range samples {
		offset := int(s.Timestamp.Sub(start).Seconds())
		if offset >= 0 && offset < durationSec {
			stream[offset] = s.Value
		}
	}

	// Forward-fill internal gaps only, up to the last point actually returned. The tail
	// after the last real sample (e.g. a partial pull that stopped early) is left as gaps (0)
	// rather than holding the last value across the rest of the session — that hold is the
	// non-GPS equivalent of stretching. Leading entries before the first sample also stay 0.
	lastDataIdx := -1
	for i := len(stream) - 1; i >= 0; i-- {
		if stream[i] != 0 {
			lastDataIdx = i
			break
		}
	}

	lastVal := 0
	for i := 0; i <= lastDataIdx; i++ {
		if stream[i] != 0 {
			lastVal = stream[i]
		} else {
			stream[i] = lastVal
		}
	}

	return stream
}

// transientSkipResult is the graceful-degradation result when a transient
// failure outlives the lag retry budget: complete the pipeline without HR data
// rather than failing it.
func transientSkipResult(detail string) *providers.EnrichmentResult {
	return &providers.EnrichmentResult{
		Metadata: map[string]string{
			"hr_source":     "skipped",
			"status_detail": detail,
		},
	}
}

// hasExistingHeartRateData checks if the activity already has heart rate data in its records
func hasExistingHeartRateData(activity *pbactivity.StandardizedActivity) bool {
	for _, session := range activity.Sessions {
		for _, lap := range session.Laps {
			for _, record := range lap.Records {
				if record.HeartRate > 0 {
					return true
				}
			}
		}
	}
	return false
}

// mergeMetadata combines two metadata maps, with second map taking precedence
func mergeMetadata(base, overlay map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		result[k] = v
	}
	return result
}
