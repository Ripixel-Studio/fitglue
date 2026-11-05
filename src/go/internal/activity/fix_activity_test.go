package activity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
)

const leedsURI = "gs://assets/showcase_data/u1/leeds_data.json"

var leedsStart = time.Date(2026, 5, 10, 8, 23, 23, 0, time.UTC)

// leedsData is the Leeds Marathon as stored: a Hevy workout with one
// "Running" set holding the watch's distance, typed as weight training.
func leedsData() *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{
		Name:        "Leeds Marathon",
		Description: "I am still speechless that I actually signed up for this",
		Type:        pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		Sessions: []*pbactivity.Session{{
			TotalElapsedTime: 18751,
			Laps:             []*pbactivity.Lap{{TotalElapsedTime: 18751, Records: []*pbactivity.Record{{}, {}}}},
			StrengthSets: []*pbactivity.StrengthSet{
				{ExerciseName: "Running", DurationSeconds: 18751, DistanceMeters: 42726},
			},
		}},
	}
}

type leedsWorld struct {
	store   *MockActivityStore
	blob    *MockBlobStore
	written map[string][]byte
	doc     *pbactivity.ShowcasedActivity
	entry   *pbactivity.ShowcaseProfileEntry
	setDoc  *pbactivity.ShowcasedActivity
	setEnt  *pbactivity.ShowcaseProfileEntry
	saved   []string // roundup keys saved
}

func newLeedsWorld(t *testing.T) *leedsWorld {
	t.Helper()
	w := &leedsWorld{written: map[string][]byte{}}
	w.doc = &pbactivity.ShowcasedActivity{
		ShowcaseId: "leeds", Title: "Leeds Marathon", ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		StartTime: timestamppb.New(leedsStart), ActivityDataUri: leedsURI,
	}
	ev := &pbevents.EnrichedActivityEvent{ActivityData: leedsData(), Enrichments: &pbactivity.ActivityEnrichments{}}
	raw, err := protojson.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	w.written[leedsURI] = raw
	cal := int32(1823)
	w.entry = &pbactivity.ShowcaseProfileEntry{
		ShowcaseId: "leeds", Title: "Leeds Marathon", ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		StartTime: timestamppb.New(leedsStart), DurationSeconds: 18751, TotalSets: 1, CaloriesKcal: &cal,
	}
	w.blob = &MockBlobStore{
		GetFunc: func(_ context.Context, _, obj string) ([]byte, error) { return w.written[obj], nil },
		WriteFunc: func(_ context.Context, _, obj string, data []byte) error {
			w.written[obj] = data
			return nil
		},
	}
	w.store = &MockActivityStore{
		GetPublicShowcaseFunc: func(_ context.Context, id string) (*pbactivity.ShowcasedActivity, string, error) {
			if id != "leeds" {
				return nil, "", nil
			}
			return w.doc, "u1", nil
		},
		ListShowcaseProfileEntriesFunc: func(context.Context, string) ([]*pbactivity.ShowcaseProfileEntry, error) {
			return []*pbactivity.ShowcaseProfileEntry{w.entry}, nil
		},
		GetShowcasePreferencesFunc: func(context.Context, string) (*pbactivity.ShowcaseProfile, error) {
			return &pbactivity.ShowcaseProfile{UserId: "u1", Slug: "james", RoundupSettings: &pbactivity.RoundupSettings{EnabledWeekly: true, EnabledMonthly: true, EnabledYearly: true}}, nil
		},
		GetRoundupFunc: func(_ context.Context, _ string, key string) (*pbactivity.ShowcaseRoundup, error) {
			if key == "year-2026" {
				return nil, nil // no yearly roundup yet
			}
			return &pbactivity.ShowcaseRoundup{PeriodKey: key}, nil
		},
		UpdateShowcaseFunc: func(_ context.Context, _ string, sc *pbactivity.ShowcasedActivity) (*pbactivity.ShowcasedActivity, error) {
			w.setDoc = sc
			return sc, nil
		},
		SetShowcaseProfileEntryFunc: func(_ context.Context, _ string, e *pbactivity.ShowcaseProfileEntry) error {
			w.setEnt = e
			return nil
		},
		ListShowcaseEntriesInRangeFunc: func(context.Context, string, time.Time, time.Time) ([]*pbactivity.ShowcaseProfileEntry, error) {
			return []*pbactivity.ShowcaseProfileEntry{w.entry}, nil
		},
		SetRoundupFunc: func(_ context.Context, r *pbactivity.ShowcaseRoundup) error {
			w.saved = append(w.saved, r.PeriodKey)
			return nil
		},
	}
	return w
}

func TestPlanActivityFix_LeedsUsesRecordedDistance(t *testing.T) {
	w := newLeedsWorld(t)
	svc := newTestSvc(w.store, w.blob)
	plan, err := svc.PlanActivityFix(context.Background(), ActivityFix{ShowcaseID: "leeds", Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN, CaloriesKcal: 3802})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.written) != 1 || w.setDoc != nil || w.setEnt != nil || len(w.saved) != 0 {
		t.Fatal("planning must not write")
	}
	d := plan.Blob.ActivityData
	if d.Type != pbactivity.ActivityType_ACTIVITY_TYPE_RUN || d.Sessions[0].TotalDistance != 42726 || len(d.Sessions[0].StrengthSets) != 0 {
		t.Errorf("data = %v", d)
	}
	if d.Name != "Leeds Marathon" || !strings.HasPrefix(d.Description, "I am still speechless") {
		t.Error("his words must be untouched")
	}
	if d.Sessions[0].GetTotalCalories() != 3802 {
		t.Errorf("calories = %v", d.Sessions[0].GetTotalCalories())
	}
	if plan.Doc.ActivityType != pbactivity.ActivityType_ACTIVITY_TYPE_RUN {
		t.Error("showcase type not patched")
	}
	e := plan.Entry
	if e.ActivityType != pbactivity.ActivityType_ACTIVITY_TYPE_RUN || e.DistanceMeters != 42726 || e.TotalSets != 0 || e.GetCaloriesKcal() != 3802 {
		t.Errorf("entry = %v", e)
	}
	if strings.Join(plan.Roundups, ",") != "month-05-2026,week-19-2026" {
		t.Errorf("roundups = %v (only stored ones)", plan.Roundups)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "no GPS track") {
		t.Errorf("bare timestamp records are no track: %v", plan.Notes)
	}
	joined := ""
	for _, c := range plan.Changes {
		joined += c.String() + "\n"
	}
	for _, want := range []string{"activityType", "session.totalDistance", "0 m -> 42726 m", "strengthSets", "Running"} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes lack %q:\n%s", want, joined)
		}
	}
}

func TestPlanActivityFix_ExplicitDistanceWins(t *testing.T) {
	w := newLeedsWorld(t)
	svc := newTestSvc(w.store, w.blob)
	d := 42195.0
	plan, err := svc.PlanActivityFix(context.Background(), ActivityFix{ShowcaseID: "leeds", Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN, DistanceM: &d})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Entry.DistanceMeters != 42195 || plan.Blob.ActivityData.Sessions[0].TotalDistance != 42195 {
		t.Errorf("explicit distance ignored")
	}
	if plan.Entry.GetCaloriesKcal() != 1823 {
		t.Error("calories untouched without --calories-kcal")
	}
}

func TestPlanActivityFix_RefusesToDropLifting(t *testing.T) {
	w := newLeedsWorld(t)
	data := leedsData()
	data.Sessions[0].StrengthSets = append(data.Sessions[0].StrengthSets, &pbactivity.StrengthSet{ExerciseName: "Squat", Reps: 5, WeightKg: 100})
	raw, _ := protojson.Marshal(&pbevents.EnrichedActivityEvent{ActivityData: data})
	w.written[leedsURI] = raw
	svc := newTestSvc(w.store, w.blob)
	if _, err := svc.PlanActivityFix(context.Background(), ActivityFix{ShowcaseID: "leeds", Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}); err == nil || !strings.Contains(err.Error(), "Squat") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanActivityFix_NothingToDoAndMissing(t *testing.T) {
	w := newLeedsWorld(t)
	svc := newTestSvc(w.store, w.blob)
	if _, err := svc.PlanActivityFix(context.Background(), ActivityFix{ShowcaseID: "nope", Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}); err == nil {
		t.Error("missing showcase must error")
	}
	if _, err := svc.PlanActivityFix(context.Background(), ActivityFix{ShowcaseID: "leeds"}); !errors.Is(err, ErrNothingToFix) {
		t.Errorf("no-op fix: %v", err)
	}
}

func TestApplyActivityFix_WritesEverythingAndRecomputes(t *testing.T) {
	w := newLeedsWorld(t)
	svc := newTestSvc(w.store, w.blob)
	svc.AI = &fakeCompleter{out: "A marathon. Legs gone, heart full."}
	x := &recordingExporter{}
	svc.SetSiteExporter(x)
	plan, err := svc.PlanActivityFix(context.Background(), ActivityFix{ShowcaseID: "leeds", Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ApplyActivityFix(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	var ev pbevents.EnrichedActivityEvent
	if err := protojson.Unmarshal(w.written[leedsURI], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.ActivityData.Type != pbactivity.ActivityType_ACTIVITY_TYPE_RUN || ev.ActivityData.Sessions[0].TotalDistance != 42726 || ev.Enrichments == nil {
		t.Errorf("blob = %v (enrichments must survive)", &ev)
	}
	if w.setDoc == nil || w.setDoc.ActivityType != pbactivity.ActivityType_ACTIVITY_TYPE_RUN || w.setDoc.ActivityDataUri != leedsURI {
		t.Errorf("doc = %v", w.setDoc)
	}
	if w.setEnt == nil || w.setEnt.DistanceMeters != 42726 {
		t.Errorf("entry = %v", w.setEnt)
	}
	if strings.Join(w.saved, ",") != "month-05-2026,week-19-2026" {
		t.Errorf("roundups recomputed = %v", w.saved)
	}
	if len(x.activities) != 1 || x.activities[0] != "leeds" || len(x.roundups) != 2 {
		t.Errorf("site export: activities %v roundups %v", x.activities, x.roundups)
	}
}

func TestMislabelledRun(t *testing.T) {
	if !MislabelledRun(pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING, leedsData()) {
		t.Error("Leeds is a mislabelled run")
	}
	if MislabelledRun(pbactivity.ActivityType_ACTIVITY_TYPE_RUN, leedsData()) {
		t.Error("already a run")
	}
	lift := leedsData()
	lift.Sessions[0].StrengthSets = []*pbactivity.StrengthSet{{ExerciseName: "Bench Press", Reps: 5, WeightKg: 60}}
	if MislabelledRun(pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING, lift) {
		t.Error("a lift is not a run")
	}
	mixed := leedsData()
	mixed.Sessions[0].StrengthSets = append(mixed.Sessions[0].StrengthSets, &pbactivity.StrengthSet{ExerciseName: "Squat", Reps: 5, WeightKg: 80})
	if MislabelledRun(pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING, mixed) {
		t.Error("a run plus lifting is a real strength session")
	}
	if RecordedSetDistance(leedsData()) != 42726 {
		t.Error("recorded set distance")
	}
}
