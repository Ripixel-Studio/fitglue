package googlehealth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHeartRateRequestAndParse(t *testing.T) {
	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v4/users/me/dataTypes/heart-rate/dataPoints" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		wantFilter := `heart_rate.sample_time.physical_time >= "2026-10-28T09:00:00Z" AND heart_rate.sample_time.physical_time < "2026-10-28T10:00:00Z"`
		if q.Get("filter") != wantFilter {
			t.Errorf("filter = %q", q.Get("filter"))
		}
		if q.Get("dataSourceFamily") != DataSourceWearables || q.Get("pageSize") != "10000" {
			t.Errorf("query = %v", q)
		}
		switch q.Get("pageToken") {
		case "":
			// Newest first, as the API orders them; one duplicate second.
			fmt.Fprint(w, `{"dataPoints":[
				{"heartRate":{"sampleTime":{"physicalTime":"2026-10-28T09:00:02Z","utcOffset":"0s"},"beatsPerMinute":"130"}},
				{"heartRate":{"sampleTime":{"physicalTime":"2026-10-28T09:00:01Z","utcOffset":"0s"},"beatsPerMinute":"125"}},
				{"heartRate":{"sampleTime":{"physicalTime":"2026-10-28T09:00:01.400Z","utcOffset":"0s"},"beatsPerMinute":"999"}}
			],"nextPageToken":"p2"}`)
		case "p2":
			fmt.Fprint(w, `{"dataPoints":[
				{"heartRate":{"sampleTime":{"physicalTime":"2026-10-28T09:00:00Z","utcOffset":"3600s"},"beatsPerMinute":120}},
				{"steps":{}},
				{"heartRate":{"sampleTime":{"physicalTime":"bad"},"beatsPerMinute":"1"}}
			]}`)
		default:
			t.Errorf("unexpected page token %q", q.Get("pageToken"))
		}
	}))
	defer srv.Close()

	got, err := NewWithBaseURL(srv.URL, srv.Client()).HeartRate(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (pagination)", calls)
	}
	if len(got) != 3 {
		t.Fatalf("samples = %+v", got)
	}
	for i, want := range []int{120, 125, 130} {
		if got[i].BPM != want || !got[i].Time.Equal(start.Add(time.Duration(i)*time.Second)) {
			t.Errorf("sample %d = %+v", i, got[i])
		}
	}
	if got[0].UTCOffset != time.Hour {
		t.Errorf("utc offset = %v", got[0].UTCOffset)
	}
}

func TestHeartRateEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	got, err := NewWithBaseURL(srv.URL, nil).HeartRate(context.Background(), time.Now().Add(-time.Hour), time.Now())
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		header     string
		wantReason string
		transient  bool
		retryAfter time.Duration
	}{
		{"not linked", 400, `{"error":{"code":400,"message":"The user has not linked","status":"FAILED_PRECONDITION","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"ACCOUNT_NOT_LINKED"}]}}`, "", "ACCOUNT_NOT_LINKED", false, 0},
		{"not linked, message only", 400, `ACCOUNT_NOT_LINKED`, "", "ACCOUNT_NOT_LINKED", false, 0},
		{"missing scope", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"reason":"MISSING_OAUTH_SCOPE"}]}}`, "", "MISSING_OAUTH_SCOPE", false, 0},
		{"rate limited", 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, "30", "", true, 30 * time.Second},
		{"server", 503, `oops`, "soon", "", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			_, err := NewWithBaseURL(srv.URL, srv.Client()).Identity(context.Background())
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v", err)
			}
			if apiErr.StatusCode != tc.status || apiErr.Reason != tc.wantReason || apiErr.Transient() != tc.transient || apiErr.RetryAfter != tc.retryAfter {
				t.Errorf("apiErr = %+v", apiErr)
			}
			if !strings.Contains(apiErr.Error(), fmt.Sprint(tc.status)) {
				t.Errorf("Error() = %q", apiErr.Error())
			}
		})
	}
}

func TestIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/users/me/identity" {
			t.Errorf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"name":"users/me/identity","legacyUserId":"ABC123","healthUserId":"1234567890"}`)
	}))
	defer srv.Close()
	id, err := NewWithBaseURL(srv.URL, srv.Client()).Identity(context.Background())
	if err != nil || id.HealthUserID != "1234567890" || id.LegacyUserID != "ABC123" {
		t.Errorf("identity = %+v, %v", id, err)
	}
}

func TestDecodeErrorAndTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	if _, err := NewWithBaseURL(srv.URL, srv.Client()).Identity(context.Background()); err == nil {
		t.Error("expected decode error")
	}
	srv.Close()
	if _, err := NewWithBaseURL(srv.URL, srv.Client()).Identity(context.Background()); err == nil {
		t.Error("expected transport error")
	}
}

func TestParseDurationSeconds(t *testing.T) {
	for in, want := range map[string]time.Duration{"3600s": time.Hour, "-18000s": -5 * time.Hour, "0s": 0, "": 0, "1h": 0, "xs": 0} {
		if got := parseDurationSeconds(in); got != want {
			t.Errorf("%q = %v", in, got)
		}
	}
}
