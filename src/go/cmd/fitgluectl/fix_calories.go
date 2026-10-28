package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fitglue/server/src/go/internal/activity/siteexport"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// `fitgluectl fix-calories` corrects the calories stored on showcase profile entries
// (F5.5). Older calories boosters always stored a MET estimate, even when the source
// platform had its own figure (Birmingham's Hyrox solo: 523 kcal stored, Strava 1,346).
// The export already prefers the platform figure (siteexport.ApplyPlatformCalories);
// this writes it to the entries too, so index refreshes and new roundups agree, and
// prints the roundups whose calorie totals were built from the old figures.
//
// Dry run by default: lists every entry that would change. --apply writes them.
//
//	fitgluectl fix-calories --slug james            # plan
//	fitgluectl fix-calories --slug james --apply    # write the entries

type fixCaloriesFlags struct {
	export exportSiteFlags
	apply  bool
}

// entryWriter is the one store call --apply makes.
type entryWriter interface {
	SetShowcaseProfileEntry(ctx context.Context, userID string, entry *pbactivity.ShowcaseProfileEntry) error
}

func parseFixCaloriesFlags(args []string, stderr io.Writer) (*fixCaloriesFlags, error) {
	f := &fixCaloriesFlags{}
	fs := flag.NewFlagSet("fix-calories", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.export.slug, "slug", "", "showcase profile slug (required)")
	fs.StringVar(&f.export.project, "project", defaultProject, "GCP project holding Firestore")
	fs.StringVar(&f.export.bucket, "bucket", "", "showcase-assets bucket the activity data blobs live in (default <project>-showcase-assets)")
	fs.BoolVar(&f.apply, "apply", false, "write the corrected entries (default: print the plan only)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if f.export.slug == "" {
		return nil, errors.New("--slug is required")
	}
	if f.export.bucket == "" {
		f.export.bucket = f.export.project + "-showcase-assets"
	}
	f.export.dryRun = true // never open a bucket writer
	return f, nil
}

type calorieFix struct {
	showcaseID string
	date       string
	before     int32
	after      int32
}

// planCalorieFixes compares every entry with its showcase's platform calories.
func planCalorieFixes(ctx context.Context, source siteexport.Source, slug string) (string, []*pbactivity.ShowcaseProfileEntry, []calorieFix, error) {
	profile, err := source.Profile(ctx, slug)
	if err != nil {
		return "", nil, nil, fmt.Errorf("profile %q: %w", slug, err)
	}
	entries, err := source.Entries(ctx, profile.UserId)
	if err != nil {
		return "", nil, nil, fmt.Errorf("entries: %w", err)
	}
	var changed []*pbactivity.ShowcaseProfileEntry
	var fixes []calorieFix
	for _, e := range entries {
		sc, err := source.Showcase(ctx, e.ShowcaseId)
		if err != nil {
			return "", nil, nil, fmt.Errorf("showcase %s: %w", e.ShowcaseId, err)
		}
		if sc == nil {
			continue
		}
		before := e.GetCaloriesKcal()
		if !siteexport.ApplyPlatformCalories(sc, e) || e.GetCaloriesKcal() == before {
			continue // only the entry matters here; the export rewrites the activity file
		}
		changed = append(changed, e)
		fixes = append(fixes, calorieFix{showcaseID: e.ShowcaseId, date: e.GetStartTime().AsTime().Format("2006-01-02"), before: before, after: e.GetCaloriesKcal()})
	}
	sort.Slice(fixes, func(i, j int) bool { return fixes[i].date < fixes[j].date })
	return profile.UserId, changed, fixes, nil
}

// affectedRoundups lists the existing roundups whose period holds a corrected entry.
func affectedRoundups(ctx context.Context, source siteexport.Source, slug string, changed []*pbactivity.ShowcaseProfileEntry) ([]string, error) {
	roundups, err := source.Roundups(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("roundups: %w", err)
	}
	exists := map[string]bool{}
	for _, r := range roundups {
		exists[r.PeriodKey] = true
	}
	keys := map[string]bool{}
	for _, e := range changed {
		t := e.GetStartTime().AsTime().UTC()
		for _, pt := range []pbactivity.RoundupPeriodType{
			pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK,
			pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH,
			pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_YEAR,
		} {
			if k := siteexport.PeriodKey(pt, t); exists[k] {
				keys[k] = true
			}
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func runFixCalories(ctx context.Context, f *fixCaloriesFlags, source siteexport.Source, writer entryWriter, stdout io.Writer) error {
	userID, changed, fixes, err := planCalorieFixes(ctx, source, f.export.slug)
	if err != nil {
		return err
	}
	for _, x := range fixes {
		fmt.Fprintf(stdout, "%s %s: %d → %d kcal\n", x.date, x.showcaseID, x.before, x.after)
	}
	fmt.Fprintf(stdout, "%d entries %s\n", len(fixes), map[bool]string{true: "corrected", false: "would change (dry run; --apply writes them)"}[f.apply])
	if keys, err := affectedRoundups(ctx, source, f.export.slug, changed); err == nil && len(keys) > 0 {
		fmt.Fprintf(stdout, "roundups built from the old figures (%d): %s\n", len(keys), strings.Join(keys, " "))
	}
	if !f.apply {
		return nil
	}
	for _, e := range changed {
		if err := writer.SetShowcaseProfileEntry(ctx, userID, e); err != nil {
			return fmt.Errorf("write entry %s: %w", e.ShowcaseId, err)
		}
	}
	return nil
}

func fixCaloriesCommand(ctx context.Context, args []string, stdout, stderr io.Writer, connect connector) error {
	f, err := parseFixCaloriesFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	source, _, cleanup, err := connect(ctx, &f.export)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	writer, ok := source.(siteexport.ServiceSource)
	if !ok {
		return errors.New("fix-calories needs the Firestore-backed source")
	}
	return runFixCalories(ctx, f, source, writer.Store, stdout)
}
