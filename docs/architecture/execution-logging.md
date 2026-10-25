# Execution Logging and Framework Architecture

## Overview

FitGlue uses a standardized framework wrapper pattern for all Cloud Run services that provides:

- **Automatic execution logging** - All function invocations logged to Firestore
- **Consistent metadata extraction** - Automatic extraction of `user_id`, `test_run_id`, `pipeline_execution_id`
- **Structured logging** - Pre-configured logger with execution context
- **Sentry integration** - Automatic error capture and context propagation
- **Error handling** - Automatic success/failure logging

## Data Model

### Execution Records

Stored in user sub-collections: `users/{userId}/executions/{executionId}`

```typescript
{
  execution_id: string;           // Unique identifier
  service: string;                // Function name (e.g., "enricher")
  user_id: string;                // User ID
  test_run_id?: string;           // For test isolation
  pipeline_execution_id?: string; // Links to PipelineRun
  trigger_type: string;           // "http" or "pubsub"
  status: ExecutionStatus;        // PENDING → STARTED → SUCCESS/FAILED
  inputs?: any;                   // Function inputs (optional)
  outputs?: any;                  // Function outputs (on success)
  error?: string;                 // Error message (on failure)
  start_time: Timestamp;
  end_time?: Timestamp;
}
```

### PipelineRun (Lifecycle Tracking)

Stored in: `users/{userId}/pipeline_runs/{pipelineExecutionId}`

Tracks complete pipeline execution lifecycle:

```typescript
{
  id: string;                     // pipelineExecutionId
  pipeline_id: string;
  activity_id: string;
  source: string;                 // ActivitySource enum
  source_activity_id?: string;    // External ID from source
  title: string;
  description: string;
  type: ActivityType;
  start_time: Timestamp;
  status: PipelineRunStatus;      // RUNNING → SUCCESS/FAILED/WAITING
  created_at: Timestamp;
  updated_at: Timestamp;
  boosters: BoosterExecution[];   // Enricher executions
  destinations: DestinationOutcome[]; // Upload results
  original_payload_uri: string;   // GCS URI for retry/repost
}
```

**BoosterExecution:**
```typescript
{
  provider_name: string;
  status: string;                 // SUCCESS, FAILED, SKIPPED, WAITING
  duration_ms: number;
  error?: string;
  metadata: Record<string, string>;
}
```

**DestinationOutcome:**
```typescript
{
  destination: Destination;
  status: DestinationStatus;      // PENDING → UPLOADED/FAILED/SKIPPED
  external_id?: string;           // ID from destination platform
  error?: string;
  synced_at?: Timestamp;
}
```

## Framework Wrappers

### Go Framework (`pkg/framework/wrapper.go`)

**Pattern:**
```go
// Entry point - unchanged signature for Cloud Functions
func EnrichActivity(ctx context.Context, e event.Event) error {
    svc, err := initService(ctx)
    if err != nil {
        return fmt.Errorf("service init failed: %v", err)
    }
    return framework.WrapCloudEvent("enricher", svc, enrichHandler)(ctx, e)
}

// Handler - receives FrameworkContext
func enrichHandler(ctx context.Context, e event.Event, fwCtx *framework.FrameworkContext) (interface{}, error) {
    // Use fwCtx.Logger (has execution_id, user_id)
    fwCtx.Logger.Info("Starting enrichment")
    
    // Use fwCtx.Service (DB, Pub, Store, Config)
    userData, err := fwCtx.Service.DB.GetUser(ctx, userId)
    
    // Return outputs for logging
    return enrichedEvent, nil
}
```

**FrameworkContext:**
```go
type FrameworkContext struct {
    Service             *bootstrap.Service  // DB, Pub, Store, Secrets, Config
    Logger              *slog.Logger        // Pre-configured with execution context
    ExecutionID         string              // Unique execution identifier
    PipelineExecutionId string              // Links to PipelineRun (if applicable)
}
```

**Automatic Features:**
- Execution logging (PENDING → STARTED → SUCCESS/FAILURE)
- CloudEvent unwrapping (handles Pub/Sub envelope)
- Metadata extraction (`user_id`, `test_run_id`, `pipeline_execution_id`)
- Sentry error capture with context
- Panic recovery

## Sentry Integration (optional)

Sentry is enabled per process by `SENTRY_DSN`; with it unset every capture
below is a no-op. There is one DSN for all services.

### SentryHandler

The Go logger uses a custom slog handler that captures errors:

```go
// pkg/infrastructure/sentry/sentry.go
type SentryHandler struct {
    handler slog.Handler
}

// Automatically captures Error-level logs as Sentry exceptions
func (h *SentryHandler) Handle(ctx context.Context, r slog.Record) error {
    if r.Level >= slog.LevelError {
        // Extract error from attributes
        // Capture to Sentry with context
        CaptureException(err, context, h.logger)
    }
    return h.handler.Handle(ctx, r)
}
```

### Context Propagation

Both frameworks propagate context to Sentry:
- `user.id` - User ID
- `service` - Function name
- `execution_id` - Execution ID
- `pipeline_execution_id` - Pipeline run ID (if applicable)
- `trigger_type` - HTTP or Pub/Sub

## Execution Logging Options

### Disable Logging (High-Volume Endpoints)

```typescript
export const registryHandler = createCloudFunction(handler, {
  skipExecutionLogging: true, // Don't log every registry fetch
});
```

### Custom Status Updates

```go
// Update execution status mid-flight
execution.LogExecutionStatus(ctx, db, userId, execId, "PROCESSING", nil)
```

## Testing with Test Run IDs

Integration tests use `test_run_id` for isolation:

```typescript
describe('Integration Test', () => {
  const testRunId = randomUUID();
  
  it('should process activity', async () => {
    // HTTP request
    await axios.post(endpoint, payload, {
      headers: { 'X-Test-Run-Id': testRunId }
    });
    
    // Verify by test run ID
    const executions = await getExecutions({ testRunId });
    expect(executions).toHaveLength(1);
  });
  
  afterAll(async () => {
    await cleanupByTestRunId(testRunId);
  });
});
```

## User Sub-Collections

All execution data is stored in user sub-collections for scalability:

```
users/{userId}/
├── executions/{executionId}      # Function execution logs
├── pipeline_runs/{pipelineRunId} # Pipeline lifecycle tracking
├── activities/{activityId}       # Synchronized activities
└── pending_inputs/{inputId}      # Paused inputs awaiting resolution
```

**Benefits:**
- Efficient queries per user
- Firestore security rules per user
- Scalable to millions of users

## Adding a New Go Service Handler

```go
// 1. Create handler
func myHandler(ctx context.Context, e event.Event, fwCtx *framework.FrameworkContext) (interface{}, error) {
    fwCtx.Logger.Info("Processing")
    // Business logic
    return outputs, nil
}

// 2. Wrap in entry point
func MyFunction(ctx context.Context, e event.Event) error {
    svc, _ := initService(ctx)
    return framework.WrapCloudEvent("my-function", svc, myHandler)(ctx, e)
}
```

No manual execution logging required!

## Related Documentation

- [Services & Stores](./services-and-stores.md) - Data access patterns
- [Plugin System](./plugin-system.md) - Plugin architecture
- [Testing Guide](../development/testing.md) - Test patterns
