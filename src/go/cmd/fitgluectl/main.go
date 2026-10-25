// Command fitgluectl is the operator CLI for a single-user FitGlue install.
//
//	fitgluectl export-site --slug james [--all | --since <RFC3339>] [--bucket B] [--prefix site/james]
//	fitgluectl export-site --slug james --dry-run --out <dir>
//
// It talks to Firestore and GCS directly with Application Default Credentials
// (gcloud auth application-default login, or a service account), so it runs
// from the box with no services deployed. See docs/guides/site-export.md.
package main

import (
	"context"
	"fmt"
	"os"

	firestore "cloud.google.com/go/firestore"
	gcs "cloud.google.com/go/storage"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/activity/siteexport"
	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/infrastructure/storage"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: fitgluectl <command> [flags]")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  export-site   export the showcase as static JSON for the personal site")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "export-site":
		err = exportSiteCommand(ctx, os.Args[2:], os.Stderr, connectGCP)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "fitgluectl: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fitgluectl: %v\n", err)
		os.Exit(1)
	}
}

// connectGCP builds the production Source and, unless dry-running, a bucket
// Writer. The GCS client is still opened for a dry run because the activity
// service reads each showcase's offloaded data blob from the showcase-assets
// bucket; nothing is written.
func connectGCP(ctx context.Context, f *exportSiteFlags) (siteexport.Source, siteexport.Writer, func(), error) {
	fs, err := firestore.NewClient(ctx, f.project)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("firestore client: %w", err)
	}
	gcsClient, err := gcs.NewClient(ctx)
	if err != nil {
		fs.Close()
		return nil, nil, nil, fmt.Errorf("gcs client: %w", err)
	}
	cleanup := func() {
		fs.Close()
		gcsClient.Close()
	}
	store := activity.NewFirestoreStore(fs)
	blobs := &storage.StorageAdapter{Client: gcsClient}
	svc := activity.NewService(store, blobs, nil, f.project+"-artifacts", f.bucket, infra.NewLoggerWithComponent("fitgluectl"))
	source := siteexport.ServiceSource{Service: svc, Store: store}

	var writer siteexport.Writer
	if !f.dryRun {
		writer = &siteexport.GCSWriter{Client: gcsClient, Bucket: f.bucket, Prefix: f.prefix}
	}
	return source, writer, cleanup, nil
}
