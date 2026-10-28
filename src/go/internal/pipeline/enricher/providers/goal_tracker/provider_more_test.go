package goal_tracker

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGoalTracker_IsIdempotent(t *testing.T) {
	if NewGoalTracker().IsIdempotent() {
		t.Error("goal_tracker should not be idempotent")
	}
}

func TestGoalTracker_Enrich_CurrentPeriodProgress(t *testing.T) {
	p := NewGoalTracker()
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	// Activity dated now → current period; 30km toward 100km default, no service.
	activity := &pbactivity.StandardizedActivity{
		Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		StartTime: timestamppb.New(time.Now()),
		Sessions:  []*pbactivity.Session{{TotalDistance: 30000}},
	}
	res, err := p.Enrich(context.Background(), discardLogger(), activity, u, nil, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	if res.Metadata["goal_status"] != "30%" {
		t.Errorf("expected goal_status=30%%, got %q", res.Metadata["goal_status"])
	}
	if res.Metadata["goal_target"] != "100" || res.Metadata["goal_metric"] != "distance" {
		t.Errorf("unexpected metadata %v", res.Metadata)
	}
	if !strings.Contains(res.Description, "Goal Progress") {
		t.Errorf("expected goal progress description, got %q", res.Description)
	}
	if res.Enrichments == nil || res.Enrichments.GoalTracker == nil || len(res.Enrichments.GoalTracker.Goals) != 1 {
		t.Fatal("expected one goal entry in enrichments")
	}
	g := res.Enrichments.GoalTracker.Goals[0]
	if g.Target != 100 || g.Current != 30 || g.OnPace {
		t.Errorf("unexpected goal entry %+v", g)
	}
}

func TestGoalTracker_Enrich_GoalComplete(t *testing.T) {
	p := NewGoalTracker()
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	activity := &pbactivity.StandardizedActivity{
		Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		StartTime: timestamppb.New(time.Now()),
		Sessions:  []*pbactivity.Session{{TotalDistance: 60000}}, // 60km
	}
	res, err := p.Enrich(context.Background(), discardLogger(), activity, u, map[string]string{"target": "50"}, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	if res.Metadata["goal_status"] != "100%" {
		t.Errorf("expected 100%% (capped), got %q", res.Metadata["goal_status"])
	}
	if !strings.Contains(res.Description, "Goal complete") {
		t.Errorf("expected goal-complete message, got %q", res.Description)
	}
	if !res.Enrichments.GoalTracker.Goals[0].OnPace {
		t.Error("expected OnPace true when goal met")
	}
}

func TestGoalTracker_Enrich_PastPeriod(t *testing.T) {
	p := NewGoalTracker()
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	// Year period; backdate to a previous year so it is not the current period.
	past := time.Date(2020, 3, 15, 9, 0, 0, 0, time.UTC)
	activity := &pbactivity.StandardizedActivity{
		Type:      pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		StartTime: timestamppb.New(past),
		Sessions:  []*pbactivity.Session{{TotalDistance: 10000}},
	}
	res, err := p.Enrich(context.Background(), discardLogger(), activity, u, map[string]string{"period": "year"}, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	if !strings.Contains(res.Description, "Past activity") {
		t.Errorf("expected past-activity note, got %q", res.Description)
	}
	if res.Metadata["goal_period"] != "year" {
		t.Errorf("expected goal_period=year, got %q", res.Metadata["goal_period"])
	}
}

// fakeStore is booster_data for one booster, written the way SetBoosterData merges.
type fakeStore struct{ data map[string]interface{} }

func (f *fakeStore) db() *mocks.MockDatabase {
	return &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId, boosterId string) (map[string]interface{}, error) {
			return f.data, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId, boosterId string, data map[string]interface{}) error {
			if f.data == nil {
				f.data = map[string]interface{}{}
			}
			for k, v := range data {
				f.data[k] = v
			}
			return nil
		},
	}
}

func weekRun(t *testing.T, p *GoalTracker, id string, start time.Time, km float64, repost bool) *providers.EnrichmentResult {
	t.Helper()
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	act := &pbactivity.StandardizedActivity{
		Type:      pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT,
		StartTime: timestamppb.New(start),
		Sessions:  []*pbactivity.Session{{TotalDistance: km * 1000}},
	}
	res, err := p.Enrich(context.Background(), discardLogger(), act, u, map[string]string{
		"period": "week", "target": "20", "external_id": id, "is_repost": strconv.FormatBool(repost),
	}, false)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return res
}

// The Birmingham weekend: doubles on Tuesday (8.6 km of 20 → 43%), solo on Wednesday
// (17.3 km → 87%). Reposting the doubles afterwards must read 43% again, not count the
// solo that came after it (it read 100%, 26.1/20 km).
func TestGoalTracker_Enrich_RepostIsAsOfStart(t *testing.T) {
	store := &fakeStore{}
	p := NewGoalTracker()
	p.SetService(&bootstrap.Service{DB: store.db()})
	doubles := time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC)
	solo := time.Date(2026, 10, 28, 8, 20, 0, 0, time.UTC)

	if got := weekRun(t, p, "doubles", doubles, 8.6, false).Metadata["goal_status"]; got != "43%" {
		t.Fatalf("doubles first run: %s, want 43%%", got)
	}
	if got := weekRun(t, p, "solo", solo, 8.8, false).Metadata["goal_status"]; got != "87%" {
		t.Fatalf("solo: %s, want 87%%", got)
	}
	res := weekRun(t, p, "doubles", doubles, 8.6, true)
	if got := res.Metadata["goal_status"]; got != "43%" {
		t.Errorf("doubles repost: %s, want 43%% (as of its start)", got)
	}
	if !strings.Contains(res.Description, "8.6/20 km") {
		t.Errorf("doubles repost description: %q", res.Description)
	}
	// Processing an activity again replaces its entry; it is never counted twice.
	if got := weekRun(t, p, "solo", solo, 8.8, true).Metadata["goal_accumulated"]; got != "17.40" {
		t.Errorf("solo repost accumulated %s, want 17.40", got)
	}
	if got := providers.ToFloat64(store.data["accumulated"]); got < 17.39 || got > 17.41 {
		t.Errorf("stored accumulated %v, want 17.4 (the latest period's total)", got)
	}
}

// A document written by the running-total code: its total is carried over, dated when
// it was last written, so a later activity adds to it and a repost of an earlier one
// does not count it.
func TestGoalTracker_Enrich_LegacyTotalCarriedOver(t *testing.T) {
	written := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{data: map[string]interface{}{
		"period_key":       "2026-W44",
		"accumulated":      17.4,
		"last_external_id": "solo",
		"last_updated":     written,
	}}
	p := NewGoalTracker()
	p.SetService(&bootstrap.Service{DB: store.db()})

	if got := weekRun(t, p, "doubles", time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC), 8.6, true).Metadata["goal_status"]; got != "43%" {
		t.Errorf("repost of an activity before the legacy total: %s, want 43%%", got)
	}
	if got := weekRun(t, p, "thursday", time.Date(2026, 10, 29, 7, 0, 0, 0, time.UTC), 2, false).Metadata["goal_accumulated"]; got != "19.40" {
		t.Errorf("new activity after the legacy total: %s, want 19.40", got)
	}
	if got := weekRun(t, p, "next-week", time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC), 5, false).Metadata["goal_accumulated"]; got != "5.00" {
		t.Errorf("next week starts from zero: %s", got)
	}
}
