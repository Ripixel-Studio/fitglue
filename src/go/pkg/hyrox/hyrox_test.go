package hyrox

import (
	"testing"
)

const roxfitSolo = `Completed via ROXFIT

🏆 Division: Open Men
📊 Overall Rank: 270
🎯 Age Group Rank: 60
Average pace: 05:48/km
Average run: 5:23

⏱️ Split Times:
Run 1: 5:39
Ski Erg: 4:30
Run 2: 5:12
Sled Push: 3:34
Run 3: 5:16
Sled Pull: 4:48
Run 4: 5:28
Burpee Broad Jump: 6:13
Run 5: 5:37
Row Erg: 4:44
Run 6: 5:15
Farmers Carry: 1:37
Run 7: 5:16
Sandbag Lunge: 4:51
Run 8: 5:27
Wall Ball: 8:59
Roxzone: 7:24

🏃 Total Running: 43:10

Two races within 20hrs of each other?!`

const fitglueBlock = `I am beyonddddd proud of myself

🏁 Hyrox - Men's Open:
🏃 Run 1: 4:49
⛷️ SkiErg: 4:18
🏃 Run 2: 5:35
🛷 Sled Push: 2:55 (152kg)
🏃 Run 3: 5:34
🛷 Sled Pull: 4:54 (103kg)
🏃 Run 4: 5:34
🏋️ Burpee Broad Jump: 6:07
🏃 Run 5: 5:39
🚣 Rowing (Machine): 4:40
🏃 Run 6: 6:23
🧳 Farmers Carry: 2:31 (48kg)
🏃 Run 7: 5:14
🎒 Sandbag Lunges: 5:41 (20kg)
🏃 Run 8: 6:59
🏐 Wall Balls: 7:01 (100 reps @ 6kg)
⏱️ Total: 1:24:02

🏆 Personal Records:`

func TestParseSplits_Official(t *testing.T) {
	s, ok := ParseSplits(roxfitSolo)
	if !ok || s.Source != SourceOfficial {
		t.Fatalf("ok=%v source=%q", ok, s.Source)
	}
	if len(s.Segments) != 16 {
		t.Fatalf("segments = %d, want 16", len(s.Segments))
	}
	if s.RoxzoneSeconds != 444 {
		t.Errorf("roxzone = %v, want 444", s.RoxzoneSeconds)
	}
	want := []struct {
		key   string
		run   bool
		secs  float64
		label string
	}{{"run_1", true, 339, "Run 1"}, {"skierg", false, 270, "SkiErg"}, {"rowing", false, 284, "Row"}, {"run_8", true, 327, "Run 8"}, {"wall_balls", false, 539, "Wall Balls"}}
	idx := map[string]Segment{}
	for _, seg := range s.Segments {
		idx[seg.Key] = seg
	}
	for _, w := range want {
		got := idx[w.key]
		if got.IsRun != w.run || got.Seconds != w.secs || got.Label != w.label {
			t.Errorf("%s = %+v", w.key, got)
		}
	}
	if s.RunSeconds() != 2590 || s.StationSeconds() != 2356 {
		t.Errorf("runs %v stations %v", s.RunSeconds(), s.StationSeconds())
	}
}

func TestParseSplits_FitGlueBlock(t *testing.T) {
	s, ok := ParseSplits(fitglueBlock)
	if !ok || s.Source != SourceDescription {
		t.Fatalf("ok=%v source=%q", ok, s.Source)
	}
	if len(s.Segments) != 16 || s.RoxzoneSeconds != 0 {
		t.Fatalf("segments=%d roxzone=%v", len(s.Segments), s.RoxzoneSeconds)
	}
	if s.Segments[3].Key != "sled_push" || s.Segments[3].Seconds != 175 {
		t.Errorf("sled push = %+v", s.Segments[3])
	}
	if s.Segments[15].Key != "wall_balls" || s.Segments[15].Seconds != 421 {
		t.Errorf("wall balls = %+v", s.Segments[15])
	}
}

func TestParseSplits_SimulationBlockWithDistances(t *testing.T) {
	desc := "🏁 Hyrox - Male Single:\n🏃 Run 1: 5:54 (1.1km)\n⛷️ SkiErg: 4:21 (87m)\n🏐 Wall Balls: 4:47 (81m @ 9kg)\n⏱️ Total: 1:19:17"
	s, ok := ParseSplits(desc)
	if !ok || len(s.Segments) != 3 || s.Segments[0].Seconds != 354 {
		t.Fatalf("ok=%v %+v", ok, s.Segments)
	}
}

func TestParseSplits_Absent(t *testing.T) {
	for _, d := range []string{"", "Easy run", "🏁 Hyrox Class:\nGreat fun"} {
		if _, ok := ParseSplits(d); ok {
			t.Errorf("%q parsed", d)
		}
	}
}

func TestParseDivision(t *testing.T) {
	cases := map[string]string{
		"Open Men":                              "men_open",
		"Men":                                   "men_open",
		"Doubles Men":                           "men_doubles",
		"Doubles Mixed":                         "mixed_doubles",
		"Mixed Doubles":                         "mixed_doubles",
		"Pro Men":                               "men_pro",
		"Pro Doubles Men":                       "men_pro_doubles",
		"Doubles Men Pro":                       "men_pro_doubles",
		"Doubles Women":                         "women_doubles",
		"Open Women":                            "women_open",
		"Hyrox Birmingham - Open Men's Solo":    "men_open",
		"Hyrox Birmingham - Open Men's Doubles": "men_doubles",
		" - Male Single":                        "men_open",
	}
	for in, want := range cases {
		d, ok := ParseDivision(in)
		if !ok || d.Key != want {
			t.Errorf("ParseDivision(%q) = %q,%v want %q", in, d.Key, ok, want)
		}
	}
	if _, ok := ParseDivision("HYROX - Rome - 01:12:34"); ok {
		t.Error("Rome title should name no division")
	}
}

func TestDetect_Precedence(t *testing.T) {
	// The official line wins over a title that says something else.
	d, src, ok := Detect("Hyrox Birmingham - Open Men's Solo", "🏆 Division: Doubles Men\n")
	if !ok || d.Key != "men_doubles" || src != "official" {
		t.Errorf("got %q %q %v", d.Key, src, ok)
	}
	d, src, ok = Detect("Hyrox - London Olympia 25/26 - 1:23:47 🎉", fitglueBlock)
	if !ok || d.Key != "men_open" || src != "description" {
		t.Errorf("olympia: %q %q %v", d.Key, src, ok)
	}
	d, src, ok = Detect("Hyrox Simulation - Open Mens Doubles", "")
	if !ok || d.Key != "men_doubles" || src != "title" {
		t.Errorf("title: %q %q %v", d.Key, src, ok)
	}
	if _, _, ok := Detect("HYROX - Rome - 01:12:34", "no splits"); ok {
		t.Error("nothing to detect")
	}
}

func TestDivisionPresetsRoundTrip(t *testing.T) {
	for _, d := range Divisions {
		got, ok := DivisionByPreset(d.PresetID)
		if !ok || got.Key != d.Key {
			t.Errorf("preset %s", d.PresetID)
		}
	}
}

func TestParseRanks(t *testing.T) {
	r := ParseRanks(roxfitSolo)
	if r.Overall != 270 || r.AgeGroup != 60 {
		t.Errorf("%+v", r)
	}
	if r := ParseRanks("📊 Overall Rank: N/A"); r.Overall != 0 || r.AgeGroup != 0 {
		t.Errorf("N/A: %+v", r)
	}
}

func TestTitleTimeAndEventName(t *testing.T) {
	if s, ok := TitleTime("HYROX - Rome - 01:12:34"); !ok || s != 4354 {
		t.Errorf("rome %v", s)
	}
	if s, ok := TitleTime("Hyrox - London Olympia 25/26 - 1:23:47 🎉"); !ok || s != 5027 {
		t.Errorf("olympia %v", s)
	}
	if _, ok := TitleTime("Hyrox Birmingham - Open Men's Solo"); ok {
		t.Error("no time")
	}
	names := map[string]string{
		"Hyrox Birmingham - Open Men's Solo":       "Birmingham",
		"Hyrox Birmingham - Open Men's Doubles":    "Birmingham",
		"HYROX - Rome - 01:12:34":                  "Rome",
		"Hyrox - London Olympia 25/26 - 1:23:47 🎉": "London Olympia",
		"Hyrox Sim": "",
	}
	for in, want := range names {
		if got := EventName(in); got != want {
			t.Errorf("EventName(%q) = %q want %q", in, got, want)
		}
	}
}

func TestIsSimulationAndStationKey(t *testing.T) {
	if !IsSimulation("Hyrox Sim") || !IsSimulation("Hyrox Simulation - Open Mens Doubles") || IsSimulation("Hyrox Birmingham - Open Men's Solo") {
		t.Error("IsSimulation")
	}
	keys := map[string]string{
		"Ski Erg": "skierg", "SkiErg": "skierg", "Row Erg": "rowing", "Rowing Machine": "rowing",
		"Farmers Walk": "farmers_carry", "Walking Lunge (Dumbbell)": "sandbag_lunges", "Wall Ball": "wall_balls",
		"Burpees Broad Jump": "burpee_broad_jump", "Sled Push (Distance & Weight)": "sled_push", "Running (Outdoor)": "",
	}
	for in, want := range keys {
		if got := StationKey(in); got != want {
			t.Errorf("StationKey(%q) = %q want %q", in, got, want)
		}
	}
}
