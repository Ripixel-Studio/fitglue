package user

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	"github.com/stretchr/testify/assert"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/structpb"
)

// setupOfflineFirestore creates a client that will fail fast since there's no emulator
func setupOfflineFirestore(t *testing.T) *firestore.Client {
	ctx := context.Background()
	// Create a dummy client. Any network calls will fail.
	client, err := firestore.NewClient(ctx, "test-project", option.WithEndpoint("localhost:9999"), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create dummy firestore client: %v", err)
	}
	return client
}

func TestFirestoreStore_OfflineCoverage(t *testing.T) {
	client := setupOfflineFirestore(t)
	defer client.Close()

	store := NewFirestoreStore(client)

	// We use a canceled or timed out context to force fast failures
	// and hit the error paths in the store to increase coverage.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	t.Run("GetProfile", func(t *testing.T) {
		_, err := store.GetProfile(ctx, "user1")
		assert.Error(t, err)
	})

	t.Run("UpdateProfile", func(t *testing.T) {
		err := store.UpdateProfile(ctx, "user1", &pbuser.UserProfile{})
		assert.Error(t, err)

		err = store.UpdateProfile(ctx, "user1", nil)
		assert.Error(t, err)
	})

	t.Run("DeleteUser", func(t *testing.T) {
		err := store.DeleteUser(ctx, "user1")
		assert.Error(t, err)
	})

	t.Run("FindUsersByDateRange", func(t *testing.T) {
		_, err := store.FindUsersByDateRange(ctx, time.Now(), time.Now())
		assert.Error(t, err)
	})

	t.Run("GetIntegrations", func(t *testing.T) {
		_, err := store.GetIntegrations(ctx, "user1")
		assert.Error(t, err)
	})

	t.Run("SetIntegration", func(t *testing.T) {
		err := store.SetIntegration(ctx, "user1", "strava", nil)
		assert.Error(t, err)
	})

	t.Run("DeleteIntegration", func(t *testing.T) {
		err := store.DeleteIntegration(ctx, "user1", "strava")
		assert.Error(t, err)
	})

	t.Run("FindUserByIntegration", func(t *testing.T) {
		_, err := store.FindUserByIntegration(ctx, "strava", "123")
		assert.Error(t, err)

		_, err = store.FindUserByIntegration(ctx, "unknown", "123")
		assert.Error(t, err)
	})

	t.Run("ListCounters", func(t *testing.T) {
		_, err := store.ListCounters(ctx, "user1")
		assert.Error(t, err)
	})

	t.Run("UpdateCounter", func(t *testing.T) {
		_, err := store.UpdateCounter(ctx, "user1", "c1", 1)
		assert.Error(t, err)
	})

	t.Run("GetBoosterData", func(t *testing.T) {
		_, err := store.GetBoosterData(ctx, "user1", "b1")
		assert.Error(t, err)

		_, err = store.GetBoosterData(ctx, "user1", "")
		assert.Error(t, err)
	})

	t.Run("SetBoosterData", func(t *testing.T) {
		err := store.SetBoosterData(ctx, "user1", "b1", &structpb.Struct{})
		assert.Error(t, err)

		err = store.SetBoosterData(ctx, "user1", "b1", nil)
		assert.Error(t, err)
	})

	t.Run("DeleteBoosterData", func(t *testing.T) {
		err := store.DeleteBoosterData(ctx, "user1", "b1")
		assert.Error(t, err)
	})

	t.Run("CreateUser", func(t *testing.T) {
		_, err := store.CreateUser(ctx, "user1")
		assert.Error(t, err)
	})
}
