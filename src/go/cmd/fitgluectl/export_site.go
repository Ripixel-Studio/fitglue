package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/activity/siteexport"
)

const (
	defaultProject = "fitglue-server-prod"
	bucketEnv      = "SHOWCASE_ASSETS_BUCKET"
)

type exportSiteFlags struct {
	slug        string
	project     string
	bucket      string
	prefix      string
	since       *time.Time
	all         bool
	dryRun      bool
	out         string
	activityID  string
	roundupKey  string
	concurrency int
}

// connector opens the data source and (unless dry-running) the bucket writer.
type connector func(ctx context.Context, f *exportSiteFlags) (siteexport.Source, siteexport.Writer, func(), error)

func parseExportSiteFlags(args []string, stderr io.Writer) (*exportSiteFlags, error) {
	f := &exportSiteFlags{}
	fs := flag.NewFlagSet("export-site", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var since string
	fs.StringVar(&f.slug, "slug", "", "showcase profile slug (required)")
	fs.StringVar(&f.project, "project", defaultProject, "GCP project holding Firestore")
	fs.StringVar(&f.bucket, "bucket", "", "showcase-assets bucket (default $"+bucketEnv+", else <project>-showcase-assets)")
	fs.StringVar(&f.prefix, "prefix", "", "object prefix inside the bucket (default site/<slug>)")
	fs.StringVar(&since, "since", "", "only write activities showcased / roundups generated after this RFC 3339 instant; index.json is always rewritten")
	fs.BoolVar(&f.all, "all", false, "rewrite everything and delete activity/roundup files no longer in the profile")
	fs.BoolVar(&f.dryRun, "dry-run", false, "write to --out instead of the bucket (never touches GCS objects)")
	fs.StringVar(&f.out, "out", "", "local directory for --dry-run")
	fs.StringVar(&f.activityID, "activity", "", "export one activity by showcase id, then refresh index.json")
	fs.StringVar(&f.roundupKey, "roundup", "", "export one roundup by period key, then refresh index.json")
	fs.IntVar(&f.concurrency, "concurrency", 8, "parallel activity reads/writes")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if f.slug == "" {
		return nil, errors.New("--slug is required")
	}
	if strings.ContainsAny(f.slug, "/ ") {
		return nil, fmt.Errorf("invalid slug %q", f.slug)
	}
	if since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return nil, fmt.Errorf("--since: %w", err)
		}
		t = t.UTC()
		f.since = &t
	}
	if f.since != nil && f.all {
		return nil, errors.New("--since and --all are mutually exclusive")
	}
	if (f.activityID != "" || f.roundupKey != "") && (f.since != nil || f.all) {
		return nil, errors.New("--activity/--roundup cannot be combined with --since or --all")
	}
	if f.dryRun && f.out == "" {
		return nil, errors.New("--dry-run requires --out <dir>")
	}
	if !f.dryRun && f.out != "" {
		return nil, errors.New("--out only applies with --dry-run")
	}
	if f.bucket == "" {
		f.bucket = os.Getenv(bucketEnv)
	}
	if f.bucket == "" {
		f.bucket = f.project + "-showcase-assets"
	}
	if f.prefix == "" {
		f.prefix = "site/" + f.slug
	}
	f.prefix = strings.Trim(f.prefix, "/")
	return f, nil
}

func exportSiteCommand(ctx context.Context, args []string, stderr io.Writer, connect connector) error {
	f, err := parseExportSiteFlags(args, stderr)
	if err != nil {
		return err
	}
	source, writer, cleanup, err := connect(ctx, f)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if f.dryRun {
		writer = &siteexport.DirWriter{Root: f.out}
	}
	return runExportSite(ctx, f, source, writer, log.New(stderr, "", log.Ltime))
}

func runExportSite(ctx context.Context, f *exportSiteFlags, source siteexport.Source, writer siteexport.Writer, logger *log.Logger) error {
	deps := siteexport.Deps{
		Source:      source,
		Writer:      writer,
		Logf:        logger.Printf,
		Concurrency: f.concurrency,
	}
	if f.dryRun {
		logger.Printf("dry run: writing to %s (bucket %s untouched)", f.out, f.bucket)
	} else {
		logger.Printf("writing to gs://%s/%s", f.bucket, f.prefix)
	}

	switch {
	case f.activityID != "":
		if err := siteexport.ExportActivity(ctx, deps, f.slug, f.activityID); err != nil {
			return err
		}
		return siteexport.RefreshIndex(ctx, deps, f.slug)
	case f.roundupKey != "":
		if err := siteexport.ExportRoundup(ctx, deps, f.slug, f.roundupKey); err != nil {
			return err
		}
		return siteexport.RefreshIndex(ctx, deps, f.slug)
	}

	opts := siteexport.Options{Since: f.since, Prune: f.all}
	res, err := siteexport.ExportAll(ctx, deps, f.slug, opts)
	if err != nil {
		return err
	}
	logger.Printf("done: %d activities, %d roundups written, %d skipped, %d orphans deleted", res.Activities, res.Roundups, res.Skipped, res.Deleted)
	return nil
}
