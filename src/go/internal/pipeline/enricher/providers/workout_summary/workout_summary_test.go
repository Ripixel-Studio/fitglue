// nolint:proto-json
package workout_summary

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

func makeSet(exercise string, reps int32, weightKg float64, setType string) *pbactivity.StrengthSet {
	return &pbactivity.StrengthSet{
		ExerciseName: exercise,
		Reps:         reps,
		WeightKg:     weightKg,
		SetType:      setType,
	}
}

func makeDistanceSet(exercise string, distM float64, durationSec int32) *pbactivity.StrengthSet {
	return &pbactivity.StrengthSet{
		ExerciseName:    exercise,
		DistanceMeters:  distM,
		DurationSeconds: durationSec,
	}
}

func enrichWith(t *testing.T, sets []*pbactivity.StrengthSet, inputs map[string]string) (string, map[string]string) {
	t.Helper()
	p := NewWorkoutSummaryProvider()
	act := &pbactivity.StandardizedActivity{
		Sessions: []*pbactivity.Session{
			{StrengthSets: sets},
		},
	}
	res, err := p.Enrich(context.Background(), slog.Default(), act, nil, inputs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res.Description, res.Metadata
}

// --- getSetTypeIndicator ---

func TestGetSetTypeIndicator(t *testing.T) {
	cases := []struct {
		setType  string
		expected string
	}{
		{"warmup", "[W] "},
		{"failure", "[F] "},
		{"dropset", "[D] "},
		{"normal", ""},
		{"unknown", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := getSetTypeIndicator(c.setType)
		if got != c.expected {
			t.Errorf("getSetTypeIndicator(%q) = %q, want %q", c.setType, got, c.expected)
		}
	}
}

// --- formatDuration ---

func TestFormatDurationWS(t *testing.T) {
	cases := []struct {
		seconds  int32
		expected string
	}{
		{0, "0:00"},
		{30, "0:30"},
		{60, "1:00"},
		{90, "1:30"},
		{3600, "60:00"},
	}
	for _, c := range cases {
		got := formatDuration(c.seconds)
		if got != c.expected {
			t.Errorf("formatDuration(%d) = %q, want %q", c.seconds, got, c.expected)
		}
	}
}

// --- formatWithCommas ---

func TestFormatWithCommas(t *testing.T) {
	got := formatWithCommas(1234567.0, "kg")
	if !strings.Contains(got, "kg") {
		t.Errorf("expected kg in output, got %q", got)
	}
}

// --- Enrich ---

func TestWorkoutSummary_NoSets_Skipped(t *testing.T) {
	p := NewWorkoutSummaryProvider()
	act := &pbactivity.StandardizedActivity{}
	res, err := p.Enrich(context.Background(), slog.Default(), act, nil, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Metadata["status"] != "skipped" {
		t.Errorf("expected skipped, got %q", res.Metadata["status"])
	}
}

func TestWorkoutSummary_BasicSets(t *testing.T) {
	sets := []*pbactivity.StrengthSet{
		makeSet("Bench Press", 10, 100.0, "normal"),
		makeSet("Bench Press", 10, 100.0, "normal"),
	}
	desc, meta := enrichWith(t, sets, nil)
	if !strings.Contains(desc, "Bench Press") {
		t.Errorf("expected Bench Press in description, got %q", desc)
	}
	if meta["total_sets"] != "2" {
		t.Errorf("expected 2 sets, got %q", meta["total_sets"])
	}
}

func TestWorkoutSummary_CompactFormat(t *testing.T) {
	sets := []*pbactivity.StrengthSet{makeSet("Squat", 5, 150.0, "normal")}
	desc, _ := enrichWith(t, sets, map[string]string{"format": "compact"})
	// compact format: "5×150kg"
	if !strings.Contains(desc, "×") {
		t.Errorf("expected compact '×' format, got %q", desc)
	}
}

func TestWorkoutSummary_VerboseFormat(t *testing.T) {
	sets := []*pbactivity.StrengthSet{makeSet("Deadlift", 3, 200.0, "normal")}
	desc, _ := enrichWith(t, sets, map[string]string{"format": "verbose"})
	if !strings.Contains(desc, "kilograms") {
		t.Errorf("expected 'kilograms' in verbose format, got %q", desc)
	}
}

func TestWorkoutSummary_SetTypeIndicators(t *testing.T) {
	sets := []*pbactivity.StrengthSet{
		makeSet("Bench Press", 12, 60.0, "warmup"),
		makeSet("Bench Press", 8, 100.0, "failure"),
		makeSet("Bench Press", 6, 80.0, "dropset"),
	}
	desc, _ := enrichWith(t, sets, nil)
	if !strings.Contains(desc, "[W]") {
		t.Errorf("expected [W] warmup indicator, got %q", desc)
	}
	if !strings.Contains(desc, "[F]") {
		t.Errorf("expected [F] failure indicator, got %q", desc)
	}
	if !strings.Contains(desc, "[D]") {
		t.Errorf("expected [D] dropset indicator, got %q", desc)
	}
}

func TestWorkoutSummary_CollapsedIdenticalSets(t *testing.T) {
	sets := []*pbactivity.StrengthSet{
		makeSet("Squat", 10, 100.0, "normal"),
		makeSet("Squat", 10, 100.0, "normal"),
		makeSet("Squat", 10, 100.0, "normal"),
	}
	desc, _ := enrichWith(t, sets, nil)
	// Should show "3 × ..." for collapsed sets
	if !strings.Contains(desc, "3 ×") && !strings.Contains(desc, "3×") {
		t.Errorf("expected collapsed sets notation (3 ×), got %q", desc)
	}
}

func TestWorkoutSummary_DistanceSet(t *testing.T) {
	sets := []*pbactivity.StrengthSet{makeDistanceSet("Rowing", 500, 120)}
	desc, _ := enrichWith(t, sets, nil)
	if !strings.Contains(desc, "500") {
		t.Errorf("expected distance in description, got %q", desc)
	}
}

func TestWorkoutSummary_NoStats(t *testing.T) {
	sets := []*pbactivity.StrengthSet{makeSet("Bench Press", 10, 100.0, "normal")}
	desc, _ := enrichWith(t, sets, map[string]string{"show_stats": "false"})
	// Stats line shows volume, etc. — without it, no "volume" text
	_ = desc // just confirm no error
}

func TestWorkoutSummary_SupersetID(t *testing.T) {
	sets := []*pbactivity.StrengthSet{
		{ExerciseName: "Bench Press", Reps: 10, WeightKg: 80, SetType: "normal", SupersetId: "ss1"},
		{ExerciseName: "Pull-Up", Reps: 8, WeightKg: 0, SetType: "normal", SupersetId: "ss1"},
	}
	_, meta := enrichWith(t, sets, nil)
	if meta["has_supersets"] != "true" {
		t.Errorf("expected has_supersets=true, got %q", meta["has_supersets"])
	}
}

func TestWorkoutSummary_BodyweightSet(t *testing.T) {
	sets := []*pbactivity.StrengthSet{makeSet("Pull-Up", 8, 0, "normal")}
	desc, _ := enrichWith(t, sets, nil)
	if !strings.Contains(desc, "8 reps") {
		t.Errorf("expected '8 reps' for bodyweight set, got %q", desc)
	}
}

func TestWorkoutSummary_EmptyExerciseName(t *testing.T) {
	sets := []*pbactivity.StrengthSet{makeSet("", 10, 50.0, "normal")}
	desc, _ := enrichWith(t, sets, nil)
	if !strings.Contains(desc, "Unknown Exercise") {
		t.Errorf("expected 'Unknown Exercise' for empty name, got %q", desc)
	}
}

// --- formatSet / formatDistanceDuration ---

func TestFormatSet_DetailedWithWeight(t *testing.T) {
	p := NewWorkoutSummaryProvider()
	set := makeSet("Bench Press", 10, 100.0, "normal")
	got := p.formatSet(set, pbplugin.WorkoutSummaryFormat_WORKOUT_SUMMARY_FORMAT_DETAILED)
	if !strings.Contains(got, "100.0kg") {
		t.Errorf("expected 100.0kg in detailed format, got %q", got)
	}
}

func TestFormatSet_CompactBodyweight(t *testing.T) {
	p := NewWorkoutSummaryProvider()
	set := makeSet("Pull-Up", 8, 0, "normal")
	got := p.formatSet(set, pbplugin.WorkoutSummaryFormat_WORKOUT_SUMMARY_FORMAT_COMPACT)
	if got != "8 reps" {
		t.Errorf("expected '8 reps', got %q", got)
	}
}

func TestFormatSet_DistanceAndDuration(t *testing.T) {
	p := NewWorkoutSummaryProvider()
	set := makeDistanceSet("Sled Push", 50, 30)
	got := p.formatSet(set, pbplugin.WorkoutSummaryFormat_WORKOUT_SUMMARY_FORMAT_DETAILED)
	if !strings.Contains(got, "50") {
		t.Errorf("expected distance in output, got %q", got)
	}
}

func TestWorkoutSummary_ProviderMetadata(t *testing.T) {
	p := NewWorkoutSummaryProvider()
	if p.Name() != "workout-summary" {
		t.Errorf("expected 'workout-summary', got %q", p.Name())
	}
}

func TestWorkoutSummary_EstimatedSetsAreNotVolume(t *testing.T) {
	guess := makeSet("Sled Push", 0, 152.0, "normal")
	guess.DistanceMeters = 20
	guess.Estimated = true
	wb := makeSet("Wall Ball", 100, 6.0, "normal")
	wb.Estimated = true
	sets := []*pbactivity.StrengthSet{makeSet("Bench Press", 10, 100.0, "normal"), guess, wb}
	_, meta := enrichWith(t, sets, nil)
	if meta["total_volume"] != "1000.00" {
		t.Errorf("volume counts only real sets: got %q, want 1000.00", meta["total_volume"])
	}
	if meta["total_sets"] != "3" {
		t.Errorf("estimated sets are still listed: total_sets %q", meta["total_sets"])
	}
}
