# Error Codes Reference

FitGlue uses structured error types for consistent error handling across Go and TypeScript.

## Error Packages

| Language | Location |
|----------|----------|
| Go | `pkg/errors/errors.go` |
| TypeScript | `shared/src/errors/index.ts` |

## Error Codes

All codes are strings matching between Go and TypeScript.

### User Errors

| Code | Retryable | Description |
|------|:---------:|-------------|
| `USER_NOT_FOUND` | ❌ | User doesn't exist |
| `USER_UNAUTHORIZED` | ❌ | Missing or invalid authentication |
| `USER_FORBIDDEN` | ❌ | Insufficient permissions |

### Integration Errors

| Code | Retryable | Description |
|------|:---------:|-------------|
| `INTEGRATION_NOT_FOUND` | ❌ | Integration not configured |
| `INTEGRATION_EXPIRED` | ✅ | Token needs refresh |
| `INTEGRATION_AUTH_FAILED` | ❌ | OAuth failed |
| `INTEGRATION_RATE_LIMITED` | ✅ | Hit API rate limit |

### Pipeline Errors

| Code | Retryable | Description |
|------|:---------:|-------------|
| `PIPELINE_NOT_FOUND` | ❌ | Pipeline doesn't exist |
| `PIPELINE_INVALID_CONFIG` | ❌ | Invalid configuration |

### Enricher Errors

| Code | Retryable | Description |
|------|:---------:|-------------|
| `ENRICHER_FAILED` | ✅ | Transient failure |
| `ENRICHER_NOT_FOUND` | ❌ | Enricher type unknown |
| `ENRICHER_TIMEOUT` | ✅ | Took too long |
| `ENRICHER_SKIPPED` | ❌ | Activity filtered |

### Activity Errors

| Code | Retryable | Description |
|------|:---------:|-------------|
| `ACTIVITY_NOT_FOUND` | ❌ | Activity doesn't exist |
| `ACTIVITY_INVALID_FORMAT` | ❌ | Malformed activity data |

### Infrastructure Errors

| Code | Retryable | Description |
|------|:---------:|-------------|
| `STORAGE_ERROR` | ✅ | Firestore/GCS issue |
| `PUBSUB_ERROR` | ✅ | Pub/Sub issue |
| `SECRET_ERROR` | ✅ | Secret Manager issue |

### General Errors

| Code | Retryable | Description |
|------|:---------:|-------------|
| `VALIDATION_ERROR` | ❌ | Invalid input |
| `INTERNAL_ERROR` | ❌ | Unexpected error |
| `TIMEOUT_ERROR` | ✅ | Operation timed out |

## Usage

### Go

```go
import "github.com/ripixel/fitglue-server/src/go/pkg/errors"

// Use sentinel error
return nil, errors.ErrUserNotFound

// Wrap with context
return nil, errors.ErrUserNotFound.WithCause(err).WithMetadata("userId", userId)

// Check retryable
if errors.IsRetryable(err) {
    // retry
}
```

### Destination Errors

```
DESTINATION_UPLOAD_FAILED     retryable=true     External upload API returned an error
DESTINATION_AUTH_EXPIRED      retryable=false    OAuth token expired and refresh failed
DESTINATION_RATE_LIMITED      retryable=true     External API rate limit exceeded
DESTINATION_NOT_FOUND         retryable=false    Unknown destination type in pipeline config
DESTINATION_PAYLOAD_MISSING   retryable=false    FIT file or payload not found in GCS
```

### Tier Errors

```
TIER_LIMIT_REACHED            retryable=false    Monthly sync limit exceeded
TIER_BLOCKED                  retryable=false    Feature requires a higher tier (creates ghost PipelineRun)
```

## Usage

### Go

```go
import "github.com/ripixel/fitglue-server/src/go/pkg/errors"

// Return sentinel error
return errors.ErrUserNotFound

// Wrap with context
return errors.ErrUserNotFound.Wrap(err).WithMetadata("userId", userId)

// Check retryable
if errors.IsRetryable(err) {
    // retry
}
```
