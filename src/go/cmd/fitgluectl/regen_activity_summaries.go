package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_companion"
	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"github.com/fitglue/server/src/go/pkg/voice"
)

// `fitgluectl regen-activity-summaries` rewrites the AI summary of every
// showcased activity on the site through the live enricher's path
// (ai_companion.Summarize: the observer guide, Sonnet, voice.Check with the echo
// guard), replacing his-voice and Gemini-era text. Only the site copy changes:
// the showcase's enrichments.aiSummary (stamped with ai_companion.SummaryModel)
// and the AI_COMPANION entry in its applied enrichments, which the site needs to
// show it. His title, description and the activity data are untouched, and
// nothing is written to Strava or any other destination. A re-run skips the
// summaries already written by the current path, so a partial run is finished
// by running it again.
//
//	fitgluectl regen-activity-summaries                     # dry run: counts, cost estimate, 3 old/new samples
//	fitgluectl regen-activity-summaries --apply             # rewrite every stale summary, newest first
//	fitgluectl regen-activity-summaries --id half-hyrox-pt-2026-11-11 --force --apply
//
// --apply saves each summary as it is written; the site-export hook re-exports
// activity/<id>.json, refreshes index.json and appends to the site-rebuild
// signal file (SITE_EXPORT_SIGNAL_FILE, default ~/.jarvis/home/site-changed).
//
// The context is the enricher's: the stored activity, its description cut to
// the text before the AI block, and the FitGlue sections after it as the other
// enrichers' descriptions (the live enricher's Phase-2 input).

// summaryOutputTokens: a 280-character summary plus thinking at --effort low,
// scaled from the roundup story's measured 1,068 (regen_roundup_stories.go).
const (
	summaryOutputTokens    = 800
	defaultSummaryInterval = 3 * time.Second
	summaryLoadConcurrency = 8
)

type regenSummariesFlags struct {
	export   exportSiteFlags
	id       string
	since    *time.Time
	force    bool
	apply    bool
	sample   int
	limit    int
	interval time.Duration
}

// summaryStore is what the command needs: the profile's entries, each
// activity's stored summary, and the write (Claude) and save (Firestore + site
// export) halves.
type summaryStore interface {
	SummaryEntries(ctx context.Context, slug string) ([]*pbactivity.ShowcaseProfileEntry, error)
	LoadActivitySummary(ctx context.Context, showcaseID string) (*activity.ActivitySummary, error)
	WriteActivitySummary(ctx context.Context, a *activity.ActivitySummary) (string, error)
	SaveActivitySummary(ctx context.Context, a *activity.ActivitySummary, text string, now time.Time) error
}

func parseRegenSummariesFlags(args []string, stderr io.Writer) (*regenSummariesFlags, error) {
	f := &regenSummariesFlags{}
	fs := flag.NewFlagSet("regen-activity-summaries", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var since string
	fs.StringVar(&f.export.slug, "slug", "james", "showcase profile slug")
	fs.StringVar(&f.export.project, "project", defaultProject, "GCP project holding Firestore")
	fs.StringVar(&f.export.bucket, "bucket", "", "showcase-assets bucket (default <project>-showcase-assets)")
	fs.StringVar(&f.id, "id", "", "only this activity (showcase id, the site slug)")
	fs.StringVar(&since, "since", "", "only activities starting on or after this date (YYYY-MM-DD or RFC 3339)")
	fs.BoolVar(&f.force, "force", false, "rewrite summaries already written by the current path too")
	fs.BoolVar(&f.apply, "apply", false, "save the new summaries (default: dry run)")
	fs.IntVar(&f.sample, "sample", 3, "dry run: write this many summaries (not saved) to show old and new")
	fs.IntVar(&f.limit, "limit", 0, "with --apply, stop after this many, newest first (0: all)")
	fs.DurationVar(&f.interval, "interval", defaultSummaryInterval, "pause between Claude calls (the window is shared with Jarvis)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if f.export.slug == "" || strings.ContainsAny(f.export.slug, "/ ") {
		return nil, fmt.Errorf("invalid --slug %q", f.export.slug)
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

func (f *regenSummariesFlags) selects(e *pbactivity.ShowcaseProfileEntry) bool {
	if f.id != "" && e.ShowcaseId != f.id {
		return false
	}
	if f.since != nil && (e.StartTime == nil || e.StartTime.AsTime().Before(*f.since)) {
		return false
	}
	return true
}

// loadSummaries reads the selected activities, newest first. An activity that
// cannot be read is reported and left out.
func loadSummaries(ctx context.Context, st summaryStore, ids []string, out io.Writer) []*activity.ActivitySummary {
	res := make([]*activity.ActivitySummary, len(ids))
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, summaryLoadConcurrency)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res[i], errs[i] = st.LoadActivitySummary(ctx, id)
		}(i, id)
	}
	wg.Wait()
	var acts []*activity.ActivitySummary
	for i, a := range res {
		if errs[i] != nil {
			fmt.Fprintf(out, "%s: unreadable, skipped: %v\n", ids[i], errs[i])
			continue
		}
		acts = append(acts, a)
	}
	sort.SliceStable(acts, func(i, j int) bool { return acts[i].StartTime.After(acts[j].StartTime) })
	return acts
}

func estimateSummaries(acts []*activity.ActivitySummary) storyEstimate {
	var e storyEstimate
	for _, a := range acts {
		sys, prompt := ai_companion.SummaryPrompt(a.Activity, a.OtherDescriptions)
		e.calls++
		e.inputTokens += int(float64(utf8.RuneCountInString(sys)+utf8.RuneCountInString(prompt))/charsPerToken + 0.5)
		e.outTokens += summaryOutputTokens
	}
	e.usd = float64(e.inputTokens)/1e6*sonnetInputPerMTok + float64(e.outTokens)/1e6*sonnetOutputPerMTok
	return e
}

func runRegenSummaries(ctx context.Context, f *regenSummariesFlags, st summaryStore, out io.Writer, now func() time.Time) error {
	entries, err := st.SummaryEntries(ctx, f.export.slug)
	if err != nil {
		return fmt.Errorf("entries: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if f.selects(e) {
			ids = append(ids, e.ShowcaseId)
		}
	}
	if f.id != "" && len(ids) == 0 {
		return fmt.Errorf("%s is not on the %s profile", f.id, f.export.slug)
	}
	selected := loadSummaries(ctx, st, ids, out)
	var todo []*activity.ActivitySummary
	current, noData, withOld := 0, 0, 0
	for _, a := range selected {
		switch {
		case !a.HasData():
			noData++
		case a.IsCurrent(ai_companion.SummaryModel) && !f.force:
			current++
		default:
			todo = append(todo, a)
			if a.Old != "" {
				withOld++
			}
		}
	}
	if f.apply && f.limit > 0 && len(todo) > f.limit {
		todo = todo[:f.limit]
		withOld = 0
		for _, a := range todo {
			if a.Old != "" {
				withOld++
			}
		}
	}
	est := estimateSummaries(todo)
	fmt.Fprintf(out, "%d activities on the %s profile, %d selected (%d unreadable, listed above): %d already current (%s), %d with no activity data (left as they are), %d to rewrite (%d with an old summary, %d with none)\n",
		len(entries), f.export.slug, len(ids), len(ids)-len(selected), current, ai_companion.SummaryModel, noData, len(todo), withOld, len(todo)-withOld)
	fmt.Fprintf(out, "estimate: %d Claude Sonnet calls (up to %d if every summary needs the voice-guard retry), ~%d input + ~%d output tokens, ~$%.2f API-equivalent (paid from the shared Claude window)\n",
		est.calls, 2*est.calls, est.inputTokens, est.outTokens, est.usd)

	if !f.apply {
		n := f.sample
		if n > len(todo) {
			n = len(todo)
		}
		for _, a := range todo[:n] {
			text, err := st.WriteActivitySummary(ctx, a)
			fmt.Fprintf(out, "\n== %s (%s)\nold: %s\n", a.ShowcaseID, a.StartTime.Format("2006-01-02"), orNone(a.Old))
			switch {
			case err != nil:
				fmt.Fprintf(out, "new: (Claude error: %v)\n", err)
				if errors.Is(err, claude.ErrLimited) {
					return err
				}
			case text == "":
				fmt.Fprintln(out, "new: (dropped by the voice guard twice; the old summary would stay)")
			default:
				fmt.Fprintf(out, "new: %s\n", text)
			}
		}
		fmt.Fprintln(out, "\ndry run: nothing saved; --apply rewrites them")
		return nil
	}

	voice.OnReject = func(text string, err error) { fmt.Fprintf(out, "  rejected (%v): %s\n", err, text) }
	defer func() { voice.OnReject = nil }()
	written, dropped, failed := 0, 0, 0
	for i, a := range todo {
		if i > 0 && f.interval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(f.interval):
			}
		}
		text, err := st.WriteActivitySummary(ctx, a)
		switch {
		case errors.Is(err, claude.ErrLimited):
			fmt.Fprintf(out, "%s: Claude usage window spent; stopping after %d written. Saved summaries are kept; re-run the same command later to finish.\n", a.ShowcaseID, written)
			return err
		case err != nil:
			failed++
			fmt.Fprintf(out, "%s: Claude error, old summary kept: %v\n", a.ShowcaseID, err)
			continue
		case text == "":
			dropped++
			fmt.Fprintf(out, "%s: dropped by the voice guard, old summary kept\n", a.ShowcaseID)
			continue
		}
		if err := st.SaveActivitySummary(ctx, a, text, now()); err != nil {
			return fmt.Errorf("%s: %w (after %d written; re-run to continue)", a.ShowcaseID, err, written)
		}
		written++
		fmt.Fprintf(out, "%s: written (%d/%d)\n", a.ShowcaseID, written, len(todo))
	}
	fmt.Fprintf(out, "done: %d written, %d dropped by the voice guard, %d Claude errors, %d skipped as current, %d with no data; the dropped and failed keep their old summary and a re-run retries them\n",
		written, dropped, failed, current, noData)
	return nil
}
