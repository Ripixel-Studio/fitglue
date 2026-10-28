package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

type entryRecorder struct {
	written []*pbactivity.ShowcaseProfileEntry
}

func (w *entryRecorder) SetShowcaseProfileEntry(_ context.Context, _ string, e *pbactivity.ShowcaseProfileEntry) error {
	w.written = append(w.written, e)
	return nil
}

// The stub's run carries Strava's 1,346 kcal; its entry holds the old estimate (523).
func calorieStub() stubSource {
	s := newStubSource()
	old := int32(523)
	s.entries[0].CaloriesKcal = &old
	strava := 1346.0
	s.showcase.ActivityData = &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{TotalCalories: &strava}}}
	return s
}

func TestFixCalories_DryRunPlansAndWritesNothing(t *testing.T) {
	f, err := parseFixCaloriesFlags([]string{"--slug", "james"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := &entryRecorder{}
	if err := runFixCalories(context.Background(), f, calorieStub(), w, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2026-10-17 run-2026-10-17: 523 → 1346 kcal", "1 entries would change", "week-42-2026"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if len(w.written) != 0 {
		t.Errorf("dry run wrote %d entries", len(w.written))
	}
}

func TestFixCalories_ApplyWritesOnlyChangedEntries(t *testing.T) {
	f, err := parseFixCaloriesFlags([]string{"--slug", "james", "--apply"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	w := &entryRecorder{}
	if err := runFixCalories(context.Background(), f, calorieStub(), w, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(w.written) != 1 || w.written[0].GetCaloriesKcal() != 1346 {
		t.Fatalf("written %+v", w.written)
	}

	// An activity without platform calories keeps its estimate and is not written.
	s := newStubSource()
	w = &entryRecorder{}
	if err := runFixCalories(context.Background(), f, s, w, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(w.written) != 0 {
		t.Errorf("wrote an entry with no platform calories: %+v", w.written)
	}
}
