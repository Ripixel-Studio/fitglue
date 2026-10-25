package server

import (
	"fmt"
	"net/http"

	"firebase.google.com/go/v4/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/fitglue/server/src/go/internal/infra"
	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	pipelinepb "github.com/fitglue/server/src/go/pkg/types/pb/services/pipeline"
	userpb "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
	"github.com/fitglue/server/src/go/services/api-webhook/internal/webhook"
)

// APIServer implements the raw JSON and webhook verification endpoints
type APIServer struct {
	router      *chi.Mux
	logger      infra.Logger
	authClient  *auth.Client
	userService userpb.UserServiceClient
	pipelineSvc pipelinepb.PipelineServiceClient
	activitySvc activitypb.ActivityServiceClient
	processor   *webhook.Processor
}

// NewAPIServer constructs the application routing and API middleware stack
func NewAPIServer(
	logger infra.Logger,
	authClient *auth.Client,
	userSvc userpb.UserServiceClient,
	pipelineSvc pipelinepb.PipelineServiceClient,
	activitySvc activitypb.ActivityServiceClient,
	processor *webhook.Processor,
) *APIServer {
	s := &APIServer{
		router:      chi.NewRouter(),
		logger:      logger,
		authClient:  authClient,
		userService: userSvc,
		pipelineSvc: pipelineSvc,
		activitySvc: activitySvc,
		processor:   processor,
	}

	s.setupRoutes()
	return s
}

// ServeHTTP implements http.Handler automatically so the APIServer can be bound to net/http
func (s *APIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *APIServer) setupRoutes() {
	// Root level middleware (shared by all endpoints)
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(SentryRecoveryMiddleware(s.logger))

	// API Webhook block
	s.router.Route("/api/webhooks", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status": "ok"}`)
		})

		s.registerStravaRoutes(r)
		s.registerFitbitRoutes(r)
		s.registerHevyRoutes(r)
	})
}

func (s *APIServer) registerStravaRoutes(r chi.Router) {
	// Strava requires a GET request during webhook registration and a POST request for event delivery
	r.Get("/strava", s.handleStravaVerification)
	r.Post("/strava", s.handleStravaEvent)
}

func (s *APIServer) handleStravaVerification(w http.ResponseWriter, r *http.Request) {
	s.processor.HandleVerification(w, r, "strava")
}

func (s *APIServer) handleStravaEvent(w http.ResponseWriter, r *http.Request) {
	s.processor.HandleEvent(w, r, "strava")
}

func (s *APIServer) registerFitbitRoutes(r chi.Router) {
	r.Get("/fitbit", s.handleFitbitVerification)
	r.Post("/fitbit", s.handleFitbitEvent)
}

func (s *APIServer) handleFitbitVerification(w http.ResponseWriter, r *http.Request) {
	s.processor.HandleVerification(w, r, "fitbit")
}

func (s *APIServer) handleFitbitEvent(w http.ResponseWriter, r *http.Request) {
	s.processor.HandleEvent(w, r, "fitbit")
}

func (s *APIServer) registerHevyRoutes(r chi.Router) {
	r.Post("/hevy", s.handleHevyEvent)
}

func (s *APIServer) handleHevyEvent(w http.ResponseWriter, r *http.Request) {
	s.processor.HandleEvent(w, r, "hevy")
}

// statusError is a helper for manually generating an error satisfying gRPC status layout
func statusError(code int, msg string) error {
	// Simple wrapper for non-gRPC errors to use WriteError
	return &CustomError{HTTPCode: code, Msg: msg}
}

// CustomError helps map generic HTTP errors into our WriteError handler
type CustomError struct {
	HTTPCode int
	Msg      string
}

func (e *CustomError) Error() string {
	return e.Msg
}
