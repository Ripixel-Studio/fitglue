package user

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRevokeStravaToken_NetworkPaths exercises revokeStravaToken without a real
// network call. A canceled context forces http.DefaultClient.Do to fail fast,
// covering the request-construction success branch and the Do-error branch.
func TestRevokeStravaToken_NetworkPaths(t *testing.T) {
	t.Run("CanceledContextDoError", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately so the HTTP round trip never reaches the network
		err := revokeStravaToken(ctx, "some-access-token")
		assert.Error(t, err)
	})

	t.Run("InvalidRequestConstruction", func(t *testing.T) {
		// A nil context makes http.NewRequestWithContext return an error,
		// covering the early return branch.
		//nolint:staticcheck // intentionally passing nil context to hit error branch
		err := revokeStravaToken(nil, "token")
		assert.Error(t, err)
	})
}
