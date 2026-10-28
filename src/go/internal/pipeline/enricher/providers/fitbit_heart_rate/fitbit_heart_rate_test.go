package fitbit_heart_rate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeHealth is a Google Health API stand-in: it serves heart-rate samples
// from a list, honours the physical-time filter, and records every request.
type fakeHealth struct {
	mu       sync.Mutex
	samples  []time.Time // one sample per entry, bpm = 100 + index
	status   int         // non-zero: answer every request with this status
	body     string      // body for status
	header   http.Header // extra headers for status
	requests []*http.Request
	srv      *httptest.Server
}

func newFakeHealth(t *testing.T) *fakeHealth {
	f := &fakeHealth{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHealth) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Clone(context.Background()))
	f.mu.Unlock()
	if f.status != 0 {
		for k, v := range f.header {
			w.Header()[k] = v
		}
		w.WriteHeader(f.status)
		fmt.Fprint(w, f.body)
		return
	}
	if r.URL.Path != "/v4/users/me/dataTypes/heart-rate/dataPoints" {
		http.NotFound(w, r)
		return
	}
	// heart_rate.sample_time.physical_time >= "<from>" AND heart_rate.sample_time.physical_time < "<to>"
	parts := strings.Split(r.URL.Query().Get("filter"), `"`)
	if len(parts) != 5 || strings.TrimSpace(parts[0]) != "heart_rate.sample_time.physical_time >=" ||
		strings.TrimSpace(parts[2]) != "AND heart_rate.sample_time.physical_time <" {
		http.Error(w, "bad filter "+r.URL.Query().Get("filter"), 400)
		return
	}
	from, err1 := time.Parse(time.RFC3339, parts[1])
	to, err2 := time.Parse(time.RFC3339, parts[3])
	if err1 != nil || err2 != nil {
		http.Error(w, "bad filter times", 400)
		return
	}
	var pts []string
	// Newest first, like the API.
	for i := len(f.samples) - 1; i >= 0; i-- {
		ts := f.samples[i]
		if ts.Before(from) || !ts.Before(to) {
			continue
		}
		pts = append(pts, fmt.Sprintf(`{"heartRate":{"sampleTime":{"physicalTime":%q,"utcOffset":"3600s"},"beatsPerMinute":"%d"}}`, ts.UTC().Format(time.RFC3339), 100+i))
	}
	fmt.Fprintf(w, `{"dataPoints":[%s]}`, strings.Join(pts, ","))
}

// every returns one sample per step from start for n samples.
func every(start time.Time, step time.Duration, n int) []time.Time {
	out := make([]time.Time, n)
	for i := range out {
		out[i] = start.Add(time.Duration(i) * step)
	}
	return out
}

func (f *fakeHealth) provider() *FitBitHeartRate {
	p := NewFitBitHeartRate()
	p.Service = &bootstrap.Service{}
	p.baseURL = f.srv.URL
	return p
}

func ghUser() *user.Record {
	return &user.Record{
		UserProfile: &pbuser.UserProfile{UserId: "test-user"},
		Integrations: &pbuser.UserIntegrations{
			Googlehealth: &pbuser.GoogleHealthIntegration{Enabled: true, AccessToken: "t"},
		},
	}
}

func act(start time.Time, durationSec float64) *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{
		StartTime: timestamppb.New(start),
		Sessions:  []*pbactivity.Session{{StartTime: timestamppb.New(start), TotalElapsedTime: durationSec}},
	}
}

func enrich(t *testing.T, f *fakeHealth, a *pbactivity.StandardizedActivity, u *user.Record, inputs map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	t.Helper()
	return f.provider().EnrichWithClient(context.Background(), slog.Default(), a, u, inputs, f.srv.Client(), doNotRetry)
}

func TestIdentity(t *testing.T) {
	p := NewFitBitHeartRate()
	if p.Name() != "fitbit-heart-rate" {
		t.Errorf("Name() = %q: the stored pipeline identifier must not change", p.Name())
	}
	if p.ProviderType() != pbplugin.EnricherProviderType_ENRICHER_PROVIDER_FITBIT_HEART_RATE {
		t.Errorf("ProviderType() = %v", p.ProviderType())
	}
	if p.IsIdempotent() {
		t.Error("must be non-idempotent so resume replays the journalled stream")
	}
	svc := &bootstrap.Service{}
	p.SetService(svc)
	if p.Service != svc {
		t.Error("SetService")
	}
}

func TestEnrich_ReadsGoogleHealthForTheActivityWindow(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Date(2025, 12, 25, 10, 0, 0, 0, time.UTC)
	f.samples = every(start, 30*time.Second, 120) // full hour at 30 s
	f.samples = append([]time.Time{start.Add(-10 * time.Minute)}, f.samples...)

	res, err := enrich(t, f, act(start, 3600), ghUser(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Metadata["hr_source"] != "googlehealth" || res.Metadata["status_detail"] != "Success" {
		t.Errorf("metadata = %v", res.Metadata)
	}
	if res.Metadata["query_start"] != "2025-12-25T10:00:00Z" || res.Metadata["query_end"] != "2025-12-25T11:00:00Z" {
		t.Errorf("window = %s..%s", res.Metadata["query_start"], res.Metadata["query_end"])
	}
	if res.Metadata["points_found"] != "120" {
		t.Errorf("points_found = %s (the pre-start sample must be filtered out)", res.Metadata["points_found"])
	}
	if res.Metadata["utc_offset"] != "1h0m0s" {
		t.Errorf("utc_offset = %s", res.Metadata["utc_offset"])
	}
	if len(res.HeartRateStream) != 3600 {
		t.Fatalf("stream len = %d", len(res.HeartRateStream))
	}
	// Sample i lands at second 30*i with bpm 100+i+1 (index 0 is the pre-start sample).
	if res.HeartRateStream[0] != 101 || res.HeartRateStream[30] != 102 || res.HeartRateStream[45] != 102 {
		t.Errorf("stream[0,30,45] = %d,%d,%d", res.HeartRateStream[0], res.HeartRateStream[30], res.HeartRateStream[45])
	}
	q := f.requests[0].URL.Query()
	if q.Get("dataSourceFamily") != "users/me/dataSourceFamilies/google-wearables" {
		t.Errorf("dataSourceFamily = %q", q.Get("dataSourceFamily"))
	}
	if !strings.Contains(q.Get("filter"), `>= "2025-12-25T10:00:00Z"`) || !strings.Contains(q.Get("filter"), `< "2025-12-25T11:00:01Z"`) {
		t.Errorf("filter = %q", q.Get("filter"))
	}
}

// TestEnrich_LocalTimezoneIsIrrelevant: a BST activity (06:00 UTC = 07:00
// local) queries 06:00Z — physical time, no profile lookup — and only the one
// request is made.
func TestEnrich_LocalTimezoneIsIrrelevant(t *testing.T) {
	f := newFakeHealth(t)
	london, _ := time.LoadLocation("Europe/London")
	start := time.Date(2026, 7, 4, 7, 0, 0, 0, london)
	f.samples = every(start, time.Second, 600)
	res, err := enrich(t, f, act(start, 600), ghUser(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Metadata["query_start"] != "2026-07-04T06:00:00Z" {
		t.Errorf("query_start = %s", res.Metadata["query_start"])
	}
	if len(f.requests) != 1 {
		t.Errorf("requests = %d, want 1 (no profile/timezone call)", len(f.requests))
	}
	if res.HeartRateStream[0] != 100 || res.HeartRateStream[599] != 699 {
		t.Errorf("stream ends = %d, %d", res.HeartRateStream[0], res.HeartRateStream[599])
	}
}

func TestEnrich_SpansMidnightIsOneQuery(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Date(2026, 1, 1, 23, 58, 0, 0, time.UTC)
	f.samples = every(start, time.Second, 720)
	res, err := enrich(t, f, act(start, 720), ghUser(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || res.Metadata["points_found"] != "720" || len(res.HeartRateStream) != 720 {
		t.Errorf("requests=%d points=%s len=%d", len(f.requests), res.Metadata["points_found"], len(res.HeartRateStream))
	}
}

// TestEnrich_PerSessionWindow is the regression test for the cross-activity HR
// duplication bug: two classes sharing a stale top-level start each query
// their own session window.
func TestEnrich_PerSessionWindow(t *testing.T) {
	f := newFakeHealth(t)
	stale := timestamppb.New(time.Date(2026, 8, 3, 7, 0, 52, 0, time.UTC))
	mk := func(s time.Time) *pbactivity.StandardizedActivity {
		return &pbactivity.StandardizedActivity{StartTime: stale, Sessions: []*pbactivity.Session{{StartTime: timestamppb.New(s), TotalElapsedTime: 3120}}}
	}
	r1, err := enrich(t, f, mk(time.Date(2026, 8, 3, 7, 0, 52, 0, time.UTC)), ghUser(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := enrich(t, f, mk(time.Date(2026, 8, 3, 8, 0, 43, 0, time.UTC)), ghUser(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Metadata["query_start"] != "2026-08-03T07:00:52Z" || r2.Metadata["query_start"] != "2026-08-03T08:00:43Z" {
		t.Errorf("windows = %s, %s", r1.Metadata["query_start"], r2.Metadata["query_start"])
	}
}

func TestEnrich_TopLevelStartFallback(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	a := &pbactivity.StandardizedActivity{StartTime: timestamppb.New(start), Sessions: []*pbactivity.Session{{TotalElapsedTime: 60}}}
	res, err := enrich(t, f, a, ghUser(), nil, false)
	if err != nil || res.Metadata["query_start"] != "2025-06-01T09:00:00Z" {
		t.Errorf("res=%v err=%v", res, err)
	}
	if _, err := enrich(t, f, &pbactivity.StandardizedActivity{}, ghUser(), nil, false); err == nil {
		t.Error("no start time should error")
	}
}

func TestEnrich_NotConnected(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	for name, u := range map[string]*user.Record{
		"no integrations": {UserProfile: &pbuser.UserProfile{UserId: "u"}},
		"disabled":        {Integrations: &pbuser.UserIntegrations{Googlehealth: &pbuser.GoogleHealthIntegration{Enabled: false}}},
		// Only the legacy Fitbit grant: Fitbit is never called any more.
		"fitbit only": {Integrations: &pbuser.UserIntegrations{Fitbit: &pbuser.FitbitIntegration{Enabled: true, AccessToken: "x"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := enrich(t, f, act(start, 60), u, nil, false)
			if err == nil || !strings.Contains(err.Error(), "fitgluectl connect googlehealth") {
				t.Errorf("err = %v", err)
			}
		})
	}
	if len(f.requests) != 0 {
		t.Errorf("made %d requests without a grant", len(f.requests))
	}
}

func TestEnrich_SkipIfExistingHR_UnlessForced(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	f.samples = every(start, time.Second, 60)
	a := act(start, 60)
	a.Sessions[0].Laps = []*pbactivity.Lap{{Records: []*pbactivity.Record{{Timestamp: timestamppb.New(start), HeartRate: 140}}}}

	res, err := enrich(t, f, a, ghUser(), nil, false)
	if err != nil || !res.Skipped || res.Metadata["hr_source"] != "skipped" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(f.requests) != 0 {
		t.Error("skipped enrichment must not call the API")
	}
	res, err = enrich(t, f, a, ghUser(), map[string]string{"force": "true"}, false)
	if err != nil || res.Skipped || res.Metadata["hr_source"] != "googlehealth" {
		t.Fatalf("forced: res=%+v err=%v", res, err)
	}
}

func TestEnrich_LagRetryWhileDataHasNotLanded(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Now().Add(-20 * time.Minute).Truncate(time.Second)
	f.samples = every(start, time.Second, 300) // only the first 5 of 15 minutes synced

	_, err := enrich(t, f, act(start, 900), ghUser(), nil, false)
	var re *providers.RetryableError
	if !errors.As(err, &re) {
		t.Fatalf("recent + partial should be retryable, got %v", err)
	}

	// Lag exhausted: accept the partial stream, gaps left as zero (no stretching).
	res, err := enrich(t, f, act(start, 900), ghUser(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.HeartRateStream[299] == 0 || res.HeartRateStream[300] != 0 || res.HeartRateStream[899] != 0 {
		t.Errorf("tail must stay a gap: [299]=%d [300]=%d [899]=%d", res.HeartRateStream[299], res.HeartRateStream[300], res.HeartRateStream[899])
	}

	// Nothing synced yet for a recent activity: retry too.
	f.samples = nil
	if _, err := enrich(t, f, act(start, 900), ghUser(), nil, false); !errors.As(err, &re) {
		t.Errorf("recent + empty should be retryable, got %v", err)
	}
}

func TestEnrich_OldActivityAcceptsWhatThereIs(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	f.samples = every(start, time.Second, 300)
	res, err := enrich(t, f, act(start, 900), ghUser(), nil, false)
	if err != nil || res.Metadata["points_found"] != "300" {
		t.Fatalf("res=%v err=%v", res, err)
	}
	f.samples = nil
	res, err = enrich(t, f, act(start, 900), ghUser(), nil, false)
	if err != nil || res.Metadata["points_found"] != "0" {
		t.Fatalf("empty old: res=%v err=%v", res, err)
	}
}

func TestEnrich_LagReArmedOnResume(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	f.samples = every(start, time.Second, 300)
	_, err := enrich(t, f, act(start, 900), ghUser(), map[string]string{"is_resume": "true"}, false)
	var re *providers.RetryableError
	if !errors.As(err, &re) {
		t.Fatalf("resume + partial should be retryable, got %v", err)
	}
}

func TestEnrich_APIErrors(t *testing.T) {
	start := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		status    int
		body      string
		retryHdr  string
		retryable bool
		wantAfter time.Duration
		contains  string
	}{
		{"429 is retryable with Retry-After", 429, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, "42", true, 42 * time.Second, "429"},
		{"503 is retryable", 503, `unavailable`, "", true, 0, "503"},
		{"account not linked is terminal", 400, `{"error":{"code":400,"status":"FAILED_PRECONDITION","message":"link the app","details":[{"reason":"ACCOUNT_NOT_LINKED"}]}}`, "", false, 0, "ACCOUNT_NOT_LINKED"},
		{"missing scope is terminal", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"reason":"MISSING_OAUTH_SCOPE"}]}}`, "", false, 0, "MISSING_OAUTH_SCOPE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHealth(t)
			f.status, f.body = tc.status, tc.body
			if tc.retryHdr != "" {
				f.header = http.Header{"Retry-After": {tc.retryHdr}}
			}
			_, err := enrich(t, f, act(start, 600), ghUser(), nil, false)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("err = %v", err)
			}
			var re *providers.RetryableError
			if errors.As(err, &re) != tc.retryable {
				t.Fatalf("retryable = %v, want %v (%v)", !tc.retryable, tc.retryable, err)
			}
			if tc.retryable {
				if re.RetryAfter != tc.wantAfter {
					t.Errorf("RetryAfter = %v, want %v", re.RetryAfter, tc.wantAfter)
				}
				// Lag exhausted: complete without HR instead of failing.
				res, err := enrich(t, f, act(start, 600), ghUser(), nil, true)
				if err != nil || res.Metadata["hr_source"] != "skipped" {
					t.Errorf("lag exhausted: res=%v err=%v", res, err)
				}
			}
		})
	}
}

func TestEnrich_TransportErrorIsRetryable(t *testing.T) {
	f := newFakeHealth(t)
	f.srv.Close()
	start := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	_, err := enrich(t, f, act(start, 600), ghUser(), nil, false)
	var re *providers.RetryableError
	if !errors.As(err, &re) {
		t.Fatalf("transport error should be retryable, got %v", err)
	}
	res, err := enrich(t, f, act(start, 600), ghUser(), nil, true)
	if err != nil || res.Metadata["hr_source"] != "skipped" {
		t.Errorf("lag exhausted: res=%v err=%v", res, err)
	}
}

func TestEnrich_GPSAlignment(t *testing.T) {
	f := newFakeHealth(t)
	start := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	f.samples = every(start, 10*time.Second, 60)
	records := make([]*pbactivity.Record, 0, 600)
	for i := 0; i < 600; i++ {
		records = append(records, &pbactivity.Record{
			Timestamp:    timestamppb.New(start.Add(time.Duration(i) * time.Second)),
			PositionLat:  51.5 + float64(i)*0.00001,
			PositionLong: -0.12,
		})
	}
	a := act(start, 600)
	a.Sessions[0].Laps = []*pbactivity.Lap{{Records: records}}
	res, err := enrich(t, f, a, ghUser(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Metadata["alignment_status"] == "skipped_no_gps" || res.Metadata["alignment_status"] == "" {
		t.Errorf("alignment_status = %q", res.Metadata["alignment_status"])
	}
	if len(res.HeartRateStream) != 600 || res.HeartRateStream[0] != 100 || res.HeartRateStream[100] == 0 {
		t.Errorf("aligned stream len=%d [0]=%d [100]=%d", len(res.HeartRateStream), res.HeartRateStream[0], res.HeartRateStream[100])
	}

	// GPS present but no HR samples: index-based fallback, all gaps.
	f.samples = nil
	res, err = enrich(t, f, a, ghUser(), nil, false)
	if err != nil || len(res.HeartRateStream) != 600 || res.HeartRateStream[0] != 0 {
		t.Errorf("empty: res=%v err=%v", res, err)
	}
}

func TestBuildStreamIndexBased(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	s := []providers.TimedSample{
		{Timestamp: start.Add(-time.Second), Value: 1}, // before start: dropped
		{Timestamp: start.Add(2 * time.Second), Value: 120},
		{Timestamp: start.Add(5 * time.Second), Value: 130},
		{Timestamp: start.Add(20 * time.Second), Value: 9}, // past the end: dropped
	}
	got := buildStreamIndexBased(s, start, 8)
	want := []int{0, 0, 120, 120, 120, 130, 0, 0}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v want %v", got, want)
	}
	if len(buildStreamIndexBased(nil, start, -1)) != 0 {
		t.Error("negative duration")
	}
}
