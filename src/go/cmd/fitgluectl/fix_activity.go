package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fitglue/server/src/go/internal/activity"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// `fitgluectl fix-activity` corrects a showcased activity's type, distance and
// calories (James's standing order, 2026-10-28: fix wrong fitness data without
// asking, keep his words). It patches every copy of the activity (the data blob,
// the showcase document, the profile entry), then the derived state: lifetime
// totals, the stored week/month/year roundups holding it (recomputed, story
// included) and the site export (re-export + index + site-rebuild signal).
//
//	fitgluectl fix-activity --id <showcase id> --type RUN                 # plan; distance from the recorded set
//	fitgluectl fix-activity --id <showcase id> --type RUN --distance-m 42195 --calories-kcal 3802 --apply
//	fitgluectl fix-activity --scan                                         # list runs stored as strength sessions
//
// Dry run unless --apply; a second --apply finds nothing to change.

type fixActivityFlags struct {
	export exportSiteFlags
	fix    activity.ActivityFix
	apply  bool
	scan   bool
}

type activityFixer interface {
	PlanActivityFix(ctx context.Context, fix activity.ActivityFix) (*activity.ActivityFixPlan, error)
	ApplyActivityFix(ctx context.Context, plan *activity.ActivityFixPlan) error
}

func parseActivityType(s string) (pbactivity.ActivityType, error) {
	name := strings.ToUpper(strings.TrimSpace(s))
	if !strings.HasPrefix(name, "ACTIVITY_TYPE_") {
		name = "ACTIVITY_TYPE_" + name
	}
	v, ok := pbactivity.ActivityType_value[name]
	if !ok || v == 0 {
		return 0, fmt.Errorf("unknown activity type %q", s)
	}
	return pbactivity.ActivityType(v), nil
}

func parseFixActivityFlags(args []string, stderr io.Writer) (*fixActivityFlags, error) {
	f := &fixActivityFlags{}
	fs := flag.NewFlagSet("fix-activity", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var typ string
	var dist float64
	fs.StringVar(&f.export.slug, "slug", "james", "showcase profile slug (site export, --scan)")
	fs.StringVar(&f.export.project, "project", defaultProject, "GCP project holding Firestore")
	fs.StringVar(&f.export.bucket, "bucket", "", "showcase-assets bucket (default <project>-showcase-assets)")
	fs.StringVar(&f.fix.ShowcaseID, "id", "", "showcase id (the site slug, e.g. bingham-sunday-running-club-25-2026-05-10)")
	fs.StringVar(&typ, "type", "", "corrected activity type: RUN, WALK, ... (ACTIVITY_TYPE_ prefix optional)")
	fs.Float64Var(&dist, "distance-m", -1, "corrected distance in metres (default: the distance the source recorded on its set)")
	var kcal int
	fs.IntVar(&kcal, "calories-kcal", 0, "the source platform's calories for it (e.g. Strava's), replacing the MET estimate")
	fs.BoolVar(&f.apply, "apply", false, "write the fix (default: print the plan only)")
	fs.BoolVar(&f.scan, "scan", false, "list runs stored as strength sessions (read-only)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	distSet := false
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "distance-m" {
			distSet = true
		}
	})
	if f.scan {
		if f.fix.ShowcaseID != "" || typ != "" || distSet || kcal != 0 || f.apply {
			return nil, errors.New("--scan takes no fix flags")
		}
	} else {
		if f.fix.ShowcaseID == "" {
			return nil, errors.New("--id is required (or --scan)")
		}
		if typ != "" {
			t, err := parseActivityType(typ)
			if err != nil {
				return nil, err
			}
			f.fix.Type = t
		}
		if distSet {
			if dist < 0 {
				return nil, errors.New("--distance-m must be >= 0")
			}
			f.fix.DistanceM = &dist
		}
		if kcal < 0 {
			return nil, errors.New("--calories-kcal must be > 0")
		}
		f.fix.CaloriesKcal = int32(kcal)
	}
	if f.export.bucket == "" {
		f.export.bucket = f.export.project + "-showcase-assets"
	}
	f.export.prefix = "site/" + f.export.slug
	f.export.dryRun = !f.apply
	f.export.concurrency = 4
	return f, nil
}

func runFixActivity(ctx context.Context, f *fixActivityFlags, fx activityFixer, out io.Writer) error {
	plan, err := fx.PlanActivityFix(ctx, f.fix)
	if errors.Is(err, activity.ErrNothingToFix) {
		fmt.Fprintf(out, "%s %q: nothing to change\n", f.fix.ShowcaseID, plan.Title)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %q\n", plan.ShowcaseID, plan.Title)
	for _, c := range plan.Changes {
		fmt.Fprintf(out, "  %s\n", c)
	}
	if len(plan.Roundups) > 0 {
		fmt.Fprintf(out, "roundups recomputed (numbers and story): %s\n", strings.Join(plan.Roundups, " "))
	}
	fmt.Fprintln(out, "then: lifetime totals recomputed, activity + roundups + index re-exported, site rebuild signalled")
	for _, n := range plan.Notes {
		fmt.Fprintf(out, "note: %s\n", n)
	}
	if !f.apply {
		fmt.Fprintln(out, "dry run: nothing written; --apply writes it")
		return nil
	}
	if err := fx.ApplyActivityFix(ctx, plan); err != nil {
		return err
	}
	fmt.Fprintln(out, "applied")
	return nil
}

// scanSource is the read side --scan needs (siteexport.ServiceSource has it).
type scanSource interface {
	Profile(ctx context.Context, slug string) (*pbactivity.ShowcaseProfile, error)
	Entries(ctx context.Context, userID string) ([]*pbactivity.ShowcaseProfileEntry, error)
	Showcase(ctx context.Context, showcaseID string) (*pbactivity.ShowcasedActivity, error)
}

// runScanMislabelledRuns lists profile entries that look like runs stored as
// strength sessions (activity.MislabelledRun). Only strength entries with no
// distance are hydrated.
func runScanMislabelledRuns(ctx context.Context, slug string, src scanSource, out io.Writer) error {
	prof, err := src.Profile(ctx, slug)
	if err != nil {
		return fmt.Errorf("profile %q: %w", slug, err)
	}
	entries, err := src.Entries(ctx, prof.UserId)
	if err != nil {
		return fmt.Errorf("entries: %w", err)
	}
	n := 0
	for _, e := range entries {
		if !activity.IsStrengthType(e.ActivityType) || e.DistanceMeters > 0 {
			continue
		}
		sc, err := src.Showcase(ctx, e.ShowcaseId)
		if err != nil {
			return fmt.Errorf("showcase %s: %w", e.ShowcaseId, err)
		}
		if sc == nil || !activity.MislabelledRun(sc.ActivityType, sc.ActivityData) {
			continue
		}
		n++
		var names []string
		for _, s := range sc.ActivityData.GetSessions() {
			for _, set := range s.StrengthSets {
				names = append(names, set.ExerciseName)
			}
		}
		fmt.Fprintf(out, "%s  %s  %q  %s, %d s, recorded %.0f m on %s\n",
			e.GetStartTime().AsTime().Format("2006-01-02"), e.ShowcaseId, e.Title, strings.TrimPrefix(e.ActivityType.String(), "ACTIVITY_TYPE_"),
			int(e.DurationSeconds), activity.RecordedSetDistance(sc.ActivityData), strings.Join(names, ", "))
	}
	fmt.Fprintf(out, "%d suspect activities (runs stored as strength sessions with no distance); fix each with --id ... --type RUN\n", n)
	return nil
}

func fixActivityCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f, err := parseFixActivityFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	site, err := openLiveSite(ctx, &f.export, stderr)
	if err != nil {
		return err
	}
	defer site.close()
	if f.scan {
		return runScanMislabelledRuns(ctx, f.export.slug, site.source, stdout)
	}
	return runFixActivity(ctx, f, site.source.Service, stdout)
}
