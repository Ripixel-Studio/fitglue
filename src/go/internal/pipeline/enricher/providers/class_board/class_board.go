// Package class_board holds a gym class until its whiteboard photo has been
// read into an exercise log (jarvis specs/class-upload-design.md).
//
// A class reaches FitGlue as a Hevy workout logged as one placeholder
// ("Bootcamp", 50 min) or nothing at all — James taps start/stop in Hevy and
// photographs the board. This enricher raises a blocking question for such a
// workout; Jarvis's class card (/class/<id>) reads the board, estimates what
// he did, and answers with workout_data: manual-workout-entry's JSON plus a
// per-set "estimated". The sets replace the placeholder; an empty answer ("I
// forgot", or Jarvis's 22:00 timeout) keeps the class as it was.
//
// Spin classes, rides and workouts with real sets never get the question.
package class_board

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/user_input"
	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	pendinginput "github.com/fitglue/server/src/go/pkg/pending_input"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// SectionHeader marks the description line, so a repost replaces it.
const SectionHeader = "📋 Class board:"

const estimatedLine = "Reps estimated from the class board."

var (
	spinTitle = regexp.MustCompile(`(?i)\bspin(ning)?\b`)
	// The gym types a class is filed under (the classifier keeps Hyrox and
	// hybrid classes as Crossfit or HIIT, gym classes as workout).
	classTypes = map[pbactivity.ActivityType]bool{
		pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED:                      true,
		pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT:                         true,
		pbactivity.ActivityType_ACTIVITY_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING: true,
		pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT:                          true,
		pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING:                  true,
	}
	london = func() *time.Location {
		if l, err := time.LoadLocation("Europe/London"); err == nil {
			return l
		}
		return time.UTC
	}()
)

// Provider is the class-board enricher.
type Provider struct {
	service *bootstrap.Service
}

func init() {
	providers.Register(&Provider{})
}

func (p *Provider) SetService(s *bootstrap.Service) { p.service = s }
func (p *Provider) Name() string                    { return "class-board" }

// AsksUser: a lag-forced partial run still asks (see providers.AsksUser).
func (p *Provider) AsksUser() bool { return true }

func (p *Provider) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_CLASS_BOARD
}

// IsClass: a Hevy gym workout logged with nothing but (at most) one
// placeholder exercise — every set shares one name and none has reps or a
// weight. Spin classes and other sport types are not classes here.
func IsClass(a *pbactivity.StandardizedActivity) bool {
	if a == nil || a.GetSource() != pbactivity.ActivitySource_SOURCE_HEVY || !classTypes[a.GetType()] {
		return false
	}
	if spinTitle.MatchString(a.GetName()) {
		return false
	}
	name := ""
	for _, s := range a.GetSessions() {
		for _, set := range s.GetStrengthSets() {
			if set.GetReps() > 0 || set.GetWeightKg() > 0 || set.GetDistanceMeters() > 0 {
				return false
			}
			if name == "" {
				name = set.GetExerciseName()
			} else if set.GetExerciseName() != name {
				return false
			}
		}
	}
	return true
}

func span(a *pbactivity.StandardizedActivity) (time.Time, time.Time, int) {
	start := a.GetStartTime().AsTime()
	if a.GetStartTime() == nil && len(a.GetSessions()) > 0 {
		start = a.GetSessions()[0].GetStartTime().AsTime()
	}
	var dur float64
	for _, s := range a.GetSessions() {
		dur += s.GetTotalElapsedTime()
	}
	return start.UTC(), start.UTC().Add(time.Duration(dur) * time.Second), int(dur)
}

// waitError is the class's question: what Jarvis's card shows and answers.
func waitError(stableID string, a *pbactivity.StandardizedActivity) *user_input.WaitForInputError {
	start, end, dur := span(a)
	title := a.GetName()
	if title == "" {
		title = "Class"
	}
	return &user_input.WaitForInputError{
		ActivityID:         stableID,
		RequiredFields:     []string{"workout_data"},
		EnricherProviderID: "class-board",
		Metadata: map[string]string{
			"display.title":       "Class board",
			"display.summary":     fmt.Sprintf("%s · %s–%s", title, start.In(london).Format("15:04"), end.In(london).Format("15:04")),
			"display.help":        "Add the class's whiteboard photo; Jarvis reads it and estimates what you did.",
			"display.field_types": `{"workout_data":"custom:class_board"}`,
			"class.title":         title,
			"class.start":         start.Format(time.RFC3339),
			"class.end":           end.Format(time.RFC3339),
			"class.duration_s":    strconv.Itoa(dur),
		},
	}
}

type inputExercise struct {
	Exercise string     `json:"exercise"`
	Notes    string     `json:"notes"`
	Sets     []inputSet `json:"sets"`
}

type inputSet struct {
	Reps            int32   `json:"reps"`
	WeightKg        float64 `json:"weight_kg"`
	DurationSeconds int32   `json:"duration_seconds"`
	DistanceMeters  float64 `json:"distance_meters"`
	Estimated       bool    `json:"estimated"`
}

// applyAnswer puts the class card's sets in place of the placeholder. An
// empty answer keeps the class as it was; anything that isn't the card's JSON
// is refused.
func applyAnswer(a *pbactivity.StandardizedActivity, raw string) (*providers.EnrichmentResult, error) {
	if raw == "" {
		return &providers.EnrichmentResult{Skipped: true, SkipReason: "no board: posted as logged",
			Metadata: map[string]string{"class_board_status": "no_board"}}, nil
	}
	var exercises []inputExercise
	if err := json.Unmarshal([]byte(raw), &exercises); err != nil {
		return nil, fmt.Errorf("class-board: workout_data is not the class card's JSON: %w", err)
	}
	var sets []*pbactivity.StrengthSet
	estimated := 0
	for _, ex := range exercises {
		for _, s := range ex.Sets {
			if s.Estimated {
				estimated++
			}
			sets = append(sets, &pbactivity.StrengthSet{
				ExerciseName: ex.Exercise, Reps: s.Reps, WeightKg: s.WeightKg,
				DurationSeconds: s.DurationSeconds, DistanceMeters: s.DistanceMeters,
				Notes: ex.Notes, SetType: "normal", Estimated: s.Estimated,
			})
		}
	}
	if len(sets) == 0 {
		return &providers.EnrichmentResult{Skipped: true, SkipReason: "class card sent no sets",
			Metadata: map[string]string{"class_board_status": "empty"}}, nil
	}
	if len(a.Sessions) == 0 {
		a.Sessions = []*pbactivity.Session{{}}
	}
	a.Sessions[0].StrengthSets = sets
	res := &providers.EnrichmentResult{Metadata: map[string]string{
		"class_board_status": "applied", "strength_set_count": strconv.Itoa(len(sets)),
		"estimated_set_count": strconv.Itoa(estimated)}}
	if estimated > 0 {
		res.SectionHeader = SectionHeader
		res.Description = SectionHeader + " " + estimatedLine
	}
	return res, nil
}

// Enrich asks for the class's board, or applies the answer once it's in.
func (p *Provider) Enrich(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, userRec *user.Record, inputs map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	if !IsClass(activity) {
		return &providers.EnrichmentResult{Skipped: true, SkipReason: "not a class logged as a placeholder"}, nil
	}
	if doNotRetry {
		return &providers.EnrichmentResult{Skipped: true, SkipReason: "out-of-band run"}, nil
	}
	if p.service == nil {
		return nil, fmt.Errorf("class-board: service not initialized")
	}
	stableID := pendinginput.GenerateID(activity.Source.String(), activity.ExternalId, p.Name())
	pending, err := p.service.DB.GetPendingInput(ctx, userRec.UserId, stableID)
	if err == nil && pending != nil && pending.Status == pbpipeline.PendingInput_STATUS_COMPLETED {
		logger.Debug("class-board: answered — applying")
		return applyAnswer(activity, pending.InputData["workout_data"])
	}
	return nil, waitError(stableID, activity)
}

// EnrichResume applies the class card's answer when the run resumes.
func (p *Provider) EnrichResume(_ context.Context, activity *pbactivity.StandardizedActivity, _ *user.Record, pendingInput *pbpipeline.PendingInput) (*providers.EnrichmentResult, error) {
	return applyAnswer(activity, pendingInput.GetInputData()["workout_data"])
}
