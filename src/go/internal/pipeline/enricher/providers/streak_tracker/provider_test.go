package streak_tracker

import (
	user "github.com/fitglue/server/src/go/pkg/domain/user"

	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	"context"
	"log/slog"
	"strings"
	"testing"

	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func makeActivity(dateStr string) *pbactivity.StandardizedActivity {
	t, _ := time.Parse("2006-01-02", dateStr)
	return &pbactivity.StandardizedActivity{
		Name:      "Morning Run",
		StartTime: timestamppb.New(t),
	}
}

func TestStreakTracker_FirstActivity(t *testing.T) {
	var savedData map[string]interface{}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string) (map[string]interface{}, error) {
			return nil, nil // No existing data
		},
		SetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string, data map[string]interface{}) error {
			savedData = data
			return nil
		},
	}

	provider := &StreakTracker{}
	provider.SetService(&bootstrap.Service{DB: mockDB})

	res, err := provider.Enrich(context.Background(), slog.Default(), makeActivity("2026-02-08"), &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}, map[string]string{}, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Metadata["streak_current"] != "1" {
		t.Errorf("Expected streak_current=1, got %s", res.Metadata["streak_current"])
	}
	if savedData == nil {
		t.Fatal("Expected SetBoosterData to be called")
	}
	if savedData["current_streak"] != 1 {
		t.Errorf("Expected persisted current_streak=1, got %v", savedData["current_streak"])
	}
}

func TestStreakTracker_ConsecutiveDay(t *testing.T) {
	var savedData map[string]interface{}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"current_streak":     float64(5),
				"longest_streak":     float64(5),
				"last_activity_date": "2026-02-07",
			}, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string, data map[string]interface{}) error {
			savedData = data
			return nil
		},
	}

	provider := &StreakTracker{}
	provider.SetService(&bootstrap.Service{DB: mockDB})

	res, err := provider.Enrich(context.Background(), slog.Default(), makeActivity("2026-02-08"), &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}, map[string]string{}, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Metadata["streak_current"] != "6" {
		t.Errorf("Expected streak_current=6, got %s", res.Metadata["streak_current"])
	}
	if savedData["current_streak"] != 6 {
		t.Errorf("Expected persisted current_streak=6, got %v", savedData["current_streak"])
	}
}

func TestStreakTracker_SameDayDuplicate(t *testing.T) {
	var saved map[string]interface{}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"current_streak":     float64(5),
				"longest_streak":     float64(5),
				"last_activity_date": "2026-02-08",
			}, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string, data map[string]interface{}) error {
			saved = data
			return nil
		},
	}

	provider := &StreakTracker{}
	provider.SetService(&bootstrap.Service{DB: mockDB})

	res, err := provider.Enrich(context.Background(), slog.Default(), makeActivity("2026-02-08"), &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}, map[string]string{"external_id": "second"}, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Metadata["streak_current"] != "5" {
		t.Errorf("Expected streak_current=5 (no increment), got %s", res.Metadata["streak_current"])
	}
	if saved["current_streak"] != 5 {
		t.Errorf("Expected the stored streak to stay 5, got %v", saved["current_streak"])
	}
}

func TestStreakTracker_StreakBroken(t *testing.T) {
	var savedData map[string]interface{}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"current_streak":     float64(10),
				"longest_streak":     float64(10),
				"last_activity_date": "2026-02-05", // 3 days ago
			}, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string, data map[string]interface{}) error {
			savedData = data
			return nil
		},
	}

	provider := &StreakTracker{}
	provider.SetService(&bootstrap.Service{DB: mockDB})

	res, err := provider.Enrich(context.Background(), slog.Default(), makeActivity("2026-02-08"), &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}, map[string]string{}, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Metadata["streak_current"] != "1" {
		t.Errorf("Expected streak_current=1 (reset), got %s", res.Metadata["streak_current"])
	}
	if savedData["current_streak"] != 1 {
		t.Errorf("Expected persisted current_streak=1, got %v", savedData["current_streak"])
	}
}

// A late upload (or repost) of the day before a 5-day streak's last day reads the
// streak as it stood that day (4), and leaves the stored streak at 5.
func TestStreakTracker_OutOfOrderActivity(t *testing.T) {
	var saved map[string]interface{}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"current_streak":     float64(5),
				"longest_streak":     float64(5),
				"last_activity_date": "2026-02-08", // Today already recorded
			}, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string, data map[string]interface{}) error {
			saved = data
			return nil
		},
	}

	provider := &StreakTracker{}
	provider.SetService(&bootstrap.Service{DB: mockDB})

	res, err := provider.Enrich(context.Background(), slog.Default(), makeActivity("2026-02-07"), &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}, map[string]string{"external_id": "late"}, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Metadata["streak_current"] != "4" {
		t.Errorf("Expected streak_current=4 (as of 7 Feb), got %s", res.Metadata["streak_current"])
	}
	if saved["current_streak"] != 5 || saved["last_activity_date"] != "2026-02-08" {
		t.Errorf("Expected the stored streak to stay 5 ending 8 Feb, got %v / %v", saved["current_streak"], saved["last_activity_date"])
	}
}

// The Birmingham weekend: the doubles on Tuesday started a streak ("Day 1"), the solo
// on Wednesday made it 2. Reposting the doubles afterwards must say Day 1 again, both
// from a ledger and from a document the running-total code wrote.
func TestStreakTracker_RepostIsAsOfStart(t *testing.T) {
	doubles := makeActivity("2026-10-27")
	solo := makeActivity("2026-10-28")
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	run := func(t *testing.T, data map[string]interface{}, act *pbactivity.StandardizedActivity, id string) (*providers.EnrichmentResult, map[string]interface{}) {
		t.Helper()
		mockDB := &mocks.MockDatabase{
			GetBoosterDataFunc: func(ctx context.Context, userId, boosterId string) (map[string]interface{}, error) {
				return data, nil
			},
			SetBoosterDataFunc: func(ctx context.Context, userId, boosterId string, d map[string]interface{}) error {
				for k, v := range d {
					data[k] = v
				}
				return nil
			},
		}
		p := &StreakTracker{}
		p.SetService(&bootstrap.Service{DB: mockDB})
		res, err := p.Enrich(context.Background(), slog.Default(), act, u, map[string]string{"external_id": id}, false)
		if err != nil {
			t.Fatalf("Enrich: %v", err)
		}
		return res, data
	}

	t.Run("ledger", func(t *testing.T) {
		data := map[string]interface{}{"current_streak": float64(3), "longest_streak": float64(120), "last_activity_date": "2026-10-24"}
		if res, _ := run(t, data, doubles, "doubles"); !strings.Contains(res.Description, "Day 1") {
			t.Fatalf("doubles first run: %q", res.Description)
		}
		if res, _ := run(t, data, solo, "solo"); res.Metadata["streak_current"] != "2" {
			t.Fatalf("solo: %v", res.Metadata)
		}
		res, saved := run(t, data, doubles, "doubles")
		if res.Metadata["streak_current"] != "1" || !strings.Contains(res.Description, "Day 1") {
			t.Errorf("doubles repost: %v %q", res.Metadata, res.Description)
		}
		if saved["current_streak"] != 2 || saved["longest_streak"] != 120 {
			t.Errorf("stored streak should stay 2 (best 120): %v / %v", saved["current_streak"], saved["longest_streak"])
		}
	})
	t.Run("legacy document", func(t *testing.T) {
		data := map[string]interface{}{"current_streak": float64(2), "longest_streak": float64(120), "last_activity_date": "2026-10-28"}
		res, _ := run(t, data, doubles, "doubles")
		if res.Metadata["streak_current"] != "1" || !strings.Contains(res.Description, "Day 1") {
			t.Errorf("doubles repost on a legacy document: %v %q", res.Metadata, res.Description)
		}
	})
}

func TestStreakTracker_LongestStreakUpdates(t *testing.T) {
	var savedData map[string]interface{}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"current_streak":     float64(9),
				"longest_streak":     float64(9),
				"last_activity_date": "2026-02-07",
			}, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string, data map[string]interface{}) error {
			savedData = data
			return nil
		},
	}

	provider := &StreakTracker{}
	provider.SetService(&bootstrap.Service{DB: mockDB})

	res, err := provider.Enrich(context.Background(), slog.Default(), makeActivity("2026-02-08"), &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}, map[string]string{}, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Metadata["streak_current"] != "10" {
		t.Errorf("Expected streak_current=10, got %s", res.Metadata["streak_current"])
	}
	if res.Metadata["streak_longest"] != "10" {
		t.Errorf("Expected streak_longest=10, got %s", res.Metadata["streak_longest"])
	}
	if savedData["longest_streak"] != 10 {
		t.Errorf("Expected persisted longest_streak=10, got %v", savedData["longest_streak"])
	}
}

func TestStreakTracker_LongestStreakPreserved(t *testing.T) {
	var savedData map[string]interface{}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"current_streak":     float64(3),
				"longest_streak":     float64(15),  // Previous best was higher
				"last_activity_date": "2026-02-05", // Streak broken
			}, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string, data map[string]interface{}) error {
			savedData = data
			return nil
		},
	}

	provider := &StreakTracker{}
	provider.SetService(&bootstrap.Service{DB: mockDB})

	res, err := provider.Enrich(context.Background(), slog.Default(), makeActivity("2026-02-08"), &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}, map[string]string{}, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Metadata["streak_current"] != "1" {
		t.Errorf("Expected streak_current=1 (reset), got %s", res.Metadata["streak_current"])
	}
	if res.Metadata["streak_longest"] != "15" {
		t.Errorf("Expected streak_longest=15 (preserved), got %s", res.Metadata["streak_longest"])
	}
	if savedData["longest_streak"] != 15 {
		t.Errorf("Expected persisted longest_streak=15, got %v", savedData["longest_streak"])
	}
}

func TestStreakTracker_FirestoreInt64Types(t *testing.T) {
	// Firestore returns int64 for integer values, not float64.
	// This test ensures ToFloat64 correctly handles real Firestore data types.
	var savedData map[string]interface{}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"current_streak":     int64(77),
				"longest_streak":     int64(77),
				"last_activity_date": "2026-02-08",
			}, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId string, boosterId string, data map[string]interface{}) error {
			savedData = data
			return nil
		},
	}

	provider := &StreakTracker{}
	provider.SetService(&bootstrap.Service{DB: mockDB})

	res, err := provider.Enrich(context.Background(), slog.Default(), makeActivity("2026-02-09"), &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}, map[string]string{}, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res.Metadata["streak_current"] != "78" {
		t.Errorf("Expected streak_current=78, got %s", res.Metadata["streak_current"])
	}
	if res.Metadata["streak_longest"] != "78" {
		t.Errorf("Expected streak_longest=78, got %s", res.Metadata["streak_longest"])
	}
	if savedData["current_streak"] != 78 {
		t.Errorf("Expected persisted current_streak=78, got %v", savedData["current_streak"])
	}
}
