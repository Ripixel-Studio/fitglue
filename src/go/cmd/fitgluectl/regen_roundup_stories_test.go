package main

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

type fakeStories struct {
	roundups []*pbactivity.ShowcaseRoundup
	write    func(r *pbactivity.ShowcaseRoundup) (string, error)
	written  []string
	saved    map[string]string
}

func (f *fakeStories) Roundups(context.Context, string) ([]*pbactivity.ShowcaseRoundup, error) {
	return f.roundups, nil
}
func (f *fakeStories) WriteRoundupStory(_ context.Context, r *pbactivity.ShowcaseRoundup) (string, error) {
	f.written = append(f.written, r.PeriodKey)
	if f.write != nil {
		return f.write(r)
	}
	return "New story for " + r.PeriodKey + ".", nil
}
func (f *fakeStories) SaveRoundupStory(_ context.Context, r *pbactivity.ShowcaseRoundup, text string, _ time.Time) error {
	if f.saved == nil {
		f.saved = map[string]string{}
	}
	f.saved[r.PeriodKey] = text
	r.AiSummary, r.AiSummaryModel = text, activity.RoundupStoryModel
	return nil
}

func rup(key string, pt pbactivity.RoundupPeriodType, start time.Time, summary, model string) *pbactivity.ShowcaseRoundup {
	return &pbactivity.ShowcaseRoundup{
		PeriodKey: key, PeriodType: pt, PeriodStart: timestamppb.New(start),
		AiSummary: summary, AiSummaryModel: model, TotalActivities: 5,
	}
}

func storyFixture() *fakeStories {
	wk, mo, yr := pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH, pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_YEAR
	return &fakeStories{roundups: []*pbactivity.ShowcaseRoundup{
		rup("week-44-2026", wk, time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), "Done in his voice.", activity.RoundupStoryModel),
		rup("week-43-2026", wk, time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), "James had a week.", ""),
		rup("month-10-2026", mo, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "October saw James commit.", ""),
		rup("week-02-2025", wk, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC), "", ""),
		rup("year-2025", yr, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), "2025 saw James.", ""),
	}}
}

func runStories(t *testing.T, f *fakeStories, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	fl, err := parseRegenStoriesFlags(args, &out)
	if err != nil {
		t.Fatal(err)
	}
	fl.interval = 0
	err = runRegenStories(context.Background(), fl, f, &out, func() time.Time { return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) })
	return out.String(), err
}

func TestRegenStoriesDryRunWritesNothing(t *testing.T) {
	f := storyFixture()
	out, err := runStories(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.saved) != 0 {
		t.Fatalf("dry run saved %v", f.saved)
	}
	for _, want := range []string{"5 roundups", "1 already current", "4 to rewrite", "tokens", "dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The sample shows old and new for up to 3, one of each kind where there is one.
	if len(f.written) != 3 {
		t.Errorf("sampled %v", f.written)
	}
	if !strings.Contains(out, "October saw James commit.") || !strings.Contains(out, "New story for month-10-2026.") {
		t.Errorf("sample lacks old/new text:\n%s", out)
	}
	if strings.Contains(strings.Join(f.written, " "), "week-44-2026") {
		t.Error("a current story is never sampled")
	}
}

func TestRegenStoriesApplyIsIdempotent(t *testing.T) {
	f := storyFixture()
	if _, err := runStories(t, f, "--apply"); err != nil {
		t.Fatal(err)
	}
	if len(f.saved) != 4 || f.saved["week-44-2026"] != "" {
		t.Fatalf("saved %v", f.saved)
	}
	f.written, f.saved = nil, nil
	out, err := runStories(t, f, "--apply")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.written) != 0 || !strings.Contains(out, "0 to rewrite") {
		t.Errorf("re-run must skip everything: %v\n%s", f.written, out)
	}
	if _, err := runStories(t, f, "--apply", "--force", "--key", "week-44-2026"); err != nil || f.saved["week-44-2026"] == "" {
		t.Errorf("--force rewrites a current story: %v %v", err, f.saved)
	}
}

func TestRegenStoriesFilters(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"key":   {[]string{"--key", "month-10-2026"}, "month-10-2026"},
		"kind":  {[]string{"--kind", "year"}, "year-2025"},
		"since": {[]string{"--since", "2026-01-01"}, "month-10-2026 week-43-2026"},
	}
	for name, c := range cases {
		f := storyFixture()
		if _, err := runStories(t, f, append(c.args, "--apply")...); err != nil {
			t.Fatal(name, err)
		}
		var got []string
		for _, r := range f.roundups {
			if _, ok := f.saved[r.PeriodKey]; ok {
				got = append(got, r.PeriodKey)
			}
		}
		if strings.Join(sortedCopy(got), " ") != c.want {
			t.Errorf("%s: saved %v, want %s", name, got, c.want)
		}
	}
}

func TestRegenStoriesStopsOnLimitAndSkipsDrops(t *testing.T) {
	f := storyFixture()
	f.write = func(r *pbactivity.ShowcaseRoundup) (string, error) {
		switch r.PeriodKey {
		case "week-43-2026":
			return "", nil // voice guard dropped it: keep the old story
		case "week-02-2025":
			return "", claude.ErrLimited
		}
		return "Fresh " + r.PeriodKey + ".", nil
	}
	out, err := runStories(t, f, "--apply")
	if !errors.Is(err, claude.ErrLimited) {
		t.Fatalf("a spent window stops the run: %v", err)
	}
	if _, ok := f.saved["week-43-2026"]; ok {
		t.Error("a dropped story must not overwrite the old one")
	}
	if _, ok := f.saved["year-2025"]; ok {
		t.Error("nothing after the limit")
	}
	if f.saved["month-10-2026"] == "" {
		t.Error("work before the limit is kept")
	}
	if !strings.Contains(out, "re-run") {
		t.Errorf("tell the operator to re-run:\n%s", out)
	}
}

func TestRegenStoriesFlagErrors(t *testing.T) {
	for _, args := range [][]string{{"--kind", "fortnight"}, {"--since", "last tuesday"}, {"--slug", ""}} {
		if _, err := parseRegenStoriesFlags(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
