package activitytag

import (
	"testing"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

const (
	run      = pbactivity.ActivityType_ACTIVITY_TYPE_RUN
	ride     = pbactivity.ActivityType_ACTIVITY_TYPE_RIDE
	vride    = pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_RIDE
	crossfit = pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT
	hiit     = pbactivity.ActivityType_ACTIVITY_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING
	weights  = pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING
	workout  = pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT
	pilates  = pbactivity.ActivityType_ACTIVITY_TYPE_PILATES
	yoga     = pbactivity.ActivityType_ACTIVITY_TYPE_YOGA
	walk     = pbactivity.ActivityType_ACTIVITY_TYPE_WALK
)

func lifts(n int) []Set {
	var s []Set
	for i := 0; i < n; i++ {
		s = append(s, Set{Name: "Squat", Reps: 8})
	}
	return s
}

func TestRuleTags(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		tag  string
		race bool
	}{
		{"parkrun", Input{Type: run, Title: "Newark parkrun", Distance: 5000, Routed: true}, Run, false},
		{"10k race", Input{Type: run, Title: "Riverside 10k", Distance: 10000}, Run, true},
		{"running club", Input{Type: run, Title: "Sunday Running Club", Distance: 8000}, Run, false},
		{"outdoor ride", Input{Type: ride, Title: "Morning Ride", Distance: 30000, Routed: true}, Ride, false},
		{"indoor ride is spin", Input{Type: vride, Title: "Evening session"}, Spin, false},
		{"spin title", Input{Type: crossfit, Title: "Spin"}, Spin, false},
		{"hyrox class", Input{Type: crossfit, Title: "Hyrox Class"}, Hybrid, false},
		{"hybrid class", Input{Type: crossfit, Title: "Hybrid class"}, Hybrid, false},
		{"hyrox pt", Input{Type: weights, Title: "Half Hyrox PT", HasSets: true, Sets: lifts(10)}, Hybrid, false},
		{"hyrox sim is no race", Input{Type: crossfit, Title: "Hyrox Sim", RaceSplit: true}, Hybrid, false},
		{"hyrox race", Input{Type: crossfit, Title: "HYROX Riverside", RaceSplit: true}, Hybrid, true},
		{"bootcamp", Input{Type: crossfit, Title: "Bootcamp"}, Class, false},
		{"hiit type", Input{Type: hiit, Title: "Thursday"}, Class, false},
		{"box fit pt", Input{Type: weights, Title: "Box Fit PT", HasSets: true, Sets: lifts(4)}, Class, false},
		{"lifting pt", Input{Type: weights, Title: "Upper Body PT", HasSets: true, Sets: lifts(12)}, Strength, false},
		{"home workout", Input{Type: workout, Title: "Bedtime Core", HasSets: true, Sets: []Set{{Name: "Plank", DurationSecs: 60}}}, Strength, false},
		{"stations outweigh lifts", Input{Type: weights, Title: "Engine", HasSets: true, Sets: []Set{
			{Name: "Row", DistanceMeters: 500}, {Name: "Ski", DistanceMeters: 500}, {Name: "Sled", DistanceMeters: 50}, {Name: "Lunge", Reps: 20},
		}}, Class, false},
		{"run logged as a set", Input{Type: workout, Title: "Long one", HasSets: true, Sets: []Set{{Name: "Running", DistanceMeters: 21100}}}, Run, false},
		{"pilates", Input{Type: pilates, Title: "Reformer"}, Pilates, false},
		{"pilates title", Input{Type: workout, Title: "Glutes & Core Pilates"}, Pilates, false},
		{"yoga", Input{Type: yoga, Title: "Flow"}, Yoga, false},
		{"walk", Input{Type: walk, Title: "Afternoon Walk", Distance: 3000}, Walk, false},
		{"padel", Input{Type: pbactivity.ActivityType_ACTIVITY_TYPE_TENNIS, Title: "Padel"}, Other, false},
		{"unknown", Input{Type: pbactivity.ActivityType_ACTIVITY_TYPE_GOLF, Title: "Range"}, Other, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag, race, why := Rule(tc.in)
			if tag != tc.tag || race != tc.race {
				t.Errorf("Rule = %s/%v (%s), want %s/%v", tag, race, why, tc.tag, tc.race)
			}
			if !Valid(tag) || why == "" {
				t.Errorf("tag %q invalid or no reason", tag)
			}
		})
	}
}

func TestValid(t *testing.T) {
	for _, tag := range Tags {
		if !Valid(tag) {
			t.Errorf("%s not valid", tag)
		}
	}
	for _, bad := range []string{"", "Run", "race", "crossfit", "mind"} {
		if Valid(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestForShowcasePrefersStoredClassification(t *testing.T) {
	sc := &pbactivity.ShowcasedActivity{Title: "Bootcamp", ActivityType: crossfit}
	if tag, race := ForShowcase(sc); tag != Class || race {
		t.Errorf("rule fallback = %s/%v", tag, race)
	}
	sc.Enrichments = &pbactivity.ActivityEnrichments{Classification: &pbactivity.ActivityClassification{Tag: "hyrox", IsRace: true}}
	if tag, race := ForShowcase(sc); tag != Hybrid || !race {
		t.Errorf("stored = %s/%v", tag, race)
	}
	sc.Enrichments.Classification.Tag = "nonsense"
	if tag, _ := ForShowcase(sc); tag != Class {
		t.Errorf("invalid stored tag must fall back to the rule, got %s", tag)
	}
}

func TestForEntry(t *testing.T) {
	e := &pbactivity.ShowcaseProfileEntry{Title: "Lower Body PT", ActivityType: weights, TotalSets: 12, TotalReps: 96}
	if tag, _ := ForEntry(e); tag != Strength {
		t.Errorf("entry rule = %s", tag)
	}
	e.Tag, e.IsRace = Run, true
	if tag, race := ForEntry(e); tag != Run || !race {
		t.Errorf("stored entry tag = %s/%v", tag, race)
	}
}

func TestFromActivity(t *testing.T) {
	a := &pbactivity.StandardizedActivity{
		Type: weights,
		Sessions: []*pbactivity.Session{{
			TotalDistance: 1200,
			Laps:          []*pbactivity.Lap{{Records: []*pbactivity.Record{{PositionLat: 53.07}}}},
			StrengthSets:  []*pbactivity.StrengthSet{{ExerciseName: "Squat", Reps: 5}},
		}},
		HybridRaceSummary: &pbactivity.HybridRaceSummary{Segments: []*pbactivity.HybridRaceSegment{{Label: "Run 1"}}},
	}
	in := FromActivity("Title", a)
	if in.Type != weights || in.Distance != 1200 || !in.Routed || len(in.Sets) != 1 || !in.RaceSplit || !in.HasSets {
		t.Errorf("FromActivity = %+v", in)
	}
}

func TestHybridCoversTheHybridRaces(t *testing.T) {
	for _, title := range []string{"Spartan Sprint", "DEKA Fit", "ATHX Manchester", "Hyrox Sim"} {
		if tag, _, _ := Rule(Input{Title: title}); tag != Hybrid {
			t.Errorf("%s: %s", title, tag)
		}
	}
	if Normalize("hyrox") != Hybrid || !Valid("hyrox") || Normalize("run") != Run {
		t.Error("the legacy hyrox tag reads as hybrid")
	}
}
