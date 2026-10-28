package hybrid_race_tagger

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Official splits: a race-timing app (ROXFIT today) creates the Strava activity and writes
// the official Hyrox splits into its description:
//
//	⏱️ Split Times:
//	Run 1: 4:54
//	Ski Erg: 3:49
//	...
//	Wall Ball: 4:17
//	Roxzone: 6:14
//
// Those times come from the race's timing mats, so they are trusted over anything the
// laps can tell us. ROXFIT's laps in particular are run-only (8-9 laps for 16 stations),
// so mapping them 1:1 onto a preset would put Run 2's time on the SkiErg and so on.

// splitTimesHeader marks the start of an official split block (matched case-insensitively,
// anywhere on the line so the leading emoji does not matter).
const splitTimesHeader = "split times:"

// officialSplitLine matches "Label: m:ss" or "Label: h:mm:ss".
var officialSplitLine = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 ]*?)\s*:\s*(\d{1,2}(?::\d{2}){1,2})$`)

// runLabel matches "Run N".
var runLabel = regexp.MustCompile(`^run\s+\d+$`)

// officialStationNames maps the official split labels (lower-cased) to the preset station
// names used in hyroxStations.
var officialStationNames = map[string]string{
	"ski erg":            "SkiErg",
	"skierg":             "SkiErg",
	"sled push":          "Sled Push",
	"sled pull":          "Sled Pull",
	"burpee broad jump":  "Burpee Broad Jump",
	"burpee broad jumps": "Burpee Broad Jump",
	"row erg":            "Rowing (Machine)",
	"rowing":             "Rowing (Machine)",
	"row":                "Rowing (Machine)",
	"farmers carry":      "Farmers Carry",
	"sandbag lunge":      "Sandbag Lunges",
	"sandbag lunges":     "Sandbag Lunges",
	"wall ball":          "Wall Balls",
	"wall balls":         "Wall Balls",
}

const roxzoneLabel = "roxzone"

// officialSplit is one parsed line of an official split block.
type officialSplit struct {
	Label   string // as written, e.g. "Run 1", "Ski Erg"
	Seconds float64
	IsRun   bool
	Station string // preset station name for non-run labels, e.g. "SkiErg"
}

// officialSplits is the parsed split block: the stations/runs in race order plus the
// aggregate Roxzone (transition) time, which ROXFIT reports as one total.
type officialSplits struct {
	Splits         []officialSplit
	RoxzoneSeconds float64
}

// parseOfficialSplits finds a "Split Times:" block in a description and parses its
// "Label: m:ss" lines until the first blank or non-matching line. Only Run N, the eight
// Hyrox stations and Roxzone are accepted; anything else ends the block. Returns false
// when there is no block or it holds no run/station line.
func parseOfficialSplits(desc string) (officialSplits, bool) {
	var out officialSplits
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
	for _, raw := range lines[start:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			break
		}
		m := officialSplitLine.FindStringSubmatch(line)
		if m == nil {
			break
		}
		secs, ok := parseClock(m[2])
		if !ok {
			break
		}
		label := strings.TrimSpace(m[1])
		key := strings.ToLower(label)
		switch {
		case key == roxzoneLabel:
			out.RoxzoneSeconds += secs
		case runLabel.MatchString(key):
			out.Splits = append(out.Splits, officialSplit{Label: label, Seconds: secs, IsRun: true})
		default:
			station, known := officialStationNames[key]
			if !known {
				return out, len(out.Splits) > 0
			}
			out.Splits = append(out.Splits, officialSplit{Label: label, Seconds: secs, Station: station})
		}
	}
	return out, len(out.Splits) > 0
}

// parseClock parses "m:ss" or "h:mm:ss" into seconds.
func parseClock(s string) (float64, bool) {
	parts := strings.Split(s, ":")
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

// stationsFromOfficialSplits builds station results and strength sets from official
// splits, taking weights/reps/distances/icons from the preset. Segment start times are
// laid out from the session start: each split runs back to back, with the aggregate
// Roxzone time spread evenly across the transitions between them (ROXFIT only reports a
// total, so per-transition placement is an approximation; durations are exact).
func stationsFromOfficialSplits(official officialSplits, preset RacePreset, sessionStart *timestamppb.Timestamp) ([]StationResult, []*pbactivity.StrengthSet) {
	var runStation *Station
	byName := make(map[string]Station, len(preset.Stations))
	for i := range preset.Stations {
		st := preset.Stations[i]
		if st.Type == StationTypeRun {
			if runStation == nil {
				runStation = &st
			}
			continue
		}
		byName[st.Name] = st
	}

	var gap float64
	if n := len(official.Splits); n > 1 && official.RoxzoneSeconds > 0 {
		gap = official.RoxzoneSeconds / float64(n-1)
	}

	var cursor time.Time
	if sessionStart != nil {
		cursor = sessionStart.AsTime()
	}

	results := make([]StationResult, 0, len(official.Splits))
	sets := make([]*pbactivity.StrengthSet, 0, len(official.Splits))
	for i, sp := range official.Splits {
		var st Station
		switch {
		case sp.IsRun && runStation != nil:
			st = *runStation
		case sp.IsRun:
			st = Station{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"}
		default:
			known, ok := byName[sp.Station]
			if !ok {
				known = Station{Name: sp.Station, Type: StationTypeStrength}
			}
			st = known
		}

		if i > 0 {
			cursor = cursor.Add(time.Duration(gap * float64(time.Second)))
		}
		var startTS *timestamppb.Timestamp
		if sessionStart != nil {
			startTS = timestamppb.New(cursor)
		}

		distance := st.DistanceMeters
		if st.Reps > 0 {
			distance = 0
		}
		results = append(results, StationResult{
			Name:         st.Name,
			Icon:         st.Icon,
			Duration:     sp.Seconds,
			Distance:     distance,
			StartTime:    startTS,
			IsRun:        sp.IsRun,
			Weight:       st.WeightKg,
			ExpectedReps: st.Reps,
		})

		set := &pbactivity.StrengthSet{
			ExerciseName:    st.Name,
			StartTime:       startTS,
			DurationSeconds: int32(sp.Seconds),
			DistanceMeters:  distance,
			SetType:         "normal",
		}
		if st.Type == StationTypeStrength {
			set.WeightKg = st.WeightKg
			if st.Reps > 0 {
				set.Reps = st.Reps
			}
		}
		sets = append(sets, set)

		cursor = cursor.Add(time.Duration(sp.Seconds * float64(time.Second)))
	}
	return results, sets
}

// officialDivision returns the value of a "Division: …" line in a description (ROXFIT
// writes "🏆 Division: Doubles Men"), or "".
func officialDivision(desc string) string {
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
