package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const allScopes = "https://www.googleapis.com/auth/googlehealth.sleep.readonly https://www.googleapis.com/auth/googlehealth.health_metrics_and_measurements.readonly https://www.googleapis.com/auth/googlehealth.nutrition.readonly"

// fakeGoogle answers the endpoints the reader calls with one day of data
// each, and counts calls.
func fakeGoogle(t *testing.T, calls *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		switch r.URL.Path {
		case "/v4/users/me/dataTypes/sleep/dataPoints:reconcile":
			if f := r.URL.Query().Get("filter"); !strings.HasPrefix(f, `sleep.interval.civil_end_time >= "2026-10-2`) {
				t.Errorf("sleep filter = %q", f)
			}
			fmt.Fprint(w, `{"dataPoints":[
				{"sleep":{"interval":{"startTime":"2026-10-28T22:30:00Z","startUtcOffset":"0s","endTime":"2026-10-29T06:30:00Z","endUtcOffset":"0s"},
					"type":"STAGES","metadata":{"mainSleep":true},
					"summary":{"minutesAsleep":"432","minutesAwake":"48","minutesInSleepPeriod":"480",
					"stagesSummary":[{"type":"AWAKE","minutes":"48"},{"type":"LIGHT","minutes":"250"},{"type":"DEEP","minutes":"64"},{"type":"REM","minutes":"118"}]}}},
				{"sleep":{"interval":{"startTime":"2026-10-27T23:00:00Z","endTime":"2026-10-28T05:00:00Z"},
					"type":"CLASSIC","summary":{"minutesAsleep":"330","minutesAwake":"30"}}}]}`)
		case "/v4/users/me/dataTypes/weight/dataPoints:dailyRollUp":
			fmt.Fprint(w, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":10,"day":29}},"weight":{"weightGramsAvg":78412}},
				{"civilStartTime":{"date":{"year":2026,"month":10,"day":28}},"weight":{"weightGramsAvg":78600}}]}`)
		case "/v4/users/me/dataTypes/body-fat/dataPoints:dailyRollUp":
			fmt.Fprint(w, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":10,"day":29}},"bodyFat":{"bodyFatPercentageAvg":18.04}}]}`)
		case "/v4/users/me/dataTypes/daily-resting-heart-rate/dataPoints":
			fmt.Fprint(w, `{"dataPoints":[{"dailyRestingHeartRate":{"date":{"year":2026,"month":10,"day":29},"beatsPerMinute":"52"}},
				{"dailyRestingHeartRate":{"date":{"year":2026,"month":9,"day":1},"beatsPerMinute":"60"}}]}`)
		case "/v4/users/me/dataTypes/daily-heart-rate-variability/dataPoints":
			fmt.Fprint(w, `{"dataPoints":[{"dailyHeartRateVariability":{"date":{"year":2026,"month":10,"day":29},"averageHeartRateVariabilityMilliseconds":41.66}}]}`)
		case "/v4/users/me/dataTypes/nutrition-log/dataPoints:dailyRollUp":
			fmt.Fprint(w, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":10,"day":29}},
				"nutritionLog":{"energy":{"kcalSum":1000.4},"totalCarbohydrate":{"gramsSum":100},"totalFat":{"gramsSum":30},
				"nutrients":[{"nutrient":"PROTEIN","quantity":{"gramsSum":80.04}}]}}]}`)
		case "/v4/users/me/dataTypes/nutrition-log/dataPoints":
			fmt.Fprint(w, `{"dataPoints":[
				{"dataSource":{"application":{"packageName":"com.sbs.diet"}},"nutritionLog":{"interval":{"startTime":"2026-10-29T12:30:00Z","startUtcOffset":"0s","civilStartTime":{"date":{"year":2026,"month":10,"day":29}}},
					"energy":{"kcal":600},"foodDisplayName":"Lunch wrap"}},
				{"dataSource":{"application":{"packageName":"com.sbs.diet"}},"nutritionLog":{"interval":{"startTime":"2026-10-29T08:00:00Z","startUtcOffset":"0s","civilStartTime":{"date":{"year":2026,"month":10,"day":29}}},
					"energy":{"kcal":400.4},"mealType":"BREAKFAST","foodDisplayName":"Oats"}},
				{"dataSource":{"application":{"packageName":"com.sbs.diet"}},"nutritionLog":{"interval":{"startTime":"2026-10-28T19:00:00Z","startUtcOffset":"0s","civilStartTime":{"date":{"year":2026,"month":10,"day":28}}},
					"energy":{"kcal":700},"totalCarbohydrate":{"grams":60},"totalFat":{"grams":25},"nutrients":[{"nutrient":"PROTEIN","quantity":{"grams":50}}],"foodDisplayName":"Curry"}}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func newTestReader(base, scope string) *Reader {
	r := NewReader(func(ctx context.Context) (*Grant, error) {
		return &Grant{Scope: scope, HTTP: http.DefaultClient}, nil
	})
	r.BaseURL = base
	r.Location = time.UTC
	r.Now = func() time.Time { return time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC) }
	return r
}

func asJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSleepMatchesJ94Shape(t *testing.T) {
	var calls int
	srv := fakeGoogle(t, &calls)
	defer srv.Close()
	v, err := newTestReader(srv.URL, allScopes).Read(context.Background(), KindSleep, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"nights":[` +
		`{"date":"2026-10-28","start":"2026-10-27T23:00:00Z","end":"2026-10-28T05:00:00Z","minutes_asleep":330,"efficiency":92},` +
		`{"date":"2026-10-29","start":"2026-10-28T22:30:00Z","end":"2026-10-29T06:30:00Z","minutes_asleep":432,"efficiency":90,"stages":{"deep":64,"light":250,"rem":118,"wake":48},"main_sleep":true}]}`
	if got := asJSON(t, v); got != want {
		t.Errorf("sleep\n got %s\nwant %s", got, want)
	}
}

func TestBody(t *testing.T) {
	var calls int
	srv := fakeGoogle(t, &calls)
	defer srv.Close()
	v, err := newTestReader(srv.URL, allScopes).Read(context.Background(), KindBody, 30)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"weights":[{"date":"2026-10-28","kg":78.6},{"date":"2026-10-29","kg":78.41}],` +
		`"body_fat":[{"date":"2026-10-29","pct":18}],` +
		`"resting_hr":[{"date":"2026-10-29","bpm":52}],` + // 1 Sep is outside the window
		`"hrv":[{"date":"2026-10-29","ms":41.7}]}`
	if got := asJSON(t, v); got != want {
		t.Errorf("body\n got %s\nwant %s", got, want)
	}
}

func TestNutritionTotalsFromRollUpElseSummed(t *testing.T) {
	var calls int
	srv := fakeGoogle(t, &calls)
	defer srv.Close()
	v, err := newTestReader(srv.URL, allScopes).Read(context.Background(), KindNutrition, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"days":[` +
		// No roll-up for the 28th: totals are the entries summed.
		`{"date":"2026-10-28","kcal":700,"protein_g":50,"carbs_g":60,"fat_g":25,"meals":[{"type":null,"name":"Curry","kcal":700,"at":"2026-10-28T19:00:00Z"}]},` +
		`{"date":"2026-10-29","kcal":1000,"protein_g":80,"carbs_g":100,"fat_g":30,"meals":[` +
		`{"type":"breakfast","name":"Oats","kcal":400,"at":"2026-10-29T08:00:00Z"},` +
		`{"type":null,"name":"Lunch wrap","kcal":600,"at":"2026-10-29T12:30:00Z"}]}]}`
	if got := asJSON(t, v); got != want {
		t.Errorf("nutrition\n got %s\nwant %s", got, want)
	}
}

func TestCacheReusesAnswer(t *testing.T) {
	var calls int
	srv := fakeGoogle(t, &calls)
	defer srv.Close()
	r := newTestReader(srv.URL, allScopes)
	now := r.Now()
	r.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if _, err := r.Read(context.Background(), KindSleep, 7); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (cached)", calls)
	}
	if _, err := r.Read(context.Background(), KindSleep, 6); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("another window is another key: calls = %d", calls)
	}
	now = now.Add(DefaultTTL + time.Second)
	if _, err := r.Read(context.Background(), KindSleep, 7); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("expired entry must refetch: calls = %d", calls)
	}
}

func TestMissingScopeIsNotConnected(t *testing.T) {
	var calls int
	srv := fakeGoogle(t, &calls)
	defer srv.Close()
	r := newTestReader(srv.URL, "https://www.googleapis.com/auth/googlehealth.sleep.readonly")
	if _, err := r.Read(context.Background(), KindNutrition, 7); !errors.Is(err, ErrNotConnected) {
		t.Errorf("nutrition without its scope: %v", err)
	}
	if _, err := r.Read(context.Background(), KindBody, 30); !errors.Is(err, ErrNotConnected) {
		t.Errorf("body without its scope: %v", err)
	}
	if calls != 0 {
		t.Errorf("a missing scope must not call Google: %d calls", calls)
	}
	if _, err := r.Read(context.Background(), KindSleep, 7); err != nil {
		t.Errorf("sleep has its scope: %v", err)
	}
}

func TestGoogleRefusalsAreNotConnected(t *testing.T) {
	for name, body := range map[string]struct {
		status int
		json   string
	}{
		"scope":      {403, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"no","details":[{"reason":"MISSING_OAUTH_SCOPE"}]}}`},
		"not linked": {400, `{"error":{"code":400,"status":"FAILED_PRECONDITION","message":"ACCOUNT_NOT_LINKED"}}`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(body.status)
			fmt.Fprint(w, body.json)
		}))
		_, err := newTestReader(srv.URL, "").Read(context.Background(), KindSleep, 7)
		if !errors.Is(err, ErrNotConnected) {
			t.Errorf("%s: %v", name, err)
		}
		srv.Close()
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"code":500,"status":"INTERNAL"}}`)
	}))
	defer srv.Close()
	_, err := newTestReader(srv.URL, "").Read(context.Background(), KindSleep, 7)
	if err == nil || errors.Is(err, ErrNotConnected) {
		t.Errorf("a 500 is an upstream error, not 'not connected': %v", err)
	}
}

func TestNoGrantAndBadDays(t *testing.T) {
	r := NewReader(func(ctx context.Context) (*Grant, error) {
		return nil, fmt.Errorf("%w: none", ErrNotConnected)
	})
	if _, err := r.Read(context.Background(), KindSleep, 7); !errors.Is(err, ErrNotConnected) {
		t.Errorf("no grant: %v", err)
	}
	for _, d := range []int{0, -1, MaxDays + 1} {
		if _, err := r.Read(context.Background(), KindSleep, d); err == nil || errors.Is(err, ErrNotConnected) {
			t.Errorf("days=%d: %v", d, err)
		}
	}
}

func TestGrantHas(t *testing.T) {
	g := &Grant{Scope: "openid https://www.googleapis.com/auth/googlehealth.sleep"}
	if !g.Has(KindSleep.Scope()) {
		t.Error("the write scope covers the read")
	}
	if g.Has(KindNutrition.Scope()) {
		t.Error("nutrition not granted")
	}
	if !(&Grant{}).Has(KindBody.Scope()) {
		t.Error("an unknown scope list defers to the API")
	}
	if !strings.HasSuffix(KindBody.Scope(), "health_metrics_and_measurements.readonly") || KindBody.DefaultDays() != 30 || KindSleep.DefaultDays() != 7 {
		t.Error("kind table")
	}
}

func TestWindowIsTodayAndThePreviousDays(t *testing.T) {
	var filter string
	var rollUp []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			buf := make([]byte, 4096)
			n, _ := r.Body.Read(buf)
			rollUp = buf[:n]
		} else {
			filter = r.URL.Query().Get("filter")
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	r := newTestReader(srv.URL, "")
	if _, err := r.Read(context.Background(), KindSleep, 7); err != nil {
		t.Fatal(err)
	}
	if filter != `sleep.interval.civil_end_time >= "2026-10-23"` {
		t.Errorf("7 nights up to 29 Oct start at the 23rd: %q", filter)
	}
	if _, err := r.Read(context.Background(), KindNutrition, 1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rollUp), `"start":{"date":{"year":2026,"month":10,"day":29}},"end":{"date":{"year":2026,"month":10,"day":30}}`) {
		t.Errorf("days=1 rolls up today only: %s", rollUp)
	}
}
