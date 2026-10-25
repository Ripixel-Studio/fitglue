package siteexport

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/infra"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
)

// fakeStore implements only the ActivityStore methods the export path uses;
// anything else panics via the nil embedded interface.
type fakeStore struct {
	activity.ActivityStore
	profile      *pbactivity.ShowcaseProfile
	entries      []*pbactivity.ShowcaseProfileEntry
	showcases    map[string]*pbactivity.ShowcasedActivity
	roundups     []*pbactivity.ShowcaseRoundup
	prs          []*pbactivity.ShowcaseTopPR
	prefsCalls   int
	profileWrite bool
}

func (s *fakeStore) GetShowcaseProfileBySlug(_ context.Context, slug string) (*pbactivity.ShowcaseProfile, error) {
	if slug != s.profile.Slug {
		return nil, nil
	}
	return s.profile, nil
}
func (s *fakeStore) ListShowcaseProfileEntries(_ context.Context, _ string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	return s.entries, nil
}
func (s *fakeStore) ListUserPersonalRecords(_ context.Context, _ string) ([]*pbactivity.ShowcaseTopPR, error) {
	return s.prs, nil
}
func (s *fakeStore) GetPublicShowcase(_ context.Context, id string) (*pbactivity.ShowcasedActivity, string, error) {
	sc, ok := s.showcases[id]
	if !ok {
		return nil, "", nil
	}
	return sc, s.profile.UserId, nil
}
func (s *fakeStore) GetShowcasePreferences(_ context.Context, _ string) (*pbactivity.ShowcaseProfile, error) {
	s.prefsCalls++
	return s.profile, nil
}
func (s *fakeStore) UpdateShowcasePreferences(_ context.Context, _ string, p *pbactivity.ShowcaseProfile) (*pbactivity.ShowcaseProfile, error) {
	s.profileWrite = true
	return p, nil
}
func (s *fakeStore) ListAllRoundups(_ context.Context, _ string) ([]*pbactivity.ShowcaseRoundup, error) {
	return s.roundups, nil
}
func (s *fakeStore) GetRoundup(_ context.Context, _, key string) (*pbactivity.ShowcaseRoundup, error) {
	for _, r := range s.roundups {
		if r.PeriodKey == key {
			return r, nil
		}
	}
	return nil, nil
}

type fakeBlob struct {
	activity.BlobStore
	blobs map[string][]byte
}

func (b *fakeBlob) Get(_ context.Context, _, path string) ([]byte, error) {
	return b.blobs[path], nil
}

func TestServiceSource(t *testing.T) {
	// The showcase document on disk has its data offloaded to a blob, as in
	// production; the service hydrates it on read.
	full := runShowcase()
	blob, err := protojson.Marshal(&pbevents.EnrichedActivityEvent{ActivityData: full.ActivityData, Enrichments: full.Enrichments})
	if err != nil {
		t.Fatal(err)
	}
	stored := runShowcase()
	stored.ActivityData = nil
	stored.Enrichments = nil
	stored.OwnerDisplayName = ""

	profile := testProfile()
	profile.StreakHistory = nil // would trigger the lazy backfill on page 1
	store := &fakeStore{
		profile:   profile,
		entries:   []*pbactivity.ShowcaseProfileEntry{runEntry()},
		showcases: map[string]*pbactivity.ShowcasedActivity{stored.ShowcaseId: stored},
		roundups:  []*pbactivity.ShowcaseRoundup{weekRoundup()},
		prs: []*pbactivity.ShowcaseTopPR{
			{RecordType: "5k", Value: 1192, Unit: "seconds", AchievedAt: ts(runStart)},
			{RecordType: "Bench Press (Barbell)_1rm", Value: 100, Unit: "kg", AchievedAt: ts(liftStart)},
			{RecordType: "Bench Press (Barbell)_volume", Value: 5000, Unit: "kg", AchievedAt: ts(liftStart)},
		},
	}
	svc := activity.NewService(store, &fakeBlob{blobs: map[string][]byte{stored.ActivityDataUri: blob}}, nil, "artifacts", "showcase-assets", infra.NewLogger())
	src := ServiceSource{Service: svc, Store: store}
	ctx := context.Background()

	p, err := src.Profile(ctx, "james")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.TopPrs) != 2 {
		t.Errorf("topPrs must be filled the way the public RPC fills them (time PRs + 1rm, no volume): %v", p.TopPrs)
	}
	if p.UserId != "uid-james" {
		t.Errorf("userId needed for entries: %q", p.UserId)
	}
	time.Sleep(20 * time.Millisecond)
	if store.profileWrite {
		t.Error("reading the profile must not write to the store")
	}
	if _, err := src.Profile(ctx, "nobody"); err == nil {
		t.Error("unknown slug must fail")
	}
	profile.Visible = false
	if _, err := src.Profile(ctx, "james"); err == nil {
		t.Error("invisible profile must fail")
	}
	profile.Visible = true

	entries, err := src.Entries(ctx, "uid-james")
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v %v", entries, err)
	}

	sc, err := src.Showcase(ctx, "colwick-parkrun-2026-10-17")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ActivityData == nil || sc.Enrichments == nil {
		t.Error("showcase must be hydrated from the blob")
	}
	if sc.OwnerDisplayName != "James" || sc.OwnerProfileSlug != "james" {
		t.Errorf("owner metadata must be hydrated from the profile: %q %q", sc.OwnerDisplayName, sc.OwnerProfileSlug)
	}
	if sc, err := src.Showcase(ctx, "missing"); err != nil || sc != nil {
		t.Errorf("missing showcase = %v %v", sc, err)
	}

	rs, err := src.Roundups(ctx, "james")
	if err != nil || len(rs) != 1 {
		t.Fatalf("roundups = %v %v", rs, err)
	}
	if r, err := src.Roundup(ctx, "james", "week-42-2026"); err != nil || r == nil {
		t.Errorf("roundup = %v %v", r, err)
	}
	if r, err := src.Roundup(ctx, "james", "week-01-2020"); err != nil || r != nil {
		t.Errorf("missing roundup = %v %v", r, err)
	}

	// End to end through the real service: the export renders what the page would.
	w := newMemWriter()
	res, err := ExportAll(ctx, Deps{Source: src, Writer: w, Now: func() time.Time { return fixedNow }}, "james", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Activities != 1 || res.Roundups != 1 {
		t.Errorf("result = %+v", res)
	}
	file := decode(t, w.files["activity/colwick-parkrun-2026-10-17.json"])
	if file["activity"].(map[string]any)["enrichments"] == nil {
		t.Error("hydrated enrichments missing from the export")
	}
}
