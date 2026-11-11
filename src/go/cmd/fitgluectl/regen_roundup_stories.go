package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// `fitgluectl regen-roundup-stories` rewrites the "THE STORY" paragraph of stored
// roundups through the current Claude path as the observer (activity.WriteRoundupStory:
// voice guide, voice guard, no emoji), replacing the old Gemini third-person text.
// Only the story changes; the period's numbers stay as stored. Each saved story records
// its model and time (ai_summary_model / ai_summary_generated_at), so a re-run skips
// the ones already done and a partial run can simply be run again.
//
//	fitgluectl regen-roundup-stories                         # dry run: counts, cost estimate, 3 old/new samples
//	fitgluectl regen-roundup-stories --apply                 # rewrite every stale story
//	fitgluectl regen-roundup-stories --key week-44-2026 --force --apply
//	fitgluectl regen-roundup-stories --kind month --since 2026-01-01
//
// --apply saves each story as it is written and the site-export hook re-exports
// roundup/<key>.json, refreshes index.json and appends to the site-rebuild signal
// file (SITE_EXPORT_SIGNAL_FILE, default ~/.jarvis/home/site-changed).

// Token estimate inputs, calibrated on one measured call (2026-11-05, `claude -p
// --model sonnet --output-format json` with the October 2026 prompt): the alias
// resolved to claude-sonnet-5-5; 3,016 input tokens (~2 characters per token for
// the voice guide + data, including the CLI's own framing), 1,068 output tokens of
// which 756 were thinking at --effort low; $0.0227 at list price, the input billed
// as a 1-hour cache write ($4 / MTok, twice the $2 base) and output at $10 / MTok.
// The box pays through the shared Claude subscription window, not per token; the
// dollar figure is the API-equivalent, for scale.
const (
	charsPerToken        = 2.0
	storyOutputTokens    = 1100
	sonnetInputPerMTok   = 4.0
	sonnetOutputPerMTok  = 10.0
	defaultStoryInterval = 5 * time.Second
)

type regenStoriesFlags struct {
	export   exportSiteFlags
	key      string
	kind     pbactivity.RoundupPeriodType
	since    *time.Time
	force    bool
	apply    bool
	sample   int
	limit    int
	interval time.Duration
}

// storyStore is what the command needs: the stored roundups, and the
// service's write (Claude) and save (Firestore + site export) halves.
type storyStore interface {
	Roundups(ctx context.Context, slug string) ([]*pbactivity.ShowcaseRoundup, error)
	WriteRoundupStory(ctx context.Context, r *pbactivity.ShowcaseRoundup) (string, error)
	SaveRoundupStory(ctx context.Context, r *pbactivity.ShowcaseRoundup, text string, now time.Time) error
}

func parseRegenStoriesFlags(args []string, stderr io.Writer) (*regenStoriesFlags, error) {
	f := &regenStoriesFlags{}
	fs := flag.NewFlagSet("regen-roundup-stories", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var kind, since string
	fs.StringVar(&f.export.slug, "slug", "james", "showcase profile slug")
	fs.StringVar(&f.export.project, "project", defaultProject, "GCP project holding Firestore")
	fs.StringVar(&f.export.bucket, "bucket", "", "showcase-assets bucket (default $"+bucketEnv+", else <project>-showcase-assets)")
	fs.StringVar(&f.key, "key", "", "only this roundup (period key, e.g. week-44-2026)")
	fs.StringVar(&kind, "kind", "", "only week, month or year roundups")
	fs.StringVar(&since, "since", "", "only periods starting on or after this date (YYYY-MM-DD or RFC 3339)")
	fs.BoolVar(&f.force, "force", false, "rewrite stories already written by the current Claude path too")
	fs.BoolVar(&f.apply, "apply", false, "save the new stories (default: dry run)")
	fs.IntVar(&f.sample, "sample", 3, "dry run: write this many stories (not saved) to show old and new")
	fs.IntVar(&f.limit, "limit", 0, "with --apply, stop after this many (0: all)")
	fs.DurationVar(&f.interval, "interval", defaultStoryInterval, "pause between Claude calls (the window is shared with Jarvis)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if f.export.slug == "" || strings.ContainsAny(f.export.slug, "/ ") {
		return nil, fmt.Errorf("invalid --slug %q", f.export.slug)
	}
	switch kind {
	case "":
	case "week":
		f.kind = pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK
	case "month":
		f.kind = pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH
	case "year":
		f.kind = pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_YEAR
	default:
		return nil, fmt.Errorf("--kind must be week, month or year, not %q", kind)
	}
	if since != "" {
		t, err := time.Parse("2006-01-02", since)
		if err != nil {
			if t, err = time.Parse(time.RFC3339, since); err != nil {
				return nil, fmt.Errorf("--since: want YYYY-MM-DD or RFC 3339, got %q", since)
			}
		}
		t = t.UTC()
		f.since = &t
	}
	if f.export.bucket == "" {
		f.export.bucket = f.export.project + "-showcase-assets"
	}
	f.export.prefix = "site/" + f.export.slug
	f.export.dryRun = !f.apply
	f.export.concurrency = 4
	return f, nil
}

func (f *regenStoriesFlags) selects(r *pbactivity.ShowcaseRoundup) bool {
	if f.key != "" && r.PeriodKey != f.key {
		return false
	}
	if f.kind != pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_UNSPECIFIED && r.PeriodType != f.kind {
		return false
	}
	if f.since != nil && (r.PeriodStart == nil || r.PeriodStart.AsTime().Before(*f.since)) {
		return false
	}
	return true
}

// storyEstimate is the token and API-equivalent cost estimate for a batch.
type storyEstimate struct {
	calls                  int
	inputTokens, outTokens int
	usd                    float64
}

func estimateStories(rs []*pbactivity.ShowcaseRoundup) storyEstimate {
	var e storyEstimate
	for _, r := range rs {
		sys, prompt := activity.RoundupStoryPrompt(r)
		e.calls++
		e.inputTokens += int(float64(utf8.RuneCountInString(sys)+utf8.RuneCountInString(prompt))/charsPerToken + 0.5)
		e.outTokens += storyOutputTokens
	}
	e.usd = float64(e.inputTokens)/1e6*sonnetInputPerMTok + float64(e.outTokens)/1e6*sonnetOutputPerMTok
	return e
}

// sampleStories picks up to n roundups to show, one of each kind first.
func sampleStories(rs []*pbactivity.ShowcaseRoundup, n int) []*pbactivity.ShowcaseRoundup {
	var out []*pbactivity.ShowcaseRoundup
	seen := map[pbactivity.RoundupPeriodType]bool{}
	picked := map[string]bool{}
	for _, r := range rs {
		if len(out) < n && !seen[r.PeriodType] {
			seen[r.PeriodType] = true
			picked[r.PeriodKey] = true
			out = append(out, r)
		}
	}
	for _, r := range rs {
		if len(out) < n && !picked[r.PeriodKey] {
			picked[r.PeriodKey] = true
			out = append(out, r)
		}
	}
	return out
}

func runRegenStories(ctx context.Context, f *regenStoriesFlags, st storyStore, out io.Writer, now func() time.Time) error {
	all, err := st.Roundups(ctx, f.export.slug)
	if err != nil {
		return fmt.Errorf("roundups: %w", err)
	}
	var selected, todo []*pbactivity.ShowcaseRoundup
	current := 0
	for _, r := range all {
		if !f.selects(r) {
			continue
		}
		selected = append(selected, r)
		if activity.RoundupStoryIsCurrent(r) && !f.force {
			current++
			continue
		}
		todo = append(todo, r)
	}
	if f.apply && f.limit > 0 && len(todo) > f.limit {
		todo = todo[:f.limit]
	}
	legacy := 0
	for _, r := range todo {
		if r.AiSummary != "" && r.AiSummaryModel == "" {
			legacy++
		}
	}
	est := estimateStories(todo)
	fmt.Fprintf(out, "%d roundups stored for %s, %d selected: %d already current (%s), %d to rewrite (%d with an old unrecorded story, %d with none)\n",
		len(all), f.export.slug, len(selected), current, activity.RoundupStoryModel, len(todo), legacy, len(todo)-legacy)
	fmt.Fprintf(out, "estimate: %d Claude Sonnet calls (up to %d if every story needs the voice-guard retry), ~%d input + ~%d output tokens, ~$%.2f API-equivalent (paid from the shared Claude window)\n",
		est.calls, 2*est.calls, est.inputTokens, est.outTokens, est.usd)

	if !f.apply {
		for _, r := range sampleStories(todo, f.sample) {
			text, err := st.WriteRoundupStory(ctx, r)
			fmt.Fprintf(out, "\n== %s\nold: %s\n", r.PeriodKey, orNone(r.AiSummary))
			switch {
			case err != nil:
				fmt.Fprintf(out, "new: (Claude error: %v)\n", err)
				if errors.Is(err, claude.ErrLimited) {
					return err
				}
			case text == "":
				fmt.Fprintln(out, "new: (dropped by the voice guard twice; the old story would stay)")
			default:
				fmt.Fprintf(out, "new: %s\n", text)
			}
		}
		fmt.Fprintln(out, "\ndry run: nothing saved; --apply rewrites them")
		return nil
	}

	written, dropped, failed := 0, 0, 0
	for i, r := range todo {
		if i > 0 && f.interval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(f.interval):
			}
		}
		text, err := st.WriteRoundupStory(ctx, r)
		switch {
		case errors.Is(err, claude.ErrLimited):
			fmt.Fprintf(out, "%s: Claude usage window spent; stopping after %d written. Saved stories are kept; re-run the same command later to finish.\n", r.PeriodKey, written)
			return err
		case err != nil:
			failed++
			fmt.Fprintf(out, "%s: Claude error, old story kept: %v\n", r.PeriodKey, err)
			continue
		case text == "":
			dropped++
			fmt.Fprintf(out, "%s: dropped by the voice guard, old story kept\n", r.PeriodKey)
			continue
		}
		if err := st.SaveRoundupStory(ctx, r, text, now()); err != nil {
			return fmt.Errorf("%s: %w (after %d written; re-run to continue)", r.PeriodKey, err, written)
		}
		written++
		fmt.Fprintf(out, "%s: written (%d/%d)\n", r.PeriodKey, written, len(todo))
	}
	fmt.Fprintf(out, "done: %d written, %d dropped by the voice guard, %d Claude errors; the dropped and failed keep their old story and a re-run retries them\n", written, dropped, failed)
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
