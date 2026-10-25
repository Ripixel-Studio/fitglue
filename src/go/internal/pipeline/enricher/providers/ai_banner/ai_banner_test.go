package ai_banner

import (
	user "github.com/fitglue/server/src/go/pkg/domain/user"

	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAIBanner_Name(t *testing.T) {
	provider := NewAIBannerProvider()
	expected := "ai-banner"
	if provider.Name() != expected {
		t.Errorf("Expected provider name %q, got %q", expected, provider.Name())
	}
}

func TestAIBanner_ProviderType(t *testing.T) {
	provider := NewAIBannerProvider()
	expected := pbplugin.EnricherProviderType_ENRICHER_PROVIDER_AI_BANNER
	if provider.ProviderType() != expected {
		t.Errorf("Expected provider type %v, got %v", expected, provider.ProviderType())
	}
}

func TestAIBanner_Enrich_NoActivityID(t *testing.T) {
	provider := NewAIBannerProvider()
	provider.Service = &bootstrap.Service{}

	// Create activity without ID
	activity := &pbactivity.StandardizedActivity{
		StartTime: timestamppb.New(time.Now()),
		Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
	}

	user := &user.Record{
		UserProfile: &pbuser.UserProfile{
			UserId: "test-user",
		},
	}

	result, err := provider.Enrich(context.Background(), slog.Default(), activity, user, nil, false)
	if err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// Verify skipped due to no asset folder ID (neither pipeline_execution_id nor activity.ExternalId)
	if result.Metadata["status"] != "skipped" {
		t.Errorf("Expected status=skipped, got %s", result.Metadata["status"])
	}
	if result.Metadata["reason"] != "no_asset_folder_id" {
		t.Errorf("Expected reason=no_asset_folder_id, got %s", result.Metadata["reason"])
	}
}

func TestBuildActivityContext(t *testing.T) {
	tests := []struct {
		name     string
		activity *pbactivity.StandardizedActivity
		contains []string
	}{
		{
			name: "morning run basic",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 7, 30, 0, 0, time.UTC)),
			},
			contains: []string{
				"run",
				"early morning",
			},
		},
		{
			name: "10K run with distance and pace",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 7, 30, 0, 0, time.UTC)),
				Sessions: []*pbactivity.Session{
					{
						TotalElapsedTime: 3120,  // 52 minutes
						TotalDistance:    10200, // 10.2 km
					},
				},
			},
			contains: []string{
				"run",
				"10.2 km",
				"10K distance",
				"steady moderate pace",
			},
		},
		{
			name: "marathon distance run",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 6, 0, 0, 0, time.UTC)),
				Sessions: []*pbactivity.Session{
					{
						TotalElapsedTime: 14400, // 4 hours
						TotalDistance:    42500, // 42.5 km
					},
				},
			},
			contains: []string{
				"marathon or ultra distance",
				"epic endurance",
			},
		},
		{
			name: "afternoon ride with distance",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RIDE,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 14, 0, 0, 0, time.UTC)),
				Sessions: []*pbactivity.Session{
					{
						TotalElapsedTime: 7200,  // 2 hours
						TotalDistance:    55000, // 55 km
					},
				},
			},
			contains: []string{
				"ride",
				"afternoon",
				"55.0 km",
				"substantial ride",
				"moderate endurance pace",
			},
		},
		{
			name: "century ride",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RIDE,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 6, 0, 0, 0, time.UTC)),
				Sessions: []*pbactivity.Session{
					{
						TotalElapsedTime: 21600,  // 6 hours
						TotalDistance:    165000, // 165 km
					},
				},
			},
			contains: []string{
				"century ride",
				"epic distance",
			},
		},
		{
			name: "pool swim",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_SWIM,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 12, 0, 0, 0, time.UTC)),
				Sessions: []*pbactivity.Session{
					{
						TotalElapsedTime: 2700, // 45 minutes
						TotalDistance:    2000, // 2 km
					},
				},
			},
			contains: []string{
				"swim",
				"2.0 km",
				"half ironman swim",
			},
		},
		{
			name: "trail run",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_TRAIL_RUN,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 9, 0, 0, 0, time.UTC)),
				Sessions: []*pbactivity.Session{
					{
						TotalElapsedTime: 5400,  // 90 minutes
						TotalDistance:    12000, // 12 km
					},
				},
			},
			contains: []string{
				"trail run",
				"trail running",
				"natural terrain",
				"forests",
			},
		},
		{
			name: "evening strength",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 18, 30, 0, 0, time.UTC)),
			},
			contains: []string{
				"weight training",
				"evening",
			},
		},
		{
			name: "bodyweight core workout",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 22, 0, 0, 0, time.UTC)),
				Sessions: []*pbactivity.Session{
					{
						TotalElapsedTime: 900, // 15 minutes
						StrengthSets: []*pbactivity.StrengthSet{
							{ExerciseName: "Plank", Reps: 1, WeightKg: 0, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_ABDOMINALS},
							{ExerciseName: "Dead Bug", Reps: 30, WeightKg: 0, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_ABDOMINALS},
							{ExerciseName: "Russian Twist", Reps: 70, WeightKg: 0, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_ABDOMINALS},
							{ExerciseName: "Crunch", Reps: 30, WeightKg: 0, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_ABDOMINALS},
						},
					},
				},
			},
			contains: []string{
				"night",
				"Plank",
				"Dead Bug",
				"Russian Twist",
				"bodyweight only",
				"abdominals",
			},
		},
		{
			name: "weighted strength workout",
			activity: &pbactivity.StandardizedActivity{
				Type:      pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
				StartTime: timestamppb.New(time.Date(2026, 1, 21, 17, 0, 0, 0, time.UTC)),
				Sessions: []*pbactivity.Session{
					{
						TotalElapsedTime: 3600, // 60 minutes
						StrengthSets: []*pbactivity.StrengthSet{
							{ExerciseName: "Bench Press", Reps: 8, WeightKg: 80, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_CHEST},
							{ExerciseName: "Squat", Reps: 5, WeightKg: 100, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_QUADRICEPS},
							{ExerciseName: "Deadlift", Reps: 5, WeightKg: 120, PrimaryMuscleGroup: pbactivity.MuscleGroup_MUSCLE_GROUP_HAMSTRINGS},
						},
					},
				},
			},
			contains: []string{
				"Bench Press",
				"Squat",
				"Deadlift",
				"weighted exercises",
				"equipment",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			context := buildActivityContext(tt.activity)
			for _, contains := range tt.contains {
				if !containsIgnoreCase(context, contains) {
					t.Errorf("Expected context to contain %q, got: %s", contains, context)
				}
			}
		})
	}
}

func TestBuildLLMPrompt(t *testing.T) {
	tests := []struct {
		name     string
		context  string
		style    string
		subject  string
		contains []string
	}{
		{
			name:    "vibrant abstract",
			context: "Activity type: run\nTime of day: morning",
			style:   "vibrant",
			subject: "abstract",
			contains: []string{
				"image prompt generator",
				"vibrant",
				"No people",
				"EQUIPMENT",
			},
		},
		{
			name:    "minimal abstract",
			context: "Activity type: ride",
			style:   "minimal",
			subject: "abstract",
			contains: []string{
				"minimalist",
				"No people",
			},
		},
		{
			name:    "dramatic abstract",
			context: "Activity type: weight training",
			style:   "dramatic",
			subject: "abstract",
			contains: []string{
				"dramatic",
				"bold contrast",
			},
		},
		{
			name:    "male subject",
			context: "Activity type: run",
			style:   "vibrant",
			subject: "male",
			contains: []string{
				"male athlete",
			},
		},
		{
			name:    "female subject",
			context: "Activity type: run",
			style:   "vibrant",
			subject: "female",
			contains: []string{
				"female athlete",
			},
		},
		{
			name:    "critical rules present",
			context: "Activity type: run",
			style:   "vibrant",
			subject: "abstract",
			contains: []string{
				"CRITICAL RULES",
				"NEVER mention text",
				"REAL-WORLD FITNESS SETTING",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildLLMPrompt(tt.context, tt.style, tt.subject)
			for _, contains := range tt.contains {
				if !containsIgnoreCase(prompt, contains) {
					t.Errorf("Expected prompt to contain %q, got: %s", contains, prompt)
				}
			}
		})
	}
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(s) > 0 && containsIgnoreCase(s[1:], substr) ||
			(len(s) >= len(substr) && equalFoldPrefix(s, substr)))
}

func equalFoldPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c1 := s[i]
		c2 := prefix[i]
		if c1 >= 'A' && c1 <= 'Z' {
			c1 += 'a' - 'A'
		}
		if c2 >= 'A' && c2 <= 'Z' {
			c2 += 'a' - 'A'
		}
		if c1 != c2 {
			return false
		}
	}
	return true
}
