// Command fitgluectl is the operator CLI for a single-user FitGlue install.
//
//	fitgluectl export-site --slug james [--all | --since <RFC3339>] [--bucket B] [--prefix site/james]
//	fitgluectl export-site --slug james --dry-run --out <dir>
//	fitgluectl rethumb-site --slug james [--dry-run --out <dir>]  re-render every route thumbnail from the privacy-trimmed track
//	fitgluectl fix-calories --slug james [--apply]          store Strava's calories on the entries
//	fitgluectl fix-activity --id ID --type RUN [--distance-m M] [--calories-kcal K] [--apply]  correct a mislabelled activity
//	fitgluectl fix-activity --scan                          list runs stored as strength sessions
//	fitgluectl regen-roundup-stories [--key K|--kind week|--since D] [--force] [--apply]  rewrite roundup stories as the observer
//	fitgluectl regen-activity-summaries [--id ID|--since D] [--limit N] [--force] [--apply]  rewrite the site's activity AI summaries as the observer
//	fitgluectl connect googlehealth|strava|github|spotify  prints the authorise URL (fitbit: legacy, dead 2 Nov 2026)
//	fitgluectl connect hevy --api-key-stdin                 stores the key + mints the webhook ingress key
//	fitgluectl connect intervals --api-key-stdin --athlete-id i12345
//	fitgluectl connections [--json]                         what is connected, token expiry
//	fitgluectl trigger roundup --data '{"period_type":"week"}' publish a roundup trigger (the box timers)
//	fitgluectl trigger parkrun-check                        publish a parkrun results-check trigger
//	fitgluectl reconcile [--since 14d] [--apply] [--json]   three-way check Hevy × FitGlue × Strava; fixes missed ingests/uploads
//	fitgluectl health-check                                 sleep/body/nutrition routes against the live grant: counts and dates only
//	fitgluectl voice-samples [--since 2025-01-01] [--out PATH] his own words from pipeline runs, JSONL 0600; prints counts only
//
// It talks to Firestore, GCS and Pub/Sub directly with Application Default Credentials
// (gcloud auth application-default login, or a service account), so it runs
// from the box with no services deployed. See docs/guides/site-export.md and
// docs/guides/connect-sources.md.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	firestore "cloud.google.com/go/firestore"
	gcs "cloud.google.com/go/storage"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/activity/siteexport"
	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/internal/user"
	stravamap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/strava"
	infradb "github.com/fitglue/server/src/go/pkg/infrastructure/database"
	"github.com/fitglue/server/src/go/pkg/infrastructure/oauth"
	"github.com/fitglue/server/src/go/pkg/infrastructure/storage"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: fitgluectl <command> [flags]")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  rethumb-site  re-render route thumbnails from the privacy-trimmed track")
	fmt.Fprintln(os.Stderr, "  export-site   export the showcase as static JSON for the personal site")
	fmt.Fprintln(os.Stderr, "  fix-calories  correct stored entry calories to the source platform's figure (dry run unless --apply)")
	fmt.Fprintln(os.Stderr, "  fix-activity  correct a showcased activity's type/distance/calories and everything derived (dry run unless --apply); --scan lists suspects")
	fmt.Fprintln(os.Stderr, "  regen-roundup-stories  rewrite roundup stories through the Claude path as the observer (dry run unless --apply)")
	fmt.Fprintln(os.Stderr, "  regen-activity-summaries  rewrite the site's activity AI summaries through the enricher's path (site only; dry run unless --apply)")
	fmt.Fprintln(os.Stderr, "  connect       print a provider's OAuth authorise URL, or store an API key (hevy, intervals)")
	fmt.Fprintln(os.Stderr, "  connections   list the owner's connected providers")
	fmt.Fprintln(os.Stderr, "  trigger       publish a roundup / parkrun-check trigger message (what the box timers run)")
	fmt.Fprintln(os.Stderr, "  reconcile     three-way check of Hevy, FitGlue and Strava; --apply replays missed workouts and reposts missing Strava uploads")
	fmt.Fprintln(os.Stderr, "  health-check  run the sleep/body/nutrition routes against Google Health; prints counts and date ranges only")
	fmt.Fprintln(os.Stderr, "  voice-samples his own words from every pipeline run, to a private JSONL (0600); prints counts only")
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
	case "rethumb-site":
		err = rethumbSiteCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, connectRethumbGCP)
	case "fix-calories":
		err = fixCaloriesCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, connectGCP)
	case "fix-activity":
		err = fixActivityCommand(ctx, os.Args[2:], os.Stdout, os.Stderr)
	case "regen-roundup-stories":
		err = regenStoriesCommand(ctx, os.Args[2:], os.Stdout, os.Stderr)
	case "regen-activity-summaries":
		err = regenSummariesCommand(ctx, os.Args[2:], os.Stdout, os.Stderr)
	case "connect":
		err = connectCommand(ctx, os.Args[2:], os.Stdin, os.Stdout, os.Stderr, openOwnerStore)
	case "connections":
		err = connectionsCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, openOwnerStore)
	case "trigger":
		err = triggerCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, publishToPubSub)
	case "reconcile":
		err = reconcileCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, openLiveReconcile, time.Now)
	case "health-check":
		err = healthCheckCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, openLiveHealth)
	case "voice-samples":
		err = voiceSamplesCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, openLiveVoiceSamples)
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
	cache := siteexport.DefaultVisibilityCachePath(os.Getenv)
	db := infradb.NewFirestoreAdapter(fs)
	vis := siteexport.NewStravaVisibility(stravamap.APIBase, cache, func(ctx context.Context, uid string) (string, error) {
		tok, err := oauth.NewFirestoreTokenSourceFromDB(db, uid, "strava").Token(ctx)
		if err != nil {
			return "", err
		}
		return tok.AccessToken, nil
	}, nil)
	source := siteexport.ServiceSource{Service: svc, Store: store, Visibility: vis}

	var writer siteexport.Writer
	if !f.dryRun {
		writer = &siteexport.GCSWriter{Client: gcsClient, Bucket: f.bucket, Prefix: f.prefix}
	}
	return source, writer, cleanup, nil
}

// openOwnerStore opens the user store that the user service itself writes
// through, so connect/connections see exactly what api-client and
// api-webhook see.
func openOwnerStore(ctx context.Context, project string) (integrationStore, func(), error) {
	fs, err := firestore.NewClient(ctx, project)
	if err != nil {
		return nil, nil, fmt.Errorf("firestore client: %w", err)
	}
	return user.NewFirestoreStore(fs), func() { fs.Close() }, nil
}
