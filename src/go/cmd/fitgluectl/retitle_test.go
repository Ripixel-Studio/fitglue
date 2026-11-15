package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fakeRetitle struct {
	entries   []retitleEntry
	showcases map[string]string
	counters  map[string]int64
	writes    int
}

func (f *fakeRetitle) Entries(context.Context, string) ([]retitleEntry, error) {
	return append([]retitleEntry(nil), f.entries...), nil
}
func (f *fakeRetitle) ShowcaseTitle(_ context.Context, id string) (string, bool, error) {
	t, ok := f.showcases[id]
	return t, ok, nil
}
func (f *fakeRetitle) SetEntryTitle(_ context.Context, _, id, t string) error {
	f.writes++
	for i := range f.entries {
		if f.entries[i].ID == id {
			f.entries[i].Title = t
		}
	}
	return nil
}
func (f *fakeRetitle) SetShowcaseTitle(_ context.Context, id, t string) error {
	f.writes++
	f.showcases[id] = t
	return nil
}
func (f *fakeRetitle) Counter(_ context.Context, _, key string) (int64, bool, error) {
	n, ok := f.counters[key]
	return n, ok, nil
}
func (f *fakeRetitle) SetCounter(_ context.Context, _, key string, n int64) error {
	f.writes++
	f.counters[key] = n
	return nil
}

func at(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func newFakeRetitle() *fakeRetitle {
	return &fakeRetitle{
		entries: []retitleEntry{
			// listed out of order: numbering is by start time
			{ID: "xmas", Title: "Newark Parkrun - Christmas Day Edition (#3)", Start: at("2025-12-25T09:00:00Z")},
			{ID: "first", Title: "Newark parkrun", Start: at("2025-12-06T09:00:00Z")},
			{ID: "second", Title: "Southwell parkrun (#2)", Start: at("2025-12-13T09:00:00Z")},
			{ID: "nyd", Title: "Newark parkrun (#9)", Start: at("2026-01-01T09:00:00Z")},
			{ID: "club1", Title: "Bingham Sunday Running Club (#7)", Start: at("2025-12-07T08:00:00Z")},
			{ID: "club2", Title: "Bingham Sunday Running Club", Start: at("2025-12-14T08:00:00Z")},
			{ID: "other", Title: "Hyrox Open Men's Doubles - Birmingham - 01:15:29", Start: at("2025-12-10T10:00:00Z")},
		},
		showcases: map[string]string{
			"xmas": "Newark Parkrun - Christmas Day Edition (#3)", "first": "Newark parkrun",
			"second": "Southwell parkrun (#2)", "nyd": "Newark parkrun (#9)",
			"club1": "Bingham Sunday Running Club (#1)", "other": "Hyrox",
		},
		counters: map[string]int64{"parkrun": 9, "bingham_sunday_running_club": 2},
	}
}

func TestRetitle_PlanApplyThenNothing(t *testing.T) {
	ctx := context.Background()
	store := newFakeRetitle()
	var out bytes.Buffer
	if err := retitleCommand(ctx, []string{"--uid", "u"}, &out, &out, func(context.Context, string) (retitleStore, func(), error) {
		return store, func() {}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 {
		t.Fatalf("dry run wrote %d times", store.writes)
	}
	for _, want := range []string{
		"first: Newark parkrun → Newark parkrun (#1)",
		"xmas: Newark Parkrun - Christmas Day Edition (#3) → Newark parkrun (#3) - Christmas Special",
		"nyd: Newark parkrun (#9) → Newark parkrun (#4) - New Year's Day Special",
		"club1: Bingham Sunday Running Club (#7) (showcase: \"Bingham Sunday Running Club (#1)\") → Bingham Sunday Running Club (#1)",
		"club2: Bingham Sunday Running Club (no showcased activity) → Bingham Sunday Running Club (#2)",
		"counter parkrun: 9 → 4",
		"5 titles, 1 counters would change",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "second:") || strings.Contains(out.String(), "other:") {
		t.Errorf("unchanged or unrelated activity listed:\n%s", out.String())
	}

	out.Reset()
	open := func(context.Context, string) (retitleStore, func(), error) { return store, func() {}, nil }
	if err := retitleCommand(ctx, []string{"--uid", "u", "--apply"}, &out, &out, open); err != nil {
		t.Fatal(err)
	}
	if store.showcases["xmas"] != "Newark parkrun (#3) - Christmas Special" || store.counters["parkrun"] != 4 {
		t.Errorf("apply: showcases %v counters %v", store.showcases, store.counters)
	}
	if _, ok := store.showcases["club2"]; ok {
		t.Error("apply created a showcased activity")
	}

	out.Reset()
	if err := retitleCommand(ctx, []string{"--uid", "u", "--json"}, &out, &out, open); err != nil {
		t.Fatal(err)
	}
	var plan retitlePlan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 0 || len(plan.Counters) != 0 {
		t.Errorf("after apply, plan = %+v", plan)
	}
}

func TestParkrunPlaceFromTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Newark parkrun (#13) - Christmas Special":    "Newark",
		"Newark Parkrun - Christmas Day Edition (#3)": "Newark",
		"Colwick Park parkrun":                        "Colwick Park",
	} {
		if got := parkrunPlaceFromTitle(in); got != want {
			t.Errorf("parkrunPlaceFromTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
