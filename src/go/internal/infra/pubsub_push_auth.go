package infra

import (
	"context"
	"net/http"
	"os"
	"strings"

	"google.golang.org/api/idtoken"
)

// Pub/Sub push subscriptions attach a Google-signed OIDC token to every push.
// On Cloud Run the platform verified that token (roles/run.invoker) before the
// request reached the service, so the handlers never checked it themselves.
// On the box the services sit behind Caddy + Tailscale Funnel with nothing in
// front of them, so the check has to happen in-process.
//
// PUBSUB_PUSH_AUDIENCE is the switch. Unset (Cloud Run, docker-compose) the
// middleware is a pass-through. Set, every push handler requires a bearer
// token that idtoken.Validate accepts for exactly that audience, and — if
// PUBSUB_PUSH_SERVICE_ACCOUNT is also set — whose verified email claim is
// that service account. The subscriptions must then mint tokens with the same
// explicit audience (`oidc_token { audience = ... }`), because the Pub/Sub
// default (the push endpoint URL) differs per subscription and would not
// survive Caddy rewriting the path.

const (
	pubsubPushAudienceEnv       = "PUBSUB_PUSH_AUDIENCE"
	pubsubPushServiceAccountEnv = "PUBSUB_PUSH_SERVICE_ACCOUNT"
)

// IDTokenVerifier validates an OIDC token for an audience and returns its
// payload. The production implementation is idtoken.Validate; tests inject
// their own.
type IDTokenVerifier func(ctx context.Context, token, audience string) (*idtoken.Payload, error)

// PubSubPushAuth is the middleware. Zero value (empty Audience) passes
// everything through.
type PubSubPushAuth struct {
	Audience       string
	ServiceAccount string
	Verify         IDTokenVerifier
	Logger         Logger
}

// PubSubPushAuthFromEnv builds the middleware from PUBSUB_PUSH_AUDIENCE and
// PUBSUB_PUSH_SERVICE_ACCOUNT, logging once which mode it is in so a
// misconfigured box is visible in the first lines of the journal.
func PubSubPushAuthFromEnv(logger Logger) *PubSubPushAuth {
	a := &PubSubPushAuth{
		Audience:       os.Getenv(pubsubPushAudienceEnv),
		ServiceAccount: os.Getenv(pubsubPushServiceAccountEnv),
		Verify:         idtoken.Validate,
		Logger:         logger,
	}
	ctx := context.Background()
	if a.Audience == "" {
		logger.Info(ctx, "pub/sub push auth: "+pubsubPushAudienceEnv+" unset, relying on the platform (Cloud Run IAM) to verify push tokens")
	} else {
		logger.Info(ctx, "pub/sub push auth: verifying OIDC tokens in-process", "audience", a.Audience, "serviceAccount", a.ServiceAccount)
	}
	return a
}

// Wrap returns next guarded by the token check.
func (a *PubSubPushAuth) Wrap(next http.Handler) http.Handler {
	if a == nil || a.Audience == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(authz, "Bearer ") || len(authz) == len("Bearer ") {
			http.Error(w, "missing or malformed Authorization header", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authz, "Bearer ")
		payload, err := a.Verify(ctx, token, a.Audience)
		if err != nil {
			a.logWarn(ctx, "pub/sub push auth: token rejected", "error", err, "path", r.URL.Path)
			http.Error(w, "invalid identity token", http.StatusForbidden)
			return
		}
		if a.ServiceAccount != "" {
			email, _ := payload.Claims["email"].(string)
			verified, _ := payload.Claims["email_verified"].(bool)
			if !verified || !strings.EqualFold(email, a.ServiceAccount) {
				a.logWarn(ctx, "pub/sub push auth: token from unexpected principal", "email", email, "path", r.URL.Path)
				http.Error(w, "identity token not issued to the push service account", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// WrapFunc is Wrap for a HandlerFunc.
func (a *PubSubPushAuth) WrapFunc(next http.HandlerFunc) http.HandlerFunc {
	return a.Wrap(next).ServeHTTP
}

func (a *PubSubPushAuth) logWarn(ctx context.Context, msg string, args ...any) {
	if a.Logger != nil {
		a.Logger.Warn(ctx, msg, args...)
	}
}
