# Services & Stores Architecture

FitGlue separates business logic from data access using a strict **Service vs Store** pattern. All implementations are in Go, leveraging struct-based IoC injection.

## Philosophy

**Domain services own their data.** Each domain service (`service.user`, `service.pipeline`, etc.) has its own Firestore collections and a `Store` interface. The API gateway layer never touches Firestore directly — it only calls domain services via gRPC.

## Store Pattern (Go)

Stores are interfaces backed by a Firestore implementation. They are injected into services at startup:

```go
// internal/user/store.go
type Store interface {
    Get(ctx context.Context, userID string) (*pb.User, error)
    Update(ctx context.Context, userID string, partial *pb.User) error
    GetIntegration(ctx context.Context, userID, provider string) (*pb.OAuthTokens, error)
    SetIntegration(ctx context.Context, userID, provider string, tokens *pb.OAuthTokens) error
    ListCounters(ctx context.Context, userID string) ([]*pb.Counter, error)
    UpdateCounter(ctx context.Context, userID string, counter *pb.Counter) error
}

// FirestoreStore implements Store
type FirestoreStore struct {
    db *firestore.Client
}
```

Stores are **never** imported by API gateway services — they are internal to the domain service's `internal/` directory.

## Go Stores by Domain

| Domain | Store Interface | Firestore Collections |
|--------|----------------|----------------------|
| `service.user` | `user.Store` | `users/`, `users/*/integrations` |
| `service.pipeline` | `pipeline.Store`, `pipeline.RunStore`, `pipeline.InputStore` | `users/*/pipelines`, `users/*/pipeline_runs`, `users/*/pending_inputs` |
| `service.activity` | `activity.Store` | `users/*/activities`, `showcased_activities/` |

## User Sub-Collections

All user data is stored in sub-collections:

```
users/{userId}/
  ├── pipelines/{pipelineId}          # Pipeline configurations
  ├── pipeline_runs/{pipelineRunId}   # Execution lifecycle tracking
  ├── pending_inputs/{inputId}        # Paused inputs awaiting resolution
  └── activities/{activityId}         # Synchronized activities

integrations/{provider}/ids/{externalId}   # Reverse-lookup: externalId → userId
```

## Service Pattern (Go)

Domain services implement the gRPC server interface generated from `.proto` definitions:

```go
// internal/user/service.go
type Service struct {
    store  Store           // injected
    logger infra.Logger    // injected
}

// NewService is the constructor — accepts all dependencies explicitly
func NewService(store Store, logger infra.Logger) *Service {
    return &Service{store: store, logger: logger}
}

// Implements pb.UserServiceServer — generated gRPC interface
func (s *Service) GetProfile(ctx context.Context, req *pb.GetProfileRequest) (*pb.GetProfileResponse, error) {
    user, err := s.store.Get(ctx, req.UserId)
    if err != nil {
        return nil, status.Errorf(codes.NotFound, "user not found: %v", err)
    }
    return &pb.GetProfileResponse{User: user}, nil
}
```

### Rules

1. **No direct DB access in API gateways** — API gateways call domain services via gRPC only
2. **Services use interfaces, not implementations** — `Store` interface, not `FirestoreStore` directly
3. **No business logic in stores** — Stores only do typed CRUD
4. **Errors use gRPC status codes** — `codes.NotFound`, `codes.InvalidArgument`, etc., so the API gateway can map them to HTTP status codes

## PipelineRun Tracking

The `PipelineRun` document tracks full pipeline execution lifecycle:

```protobuf
message PipelineRun {
  string id = 1;                       // pipelineExecutionId
  string pipeline_id = 2;
  string activity_id = 3;
  PipelineRunStatus status = 4;        // RUNNING → SUCCESS/FAILED
  repeated BoosterExecution boosters = 5;
  repeated DestinationOutcome destinations = 6;
  string original_payload_uri = 7;     // GCS URI for retry
}
```

`pipeline.RunStore` provides typed methods:

```go
func (s *RunStore) Create(ctx context.Context, userID string, run *pb.PipelineRun) error
func (s *RunStore) UpdateStatus(ctx context.Context, userID, runID string, status pb.PipelineRunStatus) error
func (s *RunStore) AddBoosterExecution(ctx context.Context, userID, runID string, exec *pb.BoosterExecution) error
func (s *RunStore) UpdateDestinationStatus(ctx context.Context, userID, runID string, dest pb.Destination, status pb.DestinationStatus) error
```

## gRPC Error → HTTP Status Mapping

API gateways convert gRPC errors to HTTP:

| gRPC Code | HTTP Status |
|-----------|-------------|
| `codes.NotFound` | 404 |
| `codes.InvalidArgument` | 400 |
| `codes.PermissionDenied` | 403 |
| `codes.Unauthenticated` | 401 |
| `codes.Internal` | 500 |

## Firestore Timestamp Handling

> [!IMPORTANT]
> Never use `time.Time` directly with Firestore in Go. Always use `timestamppb.Timestamp` from `google.golang.org/protobuf/types/known/timestamppb` for all timestamp fields in proto messages, and convert to/from Firestore `time.Time` only at the store boundary.

## Related Documentation

- [Architecture Overview](overview.md) - System topology
- [Go Services](go-services.md) - Service directory structure
- [Service Communication](service-communication.md) - gRPC contracts
- [Security](security.md) - Authorization patterns
