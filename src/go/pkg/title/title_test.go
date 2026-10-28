package title

import "testing"

const calendar = "Hyrox Birmingham - Open Men's Doubles"

const roxfit = "HYROX - Birmingham - 01:15:29"

func TestChoose(t *testing.T) {
	cases := []struct {
		name   string
		in     Input
		want   string
		keep   bool
		reason string
	}{
		{"ROXFIT title vs calendar title", Input{Existing: roxfit, Proposed: calendar, Source: roxfit}, roxfit, true, ReasonMoreDetailed},
		{"Strava default", Input{Existing: "Morning Run", Proposed: calendar, Source: "Morning Run"}, calendar, false, ReasonGeneric},
		{"Garmin place default", Input{Existing: "Birmingham Running", Proposed: calendar}, calendar, false, ReasonGeneric},
		{"less descriptive subset", Input{Existing: "Hyrox", Proposed: calendar}, calendar, false, ReasonLessDetailed},
		{"unrelated non-generic", Input{Existing: "Leg day with Dean", Proposed: calendar}, calendar, false, ReasonNoOverlap},
		{"identical", Input{Existing: calendar, Proposed: calendar}, calendar, true, ReasonIdentical},
		{"empty existing", Input{Existing: "", Proposed: calendar}, calendar, false, ReasonEmpty},
		{"no proposal", Input{Existing: roxfit, Proposed: ""}, roxfit, true, ReasonNoProposal},
		{"extra significant word", Input{Existing: "Hyrox Birmingham doubles with Dean", Proposed: "Hyrox Birmingham"}, "Hyrox Birmingham doubles with Dean", true, ReasonMoreDetailed},
		{"same words, different case and punctuation", Input{Existing: "hyrox birmingham!", Proposed: "Hyrox: Birmingham"}, "Hyrox: Birmingham", false, ReasonLessDetailed},

		// FitGlue's own earlier name: only when it equals what FitGlue last wrote.
		{"FitGlue's own previous name is replaced", Input{Existing: "Newark parkrun #41", Proposed: "Newark parkrun #42", Source: "Morning Run", LastWritten: "Newark parkrun #41", FitGlueWrote: true}, "Newark parkrun #42", false, ReasonFitGlueOwn},
		{"FitGlue's own name, source title beats the proposal: restored", Input{Existing: calendar, Proposed: calendar, Source: roxfit, LastWritten: calendar, FitGlueWrote: true}, roxfit, false, ReasonRestoreSource},
		{"a name the user typed since FitGlue wrote is not FitGlue's", Input{Existing: "Hyrox Birmingham doubles with Stephen", Proposed: calendar, Source: roxfit, LastWritten: calendar, FitGlueWrote: true}, "Hyrox Birmingham doubles with Stephen", true, ReasonMoreDetailed},

		// The repost bug (fitglue#95's Choose): FitGlue had written only the description,
		// keeping ROXFIT's title; a repost replaced the title because "FitGlue wrote".
		{"repost: FitGlue wrote the description only, ROXFIT title kept", Input{Existing: roxfit, Proposed: calendar, Source: roxfit, FitGlueWrote: true}, roxfit, true, ReasonMoreDetailed},
		// …and the record that repost left behind (no written_title yet): the activity
		// carries FitGlue's proposal where ROXFIT's title was, so ROXFIT's comes back.
		{"repost after the bug: ROXFIT title restored", Input{Existing: calendar, Proposed: calendar, Source: roxfit, FitGlueWrote: true}, roxfit, false, ReasonRestoreSource},
		{"FitGlue last wrote the restored source title: it stays", Input{Existing: roxfit, Proposed: calendar, Source: roxfit, LastWritten: roxfit, FitGlueWrote: true}, roxfit, true, ReasonRestoreSource},
		{"pre-written_title record, source title is generic", Input{Existing: calendar, Proposed: calendar, Source: "Morning Workout", FitGlueWrote: true}, calendar, false, ReasonFitGlueOwn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Choose(tc.in)
			if d.Title != tc.want || d.Keep != tc.keep || d.Reason != tc.reason {
				t.Errorf("Choose(%+v) = %q keep %v (%s), want %q keep %v (%s)", tc.in, d.Title, d.Keep, d.Reason, tc.want, tc.keep, tc.reason)
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
