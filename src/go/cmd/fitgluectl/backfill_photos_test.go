package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/activity"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

type fakePhotoSite struct {
	entries   []*pbactivity.ShowcaseProfileEntry
	showcases map[string]*pbactivity.ShowcasedActivity
	strava    map[string]string // showcase id -> strava id
	saved     map[string][]string
	roundups  []string
}

func (f *fakePhotoSite) Profile(context.Context, string) (*pbactivity.ShowcaseProfile, error) {
	return &pbactivity.ShowcaseProfile{UserId: "uid"}, nil
}
func (f *fakePhotoSite) Entries(context.Context, string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	return f.entries, nil
}
func (f *fakePhotoSite) Showcase(_ context.Context, id string) (*pbactivity.ShowcasedActivity, error) {
	return f.showcases[id], nil
}
func (f *fakePhotoSite) StravaActivityID(_ context.Context, sc *pbactivity.ShowcasedActivity) (string, error) {
	return f.strava[sc.ShowcaseId], nil
}
func (f *fakePhotoSite) SetShowcasePhotos(_ context.Context, id string, e *pbactivity.ShowcaseProfileEntry, urls []string) error {
	if len(f.showcases[id].PhotoUrls) > 0 {
		return activity.ErrHasPhotos
	}
	f.saved[id] = urls
	f.showcases[id].PhotoUrls = urls
	e.PhotoUrls = urls
	return nil
}
func (f *fakePhotoSite) RefreshRoundupPhotos(_ context.Context, _, _, key string) (bool, error) {
	f.roundups = append(f.roundups, key)
	return true, nil
}

type fakeLister struct {
	photos map[string][]stravaPhoto
	calls  int
}

func (l *fakeLister) Photos(_ context.Context, id string) ([]stravaPhoto, error) {
	l.calls++
	return l.photos[id], nil
}

func photoFixture() (*fakePhotoSite, *fakeLister) {
	site := &fakePhotoSite{showcases: map[string]*pbactivity.ShowcasedActivity{}, strava: map[string]string{}, saved: map[string][]string{}}
	add := func(id string, at time.Time, sid string, has []string) {
		site.entries = append(site.entries, &pbactivity.ShowcaseProfileEntry{ShowcaseId: id, Title: id, StartTime: timestamppb.New(at), PhotoUrls: has})
		site.showcases[id] = &pbactivity.ShowcasedActivity{ShowcaseId: id, PhotoUrls: has}
		if sid != "" {
			site.strava[id] = sid
		}
	}
	add("parkrun", time.Date(2025, 5, 3, 9, 0, 0, 0, time.UTC), "111", nil)
	add("gym", time.Date(2025, 5, 4, 9, 0, 0, 0, time.UTC), "222", nil)
	add("hevy-only", time.Date(2025, 5, 5, 9, 0, 0, 0, time.UTC), "", nil)
	add("has-photos", time.Date(2025, 5, 6, 9, 0, 0, 0, time.UTC), "333", []string{"mine.jpg"})
	add("next-year", time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC), "444", nil)
	l := &fakeLister{photos: map[string][]stravaPhoto{
		"111": {{UniqueID: "b", URL: "https://cdn/b"}, {UniqueID: "a", URL: "https://cdn/a"}},
		"333": {{UniqueID: "c", URL: "https://cdn/c"}},
		"444": {{UniqueID: "d", URL: "https://cdn/d"}},
	}}
	return site, l
}

func photoFlags(t *testing.T, args ...string) *backfillPhotosFlags {
	t.Helper()
	f, err := parseBackfillPhotosFlags(append(args, "--state", filepath.Join(t.TempDir(), "state.json")), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func fakeCopy(copied *[]string) photoCopier {
	return func(_ context.Context, uid, sid string, p stravaPhoto) (string, error) {
		*copied = append(*copied, p.UniqueID)
		return "https://storage.googleapis.com/bkt/" + photoObject(uid, sid, p.UniqueID), nil
	}
}

func TestBackfillPhotosDryRunThenApply(t *testing.T) {
	site, l := photoFixture()
	f := photoFlags(t, "--since", "2025-01-01", "--until", "2025-12-31")
	var copied []string
	var out bytes.Buffer
	if err := runBackfillPhotos(context.Background(), f, site, l, fakeCopy(&copied), &out, time.Now); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"parkrun  strava 111  2 photos", "gym  strava 222  no photos", "hevy-only  no Strava copy",
		"3 activities without photos checked (1 already had photos): 1 have Strava photos (2 photos), 1 no Strava copy, 0 failed", "dry run"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "next-year") || len(copied) != 0 || len(site.saved) != 0 || len(site.roundups) != 0 {
		t.Errorf("dry run wrote or strayed: copied=%v saved=%v roundups=%v", copied, site.saved, site.roundups)
	}
	if l.calls != 2 {
		t.Errorf("strava calls = %d, want 2", l.calls)
	}

	// --apply reuses the dry run's answers from the state file: no second listing.
	f.apply = true
	out.Reset()
	if err := runBackfillPhotos(context.Background(), f, site, l, fakeCopy(&copied), &out, time.Now); err != nil {
		t.Fatal(err)
	}
	if l.calls != 2 {
		t.Errorf("apply listed again: %d calls", l.calls)
	}
	if !slices.Equal(copied, []string{"b", "a"}) {
		t.Errorf("copied %v, want Strava's order b, a", copied)
	}
	want := []string{
		"https://storage.googleapis.com/bkt/activity_photos/uid/strava-111/b.jpg",
		"https://storage.googleapis.com/bkt/activity_photos/uid/strava-111/a.jpg",
	}
	if !slices.Equal(site.saved["parkrun"], want) || len(site.saved) != 1 {
		t.Errorf("saved %v", site.saved)
	}
	if !slices.Equal(site.showcases["has-photos"].PhotoUrls, []string{"mine.jpg"}) {
		t.Error("existing photos touched")
	}
	if !slices.Equal(site.roundups, []string{"month-05-2025", "week-18-2025", "year-2025"}) {
		t.Errorf("roundups %v", site.roundups)
	}
	if !strings.Contains(out.String(), "applied: 1 activities, 2 photos copied") {
		t.Errorf("summary:\n%s", out.String())
	}

	// A second --apply finds nothing to do.
	copied = nil
	site.roundups = nil
	out.Reset()
	if err := runBackfillPhotos(context.Background(), f, site, l, fakeCopy(&copied), &out, time.Now); err != nil {
		t.Fatal(err)
	}
	if len(copied) != 0 || len(site.roundups) != 0 {
		t.Errorf("re-run copied %v, roundups %v", copied, site.roundups)
	}
}

func TestBackfillPhotosCopyFailureLeavesActivity(t *testing.T) {
	site, l := photoFixture()
	f := photoFlags(t, "--id", "parkrun", "--apply")
	failing := func(context.Context, string, string, stravaPhoto) (string, error) {
		return "", errors.New("download status 403")
	}
	var out bytes.Buffer
	if err := runBackfillPhotos(context.Background(), f, site, l, failing, &out, time.Now); err != nil {
		t.Fatal(err)
	}
	if len(site.saved) != 0 || !strings.Contains(out.String(), "failure: parkrun: copy photo b: download status 403") {
		t.Errorf("saved=%v out:\n%s", site.saved, out.String())
	}
}

func TestParseStravaPhotos(t *testing.T) {
	body := `[
	 {"unique_id":"u1","urls":{"100":"https://cdn/u1-100","2048":"https://cdn/u1-2048"}},
	 {"unique_id":"v1","urls":{"2048":"https://cdn/v1"},"video_url":"https://cdn/v1.mp4"},
	 {"unique_id":"u2","urls":{"1800":"https://cdn/u2"}}
	]`
	got, err := parseStravaPhotos([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []stravaPhoto{{UniqueID: "u1", URL: "https://cdn/u1-2048"}, {UniqueID: "u2", URL: "https://cdn/u2"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestStravaPhotoClientWaitsOutTheWindow(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/activities/42/photos" || r.URL.Query().Get("size") != "2048" || r.Method != http.MethodGet {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		w.Header().Set("X-ReadRateLimit-Limit", "100,1000")
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("X-ReadRateLimit-Usage", "5,995")
		_, _ = w.Write([]byte(`[{"unique_id":"u","urls":{"2048":"https://cdn/u"}}]`))
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 14, 10, 7, 0, 0, time.UTC)
	var slept []time.Duration
	c := &stravaPhotoClient{
		BaseURL: srv.URL, HTTP: srv.Client(),
		Token: func(context.Context) (string, error) { return "tok", nil },
		Now:   func() time.Time { return now },
		Sleep: func(d time.Duration) { slept = append(slept, d); now = now.Add(d) },
		Logf:  func(string, ...any) {},
	}
	got, err := c.Photos(context.Background(), "42")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v err %v", got, err)
	}
	if len(slept) != 1 || slept[0] != 8*time.Minute+10*time.Second {
		t.Errorf("slept %v, want to the next quarter hour", slept)
	}
	// The daily window is near its end: the next call stops instead of waiting.
	if _, err := c.Photos(context.Background(), "42"); !errors.Is(err, errStravaDailyLimit) {
		t.Errorf("err = %v, want daily limit", err)
	}
}

func TestPhotoCopierResizes(t *testing.T) {
	var src bytes.Buffer
	if err := png.Encode(&src, image.NewRGBA(image.Rect(0, 0, 3200, 1600))); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(src.Bytes()) }))
	defer srv.Close()
	var obj string
	var stored []byte
	cp := newPhotoCopier(srv.Client(), "bkt", func(_ context.Context, o string, data []byte) error {
		obj, stored = o, data
		return nil
	})
	u, err := cp(context.Background(), "uid", "42", stravaPhoto{UniqueID: "ab/c?", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if obj != "activity_photos/uid/strava-42/ab-c-.jpg" || u != "https://storage.googleapis.com/bkt/"+obj {
		t.Errorf("obj %q url %q", obj, u)
	}
	img, err := jpeg.Decode(bytes.NewReader(stored))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1600 || b.Dy() != 800 {
		t.Errorf("resized to %v", b)
	}
}
