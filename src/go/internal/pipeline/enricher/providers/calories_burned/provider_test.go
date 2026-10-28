package calories_burned

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	domainuser "github.com/fitglue/server/src/go/pkg/domain/user"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

func newTestActivity(actType pbactivity.ActivityType, elapsedSec float64) *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{
		Type: actType,
		Sessions: []*pbactivity.Session{
			{TotalElapsedTime: elapsedSec},
		},
	}
}

func newUser() *domainuser.Record {
	return &domainuser.Record{UserProfile: &pbuser.UserProfile{UserId: "test-user"}}
}

// --- getMET ---

func TestGetMET_KnownTypes(t *testing.T) {
	cases := []struct {
		actType pbactivity.ActivityType
		minMET  float64
	}{
		{pbactivity.ActivityType_ACTIVITY_TYPE_RUN, 9.0},
		{pbactivity.ActivityType_ACTIVITY_TYPE_RIDE, 7.0},
		{pbactivity.ActivityType_ACTIVITY_TYPE_WALK, 3.0},
		{pbactivity.ActivityType_ACTIVITY_TYPE_SWIM, 7.0},
	}
	for _, c := range cases {
		met := getMET(c.actType)
		if met < c.minMET {
			t.Errorf("getMET(%v) = %.1f, expected >= %.1f", c.actType, met, c.minMET)
		}
	}
}

func TestGetMET_UnknownType(t *testing.T) {
	met := getMET(pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED)
	if met != 5.0 {
		t.Errorf("expected default MET 5.0, got %.1f", met)
	}
}

// --- Enrich ---

func TestCaloriesBurned_Enrich_NoDuration(t *testing.T) {
	p := NewCaloriesBurned()
	act := &pbactivity.StandardizedActivity{
		Type:     pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		Sessions: []*pbactivity.Session{},
	}
	res, err := p.Enrich(context.Background(), slog.Default(), act, newUser(), nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Metadata["calories_status"] != "skipped" {
		t.Errorf("expected 'skipped' for no duration, got %q", res.Metadata["calories_status"])
	}
}

func TestCaloriesBurned_Enrich_BasicRun(t *testing.T) {
	p := NewCaloriesBurned()
	// 1 hour run at default 70kg weight
	act := newTestActivity(pbactivity.ActivityType_ACTIVITY_TYPE_RUN, 3600.0)
	res, err := p.Enrich(context.Background(), slog.Default(), act, newUser(), map[string]string{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Metadata["calories_status"] != "success" {
		t.Errorf("expected success, got %q", res.Metadata["calories_status"])
	}
	if !strings.Contains(res.Description, "kcal") {
		t.Errorf("expected kcal in description, got %q", res.Description)
	}
}

func TestCaloriesBurned_Enrich_CustomWeight(t *testing.T) {
	p := NewCaloriesBurned()
	act := newTestActivity(pbactivity.ActivityType_ACTIVITY_TYPE_RIDE, 3600.0)
	inputs := map[string]string{"user_weight": "80"}
	res, err := p.Enrich(context.Background(), slog.Default(), act, newUser(), inputs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 7.5 MET * 80kg * 1hr = 600 kcal
	if !strings.Contains(res.Description, "600") {
		t.Errorf("expected 600 kcal for 80kg rider, got %q", res.Description)
	}
}

func TestCaloriesBurned_Enrich_FunMode(t *testing.T) {
	p := NewCaloriesBurned()
	act := newTestActivity(pbactivity.ActivityType_ACTIVITY_TYPE_RUN, 3600.0)
	inputs := map[string]string{"fun_mode": "true"}
	res, err := p.Enrich(context.Background(), slog.Default(), act, newUser(), inputs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fun mode adds food comparison (≈ x pizza 🍕)
	if !strings.Contains(res.Description, "≈") {
		t.Errorf("expected food equivalent in fun mode, got %q", res.Description)
	}
}

func TestCaloriesBurned_Enrich_InvalidWeight(t *testing.T) {
	p := NewCaloriesBurned()
	act := newTestActivity(pbactivity.ActivityType_ACTIVITY_TYPE_RUN, 3600.0)
	inputs := map[string]string{"user_weight": "notanumber"}
	res, err := p.Enrich(context.Background(), slog.Default(), act, newUser(), inputs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use default 70kg
	if res.Metadata["weight_kg"] != "70" {
		t.Errorf("expected default weight 70, got %q", res.Metadata["weight_kg"])
	}
}

func TestCaloriesBurned_Enrich_MultipleSessionsSummed(t *testing.T) {
	p := NewCaloriesBurned()
	act := &pbactivity.StandardizedActivity{
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		Sessions: []*pbactivity.Session{
			{TotalElapsedTime: 1800},
			{TotalElapsedTime: 1800},
		},
	}
	res, err := p.Enrich(context.Background(), slog.Default(), act, newUser(), nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Total = 3600 sec = 1hr, should be same as single session test
	if res.Metadata["calories_status"] != "success" {
		t.Errorf("expected success, got %q", res.Metadata["calories_status"])
	}
}

func TestCaloriesBurned_ProviderMetadata(t *testing.T) {
	p := NewCaloriesBurned()
	if p.Name() != "calories-burned" {
		t.Errorf("expected 'calories-burned', got %q", p.Name())
	}
}

// Birmingham solo: Strava has 1,346 kcal; the old booster estimated 523 (MET 5 for
// "Workout", 70 kg, 1:29:42). The platform figure wins.
func TestCaloriesBurned_Enrich_PrefersPlatformCalories(t *testing.T) {
	p := NewCaloriesBurned()
	strava := 1346.0
	act := &pbactivity.StandardizedActivity{
		Type:     pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT,
		Sessions: []*pbactivity.Session{{TotalElapsedTime: 5382, TotalCalories: &strava}},
	}
	res, err := p.Enrich(context.Background(), slog.Default(), act, newUser(), map[string]string{"fun_mode": "true"}, false)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if res.Enrichments.Calories.Kcal != 1346 || res.Metadata["calories_source"] != "platform" {
		t.Errorf("kcal %d source %q, want 1346 from the platform", res.Enrichments.Calories.Kcal, res.Metadata["calories_source"])
	}
	if !strings.HasPrefix(res.Description, "🔥 Calories: 1346 kcal ≈ 4.7 slice of pizza") {
		t.Errorf("description %q", res.Description)
	}
	if _, ok := res.Metadata["met_value"]; ok {
		t.Error("no MET when the platform gave calories")
	}
}

func TestCaloriesBurned_Enrich_EstimatesWithoutPlatformCalories(t *testing.T) {
	p := NewCaloriesBurned()
	res, err := p.Enrich(context.Background(), slog.Default(), newTestActivity(pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING, 3600), newUser(), nil, false)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if res.Metadata["calories_source"] != "estimate" || res.Enrichments.Calories.Kcal != 350 {
		t.Errorf("source %q kcal %d, want the MET estimate 5.0 × 70 × 1 = 350", res.Metadata["calories_source"], res.Enrichments.Calories.Kcal)
	}
}
