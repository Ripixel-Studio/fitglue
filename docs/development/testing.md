# Testing Guide

This guide covers the testing strategy for FitGlue's Go services: unit tests, integration tests, and E2E tests.

## Overview

| Test Type | Command | Purpose |
|-----------|---------|---------|
| Unit Tests | `make test` | Validate individual service and package logic |
| Integration Tests | `make test-integration` | Validate cross-service flows against dev GCP |
| E2E Tests | `make test-e2e` | BDD cucumber scenarios against the full local stack |
| Coverage Check | `make test-coverage` | Enforce 80% minimum per package |

---

## 1. Unit Tests

Unit tests use Go's built-in `testing` package with table-driven patterns. All dependencies (stores, external clients) are injected via interfaces and mocked in tests.

### Running Tests

```bash
# All unit tests
make test

# Specific package
cd src/go
go test -v ./internal/user/...
go test -v ./services/api-client/...

# With coverage
go test -coverprofile=coverage.out ./pkg/... ./services/... ./internal/...
go tool cover -html=coverage.out
```

### Go Test Structure

Tests live alongside the code they test (`*_test.go` files in the same package):

```
internal/user/
  ├── service.go
  ├── service_test.go      # Unit tests for service.go
  ├── store.go
  └── store_test.go
```

### Mock Pattern (Interface Injection)

Domain services accept interfaces, making them trivially testable:

```go
// service_test.go
type mockStore struct {
    getUserFn func(ctx context.Context, userID string) (*pb.User, error)
}

func (m *mockStore) Get(ctx context.Context, userID string) (*pb.User, error) {
    return m.getUserFn(ctx, userID)
}

func TestGetProfile(t *testing.T) {
    tests := []struct {
        name    string
        userID  string
        store   user.Store
        wantErr bool
    }{
        {
            name:   "returns profile successfully",
            userID: "user-123",
            store: &mockStore{
                getUserFn: func(_ context.Context, id string) (*pb.User, error) {
                    return &pb.User{Id: id, DisplayName: "Test User"}, nil
                },
            },
        },
        {
            name:    "returns error when user not found",
            userID:  "missing",
            store:   &mockStore{getUserFn: func(_ context.Context, _ string) (*pb.User, error) {
                return nil, errors.New("not found")
            }},
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            svc := user.NewService(tt.store, testLogger, nil, nil)
            resp, err := svc.GetProfile(context.Background(), &pb.GetProfileRequest{UserId: tt.userID})
            if (err != nil) != tt.wantErr {
                t.Errorf("GetProfile() error = %v, wantErr %v", err, tt.wantErr)
            }
            if !tt.wantErr && resp.User.Id != tt.userID {
                t.Errorf("expected user ID %s, got %s", tt.userID, resp.User.Id)
            }
        })
    }
}
```

### Coverage Requirement

An 80% minimum coverage is enforced per package by `scripts/check-coverage.sh`:

```bash
make test-coverage
```

CI blocks on coverage drops below this threshold.

---

## 2. E2E Tests (Cucumber / godog)

E2E tests use the [godog](https://github.com/cucumber/godog) BDD framework with Gherkin `.feature` files.

**Location:** `src/go/tests/e2e/`

```
tests/e2e/
  ├── features/
  │   ├── pipeline.feature    # Full pipeline flow scenarios
  │   ├── user.feature        # User management scenarios
  │   └── webhook.feature     # Webhook ingestion scenarios
  └── steps_test.go           # Step definitions
```

### Running E2E Tests

Requires the full stack (`make local`) running:

```bash
# Terminal 1
make local

# Terminal 2
make test-e2e
```

### Test Run ID Isolation

Each test generates a unique `testRunId` propagated via HTTP headers (`X-Test-Run-Id`) and Pub/Sub message attributes. This enables:
- Precise test execution verification
- Complete cleanup of test data after runs
- No cross-test pollution

---

## 3. Integration Tests

Integration tests make real calls to the dev GCP environment:

```bash
# Requires GCP Application Default Credentials
gcloud auth application-default login

make test-integration
```

These test tags are `Integration` and are excluded from `make test` (short mode).

---

## 4. Manual QA

Manual QA validates end-to-end flows with real external services (Hevy, Fitbit, Strava).

### User Setup

Use direct Firestore access (or the authenticated `service.api.client` routes as the test user) to set up test users and pipelines.

### Verifying a Pipeline Run

Query Firestore `users/{userId}/pipeline_runs/` for execution records, or use the `synchronized:list` endpoint via `service.api.client` to inspect results.

---

## Best Practices

1. **Table-driven tests** — Use `[]struct{ name string; ... }` patterns for exhaustive coverage
2. **Mock at the interface boundary** — Never mock concrete types
3. **Test one thing per test** — Keep assertions focused
4. **Use `t.Run(tt.name, ...)` always** — Makes failures identifiable
5. **Clean up in `t.Cleanup` or `defer`** — Don't leak test data to shared environments

---

## Related Documentation

- [Local Development](local-development.md) - Running the stack locally
