package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_companion"
	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

type fakeSummaries struct {
	acts    map[string]*activity.ActivitySummary
	entries []*pbactivity.ShowcaseProfileEntry
	write   func(a *activity.ActivitySummary) (string, error)
	written []string
	saved   map[string]string
}

func (f *fakeSummaries) SummaryEntries(context.Context, string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	return f.entries, nil
}
func (f *fakeSummaries) LoadActivitySummary(_ context.Context, id string) (*activity.ActivitySummary, error) {
	a, ok := f.acts[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return a, nil
}
func (f *fakeSummaries) WriteActivitySummary(_ context.Context, a *activity.ActivitySummary) (string, error) {
	f.written = append(f.written, a.ShowcaseID)
	if f.write != nil {
		return f.write(a)
	}
	return "AI Summary:\nNew for " + a.ShowcaseID + ".", nil
}
func (f *fakeSummaries) SaveActivitySummary(_ context.Context, a *activity.ActivitySummary, text string, _ time.Time) error {
	if f.saved == nil {
		f.saved = map[string]string{}
	}
	f.saved[a.ShowcaseID] = text
	a.Old, a.Model = text, ai_companion.SummaryModel
	return nil
}

func summaryFixture() *fakeSummaries {
	f := &fakeSummaries{acts: map[string]*activity.ActivitySummary{}}
	add := func(id string, day int, old, model string, data bool) {
		start := time.Date(2026, 11, day, 7, 0, 0, 0, time.UTC)
		a := &activity.ActivitySummary{ShowcaseID: id, Title: id, StartTime: start, Old: old, Model: model}
		if data {
			a.Activity = &pbactivity.StandardizedActivity{Name: id, Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
		}
		f.acts[id] = a
		f.entries = append(f.entries, &pbactivity.ShowcaseProfileEntry{ShowcaseId: id, StartTime: timestamppb.New(start)})
	}
	add("hyrox", 11, "AI Summary:\nPlain word opener, third person, no name: the summary.", "", true)
	add("done", 10, "AI Summary:\nAlready the observer.", ai_companion.SummaryModel, true)
	add("gemini", 3, "AI Summary:\nJames crushed it!", "", true)
	add("never", 2, "", "", true)
	add("nodata", 1, "AI Summary:\nOld.", "", false)
	return f
}

func runSummaries(t *testing.T, f *fakeSummaries, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	fl, err := parseRegenSummariesFlags(args, &out)
	if err != nil {
		t.Fatal(err)
	}
	fl.interval = 0
	err = runRegenSummaries(context.Background(), fl, f, &out, func() time.Time { return time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC) })
	return out.String(), err
}

func TestRegenSummariesDryRunWritesNothing(t *testing.T) {
	f := summaryFixture()
	out, err := runSummaries(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.saved) != 0 {
		t.Fatalf("dry run saved %v", f.saved)
	}
	for _, want := range []string{"5 activities", "1 already current", "3 to rewrite", "1 with no activity data", "tokens", "dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Join(f.written, " ") != "hyrox gemini never" {
		t.Errorf("sampled %v (newest first, never a current one)", f.written)
	}
	if !strings.Contains(out, "James crushed it!") || !strings.Contains(out, "New for gemini.") {
		t.Errorf("sample lacks old/new text:\n%s", out)
	}
}

func TestRegenSummariesApplyIsIdempotent(t *testing.T) {
	f := summaryFixture()
	if _, err := runSummaries(t, f, "--apply"); err != nil {
		t.Fatal(err)
	}
	if len(f.saved) != 3 || f.saved["done"] != "" || f.saved["nodata"] != "" {
		t.Fatalf("saved %v", f.saved)
	}
	if f.saved["never"] == "" {
		t.Error("an activity that never had a summary gets one")
	}
	f.written, f.saved = nil, nil
	out, err := runSummaries(t, f, "--apply")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.written) != 0 || !strings.Contains(out, "0 to rewrite") {
		t.Errorf("re-run must skip everything: %v\n%s", f.written, out)
	}
	if _, err := runSummaries(t, f, "--apply", "--force", "--id", "done"); err != nil || f.saved["done"] == "" {
		t.Errorf("--force rewrites a current one: %v %v", err, f.saved)
	}
}

func TestRegenSummariesFilters(t *testing.T) {
	f := summaryFixture()
	if _, err := runSummaries(t, f, "--apply", "--since", "2026-11-03"); err != nil {
		t.Fatal(err)
	}
	if len(f.saved) != 2 || f.saved["hyrox"] == "" || f.saved["gemini"] == "" {
		t.Errorf("--since: %v", f.saved)
	}
	f = summaryFixture()
	if _, err := runSummaries(t, f, "--apply", "--limit", "1"); err != nil {
		t.Fatal(err)
	}
	if len(f.saved) != 1 || f.saved["hyrox"] == "" {
		t.Errorf("--limit takes the newest: %v", f.saved)
	}
}

func TestRegenSummariesStopsOnLimitAndKeepsDrops(t *testing.T) {
	f := summaryFixture()
	f.write = func(a *activity.ActivitySummary) (string, error) {
		switch a.ShowcaseID {
		case "hyrox":
			return "", nil // voice guard dropped it: keep the old text
		case "gemini":
			return "", errors.New("boom")
		case "never":
			return "", claude.ErrLimited
		}
		return "AI Summary:\nFresh.", nil
	}
	out, err := runSummaries(t, f, "--apply")
	if !errors.Is(err, claude.ErrLimited) {
		t.Fatalf("a spent window stops the run: %v", err)
	}
	if len(f.saved) != 0 {
		t.Errorf("drops and errors keep the old text: %v", f.saved)
	}
	if !strings.Contains(out, "re-run") {
		t.Errorf("tell the operator to re-run:\n%s", out)
	}
}

func TestRegenSummariesFlagErrors(t *testing.T) {
	for _, args := range [][]string{{"--since", "last tuesday"}, {"--slug", ""}} {
		if _, err := parseRegenSummariesFlags(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
}
