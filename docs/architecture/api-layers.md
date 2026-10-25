# API Layers

FitGlue has three distinct HTTP gateway services, each with its own auth policy and permitted callers. None of them contain domain logic or touch Firestore directly — they are pure HTTP-to-gRPC translators.

## The Three Gateways

| Service | Callers | Auth | gRPC Clients |
|---------|---------|------|-------------|
| `service.api.client` | Web app, mobile app | Firebase Auth JWT | user, pipeline, activity |
| `service.api.public` | Unauthenticated browser | None (rate-limited) | activity |
| `service.api.webhook` | Third-party services (Strava, Fitbit, etc.) | Provider HMAC / signature | user (via RPC), Pub/Sub |

## service.api.client

The primary user-facing HTTP API.

**Base path:** `/api/`

**Auth:** Every request must carry a Firebase ID token in `Authorization: Bearer <token>`. The middleware (`internal/server/middleware.go`) validates the token and injects `userID` into context.

**Handler pattern:**
```go
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
    uid := middleware.UserID(r.Context())
    resp, err := h.users.GetProfile(r.Context(), &pb.GetProfileRequest{UserId: uid})
    if err != nil {
        writeGRPCError(w, err)
        return
    }
    writeJSON(w, http.StatusOK, resp)
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

**Auth:** Per-provider HMAC or signature verification (no Firebase JWT).

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
