package activity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// recordingExporter is a SiteExporter that records what it was told.
type recordingExporter struct {
	mu         sync.Mutex
	activities []string
	roundups   []string // "slug/periodKey"
}

func (r *recordingExporter) ActivityChanged(_ context.Context, showcaseID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activities = append(r.activities, showcaseID)
}

func (r *recordingExporter) RoundupChanged(_ context.Context, slug, periodKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roundups = append(r.roundups, slug+"/"+periodKey)
}

func TestAddShowcaseEntry_NotifiesSiteExporter(t *testing.T) {
	ctx := context.Background()
	showcaseStore := func(setErr error) *MockActivityStore {
		return &MockActivityStore{
			GetShowcaseFunc: func(_ context.Context, _, id string) (*pbactivity.ShowcasedActivity, error) {
				return &pbactivity.ShowcasedActivity{ShowcaseId: id, Title: "Run"}, nil
			},
			SetShowcaseProfileEntryFunc: func(context.Context, string, *pbactivity.ShowcaseProfileEntry) error {
				return setErr
			},
		}
	}

	t.Run("AfterEntryPersisted", func(t *testing.T) {
		svc := newTestSvc(showcaseStore(nil), &MockBlobStore{})
		rec := &recordingExporter{}
		svc.SetSiteExporter(rec)
		if _, err := svc.AddShowcaseEntry(ctx, &pbsvc.AddShowcaseEntryRequest{UserId: "u1", ShowcaseId: "colwick-parkrun-2026-10-17"}); err != nil {
			t.Fatalf("AddShowcaseEntry: %v", err)
		}
		if len(rec.activities) != 1 || rec.activities[0] != "colwick-parkrun-2026-10-17" {
			t.Fatalf("exporter should be told about the entry once, got %v", rec.activities)
		}
		if len(rec.roundups) != 0 {
			t.Fatalf("no roundup change expected, got %v", rec.roundups)
		}
	})
	t.Run("NotWhenPersistFails", func(t *testing.T) {
		svc := newTestSvc(showcaseStore(errors.New("firestore down")), &MockBlobStore{})
		rec := &recordingExporter{}
		svc.SetSiteExporter(rec)
		if _, err := svc.AddShowcaseEntry(ctx, &pbsvc.AddShowcaseEntryRequest{UserId: "u1", ShowcaseId: "s1"}); err == nil {
			t.Fatal("expected error")
		}
		if len(rec.activities) != 0 {
			t.Fatalf("exporter must not run for a failed write, got %v", rec.activities)
		}
	})
	t.Run("NotWhenShowcaseMissing", func(t *testing.T) {
		svc := newTestSvc(&MockActivityStore{}, &MockBlobStore{})
		rec := &recordingExporter{}
		svc.SetSiteExporter(rec)
		if _, err := svc.AddShowcaseEntry(ctx, &pbsvc.AddShowcaseEntryRequest{UserId: "u1", ShowcaseId: "s1"}); err == nil {
			t.Fatal("expected NotFound")
		}
		if len(rec.activities) != 0 {
			t.Fatalf("got %v", rec.activities)
		}
	})
	t.Run("NilExporterIsFine", func(t *testing.T) {
		svc := newTestSvc(showcaseStore(nil), &MockBlobStore{})
		if _, err := svc.AddShowcaseEntry(ctx, &pbsvc.AddShowcaseEntryRequest{UserId: "u1", ShowcaseId: "s1"}); err != nil {
			t.Fatalf("AddShowcaseEntry without an exporter: %v", err)
		}
	})
}

func TestRemoveShowcaseEntry_NotifiesSiteExporter(t *testing.T) {
	store := &MockActivityStore{
		ListShowcaseProfileEntriesFunc: func(context.Context, string) ([]*pbactivity.ShowcaseProfileEntry, error) {
			return []*pbactivity.ShowcaseProfileEntry{{ShowcaseId: "s1"}}, nil
		},
	}
	svc := newTestSvc(store, &MockBlobStore{})
	rec := &recordingExporter{}
	svc.SetSiteExporter(rec)
	if _, err := svc.RemoveShowcaseEntry(context.Background(), &pbsvc.RemoveShowcaseEntryRequest{UserId: "u1", ShowcaseId: "s1"}); err != nil {
		t.Fatalf("RemoveShowcaseEntry: %v", err)
	}
	if len(rec.activities) != 1 || rec.activities[0] != "s1" {
		t.Fatalf("a removal must refresh the site too, got %v", rec.activities)
	}

	// Not in the profile: nothing changed, nothing to export.
	rec2 := &recordingExporter{}
	svc.SetSiteExporter(rec2)
	if _, err := svc.RemoveShowcaseEntry(context.Background(), &pbsvc.RemoveShowcaseEntryRequest{UserId: "u1", ShowcaseId: "absent"}); err != nil {
		t.Fatalf("RemoveShowcaseEntry: %v", err)
	}
	if len(rec2.activities) != 0 {
		t.Fatalf("no-op removal must not export, got %v", rec2.activities)
	}
}

func TestGenerateRoundup_NotifiesSiteExporter(t *testing.T) {
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC) // ISO week 42
	end := start.AddDate(0, 0, 7)
	roundupStore := func(setErr error) *MockActivityStore {
		return &MockActivityStore{
			GetShowcasePreferencesFunc: func(context.Context, string) (*pbactivity.ShowcaseProfile, error) {
				return &pbactivity.ShowcaseProfile{Slug: "james", RoundupSettings: &pbactivity.RoundupSettings{EnabledWeekly: true}}, nil
			},
			ListShowcaseEntriesInRangeFunc: func(context.Context, string, time.Time, time.Time) ([]*pbactivity.ShowcaseProfileEntry, error) {
				return []*pbactivity.ShowcaseProfileEntry{{ShowcaseId: "s1", DurationSeconds: 1800, StartTime: timestamppb.New(start.Add(time.Hour))}}, nil
			},
			SetRoundupFunc: func(context.Context, *pbactivity.ShowcaseRoundup) error { return setErr },
		}
	}

	t.Run("AfterSetRoundup", func(t *testing.T) {
		svc := newTestSvc(roundupStore(nil), &MockBlobStore{})
		rec := &recordingExporter{}
		svc.SetSiteExporter(rec)
		r, err := svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, start, end)
		if err != nil || r == nil {
			t.Fatalf("generateRoundup: %v, %v", r, err)
		}
		if len(rec.roundups) != 1 || rec.roundups[0] != "james/week-42-2026" {
			t.Fatalf("exporter should be told slug + period key once, got %v", rec.roundups)
		}
		if len(rec.activities) != 0 {
			t.Fatalf("no activity change expected, got %v", rec.activities)
		}
	})
	t.Run("NotWhenSaveFails", func(t *testing.T) {
		svc := newTestSvc(roundupStore(errors.New("firestore down")), &MockBlobStore{})
		rec := &recordingExporter{}
		svc.SetSiteExporter(rec)
		if _, err := svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, start, end); err == nil {
			t.Fatal("expected error")
		}
		if len(rec.roundups) != 0 {
			t.Fatalf("exporter must not run for a failed save, got %v", rec.roundups)
		}
	})
	t.Run("NotWhenNothingGenerated", func(t *testing.T) {
		store := roundupStore(nil)
		store.ListShowcaseEntriesInRangeFunc = func(context.Context, string, time.Time, time.Time) ([]*pbactivity.ShowcaseProfileEntry, error) {
			return nil, nil
		}
		svc := newTestSvc(store, &MockBlobStore{})
		rec := &recordingExporter{}
		svc.SetSiteExporter(rec)
		if r, err := svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, start, end); err != nil || r != nil {
			t.Fatalf("expected nil,nil got %v,%v", r, err)
		}
		if len(rec.roundups) != 0 {
			t.Fatalf("got %v", rec.roundups)
		}
	})
}
