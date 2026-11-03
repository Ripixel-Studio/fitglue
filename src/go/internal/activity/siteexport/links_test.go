package siteexport

import (
	"context"
	"errors"
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
	l := BuildLinks(linkShowcase(pbactivity.ActivitySource_SOURCE_STRAVA, "12345678"), "")
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
	svc := stravaIDService(pbpipeline.DestinationStatus_DESTINATION_STATUS_PENDING, "")
	id, err := svc.StravaID(context.Background(), sc)
	if err != nil || id != "" {
		t.Fatalf("pending outcome: %q %v", id, err)
	}
	l := BuildLinks(sc, id)
	if l == nil || l.Hevy != "https://hevy.com/workout/abc-1" || l.Strava != "" {
		t.Fatalf("got %+v", l)
	}
	ok := stravaIDService(pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS, "777")
	if id, _ := ok.StravaID(context.Background(), sc); id != "777" {
		t.Fatalf("success outcome id = %q", id)
	}
}

func TestLinksBadIDOmitted(t *testing.T) {
	if l := BuildLinks(linkShowcase(pbactivity.ActivitySource_SOURCE_STRAVA, "12a/../x"), ""); l != nil {
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

func stravaIDService(st pbpipeline.DestinationStatus, ext string) ServiceSource {
	store := &runStore{run: &pbpipeline.PipelineRun{Destinations: []*pbpipeline.DestinationOutcome{
		{Destination: pbplugin.DestinationType_DESTINATION_HEVY, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS, ExternalId: ptr("h")},
		{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: st, ExternalId: ptr(ext)},
	}}}
	return ServiceSource{Store: store}
}

type runStore struct {
	activity.ActivityStore
	run *pbpipeline.PipelineRun
}

func (s *runStore) GetPublicShowcase(context.Context, string) (*pbactivity.ShowcasedActivity, string, error) {
	return &pbactivity.ShowcasedActivity{}, "uid", nil
}
func (s *runStore) GetPipelineRun(_ context.Context, uid, run string) (*pbpipeline.PipelineRun, error) {
	if uid != "uid" || run != "run-1" {
		return nil, nil
	}
	return s.run, nil
}
func ptr(s string) *string { return &s }
