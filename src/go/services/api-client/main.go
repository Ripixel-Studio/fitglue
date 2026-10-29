package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/pubsub"
	gcstorage "cloud.google.com/go/storage"
	"github.com/fitglue/server/src/go/internal/health"
	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/infrastructure/database"
	infraps "github.com/fitglue/server/src/go/pkg/infrastructure/pubsub"
	infrastorage "github.com/fitglue/server/src/go/pkg/infrastructure/storage"
	"github.com/fitglue/server/src/go/services/api-client/internal/server"

	"google.golang.org/grpc"

	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	pipelinepb "github.com/fitglue/server/src/go/pkg/types/pb/services/pipeline"
	userpb "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger := infra.NewLoggerWithComponent("api-client")
	infra.InitSentry()
	ctx := context.Background()

	// Static bearer auth. FitGlue is single-user: the one token in
	// FITGLUE_API_TOKEN authenticates every request as FITGLUE_OWNER_UID.
	// Both are required — refuse to start rather than serve an API nobody
	// can call (empty token) or that acts as nobody (empty uid).
	authCfg := server.AuthConfig{
		Token:    os.Getenv("FITGLUE_API_TOKEN"),
		OwnerUID: os.Getenv("FITGLUE_OWNER_UID"),
	}
	if authCfg.Token == "" || authCfg.OwnerUID == "" {
		logger.Error(ctx, "FITGLUE_API_TOKEN and FITGLUE_OWNER_UID must both be set — api-client authenticates with a static bearer token and acts as that one user",
			"FITGLUE_API_TOKEN_set", authCfg.Token != "",
			"FITGLUE_OWNER_UID_set", authCfg.OwnerUID != "")
		os.Exit(1)
	}

	// Helper to dial a service
	connect := func(targetEnv string, defaultTarget string) *grpc.ClientConn {
		target := os.Getenv(targetEnv)
		if target == "" {
			target = defaultTarget
		}
		conn, err := infra.GRPCDial(target)
		if err != nil {
			logger.Error(ctx, "failed to configure grpc client", "target", target, "error", err)
			os.Exit(1)
		}
		return conn
	}

	userConn := connect("USER_SERVICE_URL", "localhost:50051")
	defer userConn.Close()
	userClient := userpb.NewUserServiceClient(userConn)

	pipelineConn := connect("PIPELINE_SERVICE_URL", "localhost:50053")
	defer pipelineConn.Close()
	pipelineClient := pipelinepb.NewPipelineServiceClient(pipelineConn)

	activityConn := connect("ACTIVITY_SERVICE_URL", "localhost:50054")
	defer activityConn.Close()
	activityClient := activitypb.NewActivityServiceClient(activityConn)

	// Setup Pub/Sub Client
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID = "fitglue" // Fallback or development default
	}
	pubsubClient, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		logger.Error(ctx, "Failed to initialize Pub/Sub client", "error", err)
		os.Exit(1)
	}
	defer pubsubClient.Close()
	publisher := &infraps.PubSubAdapter{Client: pubsubClient, Logger: logger}

	// Setup Firestore Client for API key storage
	firestoreClient, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		logger.Error(ctx, "Failed to initialize Firestore client", "error", err)
		os.Exit(1)
	}
	defer firestoreClient.Close()
	apiKeyStore := server.NewFirestoreApiKeyStore(firestoreClient)

	// Setup GCS client for signed payload download URLs
	gcsClient, err := gcstorage.NewClient(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to initialize GCS client", "error", err)
		os.Exit(1)
	}
	defer gcsClient.Close()
	gcsSigner := &infrastorage.StorageAdapter{Client: gcsClient}

	// Build API Gateway router
	apiServer := server.NewAPIServer(
		logger,
		authCfg,
		publisher,
		apiKeyStore,
		gcsSigner,
		userClient,
		pipelineClient,
		activityClient,
	)

	// Sleep, body and food from Google Health (L0.1b), read with the owner's
	// googlehealth grant straight from the user store.
	apiServer.SetHealth(health.NewReader(health.FirestoreGrant(database.NewFirestoreAdapter(firestoreClient), authCfg.OwnerUID)))

	logger.Info(ctx, "Starting service.api.client", "port", port)

	if err := http.ListenAndServe(":"+port, infra.LoggingMiddleware(logger, apiServer)); err != nil {
		log.Fatalf("failed to serve HTTP: %v", err)
	}
}
