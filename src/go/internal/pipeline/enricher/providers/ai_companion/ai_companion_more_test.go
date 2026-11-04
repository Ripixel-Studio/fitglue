package ai_companion

import (
	"context"
	"errors"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAICompanion_FlagMethods(t *testing.T) {
	p := NewAICompanionProvider()
	if p.IsIdempotent() {
		t.Error("ai_companion should not be idempotent")
	}
	if !p.ShouldDefer() {
		t.Error("ai_companion should defer")
	}
}

func TestSanitiseForPrompt(t *testing.T) {
	if got := sanitiseForPrompt("short"); got != "short" {
		t.Errorf("short string should be unchanged, got %q", got)
	}
	long := strings.Repeat("a", 700)
	if got := sanitiseForPrompt(long); len(got) != 500 {
		t.Errorf("expected truncation to 500, got %d", len(got))
	}
}

// SERVER-1: a model client rejected prompts with invalid UTF-8. Two ways that happened: invalid bytes arriving from a
// provider, and the 500-byte truncation slicing a multi-byte rune in half.
func TestSanitiseForPrompt_AlwaysValidUTF8(t *testing.T) {
	// Truncation boundary lands inside a 4-byte emoji.
	emoji := strings.Repeat("\U0001F3C3", 200) // 800 bytes
	got := sanitiseForPrompt(emoji)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8: %q", got[len(got)-4:])
	}
	if len(got) > 500 || len(got) < 497 {
		t.Errorf("expected ~500 bytes on a rune boundary, got %d", len(got))
	}

	// Invalid bytes are dropped, valid text is kept.
	in := "Morning run \xff\xfe with friends \xc3"
	got = sanitiseForPrompt(in)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid bytes survived: %q", got)
	}
	if got != "Morning run  with friends " {
		t.Errorf("unexpected sanitised output %q", got)
	}
}

func TestBuildActivityContext_Rich(t *testing.T) {
	activity := &pbactivity.StandardizedActivity{
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		Name: "Tempo Run",
		Sessions: []*pbactivity.Session{
			{
				TotalElapsedTime: 1800,
				TotalDistance:    5000,
				Laps: []*pbactivity.Lap{
					{Records: []*pbactivity.Record{
						{HeartRate: 150, Cadence: 170, Power: 200},
						{HeartRate: 160, Cadence: 180, Power: 250},
					}},
				},
			},
		},
	}
	ctx := buildActivityContext(activity)
	for _, want := range []string{"Tempo Run", "Duration", "Distance", "Heart Rate", "Cadence", "Power"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context missing %q:\n%s", want, ctx)
		}
	}
}

func TestBuildActivityContext_Strength(t *testing.T) {
	activity := &pbactivity.StandardizedActivity{
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		Sessions: []*pbactivity.Session{
			{StrengthSets: []*pbactivity.StrengthSet{
				{ExerciseName: "Bench Press", Reps: 8, WeightKg: 80},
				{ExerciseName: "Bench Press", Reps: 8, WeightKg: 80},
				{ExerciseName: "Pull Up", Reps: 10, WeightKg: 0},
			}},
		},
	}
	ctx := buildActivityContext(activity)
	if !strings.Contains(ctx, "Strength Exercises:") {
		t.Errorf("expected strength section:\n%s", ctx)
	}
	if !strings.Contains(ctx, "Bench Press") || !strings.Contains(ctx, "@ 80.0kg") {
		t.Errorf("expected grouped weighted set:\n%s", ctx)
	}
	if !strings.Contains(ctx, "Pull Up") {
		t.Errorf("expected bodyweight exercise:\n%s", ctx)
	}
}

func TestAICompanion_Enrich_RepostCacheHit(t *testing.T) {
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId, boosterId string) (map[string]interface{}, error) {
			if boosterId != cacheKey {
				return nil, nil
			}
			return map[string]interface{}{
				"cached_external_id": "ext-7",
				"cached_description": "AI Summary:\nA great session.",
				"cached_name":        "Sweat Fest",
			}, nil
		},
	}
	p := NewAICompanionProvider()
	f := &fakeAI{err: errors.New("must not be called")}
	p.AI = f
	p.SetService(&bootstrap.Service{DB: mockDB})

	activity := &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	inputs := map[string]string{"is_repost": "true", "external_id": "ext-7"}

	res, err := p.Enrich(context.Background(), discardLogger(), activity, u, inputs, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	if res.Metadata["status"] != "success" || res.Metadata["cache"] != "hit" {
		t.Fatalf("expected cache hit, got %v", res.Metadata)
	}
	if res.Description != "AI Summary:\nA great session." || res.Name != "" {
		t.Errorf("unexpected cached content: name=%q desc=%q", res.Name, res.Description)
	}
	if res.Enrichments == nil || res.Enrichments.AiSummary == nil {
		t.Error("expected AiSummary enrichment")
	}
	if f.calls != 0 {
		t.Errorf("cache hit must not call Claude, got %d calls", f.calls)
	}
}

func repostCacheTest(t *testing.T, key, desc string, ai *fakeAI) (*providers.EnrichmentResult, []string) {
	var keys []string
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId, boosterId string) (map[string]interface{}, error) {
			keys = append(keys, boosterId)
			if boosterId != key {
				return nil, nil
			}
			return map[string]interface{}{"cached_external_id": "ext-7", "cached_description": desc}, nil
		},
	}
	p := NewAICompanionProvider()
	p.AI = ai
	p.SetService(&bootstrap.Service{DB: mockDB})
	activity := &pbactivity.StandardizedActivity{Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	res, err := p.Enrich(context.Background(), discardLogger(), activity, u, map[string]string{"is_repost": "true", "external_id": "ext-7"}, false)
	if err != nil {
		t.Fatal(err)
	}
	return res, keys
}

func TestAICompanion_RepostIgnoresOldGeminiKey(t *testing.T) {
	f := &fakeAI{err: errors.New("generation attempted")}
	res, keys := repostCacheTest(t, "ai_companion", "AI Summary:\nOld gemini line.", f)
	if res.Metadata["cache"] == "hit" || res.Description != "" {
		t.Fatalf("old-key cache must not be reused: %v", res.Metadata)
	}
	if f.calls == 0 {
		t.Error("expected regeneration")
	}
	for _, k := range keys {
		if k == "ai_companion" {
			t.Error("old key must never be read")
		}
	}
}

func TestAICompanion_RepostCachedLineIsChecked(t *testing.T) {
	f := &fakeAI{err: errors.New("generation attempted")}
	res, _ := repostCacheTest(t, cacheKey, "AI Summary:\nBeer legs today.", f)
	if res.Metadata["cache"] == "hit" || res.Description != "" {
		t.Fatalf("failing cached line must not be served: %v", res.Metadata)
	}
	if f.calls == 0 {
		t.Error("expected regeneration after failed cache check")
	}
}
