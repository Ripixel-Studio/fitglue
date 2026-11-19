package stravaexercise

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/fitglue/server/src/go/pkg/hyrox"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// JSONSportTypes are the Strava sport types a JSON (sets) upload is for. The docs list
// the first four; HybridRacing was accepted with every exercise named when tried live
// on 2026-11-19.
var JSONSportTypes = map[string]bool{
	"WeightTraining": true, "HighIntensityIntervalTraining": true, "Workout": true,
	"Crossfit": true, "HybridRacing": true,
}

// Upload is Strava's JSON upload body (developers.strava.com/docs/uploads, "JSON -
// Strength Training").
type Upload struct {
	Version       string         `json:"version"`
	StartTime     string         `json:"start_time"`
	UTCOffset     int            `json:"utc_offset"`
	ElapsedTime   int            `json:"elapsed_time"`
	ActiveTime    int            `json:"active_time,omitempty"`
	TotalCalories int            `json:"total_calories,omitempty"`
	Creator       *Creator       `json:"creator,omitempty"`
	Sets          []UploadSet    `json:"sets"`
	Streams       *UploadStreams `json:"streams,omitempty"`
}

type Creator struct {
	Name string `json:"name,omitempty"`
}

// UploadSet is one set. Strava takes reps, weight and duration only: a station's
// distance has nowhere to go.
type UploadSet struct {
	ExerciseType string  `json:"exercise_type"`
	Repetitions  int     `json:"repetitions,omitempty"`
	Weight       float64 `json:"weight,omitempty"`
	Duration     int     `json:"duration,omitempty"`
	StartTime    string  `json:"start_time,omitempty"`
}

type UploadStreams struct {
	Time      []int  `json:"time"`
	Active    []bool `json:"active,omitempty"`
	Heartrate []int  `json:"heartrate,omitempty"`
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// stationKeys ties a station exercise_type to the Hyrox split it can borrow a time from.
var stationKeys = map[string]string{
	"SKI_ERG": "skierg", "SLED_PUSH": "sled_push", "SLED_PULL": "sled_pull",
	"BURPEE_BROAD_JUMP": "burpee_broad_jump", "ROWING_MACHINE": "rowing",
	"FARMERS_CARRY": "farmers_carry", "SANDBAG_LUNGE": "sandbag_lunges", "WALL_BALL": "wall_balls",
}

// splitTimes hands out the Hyrox split durations in race order: each station's, and
// the runs one after another. A set with no duration of its own (Hevy often has only a
// distance and a weight for a sled) takes the next unused one for its station.
type splitTimes struct {
	stations map[string][]int
	runs     []int
}

func newSplitTimes(s *pbactivity.HybridRaceSummary) *splitTimes {
	st := &splitTimes{stations: map[string][]int{}}
	for _, seg := range s.GetSegments() {
		if seg.GetDurationSeconds() <= 0 {
			continue
		}
		if seg.GetIsRun() {
			st.runs = append(st.runs, int(seg.GetDurationSeconds()))
			continue
		}
		if k := hyrox.StationKey(seg.GetLabel()); k != "" {
			st.stations[k] = append(st.stations[k], int(seg.GetDurationSeconds()))
		}
	}
	return st
}

func (s *splitTimes) take(exerciseType string) int {
	pop := func(q *[]int) int {
		if len(*q) == 0 {
			return 0
		}
		v := (*q)[0]
		*q = (*q)[1:]
		return v
	}
	if exerciseType == "RUNNING" {
		return pop(&s.runs)
	}
	if k, ok := stationKeys[exerciseType]; ok {
		q := s.stations[k]
		v := pop(&q)
		s.stations[k] = q
		return v
	}
	return 0
}

// BuildUpload turns an activity's strength sets into Strava's JSON upload. ok is false
// when the activity has no named sets or carries a GPS track (a JSON upload has no
// route, laps or distance, so those stay FIT uploads). loc gives the displayed UTC
// offset; nil means UTC.
func BuildUpload(a *pbactivity.StandardizedActivity, loc *time.Location) ([]byte, bool) {
	if a == nil || len(a.GetSessions()) == 0 {
		return nil, false
	}
	start := a.GetStartTime().AsTime()
	if a.GetStartTime() == nil {
		start = a.GetSessions()[0].GetStartTime().AsTime()
	}
	if start.IsZero() || start.Unix() <= 0 {
		return nil, false
	}
	splits := newSplitTimes(a.GetHybridRaceSummary())

	up := Upload{Version: "1.0", StartTime: rfc3339(start), Creator: &Creator{Name: "FitGlue"}}
	if loc != nil {
		_, off := start.In(loc).Zone()
		up.UTCOffset = off
	}
	var elapsed, calories float64
	var times, hrs []int
	for _, sess := range a.GetSessions() {
		elapsed += sess.GetTotalElapsedTime()
		calories += sess.GetTotalCalories()
		for _, set := range sess.GetStrengthSets() {
			typ := Map(set.GetExerciseName())
			if typ == "" {
				continue
			}
			us := UploadSet{ExerciseType: typ, Repetitions: int(set.GetReps())}
			if w := set.GetWeightKg(); w > 0 {
				us.Weight = math.Round(w*100) / 100
			}
			us.Duration = int(set.GetDurationSeconds())
			if us.Duration <= 0 {
				us.Duration = splits.take(typ)
			} else {
				splits.take(typ) // keep later sets of this station on their own split
			}
			if set.GetStartTime() != nil && set.GetStartTime().AsTime().Unix() > 0 {
				us.StartTime = rfc3339(set.GetStartTime().AsTime())
			}
			up.Sets = append(up.Sets, us)
		}
		for _, lap := range sess.GetLaps() {
			for _, r := range lap.GetRecords() {
				if r.GetPositionLat() != 0 || r.GetPositionLong() != 0 {
					return nil, false
				}
				if r.GetHeartRate() <= 0 || r.GetTimestamp() == nil {
					continue
				}
				off := int(r.GetTimestamp().AsTime().Sub(start).Seconds())
				if off < 0 || (len(times) > 0 && off <= times[len(times)-1]) {
					continue
				}
				times = append(times, off)
				hrs = append(hrs, int(r.GetHeartRate()))
			}
		}
	}
	named := 0
	for _, s := range up.Sets {
		if !IsGeneric(s.ExerciseType) {
			named++
		}
	}
	// A class logged as one placeholder set ("Bootcamp", "Spinning") has nothing for
	// the workout log; it stays a FIT upload.
	if named == 0 {
		return nil, false
	}
	up.ElapsedTime = int(math.Round(elapsed))
	if up.ElapsedTime <= 0 {
		for _, s := range up.Sets {
			up.ElapsedTime += s.Duration
		}
	}
	if up.ElapsedTime <= 0 {
		return nil, false
	}
	up.TotalCalories = int(math.Round(calories))
	if len(times) > 0 {
		up.Streams = &UploadStreams{Time: times, Heartrate: hrs}
	}
	b, err := json.Marshal(up)
	if err != nil {
		return nil, false
	}
	return b, true
}

// SetsSummary is a one-line count of how the sets mapped, for logs.
func SetsSummary(body []byte) string {
	var up Upload
	if json.Unmarshal(body, &up) != nil {
		return ""
	}
	named := 0
	for _, s := range up.Sets {
		if !IsGeneric(s.ExerciseType) {
			named++
		}
	}
	return fmt.Sprintf("%d sets, %d named, %d generic", len(up.Sets), named, len(up.Sets)-named)
}
