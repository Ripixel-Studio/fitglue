package personal_records

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/fitglue/server/src/go/pkg/hyrox"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// Hyrox personal records are kept per division: an Open Men's Doubles time says nothing
// about an Open Men's Solo PB (two athletes share every station). Keys are
//
//	hybrid_race_hyrox_<division>_<category>
//
// with <division> a pkg/hyrox Division.Key (men_open, men_doubles, mixed_doubles,
// men_pro, men_pro_doubles, …) and <category> one of total_time, run_1 … run_8, the eight
// station keys (skierg … wall_balls) and roxzone. Simulations ("Hyrox Sim") never set a
// record, and an activity whose division cannot be read sets none either, rather than
// landing in the wrong bucket.

// HyroxRace is what one Hyrox activity contributes to the records.
type HyroxRace struct {
	Division hyrox.Division
	// Values maps a category (total_time, run_3, sled_push, roxzone) to seconds.
	Values map[string]float64
}

// HyroxRaceValues reads the division and the per-category times of a Hyrox activity.
// Times come from the official split block when a race-timing app wrote one, else from
// the tagger's strength sets (runs numbered in order). The total is the chip time in the
// title when there is one ("HYROX - Rome - 01:12:34"), else the recorded elapsed time.
// ok is false for simulations and for an unreadable division.
func HyroxRaceValues(activity *pbactivity.StandardizedActivity) (HyroxRace, bool) {
	if hyrox.IsSimulation(activity.Name) {
		return HyroxRace{}, false
	}
	div, _, ok := hyrox.Detect(activity.Name, activity.Description)
	if !ok {
		return HyroxRace{}, false
	}
	values := map[string]float64{}

	if total, ok := hyrox.TitleTime(activity.Name); ok {
		values[hyrox.KeyTotalTime] = total
	} else {
		var elapsed float64
		for _, s := range activity.Sessions {
			elapsed += s.TotalElapsedTime
		}
		if elapsed > 0 {
			values[hyrox.KeyTotalTime] = elapsed
		}
	}

	if splits, ok := hyrox.ParseOfficialSplits(activity.Description); ok {
		for _, seg := range splits.Segments {
			if seg.Seconds > 0 {
				values[seg.Key] = seg.Seconds
			}
		}
		if splits.RoxzoneSeconds > 0 {
			values[hyrox.KeyRoxzone] = splits.RoxzoneSeconds
		}
		return HyroxRace{Division: div, Values: values}, true
	}

	runs := 0
	for _, s := range activity.Sessions {
		for _, set := range s.StrengthSets {
			if key := hyrox.StationKey(set.ExerciseName); key != "" {
				if set.DurationSeconds > 0 {
					values[key] = float64(set.DurationSeconds)
				}
				continue
			}
			if strings.Contains(strings.ToLower(set.ExerciseName), "run") {
				runs++
				if set.DurationSeconds > 0 {
					values[hyrox.RunKey(runs)] = float64(set.DurationSeconds)
				}
			}
		}
	}
	return HyroxRace{Division: div, Values: values}, true
}

// hyroxCategoryOrder is the order records are checked and listed in.
func hyroxCategoryOrder() []string {
	out := []string{hyrox.KeyTotalTime}
	for i, st := range hyrox.Stations {
		out = append(out, hyrox.RunKey(i+1), st.Key)
	}
	return append(out, hyrox.KeyRoxzone)
}

// HyroxRecordType is the record key for one division and category.
func HyroxRecordType(divisionKey, category string) string {
	return FormatHybridRaceRecordType(string(HybridRaceHyrox), divisionKey+"_"+category)
}

// checkHyroxRecords checks the per-division Hyrox records of one activity.
func (p *PersonalRecordsProvider) checkHyroxRecords(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, userID string) []NewPRResult {
	race, ok := HyroxRaceValues(activity)
	if !ok {
		logger.Info("Hyrox records skipped: simulation or unknown division", "name", activity.Name)
		return nil
	}
	var results []NewPRResult
	for _, cat := range hyroxCategoryOrder() {
		v, ok := race.Values[cat]
		if !ok || v <= 0 {
			continue
		}
		pr, err := p.checkAndUpdateRecord(ctx, userID, HyroxRecordType(race.Division.Key, cat), v, "seconds", activity, true)
		if err != nil {
			logger.Warn("Failed to check Hyrox record", "error", err, "division", race.Division.Key, "category", cat)
			continue
		}
		if pr != nil {
			results = append(results, *pr)
		}
	}
	logger.Info("Checked Hyrox records", "division", race.Division.Key, "prs_found", len(results))
	return results
}

// hyroxRecordDisplay renders a per-division Hyrox key ("hybrid_race_hyrox_men_open_run_3")
// as "HYROX Open Men's Solo Run 3"; ok is false for any other key (legacy division-less
// Hyrox keys included).
func hyroxRecordDisplay(recordType string) (string, bool) {
	raceType, rest := ParseHybridRaceRecordType(recordType)
	if raceType != string(HybridRaceHyrox) {
		return "", false
	}
	// Longest key first: "men_pro_" is a prefix of "men_pro_doubles_".
	divs := append([]hyrox.Division(nil), hyrox.Divisions...)
	sort.Slice(divs, func(i, j int) bool { return len(divs[i].Key) > len(divs[j].Key) })
	for _, d := range divs {
		cat, found := strings.CutPrefix(rest, d.Key+"_")
		if !found {
			continue
		}
		return "HYROX " + d.Label + " " + hyroxCategoryLabel(cat), true
	}
	return "", false
}

func hyroxCategoryLabel(cat string) string {
	switch {
	case cat == hyrox.KeyTotalTime:
		return "Total Time"
	case cat == hyrox.KeyRoxzone:
		return "Roxzone"
	case strings.HasPrefix(cat, "run_"):
		return "Run " + strings.TrimPrefix(cat, "run_")
	}
	if st, ok := hyrox.StationByKey(cat); ok {
		return st.Label
	}
	return formatExerciseName(cat)
}
