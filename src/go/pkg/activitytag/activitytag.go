// Package activitytag is the personal site's own activity tag and the
// deterministic rule behind it. The ai-activity-type enricher asks Haiku for the
// tag; when that answer is missing or invalid, Rule decides instead, so no
// activity is ever untagged. The rule is a port of the website's activityKind
// (ripixel-website tasks/fitness-activity-kinds.mjs) plus title rules for the
// tags the site's kinds do not separate (hyrox, spin, pilates, yoga).
package activitytag

import (
	"regexp"
	"strings"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// The tags, exactly as the site reads them.
const (
	Run      = "run"
	Ride     = "ride"
	Spin     = "spin"
	Hyrox    = "hyrox"
	Class    = "class"
	Strength = "strength"
	Pilates  = "pilates"
	Yoga     = "yoga"
	Walk     = "walk"
	Other    = "other"
)

// Tags lists every valid tag, in the order the prompt presents them.
var Tags = []string{Run, Ride, Spin, Hyrox, Class, Strength, Pilates, Yoga, Walk, Other}

// RuleModel is ActivityClassification.model for a verdict the rule made.
const RuleModel = "rule"

// Valid reports whether tag is one of Tags.
func Valid(tag string) bool {
	for _, t := range Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Set is the part of a logged set the rule looks at.
type Set struct {
	Name           string
	Reps           int32
	DistanceMeters float64
	DurationSecs   int32
}

// Input is what the rule needs to know about an activity.
type Input struct {
	Type      pbactivity.ActivityType
	Title     string
	Distance  float64 // metres
	Routed    bool    // has a GPS track
	Sets      []Set
	HasSets   bool // Sets is known (false for an index entry, which keeps only counts)
	TotalSets int32
	TotalReps int32
	RaceSplit bool // carries Hyrox race splits (HybridRaceSummary)
}

var (
	runTypes = map[pbactivity.ActivityType]bool{
		pbactivity.ActivityType_ACTIVITY_TYPE_RUN: true, pbactivity.ActivityType_ACTIVITY_TYPE_TRAIL_RUN: true,
		pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_RUN: true,
	}
	walkTypes = map[pbactivity.ActivityType]bool{
		pbactivity.ActivityType_ACTIVITY_TYPE_WALK: true, pbactivity.ActivityType_ACTIVITY_TYPE_HIKE: true,
		pbactivity.ActivityType_ACTIVITY_TYPE_SNOWSHOE: true,
	}
	rideTypes = map[pbactivity.ActivityType]bool{
		pbactivity.ActivityType_ACTIVITY_TYPE_RIDE: true, pbactivity.ActivityType_ACTIVITY_TYPE_GRAVEL_RIDE: true,
		pbactivity.ActivityType_ACTIVITY_TYPE_MOUNTAIN_BIKE_RIDE: true, pbactivity.ActivityType_ACTIVITY_TYPE_EBIKE_RIDE: true,
		pbactivity.ActivityType_ACTIVITY_TYPE_EMOUNTAIN_BIKE_RIDE: true, pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_RIDE: true,
		pbactivity.ActivityType_ACTIVITY_TYPE_VELOMOBILE: true, pbactivity.ActivityType_ACTIVITY_TYPE_HANDCYCLE: true,
	}
	classTypes = map[pbactivity.ActivityType]bool{
		pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT: true, pbactivity.ActivityType_ACTIVITY_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING: true,
		pbactivity.ActivityType_ACTIVITY_TYPE_ELLIPTICAL: true, pbactivity.ActivityType_ACTIVITY_TYPE_STAIR_STEPPER: true,
	}

	hyroxTitle   = regexp.MustCompile(`(?i)\b(hyrox|hybrid)\b`)
	spinTitle    = regexp.MustCompile(`(?i)\b(spin|spinning)\b`)
	pilatesTitle = regexp.MustCompile(`(?i)\bpilates\b`)
	yogaTitle    = regexp.MustCompile(`(?i)\b(yoga|mobility)\b`)
	runTitle     = regexp.MustCompile(`(?i)\b(parkrun|run|running|jog|10k|half marathon|marathon)\b`)
	otherTitle   = regexp.MustCompile(`(?i)\b(padel|tennis|squash|badminton|football|golf)\b`)
	walkTitle    = regexp.MustCompile(`(?i)\b(walk|hike)\b`)
	// A session named like a class is one, whatever Hevy filed it under.
	classTitle = regexp.MustCompile(`(?i)\b(bootcamp|crossfit|hiit|stations?|compromised|intervals?|amrap|emom|circuits?|class|metcon|kettlebells?|box ?fit|body conditioning|ergshit)\b`)
	// Hevy's names for a run logged as a set.
	runExercise = regexp.MustCompile(`(?i)^(running|trail running|treadmill|outdoor run)$`)

	raceTitle    = regexp.MustCompile(`(?i)\b(race|marathon|half|10k|10 k|5k|triathlon|spartan|trot|ultra)\b`)
	notRaceTitle = regexp.MustCompile(`(?i)\b(sim|simulation|pt|class|training|practice|session|intervals?|club)\b`)
)

// isStation: a set done for distance or time with no reps (sled, erg, treadmill, plank).
func isStation(s Set) bool {
	return s.Reps == 0 && (s.DistanceMeters > 0 || s.DurationSecs > 0)
}

// Rule is the deterministic tag and race verdict, with a one-line reason.
func Rule(in Input) (tag string, isRace bool, why string) {
	tag = ruleTag(in)
	isRace, why = ruleRace(in, tag)
	return tag, isRace, why
}

func ruleTag(in Input) string {
	t, title := in.Type, in.Title
	switch {
	case hyroxTitle.MatchString(title) || in.RaceSplit:
		return Hyrox
	case spinTitle.MatchString(title):
		return Spin
	case pilatesTitle.MatchString(title) || t == pbactivity.ActivityType_ACTIVITY_TYPE_PILATES:
		return Pilates
	case yogaTitle.MatchString(title) || t == pbactivity.ActivityType_ACTIVITY_TYPE_YOGA:
		return Yoga
	case otherTitle.MatchString(title):
		return Other
	case runTypes[t]:
		return Run
	case walkTypes[t]:
		return Walk
	case rideTypes[t]:
		if in.Distance > 0 || in.Routed {
			return Ride
		}
		return Spin
	case classTypes[t]:
		return Class
	case t == pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING || t == pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT:
		return liftTag(in)
	case runTitle.MatchString(title) && in.Distance > 0:
		return Run
	case walkTitle.MatchString(title):
		return Walk
	case classTitle.MatchString(title):
		return Class
	}
	return Other
}

// liftTag is the website's LIFT_TYPES branch: a run logged as one running set is
// a run, a class-named session is a class, cardio stations outweighing lifts make
// a class; everything else, and every home WORKOUT, is strength.
func liftTag(in Input) string {
	sets := in.Sets
	if len(sets) > 0 {
		allRun := true
		for _, s := range sets {
			if !runExercise.MatchString(strings.TrimSpace(s.Name)) || s.DistanceMeters <= 0 {
				allRun = false
				break
			}
		}
		if allRun {
			return Run
		}
	}
	if classTitle.MatchString(in.Title) {
		return Class
	}
	if in.Type == pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT {
		return Strength
	}
	if !in.HasSets {
		// An index entry: counts only. Sets with no reps at all were stations.
		if in.TotalSets > 0 && in.TotalReps == 0 {
			return Class
		}
		if in.TotalSets == 0 {
			return Class
		}
		return Strength
	}
	if len(sets) == 0 || (len(sets) == 1 && isStation(sets[0])) {
		return Class
	}
	// A lone warm-up station first (five minutes on the bike) does not make it a class.
	body := sets
	if isStation(sets[0]) {
		n := 0
		for _, s := range sets {
			if s.Name == sets[0].Name {
				n++
			}
		}
		if n == 1 {
			body = sets[1:]
		}
	}
	stations, lifts := 0, 0
	for _, s := range body {
		if isStation(s) {
			stations++
		}
		if s.Reps > 0 {
			lifts++
		}
	}
	if stations+lifts > 0 && float64(stations)/float64(stations+lifts) > 0.5 {
		return Class
	}
	return Strength
}

// ruleRace says race only when the title does: a race word and no training word,
// or Hyrox race splits on something not called a sim. A parkrun is never a race
// by rule; whether it was an effort needs the judgement the model brings.
func ruleRace(in Input, tag string) (bool, string) {
	title := in.Title
	if notRaceTitle.MatchString(title) {
		return false, "rule: training or class wording in the title"
	}
	if in.RaceSplit && tag == Hyrox {
		return true, "rule: Hyrox race splits"
	}
	if raceTitle.MatchString(title) {
		return true, "rule: race wording in the title"
	}
	return false, "rule: no race wording in the title"
}

// FromActivity builds the rule's input from activity data and its title.
func FromActivity(title string, a *pbactivity.StandardizedActivity) Input {
	in := Input{Title: title, HasSets: true}
	if a == nil {
		in.HasSets = false
		return in
	}
	in.Type = a.Type
	in.RaceSplit = len(a.GetHybridRaceSummary().GetSegments()) > 0
	for _, s := range a.Sessions {
		in.Distance += s.TotalDistance
		for _, lap := range s.Laps {
			for _, r := range lap.Records {
				if r.PositionLat != 0 || r.PositionLong != 0 {
					in.Routed = true
					break
				}
			}
		}
		for _, st := range s.StrengthSets {
			in.Sets = append(in.Sets, Set{Name: st.ExerciseName, Reps: st.Reps, DistanceMeters: st.DistanceMeters, DurationSecs: st.DurationSeconds})
		}
	}
	return in
}

// FromShowcase builds the rule's input from a hydrated showcase.
func FromShowcase(sc *pbactivity.ShowcasedActivity) Input {
	in := FromActivity(sc.GetTitle(), sc.GetActivityData())
	if sc.GetActivityType() != pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
		in.Type = sc.GetActivityType()
	}
	return in
}

// FromEntry builds the rule's input from an index entry, which keeps only counts.
func FromEntry(e *pbactivity.ShowcaseProfileEntry) Input {
	return Input{
		Type: e.GetActivityType(), Title: e.GetTitle(), Distance: e.GetDistanceMeters(),
		Routed: e.GetRouteThumbnailUrl() != "", TotalSets: e.GetTotalSets(), TotalReps: e.GetTotalReps(),
	}
}

// ForShowcase is the tag and race flag a showcase carries: its stored
// classification when there is a valid one, else the rule's verdict.
func ForShowcase(sc *pbactivity.ShowcasedActivity) (string, bool) {
	if c := sc.GetEnrichments().GetClassification(); c != nil && Valid(c.Tag) {
		return c.Tag, c.IsRace
	}
	tag, race, _ := Rule(FromShowcase(sc))
	return tag, race
}

// ForEntry is the tag and race flag of an index entry: its stored tag when valid,
// else the rule's verdict on what the entry keeps.
func ForEntry(e *pbactivity.ShowcaseProfileEntry) (string, bool) {
	if Valid(e.GetTag()) {
		return e.GetTag(), e.GetIsRace()
	}
	tag, race, _ := Rule(FromEntry(e))
	return tag, race
}
