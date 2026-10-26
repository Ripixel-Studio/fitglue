package hybrid_race_tagger

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/user_input"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

func expectWaitForInput(t *testing.T, err error) {
	t.Helper()
	var waitErr *user_input.WaitForInputError
	if !errors.As(err, &waitErr) {
		t.Fatalf("expected *WaitForInputError, got %T: %v", err, err)
	}
	if len(waitErr.RequiredFields) == 0 || waitErr.RequiredFields[0] != "race_selection" {
		t.Errorf("expected race_selection required field, got %v", waitErr.RequiredFields)
	}
}

// Keyword in the title + a valid auto_preset: stations are produced on the first run,
// with no pending input raised.
func TestHRT_Enrich_AutoPreset_KeywordInTitle(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{Name: "HYROX London 2026", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	inputs := map[string]string{"auto_preset": "hyrox_men_open", "auto_keywords": "hyrox"}

	res, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	if err != nil {
		t.Fatalf("expected no error (and no WaitForInputError), got %T: %v", err, err)
	}
	if res.Metadata["status"] != "success" {
		t.Fatalf("expected success, got %v", res.Metadata)
	}
	if res.Metadata["preset"] != "Hyrox - Men's Open" || res.Metadata["race_type"] != "hyrox" {
		t.Errorf("unexpected preset metadata: %v", res.Metadata)
	}
	if res.HybridRaceSummary == nil || len(res.HybridRaceSummary.Segments) != 16 {
		t.Errorf("expected 16 station segments, got %+v", res.HybridRaceSummary)
	}
	if len(a.Sessions[0].StrengthSets) != 16 {
		t.Errorf("expected 16 strength sets, got %d", len(a.Sessions[0].StrengthSets))
	}
	if a.Sessions[0].Laps[1].ExerciseName != "SkiErg" {
		t.Errorf("expected lap 1 mapped to SkiErg, got %q", a.Sessions[0].Laps[1].ExerciseName)
	}
	if len(res.Tags) != 1 || res.Tags[0] != "HYROX" {
		t.Errorf("expected HYROX tag, got %v", res.Tags)
	}
}

// The auto path must produce exactly what the pending-input continuation produces.
func TestHRT_Enrich_AutoPreset_MatchesEnrichResume(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	inputs := map[string]string{"auto_preset": "hyrox_women_pro"}

	auto := &pbactivity.StandardizedActivity{Name: "Hyrox Glasgow", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	autoRes, err := p.Enrich(context.Background(), slog.Default(), auto, hrtUser(), inputs, false)
	if err != nil {
		t.Fatalf("auto path: %v", err)
	}

	manual := &pbactivity.StandardizedActivity{Name: "Hyrox Glasgow", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	manualRes, err := p.applySelection(manual, UserSelection{PresetID: "hyrox_women_pro"})
	if err != nil {
		t.Fatalf("manual path: %v", err)
	}

	if autoRes.Description != manualRes.Description {
		t.Errorf("descriptions differ:\n%s\n---\n%s", autoRes.Description, manualRes.Description)
	}
	for k, v := range manualRes.Metadata {
		if autoRes.Metadata[k] != v {
			t.Errorf("metadata %q: auto=%q manual=%q", k, autoRes.Metadata[k], v)
		}
	}
	if auto.Sessions[0].TotalDistance != manual.Sessions[0].TotalDistance {
		t.Errorf("total distance differs: auto=%v manual=%v", auto.Sessions[0].TotalDistance, manual.Sessions[0].TotalDistance)
	}
}

// Keyword matching is case-insensitive, honours a comma-separated list, and looks at tags too.
func TestHRT_Enrich_AutoPreset_KeywordInTagAndList(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{
		Name:     "Saturday race",
		Tags:     []string{"Race", "HyRoX-Open"},
		Sessions: []*pbactivity.Session{hyroxLikeSession()},
	}
	inputs := map[string]string{"auto_preset": "hyrox_men_open", "auto_keywords": " athx , Hyrox "}

	res, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	if err != nil {
		t.Fatalf("expected auto-selection from tag, got %T: %v", err, err)
	}
	if res.Metadata["status"] != "success" {
		t.Errorf("expected success, got %v", res.Metadata)
	}
}

// auto_keywords omitted defaults to "hyrox".
func TestHRT_Enrich_AutoPreset_DefaultKeyword(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{Name: "hyrox manchester", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	inputs := map[string]string{"auto_preset": "hyrox_men_open"}

	res, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	if err != nil {
		t.Fatalf("expected auto-selection with default keyword, got %T: %v", err, err)
	}
	if res.Metadata["status"] != "success" {
		t.Errorf("expected success, got %v", res.Metadata)
	}
}

// Structural heuristic matches but no configured keyword does: unchanged, still waits.
func TestHRT_Enrich_AutoPreset_NoKeywordStillWaits(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{Name: "Morning session", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	inputs := map[string]string{"auto_preset": "hyrox_men_open", "auto_keywords": "hyrox"}

	_, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	expectWaitForInput(t, err)
	if len(a.Sessions[0].StrengthSets) != 0 {
		t.Errorf("activity must be untouched when waiting, got %d strength sets", len(a.Sessions[0].StrengthSets))
	}
}

// Invalid auto_preset: warn and fall back to the pending-input flow.
func TestHRT_Enrich_AutoPreset_InvalidPresetWaits(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{Name: "HYROX London", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	inputs := map[string]string{"auto_preset": "hyrox_does_not_exist", "auto_keywords": "hyrox"}

	_, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	expectWaitForInput(t, err)
}

// No auto_preset at all: existing behaviour (keyword in title still waits for input).
func TestHRT_Enrich_AutoPreset_UnsetWaits(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{Name: "HYROX London", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	inputs := map[string]string{"auto_keywords": "hyrox"}

	_, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	expectWaitForInput(t, err)
}

// The no-laps skip still wins over auto-selection.
func TestHRT_Enrich_AutoPreset_NoLapsStillSkips(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{Name: "HYROX London"}
	inputs := map[string]string{"auto_preset": "hyrox_men_open"}

	res, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Metadata["reason"] != "no_laps" {
		t.Errorf("expected no_laps skip, got %v", res.Metadata)
	}
}

func TestAutoSelection_Table(t *testing.T) {
	cases := []struct {
		name   string
		act    *pbactivity.StandardizedActivity
		inputs map[string]string
		want   string
		wantOK bool
	}{
		{"nil inputs", &pbactivity.StandardizedActivity{Name: "hyrox"}, nil, "", false},
		{"empty preset", &pbactivity.StandardizedActivity{Name: "hyrox"}, map[string]string{"auto_preset": "  "}, "", false},
		{"unknown preset", &pbactivity.StandardizedActivity{Name: "hyrox"}, map[string]string{"auto_preset": "nope"}, "", false},
		{"title match default kw", &pbactivity.StandardizedActivity{Name: "HYROX"}, map[string]string{"auto_preset": "hyrox_men_open"}, "hyrox_men_open", true},
		{"tag match", &pbactivity.StandardizedActivity{Name: "x", Tags: []string{"athx"}}, map[string]string{"auto_preset": "hyrox_men_pro", "auto_keywords": "athx"}, "hyrox_men_pro", true},
		{"no match", &pbactivity.StandardizedActivity{Name: "run"}, map[string]string{"auto_preset": "hyrox_men_open"}, "", false},
		{"blank keyword list falls back to default", &pbactivity.StandardizedActivity{Name: "hyrox"}, map[string]string{"auto_preset": "hyrox_men_open", "auto_keywords": " , "}, "hyrox_men_open", true},
		{"preset id trimmed", &pbactivity.StandardizedActivity{Name: "hyrox"}, map[string]string{"auto_preset": " hyrox_men_open "}, "hyrox_men_open", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := autoSelection(slog.Default(), tc.act, tc.inputs)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("got (%q,%v), want (%q,%v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// --- auto_preset_map -------------------------------------------------------------

const birminghamMap = "doubles=hyrox_doubles_men;solo=hyrox_men_open;pro=hyrox_men_pro"

func enrichAuto(t *testing.T, name string, inputs map[string]string) (*pbactivity.StandardizedActivity, map[string]string) {
	t.Helper()
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{Name: name, Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	res, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	if err != nil {
		t.Fatalf("expected auto-selection for %q, got %T: %v", name, err, err)
	}
	if res.Metadata["status"] != "success" {
		t.Fatalf("expected success for %q, got %v", name, res.Metadata)
	}
	return a, res.Metadata
}

// Doubles title (from the Fitness Events calendar) picks the doubles preset via the map.
func TestHRT_Enrich_AutoPresetMap_DoublesTitle(t *testing.T) {
	inputs := map[string]string{"auto_preset_map": birminghamMap, "auto_preset": "hyrox_men_open"}
	a, meta := enrichAuto(t, "Hyrox Birmingham - Open Men's Doubles", inputs)
	if meta["preset"] != "Hyrox - Men's Doubles" {
		t.Errorf("expected Men's Doubles, got %v", meta)
	}
	if len(a.Sessions[0].StrengthSets) != 16 {
		t.Errorf("expected 16 strength sets, got %d", len(a.Sessions[0].StrengthSets))
	}
}

// Solo title picks men's open via the map (the "pro" and "doubles" pairs do not match).
func TestHRT_Enrich_AutoPresetMap_SoloTitle(t *testing.T) {
	inputs := map[string]string{"auto_preset_map": birminghamMap}
	_, meta := enrichAuto(t, "Hyrox Birmingham - Open Men's Solo", inputs)
	if meta["preset"] != "Hyrox - Men's Open" {
		t.Errorf("expected Men's Open, got %v", meta)
	}
}

// No map pair matches but auto_preset is set: auto_preset applies.
func TestHRT_Enrich_AutoPresetMap_NoMatchFallsBackToAutoPreset(t *testing.T) {
	inputs := map[string]string{"auto_preset_map": birminghamMap, "auto_preset": "hyrox_mixed_doubles"}
	_, meta := enrichAuto(t, "Hyrox Birmingham - Relay", inputs)
	if meta["preset"] != "Hyrox - Mixed Doubles" {
		t.Errorf("expected auto_preset fallback (Mixed Doubles), got %v", meta)
	}
}

// No map pair matches and no auto_preset: unchanged behaviour, pending input.
func TestHRT_Enrich_AutoPresetMap_NoMatchNoFallbackWaits(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{Name: "Hyrox Birmingham - Relay", Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	inputs := map[string]string{"auto_preset_map": birminghamMap}

	_, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	expectWaitForInput(t, err)
	if len(a.Sessions[0].StrengthSets) != 0 {
		t.Errorf("activity must be untouched when waiting, got %d strength sets", len(a.Sessions[0].StrengthSets))
	}
}

// A pair with an unknown preset id is skipped; later pairs and the fallback still apply.
func TestHRT_Enrich_AutoPresetMap_BadIDSkipped(t *testing.T) {
	inputs := map[string]string{
		"auto_preset_map": "doubles=hyrox_doubles_nope;doubles=hyrox_doubles_men",
	}
	_, meta := enrichAuto(t, "Hyrox Birmingham - Open Men's Doubles", inputs)
	if meta["preset"] != "Hyrox - Men's Doubles" {
		t.Errorf("expected the valid doubles pair to win, got %v", meta)
	}

	inputs = map[string]string{
		"auto_preset_map": "doubles=hyrox_doubles_nope",
		"auto_preset":     "hyrox_men_open",
	}
	_, meta = enrichAuto(t, "Hyrox Birmingham - Open Men's Doubles", inputs)
	if meta["preset"] != "Hyrox - Men's Open" {
		t.Errorf("expected auto_preset fallback after bad pair, got %v", meta)
	}
}

// The map never widens detection: a title with a map keyword but no hybrid-race
// recognition (no auto_keywords hit, not hyrox-shaped) is skipped as before.
func TestHRT_Enrich_AutoPresetMap_NotARaceStillSkips(t *testing.T) {
	p := &HybridRaceTaggerProvider{}
	a := &pbactivity.StandardizedActivity{
		Name:     "Tennis doubles",
		Sessions: []*pbactivity.Session{{Laps: []*pbactivity.Lap{makeLap(600, 200, 0), makeLap(600, 200, 0)}, TotalDistance: 400}},
	}
	inputs := map[string]string{"auto_preset_map": birminghamMap, "auto_preset": "hyrox_men_open"}

	res, err := p.Enrich(context.Background(), slog.Default(), a, hrtUser(), inputs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Metadata["reason"] != "not_hybrid_race_candidate" {
		t.Errorf("expected not_hybrid_race_candidate skip, got %v", res.Metadata)
	}
}

func TestAutoSelection_Map_Table(t *testing.T) {
	hyroxShaped := func(name string, tags ...string) *pbactivity.StandardizedActivity {
		return &pbactivity.StandardizedActivity{Name: name, Tags: tags, Sessions: []*pbactivity.Session{hyroxLikeSession()}}
	}
	flat := func(name string, tags ...string) *pbactivity.StandardizedActivity {
		return &pbactivity.StandardizedActivity{Name: name, Tags: tags}
	}
	cases := []struct {
		name   string
		act    *pbactivity.StandardizedActivity
		inputs map[string]string
		want   string
		wantOK bool
	}{
		{"doubles via default keyword", flat("Hyrox Birmingham - Open Men's Doubles"), map[string]string{"auto_preset_map": birminghamMap}, "hyrox_doubles_men", true},
		{"solo via default keyword", flat("Hyrox Birmingham - Open Men's Solo"), map[string]string{"auto_preset_map": birminghamMap}, "hyrox_men_open", true},
		{"first pair wins in order", flat("Hyrox - Open Men's Doubles"), map[string]string{"auto_preset_map": "open=hyrox_men_open;doubles=hyrox_doubles_men"}, "hyrox_men_open", true},
		{"order reversed", flat("Hyrox - Open Men's Doubles"), map[string]string{"auto_preset_map": "doubles=hyrox_doubles_men;open=hyrox_men_open"}, "hyrox_doubles_men", true},
		{"comma separator, spaces, case", flat("HYROX Doubles"), map[string]string{"auto_preset_map": " Solo = hyrox_men_open , DOUBLES=hyrox_doubles_men "}, "hyrox_doubles_men", true},
		{"map keyword in tag", flat("Hyrox", "Doubles"), map[string]string{"auto_preset_map": birminghamMap}, "hyrox_doubles_men", true},
		{"structural gate, no keyword", hyroxShaped("Birmingham doubles race"), map[string]string{"auto_preset_map": birminghamMap}, "hyrox_doubles_men", true},
		{"built-in keyword gate (athx) with default auto_keywords", flat("ATHX doubles"), map[string]string{"auto_preset_map": birminghamMap}, "hyrox_doubles_men", true},
		{"no gate: map keyword alone is not enough", flat("Tennis doubles"), map[string]string{"auto_preset_map": birminghamMap, "auto_preset": "hyrox_men_open"}, "", false},
		{"no pair match, auto_preset fallback", flat("Hyrox Relay"), map[string]string{"auto_preset_map": birminghamMap, "auto_preset": "hyrox_men_pro"}, "hyrox_men_pro", true},
		{"no pair match, no fallback", flat("Hyrox Relay"), map[string]string{"auto_preset_map": birminghamMap}, "", false},
		{"no pair match, structural gate, auto_preset fallback", hyroxShaped("Birmingham race"), map[string]string{"auto_preset_map": birminghamMap, "auto_preset": "hyrox_men_pro"}, "hyrox_men_pro", true},
		{"bad id skipped, next pair wins", flat("Hyrox doubles"), map[string]string{"auto_preset_map": "doubles=nope;doubles=hyrox_doubles_men"}, "hyrox_doubles_men", true},
		{"malformed pair skipped", flat("Hyrox doubles"), map[string]string{"auto_preset_map": "doubles;=hyrox_men_open;doubles=;doubles=hyrox_doubles_men"}, "hyrox_doubles_men", true},
		{"all pairs bad behaves as unset (#93)", hyroxShaped("Morning session"), map[string]string{"auto_preset_map": "doubles=nope", "auto_preset": "hyrox_men_open"}, "", false},
		{"all pairs bad, keyword hit, auto_preset (#93)", flat("Hyrox doubles"), map[string]string{"auto_preset_map": "doubles=nope", "auto_preset": "hyrox_men_open"}, "hyrox_men_open", true},
		{"bad auto_preset does not block the map", flat("Hyrox doubles"), map[string]string{"auto_preset_map": birminghamMap, "auto_preset": "nope"}, "hyrox_doubles_men", true},
		{"custom auto_keywords gate", flat("Birmingham doubles", "race-day"), map[string]string{"auto_preset_map": birminghamMap, "auto_keywords": "race-day"}, "hyrox_doubles_men", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := autoSelection(slog.Default(), tc.act, tc.inputs)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("got (%q,%v), want (%q,%v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestParseAutoPresetMap(t *testing.T) {
	if got := parseAutoPresetMap(slog.Default(), ""); got != nil {
		t.Errorf("empty map should be nil, got %v", got)
	}
	if got := parseAutoPresetMap(slog.Default(), " ; , "); got != nil {
		t.Errorf("separator-only map should be nil, got %v", got)
	}
	got := parseAutoPresetMap(slog.Default(), birminghamMap)
	want := []presetRule{
		{"doubles", "hyrox_doubles_men"},
		{"solo", "hyrox_men_open"},
		{"pro", "hyrox_men_pro"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rule %d: got %v, want %v", i, got[i], want[i])
		}
	}
}
