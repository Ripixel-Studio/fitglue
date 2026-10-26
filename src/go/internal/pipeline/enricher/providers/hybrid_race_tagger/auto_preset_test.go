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
