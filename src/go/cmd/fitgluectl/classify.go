// nolint:proto-json -- encoding/json here is for the local verdict cache, a plain struct

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_activity_type"
	"github.com/fitglue/server/src/go/pkg/activitytag"
	"github.com/fitglue/server/src/go/pkg/claude"
	"google.golang.org/protobuf/types/known/timestamppb"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// `fitgluectl classify` gives every showcased activity the site's tag, race flag
// and race reason through the ai-activity-type enricher's own path
// (ai_activity_type.Classify: Haiku, JSON, enum-checked, rule fallback). Site
// only: the showcase's enrichments.classification and the profile entry's
// tag/isRace change; the title, description, activity type and data are
// untouched, and nothing is written to Strava or any other destination.
//
//	fitgluectl classify                       # dry run: ask, count per tag, list every race; nothing saved
//	fitgluectl classify --apply               # save; skips activities already classified by Haiku
//	fitgluectl classify --id hyrox-sim-2026-03-01 --force --apply
//
// Verdicts are cached locally by showcase id and inputs hash
// (~/.local/state/fitgluectl/classify-cache.json), so --apply after a dry run
// saves what the dry run showed without asking again, and an interrupted run
// resumes. --no-cache asks afresh. A rule-fallback verdict is never cached and
// never counts as classified, so a re-run retries it.

const (
	defaultClassifyInterval = 500 * time.Millisecond
	classifyWorkers         = 2 // the claude CLI runs at most two at once
	keepParkrunHistory      = 12
)

type classifyFlags struct {
	export   exportSiteFlags
	id       string
	since    *time.Time
	force    bool
	apply    bool
	noCache  bool
	cache    string
	out      string
	limit    int
	interval time.Duration
}

// classifyStore is what the command needs: the profile's entries, each
// activity's stored state, the question (Haiku) and the save.
type classifyStore interface {
	SummaryEntries(ctx context.Context, slug string) ([]*pbactivity.ShowcaseProfileEntry, error)
	LoadActivitySummary(ctx context.Context, showcaseID string) (*activity.ActivitySummary, error)
	Classify(ctx context.Context, in ai_activity_type.Inputs) (*pbactivity.ActivityClassification, error)
	SaveClassification(ctx context.Context, a *activity.ActivitySummary, c *pbactivity.ActivityClassification, e *pbactivity.ShowcaseProfileEntry) error
}

func defaultClassifyCache() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "fitgluectl", "classify-cache.json")
}

func parseClassifyFlags(args []string, stderr io.Writer) (*classifyFlags, error) {
	f := &classifyFlags{}
	fs := flag.NewFlagSet("classify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var since string
	fs.StringVar(&f.export.slug, "slug", "james", "showcase profile slug")
	fs.StringVar(&f.export.project, "project", defaultProject, "GCP project holding Firestore")
	fs.StringVar(&f.export.bucket, "bucket", "", "showcase-assets bucket (default <project>-showcase-assets)")
	fs.StringVar(&f.id, "id", "", "only this activity (showcase id, the site slug)")
	fs.StringVar(&since, "since", "", "only activities starting on or after this date (YYYY-MM-DD or RFC 3339)")
	fs.BoolVar(&f.force, "force", false, "re-classify activities already classified by Haiku")
	fs.BoolVar(&f.apply, "apply", false, "save the verdicts (default: dry run)")
	fs.BoolVar(&f.noCache, "no-cache", false, "ask afresh instead of reusing locally cached verdicts")
	fs.StringVar(&f.cache, "cache", defaultClassifyCache(), "local verdict cache (\"\" for none)")
	fs.StringVar(&f.out, "out", "", "also write every verdict as TSV here")
	fs.IntVar(&f.limit, "limit", 0, "stop after this many, newest first (0: all)")
	fs.DurationVar(&f.interval, "interval", defaultClassifyInterval, "pause between Haiku calls per worker (the window is shared with Jarvis)")
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

// classifyJob is one activity to classify.
type classifyJob struct {
	a     *activity.ActivitySummary
	entry *pbactivity.ShowcaseProfileEntry
	in    ai_activity_type.Inputs
	hash  string
	c     *pbactivity.ActivityClassification
	err   error
	from  string // "haiku", "cache", "rule"
}

// classifyInputs is what the enricher would have been told about a stored
// activity: the title, his own words, FitGlue's sections, the data, the parkrun
// result and his parkrun times before it.
func classifyInputs(a *activity.ActivitySummary, usual []int) ai_activity_type.Inputs {
	act := a.Activity
	if act == nil {
		act = &pbactivity.StandardizedActivity{}
	}
	act.Name = a.Title
	if a.ActivityType != pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
		act.Type = a.ActivityType
	}
	if len(act.Tags) == 0 {
		act.Tags = a.Tags
	}
	return ai_activity_type.Inputs{
		Title: a.Title, Description: act.Description, Context: a.OtherDescriptions,
		Activity: act, Parkrun: a.Parkrun(), UsualParkrunSeconds: usual,
	}
}

type verdictCache struct {
	path string
	mu   sync.Mutex
	m    map[string]cachedVerdict
}

type cachedVerdict struct {
	Hash       string `json:"hash"`
	Tag        string `json:"tag"`
	IsRace     bool   `json:"isRace"`
	RaceWhy    string `json:"raceWhy"`
	StravaType string `json:"stravaType"`
}

func loadVerdictCache(path string) *verdictCache {
	vc := &verdictCache{path: path, m: map[string]cachedVerdict{}}
	if path == "" {
		return vc
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &vc.m)
	}
	return vc
}

func (vc *verdictCache) get(id, hash string) *pbactivity.ActivityClassification {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	v, ok := vc.m[id]
	if !ok || v.Hash != hash || !activitytag.Valid(v.Tag) {
		return nil
	}
	t := pbactivity.ActivityType(pbactivity.ActivityType_value[v.StravaType])
	return &pbactivity.ActivityClassification{Tag: v.Tag, IsRace: v.IsRace, RaceWhy: v.RaceWhy, StravaType: t,
		Model: ai_activity_type.Model, InputsHash: hash}
}

func (vc *verdictCache) put(id string, c *pbactivity.ActivityClassification) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.m[id] = cachedVerdict{Hash: c.InputsHash, Tag: c.Tag, IsRace: c.IsRace, RaceWhy: c.RaceWhy, StravaType: c.StravaType.String()}
}

func (vc *verdictCache) save() error {
	if vc.path == "" {
		return nil
	}
	vc.mu.Lock()
	defer vc.mu.Unlock()
	raw, err := json.MarshalIndent(vc.m, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(vc.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(vc.path, raw, 0o600)
}

func (f *classifyFlags) selects(e *pbactivity.ShowcaseProfileEntry) bool {
	if f.id != "" && e.ShowcaseId != f.id {
		return false
	}
	if f.since != nil && (e.StartTime == nil || e.StartTime.AsTime().Before(*f.since)) {
		return false
	}
	return true
}

func runClassify(ctx context.Context, f *classifyFlags, st classifyStore, out io.Writer, now func() time.Time) error {
	entries, err := st.SummaryEntries(ctx, f.export.slug)
	if err != nil {
		return fmt.Errorf("entries: %w", err)
	}
	byID := map[string]*pbactivity.ShowcaseProfileEntry{}
	var ids []string
	for _, e := range entries {
		byID[e.ShowcaseId] = e
		ids = append(ids, e.ShowcaseId)
	}
	// Every activity is read, selected or not: the parkrun history needs them all.
	all := loadSummaries(ctx, st, ids, out)
	sort.SliceStable(all, func(i, j int) bool { return all[i].StartTime.Before(all[j].StartTime) })

	var jobs []*classifyJob
	var parkruns []int
	done := 0
	for _, a := range all {
		e := byID[a.ShowcaseID]
		var usual []int
		in := classifyInputs(a, nil)
		parkrun := ai_activity_type.IsParkrun(in.Activity, in.Parkrun)
		if parkrun {
			usual = append([]int(nil), parkruns...)
			in.UsualParkrunSeconds = usual
			if secs := ai_activity_type.RunSeconds(in.Activity, in.Parkrun); secs > 0 {
				parkruns = append(parkruns, secs)
				if len(parkruns) > keepParkrunHistory {
					parkruns = parkruns[len(parkruns)-keepParkrunHistory:]
				}
			}
		}
		if e == nil || !f.selects(e) {
			continue
		}
		if c := a.Classification; c != nil && c.Model == ai_activity_type.Model && activitytag.Valid(c.Tag) && !f.force {
			done++
			continue
		}
		jobs = append(jobs, &classifyJob{a: a, entry: e, in: in, hash: ai_activity_type.InputsHash(in)})
	}
	if f.id != "" && len(jobs) == 0 && done == 0 {
		return fmt.Errorf("%s is not on the %s profile", f.id, f.export.slug)
	}
	// Newest first, so --limit does the recent ones.
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].a.StartTime.After(jobs[j].a.StartTime) })
	if f.limit > 0 && len(jobs) > f.limit {
		jobs = jobs[:f.limit]
	}

	cache := loadVerdictCache(f.cache)
	asks := 0
	for _, j := range jobs {
		if !f.noCache {
			if c := cache.get(j.a.ShowcaseID, j.hash); c != nil {
				j.c, j.from = c, "cache"
				continue
			}
		}
		asks++
	}
	fmt.Fprintf(out, "%d activities on the %s profile (%d readable): %d already classified by Haiku (skipped; --force redoes them), %d to classify (%d from the local cache, %d Haiku calls)\n",
		len(entries), f.export.slug, len(all), done, len(jobs), len(jobs)-asks, asks)

	// Ask, two at a time.
	var limited error
	var mu sync.Mutex
	work := make(chan *classifyJob)
	var wg sync.WaitGroup
	asked := 0
	for w := 0; w < classifyWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				c, err := st.Classify(ctx, j.in)
				j.c, j.err = c, err
				j.from = "haiku"
				if c.GetModel() != ai_activity_type.Model {
					j.from = "rule"
				}
				mu.Lock()
				asked++
				if j.from == "haiku" {
					cache.put(j.a.ShowcaseID, c)
				}
				if asked%25 == 0 {
					fmt.Fprintf(out, "  asked %d/%d\n", asked, asks)
					_ = cache.save()
				}
				if errors.Is(err, claude.ErrLimited) && limited == nil {
					limited = err
				}
				mu.Unlock()
				if f.interval > 0 {
					time.Sleep(f.interval)
				}
			}
		}()
	}
	for _, j := range jobs {
		if j.c != nil {
			continue
		}
		mu.Lock()
		stop := limited != nil
		mu.Unlock()
		if stop || ctx.Err() != nil {
			break
		}
		work <- j
	}
	close(work)
	wg.Wait()
	if err := cache.save(); err != nil {
		fmt.Fprintf(out, "warning: verdict cache not saved: %v\n", err)
	}

	// Report.
	var got []*classifyJob
	for _, j := range jobs {
		if j.c != nil {
			got = append(got, j)
		}
	}
	report(out, got)
	if f.out != "" {
		if err := writeClassifyTSV(f.out, got); err != nil {
			return err
		}
		fmt.Fprintf(out, "verdicts written to %s\n", f.out)
	}
	if limited != nil {
		fmt.Fprintf(out, "Claude usage window spent after %d asks; re-run later to finish (cached verdicts are kept)\n", asked)
	}

	if !f.apply {
		fmt.Fprintln(out, "\ndry run: nothing saved; --apply saves these (rule fallbacks included, re-tried next run)")
		return limited
	}
	saved := 0
	for _, j := range got {
		c := j.c
		if c.ClassifiedAt == nil {
			c.ClassifiedAt = timestamppb.New(now())
		}
		if err := st.SaveClassification(ctx, j.a, c, j.entry); err != nil {
			return fmt.Errorf("%s: %w (after %d saved; re-run to continue)", j.a.ShowcaseID, err, saved)
		}
		saved++
	}
	fmt.Fprintf(out, "done: %d saved (%d by rule fallback, re-tried on the next run)\n", saved, countFrom(got, "rule"))
	return limited
}

func countFrom(js []*classifyJob, from string) int {
	n := 0
	for _, j := range js {
		if j.from == from {
			n++
		}
	}
	return n
}

func report(out io.Writer, js []*classifyJob) {
	tags := map[string]int{}
	var races []*classifyJob
	for _, j := range js {
		tags[j.c.Tag]++
		if j.c.IsRace {
			races = append(races, j)
		}
	}
	fmt.Fprintf(out, "\nper tag (%d classified, %d by rule fallback):\n", len(js), countFrom(js, "rule"))
	for _, t := range activitytag.Tags {
		fmt.Fprintf(out, "  %-9s %d\n", t, tags[t])
	}
	sort.SliceStable(races, func(i, k int) bool { return races[i].a.StartTime.Before(races[k].a.StartTime) })
	fmt.Fprintf(out, "\nisRace=true (%d):\n", len(races))
	for _, j := range races {
		fmt.Fprintf(out, "  %s  %-40s %-8s %s\n", j.a.StartTime.Format("2006-01-02"), truncateRunes(j.a.Title, 40), j.c.Tag, j.c.RaceWhy)
	}
	for _, j := range js {
		if j.err != nil && !errors.Is(j.err, claude.ErrLimited) {
			fmt.Fprintf(out, "  fallback %s: %v\n", j.a.ShowcaseID, j.err)
		}
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "~"
}

func writeClassifyTSV(path string, js []*classifyJob) error {
	var b strings.Builder
	b.WriteString("date\tid\ttitle\trecorded\ttag\tisRace\tstravaType\tfrom\traceWhy\n")
	sorted := append([]*classifyJob(nil), js...)
	sort.SliceStable(sorted, func(i, k int) bool { return sorted[i].a.StartTime.Before(sorted[k].a.StartTime) })
	clean := strings.NewReplacer("\t", " ", "\n", " ")
	for _, j := range sorted {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%v\t%s\t%s\t%s\n", j.a.StartTime.Format("2006-01-02"), j.a.ShowcaseID,
			clean.Replace(j.a.Title), strings.TrimPrefix(j.in.Activity.GetType().String(), "ACTIVITY_TYPE_"),
			j.c.Tag, j.c.IsRace, strings.TrimPrefix(j.c.StravaType.String(), "ACTIVITY_TYPE_"), j.from, clean.Replace(j.c.RaceWhy))
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
