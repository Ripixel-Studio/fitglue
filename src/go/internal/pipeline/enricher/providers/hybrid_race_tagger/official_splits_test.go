package hybrid_race_tagger

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// roxfitBirmingham is the description ROXFIT wrote on Strava activity 20751310619
// (HYROX Birmingham, 2026-10-27), verbatim.
const roxfitBirmingham = "Completed via ROXFIT\n\n🏆 Division: Doubles Men\n📊 Overall Rank: N/A\nAverage pace: 05:48/km\nAverage run: 5:32\n\n⏱️ Split Times:\nRun 1: 4:54\nSki Erg: 3:49\nRun 2: 6:15\nSled Push: 1:41\nRun 3: 5:33\nSled Pull: 2:32\nRun 4: 5:22\nBurpee Broad Jump: 3:14\nRun 5: 5:31\nRow Erg: 4:24\nRun 6: 5:31\nFarmers Carry: 1:16\nRun 7: 5:47\nSandbag Lunge: 3:50\nRun 8: 5:28\nWall Ball: 4:17\nRoxzone: 6:14\n\n🏃 Total Running: 44:21\n💪 Total Functional: 25:03\n⏸️ Roxzone (transitions): 6:14\n"

func TestParseOfficialSplits_RoxfitBirmingham(t *testing.T) {
	got, ok := parseOfficialSplits(roxfitBirmingham)
	if !ok {
		t.Fatal("expected the split block to parse")
	}
	// 17 lines: 16 runs/stations + Roxzone.
	if len(got.Splits) != 16 {
		t.Fatalf("splits = %d, want 16", len(got.Splits))
	}
	if got.RoxzoneSeconds != 6*60+14 {
		t.Errorf("roxzone = %v, want 374", got.RoxzoneSeconds)
	}
	var running, functional float64
	for _, s := range got.Splits {
		if s.IsRun {
			running += s.Seconds
		} else {
			functional += s.Seconds
		}
	}
	if running != 44*60+21 {
		t.Errorf("total running = %s, want 44:21", formatDuration(running))
	}
	if functional != 25*60+3 {
		t.Errorf("total functional = %s, want 25:03", formatDuration(functional))
	}
	want := []string{"", "SkiErg", "", "Sled Push", "", "Sled Pull", "", "Burpee Broad Jump", "", "Rowing (Machine)", "", "Farmers Carry", "", "Sandbag Lunges", "", "Wall Balls"}
	for i, s := range got.Splits {
		if s.IsRun != (i%2 == 0) {
			t.Errorf("split %d (%s) IsRun = %v", i, s.Label, s.IsRun)
		}
		if s.Station != want[i] {
			t.Errorf("split %d station = %q, want %q", i, s.Station, want[i])
		}
	}
	if got.Splits[0].Label != "Run 1" || got.Splits[0].Seconds != 294 {
		t.Errorf("first split = %+v", got.Splits[0])
	}
}

func TestParseOfficialSplits_Absent(t *testing.T) {
	for _, d := range []string{"", "Morning run", "⏱️ Split Times:\n\nnothing", "🏁 Hyrox - Men's Open:\n🏃 Run 1: 4:00"} {
		if _, ok := parseOfficialSplits(d); ok {
			t.Errorf("unexpected splits in %q", d)
		}
	}
}

// The ROXFIT activity has 9 run-only laps. With official splits present the tagger must
// use them (16 segments, official durations), not map laps 1:1 onto stations, and must
// not emit a description section that repeats the splits.
func TestHRT_ApplySelection_UsesOfficialSplits(t *testing.T) {
	start := time.Date(2026, 10, 27, 12, 10, 2, 0, time.UTC)
	laps := make([]*pbactivity.Lap, 9)
	for i := range laps {
		laps[i] = makeLap(976, 330, time.Duration(i)*330*time.Second)
	}
	session := &pbactivity.Session{Laps: laps, TotalDistance: 8700, StartTime: timestamppb.New(start)}
	a := &pbactivity.StandardizedActivity{
		Name:        "HYROX - Birmingham - 01:15:29",
		Description: roxfitBirmingham,
		Sessions:    []*pbactivity.Session{session},
	}

	p := &HybridRaceTaggerProvider{}
	res, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), map[string]string{
		"auto_preset_map": "doubles=hyrox_doubles_men;solo=hyrox_men_open",
		"auto_preset":     "hyrox_doubles_men",
	}, false)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if res.Description != "" {
		t.Errorf("description should be empty when official splits exist, got %q", res.Description)
	}
	if res.Metadata["split_source"] != "official_description" {
		t.Errorf("split_source = %q", res.Metadata["split_source"])
	}
	segs := res.HybridRaceSummary.GetSegments()
	if len(segs) != 16 {
		t.Fatalf("segments = %d, want 16", len(segs))
	}
	if segs[0].DurationSeconds != 294 || !segs[0].IsRun {
		t.Errorf("seg 0 = %+v", segs[0])
	}
	if segs[1].Label != "SkiErg" || segs[1].DurationSeconds != 229 || segs[1].IsRun {
		t.Errorf("seg 1 = %+v, want SkiErg 229s", segs[1])
	}
	if segs[2].DurationSeconds != 375 {
		t.Errorf("seg 2 (Run 2) = %d, want 375", segs[2].DurationSeconds)
	}
	if segs[15].Label != "Wall Balls" || segs[15].DurationSeconds != 257 {
		t.Errorf("seg 15 = %+v", segs[15])
	}
	if !segs[0].StartTime.AsTime().Equal(start) {
		t.Errorf("seg 0 start = %v, want %v", segs[0].StartTime.AsTime(), start)
	}
	// Last segment ends at start + all splits + all Roxzone time.
	end := segs[15].StartTime.AsTime().Add(time.Duration(segs[15].DurationSeconds) * time.Second)
	if want := start.Add((2661 + 1503 + 374) * time.Second); end.Sub(want).Abs() > time.Second {
		t.Errorf("race end = %v, want %v", end, want)
	}
	// Laps are left alone (no relabelling onto stations).
	if len(session.Laps) != 9 || session.Laps[1].ExerciseName != "" {
		t.Errorf("laps were rewritten: %d laps, lap1 name %q", len(session.Laps), session.Laps[1].ExerciseName)
	}
	if len(session.StrengthSets) != 16 {
		t.Errorf("strength sets = %d, want 16", len(session.StrengthSets))
	}
	var wb *pbactivity.StrengthSet
	for _, s := range session.StrengthSets {
		if s.ExerciseName == "Wall Balls" {
			wb = s
		}
	}
	if wb == nil || wb.Reps != 100 || wb.WeightKg != HyroxMenProWallBallKg || wb.DurationSeconds != 257 {
		t.Errorf("wall balls set = %+v", wb)
	}
	if res.Tags[0] != "HYROX" {
		t.Errorf("tags = %v", res.Tags)
	}
}

// Without an official block the existing lap path (and its description section) is unchanged.
func TestHRT_ApplySelection_NoOfficialSplitsKeepsLapPath(t *testing.T) {
	a := &pbactivity.StandardizedActivity{Name: "HYROX London", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	res, err := (&HybridRaceTaggerProvider{}).applySelection(a, UserSelection{PresetID: "hyrox_men_open"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Description, "🏁 Hyrox - Men's Open:") {
		t.Errorf("lap-path description missing: %q", res.Description)
	}
	if res.Metadata["split_source"] != "" {
		t.Errorf("unexpected split_source %q", res.Metadata["split_source"])
	}
}

// An official split block with no laps at all is still a taggable hybrid race.
func TestHRT_Enrich_OfficialSplitsWithoutLaps(t *testing.T) {
	a := &pbactivity.StandardizedActivity{
		Name:        "Lunch Run",
		Description: roxfitBirmingham,
		Sessions:    []*pbactivity.Session{{TotalDistance: 8700, StartTime: timestamppb.Now()}},
	}
	res, err := (&HybridRaceTaggerProvider{}).Enrich(context.Background(), slog.Default(), a, hrtUser(),
		map[string]string{"auto_preset_map": "doubles=hyrox_doubles_men", "auto_preset": "hyrox_doubles_men"}, false)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if got := len(res.HybridRaceSummary.GetSegments()); got != 16 {
		t.Errorf("segments = %d, want 16", got)
	}
}
