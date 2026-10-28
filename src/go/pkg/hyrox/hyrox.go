// Package hyrox is the shared Hyrox domain: divisions, stations, and the split blocks
// race-timing apps and FitGlue itself write into activity descriptions. The hybrid race
// tagger, the personal-records enricher and the site export all read races through this
// package, so a race is recognised, divided and timed the same way everywhere.
package hyrox

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Division is one Hyrox race division. Key is stable and is part of personal-record keys
// (hybrid_race_hyrox_<key>_<category>), so never rename one.
type Division struct {
	Key      string // e.g. "men_open"
	Label    string // e.g. "Open Men's Solo"
	PresetID string // hybrid_race_tagger preset carrying this division's station standards
	Doubles  bool
	Pro      bool
	Gender   string // "men", "women" or "mixed"
}

// Divisions lists every division FitGlue knows, in display order.
var Divisions = []Division{
	{Key: "men_open", Label: "Open Men's Solo", PresetID: "hyrox_men_open", Gender: "men"},
	{Key: "men_doubles", Label: "Open Men's Doubles", PresetID: "hyrox_doubles_men", Doubles: true, Gender: "men"},
	{Key: "mixed_doubles", Label: "Mixed Doubles (Open)", PresetID: "hyrox_mixed_doubles", Doubles: true, Gender: "mixed"},
	{Key: "men_pro", Label: "Pro Men's Solo", PresetID: "hyrox_men_pro", Pro: true, Gender: "men"},
	{Key: "men_pro_doubles", Label: "Pro Men's Doubles", PresetID: "hyrox_doubles_men_pro", Doubles: true, Pro: true, Gender: "men"},
	{Key: "women_open", Label: "Open Women's Solo", PresetID: "hyrox_women_open", Gender: "women"},
	{Key: "women_doubles", Label: "Open Women's Doubles", PresetID: "hyrox_doubles_women", Doubles: true, Gender: "women"},
	{Key: "women_pro", Label: "Pro Women's Solo", PresetID: "hyrox_women_pro", Pro: true, Gender: "women"},
	{Key: "women_pro_doubles", Label: "Pro Women's Doubles", PresetID: "hyrox_doubles_women_pro", Doubles: true, Pro: true, Gender: "women"},
}

// DivisionByKey returns the division with the given key.
func DivisionByKey(key string) (Division, bool) {
	for _, d := range Divisions {
		if d.Key == key {
			return d, true
		}
	}
	return Division{}, false
}

// DivisionByPreset returns the division a tagger preset stands for.
func DivisionByPreset(presetID string) (Division, bool) {
	for _, d := range Divisions {
		if d.PresetID == presetID {
			return d, true
		}
	}
	return Division{}, false
}

func divisionFor(gender string, doubles, pro bool) Division {
	for _, d := range Divisions {
		if d.Gender == gender && d.Doubles == doubles && d.Pro == pro {
			return d
		}
	}
	// Mixed Pro Doubles is not modelled; fall back to Mixed Doubles.
	d, _ := DivisionByKey("mixed_doubles")
	return d
}

// ParseDivision reads a division from free text: ROXFIT's "Open Men", "Doubles Men",
// "Doubles Mixed", "Pro Doubles Women"; FitGlue's preset names ("Hyrox - Men's Open",
// "Hyrox - Male Single"); or a title ("Hyrox Birmingham - Open Men's Doubles"). It reports
// false when the text names no division word at all. Text that names a format but no
// gender ("Doubles", "Pro") is read as men's: FitGlue has one (male) user, and the
// official Division line, which always carries a gender, is read first by Detect.
func ParseDivision(text string) (Division, bool) {
	words := wordSet(text)
	doubles := words["doubles"] || words["double"] || words["pairs"] || words["pair"]
	mixed := words["mixed"]
	pro := words["pro"]
	women := words["women"] || words["woman"] || words["womens"] || words["female"] || words["ladies"]
	men := words["men"] || words["man"] || words["mens"] || words["male"]
	solo := words["solo"] || words["single"] || words["singles"] || words["individual"]
	open := words["open"]
	if !doubles && !mixed && !pro && !women && !men && !solo && !open {
		return Division{}, false
	}
	gender := "men"
	switch {
	case mixed:
		gender, doubles = "mixed", true
	case women && !men:
		gender = "women"
	}
	return divisionFor(gender, doubles, pro), true
}

// Detect finds an activity's division, most authoritative source first: the official
// "Division:" line a race-timing app writes, then the header of FitGlue's own split
// block, then the title. source is "official", "description" or "title".
func Detect(title, description string) (div Division, source string, ok bool) {
	if line := OfficialDivision(description); line != "" {
		if d, ok := ParseDivision(line); ok {
			return d, "official", true
		}
	}
	if header := fitglueBlockHeader(description); header != "" {
		if d, ok := ParseDivision(strings.TrimPrefix(strings.ToLower(header), "hyrox")); ok {
			return d, "description", true
		}
	}
	if d, ok := ParseDivision(stripRaceWords(title)); ok {
		return d, "title", true
	}
	return Division{}, "", false
}

// OfficialDivision returns the value of a "Division: …" line (ROXFIT writes
// "🏆 Division: Doubles Men"), or "".
func OfficialDivision(desc string) string {
	for _, line := range strings.Split(desc, "\n") {
		lower := strings.ToLower(line)
		idx := strings.Index(lower, "division:")
		if idx < 0 {
			continue
		}
		return strings.TrimSpace(line[idx+len("division:"):])
	}
	return ""
}

// Station is one of the eight Hyrox stations. Key is stable (personal-record keys);
// Name is the exercise name the tagger presets and Hevy use; Label is for display.
type Station struct {
	Key   string
	Name  string
	Label string
}

// Stations in race order.
var Stations = []Station{
	{Key: "skierg", Name: "SkiErg", Label: "SkiErg"},
	{Key: "sled_push", Name: "Sled Push", Label: "Sled Push"},
	{Key: "sled_pull", Name: "Sled Pull", Label: "Sled Pull"},
	{Key: "burpee_broad_jump", Name: "Burpee Broad Jump", Label: "Burpee Broad Jumps"},
	{Key: "rowing", Name: "Rowing (Machine)", Label: "Row"},
	{Key: "farmers_carry", Name: "Farmers Carry", Label: "Farmers Carry"},
	{Key: "sandbag_lunges", Name: "Sandbag Lunges", Label: "Sandbag Lunges"},
	{Key: "wall_balls", Name: "Wall Balls", Label: "Wall Balls"},
}

// StationByKey returns the station with the given key.
func StationByKey(key string) (Station, bool) {
	for _, s := range Stations {
		if s.Key == key {
			return s, true
		}
	}
	return Station{}, false
}

// StationKey maps an exercise or split label ("Ski Erg", "Rowing (Machine)", "Farmers
// Walk", "Wall Ball", "Walking Lunge (Dumbbell)") to a station key, or "" for anything
// that is not a station (runs included).
func StationKey(name string) string {
	lower := strings.ToLower(name)
	compact := strings.ReplaceAll(lower, " ", "")
	switch {
	case strings.Contains(compact, "skierg"):
		return "skierg"
	case strings.Contains(lower, "sled push"):
		return "sled_push"
	case strings.Contains(lower, "sled pull"):
		return "sled_pull"
	case strings.Contains(lower, "burpee"):
		return "burpee_broad_jump"
	case strings.Contains(lower, "farmer"):
		return "farmers_carry"
	case strings.Contains(lower, "sandbag"), strings.Contains(lower, "lunge"):
		return "sandbag_lunges"
	case strings.Contains(lower, "wall ball"), strings.Contains(compact, "wallball"):
		return "wall_balls"
	case strings.Contains(lower, "row"):
		return "rowing"
	default:
		return ""
	}
}

// RunKey is the record/segment key of run n (1-based): "run_1" … "run_8".
func RunKey(n int) string { return "run_" + strconv.Itoa(n) }

// Segment keys that are not a station or a run.
const (
	KeyTotalTime = "total_time"
	KeyRoxzone   = "roxzone"
)

// Segment is one run or station of a race, in race order.
type Segment struct {
	Key     string // "run_3", "sled_push"
	Label   string // "Run 3", "Sled Push"
	IsRun   bool
	Seconds float64 // official duration
}

// Split sources.
const (
	SourceOfficial    = "official"    // a race-timing app's "Split Times:" block (ROXFIT)
	SourceDescription = "description" // FitGlue's own "🏁 Hyrox - …:" block (lap-mapped)
)

// Splits is a parsed split block.
type Splits struct {
	Segments []Segment
	// RoxzoneSeconds is the aggregate transition time ROXFIT reports as one total; 0 for
	// FitGlue's own block, whose lap times already include the transitions.
	RoxzoneSeconds float64
	Source         string
}

// RunSeconds and StationSeconds total the two kinds of segment.
func (s Splits) RunSeconds() float64     { return s.sum(true) }
func (s Splits) StationSeconds() float64 { return s.sum(false) }

func (s Splits) sum(runs bool) float64 {
	var t float64
	for _, seg := range s.Segments {
		if seg.IsRun == runs {
			t += seg.Seconds
		}
	}
	return t
}

// splitTimesHeader marks the start of an official split block (matched
// case-insensitively, anywhere on the line so the leading emoji does not matter).
const splitTimesHeader = "split times:"

// splitLine matches "Label: m:ss" or "Label: h:mm:ss", optionally followed by a
// parenthetical (FitGlue's "(152kg)", "(100 reps @ 6kg)", "(1.1km)").
var splitLine = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 ()']*?)\s*:\s*(\d{1,2}(?::\d{2}){1,2})(?:\s*\(.*\))?$`)

var runLabel = regexp.MustCompile(`^run\s+(\d+)$`)

// ParseSplits finds a split block in a description: a race-timing app's official
// "Split Times:" block first (ROXFIT), else FitGlue's own "🏁 Hyrox - <preset>:" block.
func ParseSplits(desc string) (Splits, bool) {
	if s, ok := parseOfficial(desc); ok {
		return s, true
	}
	return parseFitGlueBlock(desc)
}

// ParseOfficialSplits parses only a race-timing app's "Split Times:" block.
func ParseOfficialSplits(desc string) (Splits, bool) { return parseOfficial(desc) }

func parseOfficial(desc string) (Splits, bool) {
	out := Splits{Source: SourceOfficial}
	lines := strings.Split(desc, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), splitTimesHeader) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return out, false
	}
	runs := 0
	for _, raw := range lines[start:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			break
		}
		label, secs, ok := parseSplitLine(line)
		if !ok {
			break
		}
		key := strings.ToLower(label)
		switch {
		case key == KeyRoxzone:
			out.RoxzoneSeconds += secs
		case runLabel.MatchString(key):
			runs++
			out.Segments = append(out.Segments, Segment{Key: RunKey(runs), Label: "Run " + strconv.Itoa(runs), IsRun: true, Seconds: secs})
		default:
			st := StationKey(label)
			if st == "" {
				return out, len(out.Segments) > 0
			}
			station, _ := StationByKey(st)
			out.Segments = append(out.Segments, Segment{Key: st, Label: station.Label, Seconds: secs})
		}
	}
	return out, len(out.Segments) > 0
}

// fitglueBlockHeader returns the header text of FitGlue's own split block ("Hyrox -
// Men's Open"), emoji and trailing colon removed, or "".
func fitglueBlockHeader(desc string) string {
	for _, line := range strings.Split(desc, "\n") {
		t := stripLeadingSymbols(strings.TrimSpace(line))
		if strings.HasSuffix(t, ":") && strings.HasPrefix(strings.ToLower(t), "hyrox") {
			return strings.TrimSpace(strings.TrimSuffix(t, ":"))
		}
	}
	return ""
}

func parseFitGlueBlock(desc string) (Splits, bool) {
	out := Splits{Source: SourceDescription}
	lines := strings.Split(desc, "\n")
	start := -1
	for i, line := range lines {
		t := stripLeadingSymbols(strings.TrimSpace(line))
		if strings.HasSuffix(t, ":") && strings.HasPrefix(strings.ToLower(t), "hyrox") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return out, false
	}
	runs := 0
	for _, raw := range lines[start:] {
		line := stripLeadingSymbols(strings.TrimSpace(raw))
		if line == "" {
			break
		}
		label, secs, ok := parseSplitLine(line)
		if !ok {
			break
		}
		key := strings.ToLower(label)
		if key == "total" {
			break
		}
		if runLabel.MatchString(key) {
			runs++
			out.Segments = append(out.Segments, Segment{Key: RunKey(runs), Label: "Run " + strconv.Itoa(runs), IsRun: true, Seconds: secs})
			continue
		}
		st := StationKey(label)
		if st == "" {
			break
		}
		station, _ := StationByKey(st)
		out.Segments = append(out.Segments, Segment{Key: st, Label: station.Label, Seconds: secs})
	}
	return out, len(out.Segments) > 0
}

func parseSplitLine(line string) (string, float64, bool) {
	m := splitLine.FindStringSubmatch(line)
	if m == nil {
		return "", 0, false
	}
	secs, ok := ParseClock(m[2])
	if !ok {
		return "", 0, false
	}
	return strings.TrimSpace(m[1]), secs, true
}

// ParseClock parses "m:ss" or "h:mm:ss" into seconds.
func ParseClock(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	total := 0
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, false
		}
		if i > 0 && n >= 60 {
			return 0, false
		}
		total = total*60 + n
	}
	return float64(total), true
}

// Ranks are the placings a race-timing app reports (0 = not reported / "N/A").
type Ranks struct {
	Overall  int
	AgeGroup int
}

var (
	overallRankLine  = regexp.MustCompile(`(?i)overall\s+rank\s*:\s*#?(\d+)`)
	ageGroupRankLine = regexp.MustCompile(`(?i)age\s*group\s+rank\s*:\s*#?(\d+)`)
)

// ParseRanks reads ROXFIT's "Overall Rank: 270" / "Age Group Rank: 60" lines.
func ParseRanks(desc string) Ranks {
	var r Ranks
	if m := overallRankLine.FindStringSubmatch(desc); m != nil {
		r.Overall, _ = strconv.Atoi(m[1])
	}
	if m := ageGroupRankLine.FindStringSubmatch(desc); m != nil {
		r.AgeGroup, _ = strconv.Atoi(m[1])
	}
	return r
}

// IsSimulation reports whether a title marks a practice race ("Hyrox Sim", "Hyrox
// Simulation", "Mock Hyrox"). Simulations show their splits but never set a PB.
func IsSimulation(title string) bool {
	w := wordSet(title)
	return w["sim"] || w["simulation"] || w["mock"] || w["practice"]
}

var titleClock = regexp.MustCompile(`\b(\d{1,2}:\d{2}:\d{2})\b`)

// TitleTime returns a finish time written in the title ("HYROX - Rome - 01:12:34",
// "Hyrox - London Olympia 25/26 - 1:23:47 🎉"): the official chip time, which can differ
// by seconds from the recorded elapsed time.
func TitleTime(title string) (float64, bool) {
	m := titleClock.FindStringSubmatch(title)
	if m == nil {
		return 0, false
	}
	return ParseClock(m[1])
}

// raceWords are dropped from a title to leave the event name.
var raceWords = map[string]bool{
	"hyrox": true, "open": true, "pro": true, "men": true, "mens": true, "men's": true, "women": true,
	"womens": true, "women's": true, "mixed": true, "doubles": true, "double": true, "solo": true,
	"single": true, "male": true, "female": true, "relay": true, "race": true, "s": true,
	"sim": true, "simulation": true, "mock": true,
}

var seasonOrTime = regexp.MustCompile(`\b\d{1,2}:\d{2}(?::\d{2})?\b|\b\d{2}/\d{2}\b`)

// EventName is the title with the race, division and time words removed: "Hyrox
// Birmingham - Open Men's Solo" → "Birmingham", "HYROX - Rome - 01:12:34" → "Rome",
// "Hyrox - London Olympia 25/26 - 1:23:47 🎉" → "London Olympia". "" when nothing is left.
func EventName(title string) string {
	t := seasonOrTime.ReplaceAllString(title, " ")
	var kept []string
	for _, f := range strings.FieldsFunc(t, func(r rune) bool { return !(unicode.IsLetter(r) || r == '\'') }) {
		if raceWords[strings.ToLower(f)] {
			continue
		}
		kept = append(kept, f)
	}
	return strings.Join(kept, " ")
}

func stripRaceWords(title string) string {
	return seasonOrTime.ReplaceAllString(title, " ")
}

func stripLeadingSymbols(s string) string {
	return strings.TrimLeftFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
}

func wordSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		out[f] = true
	}
	return out
}
