package activity

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

func TestSetShowcasePhotos(t *testing.T) {
	store := &MockActivityStore{}
	doc := &pbactivity.ShowcasedActivity{ShowcaseId: "run-1", Title: "Run"}
	store.GetPublicShowcaseFunc = func(context.Context, string) (*pbactivity.ShowcasedActivity, string, error) {
		return doc, "uid", nil
	}
	var savedDoc *pbactivity.ShowcasedActivity
	var savedEntry *pbactivity.ShowcaseProfileEntry
	store.UpdateShowcaseFunc = func(_ context.Context, uid string, sc *pbactivity.ShowcasedActivity) (*pbactivity.ShowcasedActivity, error) {
		if uid != "uid" {
			t.Errorf("owner %q", uid)
		}
		savedDoc = sc
		return sc, nil
	}
	store.SetShowcaseProfileEntryFunc = func(_ context.Context, _ string, e *pbactivity.ShowcaseProfileEntry) error {
		savedEntry = e
		return nil
	}
	svc := newTestSvc(store, &MockBlobStore{})
	x := &recordingExporter{}
	svc.SetSiteExporter(x)

	urls := []string{"https://storage.googleapis.com/b/a.jpg", "https://storage.googleapis.com/b/b.jpg"}
	entry := &pbactivity.ShowcaseProfileEntry{ShowcaseId: "run-1", Title: "Run", DistanceMeters: 5000}
	if err := svc.SetShowcasePhotos(context.Background(), "run-1", entry, urls); err != nil {
		t.Fatal(err)
	}
	if savedDoc == nil || !slices.Equal(savedDoc.PhotoUrls, urls) || savedDoc.Title != "Run" {
		t.Errorf("showcase = %+v", savedDoc)
	}
	if savedEntry == nil || !slices.Equal(savedEntry.PhotoUrls, urls) || savedEntry.DistanceMeters != 5000 {
		t.Errorf("entry = %+v", savedEntry)
	}
	if len(x.activities) != 1 || x.activities[0] != "run-1" {
		t.Errorf("site export not told: %v", x.activities)
	}

	// A showcase that already has photos is left alone.
	savedDoc = nil
	if err := svc.SetShowcasePhotos(context.Background(), "run-1", entry, []string{"x"}); !errors.Is(err, ErrHasPhotos) {
		t.Errorf("err = %v, want ErrHasPhotos", err)
	}
	if savedDoc != nil {
		t.Error("wrote a showcase that already had photos")
	}
}

func TestRefreshRoundupPhotos(t *testing.T) {
	store := &MockActivityStore{}
	stored := &pbactivity.ShowcaseRoundup{Slug: "james", PeriodKey: "year-2025", AiSummary: "his story", TotalActivities: 2}
	store.GetRoundupFunc = func(context.Context, string, string) (*pbactivity.ShowcaseRoundup, error) { return stored, nil }
	store.GetShowcasePreferencesFunc = func(context.Context, string) (*pbactivity.ShowcaseProfile, error) {
		return &pbactivity.ShowcaseProfile{ShowPhotoGallery: true}, nil
	}
	var from, to time.Time
	store.ListShowcaseEntriesInRangeFunc = func(_ context.Context, _ string, f, tt time.Time) ([]*pbactivity.ShowcaseProfileEntry, error) {
		from, to = f, tt
		return []*pbactivity.ShowcaseProfileEntry{
			{ShowcaseId: "a", Title: "A", StartTime: timestamppb.New(time.Date(2025, 5, 1, 7, 0, 0, 0, time.UTC)), PhotoUrls: []string{"u1", "u2"}},
			{ShowcaseId: "b", Title: "B"},
		}, nil
	}
	saves := 0
	store.SetRoundupFunc = func(context.Context, *pbactivity.ShowcaseRoundup) error { saves++; return nil }
	svc := newTestSvc(store, &MockBlobStore{})
	x := &recordingExporter{}
	svc.SetSiteExporter(x)

	changed, err := svc.RefreshRoundupPhotos(context.Background(), "uid", "james", "year-2025")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if from.Year() != 2025 || to.Year() != 2026 {
		t.Errorf("range %v..%v", from, to)
	}
	if len(stored.Photos) != 2 || stored.Photos[0].Url != "u1" || stored.Photos[0].ShowcaseId != "a" {
		t.Errorf("photos = %v", stored.Photos)
	}
	if stored.AiSummary != "his story" || stored.TotalActivities != 2 {
		t.Error("only the photos change")
	}
	if saves != 1 || len(x.roundups) != 1 || x.roundups[0] != "james/year-2025" {
		t.Errorf("saves=%d roundups=%v", saves, x.roundups)
	}

	// Unchanged: no write.
	changed, err = svc.RefreshRoundupPhotos(context.Background(), "uid", "james", "year-2025")
	if err != nil || changed || saves != 1 {
		t.Errorf("second refresh changed=%v saves=%d err=%v", changed, saves, err)
	}
}
