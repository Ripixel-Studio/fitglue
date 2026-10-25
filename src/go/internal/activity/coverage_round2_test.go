// nolint:proto-json
package activity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- decodeProtoMap / encodeProtoMap round-trip ---

func TestActivityEncodeDecodeProtoMap(t *testing.T) {
	in := &pbactivity.ShowcasedActivity{ShowcaseId: "s1", Title: "Morning Run"}
	m, err := encodeProtoMap(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if m["showcase_id"] != "s1" {
		t.Errorf("expected showcase_id s1, got %v", m["showcase_id"])
	}
	var out pbactivity.ShowcasedActivity
	if err := decodeProtoMap(m, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ShowcaseId != "s1" || out.Title != "Morning Run" {
		t.Errorf("round-trip mismatch: %+v", &out)
	}

	// Unknown keys are discarded, not errors.
	m["bogus"] = "x"
	var out2 pbactivity.ShowcasedActivity
	if err := decodeProtoMap(m, &out2); err != nil {
		t.Fatalf("decode with unknown key: %v", err)
	}
}

// --- generateRoundup error/disabled/rich branches ---

func TestGenerateRoundup_ProfileError(t *testing.T) {
	store := &MockActivityStore{
		GetShowcasePreferencesFunc: func(_ context.Context, _ string) (*pbactivity.ShowcaseProfile, error) {
			return nil, errors.New("profile boom")
		},
	}
	svc := newTestSvc(store, &MockBlobStore{})
	_, err := svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, time.Now(), time.Now())
	if err == nil {
		t.Fatal("expected error when profile load fails")
	}
}

func TestGenerateRoundup_NoSettings(t *testing.T) {
	store := &MockActivityStore{
		GetShowcasePreferencesFunc: func(_ context.Context, _ string) (*pbactivity.ShowcaseProfile, error) {
			return &pbactivity.ShowcaseProfile{Slug: "athlete", RoundupSettings: nil}, nil
		},
	}
	svc := newTestSvc(store, &MockBlobStore{})
	r, err := svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, time.Now(), time.Now())
	if err != nil || r != nil {
		t.Fatalf("expected nil,nil when no settings, got %v,%v", r, err)
	}
}

func TestGenerateRoundup_EntriesError(t *testing.T) {
	store := &MockActivityStore{
		GetShowcasePreferencesFunc: func(_ context.Context, _ string) (*pbactivity.ShowcaseProfile, error) {
			return &pbactivity.ShowcaseProfile{Slug: "athlete", RoundupSettings: &pbactivity.RoundupSettings{EnabledMonthly: true}}, nil
		},
		ListShowcaseEntriesInRangeFunc: func(_ context.Context, _ string, _, _ time.Time) ([]*pbactivity.ShowcaseProfileEntry, error) {
			return nil, errors.New("entries boom")
		},
	}
	svc := newTestSvc(store, &MockBlobStore{})
	_, err := svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH, time.Now(), time.Now())
	if err == nil {
		t.Fatal("expected error when listing entries fails")
	}
}

func TestGenerateRoundup_MonthlyAndYearlyDisabled(t *testing.T) {
	for _, pt := range []pbactivity.RoundupPeriodType{
		pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH,
		pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_YEAR,
	} {
		store := &MockActivityStore{
			GetShowcasePreferencesFunc: func(_ context.Context, _ string) (*pbactivity.ShowcaseProfile, error) {
				// All toggles off.
				return &pbactivity.ShowcaseProfile{Slug: "athlete", RoundupSettings: &pbactivity.RoundupSettings{}}, nil
			},
		}
		svc := newTestSvc(store, &MockBlobStore{})
		r, err := svc.generateRoundup(context.Background(), "u1", pt, time.Now(), time.Now())
		if err != nil || r != nil {
			t.Fatalf("%v: expected nil,nil when disabled, got %v,%v", pt, r, err)
		}
	}
}

// TestGenerateRoundup_RichHighlights drives more of the highlight/PR/elevation
// branches with multiple entries.
func TestGenerateRoundup_RichHighlights(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	cal1, cal2 := int32(500), int32(900)
	elev := float64(300)
	avg1, avg2 := int32(150), int32(165)

	store := &MockActivityStore{
		GetShowcasePreferencesFunc: func(_ context.Context, _ string) (*pbactivity.ShowcaseProfile, error) {
			return &pbactivity.ShowcaseProfile{
				Slug:            "athlete",
				DisplayName:     "Athlete",
				RoundupSettings: &pbactivity.RoundupSettings{EnabledWeekly: true},
			}, nil
		},
		ListShowcaseEntriesInRangeFunc: func(_ context.Context, _ string, _, _ time.Time) ([]*pbactivity.ShowcaseProfileEntry, error) {
			return []*pbactivity.ShowcaseProfileEntry{
				{
					ShowcaseId: "s1", Title: "Run", ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
					DurationSeconds: 3600, DistanceMeters: 10000, CaloriesKcal: &cal1, AvgHeartRate: &avg1,
					ElevationGainM: &elev, StartTime: timestamppb.New(start.AddDate(0, 0, 1)),
				},
				{
					ShowcaseId: "s2", Title: "Long Ride", ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_RIDE,
					DurationSeconds: 7200, DistanceMeters: 40000, CaloriesKcal: &cal2, AvgHeartRate: &avg2,
					StartTime: timestamppb.New(start.AddDate(0, 0, 2)),
				},
			}, nil
		},
		ListUserPersonalRecordsFunc: func(_ context.Context, _ string) ([]*pbactivity.ShowcaseTopPR, error) {
			return []*pbactivity.ShowcaseTopPR{
				{AchievedAt: timestamppb.New(start.AddDate(0, 0, 3))},  // in range
				{AchievedAt: timestamppb.New(start.AddDate(0, -1, 0))}, // out of range
				{AchievedAt: nil}, // skipped
			}, nil
		},
	}
	svc := newTestSvc(store, &MockBlobStore{})
	r, err := svc.generateRoundup(context.Background(), "u1", pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil || r.TotalActivities != 2 {
		t.Fatalf("expected 2 activities, got %+v", r)
	}
	if len(r.PrsAchieved) != 1 {
		t.Errorf("expected 1 in-range PR, got %d", len(r.PrsAchieved))
	}
	if r.HighestAvgBpm != 165 {
		t.Errorf("expected highest avg bpm 165, got %d", r.HighestAvgBpm)
	}
}

// --- buildRunArchive with retained payload + FIT (addRunToZip success) ---

func TestBuildRunArchive_WithPayloadAndFit(t *testing.T) {
	run := &pbpipeline.PipelineRun{
		Id:                 "run-9",
		Title:              "Ride",
		OriginalPayloadUri: "gs://b/payload/run-9.json",
	}
	blob := &MockBlobStore{
		GetFunc: func(_ context.Context, _, uri string) ([]byte, error) {
			if strings.Contains(uri, "payload") {
				// payload carries the FIT pointer + a secret to redact.
				return []byte(`{"fitFileUri":"gs://b/fit/run-9.fit","client_secret":"SHH"}`), nil
			}
			if strings.Contains(uri, "fit") {
				return []byte("FITBYTES"), nil
			}
			return nil, errors.New("missing")
		},
	}
	svc := newTestSvc(&MockActivityStore{}, blob)
	data, err := svc.buildRunArchive(context.Background(), run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := zipEntryNames(t, data)
	for _, want := range []string{"run.json", "README.txt", "payload.json", "activity.fit"} {
		if !names[want] {
			t.Errorf("expected %q in per-run archive, got %v", want, names)
		}
	}
	if body := zipEntry(t, data, "payload.json"); strings.Contains(string(body), "SHH") {
		t.Error("expected client_secret redacted in payload.json")
	}
}

// --- HandleRoundupTrigger success (envelope + period bounds + generation) ---

func TestHandleRoundupTrigger_Success(t *testing.T) {
	// HandleRoundupTrigger fans out one goroutine per user, so the mock is
	// invoked concurrently — guard the shared slice to avoid a racy append
	// (concurrent appends can drop an entry, e.g. "got [u1]" for two users).
	var mu sync.Mutex
	var generatedFor []string
	store := &MockActivityStore{
		ListAllShowcaseUserIDsFunc: func(_ context.Context) ([]string, error) {
			return []string{"u1", "u2"}, nil
		},
		GetShowcasePreferencesFunc: func(_ context.Context, userID string) (*pbactivity.ShowcaseProfile, error) {
			mu.Lock()
			generatedFor = append(generatedFor, userID)
			mu.Unlock()
			// No settings → generateRoundup returns nil,nil quickly.
			return &pbactivity.ShowcaseProfile{Slug: userID, RoundupSettings: nil}, nil
		},
	}
	svc := newTestSvc(store, &MockBlobStore{})

	// Pub/Sub push envelope wrapping the trigger, with explicit period bounds.
	inner, _ := json.Marshal(roundupTriggerMessage{
		PeriodType:  "week",
		PeriodStart: "2024-01-01",
		PeriodEnd:   "2024-01-08",
	})
	envelope := map[string]interface{}{"message": map[string]interface{}{"data": inner}}
	body, _ := json.Marshal(envelope)

	rec := httptest.NewRecorder()
	svc.HandleRoundupTrigger(rec, httptest.NewRequest(http.MethodPost, "/pubsub/roundup", strings.NewReader(string(body))))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(generatedFor) != 2 {
		t.Errorf("expected generation attempted for 2 users, got %v", generatedFor)
	}
}

func TestHandleRoundupTrigger_BadBody(t *testing.T) {
	svc := newTestSvc(&MockActivityStore{}, &MockBlobStore{})
	rec := httptest.NewRecorder()
	svc.HandleRoundupTrigger(rec, httptest.NewRequest(http.MethodPost, "/pubsub/roundup", strings.NewReader("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unparseable body, got %d", rec.Code)
	}
}
