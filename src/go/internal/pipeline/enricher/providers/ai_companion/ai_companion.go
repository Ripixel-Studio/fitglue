package ai_companion

import (
	"context"
	"errors"
	"fmt"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	"github.com/fitglue/server/src/go/pkg/claude"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	"github.com/fitglue/server/src/go/pkg/voice"
)

// AICompanionProvider generates the AI activity summary, told by a knowledgeable observer, using Claude Sonnet.
// The companion line sits alongside his own text; it never rewrites it.
type AICompanionProvider struct {
	Service *bootstrap.Service
	AI      claude.Completer // nil means claude.Default()
}

func init() {
	providers.Register(NewAICompanionProvider())
}

func NewAICompanionProvider() *AICompanionProvider {
	return &AICompanionProvider{}
}

func (p *AICompanionProvider) SetService(service *bootstrap.Service) {
	p.Service = service
}

func (p *AICompanionProvider) Name() string {
	return "ai-companion"
}

func (p *AICompanionProvider) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_AI_COMPANION
}

func (p *AICompanionProvider) IsIdempotent() bool { return false }

func (p *AICompanionProvider) ShouldDefer() bool {
	return true
}

func (p *AICompanionProvider) Enrich(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, user *user.Record, inputs map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	// Build context from activity
	activityContext := buildActivityContext(activity)

	// Include enriched description from other enrichers (injected by orchestrator Phase 2).
	// Scrubbed like the name; left out entirely if it touches sensitive vocabulary.
	if enrichedDesc := inputs["enriched_description"]; enrichedDesc != "" && !voice.Sensitive(enrichedDesc) {
		activityContext += "\n\nOther Enricher Descriptions:\n" + voice.Scrub(enrichedDesc)
	}

	// Repost cache: return stored result instead of calling Claude again.
	// The key is versioned so Gemini-era lines are never reused, and a cached
	// line must pass voice.Check again; a failure falls through to regenerate.
	externalId := inputs["external_id"]
	if inputs["is_repost"] == "true" && externalId != "" && p.Service != nil && p.Service.DB != nil {
		if data, err := p.Service.DB.GetBoosterData(ctx, user.UserId, cacheKey); err == nil && data != nil {
			cachedDesc, _ := data["cached_description"].(string)
			if cached, ok := data["cached_external_id"].(string); ok && cached == externalId && cachedDesc != "" &&
				voice.Check(strings.TrimPrefix(voice.StripEmoji(cachedDesc), aiLabel), checkContext(activityContext)) == nil {
				logger.Info("AI Companion: returning cached result for repost", "external_id", externalId)
				cachedDesc = voice.StripEmoji(cachedDesc)
				return &providers.EnrichmentResult{
					Description: cachedDesc,
					Metadata:    map[string]string{"status": "success", "cache": "hit"},
					Enrichments: &pbactivity.ActivityEnrichments{
						AiSummary: &pbactivity.AiSummary{Html: cachedDesc},
					},
				}, nil
			}
		}
	}

	// Generate the companion line. Every output passes voice.Check before use.
	line, err := p.generate(ctx, activityContext)
	if err != nil {
		reason := "generation_failed"
		if errors.Is(err, claude.ErrLimited) {
			reason = "usage_limited"
		}
		logger.Warn("Failed to generate AI companion content", "error", err)
		return &providers.EnrichmentResult{
			Metadata: map[string]string{
				"status":        "error",
				"reason":        reason,
				"status_detail": err.Error(),
			},
		}, nil // Don't return error to avoid pipeline failure
	}
	if line == "" {
		logger.Info("AI Companion line dropped by voice guard")
		return &providers.EnrichmentResult{
			Metadata: map[string]string{"status": "skipped", "reason": "voice_guard"},
		}, nil
	}

	result := &aiResult{Description: line}
	// The label is mandatory: it is what marks the line as AI, not his.
	result.Description = aiLabel + result.Description

	logger.Info("AI Companion content generated successfully")

	// Cache result so reposts don't re-call Claude.
	if externalId != "" && p.Service != nil && p.Service.DB != nil {
		_ = p.Service.DB.SetBoosterData(ctx, user.UserId, cacheKey, map[string]interface{}{
			"cached_external_id": externalId,
			"cached_description": result.Description,
		})
	}

	return &providers.EnrichmentResult{
		Description: result.Description,
		Metadata:    map[string]string{"status": "success"},
		Enrichments: &pbactivity.ActivityEnrichments{
			AiSummary: &pbactivity.AiSummary{
				Html: result.Description,
			},
		},
	}, nil
}

type aiResult struct {
	Description string
}

// sanitiseForPrompt truncates user-controlled strings to a safe length and
// strips obvious prompt-injection delimiters to prevent injected instructions
// from escaping the data section of the prompt.
func sanitiseForPrompt(s string) string {
	const maxLen = 500
	// Activity names/descriptions come from third-party providers and can carry
	// invalid UTF-8; strip it before it reaches the model.
	s = strings.ToValidUTF8(s, "")
	if len(s) > maxLen {
		s = s[:maxLen]
		// A byte-length cut can land mid-rune (emoji are 4 bytes), which would
		// re-introduce invalid UTF-8; back up to the last rune boundary.
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// cacheKey versions the repost cache by model/voice. Bump it when either
// changes; the pre-Haiku key "ai_companion" holds unguarded Gemini text and is
// never read.
const cacheKey = "ai_companion_sonnet_v5"

const aiLabel = "AI Summary:\n"

const taskBlock = `Write the summary for this session: 1–3 sentences, at most 280 characters, as the observer above. Give an insight the data supports (what the session says about his training, where the effort sat, how it compares with the rest of the data given); if there is none, a friendly plain summary. Use only facts in the data. Third person: he, never his name. Never I, me, my or we. No names. No 'you'. No emoji. Never mention alcohol, drink, drugs, pubs or recovery.`

const maxLineChars = 280

// summaryUserPrompt closes the data with the instruction, so the model writes the
// summary rather than describing its brief.
const summaryUserPrompt = "Write the summary of this session now. Reply with the summary text only."

// generate asks Sonnet for the activity summary (Haiku echoed its brief, 2026-11-11). It returns "" when the output
// fails voice.Line/voice.Check (never publish it); errors are Claude's
// (including claude.ErrLimited).
func (p *AICompanionProvider) generate(ctx context.Context, activityContext string) (string, error) {
	ai := p.AI
	if ai == nil {
		ai = claude.Default()
	}
	system, prompt := buildPrompt(activityContext)
	line, _, err := voice.Generate(func(extra string) (string, error) {
		sys := system
		if extra != "" {
			sys += "\n\n" + extra
		}
		return ai.Complete(ctx, claude.Request{
			Model:   claude.ModelSonnet,
			System:  sys,
			Prompt:  prompt,
			Timeout: 90 * time.Second,
		})
	}, maxLineChars, checkContext(activityContext))
	if err != nil {
		return "", err
	}
	return line, nil
}

// checkContext is the only context voice.Check sees: fitness vocabulary (type,
// metrics, exercise names from set data). Never his name, description or other
// enrichers' text, so a name he wrote cannot whitelist itself in the output.
func checkContext(activityContext string) string {
	if i := strings.Index(activityContext, "\n\nOther Enricher Descriptions:"); i >= 0 {
		activityContext = activityContext[:i]
	}
	var keep []string
	for _, l := range strings.Split(activityContext, "\n") {
		if strings.HasPrefix(l, "Original ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// buildPrompt returns the fixed system prompt (the observer guide plus the task)
// and the data-only user prompt, so instructions and activity data stay apart.
func buildPrompt(activityContext string) (system, prompt string) {
	system = voice.Observer() + "\n\n" + taskBlock
	prompt = "Activity Context:\n" + sanitiseForPrompt(activityContext) + "\n\n" + summaryUserPrompt
	return system, prompt
}

func buildActivityContext(activity *pbactivity.StandardizedActivity) string {
	var parts []string

	// Activity type
	if activity.Type != pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
		parts = append(parts, fmt.Sprintf("Type: %s", activity.Type.String()))
	}

	// Activity name (original)
	// Sensitive text never reaches a model: leave name and description out
	// together; otherwise scrub people out of both.
	if !voice.Sensitive(activity.Name + " " + activity.Description) {
		if n := voice.Scrub(activity.Name); n != "" {
			parts = append(parts, fmt.Sprintf("Original Name: %s", n))
		}
		if d := voice.Scrub(activity.Description); d != "" {
			parts = append(parts, fmt.Sprintf("Original Description: %s", d))
		}
	}

	// Duration and distance from sessions
	var totalDuration float64
	var totalDistance float64
	var strengthSets []*pbactivity.StrengthSet
	var heartRates []float32
	var cadences []uint32
	var powers []uint32

	for _, session := range activity.Sessions {
		totalDuration += session.TotalElapsedTime
		totalDistance += session.TotalDistance
		strengthSets = append(strengthSets, session.StrengthSets...)

		for _, lap := range session.Laps {
			for _, record := range lap.Records {
				if record.HeartRate > 0 {
					heartRates = append(heartRates, float32(record.HeartRate))
				}
				if record.Cadence > 0 {
					cadences = append(cadences, uint32(record.Cadence))
				}
				if record.Power > 0 {
					powers = append(powers, uint32(record.Power))
				}
			}
		}
	}

	if totalDuration > 0 {
		mins := totalDuration / 60 // seconds to minutes
		parts = append(parts, fmt.Sprintf("Duration: %.1f minutes", mins))
	}

	if totalDistance > 0 {
		km := totalDistance / 1000
		parts = append(parts, fmt.Sprintf("Distance: %.2f km", km))
	}

	// Heart Rate Summary
	if len(heartRates) > 0 {
		var sum float32
		var max float32
		min := heartRates[0]
		for _, hr := range heartRates {
			sum += hr
			if hr > max {
				max = hr
			}
			if hr < min {
				min = hr
			}
		}
		avg := sum / float32(len(heartRates))
		parts = append(parts, fmt.Sprintf("Heart Rate: Avg %.0f bpm, Max %.0f bpm, Min %.0f bpm", avg, max, min))
	}

	// Cadence Summary
	if len(cadences) > 0 {
		var sum uint32
		var max uint32
		for _, c := range cadences {
			sum += c
			if c > max {
				max = c
			}
		}
		avg := float64(sum) / float64(len(cadences))
		parts = append(parts, fmt.Sprintf("Cadence: Avg %.0f rpm, Max %d rpm", avg, max))
	}

	// Power Summary
	if len(powers) > 0 {
		var sum uint32
		var max uint32
		for _, p := range powers {
			sum += p
			if p > max {
				max = p
			}
		}
		avg := float64(sum) / float64(len(powers))
		parts = append(parts, fmt.Sprintf("Power: Avg %.0f W, Max %d W", avg, max))
	}

	// Strength Exercises Summary
	if len(strengthSets) > 0 {
		type exerciseStats struct {
			totalSets int
			reps      []int32
			weights   []float32
		}
		exercises := make(map[string]*exerciseStats)
		var exerciseOrder []string

		for _, set := range strengthSets {
			if set.ExerciseName == "" {
				continue
			}
			if _, ok := exercises[set.ExerciseName]; !ok {
				exercises[set.ExerciseName] = &exerciseStats{}
				exerciseOrder = append(exerciseOrder, set.ExerciseName)
			}
			stats := exercises[set.ExerciseName]
			stats.totalSets++
			stats.reps = append(stats.reps, set.Reps)
			stats.weights = append(stats.weights, float32(set.WeightKg))
		}

		if len(exerciseOrder) > 0 {
			parts = append(parts, "Strength Exercises:")
			for _, name := range exerciseOrder {
				stats := exercises[name]

				// Group by reps/weight to be concise
				type setGroup struct {
					reps   int32
					weight float32
					count  int
				}
				var groups []setGroup
				for i := 0; i < len(stats.reps); i++ {
					found := false
					for gIdx := range groups {
						if groups[gIdx].reps == stats.reps[i] && groups[gIdx].weight == stats.weights[i] {
							groups[gIdx].count++
							found = true
							break
						}
					}
					if !found {
						groups = append(groups, setGroup{reps: stats.reps[i], weight: stats.weights[i], count: 1})
					}
				}

				var groupParts []string
				for _, g := range groups {
					if g.weight > 0 {
						groupParts = append(groupParts, fmt.Sprintf("%d x %d @ %.1fkg", g.count, g.reps, g.weight))
					} else {
						groupParts = append(groupParts, fmt.Sprintf("%d x %d", g.count, g.reps))
					}
				}
				parts = append(parts, fmt.Sprintf("- %s: %s", name, strings.Join(groupParts, ", ")))
			}
		}
	}

	return strings.Join(parts, "\n")
}
