# API Layers

FitGlue has three distinct HTTP gateway services, each with its own auth policy and permitted callers. None of them contain domain logic or touch Firestore directly — they are pure HTTP-to-gRPC translators.

## The Three Gateways

| Service | Callers | Auth | gRPC Clients |
|---------|---------|------|-------------|
| `service.api.client` | The owner's tools (Jarvis, fitgluectl, fitglue-mcp) | Static bearer token (`FITGLUE_API_TOKEN`) | user, pipeline, activity |
| `service.api.public` | Unauthenticated browser | None (rate-limited) | activity |
| `service.api.webhook` | Third-party services (Strava, Fitbit, etc.) | Provider HMAC / signature | user (via RPC), Pub/Sub |

## service.api.client

The owner's HTTP API. FitGlue is single-user, so there is exactly one caller
identity.

**Base path:** `/api/v2/`

**Auth:** Every request must carry the static token in `Authorization: Bearer <token>`, where `<token>` is the value `api-client` was started with as `FITGLUE_API_TOKEN`. The middleware (`internal/server/middleware.go`) compares it in constant time and injects a `Principal` whose `UID` is `FITGLUE_OWNER_UID`. Missing or wrong token: `401`. The OAuth callbacks (`/oauth/{provider}/callback`, legacy `/auth/{provider}/callback`) and `/health` are outside the authenticated group. See [Security](security.md).

**Handler pattern:**
```go
func (s *APIServer) handleGetProfile(w http.ResponseWriter, r *http.Request) {
    token := getUserToken(r) // *Principal set by AuthMiddleware; UID is always the owner
    if token == nil {
        WriteError(w, statusError(http.StatusUnauthorized, "missing user context"))
        return
    }
    profile, err := s.userService.GetProfile(r.Context(), &userpb.GetProfileRequest{UserId: token.UID})
    if err != nil {
        WriteError(w, err)
        return
    }
    WriteJSON(w, profile)
}
```

No database access. No business logic. Only: extract from request → call RPC → write response.

**Key route groups:**
- `GET/PATCH /api/users/me` — Profile
- `GET/POST/PATCH/DELETE /api/users/me/pipelines/{id}` — Pipeline CRUD
- `GET /api/users/me/activities` — Activity list
- `POST /api/users/me/inputs/{id}/resolve` — Pending input submission
- `POST /api/users/me/activities/{id}/repost` — Repost activity
- `GET/POST /api/auth/{provider}` — OAuth initiation and callback

## service.api.public

Unauthenticated endpoints for public-facing consumers.

**Auth:** None. Rate-limited at the load balancer level.

**Key routes:**
- `GET /api/showcase/{id}` — Public activity showcase page data

## service.api.webhook

Inbound webhook processor for all third-party sources.

**Auth:** Per-provider HMAC or signature verification (no bearer token).

**Architecture:** Uses a `SourceProvider` interface + `WebhookProcessor` orchestrator. Each source provider is an isolated Go package:

```
internal/webhook/sources/
├── strava/provider.go      # Implements SourceProvider
├── fitbit/provider.go
├── hevy/provider.go
├── intervals/provider.go
├── github/provider.go
└── parkrun/provider.go
```

**Webhook request lifecycle:**
1. Route matched to `/{provider}` — provider looked up in `SourceRegistry`
2. `source.VerifyWebhook(r)` — HMAC / signature check
3. `source.ResolveUser(ctx, body)` — extract provider user ID
4. `h.users.GetIntegration(ctx, req)` — RPC to `service.user` for OAuth credentials
5. `source.FetchActivity(ctx, externalID, creds)` — fetch from provider API
6. Publish `StandardizedActivity` to `topic-raw-activity`

**Adding a new source:**
```bash
make plugin-source name=garmin
```
Creates `internal/webhook/sources/garmin/provider.go` with stub implementation. Implement `SourceProvider`, register in `init()`. No router changes needed.

## OpenAPI Spec

The API contract for `service.api.client` and `service.api.public` is described in `docs/api/openapi.yaml`, auto-generated from `.proto` service definitions via `buf`. Frontend TypeScript types at `web/src/types/api.ts` are generated from this spec:

```bash
make generate   # Updates openapi.yaml + web/src/types/api.ts
```

## Related Documentation

- [Go Services](go-services.md) - All service structure details
- [Service Communication](service-communication.md) - gRPC inter-service RPC
- [Security](security.md) - Auth patterns and webhook verification
- [Plugin System](plugin-system.md) - SourceProvider interface
