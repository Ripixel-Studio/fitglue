package googlehealth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSleepReconcileRequestPaginationAndParse(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v4/users/me/dataTypes/sleep/dataPoints:reconcile" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("filter") != `sleep.interval.civil_end_time >= "2026-10-23"` || q.Get("pageSize") != "25" {
			t.Errorf("query = %v", q)
		}
		if q.Get("dataSourceFamily") != "" {
			t.Errorf("sleep must not set dataSourceFamily (INVALID_ARGUMENT): %v", q)
		}
		switch q.Get("pageToken") {
		case "":
			fmt.Fprint(w, `{"dataPoints":[{"sleep":{
				"interval":{"startTime":"2026-10-28T22:30:00Z","startUtcOffset":"3600s","endTime":"2026-10-29T06:30:00Z","endUtcOffset":"3600s"},
				"type":"STAGES","metadata":{"mainSleep":true},
				"summary":{"minutesAsleep":"440","minutesAwake":"40","minutesInSleepPeriod":"480",
					"stagesSummary":[{"type":"AWAKE","minutes":"40"},{"type":"LIGHT","minutes":"250"},{"type":"DEEP","minutes":"80"},{"type":"REM","minutes":"110"}]}}}],
				"nextPageToken":"p2"}`)
		case "p2":
			fmt.Fprint(w, `{"dataPoints":[{"sleep":{
				"interval":{"startTime":"2026-10-27T13:00:00Z","endTime":"2026-10-27T13:40:00Z"},
				"type":"CLASSIC","metadata":{"nap":true},
				"summary":{"minutesAsleep":35,"minutesInSleepPeriod":40}}},
				{"steps":{}},
				{"sleep":{"interval":{"startTime":"bad","endTime":"bad"}}}]}`)
		default:
			t.Errorf("page token %q", q.Get("pageToken"))
		}
	}))
	defer srv.Close()

	got, err := NewWithBaseURL(srv.URL, srv.Client()).Sleep(context.Background(), Date{2026, 10, 23})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(got) != 2 {
		t.Fatalf("calls=%d sessions=%+v", calls, got)
	}
	s := got[0]
	if s.WakeDate().String() != "2026-10-29" || s.End.Format(time.RFC3339) != "2026-10-29T07:30:00+01:00" {
		t.Errorf("wake %s end %s", s.WakeDate(), s.End.Format(time.RFC3339))
	}
	if s.MinutesAsleep != 440 || s.MinutesInPeriod != 480 || s.Stages["DEEP"] != 80 || s.Stages["AWAKE"] != 40 || !s.MainSleep {
		t.Errorf("session = %+v", s)
	}
	if !got[1].Nap || got[1].Type != "CLASSIC" || got[1].MinutesAsleep != 35 {
		t.Errorf("nap = %+v", got[1])
	}
}

func TestDailyRollUpRequestAndParse(t *testing.T) {
	var bodies []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v4/users/me/dataTypes/weight/dataPoints:dailyRollUp" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]interface{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, m)
		if m["pageToken"] == nil {
			fmt.Fprint(w, `{"rollupDataPoints":[
				{"civilStartTime":{"date":{"year":2026,"month":10,"day":28}},"weight":{"weightGramsAvg":78412.5}},
				{"civilStartTime":{"date":{"year":2026,"month":10,"day":29}}}
			],"nextPageToken":"n"}`)
			return
		}
		fmt.Fprint(w, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":10,"day":30}},"weight":{"weightGramsAvg":"78000"}}]}`)
	}))
	defer srv.Close()

	got, err := NewWithBaseURL(srv.URL, nil).DailyWeightKg(context.Background(), Date{2026, 10, 1}, Date{2026, 10, 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Date.String() != "2026-10-28" || got[0].Value != 78.4125 || got[1].Value != 78 {
		t.Errorf("got %+v", got)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d", len(bodies))
	}
	want := `{"dataSourceFamily":"users/me/dataSourceFamilies/all-sources","pageSize":90,"range":{"end":{"date":{"day":31,"month":10,"year":2026}},"start":{"date":{"day":1,"month":10,"year":2026}}},"windowSizeDays":1}`
	if b, _ := json.Marshal(bodies[0]); string(b) != want {
		t.Errorf("body = %s", b)
	}
	if bodies[1]["pageToken"] != "n" {
		t.Errorf("second body = %v", bodies[1])
	}
}

func TestRollUpRangeLimit(t *testing.T) {
	c := NewWithBaseURL("http://unused.invalid", nil)
	if _, err := c.DailyBodyFatPct(context.Background(), Date{2026, 1, 1}, Date{2026, 6, 1}); err == nil {
		t.Error("a 152-day roll-up should be refused before any call")
	}
}

func TestDailyNutritionAndFoodLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/users/me/dataTypes/nutrition-log/dataPoints:dailyRollUp":
			fmt.Fprint(w, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":10,"day":29}},
				"nutritionLog":{"energy":{"kcalSum":2101.4},"totalCarbohydrate":{"gramsSum":250},"totalFat":{"gramsSum":70.25},
				"nutrients":[{"nutrient":"PROTEIN","quantity":{"gramsSum":150.5}},{"nutrient":"DIETARY_FIBER","quantity":{"gramsSum":30}}]}}]}`)
		case "/v4/users/me/dataTypes/nutrition-log/dataPoints":
			q := r.URL.Query()
			if q.Get("filter") != `nutrition_log.interval.civil_start_time >= "2026-10-29"` || q.Get("dataSourceFamily") != DataSourceAll {
				t.Errorf("query = %v", q)
			}
			fmt.Fprint(w, `{"dataPoints":[{"dataSource":{"platform":"HEALTH_CONNECT","application":{"packageName":"com.sbs.diet"}},
				"nutritionLog":{"interval":{"startTime":"2026-10-29T12:30:00Z","startUtcOffset":"0s",
					"civilStartTime":{"date":{"year":2026,"month":10,"day":29},"time":{"hours":12,"minutes":30}}},
				"energy":{"kcal":612},"totalCarbohydrate":{"grams":70},"totalFat":{"grams":20},
				"nutrients":[{"nutrient":"PROTEIN","quantity":{"grams":45}}],"foodDisplayName":"Chicken wrap"}},
				{"dataSource":{"platform":"FITBIT"},"nutritionLog":{"interval":{"startTime":"2026-10-29T08:00:00Z"},
				"mealType":"BREAKFAST","foodDisplayName":"Oats","energy":{"kcal":"350"}}}]}`)
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewWithBaseURL(srv.URL, nil)

	days, err := c.DailyNutrition(context.Background(), Date{2026, 10, 29}, Date{2026, 10, 29})
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0].Kcal != 2101.4 || days[0].ProteinG != 150.5 || days[0].CarbsG != 250 || days[0].FatG != 70.25 {
		t.Errorf("days = %+v", days)
	}

	food, err := c.FoodLog(context.Background(), Date{2026, 10, 29})
	if err != nil {
		t.Fatal(err)
	}
	if len(food) != 2 {
		t.Fatalf("food = %+v", food)
	}
	if f := food[0]; f.Name != "Chicken wrap" || f.Kcal != 612 || f.ProteinG != 45 || f.Source != "com.sbs.diet" || f.MealType != "" || f.Date.String() != "2026-10-29" {
		t.Errorf("entry 0 = %+v", f)
	}
	if f := food[1]; f.MealType != "BREAKFAST" || f.Kcal != 350 || f.Source != "FITBIT" || f.Date.String() != "2026-10-29" {
		t.Errorf("entry 1 = %+v", f)
	}
}

func TestDailyRestingHRAndHRV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/v4/users/me/dataTypes/daily-resting-heart-rate/dataPoints":
			if q.Get("filter") != `daily_resting_heart_rate.date >= "2026-10-01"` {
				t.Errorf("filter = %q", q.Get("filter"))
			}
			if q.Get("pageToken") == "" {
				fmt.Fprint(w, `{"dataPoints":[{"dailyRestingHeartRate":{"date":{"year":2026,"month":10,"day":29},"beatsPerMinute":"52"}},
					{"dailyRestingHeartRate":{"date":{"year":2026,"month":10,"day":29},"beatsPerMinute":"60"}}],"nextPageToken":"x"}`)
				return
			}
			fmt.Fprint(w, `{"dataPoints":[{"dailyRestingHeartRate":{"date":{"year":2026,"month":10,"day":28},"beatsPerMinute":"53"}}]}`)
		case "/v4/users/me/dataTypes/daily-heart-rate-variability/dataPoints":
			if q.Get("filter") != `daily_heart_rate_variability.date >= "2026-10-01"` {
				t.Errorf("filter = %q", q.Get("filter"))
			}
			fmt.Fprint(w, `{"dataPoints":[{"dailyHeartRateVariability":{"date":{"year":2026,"month":10,"day":29},"averageHeartRateVariabilityMilliseconds":41.7}}]}`)
		}
	}))
	defer srv.Close()
	c := NewWithBaseURL(srv.URL, nil)

	rhr, err := c.DailyRestingHR(context.Background(), Date{2026, 10, 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rhr) != 2 || rhr[0].Value != 52 || rhr[1].Date.String() != "2026-10-28" {
		t.Errorf("rhr = %+v (one per day, first wins, both pages)", rhr)
	}
	hrv, err := c.DailyHRV(context.Background(), Date{2026, 10, 1})
	if err != nil || len(hrv) != 1 || hrv[0].Value != 41.7 {
		t.Errorf("hrv = %+v, %v", hrv, err)
	}
}

func TestSourcesCensus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filter") != `weight.sample_time.civil_time >= "2026-10-01"` {
			t.Errorf("filter = %q", r.URL.Query().Get("filter"))
		}
		fmt.Fprint(w, `{"dataPoints":[{"dataSource":{"platform":"HEALTH_CONNECT","application":{"packageName":"com.renpho.health"}}},
			{"dataSource":{"platform":"GOOGLE_WEB_API"}},{"dataSource":{"platform":"HEALTH_CONNECT","application":{"packageName":"com.renpho.health"}}}]}`)
	}))
	defer srv.Close()
	got, err := NewWithBaseURL(srv.URL, nil).Sources(context.Background(), "weight", Date{2026, 10, 1})
	if err != nil || got["com.renpho.health"] != 2 || got["GOOGLE_WEB_API"] != 1 {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := NewWithBaseURL(srv.URL, nil).Sources(context.Background(), "sleep", Date{2026, 10, 1}); err == nil {
		t.Error("sleep has no census")
	}
}

func TestNumAndDate(t *testing.T) {
	var n struct{ A, B, C, D Num }
	if err := json.Unmarshal([]byte(`{"A":"12","B":3.5,"C":null,"D":""}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.A != 12 || n.B != 3.5 || n.C != 0 || n.D != 0 {
		t.Errorf("%+v", n)
	}
	if err := json.Unmarshal([]byte(`{"A":"x"}`), &n); err == nil {
		t.Error("want an error for a non-number")
	}
	if d := (Date{2026, 3, 1}).AddDays(-1); d.String() != "2026-02-28" {
		t.Errorf("AddDays = %s", d)
	}
	if daysBetween(Date{2026, 10, 1}, Date{2026, 10, 31}) != 30 {
		t.Error("daysBetween")
	}
}

func rollupServer(t *testing.T, wantPath, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != wantPath {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, body)
	}))
}

func TestDailyStepsRollUp(t *testing.T) {
	srv := rollupServer(t, "/v4/users/me/dataTypes/steps/dataPoints:dailyRollUp", `{"rollupDataPoints":[
		{"civilStartTime":{"date":{"year":2026,"month":11,"day":1}},"civilEndTime":{"date":{"year":2026,"month":11,"day":2}},"steps":{"countSum":"8123"}},
		{"civilStartTime":{"date":{"year":2026,"month":11,"day":2}},"steps":{"countSum":"10450"}}]}`)
	defer srv.Close()
	got, err := NewWithBaseURL(srv.URL, nil).DailySteps(context.Background(), Date{2026, 11, 1}, Date{2026, 11, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Date.String() != "2026-11-01" || got[0].Value != 8123 || got[1].Value != 10450 {
		t.Errorf("got %+v", got)
	}
}

func TestDailyAZMRollUp(t *testing.T) {
	srv := rollupServer(t, "/v4/users/me/dataTypes/active-zone-minutes/dataPoints:dailyRollUp", `{"rollupDataPoints":[
		{"civilStartTime":{"date":{"year":2026,"month":11,"day":1}},"activeZoneMinutes":{"sumInFatBurnHeartZone":"10","sumInCardioHeartZone":"5","sumInPeakHeartZone":"2"}},
		{"civilStartTime":{"date":{"year":2026,"month":11,"day":2}},"activeZoneMinutes":{"sumInFatBurnHeartZone":"0"}},
		{"civilStartTime":{"date":{"year":2026,"month":11,"day":3}},"activeZoneMinutes":{"sumInCardioHeartZone":"3"}}]}`)
	defer srv.Close()
	got, err := NewWithBaseURL(srv.URL, nil).DailyActiveZoneMinutes(context.Background(), Date{2026, 11, 1}, Date{2026, 11, 3})
	if err != nil {
		t.Fatal(err)
	}
	// the zone sums are already credited minutes, so the day is their plain sum; a zero day is dropped.
	if len(got) != 2 || got[0].Value != 17 || got[1].Date.String() != "2026-11-03" || got[1].Value != 6 {
		t.Errorf("got %+v", got)
	}
}

func TestDailyVO2SpO2Resp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/v4/users/me/dataTypes/daily-vo2-max/dataPoints":
			if q.Get("filter") != `daily_vo2_max.date >= "2026-11-01"` {
				t.Errorf("filter %q", q.Get("filter"))
			}
			fmt.Fprint(w, `{"dataPoints":[{"dailyVo2Max":{"date":{"year":2026,"month":11,"day":1},"vo2Max":"47.5"}}]}`)
		case "/v4/users/me/dataTypes/daily-oxygen-saturation/dataPoints":
			if q.Get("filter") != `daily_oxygen_saturation.date >= "2026-11-01"` {
				t.Errorf("filter %q", q.Get("filter"))
			}
			fmt.Fprint(w, `{"dataPoints":[{"dailyOxygenSaturation":{"date":{"year":2026,"month":11,"day":1},"averagePercentage":96.4}}]}`)
		case "/v4/users/me/dataTypes/daily-respiratory-rate/dataPoints":
			if q.Get("filter") != `daily_respiratory_rate.date >= "2026-11-01"` {
				t.Errorf("filter %q", q.Get("filter"))
			}
			fmt.Fprint(w, `{"dataPoints":[{"dailyRespiratoryRate":{"date":{"year":2026,"month":11,"day":1},"breathsPerMinute":"14.2"}}]}`)
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewWithBaseURL(srv.URL, nil)
	from := Date{2026, 11, 1}
	for name, tc := range map[string]struct {
		fn   func(context.Context, Date) ([]DailyValue, error)
		want float64
	}{"vo2": {c.DailyVO2Max, 47.5}, "spo2": {c.DailySpO2, 96.4}, "resp": {c.DailyRespiratoryRate, 14.2}} {
		got, err := tc.fn(context.Background(), from)
		if err != nil || len(got) != 1 || got[0].Value != tc.want || got[0].Date.String() != "2026-11-01" {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
}

func TestDailyTypeRefusedIsEmpty(t *testing.T) {
	for _, code := range []int{400, 403, 404} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"no"}}`, code)
		}))
		_, err := NewWithBaseURL(srv.URL, nil).DailyVO2Max(context.Background(), Date{2026, 11, 1})
		srv.Close()
		if !IsTypeRefused(err) {
			t.Errorf("%d: IsTypeRefused(%v) = false", code, err)
		}
	}
	for _, err := range []error{nil, fmt.Errorf("boom"), &APIError{StatusCode: 500}, &APIError{StatusCode: 429}} {
		if IsTypeRefused(err) {
			t.Errorf("IsTypeRefused(%v) = true", err)
		}
	}
}

func TestNutritionFibre(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/users/me/dataTypes/nutrition-log/dataPoints:dailyRollUp" {
			fmt.Fprint(w, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":10,"day":29}},
				"nutritionLog":{"nutrients":[{"nutrient":"DIETARY_FIBER","quantity":{"gramsSum":31.5}}]}}]}`)
			return
		}
		fmt.Fprint(w, `{"dataPoints":[{"nutritionLog":{"interval":{"startTime":"2026-10-29T12:30:00Z"},
			"nutrients":[{"nutrient":"DIETARY_FIBER","quantity":{"grams":4.5}}]}}]}`)
	}))
	defer srv.Close()
	c := NewWithBaseURL(srv.URL, nil)
	days, err := c.DailyNutrition(context.Background(), Date{2026, 10, 29}, Date{2026, 10, 29})
	if err != nil || len(days) != 1 || days[0].FibreG != 31.5 {
		t.Errorf("days %+v %v", days, err)
	}
	food, err := c.FoodLog(context.Background(), Date{2026, 10, 29})
	if err != nil || len(food) != 1 || food[0].FibreG != 4.5 {
		t.Errorf("food %+v %v", food, err)
	}
}
