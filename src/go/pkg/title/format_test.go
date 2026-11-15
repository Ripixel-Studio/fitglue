package title

import (
	"testing"
	"time"
)

func TestSpecialDay(t *testing.T) {
	cases := map[string]string{
		"2025-12-25": "Christmas",
		"2026-01-01": "New Year's Day",
		"2025-12-24": "",
		"2026-01-03": "",
	}
	for date, want := range cases {
		d, _ := time.Parse("2006-01-02", date)
		if got := SpecialDay(d); got != want {
			t.Errorf("SpecialDay(%s) = %q, want %q", date, got, want)
		}
	}
}

func TestParkrun(t *testing.T) {
	cases := []struct {
		place   string
		n       int
		special string
		want    string
	}{
		{"Newark", 13, "", "Newark parkrun (#13)"},
		{"Newark Parkrun", 13, "Christmas", "Newark parkrun (#13) - Christmas Special"},
		{"Newark parkrun", 0, "New Year's Day", "Newark parkrun - New Year's Day Special"},
		{"Bushy Park", 0, "", "Bushy Park parkrun"},
		{"Albert Parkrun, Melbourne", 2, "", "Albert parkrun, Melbourne (#2)"},
	}
	for _, tc := range cases {
		if got := Parkrun(tc.place, tc.n, tc.special); got != tc.want {
			t.Errorf("Parkrun(%q, %d, %q) = %q, want %q", tc.place, tc.n, tc.special, got, tc.want)
		}
	}
}

func TestWithNumber(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"Newark parkrun", 13, "Newark parkrun (#13)"},
		{"Newark parkrun - Christmas Special", 13, "Newark parkrun (#13) - Christmas Special"},
		{"Newark parkrun (#12) - Christmas Special", 13, "Newark parkrun (#13) - Christmas Special"},
		{"Bingham Sunday Running Club (#4)", 5, "Bingham Sunday Running Club (#5)"},
		{"Bingham Sunday Running Club (#4)", 0, "Bingham Sunday Running Club"},
		{"Hyrox Open Men's Solo - London Olympia - 01:23:47", 2, "Hyrox Open Men's Solo - London Olympia - 01:23:47 (#2)"},
	}
	for _, tc := range cases {
		if got := WithNumber(tc.in, tc.n); got != tc.want {
			t.Errorf("WithNumber(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestRaceString(t *testing.T) {
	d := func(h, m, s int) time.Duration {
		return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
	}
	cases := []struct {
		r    Race
		want string
	}{
		{Race{Series: "Hyrox", Division: "Open", Gender: "Men's", Format: "Solo", Location: "London Olympia", Time: d(1, 23, 47)}, "Hyrox Open Men's Solo - London Olympia - 01:23:47"},
		{Race{Series: "Hyrox", Division: "Pro", Gender: "Women's", Format: "Doubles", Location: "Birmingham", Time: d(1, 5, 2)}, "Hyrox Pro Women's Doubles - Birmingham - 01:05:02"},
		{Race{Series: "Spartan", Event: "Sprint", Division: "Open", Gender: "Men's", Location: "Midlands", Time: d(0, 49, 18)}, "Spartan Sprint Open Men's - Midlands - 00:49:18"},
		{Race{Series: "DEKA", Event: "FIT", Division: "Open", Gender: "Men's", Location: "Leeds", Time: d(0, 58, 10)}, "DEKA FIT Open Men's - Leeds - 00:58:10"},
		{Race{Series: "Spartan", Event: "Beast", Division: "Age Group", Gender: "Men's"}, "Spartan Beast Age Group Men's"},
		{Race{Series: "Hyrox", Gender: "Men's", Time: d(10, 0, 0)}, "Hyrox Men's - 10:00:00"},
	}
	for _, tc := range cases {
		if got := tc.r.String(); got != tc.want {
			t.Errorf("%+v.String() = %q, want %q", tc.r, got, tc.want)
		}
	}
}

func TestHyroxRace(t *testing.T) {
	cases := []struct {
		division, title string
		want            string
		ok              bool
	}{
		{"Open Men", "HYROX - London Olympia 25/26 - 1:23:47", "Hyrox Open Men's Solo - London Olympia - 01:23:47", true},
		{"Doubles Men", "HYROX Men's Doubles - Birmingham - 01:15:29", "Hyrox Open Men's Doubles - Birmingham - 01:15:29", true},
		{"Doubles Mixed", "HYROX - Rome - 01:12:34", "Hyrox Open Mixed Doubles - Rome - 01:12:34", true},
		{"Pro Men", "HYROX - Manchester - 01:10:00", "Hyrox Pro Men's Solo - Manchester - 01:10:00", true},
		{"Pro Doubles Women", "HYROX - Glasgow - 01:20:00", "Hyrox Pro Women's Doubles - Glasgow - 01:20:00", true},
		{"Relay Men", "HYROX - Cardiff - 00:58:01", "Hyrox Open Men's Relay - Cardiff - 00:58:01", true},
		// The canonical title reads back to itself.
		{"Doubles Men", "Hyrox Open Men's Doubles - Birmingham - 01:15:29", "Hyrox Open Men's Doubles - Birmingham - 01:15:29", true},
		{"Open Men", "HYROX - Rome", "", false},
		{"", "HYROX - Rome - 01:12:34", "", false},
	}
	for _, tc := range cases {
		r, ok := HyroxRace(tc.division, tc.title)
		if ok != tc.ok || (ok && r.String() != tc.want) {
			t.Errorf("HyroxRace(%q, %q) = %q, %v; want %q, %v", tc.division, tc.title, r.String(), ok, tc.want, tc.ok)
		}
	}
}

// The canonical race title replaces ROXFIT's title on a same-source update: it is about
// the same race and says at least as much (division, location, time).
func TestChooseCanonicalRaceBeatsRoxfit(t *testing.T) {
	for _, roxfitTitle := range []string{"HYROX - Birmingham - 01:15:29", "HYROX Men's Doubles - Birmingham - 01:15:29"} {
		r, ok := HyroxRace("Doubles Men", roxfitTitle)
		if !ok {
			t.Fatalf("HyroxRace(%q) not ok", roxfitTitle)
		}
		canonical := r.String()
		d := Choose(Input{Existing: roxfitTitle, Proposed: canonical, Source: roxfitTitle})
		if d.Keep || d.Title != canonical {
			t.Errorf("Choose(existing %q, proposed %q) = %q keep %v (%s), want the canonical title", roxfitTitle, canonical, d.Title, d.Keep, d.Reason)
		}
		// A repost of an activity FitGlue already retitled keeps the canonical title.
		d = Choose(Input{Existing: canonical, Proposed: canonical, Source: roxfitTitle, LastWritten: canonical, FitGlueWrote: true})
		if d.Title != canonical {
			t.Errorf("repost: Choose = %q (%s), want %q", d.Title, d.Reason, canonical)
		}
	}
}
