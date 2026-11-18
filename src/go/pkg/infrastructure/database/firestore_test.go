package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	fs "github.com/fitglue/server/src/go/pkg/docstore"
)

func offlineFS(t *testing.T) *fs.Client {
	t.Helper()
	c, err := offlineDocstore(t)
	if err != nil {
		t.Skipf("firestore client: %v", err)
	}
	return c
}

func TestNewFirestoreAdapter(t *testing.T) {
	c := offlineFS(t)
	defer c.Close()
	a := NewFirestoreAdapter(c)
	if a == nil || a.Client == nil {
		t.Fatal("expected adapter with client")
	}

	// Reads against an unreachable backend should error (not panic).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := a.GetUser(ctx, "u1"); err == nil {
		t.Error("expected error from unreachable Firestore")
	}
}

// offlineDocstore is a store whose database is already closed, so every read
// and write fails fast, as the unreachable Firestore emulator used to.
func offlineDocstore(t *testing.T) (*fs.Client, error) {
	t.Helper()
	c, err := fs.Open(filepath.Join(t.TempDir(), "fitglue.db"))
	if err != nil {
		return nil, err
	}
	_ = c.Close()
	return c, nil
}
