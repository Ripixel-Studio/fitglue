package class_board

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/user_input"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var classStart = time.Date(2026, 11, 19, 18, 3, 10, 0, time.UTC)

func hevyClass(t pbactivity.ActivityType, name string, sets ...*pbactivity.StrengthSet) *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{
		Source: pbactivity.ActivitySource_SOURCE_HEVY, ExternalId: "hevy-abc", Name: name, Type: t,
		StartTime: timestamppb.New(classStart),
		Sessions: []*pbactivity.Session{{StartTime: timestamppb.New(classStart), TotalElapsedTime: 3299,
			StrengthSets: sets}},
	}
}

func placeholder() *pbactivity.StrengthSet {
	return &pbactivity.StrengthSet{ExerciseName: "Bootcamp", DurationSeconds: 3000}
}

func TestIsClass(t *testing.T) {
	cf := pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT
	cases := []struct {
		name string
		a    *pbactivity.StandardizedActivity
		want bool
	}{
		{"hevy placeholder class", hevyClass(cf, "Hyrox Class", placeholder()), true},
		{"HIIT with no sets", hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING, "Hybrid Class"), true},
		{"spin class", hevyClass(cf, "Hybrid Spinning Class", placeholder()), false},
		{"a ride", hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_RIDE, "Hybrid Class", placeholder()), false},
		{"real sets logged", hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING, "Half Hyrox PT",
			&pbactivity.StrengthSet{ExerciseName: "Bench Press", Reps: 10, WeightKg: 60},
			&pbactivity.StrengthSet{ExerciseName: "Sled Push", DistanceMeters: 50}), false},
		{"one real exercise with reps", hevyClass(cf, "Hyrox Class", &pbactivity.StrengthSet{ExerciseName: "Wall Ball", Reps: 100}), false},
		{"not from hevy", func() *pbactivity.StandardizedActivity {
			a := hevyClass(cf, "Hyrox Class", placeholder())
			a.Source = pbactivity.ActivitySource_SOURCE_STRAVA
			return a
		}(), false},
		{"pilates", hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_PILATES, "Tower Pilates", placeholder()), false},
	}
	for _, c := range cases {
		if got := IsClass(c.a); got != c.want {
			t.Errorf("%s: IsClass = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWaitErrorCarriesTheClass(t *testing.T) {
	a := hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT, "Hyrox Class", placeholder())
	w := waitError("stable-1", a)
	if w.ActivityID != "stable-1" || w.EnricherProviderID != "class-board" || w.RequiredFields[0] != "workout_data" {
		t.Fatalf("wait = %+v", w)
	}
	m := w.Metadata
	if m["display.field_types"] != `{"workout_data":"custom:class_board"}` || m["display.title"] != "Class board" {
		t.Errorf("display = %v", m)
	}
	if m["class.title"] != "Hyrox Class" || m["class.start"] != "2026-11-19T18:03:10Z" ||
		m["class.end"] != "2026-11-19T18:58:09Z" || m["class.duration_s"] != "3299" {
		t.Errorf("class = %v", m)
	}
	if !strings.Contains(m["display.summary"], "Hyrox Class") {
		t.Errorf("summary = %q", m["display.summary"])
	}
}

func answer(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyAnswerReplacesThePlaceholder(t *testing.T) {
	a := hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT, "Hyrox Class", placeholder())
	raw := answer(t, []map[string]any{
		{"exercise": "Wall Balls", "notes": "", "sets": []map[string]any{
			{"reps": 25, "weight_kg": 6, "estimated": true}, {"reps": 25, "weight_kg": 6, "estimated": true}}},
		{"exercise": "Sled Push", "notes": "", "sets": []map[string]any{{"distance_meters": 20, "weight_kg": 152, "estimated": false}}},
	})
	res, err := applyAnswer(a, raw)
	if err != nil {
		t.Fatal(err)
	}
	sets := a.Sessions[0].StrengthSets
	if len(sets) != 3 || sets[0].ExerciseName != "Wall Balls" || !sets[0].Estimated || sets[2].Estimated || sets[2].DistanceMeters != 20 {
		t.Fatalf("sets = %v", sets)
	}
	if res.SectionHeader != SectionHeader || !strings.Contains(res.Description, "Reps estimated from the class board.") {
		t.Errorf("description = %q / %q", res.SectionHeader, res.Description)
	}
	// Nothing estimated: no line.
	a2 := hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT, "Hyrox Class", placeholder())
	res2, _ := applyAnswer(a2, answer(t, []map[string]any{{"exercise": "Burpees", "sets": []map[string]any{{"reps": 10}}}}))
	if res2.Description != "" {
		t.Errorf("no estimate, no line: %q", res2.Description)
	}
}

func TestApplyAnswerEmptyKeepsThePlaceholder(t *testing.T) {
	a := hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT, "Hyrox Class", placeholder())
	res, err := applyAnswer(a, "")
	if err != nil || !res.Skipped || a.Sessions[0].StrengthSets[0].ExerciseName != "Bootcamp" {
		t.Fatalf("res=%+v err=%v sets=%v", res, err, a.Sessions[0].StrengthSets)
	}
	if _, err := applyAnswer(a, "ten burpees"); err == nil {
		t.Error("a typed answer must be refused")
	}
}

func TestEnrichSkipsWhatIsNotAClass(t *testing.T) {
	a := hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_RIDE, "Spin", placeholder())
	res, err := (&Provider{}).Enrich(context.Background(), quiet(), a, nil, nil, false)
	if err != nil || !res.Skipped {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	c := hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT, "Hyrox Class", placeholder())
	res, err = (&Provider{}).Enrich(context.Background(), quiet(), c, nil, nil, true)
	if err != nil || !res.Skipped {
		t.Fatalf("doNotRetry: res=%+v err=%v", res, err)
	}
	if _, err := (&Provider{}).Enrich(context.Background(), quiet(), c, nil, nil, false); err == nil {
		t.Error("a class with no service must error, not silently pass")
	} else {
		var w *user_input.WaitForInputError
		if errors.As(err, &w) {
			t.Error("no service: a plain error, not a wait")
		}
	}
}

func TestEnrichResume(t *testing.T) {
	a := hevyClass(pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT, "Hyrox Class", placeholder())
	pi := &pbpipeline.PendingInput{InputData: map[string]string{"workout_data": answer(t, []map[string]any{
		{"exercise": "Wall Balls", "sets": []map[string]any{{"reps": 30, "estimated": true}}}})}}
	if _, err := (&Provider{}).EnrichResume(context.Background(), a, nil, pi); err != nil {
		t.Fatal(err)
	}
	if a.Sessions[0].StrengthSets[0].ExerciseName != "Wall Balls" {
		t.Fatalf("sets = %v", a.Sessions[0].StrengthSets)
	}
	if (&Provider{}).ProviderType() != pbplugin.EnricherProviderType_ENRICHER_PROVIDER_CLASS_BOARD {
		t.Error("provider type")
	}
}
