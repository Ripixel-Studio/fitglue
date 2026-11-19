// Package stravaexercise maps the exercise names FitGlue holds (Hevy's, or the ones
// James typed) onto Strava's JSON-upload exercise_type values, so Strava's workout
// log names every exercise instead of filing it as "Unknown".
//
// Only the name is trusted. Hevy's exercise template is often a stand-in (a sled
// push logged under a "Distance & Weight" template, a lunge station logged as a
// dumbbell walking lunge), so nothing here reads the template's type or fields.
package stravaexercise

import (
	"regexp"
	"strings"
)

// Category is one of Strava's exercise groups and its exercise_type values.
type Category struct {
	Name  string
	Types []string
}

var known = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range Categories {
		for _, t := range c.Types {
			m[t] = true
		}
	}
	return m
}()

// Known reports whether t is one of Strava's exercise_type values.
func Known(t string) bool { return known[t] }

// alias is a name pattern that names one exercise_type outright: the Hyrox stations
// and the cardio pieces a hybrid class is built from, which the general matcher
// would otherwise miss or file under a strength exercise ("Row" is not a bent-over row).
type alias struct {
	re  *regexp.Regexp
	typ string
}

var aliases = []alias{
	{regexp.MustCompile(`\bski ?erg\b|\bskierg\b`), "SKI_ERG"},
	{regexp.MustCompile(`\bsled push\b`), "SLED_PUSH"},
	{regexp.MustCompile(`\bsled (pull|drag)\b`), "SLED_PULL"},
	{regexp.MustCompile(`\bburpees? broad ?jumps?\b|\bbbjs?\b`), "BURPEE_BROAD_JUMP"},
	{regexp.MustCompile(`^(rowing|row|row erg|rower|indoor row(ing)?)( machine)?$|\browing machine\b|\brow ?erg\b|\bconcept ?2\b`), "ROWING_MACHINE"},
	{regexp.MustCompile(`\bfarmers?'? (walk|carry)\b`), "FARMERS_CARRY"},
	{regexp.MustCompile(`\bsand ?bag lunges?\b`), "SANDBAG_LUNGE"},
	{regexp.MustCompile(`^wall ?balls?$|\bwall ?balls? (shots?|throws?)\b`), "WALL_BALL"},
	{regexp.MustCompile(`^(running|run|runs|treadmill|treadmill run|outdoor run|trail running|jog|jogging|roxzone run)$`), "RUNNING"},
	{regexp.MustCompile(`\b(air|assault|echo|fan) ?bike\b`), "ASSAULT_BIKE"},
	{regexp.MustCompile(`\bbattle ?ropes?\b`), "BATTLE_ROPES"},
	{regexp.MustCompile(`\bstair ?(machine|master|climber|mill)\b|\bstairmaster\b`), "STAIRMASTER"},
	{regexp.MustCompile(`\b(jump ?rope|skipping)\b`), "JUMP_ROPE"},
	{regexp.MustCompile(`\bdouble ?unders?\b`), "DOUBLE_UNDERS"},
	{regexp.MustCompile(`\bball slams?\b|\bslam ?balls?\b`), "BALL_SLAMS"},
	{regexp.MustCompile(`^burpees?$`), "BURPEE"},
	{regexp.MustCompile(`^box jumps?$`), "BOX_JUMP"},
	{regexp.MustCompile(`^(warm ?up|warmup)$`), "WARM_UP_GENERIC"},
}

// equipment words Hevy puts in brackets ("Bench Press (Barbell)"), as Strava spells them.
var equipment = map[string]string{
	"barbell": "BARBELL", "dumbbell": "DUMBBELL", "cable": "CABLE", "machine": "MACHINE",
	"kettlebell": "KETTLEBELL", "smith machine": "SMITH_MACHINE", "band": "BAND",
	"ez bar": "EZ_BAR", "trap bar": "TRAP_BAR", "landmine": "LANDMINE", "plate": "PLATE",
	"weighted": "WEIGHTED", "machine plates": "MACHINE", "medicine ball": "MEDICINE_BALL",
}

var (
	bracket  = regexp.MustCompile(`\(([^)]*)\)`)
	nonWord  = regexp.MustCompile(`[^a-z0-9 ]+`)
	trailNum = regexp.MustCompile(`(\s+\d+)+$`)
	leadJunk = regexp.MustCompile(`^[^a-z]+`)
	spaces   = regexp.MustCompile(`\s+`)
)

// clean lowercases a name and drops brackets, punctuation, list markers and trailing
// numbers ("1️⃣ SkiERG 1" → "skierg"). It returns the bare name and the bracketed parts.
func clean(name string) (string, []string) {
	n := strings.ToLower(strings.TrimSpace(name))
	var parts []string
	for _, m := range bracket.FindAllStringSubmatch(n, -1) {
		parts = append(parts, strings.TrimSpace(m[1]))
	}
	n = bracket.ReplaceAllString(n, " ")
	n = strings.ReplaceAll(n, "-", " ")
	n = nonWord.ReplaceAllString(n, " ")
	n = leadJunk.ReplaceAllString(strings.TrimSpace(n), "")
	n = trailNum.ReplaceAllString(n, "")
	return strings.TrimSpace(spaces.ReplaceAllString(n, " ")), parts
}

func upper(words []string) string { return strings.ToUpper(strings.Join(words, "_")) }

// singular drops a plural s from each word ("lunges" → "lunge", but not "press").
func singular(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		out[i] = w
	}
	return out
}

// synonyms folds spellings Hevy and James use onto Strava's.
var synonyms = map[string]string{"bicep": "biceps", "tricep": "triceps", "situp": "sit up", "pushup": "push up", "pullup": "pull up"}

// lookup tries words as Strava spells exercises, with the bracketed equipment in front
// or behind, then bare.
func lookup(words []string, equip []string) string {
	for _, f := range [][]string{words, singular(words)} {
		b := upper(f)
		for _, e := range equip {
			cands := []string{e + "_" + b, e + "_" + b + "S", b + "_" + e}
			if len(f) > 1 { // "Incline Bench Press (Dumbbell)" is INCLINE_DUMBBELL_BENCH_PRESS
				cands = append(cands, upper(f[:1])+"_"+e+"_"+upper(f[1:]))
			}
			for _, c := range cands {
				if known[c] {
					return c
				}
			}
		}
		for _, c := range []string{b, b + "S"} {
			if known[c] {
				return c
			}
		}
	}
	return ""
}

// exact finds Strava's spelling of the name: the whole name first, then shorter runs
// of its words — dropping leading words first, since the movement is usually last
// ("Box Step Up" is a step up, "Wall Ball Crunch" a crunch), then trailing ones
// ("Seated Cable Row - Bar Grip" is a seated cable row). A run of one word counts only
// as a suffix.
func exact(base string, parts []string) string {
	words := strings.Fields(base)
	for i, w := range words {
		if s, ok := synonyms[w]; ok {
			words[i] = s
		}
	}
	words = strings.Fields(strings.Join(words, " "))
	if len(words) == 0 {
		return ""
	}
	var equip []string
	for _, p := range parts {
		if e, ok := equipment[strings.TrimSpace(p)]; ok {
			equip = append(equip, e)
		}
	}
	if t := lookup(words, equip); t != "" {
		return t
	}
	for i := 1; i < len(words); i++ {
		if t := lookup(words[i:], equip); t != "" {
			return t
		}
	}
	for j := len(words) - 1; j >= 2; j-- {
		if t := lookup(words[:j], equip); t != "" {
			return t
		}
	}
	return ""
}

// family is a keyword → Strava category generic, for a name nothing else named.
var family = []alias{
	{regexp.MustCompile(`stretch|mobility|pose|foam roll`), "WARM_UP_GENERIC"},
	{regexp.MustCompile(`push ?up|press ?up`), "PUSH_UP_GENERIC"},
	{regexp.MustCompile(`bench|chest press`), "BENCH_PRESS_GENERIC"},
	{regexp.MustCompile(`calf`), "CALF_RAISE_GENERIC"},
	{regexp.MustCompile(`carry|suitcase`), "CARRY_GENERIC"},
	{regexp.MustCompile(`chop`), "CHOP_GENERIC"},
	{regexp.MustCompile(`leg curl|hamstring curl|nordic`), "LEG_CURL_GENERIC"},
	{regexp.MustCompile(`curl`), "CURL_GENERIC"},
	{regexp.MustCompile(`deadlift|rdl`), "DEADLIFT_GENERIC"},
	{regexp.MustCompile(`\bfly|flye`), "FLYE_GENERIC"},
	{regexp.MustCompile(`glute bridge|hip raise|hip thrust`), "HIP_RAISE_GENERIC"},
	{regexp.MustCompile(`swing`), "HIP_SWING_GENERIC"},
	{regexp.MustCompile(`back extension|hyperextension|superman`), "HYPEREXTENSION_GENERIC"},
	{regexp.MustCompile(`lateral raise|front raise|rear delt`), "LATERAL_RAISE_GENERIC"},
	{regexp.MustCompile(`leg raise|knee raise|toes to bar`), "LEG_RAISE_GENERIC"},
	{regexp.MustCompile(`lunge|split squat|step up`), "LUNGE_GENERIC"},
	{regexp.MustCompile(`clean|snatch|jerk`), "OLYMPIC_LIFT_GENERIC"},
	{regexp.MustCompile(`plank`), "PLANK_GENERIC"},
	{regexp.MustCompile(`jump|hop|bound|plyo`), "PLYO_GENERIC"},
	{regexp.MustCompile(`pull ?up|chin ?up|pulldown|pull down`), "PULL_UP_GENERIC"},
	{regexp.MustCompile(`\brows?\b`), "ROW_GENERIC"},
	{regexp.MustCompile(`shoulder press|overhead press|military press|\bohp\b|arnold`), "SHOULDER_PRESS_GENERIC"},
	{regexp.MustCompile(`shrug`), "SHRUG_GENERIC"},
	{regexp.MustCompile(`sit ?up`), "SIT_UP_GENERIC"},
	{regexp.MustCompile(`crunch|dead bug|hollow|twist|v up|flutter|heel tap|ab wheel|rollout`), "CORE_GENERIC"},
	{regexp.MustCompile(`squat|leg press|thruster|wall sit`), "SQUAT_GENERIC"},
	{regexp.MustCompile(`tricep|skull ?crusher|\bdips?\b|pushdown|kickback`), "TRICEPS_EXTENSION_GENERIC"},
	{regexp.MustCompile(`burpee|man maker|bear crawl|turkish`), "TOTAL_BODY_GENERIC"},
	{regexp.MustCompile(`bike|cycl|spin|erg|treadmill|run|sprint|walk|elliptical|cardio|rope|shuttle`), "CARDIO_GENERIC"},
}

// Map returns the exercise_type for an exercise name: a named station or cardio piece,
// then Strava's own spelling of the name, then the family's generic, then
// TOTAL_BODY_GENERIC. It never returns "" for a non-empty name, so an upload is never
// refused for an exercise Strava does not list.
func Map(name string) string {
	base, parts := clean(name)
	if base == "" && len(parts) == 0 {
		return ""
	}
	whole := strings.TrimSpace(base + " " + strings.Join(parts, " "))
	for _, a := range aliases {
		if a.re.MatchString(base) || a.re.MatchString(whole) {
			return a.typ
		}
	}
	if t := exact(base, parts); t != "" {
		return t
	}
	for _, f := range family {
		if f.re.MatchString(whole) {
			return f.typ
		}
	}
	return "TOTAL_BODY_GENERIC"
}

// IsGeneric reports whether t is a family fallback rather than a named exercise.
func IsGeneric(t string) bool { return strings.HasSuffix(t, "_GENERIC") }
