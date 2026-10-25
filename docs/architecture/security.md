# Security Architecture

This document describes FitGlue's security model, authorization patterns, and access control mechanisms.

## Overview

FitGlue follows a **"Deny All" baseline** security philosophy. All access is denied by default and must be explicitly granted.

## Auth Boundary

FitGlue operates with two distinct trust zones:

```
┌───────────────────────────────────────────────────────────────────┐
│                     UNTRUSTED (Client)                            │
│                                                                   │
│  • The owner's tools (Jarvis, fitgluectl, fitglue-mcp, curl)      │
│  • External Webhooks                                              │
│  • OAuth provider redirects                                       │
│                                                                   │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │          AUTH BOUNDARY (static bearer token / HMAC)         │  │
│  └─────────────────────────────────────────────────────────────┘  │
│                                                                   │
├───────────────────────────────────────────────────────────────────┤
│                     TRUSTED (Internal)                            │
│                                                                   │
│  • Go Cloud Run services (after auth validation)                  │
│  • Pub/Sub Messages (system-generated)                            │
│  • gRPC inter-service calls (OIDC verified)                       │
│                                                                   │
└───────────────────────────────────────────────────────────────────┘
```

### Key Principles

1. **Clients are never trusted** — All client requests must be authenticated
2. **Internal messages are trusted** — Pub/Sub events and gRPC calls from our own services are pre-authenticated
3. **Auth happens at the boundary** — API gateways validate auth; domain services trust the gRPC caller

## API Gateway Authorization

Authorization middleware lives in each API service's `internal/server/middleware.go`.

### service.api.client — static bearer token, one user

FitGlue is single-user. There are no accounts, no sign-up and no identity
provider: `api-client` is started with two environment variables and refuses
to start without either.

| Variable | Meaning |
|----------|---------|
| `FITGLUE_API_TOKEN` | The one bearer token. Every request under `/api/v2` (except the OAuth callbacks and `/health`) must send `Authorization: Bearer <token>`. |
| `FITGLUE_OWNER_UID` | The user every authenticated request acts as: the `users/{uid}` Firestore document all data hangs off. |

```go
// middleware.go
func AuthMiddleware(cfg AuthConfig) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
            if cfg.Token == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(cfg.Token)) != 1 {
                http.Error(w, "invalid token", http.StatusUnauthorized)
                return
            }
            ctx := context.WithValue(r.Context(), userContextKey, &Principal{UID: cfg.OwnerUID})
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

- Comparison is constant-time; a missing, malformed or wrong header is `401`.
- An empty configured token never matches, so a misconfigured server rejects
  everything rather than accepting anything.
- Handlers never see a token: they read the `Principal` the middleware set
  (`getUserToken(r).UID`), which is always the owner.
- `GET /oauth/{provider}/callback` and the legacy `/auth/{provider}/callback`
  are outside the authenticated group: Strava, Fitbit and Hevy redirect the
  browser there, with no bearer. They are protected by the signed OAuth
  `state` parameter instead (see `oauth.go`).
- Rotating the token is `gcloud secrets versions add fitglue-api-token`
  (cloud) or editing `~/.jarvis/fitglue.env` (box) plus a restart.

The webhook gateway and the public gateway are unchanged: per-provider
signatures, and no auth, respectively.

### service.api.webhook — HMAC Signature Verification

Each source provider implements `VerifyWebhook(r *http.Request) error` in its `SourceProvider` interface:

```go
// services/api-webhook/internal/webhook/sources/hevy/provider.go
func (p *HevyProvider) VerifyWebhook(r *http.Request) error {
    signature := r.Header.Get("X-Hevy-Webhook-Secret")
    expected := r.Header.Get("X-Fitglue-Ingress-Key")
    if !hmac.Equal([]byte(signature), []byte(expected)) {
        return errors.New("invalid webhook signature")
    }
    return nil
}
```

Each user has a unique ingress API key (`fg_sk_...`) stored in their Firestore record and used for webhook authentication.

## Service-to-Service Security

gRPC calls between services use Google-managed OIDC tokens on Cloud Run. The calling service's Cloud Run identity is verified by the receiving service. See [Service Communication](service-communication.md) for details.

## Firestore Security Rules

Firestore uses a **"Deny All" baseline**. With no Firebase Auth clients left,
nothing authenticates to Firestore as a user any more: the only callers are the
Go services (and `bootstrap-owner`) with service-account or ADC credentials,
which bypass rules. The user-scoped grants below are the pre-single-user
rules and are kept only until the Firestore rules file is retired with the
Firebase web app:

```javascript
rules_version = '2';
service cloud.firestore {
  match /databases/{database}/documents {
    // Default: deny all
    match /{document=**} {
      allow read, write: if false;
    }

    // Users can read/write their own data
    match /users/{userId} {
      allow read, write: if request.auth != null
                         && request.auth.uid == userId;
    }

    // Pipelines: user owns their pipelines
    match /users/{userId}/pipelines/{pipelineId} {
      allow read, write: if request.auth != null
                         && request.auth.uid == userId;
    }
  }
}
```

> [!NOTE]
> Firestore reads and writes happen exclusively through the domain services. The rules are a defence-in-depth backstop, not the enforcement layer.

## OAuth Token Management

OAuth tokens for external services (Strava, Fitbit, etc.) are:

1. **Stored** in Firestore under `users/{userId}` (via `service.user`)
2. **Refreshed automatically** during webhook processing when a 401 is detected
3. **Scoped minimally** — Only request needed permissions per provider

Tokens are never logged. See [OAuth Integration Guide](../guides/oauth-integration.md) for setup instructions.

## Related Documentation

- [Services & Stores](services-and-stores.md) - Domain service architecture
- [Architecture Overview](overview.md) - System components
- [API Layers](api-layers.md) - Auth per gateway
- [Error Codes](../reference/errors.md) - Error handling
