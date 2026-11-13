package ai_activity_type

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/claude"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

func aiLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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

func hyroxClass() *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{
		Name:       "Hyrox Class",
		Type:       pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		Source:     pbactivity.ActivitySource_SOURCE_HEVY,
		ExternalId: "hevy-1",
		Sessions: []*pbactivity.Session{{
			TotalElapsedTime: 3600,
			StrengthSets: []*pbactivity.StrengthSet{
				{ExerciseName: "Sled Push", DistanceMeters: 50},
				{ExerciseName: "Wall Balls", Reps: 20},
			},
		}},
	}
}

func TestMetadata(t *testing.T) {
	p := NewAIActivityTypeProvider()
	if p.Name() != "ai-activity-type" || p.ProviderType() != pbplugin.EnricherProviderType_ENRICHER_PROVIDER_AI_ACTIVITY_TYPE || !p.ShouldDefer() {
		t.Errorf("metadata %s %v %v", p.Name(), p.ProviderType(), p.ShouldDefer())
	}
	p.SetService(nil)
}

func TestParseVerdict(t *testing.T) {
	good := `{"tag":"hybrid","isRace":false,"raceWhy":"Hyrox sim, training","stravaType":"ACTIVITY_TYPE_CROSSFIT"}`
	c, err := ParseVerdict("```json\n" + good + "\n```")
	if err != nil || c.Tag != "hybrid" || c.IsRace || c.StravaType != pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT || c.Model != Model {
		t.Fatalf("good verdict: %v %v", c, err)
	}
	if c, err := ParseVerdict(`{"tag":"Run","isRace":true,"raceWhy":"x","stravaType":"RUN"}`); err != nil || c.Tag != "run" || c.StravaType != pbactivity.ActivityType_ACTIVITY_TYPE_RUN {
		t.Errorf("case and short type: %v %v", c, err)
	}
	for name, raw := range map[string]string{
		"not json":       "it's a run",
		"unknown tag":    `{"tag":"race","isRace":true,"raceWhy":"x","stravaType":"ACTIVITY_TYPE_RUN"}`,
		"missing isRace": `{"tag":"run","raceWhy":"x","stravaType":"ACTIVITY_TYPE_RUN"}`,
		"bad type":       `{"tag":"run","isRace":false,"raceWhy":"x","stravaType":"ACTIVITY_TYPE_PADEL"}`,
		"unspecified":    `{"tag":"run","isRace":false,"raceWhy":"x","stravaType":"ACTIVITY_TYPE_UNSPECIFIED"}`,
	} {
		if _, err := ParseVerdict(raw); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestEnrichAppliesVerdict(t *testing.T) {
	f := &fakeAI{out: `{"tag":"hybrid","isRace":false,"raceWhy":"class","stravaType":"ACTIVITY_TYPE_CROSSFIT"}`}
	p := NewAIActivityTypeProvider()
	p.AI = f
	act := hyroxClass()
	res, err := p.Enrich(context.Background(), aiLogger(), act, nil, map[string]string{"his_words": "Felt strong"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.req.Model != claude.ModelHaiku {
		t.Errorf("model %q", f.req.Model)
	}
	if act.Type != pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT || res.ActivityType != pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT {
		t.Errorf("strava type not applied: %v / %v", act.Type, res.ActivityType)
	}
	c := res.Enrichments.GetClassification()
	if c.GetTag() != "hybrid" || c.GetIsRace() || c.GetModel() != Model || c.GetInputsHash() == "" {
		t.Errorf("classification %v", c)
	}
	if act.Name != "Hyrox Class" || res.Name != "" || res.Description != "" {
		t.Error("the classifier must not touch the title or description")
	}
	if !strings.Contains(f.req.Prompt, "Sled Push x1") || !strings.Contains(f.req.Prompt, "Felt strong") {
		t.Errorf("prompt misses the sets or his words:\n%s", f.req.Prompt)
	}
}

func TestEnrichFallsBackToRule(t *testing.T) {
	for name, f := range map[string]*fakeAI{
		"error":       {err: errors.New("boom")},
		"limited":     {err: claude.ErrLimited},
		"invalid tag": {out: `{"tag":"crossfit","isRace":false,"raceWhy":"","stravaType":"ACTIVITY_TYPE_CROSSFIT"}`},
	} {
		t.Run(name, func(t *testing.T) {
			p := NewAIActivityTypeProvider()
			p.AI = f
			act := hyroxClass()
			res, err := p.Enrich(context.Background(), aiLogger(), act, nil, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			c := res.Enrichments.GetClassification()
			if c.GetTag() != "hybrid" || c.GetModel() != "rule" || res.Metadata["status"] != "fallback" {
				t.Errorf("fallback = %v meta %v", c, res.Metadata)
			}
			if act.Type != pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING {
				t.Errorf("type changed on fallback: %v", act.Type)
			}
		})
	}
}

func TestClassifyPassesLimitThrough(t *testing.T) {
	c, err := Classify(context.Background(), &fakeAI{err: claude.ErrLimited}, Inputs{Title: "Bootcamp", Activity: &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT}})
	if !errors.Is(err, claude.ErrLimited) || c.GetTag() != "class" {
		t.Errorf("Classify = %v, %v", c, err)
	}
}

// A repost with the same inputs reuses the stored verdict; changed inputs ask again.
func TestEnrichReusesCachedVerdict(t *testing.T) {
	store := map[string]map[string]interface{}{}
	db := &mocks.MockDatabase{
		GetBoosterDataFunc: func(_ context.Context, _, id string) (map[string]interface{}, error) { return store[id], nil },
		SetBoosterDataFunc: func(_ context.Context, _, id string, data map[string]interface{}) error {
			store[id] = data
			return nil
		},
	}
	f := &fakeAI{out: `{"tag":"hybrid","isRace":false,"raceWhy":"class","stravaType":"ACTIVITY_TYPE_CROSSFIT"}`}
	p := NewAIActivityTypeProvider()
	p.AI = f
	p.SetService(&bootstrap.Service{DB: db})
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	u.UserId = "u1"

	if _, err := p.Enrich(context.Background(), aiLogger(), hyroxClass(), u, nil, false); err != nil {
		t.Fatal(err)
	}
	res, _ := p.Enrich(context.Background(), aiLogger(), hyroxClass(), u, map[string]string{"is_repost": "true"}, false)
	if f.calls != 1 || res.Metadata["status"] != "cache" || res.Enrichments.GetClassification().GetTag() != "hybrid" {
		t.Errorf("repost asked again (%d calls) or lost the verdict: %v", f.calls, res.Metadata)
	}
	renamed := hyroxClass()
	renamed.Name = "Hyrox Sim"
	if _, err := p.Enrich(context.Background(), aiLogger(), renamed, u, nil, false); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 {
		t.Errorf("changed inputs must ask again, calls = %d", f.calls)
	}
}

func TestPromptScrubsPeopleAndSensitiveText(t *testing.T) {
	_, prompt := BuildPrompt(Inputs{
		Title:       "Tempo w/ Alex Example",
		Description: "Ran it with Sam Placeholder",
		Activity:    &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN},
	})
	if strings.Contains(prompt, "Alex") || strings.Contains(prompt, "Sam Placeholder") {
		t.Errorf("names not scrubbed:\n%s", prompt)
	}
	_, prompt = BuildPrompt(Inputs{
		Title:    "walk to NA meeting",
		Activity: &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_WALK},
	})
	if strings.Contains(prompt, "NA meeting") {
		t.Errorf("sensitive title sent:\n%s", prompt)
	}
}

func TestPromptParkrunContext(t *testing.T) {
	act := &pbactivity.StandardizedActivity{
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN, Name: "Riverside parkrun", Tags: []string{"Parkrun"},
		Sessions: []*pbactivity.Session{{TotalElapsedTime: 1500, TotalDistance: 5000}},
	}
	system, prompt := BuildPrompt(Inputs{Title: act.Name, Activity: act, UsualParkrunSeconds: []int{1400, 1420, 1380}})
	if !strings.Contains(prompt, "This parkrun's time: 25:00") || !strings.Contains(prompt, "median 23:20") {
		t.Errorf("parkrun context missing:\n%s", prompt)
	}
	if !strings.Contains(system, "ACTIVITY_TYPE_CROSSFIT") || strings.Contains(system, "ACTIVITY_TYPE_UNSPECIFIED") {
		t.Error("system prompt must list the types without UNSPECIFIED")
	}
	for _, tag := range []string{"run", "ride", "spin", "hybrid", "class", "strength", "pilates", "yoga", "walk", "other"} {
		if !strings.Contains(system, "- "+tag+":") {
			t.Errorf("system prompt misses tag %s", tag)
		}
	}
}

func TestInputsHashIgnoresFitGlueSections(t *testing.T) {
	a := Inputs{Title: "Bootcamp", Context: "Streak: 4 days", Activity: hyroxClass()}
	b := a
	b.Context = "Streak: 5 days"
	if InputsHash(a) != InputsHash(b) {
		t.Error("a streak counter must not change the hash")
	}
	b.Description = "Raced it"
	if InputsHash(a) == InputsHash(b) {
		t.Error("his words must change the hash")
	}
}

func TestClassifyHyroxTitleIsAlwaysHyrox(t *testing.T) {
	f := &fakeAI{out: `{"tag":"strength","isRace":false,"raceWhy":"PT","stravaType":"ACTIVITY_TYPE_WEIGHT_TRAINING"}`}
	c, err := Classify(context.Background(), f, Inputs{Title: "Push Focused Hyrox PT", Activity: hyroxClass()})
	if err != nil || c.Tag != "hybrid" || c.StravaType != pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING {
		t.Errorf("Classify = %v, %v", c, err)
	}
	c, _ = Classify(context.Background(), f, Inputs{Title: "Upper Body PT", Activity: hyroxClass()})
	if c.Tag != "strength" {
		t.Errorf("non-Hyrox title changed: %v", c.Tag)
	}
}
