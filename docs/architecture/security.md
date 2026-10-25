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
│  • Web App (Browser)                                              │
│  • Mobile App                                                     │
│  • External Webhooks                                              │
│                                                                   │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │            AUTH BOUNDARY (Firebase Auth / HMAC)             │  │
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

### service.api.client — Firebase JWT

```go
// middleware.go
func AuthMiddleware(authClient *firebase.AuthClient) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            token := r.Header.Get("Authorization")
            decoded, err := authClient.VerifyIDToken(r.Context(), strings.TrimPrefix(token, "Bearer "))
            if err != nil {
                http.Error(w, "Unauthorized", http.StatusUnauthorized)
                return
            }
            ctx := context.WithValue(r.Context(), userIDKey, decoded.UID)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

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

Firestore uses a **"Deny All" baseline** with explicit grants:

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
> In practice, Firestore writes happen exclusively through domain services (not from the client SDK directly). The Firestore rules serve as a defence-in-depth backstop rather than the primary enforcement layer.

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
