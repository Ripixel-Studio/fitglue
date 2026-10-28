package siteexport

import (
	"encoding/json"
	"testing"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const roxfitDoubles = "Completed via ROXFIT\n\n🏆 Division: Doubles Men\n📊 Overall Rank: N/A\n\n⏱️ Split Times:\nRun 1: 4:54\nSki Erg: 3:49\nRun 2: 6:15\nSled Push: 1:41\nRun 3: 5:33\nSled Pull: 2:32\nRun 4: 5:22\nBurpee Broad Jump: 3:14\nRun 5: 5:31\nRow Erg: 4:24\nRun 6: 5:31\nFarmers Carry: 1:16\nRun 7: 5:47\nSandbag Lunge: 3:50\nRun 8: 5:28\nWall Ball: 4:17\nRoxzone: 6:14\n\nFirst of two races over two days, and Stephen's first Hyrox"

var raceStart = time.Date(2026, 10, 27, 12, 10, 2, 0, time.UTC)

// hrRecords is a 1 Hz HR stream whose value is hr(t) for t seconds after raceStart,
// appended after a sparse distance track (the ROXFIT + Fitbit shape: not in time order).
func hrRecords(seconds int, hr func(t int) int32) []*pbactivity.Record {
	recs := []*pbactivity.Record{
		{Timestamp: timestamppb.New(raceStart.Add(30 * time.Minute)), Distance: 4000},
		{Timestamp: timestamppb.New(raceStart.Add(60 * time.Minute)), Distance: 8000},
	}
	for t := 0; t < seconds; t++ {
		recs = append(recs, &pbactivity.Record{Timestamp: timestamppb.New(raceStart.Add(time.Duration(t) * time.Second)), HeartRate: hr(t)})
	}
	return recs
}

func showcase(title, desc string, records []*pbactivity.Record, elapsed float64) *pbactivity.ShowcasedActivity {
	return &pbactivity.ShowcasedActivity{
		ShowcaseId:   "x",
		Title:        title,
		Description:  desc,
		ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT,
		ActivityData: &pbactivity.StandardizedActivity{
			Name: title,
			Tags: []string{"HYROX"},
			Sessions: []*pbactivity.Session{{
				StartTime:        timestamppb.New(raceStart),
				TotalElapsedTime: elapsed,
				Laps:             []*pbactivity.Lap{{Records: records}},
			}},
		},
	}
}

func TestDeriveRace_OfficialSplitsWithHeartRate(t *testing.T) {
	// HR 150 on runs, 180 on stations, using the same reconstruction the export does.
	sc := showcase("Hyrox Birmingham - Open Men's Doubles", roxfitDoubles, nil, 4529)
	race := DeriveRace(sc)
	if race == nil {
		t.Fatal("no race")
	}
	segs := race.Segments
	hr := func(t int) int32 {
		for _, s := range segs {
			if float64(t) >= s.StartOffsetSeconds && float64(t) < s.StartOffsetSeconds+s.DurationSeconds {
				if s.IsRun {
					return 150
				}
				return 180
			}
		}
		return 120 // Roxzone
	}
	sc.ActivityData.Sessions[0].Laps[0].Records = hrRecords(4529, hr)
	race = DeriveRace(sc)

	if race.Division == nil || race.Division.Key != "men_doubles" || race.Division.Source != "official" {
		t.Errorf("division = %+v", race.Division)
	}
	if race.SplitSource != "official" || !race.StartsApprox || race.RoxzoneSecs != 374 {
		t.Errorf("source %q approx %v roxzone %v", race.SplitSource, race.StartsApprox, race.RoxzoneSecs)
	}
	if race.TotalSeconds != 4529 || race.TotalSource != "elapsed" || race.Event != "Birmingham" {
		t.Errorf("total %v %q event %q", race.TotalSeconds, race.TotalSource, race.Event)
	}
	if race.OverallRank != 0 || race.IsSimulation {
		t.Errorf("rank %d sim %v", race.OverallRank, race.IsSimulation)
	}
	if len(race.Segments) != 16 || !race.HasHeartRate {
		t.Fatalf("segments %d hr %v", len(race.Segments), race.HasHeartRate)
	}
	// Roxzone (374s) spread over 15 transitions: SkiErg starts at 294 + 24.93.
	if got := race.Segments[1].StartOffsetSeconds; got < 318.8 || got > 319.0 {
		t.Errorf("skierg offset = %v", got)
	}
	for _, s := range race.Segments {
		want := 150
		if !s.IsRun {
			want = 180
		}
		if s.AvgHr != want || s.MaxHr != want {
			t.Errorf("%s avg %d max %d, want %d", s.Key, s.AvgHr, s.MaxHr, want)
		}
	}
	if race.RunSeconds != 2661 || race.StationSecs != 1503 {
		t.Errorf("runs %v stations %v", race.RunSeconds, race.StationSecs)
	}
}

func TestDeriveRace_TitleTimeAndFitGlueBlock(t *testing.T) {
	desc := "I am beyonddddd proud\n\n🏁 Hyrox - Men's Open:\n🏃 Run 1: 4:49\n⛷️ SkiErg: 4:18\n🏃 Run 2: 5:35\n⏱️ Total: 1:24:02"
	race := DeriveRace(showcase("Hyrox - London Olympia 25/26 - 1:23:47 🎉", desc, nil, 5043))
	if race == nil {
		t.Fatal("no race")
	}
	if race.TotalSeconds != 5027 || race.TotalSource != "title" {
		t.Errorf("total %v %q", race.TotalSeconds, race.TotalSource)
	}
	if race.SplitSource != "description" || race.Division.Key != "men_open" || race.Event != "London Olympia" {
		t.Errorf("%q %+v %q", race.SplitSource, race.Division, race.Event)
	}
	if race.Segments[2].StartOffsetSeconds != 289+258 || race.HasHeartRate {
		t.Errorf("offset %v hr %v", race.Segments[2].StartOffsetSeconds, race.HasHeartRate)
	}
}

func TestDeriveRace_LapSummaryKeepsRealStarts(t *testing.T) {
	sc := showcase("Hyrox Manchester - Open Men's Solo", "", nil, 600)
	sc.ActivityData.HybridRaceSummary = &pbactivity.HybridRaceSummary{Segments: []*pbactivity.HybridRaceSegment{
		{StartTime: timestamppb.New(raceStart), DurationSeconds: 300, Label: "Running (Outdoor)", IsRun: true},
		{StartTime: timestamppb.New(raceStart.Add(310 * time.Second)), DurationSeconds: 250, Label: "SkiErg"},
	}}
	race := DeriveRace(sc)
	if race == nil || race.SplitSource != "laps" || race.StartsApprox {
		t.Fatalf("%+v", race)
	}
	if race.Segments[1].Key != "skierg" || race.Segments[1].StartOffsetSeconds != 310 {
		t.Errorf("%+v", race.Segments[1])
	}
}

func TestDeriveRace_NotARace(t *testing.T) {
	if DeriveRace(showcase("Hyrox Class", "Great class", nil, 3000)) != nil {
		t.Error("a class without splits is not a race")
	}
	sc := showcase("Morning Run", "", nil, 1800)
	sc.ActivityData.Tags = nil
	if DeriveRace(sc) != nil {
		t.Error("not hyrox")
	}
	if r := DeriveRace(showcase("Hyrox Sim", "🏁 Hyrox - Male Single:\n🏃 Run 1: 5:54 (1.1km)\n⛷️ SkiErg: 4:21 (87m)", nil, 4756)); r == nil || !r.IsSimulation {
		t.Errorf("simulation: %+v", r)
	}
}

func TestDeriveKmSplits(t *testing.T) {
	// 3.5 km at 5:00/km, 1 Hz, HR = 140 + km.
	var recs []*pbactivity.Record
	for s := 0; s <= 1050; s++ {
		d := float64(s) * 1000 / 300
		recs = append(recs, &pbactivity.Record{Timestamp: timestamppb.New(raceStart.Add(time.Duration(s) * time.Second)), Distance: d, HeartRate: int32(141 + s/300)})
	}
	data := &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{StartTime: timestamppb.New(raceStart), Laps: []*pbactivity.Lap{{Records: recs}}}}}
	splits := DeriveKmSplits(data, CategoryCardioDist)
	if len(splits) != 3 {
		t.Fatalf("splits = %+v", splits)
	}
	for i, s := range splits {
		if s.Km != i+1 || s.DurationSeconds < 299 || s.DurationSeconds > 301 || s.AvgHr != 141+i {
			t.Errorf("split %d = %+v", i, s)
		}
	}
	if DeriveKmSplits(data, CategoryStrength) != nil {
		t.Error("strength gets no km splits")
	}
}

// BuildActivity carries both keys, sorts the series by time, and omits them when absent.
func TestBuildActivity_RaceAndSortedSeries(t *testing.T) {
	sc := showcase("Hyrox Birmingham - Open Men's Doubles", roxfitDoubles, hrRecords(4529, func(int) int32 { return 160 }), 4529)
	b, err := BuildActivity(sc, &pbactivity.ShowcaseProfileEntry{ShowcaseId: "x"}, raceStart)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Race     *Race     `json:"race"`
		KmSplits []KmSplit `json:"kmSplits"`
		Series   Series    `json:"series"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Race == nil || len(out.Race.Segments) != 16 {
		t.Fatalf("race = %+v", out.Race)
	}
	if out.KmSplits != nil {
		t.Error("crossfit has no km splits")
	}
	prev := -1.0
	for _, e := range out.Series.ElapsedS {
		if e == nil {
			continue
		}
		if *e < prev {
			t.Fatalf("series not in time order: %v after %v", *e, prev)
		}
		prev = *e
	}
}
