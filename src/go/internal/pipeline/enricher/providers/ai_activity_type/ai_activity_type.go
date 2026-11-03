// nolint:proto-json
package ai_activity_type

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/claude"
	domainactivity "github.com/fitglue/server/src/go/pkg/domain/activity"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"github.com/fitglue/server/src/go/pkg/voice"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// ambiguousTypes are activity types that are too generic to be meaningful.
// When an activity has one of these types, we ask Haiku to infer a better one.
var ambiguousTypes = map[pbactivity.ActivityType]bool{
	pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT:     true,
	pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED: true,
}

// AIActivityTypeProvider infers the correct ActivityType from activity context using Claude Haiku.
// It runs in Phase 2 (deferred) so it has access to the full enriched description.
type AIActivityTypeProvider struct {
	Service *bootstrap.Service
	AI      claude.Completer // nil means claude.Default()
}

func init() {
	providers.Register(NewAIActivityTypeProvider())
}

func NewAIActivityTypeProvider() *AIActivityTypeProvider {
	return &AIActivityTypeProvider{}
}

func (p *AIActivityTypeProvider) SetService(service *bootstrap.Service) {
	p.Service = service
}

func (p *AIActivityTypeProvider) Name() string {
	return "ai-activity-type"
}

func (p *AIActivityTypeProvider) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_AI_ACTIVITY_TYPE
}

// ShouldDefer returns true so this provider runs in Phase 2 after all other enrichers,
// giving it access to the accumulated enriched_description in inputConfig.
func (p *AIActivityTypeProvider) ShouldDefer() bool {
	return true
}

func (p *AIActivityTypeProvider) Enrich(
	ctx context.Context,
	logger *slog.Logger,
	activity *pbactivity.StandardizedActivity,
	user *user.Record,
	inputConfig map[string]string,
	doNotRetry bool,
) (*providers.EnrichmentResult, error) {
	// Only run when the activity type is ambiguous/generic.
	if !ambiguousTypes[activity.Type] {
		logger.Debug("ai-activity-type: skipping — activity already has a specific type",
			"type", activity.Type.String(),
		)
		return &providers.EnrichmentResult{
			Skipped:    true,
			SkipReason: fmt.Sprintf("activity type %s is already specific", activity.Type.String()),
			Metadata: map[string]string{
				"status":       "skipped",
				"reason":       "type_already_specific",
				"current_type": activity.Type.String(),
			},
		}, nil
	}

	// Build prompt (system instruction + sanitised user data kept separate).
	systemInstruction, userData := buildPrompt(activity, inputConfig)

	ai := p.AI
	if ai == nil {
		ai = claude.Default()
	}
	rawResponse, err := classify(ctx, ai, systemInstruction, userData)
	if err != nil {
		logger.Warn("ai-activity-type: Claude call failed", "error", err)
		return &providers.EnrichmentResult{
			Metadata: map[string]string{
				"status": "error",
				"reason": err.Error(),
			},
		}, nil // Don't fail the pipeline
	}

	// Parse the response — expect a single proto enum string like "ACTIVITY_TYPE_RUN".
	candidate := strings.TrimSpace(rawResponse)

	newType := domainactivity.ParseActivityTypeFromString(candidate)
	if newType == pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
		logger.Warn("ai-activity-type: Claude returned unrecognised activity type",
			"raw_response", rawResponse,
		)
		return &providers.EnrichmentResult{
			Skipped:    true,
			SkipReason: fmt.Sprintf("Claude returned unrecognised value: %q", candidate),
			Metadata: map[string]string{
				"status":       "skipped",
				"reason":       "unrecognised_response",
				"raw_response": rawResponse,
			},
		}, nil
	}

	originalType := activity.Type.String()

	// Apply the type override directly (same pattern as type_mapper).
	activity.Type = newType

	logger.Info("ai-activity-type: activity type updated",
		"original_type", originalType,
		"new_type", newType.String(),
	)

	return &providers.EnrichmentResult{
		Metadata: map[string]string{
			"status":        "success",
			"original_type": originalType,
			"new_type":      newType.String(),
			"raw_response":  rawResponse,
		},
	}, nil
}

// sanitiseForPrompt truncates user-controlled strings to a safe length and
// strips obvious prompt-injection delimiters to prevent injected instructions
// from escaping the data section of the prompt.
func sanitiseForPrompt(s string) string {
	const maxLen = 500
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

// buildPrompt returns the fixed system instruction and the sanitised user-data
// content as separate strings, keeping instructions and data structurally
// separated at the API level.
func buildPrompt(activity *pbactivity.StandardizedActivity, inputConfig map[string]string) (systemInstruction, userData string) {
	// Collect activity signals.
	var durationMins float64
	var distanceKm float64
	hasHeartRate := false
	hasGPS := false
	hasStrengthSets := false

	for _, session := range activity.Sessions {
		durationMins += session.TotalElapsedTime / 60.0
		distanceKm += session.TotalDistance / 1000.0

		// Heart rate and GPS presence are inferred from lap records
		for _, lap := range session.Laps {
			for _, rec := range lap.Records {
				if rec.HeartRate > 0 {
					hasHeartRate = true
				}
				if rec.PositionLat != 0 || rec.PositionLong != 0 {
					hasGPS = true
				}
			}
		}
		if len(session.StrengthSets) > 0 {
			hasStrengthSets = true
		}
	}

	// Sensitive text never reaches a model: leave the name and the enriched
	// description out when either touches recovery vocabulary; scrub people
	// out of whatever is sent.
	name := voice.Scrub(activity.Name)
	enriched := voice.Scrub(inputConfig["enriched_description"])
	if voice.Sensitive(activity.Name + " " + inputConfig["enriched_description"]) {
		name, enriched = "", ""
	}

	// Build valid type list (exclude UNSPECIFIED).
	var validTypes []string
	for name, val := range pbactivity.ActivityType_value {
		if pbactivity.ActivityType(val) == pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
			continue
		}
		validTypes = append(validTypes, name)
	}

	systemInstruction = fmt.Sprintf(`You are a fitness data classifier. Given the activity details provided by the user, respond with ONLY the single most appropriate ActivityType enum value from the list below. Do not add any explanation, punctuation, or extra text — just the enum string.

Valid ActivityType values:
%s

Respond with exactly one value from the list above.`,
		strings.Join(validTypes, "\n"),
	)

	// User-controlled fields are sanitised before embedding.
	userData = fmt.Sprintf(`Activity details:
- Name: %s
- Source: %s
- Duration: %.1f minutes
- Distance: %.2f km
- Has heart rate data: %v
- Has GPS data: %v
- Has strength/weight sets: %v
- Enriched description context: %s`,
		sanitiseForPrompt(name),
		activity.Source.String(),
		durationMins,
		distanceKm,
		hasHeartRate,
		hasGPS,
		hasStrengthSets,
		sanitiseForPrompt(enriched),
	)

	return systemInstruction, userData
}

// classify sends the prompt to Haiku and returns the raw text response.
// system holds the fixed classifier directives; user holds only sanitised
// activity data, keeping instructions and data separate.
func classify(ctx context.Context, ai claude.Completer, system, user string) (string, error) {
	return ai.Complete(ctx, claude.Request{
		Model:   claude.ModelHaiku,
		System:  system,
		Prompt:  user,
		Timeout: 30 * time.Second,
	})
}
