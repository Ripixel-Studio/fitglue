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

// A backfill larger than the per-process cap fills in over several runs: the second
// run (a new process sharing the cache file) skips what the first cached and spends
// its budget on the rest, in the order given (newest first). Unknown answers are not
// cached, so they are retried.
func TestStravaVisibilityBackfillProgress(t *testing.T) {
	hits := map[string]int{}
	fail := map[string]bool{"6": true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/activities/"):]
		hits[id]++
		if fail[id] {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"private":false,"visibility":"everyone"}`))
	}))
	defer srv.Close()
	path := t.TempDir() + "/vis.json"
	run := func(max int) *StravaVisibility {
		return &StravaVisibility{Token: func(context.Context, string) (string, error) { return "t", nil },
			BaseURL: srv.URL, CachePath: path, MaxLookups: max}
	}
	own := func(context.Context) (string, error) { return "u", nil }
	ids := []string{"10", "9", "8", "7", "6", "5"} // newest first
	var linked []string
	pass := func(v *StravaVisibility) {
		linked = nil
		for _, id := range ids {
			if ok, _ := v.Public(context.Background(), id, own); ok {
				linked = append(linked, id)
			}
		}
	}
	pass(run(3))
	if strings.Join(linked, ",") != "10,9,8" {
		t.Fatalf("first run should link the newest three: %v", linked)
	}
	pass(run(3))
	if strings.Join(linked, ",") != "10,9,8,7,5" {
		t.Fatalf("second run should keep the cached three and add the next: %v", linked)
	}
	for _, id := range []string{"10", "9", "8"} {
		if hits[id] != 1 {
			t.Errorf("cached id %s fetched %d times", id, hits[id])
		}
	}
	pass(run(3))
	if strings.Join(linked, ",") != "10,9,8,7,5" || hits["7"] != 1 || hits["5"] != 1 {
		t.Fatalf("third run: %v, hits %v", linked, hits)
	}
	if hits["6"] != 2 {
		t.Errorf("an unknown (500) answer must be retried, not cached: %d fetches", hits["6"])
	}
}

func TestStravaVisibilityTTLs(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	body := `{"private":false,"visibility":"followers_only"}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	v := &StravaVisibility{Token: func(context.Context, string) (string, error) { return "t", nil }, BaseURL: srv.URL, Now: func() time.Time { return now }}
	own := func(context.Context) (string, error) { return "u", nil }
	v.Public(context.Background(), "1", own) // followers-only: cached 7 days
	now = now.Add(6 * 24 * time.Hour)
	v.Public(context.Background(), "1", own)
	if calls != 1 {
		t.Fatalf("non-public answer should be trusted for 7 days, calls=%d", calls)
	}
	now = now.Add(2 * 24 * time.Hour)
	v.Public(context.Background(), "1", own)
	if calls != 2 {
		t.Fatalf("non-public answer should expire after 7 days, calls=%d", calls)
	}
	body = `{"private":false,"visibility":"everyone"}`
	now = now.Add(8 * 24 * time.Hour)
	v.Public(context.Background(), "1", own) // now public: 30 days
	now = now.Add(29 * 24 * time.Hour)
	v.Public(context.Background(), "1", own)
	if calls != 3 {
		t.Fatalf("public answer should be trusted for 30 days, calls=%d", calls)
	}
	now = now.Add(2 * 24 * time.Hour)
	v.Public(context.Background(), "1", own)
	if calls != 4 {
		t.Fatalf("public answer should expire after 30 days, calls=%d", calls)
	}
}

// The service is long-lived: the lookup budget refills every 15 minutes and a 429
// pause lifts after its Retry-After (15 minutes without one).
func TestStravaVisibilityWindowsReset(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	code, retry := 200, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.WriteHeader(code)
		if code == 200 {
			_, _ = w.Write([]byte(`{"private":false,"visibility":"everyone"}`))
		}
	}))
	defer srv.Close()
	v := &StravaVisibility{Token: func(context.Context, string) (string, error) { return "t", nil },
		BaseURL: srv.URL, MaxLookups: 2, Now: func() time.Time { return now }}
	own := func(context.Context) (string, error) { return "u", nil }
	pub := func(id string) bool { ok, _ := v.Public(context.Background(), id, own); return ok }

	if !pub("1") || !pub("2") || pub("3") {
		t.Fatal("window budget of 2: first two lookups succeed, third is refused")
	}
	now = now.Add(14 * time.Minute)
	if pub("3") {
		t.Fatal("budget must not refill inside the 15-minute window")
	}
	now = now.Add(2 * time.Minute)
	if !pub("3") {
		t.Fatal("budget must refill after 15 minutes")
	}
	// 429 with Retry-After: paused for exactly that long.
	now = now.Add(15 * time.Minute)
	code, retry = 429, "120"
	pub("4")
	code, retry = 200, ""
	now = now.Add(60 * time.Second)
	if pub("5") {
		t.Fatal("still inside Retry-After")
	}
	now = now.Add(61 * time.Second)
	if !pub("5") {
		t.Fatal("pause must lift once Retry-After has passed")
	}
	// 429 without Retry-After: 15 minutes.
	now = now.Add(16 * time.Minute)
	code = 429
	pub("6")
	code = 200
	now = now.Add(14 * time.Minute)
	if pub("7") {
		t.Fatal("still inside the default 15-minute pause")
	}
	now = now.Add(2 * time.Minute)
	if !pub("7") {
		t.Fatal("default pause must lift after 15 minutes")
	}
}
