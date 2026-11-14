package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/storage"
	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/activity/siteexport"
	"github.com/fitglue/server/src/go/internal/infra"
	shared "github.com/fitglue/server/src/go/pkg"
	stravamap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/strava"
	infradb "github.com/fitglue/server/src/go/pkg/infrastructure/database"
	"github.com/fitglue/server/src/go/pkg/infrastructure/oauth"
	infrapubsub "github.com/fitglue/server/src/go/pkg/infrastructure/pubsub"
	gcsstorage "github.com/fitglue/server/src/go/pkg/infrastructure/storage"
	pb "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
	}

	ctx := context.Background()
	logger := infra.NewLoggerWithComponent("activity")
	infra.InitSentry()

	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		projectID = "fitglue-server-dev"
	}

	fsClient, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("failed to init firestore: %v", err)
	}
	defer fsClient.Close()
	store := activity.NewFirestoreStore(fsClient)

	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("failed to init gcs: %v", err)
	}
	defer gcsClient.Close()
	blobStore := &GCSBlobStore{adapter: &gcsstorage.StorageAdapter{Client: gcsClient}}

	pubsubClient, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("failed to init pubsub: %v", err)
	}
	defer pubsubClient.Close()
	pub := &infrapubsub.PubSubAdapter{Client: pubsubClient, Logger: logger}

	bucketName := os.Getenv("ARTIFACT_BUCKET")
	if bucketName == "" {
		bucketName = "fitglue-server-dev-artifacts"
	}
	showcaseAssetsBucket := os.Getenv("SHOWCASE_ASSETS_BUCKET")
	if showcaseAssetsBucket == "" {
		showcaseAssetsBucket = "fitglue-server-dev-showcase-assets"
	}

	svc := activity.NewService(store, blobStore, pub, bucketName, showcaseAssetsBucket, logger)

	// Site export on change (fitness-zine S1.2): after a showcase entry or a
	// roundup is written, the matching site file and index.json are rewritten
	// in the showcase-assets bucket, then the box's site rebuild is signalled
	// (SITE_EXPORT_SIGNAL_FILE, S6). SITE_EXPORT_DISABLED=1 turns it off;
	// `fitgluectl export-site --all` is the repair path either way.
	if cfg := siteexport.ConfigFromEnv(os.Getenv, showcaseAssetsBucket); cfg.Disabled {
		logger.Info(ctx, "site export on change is disabled")
	} else {
		svc.SetSiteExporter(siteexport.NewHook(siteexport.HookConfig{
			Slug:       cfg.Slug,
			HookURL:    cfg.HookURL,
			SignalFile: cfg.SignalFile,
			Logger:     logger,
			Deps: siteexport.Deps{
				Source: siteexport.ServiceSource{Service: svc, Store: store, Visibility: siteexport.NewStravaVisibility(
					stravamap.APIBase, siteexport.DefaultVisibilityCachePath(os.Getenv), stravaTokenFor(infradb.NewFirestoreAdapter(fsClient)), nil)},
				Writer: &siteexport.GCSWriter{Client: gcsClient, Bucket: cfg.Bucket, Prefix: cfg.Prefix()},
				Zones:  siteexport.ZonesFromEnv(os.Getenv),
			},
		}))
		logger.Info(ctx, "site export on change enabled", "slug", cfg.Slug, "bucket", cfg.Bucket, "hook_url", cfg.HookURL, "signal_file", cfg.SignalFile)
	}

	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(infra.LoggingUnaryInterceptor(logger)))
	pb.RegisterActivityServiceServer(grpcServer, svc)
	healthcheck := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthcheck)

	mux := http.NewServeMux()
	// On Cloud Run the platform verifies the push subscription's OIDC token;
	// on the box PUBSUB_PUSH_AUDIENCE turns the check on here.
	pushAuth := infra.PubSubPushAuthFromEnv(logger)
	mux.HandleFunc("/pubsub/roundup", pushAuth.WrapFunc(svc.HandleRoundupTrigger))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	loggingMux := infra.LoggingMiddleware(logger, mux)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
		} else {
			loggingMux.ServeHTTP(w, r)
		}
	})

	h2s := &http2.Server{}
	logger.Info(ctx, "Starting service.activity (gRPC + HTTP)", "port", port)
	if err := http.ListenAndServe(os.Getenv("BIND_ADDR")+":"+port, h2c.NewHandler(handler, h2s)); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// stravaTokenFor returns an owner's Strava access token for the site export's
// visibility check (stored connection, refreshed as needed).
func stravaTokenFor(db shared.Database) func(ctx context.Context, uid string) (string, error) {
	return func(ctx context.Context, uid string) (string, error) {
		tok, err := oauth.NewFirestoreTokenSourceFromDB(db, uid, "strava").Token(ctx)
		if err != nil {
			return "", err
		}
		return tok.AccessToken, nil
	}
}
