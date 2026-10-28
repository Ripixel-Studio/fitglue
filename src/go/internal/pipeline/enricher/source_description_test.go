package enricher

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/description"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const roxfitText = "Completed via ROXFIT\n\n⏱️ Split Times:\nRun 1: 4:54\nSki Erg: 3:49\nRoxzone: 6:14\n"

func stravaPayload(desc string) *pbevents.ActivityPayload {
	pipelineID := "pipeline-run"
	return &pbevents.ActivityPayload{
		UserId:     "user-1",
		Source:     pbactivity.ActivitySource_SOURCE_STRAVA,
		PipelineId: &pipelineID,
		Timestamp:  timestamppb.New(time.Date(2026, 10, 27, 12, 10, 2, 0, time.UTC)),
		StandardizedActivity: &pbactivity.StandardizedActivity{
			ExternalId:  "20751310619",
			Name:        "HYROX - Birmingham",
			Description: desc,
			Sessions: []*pbactivity.Session{{
				StartTime:        timestamppb.New(time.Date(2026, 10, 27, 12, 10, 2, 0, time.UTC)),
				TotalElapsedTime: 4529,
			}},
		},
	}
}

type fetchRecorder struct {
	calls int
	desc  string
	err   error
}

func (f *fetchRecorder) fetch(_ context.Context, _ string, _ pbactivity.ActivitySource, _ string) (string, error) {
	f.calls++
	return f.desc, f.err
}

func stravaSuccess() *pbpipeline.DestinationOutcome {
	return &pbpipeline.DestinationOutcome{
		Destination: pbplugin.DestinationType_DESTINATION_STRAVA,
		Status:      pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS,
	}
}

func TestRefreshSourceDescription_Guard(t *testing.T) {
	cases := []struct {
		name        string
		db          *MockDatabase
		run         *pbpipeline.PipelineRun
		repost      bool
		source      pbactivity.ActivitySource
		fetchErr    error
		wantDesc    string
		wantWritten bool
		wantApplies bool
		wantFetch   int
	}{
		{
			name:        "never written: adopts Strava's current description",
			db:          &MockDatabase{},
			wantDesc:    roxfitText,
			wantApplies: true,
			wantFetch:   1,
		},
		{
			name:        "run already has a Strava SUCCESS: keeps ingested",
			db:          &MockDatabase{},
			run:         &pbpipeline.PipelineRun{Destinations: []*pbpipeline.DestinationOutcome{stravaSuccess()}},
			wantWritten: true,
			wantApplies: true,
		},
		{
			name: "outcome subcollection has a Strava SUCCESS: keeps ingested",
			db: &MockDatabase{GetDestinationOutcomesFunc: func(context.Context, string, string) ([]*pbpipeline.DestinationOutcome, error) {
				return []*pbpipeline.DestinationOutcome{stravaSuccess()}, nil
			}},
			wantWritten: true,
			wantApplies: true,
		},
		{
			name: "another pipeline/run already wrote (uploaded-activity record): keeps ingested",
			db: &MockDatabase{GetUploadedActivityFunc: func(_ context.Context, _ string, d pbplugin.DestinationType, id string) (*pbactivity.UploadedActivityRecord, error) {
				if d == pbplugin.DestinationType_DESTINATION_STRAVA && id == "20751310619" {
					return &pbactivity.UploadedActivityRecord{Id: "strava:20751310619"}, nil
				}
				return nil, nil
			}},
			wantWritten: true,
			wantApplies: true,
		},
		{
			name:        "repost keeps the stored clean payload (Rule E22)",
			db:          &MockDatabase{},
			repost:      true,
			wantWritten: true,
			wantApplies: true,
		},
		{
			name:        "fetch error fails open",
			db:          &MockDatabase{},
			fetchErr:    errors.New("strava 503"),
			wantApplies: true,
			wantFetch:   1,
		},
		{
			name:   "non-Strava source is untouched",
			db:     &MockDatabase{},
			source: pbactivity.ActivitySource_SOURCE_HEVY,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := NewOrchestrator(tc.db, &MockBlobStore{}, "b")
			f := &fetchRecorder{desc: roxfitText, err: tc.fetchErr}
			o.fetchSourceDescription = f.fetch
			p := stravaPayload("")
			p.IsRepost = tc.repost
			if tc.source != pbactivity.ActivitySource_SOURCE_UNSPECIFIED {
				p.Source = tc.source
			}
			written, applies := o.refreshSourceDescription(context.Background(), slog.Default(), p, "exec-1", tc.run)
			if written != tc.wantWritten || applies != tc.wantApplies {
				t.Errorf("written,applies = %v,%v want %v,%v", written, applies, tc.wantWritten, tc.wantApplies)
			}
			if got := p.StandardizedActivity.Description; got != tc.wantDesc {
				t.Errorf("description = %q, want %q", got, tc.wantDesc)
			}
			if f.calls != tc.wantFetch {
				t.Errorf("fetch calls = %d, want %d", f.calls, tc.wantFetch)
			}
		})
	}
}

// End to end through Process: an empty ingested description (ROXFIT wrote after the
// webhook) becomes slot 0, the user's own text and enricher sections follow, the
// persisted payload carries the refreshed text, and the destination is told FitGlue
// has never written to the activity.
func TestProcess_RefreshesStravaSourceDescriptionIntoSlotZero(t *testing.T) {
	mockDB := &MockDatabase{
		GetUserFunc: func(_ context.Context, id string) (*user.Record, error) {
			return &user.Record{UserProfile: &pbuser.UserProfile{UserId: id}}, nil
		},
		GetUserPipelinesFunc: func(context.Context, string) ([]*pbpipeline.PipelineConfig, error) {
			return []*pbpipeline.PipelineConfig{{
				Id:           "pipeline-run",
				Source:       "SOURCE_STRAVA",
				Destinations: []pbplugin.DestinationType{pbplugin.DestinationType_DESTINATION_STRAVA},
				Enrichers:    []*pbpipeline.EnricherConfig{{ProviderType: pbplugin.EnricherProviderType_ENRICHER_PROVIDER_MOCK}},
			}}, nil
		},
	}
	var persisted string
	store := &MockBlobStore{WriteFunc: func(_ context.Context, _, object string, data []byte) error {
		if strings.HasPrefix(object, "payloads/") {
			persisted = string(data)
		}
		return nil
	}}
	o := NewOrchestrator(mockDB, store, "test-bucket")
	f := &fetchRecorder{desc: roxfitText}
	o.fetchSourceDescription = f.fetch

	var seenByEnricher string
	o.Register(&MockProvider{
		NameFunc: func() string { return "mock-enricher" },
		EnrichFunc: func(_ context.Context, _ *slog.Logger, a *pbactivity.StandardizedActivity, _ *user.Record, _ map[string]string, _ bool) (*providers.EnrichmentResult, error) {
			seenByEnricher = a.Description
			return &providers.EnrichmentResult{Description: "Brutal sleds, great partner."}, nil
		},
	})

	res, err := o.Process(context.Background(), slog.Default(), stravaPayload(""), "parent", "exec-1", false)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events = %d", len(res.Events))
	}
	ev := res.Events[0]
	want := roxfitText + "\n\nBrutal sleds, great partner."
	if ev.Description != want {
		t.Errorf("description =\n%q\nwant\n%q", ev.Description, want)
	}
	if seenByEnricher != roxfitText {
		t.Errorf("enrichers should see the refreshed description, saw %q", seenByEnricher)
	}
	if !strings.Contains(persisted, "Split Times") {
		t.Errorf("persisted original payload should carry the refreshed description")
	}
	if ev.EnrichmentMetadata[description.MetadataSourceWrittenByFitGlue] != "false" {
		t.Errorf("%s = %q, want false", description.MetadataSourceWrittenByFitGlue, ev.EnrichmentMetadata[description.MetadataSourceWrittenByFitGlue])
	}
	if ev.EnrichmentMetadata["same_source_destination_strava"] != "true" {
		t.Errorf("expected same-source strava flag")
	}
}

func TestFetchStravaDescription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/activities/1":
			w.Write([]byte(`{"name":"x","description":"Completed via ROXFIT"}`))
		case "/activities/2":
			w.Write([]byte(`{"name":"x","description":null}`))
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	if d, err := fetchStravaDescription(ctx, srv.Client(), srv.URL, "1"); err != nil || d != "Completed via ROXFIT" {
		t.Errorf("1: %q, %v", d, err)
	}
	if d, err := fetchStravaDescription(ctx, srv.Client(), srv.URL, "2"); err != nil || d != "" {
		t.Errorf("2: %q, %v", d, err)
	}
	if _, err := fetchStravaDescription(ctx, srv.Client(), srv.URL, "3"); err == nil {
		t.Error("3: expected an error on 404")
	}
}
