package siteexport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"

	"github.com/fitglue/server/src/go/internal/activity"
)

func linkShowcase(src pbactivity.ActivitySource, ext string) *pbactivity.ShowcasedActivity {
	return &pbactivity.ShowcasedActivity{
		ShowcaseId: "s1", Source: src, PipelineExecutionId: func() *string { s := "run-1"; return &s }(),
		ActivityData: &pbactivity.StandardizedActivity{ExternalId: ext},
	}
}

func TestLinksStravaSource(t *testing.T) {
	l := BuildLinks(linkShowcase(pbactivity.ActivitySource_SOURCE_STRAVA, "12345678"), "12345678")
	if l == nil || l.Strava != "https://www.strava.com/activities/12345678" || l.Hevy != "" {
		t.Fatalf("got %+v", l)
	}
}

func TestLinksHevyWithStrava(t *testing.T) {
	l := BuildLinks(linkShowcase(pbactivity.ActivitySource_SOURCE_HEVY, "d7ed6d0a-8ef9-439c-ba7d-fb180d2e2312"), "999")
	if l == nil || l.Hevy != "https://hevy.com/workout/d7ed6d0a-8ef9-439c-ba7d-fb180d2e2312" || l.Strava != "https://www.strava.com/activities/999" {
		t.Fatalf("got %+v", l)
	}
}

// A pending Strava upload yields no id, so only hevy is set (StravaID is "").
func TestLinksHevyPendingStrava(t *testing.T) {
	sc := linkShowcase(pbactivity.ActivitySource_SOURCE_HEVY, "abc-1")
	svc, _ := stravaIDService(pbpipeline.DestinationStatus_DESTINATION_STATUS_PENDING, "", `{"private":false,"visibility":"everyone"}`)
	id, err := svc.StravaID(context.Background(), sc)
	if err != nil || id != "" {
		t.Fatalf("pending outcome: %q %v", id, err)
	}
	l := BuildLinks(sc, id)
	if l == nil || l.Hevy != "https://hevy.com/workout/abc-1" || l.Strava != "" {
		t.Fatalf("got %+v", l)
	}
	ok, _ := stravaIDService(pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS, "777", `{"private":false,"visibility":"everyone"}`)
	if id, _ := ok.StravaID(context.Background(), sc); id != "777" {
		t.Fatalf("success outcome id = %q", id)
	}
}

func TestLinksBadIDOmitted(t *testing.T) {
	if l := BuildLinks(linkShowcase(pbactivity.ActivitySource_SOURCE_STRAVA, "12a/../x"), "12a/../x"); l != nil {
		t.Errorf("bad strava id: %+v", l)
	}
	l := BuildLinks(linkShowcase(pbactivity.ActivitySource_SOURCE_HEVY, "a/b?x"), "12 3")
	if l != nil {
		t.Errorf("bad hevy and strava ids: %+v", l)
	}
	l = BuildLinks(linkShowcase(pbactivity.ActivitySource_SOURCE_HEVY, "ok-1"), "x")
	if l == nil || l.Strava != "" || l.Hevy == "" {
		t.Errorf("bad strava id must only drop strava: %+v", l)
	}
	if BuildLinks(linkShowcase(pbactivity.ActivitySource_SOURCE_FITBIT, "1"), "5") != nil {
		t.Error("other sources must not link")
	}
}

func TestActivityFileOmitsEmptyLinks(t *testing.T) {
	b, err := BuildActivity(runShowcase(), runEntry(), fixedNow, farZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"links"`) {
		t.Error("links key must be absent when there are no links")
	}
	b, err = BuildActivity(runShowcase(), runEntry(), fixedNow, farZones, &Links{Strava: "https://www.strava.com/activities/1"})
	if err != nil || !strings.Contains(string(b), `"links":{"strava":"https://www.strava.com/activities/1"}`) {
		t.Errorf("links missing: %v %s", err, b)
	}
}

// An error from StravaID is logged and the export still writes the file, without strava.
func TestExportSurvivesStravaIDError(t *testing.T) {
	src := newFakeSource()
	src.errs["strava:push-day-2026-10-13"] = errors.New("boom")
	w := newMemWriter()
	if err := ExportActivity(context.Background(), Deps{Source: src, Writer: w, Now: func() time.Time { return fixedNow }}, "james", "push-day-2026-10-13"); err != nil {
		t.Fatal(err)
	}
}

// stravaIDService is a ServiceSource whose run has the given Strava outcome and whose
// Strava API (an httptest server) answers body for any activity.
func stravaIDService(st pbpipeline.DestinationStatus, ext, body string) (ServiceSource, *runStore) {
	store := &runStore{run: &pbpipeline.PipelineRun{Destinations: []*pbpipeline.DestinationOutcome{
		{Destination: pbplugin.DestinationType_DESTINATION_HEVY, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS, ExternalId: ptr("h")},
		{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: st, ExternalId: ptr(ext)},
	}}}
	return ServiceSource{Store: store, Visibility: fakeStrava(body, 200, nil)}, store
}

func fakeStrava(body string, code int, hdr map[string]string) *StravaVisibility {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	return &StravaVisibility{
		Token:   func(context.Context, string) (string, error) { return "tok", nil },
		BaseURL: srv.URL,
	}
}

type runStore struct {
	activity.ActivityStore
	run   *pbpipeline.PipelineRun
	reads int
}

func (s *runStore) GetPublicShowcase(context.Context, string) (*pbactivity.ShowcasedActivity, string, error) {
	s.reads++
	return &pbactivity.ShowcasedActivity{}, "uid", nil
}
func (s *runStore) GetPipelineRun(_ context.Context, uid, run string) (*pbpipeline.PipelineRun, error) {
	if uid != "uid" || run != "run-1" {
		return nil, nil
	}
	return s.run, nil
}
func ptr(s string) *string { return &s }

// Visibility gate: only "everyone" and not private produces a Strava link; private,
// followers-only, only-me and anything unknown fail closed.
func TestLinksStravaVisibility(t *testing.T) {
	cases := []struct {
		name string
		body string
		code int
		want bool
	}{
		{"everyone", `{"private":false,"visibility":"everyone"}`, 200, true},
		{"private", `{"private":true,"visibility":"only_me"}`, 200, false},
		{"private flag wins", `{"private":true,"visibility":"everyone"}`, 200, false},
		{"followers only", `{"private":false,"visibility":"followers_only"}`, 200, false},
		{"unknown: no flags", `{"name":"x"}`, 200, false},
		{"unknown: server error", `oops`, 500, false},
		{"unknown: bad json", `not json`, 200, false},
		{"not found", `{}`, 404, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := linkShowcase(pbactivity.ActivitySource_SOURCE_STRAVA, "4242")
			src := ServiceSource{Store: &runStore{}, Visibility: fakeStrava(tc.body, tc.code, nil)}
			id, _ := src.StravaID(context.Background(), sc)
			l := BuildLinks(sc, id)
			if got := l != nil && l.Strava != ""; got != tc.want {
				t.Fatalf("link present = %v, want %v (%+v)", got, tc.want, l)
			}
		})
	}
	// The same rule covers the Strava copy of a Hevy workout; the Hevy link stays.
	hevy := linkShowcase(pbactivity.ActivitySource_SOURCE_HEVY, "abc-1")
	priv, _ := stravaIDService(pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS, "777", `{"private":true,"visibility":"only_me"}`)
	id, _ := priv.StravaID(context.Background(), hevy)
	if l := BuildLinks(hevy, id); l == nil || l.Strava != "" || l.Hevy == "" {
		t.Errorf("private Strava copy: %+v", l)
	}
	// No checker configured: unknown, so no Strava link.
	none := ServiceSource{Store: &runStore{}}
	if id, _ := none.StravaID(context.Background(), linkShowcase(pbactivity.ActivitySource_SOURCE_STRAVA, "4242")); id != "" {
		t.Errorf("nil checker must fail closed, got %q", id)
	}
}

// A Strava-sourced activity needs no Firestore read when its answer is cached; one
// that is not cached needs only the owner lookup; the API is hit once per id.
func TestStravaVisibilityCacheAndReads(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"private":false,"visibility":"everyone"}`))
	}))
	defer srv.Close()
	store := &runStore{}
	src := ServiceSource{Store: store, Visibility: &StravaVisibility{
		Token: func(context.Context, string) (string, error) { return "t", nil }, BaseURL: srv.URL,
		CachePath: t.TempDir() + "/vis.json",
	}}
	sc := linkShowcase(pbactivity.ActivitySource_SOURCE_STRAVA, "4242")
	for i := 0; i < 3; i++ {
		if id, err := src.StravaID(context.Background(), sc); id != "4242" || err != nil {
			t.Fatalf("round %d: %q %v", i, id, err)
		}
	}
	if hits != 1 || store.reads != 1 {
		t.Errorf("API hits %d, owner reads %d; want 1 and 1", hits, store.reads)
	}
	// Other sources never touch the store.
	if id, _ := src.StravaID(context.Background(), linkShowcase(pbactivity.ActivitySource_SOURCE_FITBIT, "1")); id != "" || store.reads != 1 {
		t.Errorf("non-strava/hevy source read the store")
	}
	// The cache file survives a new process.
	again := &StravaVisibility{Token: func(context.Context, string) (string, error) { return "", errors.New("no") },
		BaseURL: srv.URL, CachePath: src.Visibility.CachePath}
	if ok, err := again.Public(context.Background(), "4242", nil); !ok || err != nil {
		t.Errorf("persisted cache: %v %v", ok, err)
	}
}

// Near Strava's rate limit, or on a 429, lookups stop and the answer is unknown.
func TestStravaVisibilityRateLimit(t *testing.T) {
	v := fakeStrava(`{"private":false,"visibility":"everyone"}`, 200, map[string]string{"X-RateLimit-Limit": "100,1000", "X-RateLimit-Usage": "85,300"})
	if ok, err := v.Public(context.Background(), "1", func(context.Context) (string, error) { return "u", nil }); !ok || err != nil {
		t.Fatalf("first lookup: %v %v", ok, err)
	}
	if ok, err := v.Public(context.Background(), "2", func(context.Context) (string, error) { return "u", nil }); ok || err == nil {
		t.Errorf("must stop near the limit: %v %v", ok, err)
	}
	b := fakeStrava(`{}`, 429, nil)
	if ok, err := b.Public(context.Background(), "3", func(context.Context) (string, error) { return "u", nil }); ok || err == nil {
		t.Errorf("429 must be unknown: %v %v", ok, err)
	}
	m := fakeStrava(`{"private":false,"visibility":"everyone"}`, 200, nil)
	m.MaxLookups = 1
	own := func(context.Context) (string, error) { return "u", nil }
	_, _ = m.Public(context.Background(), "1", own)
	if ok, err := m.Public(context.Background(), "2", own); ok || err == nil {
		t.Errorf("budget cap: %v %v", ok, err)
	}
}
