# Local Development

This guide explains how to run the FitGlue server stack locally.

## Prerequisites

- **Go 1.22+**: [Install Go](https://go.dev/doc/install)
- **Docker + Docker Compose**: Required for `make local`
- **Protocol Buffers**: `sudo apt-get install protobuf-compiler` (Linux) or `brew install protobuf` (macOS)
- **buf** (optional, for proto regeneration): [Install buf](https://buf.build/docs/installation)

## 1. Initial Setup

Install all dependencies:

```bash
cd server
make setup
```

This downloads Go module dependencies.

## 2. Configuration (`.env`)

```bash
cp .env.example .env
```

Edit `.env` for local secrets (bypasses Google Secret Manager):

```bash
GOOGLE_CLOUD_PROJECT=fitglue-server-dev
GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json
EMAIL_APP_PASSWORD=local-only
SYSTEM_EMAIL=noreply@fitglue.tech
```

## 3. Starting Services

### All 10 Services (Recommended)

```bash
make local
```

Starts all 10 Cloud Run emulators via Docker Compose. Services communicate with each other over the Docker internal network. Logs stream to stdout.

```bash
make local-down   # Stop and remove containers
```

### Single Service (Debugging)

Run an individual service directly as a Go binary:

```bash
cd src/go
go run ./services/user/...
go run ./services/api-client/...
go run ./services/api-webhook/...
```

Each service reads its configuration from environment variables (see `.env`).

## 4. Triggering Events

### Via Direct API Calls

With `make local` running, hit the API gateways directly:

```bash
# Check health
curl http://localhost:8080/health

# Trigger a test webhook (Hevy)
curl -X POST http://localhost:8080/webhook/source/hevy \
  -H "X-Fitglue-Ingress-Key: <ingress-key>" \
  -H "Content-Type: application/json" \
  -d '{"event_type": "workout_created", "workout_id": "test-123"}'
```

## 5. Running Tests

### All Unit Tests

```bash
make test
```

Runs all Go unit tests (short mode) across `pkg/`, `services/`, `cmd/`, and `internal/`.

### Go Tests Only

```bash
cd src/go
go test ./...

# Verbose output with coverage
go test -v -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### E2E Tests (Cucumber / godog)

E2E tests require the full stack running (`make local`):

```bash
# Terminal 1
make local

# Terminal 2
make test-e2e
```

### Integration Tests

```bash
make test-integration
```

Runs tests tagged `Integration` against the dev environment.

## 6. Code Generation

After modifying any `.proto` file:

```bash
make generate
```

This regenerates:
- Go gRPC stubs → `src/go/pkg/types/pb/`
- OpenAPI spec → `docs/api/openapi.yaml`
- TypeScript types → `../web/src/types/pb/`

> [!IMPORTANT]
> Always commit generated files alongside proto changes.

## 7. Building

```bash
make build          # Build all Go services
make build-go       # Same
make docker         # Build Docker images for all 10 services
```

## 8. Linting

```bash
make lint
```

Checks Go formatting (`gofmt`), vet, and the proto-JSON misuse linter.

## Troubleshooting

### Service fails to connect to another service

Check Docker Compose service names in `docker-compose.yaml` match the env vars (e.g., `USER_SERVICE_ADDR`).

### "Firestore credentials not found"

Ensure `GOOGLE_APPLICATION_CREDENTIALS` points to a valid service account key with Firestore access to `fitglue-server-dev`.

### Port already in use

```bash
lsof -ti:8080 | xargs kill -9
```

### Proto regeneration fails

Ensure `protoc` and `buf` are installed. Run `protoc --version` and `buf --version` to verify.

## Related Documentation

- [Testing Guide](testing.md) - Test strategy and patterns
- [CI/CD](../infrastructure/cicd.md) - Deployment pipeline
- [API Layers](../architecture/api-layers.md) - Client, public and webhook API reference
