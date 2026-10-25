package main

import (
	"context"
	"log"
	"net"
	"os"

	cloudFirestore "cloud.google.com/go/firestore"
	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/internal/user"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger := infra.NewLoggerWithComponent("user")
	infra.InitSentry()
	ctx := context.Background()

	// Firestore Setup — use ADC on Cloud Run
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID = os.Getenv("PROJECT_ID")
	}
	fsClient, err := cloudFirestore.NewClient(ctx, projectID)
	if err != nil {
		logger.Error(ctx, "failed to initialize firestore client", "err", err)
		os.Exit(1)
	}

	store := user.NewFirestoreStore(fsClient)
	svc := user.NewService(store, logger)

	server := grpc.NewServer(grpc.UnaryInterceptor(infra.LoggingUnaryInterceptor(logger)))
	pbsvc.RegisterUserServiceServer(server, svc)

	healthcheck := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthcheck)

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	logger.Info(context.Background(), "Starting service.user", "port", port)
	if err := server.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
