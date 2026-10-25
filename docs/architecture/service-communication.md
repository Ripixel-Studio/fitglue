# Service Communication

All inter-service communication in FitGlue uses **gRPC** with protobuf-defined contracts. Domain services (`service.user`, `service.pipeline`, etc.) expose gRPC servers. API gateway services (`service.api.client`, etc.) hold gRPC clients and act as thin HTTP-to-RPC translators.

## Why gRPC?

- **Compile-time safety** — generated client stubs enforce request/response shapes at build time
- **Single source of truth** — `.proto` service definitions generate both the server interface and the client
- **Protobuf wire format** — efficient binary serialisation, no JSON roundtrips between services
- **Streaming support** — available for future high-throughput scenarios

## Service Ports

| Service | Port | Protocol |
|---------|------|----------|
| `service.user` | 8080+ (Cloud Run auto) | gRPC |
| `service.pipeline` | 8080+ | gRPC + Pub/Sub consumer |
| `service.activity` | 8080+ | gRPC + Pub/Sub consumer |
| `service.registry` | 8080+ | gRPC |
| `service.destination` | — | Pub/Sub consumer only |
| `service.api.*` | 8080 | HTTP (external) |

## gRPC Client Setup

API gateways connect to domain services via generated client stubs:

```go
// services/api-client/main.go
userConn, _ := grpc.Dial(os.Getenv("USER_SERVICE_ADDR"),
    grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
    grpc.WithPerRPCCredentials(oauth.NewOauthAccess(token)),
)
userClient := pbsvc.NewUserServiceClient(userConn)

api := server.NewClientAPI(userClient, pipelineClient, activityClient, registryClient)
```

The generated `pb.UserServiceClient` is type-safe — the compiler enforces that every call passes the correct request type and handles the correct response type.

## Service-to-Service Authentication

On Cloud Run, services authenticate to each other using **Google-managed OIDC tokens**:

1. The calling service obtains an OIDC token from the metadata server
2. The token is passed as an `Authorization: Bearer <token>` gRPC credential
3. The receiving service verifies the token against the calling service's Cloud Run identity

Locally (`make local`), services connect without auth (Docker Compose internal network).

## Pub/Sub Topics

Where services communicate asynchronously, they share 4 topics:

| Topic | Producer | Consumer |
|-------|----------|----------|
| `topic-raw-activity` | `service.api.webhook` | `service.pipeline` (splitter) |
| `topic-pipeline-activity` | `service.pipeline` (splitter) | `service.pipeline` (enricher) |
| `topic-enriched-activity` | `service.pipeline` (enricher) | `service.destination` |
| `topic-destination-upload` | `service.pipeline` (router) | `service.destination` |

## Proto File Layout

```
src/proto/
├── models/                   # Message types (no service defs)
│   ├── user/
│   ├── pipeline/
│   ├── activity/
│   ├── plugin/
│   └── events/
└── services/                 # Service definitions (RPC contracts)
    ├── user.proto             # UserService
    ├── pipeline.proto         # PipelineService
    ├── activity.proto         # ActivityService
    └── registry.proto         # RegistryService
```

## Regenerating Stubs

```bash
make generate
```

This runs:
1. `protoc` → Go gRPC stubs to `src/go/pkg/types/pb/`
2. `buf` → OpenAPI 3.x spec to `docs/api/openapi.yaml`
3. `ts-proto` → TypeScript types to `../web/src/types/pb/`

> [!IMPORTANT]
> Run `make generate` after any `.proto` change. Commit the generated files alongside the proto changes — never edit generated files directly.

## Call Graph Summary

```
Web/Mobile → service.api.client → service.user      (GetProfile, UpdateProfile, etc.)
                                → service.pipeline   (ListPipelines, CreatePipeline, etc.)
                                → service.activity   (ListActivities, GetShowcase, etc.)
                                → service.registry   (GetRegistry)

Public     → service.api.public → service.registry  (public registry listing)
                                → service.activity   (GetPublicShowcase)

Webhooks   → service.api.webhook → service.user     (GetIntegration via RPC)
                                 → Pub/Sub           (publishes topic-raw-activity)
```

## Related Documentation

- [Go Services](go-services.md) - Service structure and IoC pattern
- [API Layers](api-layers.md) - The four HTTP gateways
- [Architecture Overview](overview.md) - System topology
