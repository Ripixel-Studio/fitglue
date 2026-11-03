package strava

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

func TestFetchPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/activities/42/streams"):
			http.Error(w, "no streams", http.StatusNotFound) // best-effort
		case strings.HasSuffix(r.URL.Path, "/activities/42"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":42,"name":"Hyrox","sport_type":"Crossfit","type":"Crossfit","start_date":"2026-10-28T08:20:02Z","elapsed_time":4500,"moving_time":4500}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := FetchPayload(context.Background(), srv.URL, "tok", "42", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != activitypb.ActivitySource_SOURCE_STRAVA || p.GetActivityId() != "42" || p.StandardizedActivity.GetName() != "Hyrox" {
		t.Errorf("payload %+v", p)
	}
	if _, err := FetchPayload(context.Background(), srv.URL, "bad", "42", "owner"); err == nil {
		t.Error("want error for 401")
	}
	if _, err := FetchPayload(context.Background(), srv.URL, "tok", "x", "owner"); err == nil {
		t.Error("want error for a non-numeric id")
	}
}
