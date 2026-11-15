package title

// The canonical title formats. Every place that writes a parkrun, a numbered habit or a
// race title builds it here, so the parkrun enricher, auto_increment, the hybrid race
// tagger and `fitgluectl retitle` can never disagree:
//
//	Newark parkrun (#13) - Christmas Special
//	Bingham Sunday Running Club (#42)
//	Hyrox Open Men's Doubles - Birmingham - 01:15:29
//	Spartan Sprint Open Men's - Midlands - 00:49:18

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/pkg/hyrox"
)

// SpecialDay names a parkrun special event from its local date: "Christmas" (25 Dec),
// "New Year's Day" (1 Jan), else "".
func SpecialDay(local time.Time) string {
	switch {
	case local.Month() == time.December && local.Day() == 25:
		return "Christmas"
	case local.Month() == time.January && local.Day() == 1:
		return "New Year's Day"
	}
	return ""
}

// ParkrunPlace strips a trailing "parkrun" (any case) from an event name: "Newark
// Parkrun" → "Newark".
func ParkrunPlace(name string) string {
	name = strings.TrimSpace(name)
	if len(name) >= len("parkrun") && strings.EqualFold(name[len(name)-len("parkrun"):], "parkrun") {
		name = strings.TrimSpace(name[:len(name)-len("parkrun")])
	}
	return name
}

// Parkrun is "<Place> parkrun", then " (#n)" when n > 0, then " - <special> Special"
// when special is set. A name that already says parkrun elsewhere ("Albert Parkrun,
// Melbourne") keeps its words, with "parkrun" lower-cased.
func Parkrun(place string, n int, special string) string {
	t := ParkrunPlace(place) + " parkrun"
	if anyParkrun.MatchString(ParkrunPlace(place)) {
		t = anyParkrun.ReplaceAllString(strings.TrimSpace(place), "parkrun")
	}
	if special != "" {
		t += " - " + special + " Special"
	}
	return WithNumber(t, n)
}

var anyParkrun = regexp.MustCompile(`(?i)\bparkrun\b`)

// specialTail matches a trailing " - <...> Special" tail.
var specialTail = regexp.MustCompile(` - [^-]+ Special$`)

// numberSuffix matches an existing " (#n)".
var numberSuffix = regexp.MustCompile(` \(#\d+\)`)

// AddSuffix puts suffix on a title before a trailing " - <...> Special" tail, so a
// number lands as "Newark parkrun (#13) - Christmas Special", else at the end.
func AddSuffix(title, suffix string) string {
	if loc := specialTail.FindStringIndex(title); loc != nil {
		return title[:loc[0]] + suffix + title[loc[0]:]
	}
	return title + suffix
}

// WithNumber numbers a title " (#n)" (see AddSuffix), replacing any number it already
// carries. n <= 0 strips the number.
func WithNumber(title string, n int) string {
	title = strings.TrimSpace(numberSuffix.ReplaceAllString(title, ""))
	if n <= 0 {
		return title
	}
	return AddSuffix(title, NumberSuffix(n))
}

// NumberSuffix is " (#n)".
func NumberSuffix(n int) string { return fmt.Sprintf(" (#%d)", n) }

// Race is a race title: "<Series> <Event> <Division> <Gender> <Format> - <Location> -
// <HH:MM:SS>", empty parts dropped.
//
//	Series   Hyrox, DEKA, Spartan
//	Event    DEKA: FIT, MILE, STRONG; Spartan: Sprint, Super, Beast, Ultra, Trail; Hyrox: ""
//	Division Open, Pro (Hyrox); Open, Age Group, Elite (Spartan)
//	Gender   Men's, Women's, Mixed
//	Format   Solo, Doubles, Relay (Hyrox)
type Race struct {
	Series   string
	Event    string
	Division string
	Gender   string
	Format   string
	Location string
	Time     time.Duration
}

// String renders the canonical race title.
func (r Race) String() string {
	var head []string
	for _, p := range []string{r.Series, r.Event, r.Division, r.Gender, r.Format} {
		if p = strings.TrimSpace(p); p != "" {
			head = append(head, p)
		}
	}
	parts := []string{strings.Join(head, " ")}
	if l := strings.TrimSpace(r.Location); l != "" {
		parts = append(parts, l)
	}
	if r.Time > 0 {
		parts = append(parts, Clock(r.Time))
	}
	return strings.Join(parts, " - ")
}

// Clock is a zero-padded HH:MM:SS.
func Clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
}

// HyroxRace builds a Hyrox race from the official division line a race-timing app
// writes ("Division: Doubles Men", "Open Men", "Pro Women", "Relay Mixed") and a title
// carrying the location and finish time (ROXFIT's "HYROX - Rome - 01:12:34", or the
// canonical title itself). ok is false without a division or a finish time.
func HyroxRace(divisionLine, title string) (Race, bool) {
	div, ok := hyrox.ParseDivision(divisionLine)
	relay := hasWord(divisionLine, "relay")
	if !ok && !relay {
		return Race{}, false
	}
	secs, ok := hyrox.TitleTime(title)
	if !ok || secs <= 0 {
		return Race{}, false
	}
	r := Race{
		Series:   "Hyrox",
		Division: "Open",
		Location: hyrox.EventName(title),
		Time:     time.Duration(secs * float64(time.Second)),
	}
	if div.Pro {
		r.Division = "Pro"
	}
	switch div.Gender {
	case "women":
		r.Gender = "Women's"
	case "mixed":
		r.Gender = "Mixed"
	default:
		r.Gender = "Men's"
	}
	switch {
	case relay:
		r.Format = "Relay"
	case div.Doubles:
		r.Format = "Doubles"
	default:
		r.Format = "Solo"
	}
	return r, true
}

func hasWord(s, w string) bool {
	for _, f := range words(s) {
		if f == w {
			return true
		}
	}
	return false
}
