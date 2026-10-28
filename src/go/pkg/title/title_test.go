package title

import "testing"

const calendar = "Hyrox Birmingham - Open Men's Doubles"

func TestChoose(t *testing.T) {
	cases := []struct {
		name, existing, proposed string
		wrote                    bool
		keep                     bool
		reason                   string
	}{
		{"ROXFIT title vs calendar title", "HYROX - Birmingham - 01:15:29", calendar, false, true, ReasonMoreDetailed},
		{"Strava default", "Morning Run", calendar, false, false, ReasonGeneric},
		{"Garmin place default", "Birmingham Running", calendar, false, false, ReasonGeneric},
		{"less descriptive subset", "Hyrox", calendar, false, false, ReasonLessDetailed},
		{"unrelated non-generic", "Leg day with Dean", calendar, false, false, ReasonNoOverlap},
		{"identical", calendar, calendar, false, true, ReasonIdentical},
		{"empty existing", "", calendar, false, false, ReasonEmpty},
		{"FitGlue's own previous name", "HYROX - Birmingham - 01:15:29", calendar, true, false, ReasonFitGlueOwn},
		{"no proposal", "HYROX - Birmingham - 01:15:29", "", false, true, ReasonNoProposal},
		{"extra significant word", "Hyrox Birmingham doubles with Dean", "Hyrox Birmingham", false, true, ReasonMoreDetailed},
		{"same words, different case and punctuation", "hyrox birmingham!", "Hyrox: Birmingham", false, false, ReasonLessDetailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Choose(tc.existing, tc.proposed, tc.wrote)
			if d.Keep != tc.keep || d.Reason != tc.reason {
				t.Errorf("Choose(%q, %q, %v) = keep %v (%s), want keep %v (%s)", tc.existing, tc.proposed, tc.wrote, d.Keep, d.Reason, tc.keep, tc.reason)
			}
			want := tc.proposed
			if tc.keep {
				want = tc.existing
			}
			if d.Title != want {
				t.Errorf("title = %q, want %q", d.Title, want)
			}
		})
	}
}

func TestIsGeneric(t *testing.T) {
	generic := []string{"", "Morning Run", "Afternoon Workout", "Evening Weight Training", "Lunch Ride",
		"Birmingham Running", "Sutton Park Running", "Run", "Workout", "Night Walk", "Morning Trail Run", "Indoor Cycling"}
	for _, s := range generic {
		if !IsGeneric(s) {
			t.Errorf("IsGeneric(%q) = false, want true", s)
		}
	}
	specific := []string{"HYROX - Birmingham - 01:15:29", calendar, "Hyrox", "parkrun #412", "Leg day with Dean", "10k Running"}
	for _, s := range specific {
		if IsGeneric(s) {
			t.Errorf("IsGeneric(%q) = true, want false", s)
		}
	}
}
