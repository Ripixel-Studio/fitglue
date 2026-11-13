package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/fitglue/server/src/go/pkg/paidevents"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_activity_type"
	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

type fakeClassifyStore struct {
	acts    map[string]*activity.ActivitySummary
	entries []*pbactivity.ShowcaseProfileEntry
	asks    int
	saves   map[string]*pbactivity.ActivityClassification
	answer  func(in ai_activity_type.Inputs) (*pbactivity.ActivityClassification, error)
	paid    []paidevents.Event
}

func (f *fakeClassifyStore) SummaryEntries(context.Context, string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	return f.entries, nil
}

func (f *fakeClassifyStore) LoadActivitySummary(_ context.Context, id string) (*activity.ActivitySummary, error) {
	a, ok := f.acts[id]
	if !ok {
		return nil, fmt.Errorf("showcase %s not found", id)
	}
	return a, nil
}

func (f *fakeClassifyStore) Classify(_ context.Context, in ai_activity_type.Inputs) (*pbactivity.ActivityClassification, error) {
	f.asks++
	return f.answer(in)
}

func (f *fakeClassifyStore) PaidEvents(context.Context) ([]paidevents.Event, error) {
	return f.paid, nil
}

func (f *fakeClassifyStore) SaveClassification(_ context.Context, a *activity.ActivitySummary, c *pbactivity.ActivityClassification, e *pbactivity.ShowcaseProfileEntry) error {
	f.saves[a.ShowcaseID] = c
	a.Classification = c
	e.Tag, e.IsRace = c.Tag, c.IsRace
	return nil
}

func newFakeClassifyStore() *fakeClassifyStore {
	f := &fakeClassifyStore{acts: map[string]*activity.ActivitySummary{}, saves: map[string]*pbactivity.ActivityClassification{}}
	add := func(id, title string, typ pbactivity.ActivityType, day int) {
		start := time.Date(2026, 10, day, 9, 0, 0, 0, time.UTC)
		f.acts[id] = &activity.ActivitySummary{ShowcaseID: id, Title: title, StartTime: start, ActivityType: typ,
			Activity: &pbactivity.StandardizedActivity{Type: typ, Sessions: []*pbactivity.Session{{TotalElapsedTime: 1500, TotalDistance: 5000}}}}
		f.entries = append(f.entries, &pbactivity.ShowcaseProfileEntry{ShowcaseId: id, Title: title, ActivityType: typ, StartTime: timestamppb.New(start)})
	}
	add("riverside-parkrun-2026-10-03", "Riverside parkrun", pbactivity.ActivityType_ACTIVITY_TYPE_RUN, 3)
	add("riverside-parkrun-2026-10-10", "Riverside parkrun", pbactivity.ActivityType_ACTIVITY_TYPE_RUN, 10)
	add("hyrox-sim-2026-10-11", "Hyrox Sim", pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT, 11)
	f.answer = func(in ai_activity_type.Inputs) (*pbactivity.ActivityClassification, error) {
		c := &pbactivity.ActivityClassification{Tag: "run", IsRace: true, RaceWhy: "parkrun effort", StravaType: pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
			Model: ai_activity_type.Model, InputsHash: ai_activity_type.InputsHash(in)}
		if strings.Contains(in.Title, "Hyrox") {
			c.Tag, c.IsRace, c.RaceWhy = "hybrid", false, "sim, training"
		}
		return c, nil
	}
	return f
}

func classifyFlagsFor(t *testing.T, args ...string) *classifyFlags {
	t.Helper()
	f, err := parseClassifyFlags(append([]string{"--cache", filepath.Join(t.TempDir(), "cache.json"), "--interval", "0"}, args...), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestClassifyDryRunSavesNothing(t *testing.T) {
	st := newFakeClassifyStore()
	var out bytes.Buffer
	if err := runClassify(context.Background(), classifyFlagsFor(t), st, &out, time.Now); err != nil {
		t.Fatal(err)
	}
	if st.asks != 3 || len(st.saves) != 0 {
		t.Errorf("asks %d saves %d", st.asks, len(st.saves))
	}
	for _, want := range []string{"hybrid    1", "run       2", "isRace=true (2)", "parkrun effort", "dry run"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report misses %q:\n%s", want, out.String())
		}
	}
}

// --apply saves; a second run skips everything already classified; --force redoes it.
func TestClassifyIdempotent(t *testing.T) {
	st := newFakeClassifyStore()
	f := classifyFlagsFor(t, "--apply")
	if err := runClassify(context.Background(), f, st, io.Discard, time.Now); err != nil {
		t.Fatal(err)
	}
	if len(st.saves) != 3 || st.entries[2].Tag != "hybrid" || !st.entries[0].IsRace {
		t.Fatalf("saves %d entries %v", len(st.saves), st.entries)
	}
	st.saves = map[string]*pbactivity.ActivityClassification{}
	asks := st.asks
	var out bytes.Buffer
	if err := runClassify(context.Background(), f, st, &out, time.Now); err != nil {
		t.Fatal(err)
	}
	if st.asks != asks || len(st.saves) != 0 || !strings.Contains(out.String(), "3 already classified") {
		t.Errorf("second run asked %d / saved %d:\n%s", st.asks-asks, len(st.saves), out.String())
	}
	f.force = true
	if err := runClassify(context.Background(), f, st, io.Discard, time.Now); err != nil {
		t.Fatal(err)
	}
	if len(st.saves) != 3 {
		t.Errorf("--force saved %d", len(st.saves))
	}
}

// A dry run's verdicts are reused by --apply without asking again.
func TestClassifyApplyReusesDryRunCache(t *testing.T) {
	st := newFakeClassifyStore()
	f := classifyFlagsFor(t)
	if err := runClassify(context.Background(), f, st, io.Discard, time.Now); err != nil {
		t.Fatal(err)
	}
	f.apply = true
	if err := runClassify(context.Background(), f, st, io.Discard, time.Now); err != nil {
		t.Fatal(err)
	}
	if st.asks != 3 || len(st.saves) != 3 {
		t.Errorf("asks %d saves %d", st.asks, len(st.saves))
	}
}

// A rule fallback is saved but not counted as classified, so the next run retries it.
func TestClassifyRetriesRuleFallbacks(t *testing.T) {
	st := newFakeClassifyStore()
	st.answer = func(in ai_activity_type.Inputs) (*pbactivity.ActivityClassification, error) {
		return ai_activity_type.RuleClassification(in), claude.ErrLimited
	}
	f := classifyFlagsFor(t, "--apply")
	err := runClassify(context.Background(), f, st, io.Discard, time.Now)
	if err == nil {
		t.Fatal("a spent usage window must be reported")
	}
	asks := st.asks
	_ = runClassify(context.Background(), f, st, io.Discard, time.Now)
	if st.asks == asks {
		t.Error("rule fallbacks were not retried")
	}
}

// Parkrun history: each parkrun is judged against the ones before it.
func TestClassifyParkrunHistory(t *testing.T) {
	st := newFakeClassifyStore()
	var seen [][]int
	inner := st.answer
	st.answer = func(in ai_activity_type.Inputs) (*pbactivity.ActivityClassification, error) {
		if strings.Contains(in.Title, "parkrun") {
			seen = append(seen, in.UsualParkrunSeconds)
		}
		return inner(in)
	}
	if err := runClassify(context.Background(), classifyFlagsFor(t), st, io.Discard, time.Now); err != nil {
		t.Fatal(err)
	}
	var withHistory int
	for _, s := range seen {
		if len(s) == 1 && s[0] == 1500 {
			withHistory++
		}
	}
	if len(seen) != 2 || withHistory != 1 {
		t.Errorf("parkrun histories %v", seen)
	}
}

func TestClassifyShowsThePaidEventsOnTheDay(t *testing.T) {
	st := newFakeClassifyStore()
	st.paid = []paidevents.Event{{Date: "2026-10-11", Name: "Hyrox Sim"}}
	seen := map[string][]string{}
	answer := st.answer
	st.answer = func(in ai_activity_type.Inputs) (*pbactivity.ActivityClassification, error) {
		seen[in.Title] = in.PaidEvents
		return answer(in)
	}
	if err := runClassify(context.Background(), classifyFlagsFor(t), st, io.Discard, time.Now); err != nil {
		t.Fatal(err)
	}
	if got := seen["Hyrox Sim"]; len(got) != 1 || got[0] != "Hyrox Sim" {
		t.Errorf("the sim's day: %v", got)
	}
	if got := seen["Riverside parkrun"]; len(got) != 0 {
		t.Errorf("a day with no paid event: %v", got)
	}
}

func TestClassifyRaceOverrideByHand(t *testing.T) {
	st := newFakeClassifyStore()
	f := classifyFlagsFor(t, "--id", "hyrox-sim-2026-10-11", "--force", "--apply", "--race", "yes", "--race-type", "hyrox", "--race-variant", "Open Men's Doubles")
	if err := runClassify(context.Background(), f, st, io.Discard, time.Now); err != nil {
		t.Fatal(err)
	}
	c := st.saves["hyrox-sim-2026-10-11"]
	if c == nil || !c.IsRace || c.RaceType != "hyrox" || c.RaceVariant != "Open Men's Doubles" {
		t.Errorf("override not saved: %+v", c)
	}
	if _, err := parseClassifyFlags([]string{"--race", "yes"}, io.Discard); err == nil {
		t.Error("--race without --id must fail")
	}
}
