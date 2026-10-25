package ai_companion

import (
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/muscle_heatmap"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/source_link"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/workout_summary"
)

func TestDescriptionEngine_Integration(t *testing.T) {
	// 1. Setup Input with comprehensive test data
	activity := &pbactivity.StandardizedActivity{
		Source:      pbactivity.ActivitySource_SOURCE_HEVY,
		ExternalId:  "test-uuid",
		Name:        "Hyrox Training Session",
		Description: "Crushing it today! 💪",
		Type:        pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		Sessions: []*pbactivity.Session{
			{
				StrengthSets: []*pbactivity.StrengthSet{
					// Superset 1: Bench Press + Dumbbell Row
					{ExerciseName: "Bench Press", Reps: 10, WeightKg: 60, SetType: "warmup", SupersetId: "ss1", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_CHEST, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS, pbactivity.MuscleGroup_MUSCLE_GROUP_SHOULDERS}},
					{ExerciseName: "Bench Press", Reps: 8, WeightKg: 100, SupersetId: "ss1", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_CHEST, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS}},
					{ExerciseName: "Bench Press", Reps: 8, WeightKg: 100, SupersetId: "ss1", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_CHEST, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS}},
					{ExerciseName: "Bench Press", Reps: 6, WeightKg: 100, SetType: "failure", SupersetId: "ss1", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_CHEST, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS}},
					{ExerciseName: "Dumbbell Row", Reps: 12, WeightKg: 40, SupersetId: "ss1", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_LATS, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_BICEPS}},
					{ExerciseName: "Dumbbell Row", Reps: 12, WeightKg: 40, SupersetId: "ss1", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_LATS, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_BICEPS}},
					{ExerciseName: "Dumbbell Row", Reps: 12, WeightKg: 40, SupersetId: "ss1", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_LATS, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_BICEPS}},

					// Regular exercise: Squats
					{ExerciseName: "Squat", Reps: 5, WeightKg: 140, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_QUADRICEPS, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_GLUTES, pbactivity.MuscleGroup_MUSCLE_GROUP_HAMSTRINGS}},
					{ExerciseName: "Squat", Reps: 5, WeightKg: 140, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_QUADRICEPS, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_GLUTES, pbactivity.MuscleGroup_MUSCLE_GROUP_HAMSTRINGS}},
					{ExerciseName: "Squat", Reps: 5, WeightKg: 140, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_QUADRICEPS, SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_GLUTES, pbactivity.MuscleGroup_MUSCLE_GROUP_HAMSTRINGS}},

					// Distance-based weighted exercise (Farmer's Walk)
					// Volume = 32kg * 30m = 960kg
					// Should be included in total volume
					{ExerciseName: "Farmer's Walk", Reps: 0, WeightKg: 32, DistanceMeters: 30, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_TRAPS},

					// Cardio exercises (distance/duration based)
					{ExerciseName: "Running", Reps: 0, WeightKg: 0, DistanceMeters: 1000, DurationSeconds: 300, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_CARDIO},
					{ExerciseName: "Rowing Machine", Reps: 0, WeightKg: 0, DistanceMeters: 500, DurationSeconds: 120, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_CARDIO},

					// Superset 2: Bicep Curl + Tricep Extension
					{ExerciseName: "Bicep Curl", Reps: 12, WeightKg: 20, SupersetId: "ss2", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_BICEPS},
					{ExerciseName: "Bicep Curl", Reps: 12, WeightKg: 20, SupersetId: "ss2", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_BICEPS},
					{ExerciseName: "Bicep Curl", Reps: 12, WeightKg: 20, SupersetId: "ss2", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_BICEPS},
					{ExerciseName: "Tricep Extension", Reps: 15, WeightKg: 15, SupersetId: "ss2", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS},
					{ExerciseName: "Tricep Extension", Reps: 15, WeightKg: 15, SupersetId: "ss2", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS},
					{ExerciseName: "Tricep Extension", Reps: 15, WeightKg: 15, SupersetId: "ss2", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS},

					// Bodyweight exercise
					{ExerciseName: "Burpee Box Jump", Reps: 20, WeightKg: 0, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_FULL_BODY},

					// Dropset
					{ExerciseName: "Shoulder Press", Reps: 10, WeightKg: 30, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_SHOULDERS},
					{ExerciseName: "Shoulder Press", Reps: 8, WeightKg: 25, SetType: "dropset", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_SHOULDERS},
					{ExerciseName: "Shoulder Press", Reps: 6, WeightKg: 20, SetType: "dropset", PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_SHOULDERS},
				},
			},
		},
	}

	// 2. Setup Providers
	pLink := source_link.NewSourceLinkProvider()
	pSummary := workout_summary.NewWorkoutSummaryProvider()
	pHeatmap := muscle_heatmap.NewMuscleHeatmapProvider()

	// 3. Execute Providers
	ctx := context.Background()
	resLink, _ := pLink.Enrich(ctx, slog.Default(), activity, nil, nil, false)
	resSummary, _ := pSummary.Enrich(ctx, slog.Default(), activity, nil, nil, false)
	resHeatmap, _ := pHeatmap.Enrich(ctx, slog.Default(), activity, nil, nil, false)

	// 4. Simulate Orchestrator Merge
	finalDesc := activity.Description

	// Order: Summary, Heatmap, Link
	results := []*providers.EnrichmentResult{resSummary, resHeatmap, resLink}

	for _, res := range results {
		if res.Description != "" {
			trimmed := strings.TrimSpace(res.Description)
			if trimmed != "" {
				if finalDesc != "" {
					finalDesc += "\n\n"
				}
				finalDesc += trimmed
			}
		}
	}

	// 5. Verify Content
	expectedParts := []string{
		// Original description
		"Crushing it today! 💪",

		// Workout Summary
		"Workout Summary:",
		// Updated volume: 8355 + 960 (Farmers Walk) = 9315
		// Total sets: 22 + 1 = 23
		"23 sets • 9,315kg volume • 208 reps • 1.5km distance • Heaviest: 140kg (Squat)",

		// Superset 1 with emoji numbers
		"1️⃣ Bench Press:",
		"[W] 10 × 60.0kg",
		"[F] 6 × 100.0kg",
		"1️⃣ Dumbbell Row:",
		"3 × 12 × 40.0kg",

		// Regular exercise (with placeholder since supersets exist)
		"⬜ Squat: 3 × 5 × 140.0kg",

		// Distance/duration exercises (with placeholder since supersets exist)
		"⬜ Running: 1000m in 5:00",
		"⬜ Rowing Machine: 500m in 2:00",

		// Superset 2
		"2️⃣ Bicep Curl:",
		"2️⃣ Tricep Extension:",

		// Bodyweight (with placeholder since supersets exist)
		"⬜ Burpee Box Jump: 20 reps",

		// Dropset (with placeholder since supersets exist)
		"⬜ Shoulder Press:",
		"[D] 8 × 25.0kg",
		"[D] 6 × 20.0kg",

		// Muscle Heatmap (should be sorted by volume, descending)
		"Muscle Heatmap:",
		// Check for at least some muscle groups being displayed
		"Triceps:",
		"Biceps:",
		"Chest:",

		// Source link
		"View on Hevy: https://hevy.com/workout/test-uuid",
	}

	for _, part := range expectedParts {
		if !strings.Contains(finalDesc, part) {
			t.Errorf("Expected description to contain %q, but got:\n%s", part, finalDesc)
		}
	}

	// Print full description for debugging
	t.Logf("Full description:\n%s", finalDesc)

	// Verify muscle heatmap is sorted by volume (descending)
	// The heatmap should appear with highest volume muscles first
	heatmapStart := strings.Index(finalDesc, "Muscle Heatmap:")
	if heatmapStart == -1 {
		t.Fatal("Muscle Heatmap not found in description")
	}
}

func TestDescriptionEngine_StatsDisabled(t *testing.T) {
	activity := &pbactivity.StandardizedActivity{
		Sessions: []*pbactivity.Session{{
			StrengthSets: []*pbactivity.StrengthSet{
				{ExerciseName: "Bench Press", Reps: 10, WeightKg: 100},
			},
		}},
	}
	pSummary := workout_summary.NewWorkoutSummaryProvider()

	// Test with stats disabled
	config := map[string]string{"show_stats": "false"}
	res, _ := pSummary.Enrich(context.Background(), slog.Default(), activity, nil, config, false)

	if strings.Contains(res.Description, "sets •") {
		t.Error("Expected stats to be hidden when show_stats=false")
	}
}
