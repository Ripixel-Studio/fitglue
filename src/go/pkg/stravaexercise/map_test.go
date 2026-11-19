package stravaexercise

import "testing"

func TestMapNamesStationsAndGym(t *testing.T) {
	cases := map[string]string{
		// Hyrox stations, however they were typed or templated in Hevy.
		"SkiErg": "SKI_ERG", "Ski Erg": "SKI_ERG", "⬜ SkiERG 1": "SKI_ERG",
		"Sled Push": "SLED_PUSH", "Sled Push (Distance & Weight)": "SLED_PUSH",
		"sled pull": "SLED_PULL", "Burpee broad jumps": "BURPEE_BROAD_JUMP", "Burpee Broad Jump": "BURPEE_BROAD_JUMP",
		"Rowing Machine": "ROWING_MACHINE", "Rowing (Machine)": "ROWING_MACHINE",
		"Farmers Walk": "FARMERS_CARRY", "Farmers Carry": "FARMERS_CARRY",
		"Sandbag Lunges": "SANDBAG_LUNGE", "Wall Balls": "WALL_BALL", "Wall Ball": "WALL_BALL",
		"Running": "RUNNING", "Treadmill": "RUNNING", "Running (Outdoor)": "RUNNING",
		// A row that is not the erg, and a lunge that is not the sandbag.
		"Bent Over Row (Dumbbell)": "BENT_OVER_ROW",
		"Walking Lunge (Dumbbell)": "DUMBBELL_WALKING_LUNGES",
		"Wall Ball Crunch":         "CRUNCH",
		// Gym names, with Hevy's equipment in brackets.
		"Bench Press (Barbell)":          "BARBELL_BENCH_PRESS",
		"Incline Bench Press (Dumbbell)": "INCLINE_DUMBBELL_BENCH_PRESS",
		"Seated Cable Row - Bar Grip":    "SEATED_CABLE_ROW",
		"Box Step Up":                    "STEP_UP",
		"Bicep Curl (Cable)":             "CABLE_BICEPS_CURL",
		"Deadlift (Trap bar)":            "TRAP_BAR_DEADLIFT",
		// Family generics for names Strava does not list.
		"Lying Leg Curl (Machine)": "LEG_CURL_GENERIC",
		"Calf Stretch":             "STRETCH",
		"Push Up":                  "PUSH_UP_GENERIC",
		"Sit Up":                   "SIT_UP_GENERIC",
		"Bulgarian Split Squat":    "LUNGE_GENERIC",
		"Spinning":                 "CARDIO_GENERIC",
		"Pilates":                  "TOTAL_BODY_GENERIC",
	}
	for in, want := range cases {
		if got := Map(in); got != want {
			t.Errorf("Map(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestMapAlwaysKnown(t *testing.T) {
	for _, n := range []string{"Devil's Press", "Shoulder Taps", "x", "Around The World", "🙂"} {
		if got := Map(n); got != "" && !Known(got) {
			t.Errorf("Map(%q) = %s, not a Strava exercise_type", n, got)
		}
	}
	if Map("") != "" {
		t.Error("empty name should map to empty")
	}
}
