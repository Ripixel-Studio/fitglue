package reconcile

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 3, 21, 30, 0, 0, time.UTC)

func at(daysAgo int, hour, min int) time.Time {
	d := now.AddDate(0, 0, -daysAgo)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, time.UTC)
}

func snapshot() Snapshot {
	return Snapshot{Since: now.AddDate(0, 0, -10), Now: now, HevyListed: true, StravaListed: true, Uploaded: map[string]string{}}
}

func hevyW(id, title string, start time.Time, mins int) HevyWorkout {
	return HevyWorkout{ID: id, Title: title, Start: start, End: start.Add(time.Duration(mins) * time.Minute)}
}

func stravaA(id, name, sport string, start time.Time, mins int, file string) StravaActivity {
	return StravaActivity{ID: id, Name: name, SportType: sport, Start: start, Elapsed: time.Duration(mins) * time.Minute, UploadFile: file}
}

// hevyRun is the run FitGlue makes for a Hevy workout; stravaID "" and
// status "" mean the Strava upload has no id yet.
func hevyRun(runID, hevyID, title string, start time.Time, status string, strava *Dest) Run {
	return Run{ID: runID, ActivityID: "act-" + runID, Source: "SOURCE_HEVY", SourceID: hevyID, Title: title,
		Type: "ACTIVITY_TYPE_PILATES", Status: status, Start: start, Created: start.Add(time.Hour), Updated: start.Add(time.Hour),
		Strava: strava, Hevy: &Dest{Status: "DESTINATION_STATUS_SUCCESS"}}
}

func stravaRun(runID, stravaID, title, typ string, start time.Time, status string, hevy *Dest) Run {
	return Run{ID: runID, ActivityID: "act-" + runID, Source: "SOURCE_STRAVA", SourceID: stravaID, Title: title,
		Type: typ, Status: status, Start: start, Created: start.Add(time.Hour), Updated: start.Add(time.Hour),
		Strava: &Dest{Status: "DESTINATION_STATUS_SUCCESS"}, Hevy: hevy}
}

func ok(id string) *Dest { return &Dest{Status: "DESTINATION_STATUS_SUCCESS", ExternalID: id} }

const synced = "PIPELINE_RUN_STATUS_SYNCED"

// row renders a session's three cells as "HFS" marks, e.g. "✓✓✗".
func row(s *Session) string { return mark(s.Hevy) + mark(s.FitGlue) + mark(s.Strava) }

func kinds(s *Session) []string {
	var k []string
	for _, i := range s.Issues {
		k = append(k, i.Kind+"/"+i.Status)
	}
	return k
}

func only(t *testing.T, rep *Report) *Session {
	t.Helper()
	if len(rep.Sessions) != 1 {
		var b strings.Builder
		WriteText(&b, rep, time.UTC)
		t.Fatalf("want 1 session, got %d:\n%s", len(rep.Sessions), b.String())
	}
	return rep.Sessions[0]
}

func expect(t *testing.T, s *Session, marks string, wantKinds ...string) {
	t.Helper()
	if got := row(s); got != marks {
		t.Errorf("%q: cells %s, want %s", s.Title, got, marks)
	}
	got := kinds(s)
	if strings.Join(got, ",") != strings.Join(wantKinds, ",") {
		t.Errorf("%q: issues %v, want %v", s.Title, got, wantKinds)
	}
}

// --- Hevy-origin sessions: H → F → S -------------------------------------

func TestHevy_AllThree(t *testing.T) {
	sn := snapshot()
	st := at(1, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Tower Pilates", st, 50)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Tower Pilates", st, synced, ok("s1"))}
	sn.Strava = []StravaActivity{stravaA("s1", "Tower Pilates", "Pilates", st, 50, "activity.fit")}
	s := only(t, Plan(sn))
	expect(t, s, "✓✓✓")
	if s.Origin != "hevy" || s.Strava.Note != "FitGlue upload" {
		t.Errorf("origin %s note %q", s.Origin, s.Strava.Note)
	}
}

func TestHevy_FitGlueNoStrava_RepostsUpload(t *testing.T) {
	sn := snapshot()
	st := at(2, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, "PIPELINE_RUN_STATUS_PARTIAL", &Dest{Status: "DESTINATION_STATUS_FAILED"})}
	s := only(t, Plan(sn))
	expect(t, s, "✓✓✗", "strava_upload_missing/fixable")
	a := s.Issues[0].Action
	if a.Kind != ActRepostStrava || a.Target != "act-r1" || a.Mode != "retry-destination" {
		t.Errorf("action %+v", a)
	}
}

func TestHevy_FitGlueNoStrava_UploadDeletedOnStrava(t *testing.T) {
	sn := snapshot()
	st := at(2, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, synced, ok("s-gone"))}
	expect(t, only(t, Plan(sn)), "✓✓✗", "strava_copy_deleted/report")
}

func TestHevy_FitGlueNoStrava_RunStillRunning(t *testing.T) {
	sn := snapshot()
	st := now.Add(-3 * time.Hour)
	r := hevyRun("r1", "h1", "Pilates", st, "PIPELINE_RUN_STATUS_RUNNING", &Dest{Status: "DESTINATION_STATUS_PENDING"})
	r.Updated = now.Add(-10 * time.Minute)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Runs = []Run{r}
	expect(t, only(t, Plan(sn)), "✓✓✗", "in_progress/in_progress")
}

func TestHevy_FitGlueNoStrava_WaitingOnPendingInput(t *testing.T) {
	sn := snapshot()
	st := at(2, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, "PIPELINE_RUN_STATUS_PENDING", &Dest{Status: "DESTINATION_STATUS_PENDING"})}
	expect(t, only(t, Plan(sn)), "✓✓✗", "in_progress/in_progress")
}

func TestHevy_PipelineWithoutStrava_IsNotApplicable(t *testing.T) {
	sn := snapshot()
	st := at(2, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, synced, nil)}
	expect(t, only(t, Plan(sn)), "✓✓–")
}

func TestHevy_StravaUploadLinkedByTime(t *testing.T) {
	// The run never recorded the Strava id (outcome still PENDING), but
	// FitGlue's file is on Strava at the same time: one session, all ✓.
	sn := snapshot()
	st := at(2, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, synced, &Dest{Status: "DESTINATION_STATUS_PENDING"})}
	sn.Strava = []StravaActivity{stravaA("s1", "Pilates", "Pilates", st.Add(30*time.Second), 50, "activity.fit")}
	expect(t, only(t, Plan(sn)), "✓✓✓")
}

func TestHevy_StravaUploadLinkedByUploadedRecord(t *testing.T) {
	sn := snapshot()
	st := at(2, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, synced, &Dest{Status: "DESTINATION_STATUS_PENDING"})}
	sn.Strava = []StravaActivity{stravaA("s1", "Pilates", "Pilates", st.Add(10*time.Minute), 50, "")}
	sn.Uploaded["strava:s1"] = "SOURCE_HEVY:h1"
	expect(t, only(t, Plan(sn)), "✓✓✓")
}

func TestHevy_MissingFromFitGlueAndStrava_Replays(t *testing.T) {
	sn := snapshot()
	sn.Hevy = []HevyWorkout{hevyW("h1", "Tower Pilates", at(1, 8, 0), 50)}
	s := only(t, Plan(sn))
	expect(t, s, "✓✗✗", "missing_in_fitglue/fixable")
	if a := s.Issues[0].Action; a.Kind != ActReplayHevy || a.Target != "h1" {
		t.Errorf("action %+v", a)
	}
}

func TestHevy_MissingFromFitGlue_TooRecent(t *testing.T) {
	sn := snapshot()
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", now.Add(-time.Hour), 50)}
	expect(t, only(t, Plan(sn)), "✓✗✗", "in_progress/in_progress")
}

func TestHevy_MissingFromFitGlue_StravaUploadExists(t *testing.T) {
	// FitGlue's upload is on Strava but the run is gone: replay (the Strava
	// uploader recovers the duplicate FIT), and the upload stays matched.
	sn := snapshot()
	st := at(3, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Strava = []StravaActivity{stravaA("s1", "Pilates", "Pilates", st, 50, "activity.fit")}
	sn.Uploaded["strava:s1"] = "SOURCE_HEVY:h1"
	expect(t, only(t, Plan(sn)), "✓✗✓", "missing_in_fitglue/fixable")
}

func TestHevy_MissingFromFitGlue_WatchAlsoRecordedIt(t *testing.T) {
	// Hevy log + a Garmin strength recording, neither in FitGlue: probable
	// duplicate across sources; nothing is replayed.
	sn := snapshot()
	st := at(3, 10, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Lower Body PT", st, 60)}
	sn.Strava = []StravaActivity{stravaA("g1", "Strength", "WeightTraining", st.Add(time.Minute), 58, "garmin_ping_1.fit")}
	rep := Plan(sn)
	if len(rep.Sessions) != 2 {
		t.Fatalf("sessions %d", len(rep.Sessions))
	}
	for _, s := range rep.Sessions {
		for _, i := range s.Issues {
			if i.Action != nil && i.Status == StatusFixable {
				t.Errorf("%q: should not replay an ambiguous session: %+v", s.Title, i)
			}
		}
	}
	var g *Session
	for _, s := range rep.Sessions {
		if s.Origin == "strava" {
			g = s
		}
	}
	if g == nil || !strings.Contains(strings.Join(kinds(g), ","), "cross_source_duplicate/report") {
		t.Errorf("garmin session issues %v", kinds(g))
	}
}

func TestHevy_DeletedOnHevy(t *testing.T) {
	sn := snapshot()
	st := at(2, 8, 0)
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, synced, ok("s1"))}
	sn.Strava = []StravaActivity{stravaA("s1", "Pilates", "Pilates", st, 50, "activity.fit")}
	expect(t, only(t, Plan(sn)), "✗✓✓", "source_deleted/report")
}

func TestHevy_DeletedOnHevyAndStrava(t *testing.T) {
	sn := snapshot()
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", at(2, 8, 0), synced, ok("s1"))}
	expect(t, only(t, Plan(sn)), "✗✓✗", "source_deleted/report", "strava_copy_deleted/report")
}

func TestOrphan_StravaUploadWithNoRun(t *testing.T) {
	sn := snapshot()
	sn.Strava = []StravaActivity{stravaA("s1", "Pilates", "Pilates", at(2, 8, 0), 50, "activity.fit")}
	sn.Uploaded["strava:s1"] = "SOURCE_HEVY:h-gone"
	expect(t, only(t, Plan(sn)), "✗✗✓", "orphan_upload/report")
}

// --- Strava-origin sessions: S → F → (Hevy copy) ---------------------------

func TestStrava_AllThree(t *testing.T) {
	sn := snapshot()
	st := at(4, 8, 0)
	sn.Strava = []StravaActivity{stravaA("s1", "Parkrun", "Run", st, 25, "garmin_ping_1.fit")}
	sn.Runs = []Run{stravaRun("r1", "s1", "Parkrun", "ACTIVITY_TYPE_RUN", st, synced, ok("hc1"))}
	sn.Hevy = []HevyWorkout{hevyW("hc1", "Parkrun", st, 25)}
	s := only(t, Plan(sn))
	expect(t, s, "✓✓✓")
	if s.Origin != "strava" || s.Hevy.Note != "FitGlue copy" {
		t.Errorf("origin %s hevy note %q", s.Origin, s.Hevy.Note)
	}
}

func TestStrava_NoHevyCopyConfigured(t *testing.T) {
	sn := snapshot()
	st := at(4, 8, 0)
	sn.Strava = []StravaActivity{stravaA("s1", "Run", "Run", st, 25, "garmin_ping_1.fit")}
	sn.Runs = []Run{stravaRun("r1", "s1", "Run", "ACTIVITY_TYPE_RUN", st, synced, nil)}
	expect(t, only(t, Plan(sn)), "–✓✓")
}

func TestStrava_HevyCopyDeleted(t *testing.T) {
	sn := snapshot()
	st := at(4, 8, 0)
	sn.Strava = []StravaActivity{stravaA("s1", "Run", "Run", st, 25, "garmin_ping_1.fit")}
	sn.Runs = []Run{stravaRun("r1", "s1", "Run", "ACTIVITY_TYPE_RUN", st, synced, ok("hc-gone"))}
	expect(t, only(t, Plan(sn)), "✗✓✓", "hevy_copy_deleted/report")
}

func TestStrava_HevyCopyFailed(t *testing.T) {
	sn := snapshot()
	st := at(4, 8, 0)
	sn.Strava = []StravaActivity{stravaA("s1", "Run", "Run", st, 25, "garmin_ping_1.fit")}
	sn.Runs = []Run{stravaRun("r1", "s1", "Run", "ACTIVITY_TYPE_RUN", st, "PIPELINE_RUN_STATUS_PARTIAL", &Dest{Status: "DESTINATION_STATUS_FAILED"})}
	expect(t, only(t, Plan(sn)), "✗✓✓", "hevy_upload_failed/report")
}

func TestStrava_MissingFromFitGlue_Replays(t *testing.T) {
	sn := snapshot()
	sn.Strava = []StravaActivity{stravaA("s1", "Hyrox", "Crossfit", at(5, 8, 20), 75, "roxfit.fit")}
	s := only(t, Plan(sn))
	expect(t, s, "–✗✓", "missing_in_fitglue/fixable")
	if a := s.Issues[0].Action; a.Kind != ActReplayStrava || a.Target != "s1" {
		t.Errorf("action %+v", a)
	}
}

func TestStrava_MissingFromFitGlue_HevyCopyExists(t *testing.T) {
	sn := snapshot()
	st := at(5, 8, 0)
	sn.Strava = []StravaActivity{stravaA("s1", "Run", "Run", st, 25, "garmin_ping_1.fit")}
	sn.Hevy = []HevyWorkout{hevyW("hc1", "Run", st, 25)}
	sn.Uploaded["hevy:hc1"] = "SOURCE_STRAVA:s1"
	expect(t, only(t, Plan(sn)), "✓✗✓", "missing_in_fitglue/fixable")
}

func TestStrava_DeletedOnStrava(t *testing.T) {
	// The Garmin "Morning Run" James deleted on Strava; FitGlue's run was cancelled.
	sn := snapshot()
	sn.Runs = []Run{stravaRun("r1", "s1", "Morning Run", "ACTIVITY_TYPE_RUN", at(6, 8, 20), "PIPELINE_RUN_STATUS_CANCELLED", &Dest{Status: "DESTINATION_STATUS_PENDING"})}
	expect(t, only(t, Plan(sn)), "–✓✗", "source_deleted/report")
}

func TestStrava_DeletedOnStrava_HevyCopyRemains(t *testing.T) {
	sn := snapshot()
	st := at(6, 8, 0)
	sn.Runs = []Run{stravaRun("r1", "s1", "Run", "ACTIVITY_TYPE_RUN", st, synced, ok("hc1"))}
	sn.Hevy = []HevyWorkout{hevyW("hc1", "Run", st, 25)}
	expect(t, only(t, Plan(sn)), "✓✓✗", "source_deleted/report")
}

func TestOrphan_HevyCopyWithNoRun(t *testing.T) {
	sn := snapshot()
	sn.Hevy = []HevyWorkout{hevyW("hc1", "Run", at(6, 8, 0), 25)}
	sn.Uploaded["hevy:hc1"] = "SOURCE_STRAVA:s-gone"
	expect(t, only(t, Plan(sn)), "✓✗✗", "orphan_upload/report")
}

func TestStrava_UploadOfHevyIsNotANewSession(t *testing.T) {
	// FitGlue's Strava upload of a Hevy workout matches back by id.
	sn := snapshot()
	st := at(2, 17, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Hyrox Class", st, 60)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Hyrox Class", st, synced, ok("s1"))}
	sn.Strava = []StravaActivity{stravaA("s1", "Hyrox Class", "Crossfit", st, 60, "activity.fit")}
	rep := Plan(sn)
	expect(t, only(t, rep), "✓✓✓")
	if len(rep.Actions) != 0 {
		t.Errorf("actions %+v", rep.Actions)
	}
}

// --- duplicates ------------------------------------------------------------

func TestDuplicate_SameSourceTwice(t *testing.T) {
	sn := snapshot()
	st := at(9, 8, 1)
	sn.Strava = []StravaActivity{stravaA("s1", "Newark Parkrun (#42)", "Run", st, 25, "garmin_ping_1.fit")}
	r1 := stravaRun("r-synced", "s1", "Newark Parkrun (#42)", "ACTIVITY_TYPE_RUN", st, synced, ok("hc1"))
	r2 := stravaRun("r-partial", "s1", "Newark Parkrun (#42)", "ACTIVITY_TYPE_RUN", st, "PIPELINE_RUN_STATUS_PARTIAL", &Dest{Status: "DESTINATION_STATUS_FAILED"})
	r2.Created = r1.Created.Add(2 * time.Minute)
	sn.Runs = []Run{r1, r2}
	sn.Hevy = []HevyWorkout{hevyW("hc1", "Newark Parkrun (#42)", st, 25)}
	s := only(t, Plan(sn))
	expect(t, s, "✓✓✓", "duplicate_same_source/report")
	if !strings.Contains(s.Issues[0].Detail, "recommend keeping r-synced") {
		t.Errorf("detail %q", s.Issues[0].Detail)
	}
}

func TestDuplicate_TwoActivitiesSameTime(t *testing.T) {
	// The Hyrox solo (ROXFIT) and the Garmin "Morning Run" of the same race.
	sn := snapshot()
	st := at(6, 8, 20)
	sn.Strava = []StravaActivity{stravaA("rox", "Hyrox Solo", "Crossfit", st.Add(2*time.Second), 76, "roxfit.fit")}
	sn.Runs = []Run{
		stravaRun("r-hyrox", "rox", "Hyrox Solo", "ACTIVITY_TYPE_CROSSFIT", st.Add(2*time.Second), synced, nil),
		stravaRun("r-run", "garmin", "Morning Run", "ACTIVITY_TYPE_RUN", st, "PIPELINE_RUN_STATUS_CANCELLED", nil),
	}
	rep := Plan(sn)
	if len(rep.Sessions) != 2 {
		t.Fatalf("sessions %d", len(rep.Sessions))
	}
	var run *Session
	for _, s := range rep.Sessions {
		if s.Title == "Morning Run" {
			run = s
		}
	}
	got := strings.Join(kinds(run), ",")
	if !strings.Contains(got, "duplicate_overlap/report") || !strings.Contains(got, "source_deleted/report") {
		t.Errorf("morning run issues %v", got)
	}
	if !strings.Contains(run.Issues[0].Detail+run.Issues[1].Detail, `recommend deleting "Morning Run"`) {
		t.Errorf("recommendation missing: %+v", run.Issues)
	}
}

func TestDuplicate_OverlapNeedsSameTypeWhenStartsDiffer(t *testing.T) {
	sn := snapshot()
	st := at(6, 8, 0)
	sn.Strava = []StravaActivity{
		stravaA("a", "Long run", "Run", st, 120, "garmin_ping_1.fit"),
		stravaA("b", "Ride", "Ride", st.Add(10*time.Minute), 60, "garmin_ping_2.fit"),
	}
	sn.Runs = []Run{
		stravaRun("ra", "a", "Long run", "ACTIVITY_TYPE_RUN", st, synced, nil),
		stravaRun("rb", "b", "Ride", "ACTIVITY_TYPE_RIDE", st.Add(10*time.Minute), synced, nil),
	}
	for _, s := range Plan(sn).Sessions {
		if len(s.Issues) != 0 {
			t.Errorf("%q: %v", s.Title, kinds(s))
		}
	}
}

func TestDuplicate_MissingOverlapIsNotReplayed(t *testing.T) {
	// Two Strava recordings of one session, neither in FitGlue: replaying
	// both would put the duplicate into FitGlue too.
	sn := snapshot()
	st := at(4, 8, 20)
	sn.Strava = []StravaActivity{
		stravaA("rox", "Hyrox", "Crossfit", st, 76, "roxfit.fit"),
		stravaA("gar", "Morning Run", "Run", st.Add(time.Second), 76, "garmin_ping_1.fit"),
	}
	rep := Plan(sn)
	if len(rep.Actions) != 0 || rep.Counts.NeedsAttention != 2 {
		t.Errorf("actions %+v needs %d", rep.Actions, rep.Counts.NeedsAttention)
	}
}

// --- unknown sources and the window ----------------------------------------

func TestHevyNotListed_ConcludesNothingFromIt(t *testing.T) {
	sn := snapshot()
	sn.HevyListed = false
	st := at(2, 8, 0)
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, synced, ok("s1"))}
	sn.Strava = []StravaActivity{stravaA("s1", "Pilates", "Pilates", st, 50, "activity.fit")}
	rep := Plan(sn)
	expect(t, only(t, rep), "?✓✓")
	if len(rep.Warnings) == 0 {
		t.Error("want a warning")
	}
}

func TestStravaNotListed_NoUploadRepost(t *testing.T) {
	sn := snapshot()
	sn.StravaListed = false
	st := at(2, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Pilates", st, 50)}
	sn.Runs = []Run{hevyRun("r1", "h1", "Pilates", st, synced, &Dest{Status: "DESTINATION_STATUS_FAILED"})}
	expect(t, only(t, Plan(sn)), "✓✓?")
}

func TestOutsideWindowNotReported(t *testing.T) {
	sn := snapshot()
	sn.Hevy = []HevyWorkout{hevyW("h1", "Old", sn.Since.Add(-2*time.Hour), 50)}
	if rep := Plan(sn); len(rep.Sessions) != 0 {
		t.Errorf("sessions %d", len(rep.Sessions))
	}
}

// --- Apply -----------------------------------------------------------------

type fakeApplier struct {
	calls []string
	fail  error
}

func (f *fakeApplier) ReplayHevy(_ context.Context, id string) error {
	f.calls = append(f.calls, "hevy:"+id)
	return f.fail
}
func (f *fakeApplier) ReplayStrava(_ context.Context, id string) error {
	f.calls = append(f.calls, "strava:"+id)
	return f.fail
}
func (f *fakeApplier) RepostStrava(_ context.Context, id, mode string) error {
	f.calls = append(f.calls, "repost:"+id+":"+mode)
	return f.fail
}

func missingEverywhere() Snapshot {
	sn := snapshot()
	st := at(2, 8, 0)
	sn.Hevy = []HevyWorkout{hevyW("h1", "Tower Pilates", st, 50), hevyW("h2", "PT", at(3, 10, 0), 60)}
	sn.Runs = []Run{hevyRun("r2", "h2", "PT", at(3, 10, 0), synced, &Dest{Status: "DESTINATION_STATUS_FAILED"})}
	sn.Strava = []StravaActivity{stravaA("s9", "Hyrox", "Crossfit", at(4, 8, 0), 75, "roxfit.fit")}
	return sn
}

func TestApply_DryRunChangesNothing(t *testing.T) {
	rep := Plan(missingEverywhere())
	ap := &fakeApplier{}
	st := &State{Attempts: map[string]*Attempt{}}
	Apply(context.Background(), rep, ap, st, now, false)
	if len(ap.calls) != 0 || len(st.Attempts) != 0 {
		t.Fatalf("dry run acted: %v %v", ap.calls, st.Attempts)
	}
	if rep.Counts.Fixable != 3 || rep.Applied {
		t.Errorf("counts %+v", rep.Counts)
	}
}

func TestApply_FixesThenIsIdempotent(t *testing.T) {
	ap := &fakeApplier{}
	st := &State{Attempts: map[string]*Attempt{}}
	rep := Plan(missingEverywhere())
	Apply(context.Background(), rep, ap, st, now, true)
	want := "hevy:h1,repost:act-r2:retry-destination,strava:s9"
	if got := strings.Join(sortedCopy(ap.calls), ","); got != want {
		t.Fatalf("calls %s, want %s", got, want)
	}
	if rep.Counts.Applied != 3 || len(rep.Fixed) != 3 || rep.Counts.NeedsAttention != 0 {
		t.Errorf("counts %+v fixed %v", rep.Counts, rep.Fixed)
	}

	// Pipeline has not caught up yet: the same snapshot again changes nothing.
	ap2 := &fakeApplier{}
	rep2 := Plan(missingEverywhere())
	Apply(context.Background(), rep2, ap2, st, now.Add(10*time.Minute), true)
	if len(ap2.calls) != 0 {
		t.Fatalf("second run acted: %v", ap2.calls)
	}
	if rep2.Counts.Awaiting != 3 || rep2.Counts.NeedsAttention != 0 {
		t.Errorf("second run counts %+v", rep2.Counts)
	}

	// After the cooldown, once more; then reported as unfixed.
	ap3 := &fakeApplier{}
	later := now.Add(Cooldown + time.Minute)
	Apply(context.Background(), Plan(missingEverywhere()), ap3, st, later, true)
	if len(ap3.calls) != 3 {
		t.Fatalf("retry calls %v", ap3.calls)
	}
	ap4 := &fakeApplier{}
	rep4 := Plan(missingEverywhere())
	Apply(context.Background(), rep4, ap4, st, later.Add(Cooldown+time.Minute), true)
	if len(ap4.calls) != 0 || rep4.Counts.NeedsAttention != 3 {
		t.Fatalf("after max attempts: calls %v counts %+v", ap4.calls, rep4.Counts)
	}
}

func TestApply_FailureNeedsAttention(t *testing.T) {
	ap := &fakeApplier{fail: errors.New("hevy api error: status=404")}
	rep := Plan(missingEverywhere())
	Apply(context.Background(), rep, ap, &State{Attempts: map[string]*Attempt{}}, now, true)
	if rep.Counts.NeedsAttention != 3 || rep.Counts.Applied != 0 {
		t.Errorf("counts %+v", rep.Counts)
	}
	if !strings.Contains(rep.NeedsAttention[0], "fix failed") {
		t.Errorf("needs attention %v", rep.NeedsAttention)
	}
}

func TestState_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	st, err := LoadState(path)
	if err != nil || len(st.Attempts) != 0 {
		t.Fatalf("empty load: %v %v", st, err)
	}
	st.Attempts["replay_hevy:h1"] = &Attempt{Count: 1, Last: now}
	st.Attempts["old"] = &Attempt{Count: 1, Last: now.AddDate(0, -2, 0)}
	if err := st.Save(path, now); err != nil {
		t.Fatal(err)
	}
	back, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Attempts) != 1 || back.Attempts["replay_hevy:h1"].Count != 1 {
		t.Errorf("round trip %+v", back.Attempts)
	}
}

func TestWriteText_Table(t *testing.T) {
	rep := Plan(missingEverywhere())
	Apply(context.Background(), rep, &fakeApplier{}, &State{Attempts: map[string]*Attempt{}}, now, false)
	var b strings.Builder
	WriteText(&b, rep, time.UTC)
	out := b.String()
	for _, want := range []string{"HEVY", "FITGLUE", "STRAVA", "Tower Pilates", "✓ h1", "✗", "would replay from Hevy", "3 fixable"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
