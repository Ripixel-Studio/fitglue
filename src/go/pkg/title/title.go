// Package title decides whether a same-source update should replace an activity's
// existing title with the one FitGlue proposes.
//
// FitGlue's proposed title (from ical_title, location naming, condition rules …) is a
// big improvement over a platform default such as "Morning Run". But when the source
// title was written by a person or a race-timing app — ROXFIT's "HYROX - Birmingham -
// 01:15:29" — and it is about the same thing as FitGlue's proposal while saying more
// (a finish time, extra detail), the existing title is kept.
package title

import (
	"strings"
	"unicode"
)

// Decision is the outcome of Choose.
type Decision struct {
	Title  string // the title to end up with
	Keep   bool   // true when the existing title is kept
	Reason string // short machine-readable reason, for logging
}

// Reasons returned in Decision.Reason.
const (
	ReasonNoProposal   = "no_proposal"
	ReasonIdentical    = "identical"
	ReasonEmpty        = "existing_empty"
	ReasonFitGlueOwn   = "existing_is_fitglue_own"
	ReasonGeneric      = "existing_generic"
	ReasonNoOverlap    = "no_meaningful_overlap"
	ReasonLessDetailed = "existing_less_descriptive"
	ReasonMoreDetailed = "existing_matches_and_more_descriptive"
)

// Choose decides between the existing title and FitGlue's proposed one.
// fitglueWrote reports that FitGlue has already written to this activity, in which case
// the existing title is FitGlue's own previous name and is always replaced.
//
//   - Replace when the existing title is empty, FitGlue's own, or a platform default
//     ("Morning Run", "Evening Weight Training", "<Place> Running", "Workout", …).
//   - Keep when it matches the proposal (at least half of its significant words appear
//     in the proposal, ignoring case, punctuation, stopwords and numbers) AND it is at
//     least as descriptive: it has a significant word, or a number/time, that the
//     proposal lacks.
//   - Replace otherwise.
func Choose(existing, proposed string, fitglueWrote bool) Decision {
	existing = strings.TrimSpace(existing)
	proposed = strings.TrimSpace(proposed)
	replace := func(reason string) Decision { return Decision{Title: proposed, Reason: reason} }
	keep := func(reason string) Decision { return Decision{Title: existing, Keep: true, Reason: reason} }

	switch {
	case proposed == "":
		return keep(ReasonNoProposal)
	case existing == proposed:
		return keep(ReasonIdentical)
	case existing == "":
		return replace(ReasonEmpty)
	case fitglueWrote:
		return replace(ReasonFitGlueOwn)
	case IsGeneric(existing):
		return replace(ReasonGeneric)
	}

	exTokens := significant(existing)
	prTokens := significant(proposed)
	prSet := toSet(prTokens)
	overlap := 0
	extra := false
	for _, t := range exTokens {
		if prSet[t] {
			overlap++
		} else {
			extra = true
		}
	}
	if overlap == 0 || overlap*2 < len(exTokens) {
		return replace(ReasonNoOverlap)
	}
	if extra || hasNumberNotIn(existing, proposed) {
		return keep(ReasonMoreDetailed)
	}
	return replace(ReasonLessDetailed)
}

// timeOfDayWords prefix platform defaults: Strava's "Morning Run", "Lunch Ride", …
var timeOfDayWords = map[string]bool{
	"morning": true, "afternoon": true, "evening": true, "night": true, "lunch": true,
	"midday": true, "early": true, "late": true,
}

// activityWords are the activity nouns platform defaults are built from (Strava, Garmin,
// Fitbit, Hevy).
var activityWords = map[string]bool{
	"run": true, "running": true, "trail": true, "treadmill": true, "jog": true,
	"ride": true, "cycling": true, "bike": true, "biking": true, "indoor": true, "virtual": true,
	"walk": true, "walking": true, "hike": true, "hiking": true,
	"swim": true, "swimming": true, "pool": true, "open": true, "water": true,
	"row": true, "rowing": true,
	"workout": true, "weight": true, "weights": true, "training": true, "strength": true,
	"cardio": true, "hiit": true, "yoga": true, "pilates": true, "crossfit": true,
	"elliptical": true, "stair": true, "stepper": true, "activity": true, "exercise": true,
	"sport": true, "session": true,
}

// garminActivityWords end Garmin's "<Place> Running" style defaults.
var garminActivityWords = map[string]bool{
	"running": true, "cycling": true, "walking": true, "hiking": true, "swimming": true,
	"rowing": true, "strength": true, "cardio": true, "training": true,
}

// IsGeneric reports whether a title looks like a platform's auto-generated default:
// only time-of-day and activity words ("Morning Run", "Afternoon Weight Training",
// "Run", "Workout"), or Garmin's "<Place> <Activity>" ("Birmingham Running") with at most
// three place words and no numbers.
func IsGeneric(s string) bool {
	words := words(s)
	if len(words) == 0 {
		return true
	}
	rest := words
	for len(rest) > 0 && timeOfDayWords[rest[0]] {
		rest = rest[1:]
	}
	allActivity := true
	for _, w := range rest {
		if !activityWords[w] {
			allActivity = false
			break
		}
	}
	if allActivity {
		return true
	}
	last := words[len(words)-1]
	if garminActivityWords[last] && len(words) <= 4 && !strings.ContainsFunc(s, unicode.IsDigit) {
		return true
	}
	return false
}

var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "of": true, "in": true, "at": true,
	"on": true, "to": true, "for": true, "with": true, "by": true, "vs": true, "via": true,
}

// words lower-cases s and splits it into letter-only words; digits and punctuation
// separate words and are dropped.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) })
}

// significant returns the words of s minus stopwords and one-letter fragments ("men's"
// → "men").
func significant(s string) []string {
	var out []string
	for _, w := range words(s) {
		if len([]rune(w)) < 2 || stopwords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

func toSet(ws []string) map[string]bool {
	m := make(map[string]bool, len(ws))
	for _, w := range ws {
		m[w] = true
	}
	return m
}

// hasNumberNotIn reports whether a carries a number (or time, e.g. "01:15:29") that b
// does not.
func hasNumberNotIn(a, b string) bool {
	numbers := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsDigit(r) && r != ':' && r != '.' })
	}
	inB := map[string]bool{}
	for _, n := range numbers(b) {
		inB[strings.Trim(n, ":.")] = true
	}
	for _, n := range numbers(a) {
		n = strings.Trim(n, ":.")
		if n != "" && strings.ContainsFunc(n, unicode.IsDigit) && !inB[n] {
			return true
		}
	}
	return false
}
