package stravaexercise

import (
	"testing"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

func TestSportType(t *testing.T) {
	race := &pbactivity.StandardizedActivity{HybridRaceSummary: &pbactivity.HybridRaceSummary{
		Segments: []*pbactivity.HybridRaceSegment{{Label: "Run 1", IsRun: true}}}}
	plain := &pbactivity.StandardizedActivity{}
	T := pbactivity.ActivityType_value
	cases := []struct {
		typ        string
		base, tag  string
		a          *pbactivity.StandardizedActivity
		want, name string
	}{
		{"ACTIVITY_TYPE_CROSSFIT", "Crossfit", "hybrid", plain, HybridRacing, "hyrox class"},
		{"ACTIVITY_TYPE_WEIGHT_TRAINING", "WeightTraining", "hybrid", plain, HybridRacing, "hyrox PT"},
		{"ACTIVITY_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING", "HighIntensityIntervalTraining", "hybrid", plain, HybridRacing, "hybrid class"},
		{"ACTIVITY_TYPE_RUN", "Run", "hybrid", race, HybridRacing, "ROXFIT race"},
		{"ACTIVITY_TYPE_RUN", "Run", "hybrid", plain, "Run", "a run with hyrox in the title stays a run"},
		{"ACTIVITY_TYPE_RIDE", "Ride", "hybrid", plain, "Ride", "hybrid spin stays a ride"},
		{"ACTIVITY_TYPE_CROSSFIT", "Crossfit", "class", plain, "Crossfit", "not hybrid"},
		{"ACTIVITY_TYPE_CROSSFIT", "Crossfit", "hybrid", plain, "Crossfit", "Hybrid Spinning Class"},
		{"ACTIVITY_TYPE_TRAIL_RUN", "TrailRun", "hybrid", plain, "TrailRun", "Spartan Sprint, no hyrox splits"},
		{"ACTIVITY_TYPE_CROSSFIT", "Crossfit", "", plain, "Crossfit", "untagged"},
	}
	for _, c := range cases {
		if got := SportType(pbactivity.ActivityType(T[c.typ]), c.base, c.tag, c.name, c.a); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
