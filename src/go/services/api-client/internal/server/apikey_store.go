package server

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/fitglue/server/src/go/internal/user"
)

// FirestoreApiKeyStore implements ApiKeyStore using Cloud Firestore. The write
// itself lives in internal/user so `fitgluectl connect hevy` stores the same
// document this gateway does.
type FirestoreApiKeyStore struct {
	store *user.FirestoreStore
}

// NewFirestoreApiKeyStore creates a new Firestore-backed API key store
func NewFirestoreApiKeyStore(client *firestore.Client) *FirestoreApiKeyStore {
	return &FirestoreApiKeyStore{store: user.NewFirestoreStore(client)}
}

// CreateIngressKey stores a hashed API key record in the ingress_api_keys collection
func (s *FirestoreApiKeyStore) CreateIngressKey(ctx context.Context, keyHash, userID, label string, scopes []string, createdAt time.Time) error {
	return s.store.CreateIngressKey(ctx, keyHash, userID, label, scopes, createdAt)
}
