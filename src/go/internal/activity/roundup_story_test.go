package activity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

func TestRoundupStoryIsCurrent(t *testing.T) {
	r := syntheticRoundup()
	r.AiSummary = "October saw James commit to a relentless schedule."
	if RoundupStoryIsCurrent(r) {
		t.Error("an unstamped (Gemini-era) summary is not current")
	}
	r.AiSummaryModel = RoundupStoryModel
	if !RoundupStoryIsCurrent(r) {
		t.Error("a stamped Claude summary is current")
	}
	r.AiSummary = ""
	if RoundupStoryIsCurrent(r) {
		t.Error("an empty summary is never current")
	}
	r.AiSummary, r.AiSummaryModel = "x", "gemini"
	if RoundupStoryIsCurrent(r) {
		t.Error("another model's summary is not current")
	}
}

func TestWriteRoundupStoryErrorsSurface(t *testing.T) {
	svc := newTestSvc(&MockActivityStore{}, &MockBlobStore{})
	svc.AI = &fakeCompleter{err: claude.ErrLimited}
	if _, err := svc.WriteRoundupStory(context.Background(), syntheticRoundup()); !errors.Is(err, claude.ErrLimited) {
		t.Errorf("limited must surface so a batch can stop: %v", err)
	}
	svc.AI = &fakeCompleter{out: "Four runs and legs shaking. Back tomorrow."}
	got, err := svc.WriteRoundupStory(context.Background(), syntheticRoundup())
	if err != nil || got == "" {
		t.Errorf("clean output: %q %v", got, err)
	}
	svc.AI = &fakeCompleter{out: "Four runs and Sam kept up."}
	got, err = svc.WriteRoundupStory(context.Background(), syntheticRoundup())
	if err != nil || got != "" {
		t.Errorf("voice-guard drop is empty text, no error: %q %v", got, err)
	}
}

func TestSaveRoundupStoryStampsAndSignals(t *testing.T) {
	store := &MockActivityStore{}
	var saved *pbactivity.ShowcaseRoundup
	store.SetRoundupFunc = func(_ context.Context, r *pbactivity.ShowcaseRoundup) error { saved = r; return nil }
	svc := newTestSvc(store, &MockBlobStore{})
	x := &recordingExporter{}
	svc.SetSiteExporter(x)

	r := syntheticRoundup()
	r.Slug, r.PeriodKey = "james", "week-44-2026"
	r.AiSummary = "old third-person text"
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	if err := svc.SaveRoundupStory(context.Background(), r, "Four runs. Legs gone.", now); err != nil {
		t.Fatal(err)
	}
	if saved == nil || saved.AiSummary != "Four runs. Legs gone." || saved.AiSummaryModel != RoundupStoryModel ||
		!saved.AiSummaryGeneratedAt.AsTime().Equal(now) {
		t.Fatalf("saved = %+v", saved)
	}
	if saved.TotalActivities != 4 || saved.HighestAvgBpm != 160 {
		t.Error("only the story changes; the period's numbers stay as stored")
	}
	if len(x.roundups) != 1 || x.roundups[0] != "james/week-44-2026" {
		t.Errorf("site export not told: %v", x.roundups)
	}

	if err := svc.SaveRoundupStory(context.Background(), r, "", now); err == nil {
		t.Error("empty text must never overwrite a story")
	}
	store.SetRoundupFunc = func(context.Context, *pbactivity.ShowcaseRoundup) error { return errors.New("boom") }
	x.roundups = nil
	if err := svc.SaveRoundupStory(context.Background(), r, "Text.", now); err == nil || len(x.roundups) != 0 {
		t.Errorf("failed save must error and not signal: %v %v", err, x.roundups)
	}
}

func TestGenerateRoundupStampsStory(t *testing.T) {
	store := &MockActivityStore{}
	store.GetShowcasePreferencesFunc = func(context.Context, string) (*pbactivity.ShowcaseProfile, error) {
		return &pbactivity.ShowcaseProfile{Slug: "james", RoundupSettings: &pbactivity.RoundupSettings{EnabledWeekly: true}}, nil
	}
	store.ListShowcaseEntriesInRangeFunc = func(context.Context, string, time.Time, time.Time) ([]*pbactivity.ShowcaseProfileEntry, error) {
		return []*pbactivity.ShowcaseProfileEntry{{ShowcaseId: "s1", ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_RUN, DurationSeconds: 1800}}, nil
	}
	svc := newTestSvc(store, &MockBlobStore{})
	svc.AI = &fakeCompleter{out: "One run and done."}
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	r, err := svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, start, start.AddDate(0, 0, 7))
	if err != nil || r == nil {
		t.Fatal(err)
	}
	if r.AiSummaryModel != RoundupStoryModel || r.AiSummaryGeneratedAt == nil {
		t.Errorf("new roundups must record who wrote the story: %q %v", r.AiSummaryModel, r.AiSummaryGeneratedAt)
	}

	svc.AI = &fakeCompleter{err: errors.New("down")}
	r, _ = svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, start, start.AddDate(0, 0, 7))
	if r.AiSummary != "" || r.AiSummaryModel != "" || r.AiSummaryGeneratedAt != nil {
		t.Error("no story, no stamp")
	}
}

func TestRoundupStoryPromptMatchesGenerator(t *testing.T) {
	r := syntheticRoundup()
	sys, prompt := RoundupStoryPrompt(r)
	f := &fakeCompleter{out: "Four runs."}
	generateRoundupAISummary(context.Background(), infra.NewLogger(), f, r)
	if f.got.System != sys || f.got.Prompt != prompt {
		t.Error("the cost estimate must measure the prompt actually sent")
	}
	if !strings.Contains(prompt, "Sessions: 4") {
		t.Errorf("prompt lacks the period data: %s", prompt)
	}
}
