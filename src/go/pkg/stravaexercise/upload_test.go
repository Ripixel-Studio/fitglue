package stravaexercise

import (
	"encoding/json"
	"testing"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func hyroxClass(gps bool) *pbactivity.StandardizedActivity {
	start := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	ts := func(s int) *timestamppb.Timestamp { return timestamppb.New(start.Add(time.Duration(s) * time.Second)) }
	rec := []*pbactivity.Record{{Timestamp: ts(0), HeartRate: 120}, {Timestamp: ts(1), HeartRate: 125}, {Timestamp: ts(1), HeartRate: 126}, {Timestamp: ts(2)}}
	if gps {
		rec[0].PositionLat, rec[0].PositionLong = 52.9, -1.1
	}
	return &pbactivity.StandardizedActivity{
		StartTime: timestamppb.New(start),
		Sessions: []*pbactivity.Session{{
			StartTime: timestamppb.New(start), TotalElapsedTime: 3300,
			Laps: []*pbactivity.Lap{{Records: rec}},
			StrengthSets: []*pbactivity.StrengthSet{
				{ExerciseName: "Treadmill", DistanceMeters: 1000},
				{ExerciseName: "SkiERG 1", DistanceMeters: 1000, DurationSeconds: 250},
				{ExerciseName: "Sled Push (Distance & Weight)", DistanceMeters: 50, WeightKg: 152},
				{ExerciseName: "Wall Ball", Reps: 100, WeightKg: 6},
				{ExerciseName: ""},
			},
		}},
		HybridRaceSummary: &pbactivity.HybridRaceSummary{Segments: []*pbactivity.HybridRaceSegment{
			{Label: "Run 1", IsRun: true, DurationSeconds: 330},
			{Label: "Ski Erg", DurationSeconds: 270},
			{Label: "Sled Push", DurationSeconds: 214},
		}},
	}
}

func TestBuildUploadSetsAndSplits(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/London")
	body, ok := BuildUpload(hyroxClass(false), loc)
	if !ok {
		t.Fatal("BuildUpload refused a gym session with sets")
	}
	var up Upload
	if err := json.Unmarshal(body, &up); err != nil {
		t.Fatal(err)
	}
	want := []UploadSet{
		{ExerciseType: "RUNNING", Duration: 330},                 // no time of its own: the run split
		{ExerciseType: "SKI_ERG", Duration: 250},                 // its own time wins over the split
		{ExerciseType: "SLED_PUSH", Weight: 152, Duration: 214},  // distance & weight in Hevy: time from the split
		{ExerciseType: "WALL_BALL", Repetitions: 100, Weight: 6}, // no split left: reps and weight only
	}
	if len(up.Sets) != len(want) {
		t.Fatalf("sets = %+v, want %+v", up.Sets, want)
	}
	for i := range want {
		if up.Sets[i] != want[i] {
			t.Errorf("set %d = %+v, want %+v", i, up.Sets[i], want[i])
		}
	}
	if up.Version != "1.0" || up.StartTime != "2026-11-19T18:00:00Z" || up.ElapsedTime != 3300 || up.UTCOffset != 0 {
		t.Errorf("header = %+v", up)
	}
	if up.Streams == nil || len(up.Streams.Time) != 2 || up.Streams.Heartrate[1] != 125 {
		t.Errorf("streams = %+v, want two strictly increasing HR points", up.Streams)
	}
}

func TestBuildUploadRefusesRoutesAndEmpty(t *testing.T) {
	if _, ok := BuildUpload(hyroxClass(true), nil); ok {
		t.Error("an activity with a GPS track must stay a FIT upload")
	}
	a := hyroxClass(false)
	a.Sessions[0].StrengthSets = nil
	if _, ok := BuildUpload(a, nil); ok {
		t.Error("no sets: nothing for a JSON upload to carry")
	}
	if _, ok := BuildUpload(nil, nil); ok {
		t.Error("nil activity")
	}
}
