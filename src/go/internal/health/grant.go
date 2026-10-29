package health

import (
	"context"
	"fmt"
	"net/http"
	"time"

	shared "github.com/fitglue/server/src/go/pkg"
	"github.com/fitglue/server/src/go/pkg/infrastructure/oauth"
)

// FirestoreGrant reads the owner's googlehealth integration from the user
// store and authenticates with its tokens, refreshing through
// pkg/infrastructure/oauth (Google refresh tokens are reusable, so this and
// the pipeline's heart-rate enricher refreshing the same grant is safe —
// unlike Fitbit's single-use ones).
func FirestoreGrant(db shared.Database, ownerUID string) GrantSource {
	ts := oauth.NewFirestoreTokenSourceFromDB(db, ownerUID, "googlehealth")
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: &oauth.Transport{Source: ts}}
	return func(ctx context.Context) (*Grant, error) {
		u, err := db.GetUser(ctx, ownerUID)
		if err != nil {
			return nil, fmt.Errorf("reading the owner: %w", err)
		}
		if u == nil || u.Integrations == nil || u.Integrations.Googlehealth == nil ||
			!u.Integrations.Googlehealth.Enabled || u.Integrations.Googlehealth.RefreshToken == "" {
			return nil, fmt.Errorf("%w: no googlehealth integration (fitgluectl connect googlehealth)", ErrNotConnected)
		}
		return &Grant{Scope: u.Integrations.Googlehealth.Scope, HTTP: httpClient}, nil
	}
}
