package hybrid_race_tagger

import (
	"time"

	"github.com/fitglue/server/src/go/pkg/hyrox"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Official splits: a race-timing app (ROXFIT today) creates the Strava activity and writes
// the official Hyrox splits into its description:
//
//	🏆 Division: Doubles Men
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
// Parsing lives in pkg/hyrox, shared with the personal-records enricher and the site export.

// parseOfficialSplits finds the official "Split Times:" block (not FitGlue's own block,
// which is this tagger's lap-mapped output and must never be read back as input).
func parseOfficialSplits(desc string) (hyrox.Splits, bool) {
	return hyrox.ParseOfficialSplits(desc)
}

// stationsFromOfficialSplits builds station results and strength sets from official
// splits, taking weights/reps/distances/icons from the preset. Segment start times are
// laid out from the session start: each split runs back to back, with the aggregate
// Roxzone time spread evenly across the transitions between them (ROXFIT only reports a
// total, so per-transition placement is an approximation; durations are exact).
func stationsFromOfficialSplits(official hyrox.Splits, preset RacePreset, sessionStart *timestamppb.Timestamp) ([]StationResult, []*pbactivity.StrengthSet) {
	var runStation *Station
	byKey := make(map[string]Station, len(preset.Stations))
	for i := range preset.Stations {
		st := preset.Stations[i]
		if st.Type == StationTypeRun {
			if runStation == nil {
				runStation = &st
			}
			continue
		}
		byKey[hyrox.StationKey(st.Name)] = st
	}

	var gap float64
	if n := len(official.Segments); n > 1 && official.RoxzoneSeconds > 0 {
		gap = official.RoxzoneSeconds / float64(n-1)
	}

	var cursor time.Time
	if sessionStart != nil {
		cursor = sessionStart.AsTime()
	}

	results := make([]StationResult, 0, len(official.Segments))
	sets := make([]*pbactivity.StrengthSet, 0, len(official.Segments))
	for i, sp := range official.Segments {
		var st Station
		switch {
		case sp.IsRun && runStation != nil:
			st = *runStation
		case sp.IsRun:
			st = Station{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"}
		default:
			known, ok := byKey[sp.Key]
			if !ok {
				name := sp.Label
				if s, found := hyrox.StationByKey(sp.Key); found {
					name = s.Name
				}
				known = Station{Name: name, Type: StationTypeStrength}
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
