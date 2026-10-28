package distance_milestones

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func runActivity(distanceM float64) *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{
		Type:     pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		Sessions: []*pbactivity.Session{{TotalDistance: distanceM}},
	}
}

func TestDistanceMilestones_IsIdempotent(t *testing.T) {
	if NewDistanceMilestones().IsIdempotent() {
		t.Error("distance_milestones should not be idempotent")
	}
}

func TestDistanceMilestones_Enrich_NoDistance(t *testing.T) {
	p := NewDistanceMilestones()
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	res, err := p.Enrich(context.Background(), discardLogger(), &pbactivity.StandardizedActivity{}, u, nil, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	if res.Metadata["milestone_status"] != "no_distance" {
		t.Errorf("expected no_distance, got %v", res.Metadata)
	}
}

func TestDistanceMilestones_Enrich_SportFiltered(t *testing.T) {
	p := NewDistanceMilestones()
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	// Run activity, but filter is cycling → filtered.
	res, err := p.Enrich(context.Background(), discardLogger(), runActivity(5000), u, map[string]string{"sport": "cycling"}, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	if res.Metadata["milestone_status"] != "filtered" {
		t.Errorf("expected filtered, got %v", res.Metadata)
	}
}

func TestDistanceMilestones_Enrich_Progress_NoService(t *testing.T) {
	p := NewDistanceMilestones()
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	// 50km, no prior lifetime (no DB) → no milestone crossed, progress shown.
	res, err := p.Enrich(context.Background(), discardLogger(), runActivity(50000), u, nil, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	if res.Metadata["lifetime_distance"] != "50.0" {
		t.Errorf("expected lifetime 50.0, got %q", res.Metadata["lifetime_distance"])
	}
	if !strings.Contains(res.Description, "Next milestone") {
		t.Errorf("expected progress description, got %q", res.Description)
	}
	if res.Enrichments == nil || res.Enrichments.DistanceMilestone == nil {
		t.Error("expected DistanceMilestone enrichment")
	}
	if res.Enrichments.DistanceMilestone.NextMilestoneKm == nil {
		t.Error("expected NextMilestoneKm set on progress result")
	}
}

func TestDistanceMilestones_Enrich_MilestoneCrossed(t *testing.T) {
	// Lifetime at 95km, +10km run → crosses 100km milestone.
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId, boosterId string) (map[string]interface{}, error) {
			return map[string]interface{}{"lifetime_distance": float64(95)}, nil
		},
	}
	p := NewDistanceMilestones()
	p.SetService(&bootstrap.Service{DB: mockDB})

	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	res, err := p.Enrich(context.Background(), discardLogger(), runActivity(10000), u, map[string]string{"sport": "running"}, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	if res.Metadata["milestone_reached"] != "100" {
		t.Errorf("expected milestone_reached=100, got %v", res.Metadata)
	}
	if !strings.Contains(res.Description, "MILESTONE") {
		t.Errorf("expected MILESTONE in description, got %q", res.Description)
	}
	if res.Enrichments == nil || res.Enrichments.DistanceMilestone == nil ||
		res.Enrichments.DistanceMilestone.MilestoneKm != 100 {
		t.Errorf("expected milestone summary at 100km, got %+v", res.Enrichments.GetDistanceMilestone())
	}
}

// A repost reads the lifetime total as of its start and does not add itself again;
// with a ledger the total is exact, with a legacy running total it is that total
// minus the activity.
func TestDistanceMilestones_Enrich_RepostIsAsOfStart(t *testing.T) {
	written := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	store := map[string]interface{}{"lifetime_distance": float64(120), "last_external_id": "ext-9", "last_updated": written}
	mockDB := &mocks.MockDatabase{
		GetBoosterDataFunc: func(ctx context.Context, userId, boosterId string) (map[string]interface{}, error) {
			return store, nil
		},
		SetBoosterDataFunc: func(ctx context.Context, userId, boosterId string, data map[string]interface{}) error {
			for k, v := range data {
				store[k] = v
			}
			return nil
		},
	}
	p := NewDistanceMilestones()
	p.SetService(&bootstrap.Service{DB: mockDB})
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u1"}}
	run := func(id string, day int, m float64, repost bool) map[string]string {
		t.Helper()
		act := runActivity(m)
		act.StartTime = timestamppb.New(time.Date(2026, 10, day, 8, 0, 0, 0, time.UTC))
		in := map[string]string{"external_id": id}
		if repost {
			in["is_repost"] = "true"
		}
		res, err := p.Enrich(context.Background(), discardLogger(), act, u, in, false)
		if err != nil {
			t.Fatalf("Enrich error: %v", err)
		}
		return res.Metadata
	}

	if got := run("ext-9", 28, 5000, true)["lifetime_distance"]; got != "120.0" {
		t.Errorf("repost of the activity inside the legacy total: %s, want 120.0", got)
	}
	if got := run("ext-10", 29, 10000, false)["lifetime_distance"]; got != "130.0" {
		t.Errorf("next activity: %s, want 130.0", got)
	}
	if got := run("ext-10", 29, 10000, true)["lifetime_distance"]; got != "130.0" {
		t.Errorf("repost of a ledger activity: %s, want 130.0", got)
	}
	if got := providers.ToFloat64(store["lifetime_distance"]); got != 130 {
		t.Errorf("stored lifetime %v, want 130", got)
	}
}
