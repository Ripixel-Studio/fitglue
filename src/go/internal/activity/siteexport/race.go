package siteexport

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/pkg/hyrox"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// Race is the derived `race` key of an activity file (contract §3.4): a Hyrox race's
// official splits in race order, its division and placings, and the heart rate of each
// run and station read from the full record stream (the 120-bucket series is too coarse
// for a 76-second Farmers Carry). It is present only for a Hyrox activity that has
// splits; every value is per-activity, so PBs across races are the site's to compute.
type Race struct {
	RaceType     string        `json:"raceType"`
	Division     *RaceDivision `json:"division"`
	IsSimulation bool          `json:"isSimulation"`
	Event        string        `json:"event"`
	TotalSeconds float64       `json:"totalSeconds"`
	TotalSource  string        `json:"totalSource"` // "title" (chip time in the title) or "elapsed"
	RunSeconds   float64       `json:"runSeconds"`
	StationSecs  float64       `json:"stationSeconds"`
	RoxzoneSecs  float64       `json:"roxzoneSeconds"`
	OverallRank  int           `json:"overallRank"`  // 0 = not reported
	AgeGroupRank int           `json:"ageGroupRank"` // 0 = not reported
	SplitSource  string        `json:"splitSource"`  // "official" | "laps" | "description"
	StartsApprox bool          `json:"startsEstimated"`
	HasHeartRate bool          `json:"hasHeartRate"`
	Segments     []RaceSegment `json:"segments"`
}

// RaceDivision names the division (pkg/hyrox keys) and how it was read.
type RaceDivision struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Source string `json:"source"` // "official" | "description" | "title"
}

// RaceSegment is one run or station. StartOffsetSeconds is from the session start; when
// the race's startsEstimated is true it is reconstructed (official durations laid back to
// back, ROXFIT's aggregate Roxzone spread evenly across the transitions), so the heart
// rate of a short segment may include a few seconds of the neighbouring transition.
type RaceSegment struct {
	Key                string  `json:"key"` // run_1 … run_8, skierg … wall_balls
	Label              string  `json:"label"`
	IsRun              bool    `json:"isRun"`
	DurationSeconds    float64 `json:"durationSeconds"`
	StartOffsetSeconds float64 `json:"startOffsetSeconds"`
	AvgHr              int     `json:"avgHr,omitempty"`
	MaxHr              int     `json:"maxHr,omitempty"`
}

// KmSplit is one element of the derived `kmSplits` key (contract §3.5): the heart rate of
// each whole kilometre of a distance activity, windowed by the record stream's own
// distance and timestamps, so it lines up with the pace splits.
type KmSplit struct {
	Km              int     `json:"km"`
	DurationSeconds float64 `json:"durationSeconds"`
	AvgHr           int     `json:"avgHr"`
	MaxHr           int     `json:"maxHr"`
}

// minHrSamples is how many HR samples a window needs before its average is reported.
const minHrSamples = 3

// timedRecords returns the records that carry a timestamp, oldest first. Records from
// several sources (a race-timing app's sparse track plus a Fitbit HR stream) arrive
// concatenated, not interleaved; everything downstream windows by time.
func timedRecords(data *pbactivity.StandardizedActivity) []*pbactivity.Record {
	var out []*pbactivity.Record
	if data == nil {
		return out
	}
	for _, sess := range data.Sessions {
		for _, lap := range sess.Laps {
			for _, r := range lap.Records {
				if r.Timestamp != nil {
					out = append(out, r)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.AsTime().Before(out[j].Timestamp.AsTime()) })
	return out
}

func sessionStart(data *pbactivity.StandardizedActivity, records []*pbactivity.Record) time.Time {
	if data != nil {
		for _, s := range data.Sessions {
			if s.StartTime != nil {
				return s.StartTime.AsTime()
			}
		}
		if data.StartTime != nil {
			return data.StartTime.AsTime()
		}
	}
	if len(records) > 0 {
		return records[0].Timestamp.AsTime()
	}
	return time.Time{}
}

// hrWindow is the average and max HR of the records in [from, to).
func hrWindow(records []*pbactivity.Record, from, to time.Time) (avg, max int, ok bool) {
	i := sort.Search(len(records), func(i int) bool { return !records[i].Timestamp.AsTime().Before(from) })
	var sum float64
	n := 0
	for ; i < len(records); i++ {
		t := records[i].Timestamp.AsTime()
		if !t.Before(to) {
			break
		}
		if hr := records[i].HeartRate; hr > 0 {
			sum += float64(hr)
			n++
			if int(hr) > max {
				max = int(hr)
			}
		}
	}
	if n < minHrSamples {
		return 0, 0, false
	}
	return int(math.Round(sum / float64(n))), max, true
}

func isHyroxActivity(sc *pbactivity.ShowcasedActivity) bool {
	if strings.Contains(strings.ToLower(sc.Title), "hyrox") {
		return true
	}
	tags := append([]string{}, sc.Tags...)
	if sc.ActivityData != nil {
		tags = append(tags, sc.ActivityData.Tags...)
		if strings.Contains(strings.ToLower(sc.ActivityData.Name), "hyrox") {
			return true
		}
	}
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), "hyrox") {
			return true
		}
	}
	_, official := hyrox.ParseOfficialSplits(sc.Description)
	return official
}

// DeriveRace builds the `race` key, or nil when the activity is not a Hyrox race with
// splits. Splits come, most authoritative first, from a race-timing app's official block
// in the description, the tagger's lap-based summary (real lap start times), or FitGlue's
// own split block in the description (older races re-ingested without their laps).
func DeriveRace(sc *pbactivity.ShowcasedActivity) *Race {
	if sc == nil || !isHyroxActivity(sc) {
		return nil
	}
	data := sc.ActivityData
	desc := sc.Description
	if desc == "" && data != nil {
		desc = data.Description
	}
	title := sc.Title
	if title == "" && data != nil {
		title = data.Name
	}

	records := timedRecords(data)
	start := sessionStart(data, records)

	race := &Race{RaceType: "hyrox", IsSimulation: hyrox.IsSimulation(title), Event: hyrox.EventName(title)}
	type seg struct {
		hyrox.Segment
		offset float64
	}
	var segs []seg

	if splits, ok := hyrox.ParseOfficialSplits(desc); ok {
		race.SplitSource, race.StartsApprox, race.RoxzoneSecs = "official", true, splits.RoxzoneSeconds
		gap := 0.0
		if n := len(splits.Segments); n > 1 {
			gap = splits.RoxzoneSeconds / float64(n-1)
		}
		cursor := 0.0
		for i, s := range splits.Segments {
			if i > 0 {
				cursor += gap
			}
			segs = append(segs, seg{s, cursor})
			cursor += s.Seconds
		}
	} else if summary := lapSummary(data, start); len(summary) >= 2 {
		race.SplitSource = "laps"
		for _, s := range summary {
			segs = append(segs, seg{s.Segment, s.offset})
		}
	} else if splits, ok := hyrox.ParseSplits(desc); ok {
		race.SplitSource, race.StartsApprox = "description", true
		cursor := 0.0
		for _, s := range splits.Segments {
			segs = append(segs, seg{s, cursor})
			cursor += s.Seconds
		}
	}
	if len(segs) < 2 {
		return nil
	}

	if div, src, ok := hyrox.Detect(title, desc); ok {
		race.Division = &RaceDivision{Key: div.Key, Label: div.Label, Source: src}
	}
	ranks := hyrox.ParseRanks(desc)
	race.OverallRank, race.AgeGroupRank = ranks.Overall, ranks.AgeGroup

	var elapsed float64
	if data != nil {
		for _, s := range data.Sessions {
			elapsed += s.TotalElapsedTime
		}
	}
	if t, ok := hyrox.TitleTime(title); ok {
		race.TotalSeconds, race.TotalSource = t, "title"
	} else if data != nil {
		if t, ok := hyrox.TitleTime(data.Name); ok {
			race.TotalSeconds, race.TotalSource = t, "title"
		}
	}
	if race.TotalSource == "" {
		race.TotalSeconds, race.TotalSource = elapsed, "elapsed"
	}

	race.Segments = make([]RaceSegment, 0, len(segs))
	for _, s := range segs {
		rs := RaceSegment{Key: s.Key, Label: s.Label, IsRun: s.IsRun, DurationSeconds: s.Seconds, StartOffsetSeconds: round(s.offset, 1)}
		if !start.IsZero() && len(records) > 0 {
			from := start.Add(time.Duration(s.offset * float64(time.Second)))
			to := from.Add(time.Duration(s.Seconds * float64(time.Second)))
			if avg, max, ok := hrWindow(records, from, to); ok {
				rs.AvgHr, rs.MaxHr = avg, max
				race.HasHeartRate = true
			}
		}
		if s.IsRun {
			race.RunSeconds += s.Seconds
		} else {
			race.StationSecs += s.Seconds
		}
		race.Segments = append(race.Segments, rs)
	}
	return race
}

type offsetSegment struct {
	hyrox.Segment
	offset float64
}

// lapSummary reads the tagger's hybridRaceSummary (lap-based path), whose start times are
// the laps' own.
func lapSummary(data *pbactivity.StandardizedActivity, start time.Time) []offsetSegment {
	if data == nil || data.HybridRaceSummary == nil {
		return nil
	}
	var out []offsetSegment
	runs := 0
	for _, s := range data.HybridRaceSummary.Segments {
		if s.StartTime == nil || s.DurationSeconds <= 0 {
			continue
		}
		var hs hyrox.Segment
		if s.IsRun {
			runs++
			hs = hyrox.Segment{Key: hyrox.RunKey(runs), Label: "Run " + strconv.Itoa(runs), IsRun: true}
		} else {
			key := hyrox.StationKey(s.Label)
			if key == "" {
				continue
			}
			st, _ := hyrox.StationByKey(key)
			hs = hyrox.Segment{Key: key, Label: st.Label}
		}
		hs.Seconds = float64(s.DurationSeconds)
		offset := 0.0
		if !start.IsZero() {
			offset = s.StartTime.AsTime().Sub(start).Seconds()
		}
		out = append(out, offsetSegment{hs, offset})
	}
	return out
}

// DeriveKmSplits returns the per-kilometre heart rate of a distance activity, or nil when
// the records do not carry both distance and heart rate.
func DeriveKmSplits(data *pbactivity.StandardizedActivity, category Category) []KmSplit {
	if category != CategoryCardioDist {
		return nil
	}
	records := timedRecords(data)
	var out []KmSplit
	var (
		maxDist    float64
		km         = 1
		splitStart time.Time
		prevT      time.Time
		prevD      float64
		sum        float64
		n, maxHr   int
	)
	flush := func(end time.Time) {
		if n >= minHrSamples {
			out = append(out, KmSplit{Km: km, DurationSeconds: round(end.Sub(splitStart).Seconds(), 1), AvgHr: int(math.Round(sum / float64(n))), MaxHr: maxHr})
		}
		km++
		splitStart = end
		sum, n, maxHr = 0, 0, 0
	}
	for _, r := range records {
		t := r.Timestamp.AsTime()
		if splitStart.IsZero() {
			splitStart, prevT = t, t
		}
		if r.Distance > maxDist {
			maxDist = r.Distance
		}
		for maxDist >= float64(km)*1000 {
			// Interpolate the crossing between the previous record and this one.
			cross := t
			if maxDist > prevD && !prevT.IsZero() {
				frac := (float64(km)*1000 - prevD) / (maxDist - prevD)
				if frac >= 0 && frac <= 1 {
					cross = prevT.Add(time.Duration(frac * float64(t.Sub(prevT))))
				}
			}
			flush(cross)
		}
		if hr := r.HeartRate; hr > 0 {
			sum += float64(hr)
			n++
			if int(hr) > maxHr {
				maxHr = int(hr)
			}
		}
		prevT, prevD = t, maxDist
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
