package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/activity/siteexport"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_activity_type"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/ai_companion"
	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// liveSite is the activity service as the box runs it, for the commands that
// write through it (fix-activity, regen-roundup-stories). With --apply it
// carries the same site-export hook the activity service installs: every saved
// roundup or changed showcase is re-exported to the bucket, index.json is
// refreshed (debounced) and a line is appended to the site-rebuild signal file,
// so james.ripixel.co.uk rebuilds as it does after a normal ingest.
type liveSite struct {
	source  siteexport.ServiceSource
	hook    *siteexport.Hook
	cleanup func()
}

// defaultSignalFile is the box's site-rebuild queue when SITE_EXPORT_SIGNAL_FILE
// is unset (the activity unit sets it; fitglue.env does not): the file the
// jarvis-site-rebuild.path unit watches, when its directory exists.
func defaultSignalFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".jarvis", "home")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return filepath.Join(dir, "site-changed")
}

func openLiveSite(ctx context.Context, f *exportSiteFlags, stderr io.Writer) (*liveSite, error) {
	source, writer, cleanup, err := connectGCP(ctx, f)
	if err != nil {
		return nil, err
	}
	ss, ok := source.(siteexport.ServiceSource)
	if !ok {
		cleanup()
		return nil, errors.New("needs the Firestore-backed source")
	}
	site := &liveSite{source: ss, cleanup: cleanup}
	if !f.dryRun {
		cfg := siteexport.ConfigFromEnv(os.Getenv, f.bucket)
		signal := cfg.SignalFile
		if signal == "" {
			signal = defaultSignalFile()
		}
		logger := log.New(stderr, "", log.Ltime)
		site.hook = siteexport.NewHook(siteexport.HookConfig{
			Slug: f.slug,
			Deps: siteexport.Deps{
				Source: ss, Writer: writer, Logf: logger.Printf,
				Concurrency: f.concurrency, Zones: siteexport.ZonesFromEnv(os.Getenv),
			},
			HookURL:       cfg.HookURL,
			SignalFile:    signal,
			ExportTimeout: 2 * time.Minute,
		})
		ss.Service.SetSiteExporter(site.hook)
		fmt.Fprintf(stderr, "site export: gs://%s/%s, rebuild signal %s\n", f.bucket, f.prefix, orNone(signal))
	}
	return site, nil
}

// close waits for the background exports and the final index refresh (and
// its rebuild signal) before closing the clients.
func (s *liveSite) close() {
	if s.hook != nil {
		s.hook.Wait()
	}
	s.cleanup()
}

// storyAdapter joins the store's roundup listing to the service's story halves.
type storyAdapter struct{ siteexport.ServiceSource }

func (a storyAdapter) Roundups(ctx context.Context, slug string) ([]*pbactivity.ShowcaseRoundup, error) {
	return a.ServiceSource.Roundups(ctx, slug)
}

func (a storyAdapter) WriteRoundupStory(ctx context.Context, r *pbactivity.ShowcaseRoundup) (string, error) {
	return a.Service.WriteRoundupStory(ctx, r)
}

func (a storyAdapter) SaveRoundupStory(ctx context.Context, r *pbactivity.ShowcaseRoundup, text string, now time.Time) error {
	return a.Service.SaveRoundupStory(ctx, r, text, now)
}

func regenStoriesCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f, err := parseRegenStoriesFlags(args, stderr)
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
	return runRegenStories(ctx, f, storyAdapter{site.source}, stdout, time.Now)
}

// summaryAdapter joins the profile entries to the service's activity-summary
// halves and the live enricher's writer.
type summaryAdapter struct{ siteexport.ServiceSource }

func (a summaryAdapter) SummaryEntries(ctx context.Context, slug string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	prof, err := a.Profile(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", slug, err)
	}
	return a.Entries(ctx, prof.UserId)
}

func (a summaryAdapter) LoadActivitySummary(ctx context.Context, id string) (*activity.ActivitySummary, error) {
	return a.Service.LoadActivitySummary(ctx, id)
}

func (a summaryAdapter) WriteActivitySummary(ctx context.Context, s *activity.ActivitySummary) (string, error) {
	return ai_companion.Summarize(ctx, nil, s.Activity, s.OtherDescriptions)
}

func (a summaryAdapter) SaveActivitySummary(ctx context.Context, s *activity.ActivitySummary, text string, now time.Time) error {
	return a.Service.SaveActivitySummary(ctx, s, text, ai_companion.SummaryModel, now)
}

func regenSummariesCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f, err := parseRegenSummariesFlags(args, stderr)
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
	return runRegenSummaries(ctx, f, summaryAdapter{site.source}, stdout, time.Now)
}

// classifyAdapter joins the profile entries to the service's classification
// halves and the live enricher's classifier.
type classifyAdapter struct{ summaryAdapter }

func (a classifyAdapter) Classify(ctx context.Context, in ai_activity_type.Inputs) (*pbactivity.ActivityClassification, error) {
	return ai_activity_type.Classify(ctx, claude.Default(), in)
}

func (a classifyAdapter) SaveClassification(ctx context.Context, s *activity.ActivitySummary, c *pbactivity.ActivityClassification, e *pbactivity.ShowcaseProfileEntry) error {
	return a.Service.SaveClassification(ctx, s, c, e)
}

func classifyCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f, err := parseClassifyFlags(args, stderr)
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
	return runClassify(ctx, f, classifyAdapter{summaryAdapter{site.source}}, stdout, time.Now)
}
