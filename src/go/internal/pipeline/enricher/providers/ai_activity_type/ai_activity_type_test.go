package ai_activity_type

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

func aiLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestAIActivityType_Metadata(t *testing.T) {
	p := NewAIActivityTypeProvider()
	if p.Name() != "ai-activity-type" {
		t.Errorf("name %q", p.Name())
	}
	if p.ProviderType() != pbplugin.EnricherProviderType_ENRICHER_PROVIDER_AI_ACTIVITY_TYPE {
		t.Errorf("type %v", p.ProviderType())
	}
	if !p.ShouldDefer() {
		t.Error("ShouldDefer should be true")
	}
	p.SetService(nil)
}

func TestAIActivityType_SkipsSpecificType(t *testing.T) {
	act := &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
	res, err := NewAIActivityTypeProvider().Enrich(context.Background(), aiLogger(), act, nil, nil, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.Skipped || res.Metadata["reason"] != "type_already_specific" {
		t.Errorf("expected skip for specific type, got %+v", res)
	}
}

type fakeAI struct {
	req   claude.Request
	calls int
	out   string
	err   error
}

func (f *fakeAI) Complete(_ context.Context, r claude.Request) (string, error) {
	f.req = r
	f.calls++
	return f.out, f.err
}

func TestActivityTypeHaiku(t *testing.T) {
	f := &fakeAI{out: "ACTIVITY_TYPE_RUN"}
	p := NewAIActivityTypeProvider()
	p.AI = f
	act := &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT, Name: "Lunch"}
	res, err := p.Enrich(context.Background(), aiLogger(), act, nil, nil, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.req.Model != "haiku" {
		t.Errorf("model %q", f.req.Model)
	}
	if act.Type != pbactivity.ActivityType_ACTIVITY_TYPE_RUN || res.Metadata["status"] != "success" {
		t.Errorf("type %v meta %v", act.Type, res.Metadata)
	}
}

func TestActivityTypeClaudeErrorDoesNotFail(t *testing.T) {
	p := NewAIActivityTypeProvider()
	p.AI = &fakeAI{err: claude.ErrLimited}
	act := &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT}
	res, err := p.Enrich(context.Background(), aiLogger(), act, nil, nil, false)
	if err != nil || res.Metadata["status"] != "error" {
		t.Errorf("err %v meta %v", err, res.Metadata)
	}
	if act.Type != pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT {
		t.Error("type changed on error")
	}
}

func TestActivityTypeScrubbedAndSensitiveOmitted(t *testing.T) {
	f := &fakeAI{out: "ACTIVITY_TYPE_RUN"}
	p := NewAIActivityTypeProvider()
	p.AI = f
	act := &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT, Name: "Tempo w/ Tom"}
	_, _ = p.Enrich(context.Background(), aiLogger(), act, nil, nil, false)
	if strings.Contains(f.req.Prompt, "Tom") {
		t.Errorf("name not scrubbed: %s", f.req.Prompt)
	}
	act = &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT, Name: "walk to NA meeting"}
	_, _ = p.Enrich(context.Background(), aiLogger(), act, nil, nil, false)
	if strings.Contains(f.req.Prompt, "NA meeting") {
		t.Errorf("sensitive name sent: %s", f.req.Prompt)
	}
}

func TestSanitiseForPrompt(t *testing.T) {
	if got := sanitiseForPrompt("short"); got != "short" {
		t.Errorf("short string altered: %q", got)
	}
	long := strings.Repeat("x", 600)
	if got := sanitiseForPrompt(long); len(got) != 500 {
		t.Errorf("expected truncation to 500, got %d", len(got))
	}
}

func TestBuildPrompt(t *testing.T) {
	act := &pbactivity.StandardizedActivity{
		Name: "Mystery Session",
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT,
		Sessions: []*pbactivity.Session{
			{
				TotalElapsedTime: 1800,
				TotalDistance:    5000,
				Laps: []*pbactivity.Lap{{Records: []*pbactivity.Record{
					{HeartRate: 150, PositionLat: 51.5},
				}}},
				StrengthSets: []*pbactivity.StrengthSet{{ExerciseName: "Squat"}},
			},
		},
	}
	sys, userData := buildPrompt(act, map[string]string{"enriched_description": "felt great"})
	if !strings.Contains(sys, "ActivityType") {
		t.Error("system instruction should mention ActivityType")
	}
	if !strings.Contains(userData, "Mystery Session") {
		t.Error("user data should contain activity name")
	}
	if !strings.Contains(userData, "Has heart rate data: true") {
		t.Errorf("expected heart rate true in: %s", userData)
	}
	if !strings.Contains(userData, "Has GPS data: true") {
		t.Error("expected GPS true")
	}
	if !strings.Contains(userData, "Has strength/weight sets: true") {
		t.Error("expected strength true")
	}
	if !strings.Contains(userData, "felt great") {
		t.Error("expected enriched description in user data")
	}
}
