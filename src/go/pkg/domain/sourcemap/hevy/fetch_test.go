package hevy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

func TestFetchPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workouts/w1" || r.Header.Get("api-key") != "k" {
			http.Error(w, "bad", http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"id":"w1","title":"Tower Pilates","start_time":"2026-11-02T08:00:13Z","end_time":"2026-11-02T08:50:13Z","exercises":[]}`))
	}))
	defer srv.Close()

	p, err := FetchPayload(context.Background(), srv.Client(), srv.URL, "k", "w1", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != activitypb.ActivitySource_SOURCE_HEVY || p.GetActivityId() != "w1" || p.UserId != "owner" {
		t.Errorf("payload %+v", p)
	}
	if p.StandardizedActivity.Name != "Tower Pilates" || p.StandardizedActivity.Sessions[0].TotalElapsedTime != 3000 {
		t.Errorf("activity %+v", p.StandardizedActivity)
	}
	if p.OriginalPayloadJson == "" || p.PipelineExecutionId != nil {
		t.Errorf("raw/exec id: %q %v", p.OriginalPayloadJson, p.PipelineExecutionId)
	}

	if _, err := FetchPayload(context.Background(), srv.Client(), srv.URL, "wrong", "w1", "owner"); err == nil {
		t.Error("want error for a 404")
	}
	if _, err := FetchPayload(context.Background(), srv.Client(), srv.URL, "k", "", "owner"); err == nil {
		t.Error("want error for an empty id")
	}
}
