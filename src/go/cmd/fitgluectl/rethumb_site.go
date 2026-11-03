package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	gcs "cloud.google.com/go/storage"

	"github.com/fitglue/server/src/go/internal/activity/siteexport"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/route_thumbnail"
)

type rethumbFlags struct {
	exportSiteFlags
}

// rethumbConnector opens the data source and the thumbnail store (a bucket, or a
// directory when dry-running).
type rethumbConnector func(ctx context.Context, f *rethumbFlags) (siteexport.Source, siteexport.ThumbnailPut, func(), error)

func parseRethumbFlags(args []string, stderr io.Writer) (*rethumbFlags, error) {
	f := &rethumbFlags{}
	fs := flag.NewFlagSet("rethumb-site", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.slug, "slug", "", "showcase profile slug (required)")
	fs.StringVar(&f.project, "project", defaultProject, "GCP project holding Firestore")
	fs.StringVar(&f.bucket, "bucket", "", "showcase-assets bucket (default $"+bucketEnv+", else <project>-showcase-assets)")
	fs.BoolVar(&f.dryRun, "dry-run", false, "write to --out instead of the bucket")
	fs.StringVar(&f.out, "out", "", "local directory for --dry-run")
	fs.IntVar(&f.concurrency, "concurrency", 8, "parallel thumbnail renders")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if f.slug == "" {
		return nil, fmt.Errorf("--slug is required")
	}
	if f.dryRun && f.out == "" {
		return nil, fmt.Errorf("--dry-run requires --out <dir>")
	}
	if !f.dryRun && f.out != "" {
		return nil, fmt.Errorf("--out only applies with --dry-run")
	}
	if f.bucket == "" {
		f.bucket = os.Getenv(bucketEnv)
	}
	if f.bucket == "" {
		f.bucket = f.project + "-showcase-assets"
	}
	return f, nil
}

// rethumbSiteCommand re-renders every published route thumbnail from the trimmed track
// (same privacy zones and whole rule as the site export), overwriting the same objects
// at the same URLs. Prints counts only.
func rethumbSiteCommand(ctx context.Context, args []string, stdout, stderr io.Writer, connect rethumbConnector) error {
	f, err := parseRethumbFlags(args, stderr)
	if err != nil {
		return err
	}
	source, put, cleanup, err := connect(ctx, f)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	return runRethumb(ctx, f, source, put, stdout, log.New(stderr, "", log.Ltime))
}

func runRethumb(ctx context.Context, f *rethumbFlags, source siteexport.Source, put siteexport.ThumbnailPut, stdout io.Writer, logger *log.Logger) error {
	res, err := siteexport.RewriteThumbnails(ctx, siteexport.ThumbsDeps{
		Source:      source,
		Zones:       siteexport.ZonesFromEnv(os.Getenv),
		Render:      route_thumbnail.RenderTrack,
		Put:         put,
		Logf:        logger.Printf,
		Concurrency: f.concurrency,
	}, f.slug)
	fmt.Fprintf(stdout, "thumbnails: %d trimmed, %d whole, %d placeholder, %d skipped (no thumbnail URL)\n",
		res.Trimmed, res.Whole, res.Placeholder, res.Skipped)
	return err
}

// connectRethumbGCP reuses export-site's Firestore/GCS wiring.
func connectRethumbGCP(ctx context.Context, f *rethumbFlags) (siteexport.Source, siteexport.ThumbnailPut, func(), error) {
	ef := f.exportSiteFlags
	ef.dryRun = false // always open the clients: the showcase blobs are read from GCS
	source, writer, cleanup, err := connectGCP(ctx, &ef)
	if err != nil {
		return nil, nil, nil, err
	}
	if f.dryRun {
		return source, func(_ context.Context, obj string, svg []byte) error {
			p := filepath.Join(f.out, filepath.FromSlash(obj))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			return os.WriteFile(p, svg, 0o644)
		}, cleanup, nil
	}
	gw, ok := writer.(*siteexport.GCSWriter)
	if !ok {
		cleanup()
		return nil, nil, nil, fmt.Errorf("unexpected writer %T", writer)
	}
	client := gw.Client
	return source, func(ctx context.Context, obj string, svg []byte) error {
		return putSVG(ctx, client, f.bucket, obj, svg)
	}, cleanup, nil
}

func putSVG(ctx context.Context, client *gcs.Client, bucket, obj string, svg []byte) error {
	wc := client.Bucket(bucket).Object(strings.TrimPrefix(obj, "/")).NewWriter(ctx)
	wc.ContentType = "image/svg+xml"
	wc.CacheControl = "public, max-age=300"
	if _, err := wc.Write(svg); err != nil {
		_ = wc.Close()
		return err
	}
	return wc.Close()
}
