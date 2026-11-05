package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/activity"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

type fakeFixer struct {
	gotFix  activity.ActivityFix
	plan    *activity.ActivityFixPlan
	planErr error
	applied bool
}

func (f *fakeFixer) PlanActivityFix(_ context.Context, fix activity.ActivityFix) (*activity.ActivityFixPlan, error) {
	f.gotFix = fix
	return f.plan, f.planErr
}

func (f *fakeFixer) ApplyActivityFix(context.Context, *activity.ActivityFixPlan) error {
	f.applied = true
	return nil
}

func leedsPlan() *activity.ActivityFixPlan {
	return &activity.ActivityFixPlan{
		ShowcaseID: "leeds", Title: "Leeds Marathon",
		Changes: []activity.FieldChange{
			{Where: "showcase", Field: "activityType", Before: "ACTIVITY_TYPE_WEIGHT_TRAINING", After: "ACTIVITY_TYPE_RUN"},
			{Where: "entry", Field: "distanceMeters", Before: "0 m", After: "42726 m"},
		},
		Roundups: []string{"month-05-2026", "week-19-2026"},
		Notes:    []string{"no GPS/record stream stored"},
	}
}

func TestFixActivityFlags(t *testing.T) {
	f, err := parseFixActivityFlags([]string{"--id", "leeds", "--type", "run", "--distance-m", "42195", "--calories-kcal", "3802"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if f.fix.Type != pbactivity.ActivityType_ACTIVITY_TYPE_RUN || f.fix.DistanceM == nil || *f.fix.DistanceM != 42195 || f.fix.CaloriesKcal != 3802 {
		t.Errorf("fix = %+v", f.fix)
	}
	f, err = parseFixActivityFlags([]string{"--id", "leeds", "--type", "ACTIVITY_TYPE_RUN"}, &bytes.Buffer{})
	if err != nil || f.fix.DistanceM != nil {
		t.Errorf("no --distance-m means use the recorded figure: %+v %v", f, err)
	}
	for _, args := range [][]string{{"--type", "RUN"}, {"--id", "x", "--type", "SPRINTING"}, {"--id", "x", "--distance-m", "-3"}, {"--scan", "--id", "x"}} {
		if _, err := parseFixActivityFlags(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
}

func TestFixActivityDryRunPrintsAndDoesNotApply(t *testing.T) {
	fx := &fakeFixer{plan: leedsPlan()}
	f, _ := parseFixActivityFlags([]string{"--id", "leeds", "--type", "RUN"}, &bytes.Buffer{})
	var out bytes.Buffer
	if err := runFixActivity(context.Background(), f, fx, &out); err != nil {
		t.Fatal(err)
	}
	if fx.applied {
		t.Fatal("dry run applied")
	}
	for _, want := range []string{"Leeds Marathon", "0 m -> 42726 m", "month-05-2026 week-19-2026", "no GPS", "dry run"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	f.apply = true
	if err := runFixActivity(context.Background(), f, fx, &out); err != nil || !fx.applied {
		t.Errorf("--apply: %v %v", err, fx.applied)
	}
}

func TestFixActivityNothingToDo(t *testing.T) {
	fx := &fakeFixer{plan: &activity.ActivityFixPlan{Title: "x"}, planErr: activity.ErrNothingToFix}
	f, _ := parseFixActivityFlags([]string{"--id", "leeds", "--type", "RUN", "--apply"}, &bytes.Buffer{})
	var out bytes.Buffer
	if err := runFixActivity(context.Background(), f, fx, &out); err != nil || fx.applied || !strings.Contains(out.String(), "nothing to change") {
		t.Errorf("re-run after apply is a no-op: %v %v %s", err, fx.applied, out.String())
	}
}

type scanFake struct {
	entries []*pbactivity.ShowcaseProfileEntry
	shows   map[string]*pbactivity.ShowcasedActivity
	reads   int
}

func (s *scanFake) Profile(context.Context, string) (*pbactivity.ShowcaseProfile, error) {
	return &pbactivity.ShowcaseProfile{UserId: "u1"}, nil
}
func (s *scanFake) Entries(context.Context, string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	return s.entries, nil
}
func (s *scanFake) Showcase(_ context.Context, id string) (*pbactivity.ShowcasedActivity, error) {
	s.reads++
	return s.shows[id], nil
}

func TestScanMislabelledRuns(t *testing.T) {
	wt := pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING
	runSet := func(name string, m float64) *pbactivity.StandardizedActivity {
		return &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{StrengthSets: []*pbactivity.StrengthSet{{ExerciseName: name, DistanceMeters: m, DurationSeconds: 1800}}}}}
	}
	at := timestamppb.Now()
	s := &scanFake{
		entries: []*pbactivity.ShowcaseProfileEntry{
			{ShowcaseId: "leeds", Title: "Leeds Marathon", ActivityType: wt, StartTime: at, DurationSeconds: 18751},
			{ShowcaseId: "jog", Title: "Treadmill", ActivityType: wt, StartTime: at},
			{ShowcaseId: "legs", Title: "Leg day", ActivityType: wt, StartTime: at},
			{ShowcaseId: "run", Title: "Parkrun", ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_RUN, DistanceMeters: 5000, StartTime: at},
		},
		shows: map[string]*pbactivity.ShowcasedActivity{
			"leeds": {ActivityType: wt, ActivityData: runSet("Running", 42726)},
			"jog":   {ActivityType: wt, ActivityData: runSet("Treadmill Running", 0)},
			"legs":  {ActivityType: wt, ActivityData: &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{StrengthSets: []*pbactivity.StrengthSet{{ExerciseName: "Squat", Reps: 5, WeightKg: 80}}}}}},
		},
	}
	var out bytes.Buffer
	if err := runScanMislabelledRuns(context.Background(), "james", s, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "leeds") || !strings.Contains(got, "42726 m") || !strings.Contains(got, "jog") || strings.Contains(got, "legs") {
		t.Errorf("scan:\n%s", got)
	}
	if s.reads != 3 {
		t.Errorf("only strength entries with no distance are read: %d", s.reads)
	}
	if !strings.Contains(got, "2 suspect") {
		t.Errorf("count line:\n%s", got)
	}
}
